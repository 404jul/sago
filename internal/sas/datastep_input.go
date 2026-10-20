package sas

import (
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
)

// DATA step input sources: INFILE/INPUT and SET/MERGE (ds prefix).

// ---------------------------------------------------------------- INFILE

type dsInfile struct {
	st       *dsStep
	path     string
	display  string
	lines    []string
	dlm      string
	dsd      bool
	missover bool
	truncov  bool
	stopover bool
	firstobs int
	obs      int
	endSlot  int // END= variable slot or -1
	// runtime
	cur     int // index of the line being read (-1 = none yet)
	col     int // column within the current line
	atEOF   bool
	spent   bool // current record fully consumed: next INPUT starts a new one
	holdAll bool // trailing @@: record persists across iterations
}

func dsNewInfile(st *dsStep, s *Stmt) (*dsInfile, error) {
	f := &dsInfile{st: st, dlm: " ", firstobs: 1, obs: -1, cur: -1, endSlot: -1}
	c := NewCursor(s.Args())
	if !c.AtEnd() {
		t := c.Next()
		switch {
		case t.Kind == TString:
			p, err := st.s.ResolvePath(t.Text)
			if err != nil {
				return nil, dsErrf(s.Line, "%v", err)
			}
			f.path = p
			f.display = t.Text
		case t.Is("DATALINES", "CARDS", "LINES"):
			f.display = "DATALINES"
		case t.Kind == TIdent:
			// fileref
			if p, ok := st.s.Filerefs[t.Upper()]; ok {
				pp, err := st.s.ResolvePath(p)
				if err != nil {
					return nil, dsErrf(s.Line, "%v", err)
				}
				f.path = pp
				f.display = t.Text
			} else {
				f.display = t.Text
			}
		}
	}
	o := ParseOpts(c.Rest())
	if op, ok := o.Get("DLM", "DELIMITER"); ok {
		v := op.Str()
		if len(v) == 1 {
			f.dlm = v
		} else if u := strings.ToUpper(v); u == "TAB" {
			f.dlm = "\t"
		} else if u == "COMMA" {
			f.dlm = ","
		} else if u == "SPACE" {
			f.dlm = " "
		} else if u == "SEMICOLON" {
			f.dlm = ";"
		} else if len(v) >= 3 && strings.HasSuffix(u, "'X") && strings.HasPrefix(v, "'") {
			// hex literal '09'x already tokenized as string '09' + ident x? handled below
			f.dlm = hexStr(v)
		}
	}
	// hex delimiter written as '09'x lexes to a string token; TokText joins pieces
	if op, ok := o.Get("DLM", "DELIMITER"); ok {
		s := TokText(op.Val)
		if h := hexStr(s); h != "" {
			f.dlm = h
		}
	}
	f.dsd = o.Has("DSD")
	f.missover = o.Has("MISSOVER")
	f.truncov = o.Has("TRUNCOVER")
	f.stopover = o.Has("STOPOVER")
	f.firstobs = int(o.Num("FIRSTOBS", 1))
	f.obs = int(o.Num("OBS", -1))
	if op, ok := o.Get("END"); ok && op.Eq && len(op.Val) == 1 && op.Val[0].Kind == TIdent {
		v := st.variable(op.Val[0].Text)
		st.numeric(v, s.Line)
		v.retain = true
		f.endSlot = v.slot
	}
	return f, nil
}

func hexStr(s string) string {
	s = strings.ToUpper(strings.TrimSpace(s))
	if strings.HasSuffix(s, "'X") && strings.HasPrefix(s, "'") {
		h := strings.TrimSuffix(strings.TrimPrefix(s, "'"), "'X")
		var out []byte
		for i := 0; i+1 < len(h); i += 2 {
			b, err := strconv.ParseUint(h[i:i+2], 16, 8)
			if err != nil {
				return ""
			}
			out = append(out, byte(b))
		}
		return string(out)
	}
	return ""
}

// load reads the physical lines (deferred to openSources).
func (f *dsInfile) load(st *dsStep) error {
	if f.display == "DATALINES" && f.path == "" {
		f.lines = st.datalines
	} else if f.path != "" {
		raw, err := os.ReadFile(f.path)
		if err != nil {
			return dsErrf(st.line, "%v", err)
		}
		s := strings.ReplaceAll(string(raw), "\r\n", "\n")
		s = strings.TrimSuffix(s, "\n")
		if s == "" {
			f.lines = nil
		} else {
			f.lines = strings.Split(s, "\n")
		}
	}
	if f.firstobs > 1 || f.obs >= 0 {
		lo := f.firstobs - 1
		hi := len(f.lines)
		if f.obs >= 0 {
			hi = f.obs
		}
		if lo > len(f.lines) {
			lo = len(f.lines)
		}
		if lo < 0 {
			lo = 0
		}
		if hi > len(f.lines) {
			hi = len(f.lines)
		}
		f.lines = f.lines[lo:hi]
	}
	f.cur = -1
	f.col = 0
	return nil
}

// iterStart is called at the top of each DATA step iteration: the current
// record is discarded unless the previous INPUT held it with @@.
func (f *dsInfile) iterStart() {
	if f.holdAll {
		return
	}
	f.spent = true
	f.holdAll = false
}

// nextLine advances to the next physical line; false at EOF.
func (f *dsInfile) nextLine() bool {
	if f.cur+1 >= len(f.lines) {
		f.atEOF = true
		return false
	}
	f.cur++
	f.col = 0
	return true
}

// readToken returns the next delimited token (""+"false" at end of record).
func (f *dsInfile) readToken() (string, bool) {
	if f.spent {
		f.spent = false
		if !f.nextLine() {
			return "", false
		}
	}
	for {
		if f.cur < 0 || f.cur >= len(f.lines) {
			if !f.nextLine() {
				return "", false
			}
		}
		line := f.lines[f.cur]
		if f.col >= len(line) {
			// record exhausted
			if f.dsd {
				// consecutive delimiters: missing value signalled by caller
				return "\x00missing", true
			}
			if !f.nextLine() {
				return "", false
			}
			continue
		}
		rest := line[f.col:]
		if f.dsd {
			// quoted fields may contain delimiters
			if rest[0] == '"' || rest[0] == '\'' {
				q := rest[0]
				i := 1
				var b strings.Builder
				for i < len(rest) {
					if rest[i] == q {
						if i+1 < len(rest) && rest[i+1] == q {
							b.WriteByte(q)
							i += 2
							continue
						}
						i++
						break
					}
					b.WriteByte(rest[i])
					i++
				}
				// consume following delimiter
				f.col += i
				if f.col < len(line) && strings.HasPrefix(line[f.col:], f.dlm) {
					f.col += len(f.dlm)
				} else {
					f.col = len(line)
				}
				return b.String(), true
			}
		}
		idx := strings.Index(rest, f.dlm)
		var tok string
		if idx < 0 {
			tok = rest
			f.col = len(line)
		} else {
			tok = rest[:idx]
			f.col += idx + len(f.dlm)
		}
		if !f.dsd {
			// collapse repeated delimiters and skip leading ones
			if strings.TrimSpace(tok) == "" {
				continue
			}
			return strings.TrimLeft(tok, " "), true
		}
		if tok == "" {
			return "\x00missing", true
		}
		return tok, true
	}
}

// logRule prints the invalid-data ruler and the offending record, the way
// SAS formats them (both are plain log lines, not notes).
func (f *dsInfile) logRule(st *dsStep) {
	lineNo := st.dataLine
	if f.cur >= 0 && f.cur < len(f.lines) {
		lineNo = st.dataLine + f.cur
		var b strings.Builder
		b.WriteString("RULE:      ")
		b.WriteString(sasRuler())
		st.s.Log.Put(strings.TrimRight(b.String(), ""))
		st.s.Log.Put(fmt.Sprintf("%-11d%s", lineNo, f.lines[f.cur]))
	}
}

// sasRuler is SAS's column ruler: '+' every 5 columns, digit every 10.
func sasRuler() string {
	r := make([]rune, 100)
	for i := range r {
		pos := i + 1
		switch {
		case pos%10 == 0:
			r[i] = rune('0' + (pos/10)%10)
		case pos%5 == 0:
			r[i] = '+'
		default:
			r[i] = '-'
		}
	}
	return string(r)
}

func (f *dsInfile) finishNotes(st *dsStep) {
	if f.path == "" {
		return
	}
	n := len(f.lines)
	inf := "The infile " + f.display + " is:\n      Filename=" + f.path
	st.s.Log.Note(st.line, fmt.Sprintf("%s\n      The number of records read is unknown; at least %d records were read.", inf, n))
	_ = n
}

// ---------------------------------------------------------------- INPUT

type dsInItem struct {
	kind    byte // 'v' variable, '@' column pointer, '+' relative, '#' line, '/' newline, 'h' hold, 'H' hold-all
	name    string
	char    bool
	length  int
	inform  string
	colSpec int // absolute column for column input
	v       *dsVar
	expr    *dsExpr // for @expr
}

type dsInputNode struct {
	items []dsInItem
	line  int
}

func (p *dsParser) parseInput(s *Stmt) (dsNode, error) {
	st := p.st
	n := &dsInputNode{line: s.Line}
	c := NewCursor(s.Args())
	if c.Peek().IsOp("@") && c.PeekN(1).Kind == TIdent {
		// @ptr: pointer variable INPUT — unsupported
		return nil, dsErrf(s.Line, "Pointer-variable INPUT is not supported by sago.")
	}
	for !c.AtEnd() {
		t := c.Next()
		switch {
		case t.IsOp("/"):
			n.items = append(n.items, dsInItem{kind: '/'})
		case t.IsOp("#"):
			if c.Peek().Kind == TNumber {
				c.Next()
				n.items = append(n.items, dsInItem{kind: '#'})
			}
		case t.IsOp("@"):
			nt := c.Peek()
			if nt.Kind == TNumber {
				c.Next()
				v, _ := strconv.Atoi(nt.Text)
				n.items = append(n.items, dsInItem{kind: '@', colSpec: v})
			} else if nt.Kind == TIdent {
				e, err := p.expr([]Token{c.Next()}, s.Line)
				if err != nil {
					return nil, err
				}
				n.items = append(n.items, dsInItem{kind: '@', expr: e})
			}
		case t.IsOp("+"):
			if c.Peek().Kind == TNumber {
				v, _ := strconv.Atoi(c.Next().Text)
				n.items = append(n.items, dsInItem{kind: '+', colSpec: v})
			}
		case t.Kind == TIdent:
			item := dsInItem{kind: 'v', name: t.Text}
			// modifiers
			for !c.AtEnd() {
				t2 := c.Peek()
				if t2.IsOp("$") {
					item.char = true
					c.Next()
					continue
				}
				if t2.Kind == TNumber {
					// column or column range or informat width: n, n-m, n.
					col := int(t2.Num)
					c.Next()
					if c.Peek().IsOp("-") && c.PeekN(1).Kind == TNumber {
						c.P += 2 // column range start-end: store start
					}
					item.colSpec = col
					continue
				}
				if t2.Kind == TIdent && strings.Contains(t2.Text, ".") {
					f := ParseFormat(t2.Text)
					if f != nil {
						item.inform = t2.Text
						if f.Char {
							item.char = true
							if f.HasW && item.length == 0 {
								item.length = f.W
							}
						}
						c.Next()
						continue
					}
					break
				}
				break
			}
			v := st.variable(item.name)
			if err := st.define(v, item.char, item.length, s.Line); err != nil {
				return nil, err
			}
			if !v.Char && !item.char && item.inform == "" {
				// numeric list input
			}
			v.assigned = true
			item.v = v
			n.items = append(n.items, item)
		case t.Kind == TString:
			// (list) (informat) grouping — not supported in detail
		}
	}
	if len(n.items) > 0 {
		last := n.items[len(n.items)-1]
		if last.kind == 'v' {
			// check trailing @ / @@ in raw text
			text := strings.ToUpper(s.Text)
			if strings.HasSuffix(text, " @@") || strings.HasSuffix(text, "@@") {
				n.items = append(n.items, dsInItem{kind: 'H'})
			} else if strings.HasSuffix(text, " @") || strings.HasSuffix(text, "@") {
				n.items = append(n.items, dsInItem{kind: 'h'})
			}
		}
	}
	if st.curInfile == nil {
		// implicit DATALINES infile
		f := &dsInfile{st: st, display: "DATALINES", dlm: " ", firstobs: 1, obs: -1, cur: -1, endSlot: -1}
		st.infiles = append(st.infiles, f)
		st.curInfile = f
	}
	st.nReaders++
	return n, nil
}

func (n *dsInputNode) exec(st *dsStep) dsCtl {
	f := st.curInfile
	if f == nil {
		return dsEOF
	}
	readColumn := func(col int) (string, bool) {
		if f.cur < 0 || f.cur >= len(f.lines) {
			if !f.nextLine() {
				return "", false
			}
		}
		line := f.lines[f.cur]
		if col-1 >= len(line) {
			return "", true // past end: missing (TRUNCOVER semantics)
		}
		return line[col-1:], true
	}
	fail := func() dsCtl {
		if f.stopover {
			st.runtimeError("Stopped due to the STOPOVER option.")
			return dsAbort
		}
		return dsNext
	}
	firstItem := true
	for _, it := range n.items {
		switch it.kind {
		case '/':
			if !f.nextLine() {
				return fail()
			}
		case '#':
			// treated as newline (multi-line detail not needed)
			if !f.nextLine() {
				return fail()
			}
		case '@':
			if it.expr != nil {
				f.col = int(it.expr.eval(st).AsNum()) - 1
			} else {
				f.col = it.colSpec - 1
			}
			if f.cur < 0 && len(f.lines) > 0 {
				f.cur = 0
			}
		case '+':
			f.col += it.colSpec
		case 'h':
			f.holdAll = false
		case 'H':
			f.holdAll = true
		case 'v':
			var raw string
			var ok bool
			sc := f.col + 1
			invalid := func() {
				ec := sc + len(strings.TrimRight(raw, " ")) - 1
				lineNo := st.dataLine
				if f.cur >= 0 {
					lineNo = st.dataLine + f.cur
				}
				st.invalidInputData(it.v, lineNo, sc, ec)
			}
			if it.colSpec > 0 {
				raw, ok = readColumn(it.colSpec)
			} else {
				raw, ok = f.readToken()
			}
			if !ok {
				if firstItem {
					// clean end of file: the step stops without output
					st.readIter = true
					return dsEOF
				}
				st.lostCard = true
				st.s.Log.Note(st.ctx.Line, "LOST CARD.")
				return dsEOF
			}
			firstItem = false
			if raw == "\x00missing" {
				st.pdv[it.v.slot] = it.v.missing()
				continue
			}
			if it.colSpec > 0 && it.inform == "" {
				if it.char {
					st.store(it.v, StrV(raw))
				} else {
					fld := raw
					if x, ok2 := dsCharToNum(fld); ok2 {
						st.pdv[it.v.slot] = NumV(x)
					} else {
						invalid()
						st.pdv[it.v.slot] = Missing
					}
				}
				continue
			}
			var fm *FormatSpec
			if it.inform != "" {
				fm = ParseFormat(it.inform)
			}
			v, ok2 := ApplyInformat(raw, fm, it.v.Char)
			if !ok2 {
				invalid()
			}
			if !it.v.Char && v.Char {
				x, ok3 := dsCharToNum(v.Str)
				if !ok3 {
					invalid()
				}
				v = NumV(x)
			}
			if it.v.Char && !v.Char {
				v = StrV(numToCharAuto(v.Num))
			}
			st.store(it.v, v)
		}
	}
	if f.atEOF && f.endSlot >= 0 {
		st.pdv[f.endSlot] = NumV(1)
	}
	st.readIter = true
	// record consumption bookkeeping
	held := false
	for i := len(n.items) - 1; i >= 0; i-- {
		if n.items[i].kind == 'H' || n.items[i].kind == 'h' {
			held = true
			break
		}
	}
	if !held {
		f.spent = true
	}
	return dsNext
}

// ---------------------------------------------------------------- SET / MERGE

type dsSrc struct {
	ref       *DSRef
	ds        *Dataset
	rows      [][]Value
	pos       int
	read      int
	display   string
	whereText string
	inSlot    int // IN= variable slot or -1
	cols      []int
	keys      []int // by column indices in source
}

type dsSetStmt struct {
	st       *dsStep
	kind     string
	refs     []*DSRef
	opts     [][2]string
	by       []Token
	line     int
	srcs     []*dsSrc
	endSlot  int
	nobsSlot int
	byInfo   *ByInfo
	pd       []int // by column slots in PDV
	// merge queue state: current group rows per src
	nobsVal   float64
	nobsSet   bool
	groupRows [][]Value
	groupPos  []int
	prevKey   []Value
	havePrev  bool
	nWritten  int

	repeatsNoted bool // "MERGE ... repeats of BY values" note emitted
}

func (s *dsSetStmt) compile() error {
	st := s.st
	var wheres = st.wheres
	for _, ref := range s.refs {
		d, err := st.s.ReadDS(ref)
		if err != nil {
			return dsErrf(s.line, "%v", err)
		}
		for _, w := range wheres {
			d = st.s.FilterWhere(d, w)
		}
		src := &dsSrc{ref: ref, ds: d, rows: d.Rows, display: DisplayName(ref.Name), inSlot: -1}
		if ref.Opts != nil {
			if op, ok := ref.Opts.Get("IN"); ok && op.Eq && len(op.Val) == 1 {
				v := st.variable(op.Val[0].Text)
				st.numeric(v, s.line)
				v.retain = true
				v.assigned = true
				v.noOut = true // IN= variables are hidden unless explicitly kept
				src.inSlot = v.slot
			}
			if op, ok := ref.Opts.Get("WHERE"); ok {
				src.whereText = TokText(op.Inner())
			}
		}
		// define PDV variables from the source (in source order)
		src.cols = make([]int, len(d.Vars))
		for i, v := range d.Vars {
			pv := st.variable(v.Name)
			if err := st.define(pv, v.Char, v.Length, s.line); err != nil {
				return err
			}
			pv.fromData = true
			pv.assigned = true
			if v.Format != "" {
				pv.Format = v.Format
			}
			if v.Label != "" && pv.Label == "" {
				pv.Label = v.Label
			}
			src.cols[i] = pv.slot
		}
		s.srcs = append(s.srcs, src)
	}
	for _, o := range s.opts {
		switch o[0] {
		case "END":
			v := st.variable(o[1])
			st.numeric(v, s.line)
			v.retain = true
			v.assigned = true
			v.noOut = true
			s.endSlot = v.slot
		case "NOBS":
			v := st.variable(o[1])
			st.numeric(v, s.line)
			v.retain = true
			v.assigned = true
			v.noOut = true
			s.nobsSlot = v.slot
		}
	}
	if s.by != nil {
		// resolve BY against the first source
		first := s.srcs[0].ds
		bi, err := ParseBy(first, s.by)
		if err != nil {
			return dsErrf(s.line, "%v", err)
		}
		s.byInfo = bi
		s.pd = make([]int, len(bi.Names))
		for i, n := range bi.Names {
			s.pd[i] = st.lookup(n).slot
		}
		for _, src := range s.srcs {
			idx, err := src.ds.Indices(bi.Names)
			if err != nil {
				return dsErrf(s.line, "BY variable %v", err)
			}
			src.keys = idx
			if !src.ds.IsSortedBy(idx, bi.Desc) && !s.hasNotSorted() {
				return dsErrf(s.line, "Data set %s is not sorted in ascending sequence.", src.display)
			}
			_ = idx
		}
	}
	if s.nobsSlot >= 0 {
		total := 0
		for _, src := range s.srcs {
			total += len(src.rows)
		}
		s.nobsVal = float64(total)
	}
	st.nReaders++
	return nil
}

func (s *dsSetStmt) hasNotSorted() bool {
	for _, t := range s.by {
		if t.Is("NOTSORTED") {
			return true
		}
	}
	return false
}

func parseOptEq(toks []Token, key string) (string, bool) {
	for i, t := range toks {
		if t.Is(key) && i+2 < len(toks) && toks[i+1].IsOp("=") && toks[i+2].Kind == TIdent {
			return toks[i+2].Text, true
		}
	}
	return "", false
}

// srcKey returns the BY key of the row the src will read next.
func (s *dsSetStmt) srcKey(src *dsSrc) []Value {
	if src.pos >= len(src.rows) {
		return nil
	}
	row := src.rows[src.pos]
	key := make([]Value, len(src.keys))
	for j, ci := range src.keys {
		key[j] = row[ci]
	}
	return key
}

func cmpKeys(a, b []Value, desc []bool) int {
	for i := range a {
		c := Compare(a[i], b[i])
		if desc != nil && desc[i] {
			c = -c
		}
		if c != 0 {
			return c
		}
	}
	return 0
}

func (s *dsSetStmt) exec(st *dsStep) dsCtl {
	if s.nobsSlot >= 0 && !s.nobsSet {
		st.pdv[s.nobsSlot] = NumV(s.nobsVal)
		s.nobsSet = true
	}
	switch s.kind {
	case "SET":
		return s.execSet(st)
	default:
		return s.execMerge(st)
	}
}

// execSet: concatenation, interleaving with BY, or independent stream.
func (s *dsSetStmt) execSet(st *dsStep) dsCtl {
	if s.byInfo != nil {
		// interleaved: pick the source with the smallest next key
		best := -1
		for i, src := range s.srcs {
			if src.pos >= len(src.rows) {
				if src.inSlot >= 0 {
					st.pdv[src.inSlot] = NumV(0)
				}
				continue
			}
			if best < 0 {
				best = i
				continue
			}
			if cmpKeys(s.srcKey(src), s.srcKey(s.srcs[best]), s.byInfo.Desc) < 0 {
				best = i
			}
		}
		if best < 0 {
			return s.atEnd(st)
		}
		return s.readFrom(st, best, true)
	}
	for i, src := range s.srcs {
		if src.pos < len(src.rows) {
			return s.readFrom(st, i, false)
		}
		if src.inSlot >= 0 {
			st.pdv[src.inSlot] = NumV(0)
		}
	}
	return s.atEnd(st)
}

// resetSources clears every SET source's variables in the PDV: when a SET
// statement reads from one of several data sets, the variables of the others
// are missing for this iteration.
func (s *dsSetStmt) resetSources(st *dsStep) {
	for _, src := range s.srcs {
		for _, sl := range src.cols {
			st.pdv[sl] = st.vars[sl].missing()
		}
	}
}

func (s *dsSetStmt) readFrom(st *dsStep, i int, reset bool) dsCtl {
	src := s.srcs[i]
	if reset {
		s.resetSources(st)
	}
	row := src.rows[src.pos]
	src.pos++
	src.read++
	st.readIter = true
	for j, slot := range src.cols {
		x := row[j]
		if x.Char {
			x.Str = dsFit(x.Str, st.vars[slot].Len)
		}
		st.pdv[slot] = x
	}
	if src.inSlot >= 0 {
		st.pdv[src.inSlot] = NumV(1)
	}
	if s.endSlot >= 0 {
		last := src.pos >= len(src.rows)
		if s.byInfo == nil {
			for _, s2 := range s.srcs {
				if s2.pos < len(s2.rows) {
					last = false
					break
				}
			}
		}
		if last {
			st.pdv[s.endSlot] = NumV(1)
		} else {
			st.pdv[s.endSlot] = NumV(0)
		}
	}
	if s.byInfo != nil {
		key := make([]Value, len(s.pd))
		for j, sl := range s.pd {
			key[j] = st.pdv[sl]
		}
		if !s.havePrev || cmpKeys(key, s.prevKey, s.byInfo.Desc) != 0 {
			for _, n := range s.byInfo.Names {
				if v := st.lookup("FIRST." + n); v != nil {
					st.pdv[v.slot] = NumV(1)
				}
				if v := st.lookup("LAST." + n); v != nil {
					st.pdv[v.slot] = NumV(0)
				}
			}
		} else {
			for _, n := range s.byInfo.Names {
				if v := st.lookup("FIRST." + n); v != nil {
					st.pdv[v.slot] = NumV(0)
				}
			}
		}
		// LAST.: peek next emitted key
		nxt := s.peekNextKey()
		for j, n := range s.byInfo.Names {
			if v := st.lookup("LAST." + n); v != nil {
				if nxt == nil || Compare(key[j], nxt[j]) != 0 {
					st.pdv[v.slot] = NumV(1)
				} else {
					st.pdv[v.slot] = NumV(0)
				}
			}
		}
		s.prevKey = key
		s.havePrev = true
	}
	return dsNext
}

// peekNextKey returns the key of the next row in merged order (nil at end).
func (s *dsSetStmt) peekNextKey() []Value {
	var best []Value
	for i, src := range s.srcs {
		k := s.srcKey(src)
		if k == nil {
			continue
		}
		if best == nil || cmpKeys(k, best, s.byInfo.Desc) < 0 {
			best = k
		}
		_ = i
	}
	return best
}

func (s *dsSetStmt) atEnd(st *dsStep) dsCtl {
	if s.endSlot >= 0 {
		st.pdv[s.endSlot] = NumV(1)
	}
	st.readIter = true
	return dsEOF
}

// execMerge: BY match-merge or positional merge.
func (s *dsSetStmt) execMerge(st *dsStep) dsCtl {
	if s.byInfo == nil {
		if s.nWritten >= s.maxLen() {
			return s.atEnd(st)
		}
		s.nWritten++
		st.readIter = true
		for _, src := range s.srcs {
			if src.pos < len(src.rows) {
				row := src.rows[src.pos]
				src.pos++
				src.read++
				s.copyRow(st, src, row)
				if src.inSlot >= 0 {
					st.pdv[src.inSlot] = NumV(1)
				}
			}
		}
		return dsNext
	}
	// BY merge: determine the minimum current key among sources
	type cand struct {
		i    int
		keys []Value
	}
	var cands []cand
	for i, src := range s.srcs {
		k := s.srcKey(src)
		if k != nil {
			cands = append(cands, cand{i, k})
		} else if src.inSlot >= 0 {
			st.pdv[src.inSlot] = NumV(0)
		}
	}
	if len(cands) == 0 {
		return s.atEnd(st)
	}
	minK := cands[0].keys
	for _, c := range cands[1:] {
		if cmpKeys(c.keys, minK, s.byInfo.Desc) < 0 {
			minK = c.keys
		}
	}
	newGroup := !s.havePrev || cmpKeys(minK, s.prevKey, s.byInfo.Desc) != 0
	if newGroup {
		// a new BY group starts with all merged variables missing; sources
		// without rows in the group stay missing for the whole group
		s.resetSources(st)
		// SAS reports repeats of BY values once per merge when more than one
		// data set contributes several observations to the same group
		if !s.repeatsNoted {
			n := 0
			for _, src := range s.srcs {
				if s.rowsInGroup(src, minK) > 1 {
					n++
				}
			}
			if n > 1 {
				st.note("MERGE statement has more than one data set with repeats of BY values.")
				s.repeatsNoted = true
			}
		}
		// IN= flags hold for the whole group: 1 when the source has rows in it
		for _, src := range s.srcs {
			if src.inSlot >= 0 {
				if s.rowsInGroup(src, minK) > 0 {
					st.pdv[src.inSlot] = NumV(1)
				} else {
					st.pdv[src.inSlot] = NumV(0)
				}
			}
		}
	}
	// write BY values into PDV
	for j, sl := range s.pd {
		st.pdv[sl] = minK[j]
	}
	st.readIter = true
	for _, src := range s.srcs {
		if k := s.srcKey(src); k != nil && cmpKeys(k, minK, s.byInfo.Desc) == 0 {
			row := src.rows[src.pos]
			src.pos++
			src.read++
			s.copyRow(st, src, row)
		}
	}
	// FIRST./LAST. based on group boundaries
	for _, n := range s.byInfo.Names {
		if v := st.lookup("FIRST." + n); v != nil {
			if newGroup {
				st.pdv[v.slot] = NumV(1)
			} else {
				st.pdv[v.slot] = NumV(0)
			}
		}
	}
	// LAST.: is there any source whose NEXT key is still minK?
	moreSame := false
	for _, src := range s.srcs {
		if src.pos < len(src.rows) {
			k := s.srcKey(src)
			if cmpKeys(k, minK, s.byInfo.Desc) == 0 {
				moreSame = true
			}
		}
	}
	for _, n := range s.byInfo.Names {
		if v := st.lookup("LAST." + n); v != nil {
			if moreSame {
				st.pdv[v.slot] = NumV(0)
			} else {
				st.pdv[v.slot] = NumV(1)
			}
		}
	}
	s.prevKey = minK
	s.havePrev = true
	return dsNext
}

// rowsInGroup counts the rows src still contributes to the BY group of key k.
func (s *dsSetStmt) rowsInGroup(src *dsSrc, k []Value) int {
	n := 0
	for i := src.pos; i < len(src.rows); i++ {
		key := make([]Value, len(src.keys))
		for j, ci := range src.keys {
			key[j] = src.rows[i][ci]
		}
		if cmpKeys(key, k, s.byInfo.Desc) != 0 {
			break
		}
		n++
	}
	return n
}

func (s *dsSetStmt) maxLen() int {
	m := 0
	for _, src := range s.srcs {
		if len(src.rows) > m {
			m = len(src.rows)
		}
	}
	return m
}

func (s *dsSetStmt) copyRow(st *dsStep, src *dsSrc, row []Value) {
	for j, slot := range src.cols {
		x := row[j]
		if x.Char {
			x.Str = dsFit(x.Str, st.vars[slot].Len)
		}
		st.pdv[slot] = x
	}
}

// openSources loads infile data and compiles SET/MERGE sources.
func (st *dsStep) openSources() error {
	for _, f := range st.infiles {
		if err := f.load(st); err != nil {
			return err
		}
	}
	for _, s := range st.sets {
		if err := s.compile(); err != nil {
			return err
		}
	}
	return nil
}

var _ = math.IsNaN
