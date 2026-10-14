package sas

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

// DATA step: compilation into a program over a slot-addressed program data
// vector (PDV) and execution with SAS semantics (implicit loop, retain rules,
// implicit OUTPUT, end-of-input termination).

func init() { RunDataStepFunc = dsRun }

// dsVar is a PDV variable.
type dsVar struct {
	Name     string
	Char     bool
	Len      int
	Format   string
	Informat string
	Label    string

	slot     int
	typed    bool // type determined (otherwise numeric at end of compilation)
	lenSet   bool // length fixed by LENGTH/ATTRIB/INPUT/first assignment/data set
	retain   bool // RETAIN, sum statement, initial array values
	auto     bool // automatic or temporary variable: never written to output
	assigned bool // receives a value somewhere (otherwise "uninitialized")
	fromData bool // read by SET/MERGE: retained
	init     *Value
	blank    string // char missing value (blank-padded)
}

func (v *dsVar) missing() Value {
	if v.Char {
		return Value{Char: true, Str: v.blank}
	}
	return Missing
}

// dsCtl is the control-flow result of executing a statement.
type dsCtl uint8

const (
	dsNext     dsCtl = iota
	dsDelete         // DELETE / false subsetting IF: next iteration, no implicit output
	dsReturn         // RETURN: end iteration (implicit output applies)
	dsStop           // STOP: end the step now
	dsEOF            // input exhausted: end the step now
	dsLeave          // LEAVE: exit innermost DO loop
	dsContinue       // CONTINUE: next iteration of innermost DO loop
	dsAbort          // ABORT / runtime error: end the step with error
)

// dsNode is an executable statement.
type dsNode interface {
	exec(st *dsStep) dsCtl
}

// dsOut is an output data set of the DATA statement.
type dsOut struct {
	ref   *DSRef
	name  string
	slots []int
	vars  []*Var
	rows  [][]Value
	slab  []Value
}

// dsErr is a compile-time error with a source line.
type dsErr struct {
	line int
	msg  string
}

func (e *dsErr) Error() string { return e.msg }

func dsErrf(line int, f string, a ...any) error { return &dsErr{line: line, msg: fmt.Sprintf(f, a...)} }

// dsStep is a compiled DATA step.
type dsStep struct {
	s    *Session
	ds   *DataStep
	line int

	vars   []*dsVar
	byName map[string]*dsVar
	arrays map[string]*dsArray
	pdv    []Value
	ctx    *EvalCtx
	body   []dsNode
	outs   []*dsOut
	null   bool // DATA _NULL_

	hasOutput  bool // an explicit OUTPUT statement exists
	nReaders   int  // SET/MERGE/INPUT statements
	readIter   bool // a reading statement executed in this iteration
	sets       []*dsSetStmt
	infiles    []*dsInfile
	curInfile  *dsInfile
	datalines  []string
	dataLine   int
	byPending  *dsSetStmt
	nVar, eVar *dsVar
	keepToks   [][]Token
	dropToks   [][]Token
	renames    [][2]string
	wheres     []Expr
	whereText  []string
	lineOfStmt int
	ioCache    map[*Stmt]dsNode

	convC2N, convN2C []int // compile-time conversion places (lines)

	// runtime
	resetSlots  []int
	resetVals   []Value
	fatal       bool
	stopErr     bool // step stopped because of an error
	missAt      map[int]int
	missOrder   []int
	invAt       map[int]int
	invOrder    []int
	errDumps    int
	errLimit    bool
	pendNotes   []string // invalid-data notes of the current iteration
	pendRule    *dsInfile
	rng         *dsRNG
	putFile     string // "" = LOG, "PRINT" = output window
	printLines  []string
	putLine     []byte
	putCol      int
	dataN       int
	wentNewNote bool
	lostCard    bool
}

func dsRun(s *Session, d *DataStep) error {
	st := &dsStep{s: s, ds: d, line: d.Line, byName: map[string]*dsVar{}, arrays: map[string]*dsArray{},
		missAt: map[int]int{}, invAt: map[int]int{}, ioCache: map[*Stmt]dsNode{}}
	st.ctx = &EvalCtx{Sess: s, Line: d.Line}
	st.ctx.Get = func(name string) (Value, bool) {
		if v := st.byName[strings.ToUpper(name)]; v != nil && v.slot < len(st.pdv) {
			return st.pdv[v.slot], true
		}
		return Missing, false
	}
	st.ctx.Hook = dsHook(st)
	st.ctx.ExpandOf = dsExpandOf(st)
	st.ctx.State = map[*Call]any{}
	st.rng = &dsRNG{}
	if err := st.compile(); err != nil {
		line := d.Line
		if e, ok := err.(*dsErr); ok && e.line > 0 {
			line = e.line
		}
		s.Log.Error(line, err.Error())
		s.Log.Note(d.Line, "The SAS System stopped processing this step because of errors.")
		for _, o := range st.outs {
			s.Log.Warn(d.Line, fmt.Sprintf("Data set %s was not replaced because this step was stopped.", DisplayName(o.name)))
		}
		return nil
	}
	st.compileNotes()
	st.run()
	st.finish()
	return nil
}

// ---------------------------------------------------------------- variables

func (st *dsStep) lookup(name string) *dsVar { return st.byName[strings.ToUpper(name)] }

// variable returns the PDV variable, creating an untyped one if needed.
func (st *dsStep) variable(name string) *dsVar {
	u := strings.ToUpper(name)
	if v := st.byName[u]; v != nil {
		return v
	}
	v := &dsVar{Name: name, Len: 8, slot: len(st.vars)}
	st.vars = append(st.vars, v)
	st.byName[u] = v
	return v
}

// autoVar creates an automatic (never output, retained) numeric variable.
func (st *dsStep) autoVar(name string) *dsVar {
	v := st.variable(name)
	v.auto, v.typed, v.lenSet, v.retain, v.assigned = true, true, true, true, true
	return v
}

// define fixes the variable type (and length for new character variables).
func (st *dsStep) define(v *dsVar, char bool, length int, line int) error {
	if !v.typed {
		v.typed, v.Char = true, char
		if char {
			if !v.lenSet {
				if length <= 0 {
					length = 8
				}
				v.Len, v.lenSet = length, true
			}
		} else if !v.lenSet {
			v.Len = 8
		}
		return nil
	}
	if v.Char != char {
		return dsErrf(line, "Variable %s has been defined as both character and numeric.", v.Name)
	}
	if char && !v.lenSet {
		if length <= 0 {
			length = 8
		}
		v.Len, v.lenSet = length, true
	}
	return nil
}

// numeric declares a variable used where a number is required.
func (st *dsStep) numeric(v *dsVar, line int) error { return st.define(v, false, 8, line) }

// ---------------------------------------------------------------- storing values

// dsFit pads or truncates a character value to length n.
func dsFit(s string, n int) string {
	if len(s) == n {
		return s
	}
	if len(s) > n {
		return s[:n]
	}
	return s + strings.Repeat(" ", n-len(s))
}

// dsNumToChar converts a number for storage in a character variable of the
// given length (SAS: BESTw. with w = variable length, right aligned).
func dsNumToChar(f float64, length int) string {
	w := length
	if w > 32 {
		w = 32
	}
	if w < 1 {
		w = 1
	}
	s := formatBest(f, w)
	if len(s) < w {
		s = strings.Repeat(" ", w-len(s)) + s
	}
	return s
}

// dsCharToNum converts character data to a number like the w. informat.
func dsCharToNum(s string) (float64, bool) {
	t := strings.TrimSpace(s)
	if t == "" || t == "." {
		return math.NaN(), true
	}
	return dsParseNum(t)
}

// dsParseNum parses a standard numeric field (digits, sign, point, exponent).
func dsParseNum(t string) (float64, bool) {
	for i := 0; i < len(t); i++ {
		c := t[i]
		if !(c >= '0' && c <= '9' || c == '.' || c == '-' || c == '+' || c == 'e' || c == 'E' || c == 'd' || c == 'D') {
			return math.NaN(), false
		}
	}
	if strings.ContainsAny(t, "dD") {
		t = strings.NewReplacer("d", "e", "D", "e").Replace(t)
	}
	if t == "." {
		return math.NaN(), true
	}
	f, err := strconv.ParseFloat(t, 64)
	if err != nil {
		return math.NaN(), false
	}
	return f, true
}

// store assigns a value to a PDV variable with SAS conversion and length rules.
func (st *dsStep) store(v *dsVar, x Value) {
	st.pdv[v.slot] = st.convert(v, x)
}

func (st *dsStep) convert(v *dsVar, x Value) Value {
	if v.Char {
		var s string
		if x.Char {
			s = x.Str
		} else {
			s = dsNumToChar(x.Num, v.Len)
		}
		return Value{Char: true, Str: dsFit(s, v.Len)}
	}
	if !x.Char {
		return x
	}
	f, ok := dsCharToNum(x.Str)
	if !ok {
		st.invalidNumeric(x.Str)
	}
	return Value{Num: f}
}

func (st *dsStep) invalidNumeric(s string) {
	st.note(fmt.Sprintf("Invalid numeric data, '%s' , at line %d.", strings.TrimRight(s, " "), st.ctx.Line))
	st.setError()
}

func (st *dsStep) setError() { st.pdv[st.eVar.slot] = Value{Num: 1} }

// note logs a runtime note subject to the ERRORS= style limit.
func (st *dsStep) note(msg string) {
	if st.errDumps >= dsMaxErrors {
		return
	}
	st.s.Log.Note(st.ctx.Line, msg)
}

const dsMaxErrors = 20

// runtimeError reports an error that stops the step.
func (st *dsStep) runtimeError(msg string) {
	if st.fatal {
		return
	}
	st.fatal = true
	st.s.Log.Error(st.ctx.Line, msg)
	st.dumpPDV()
}

// ---------------------------------------------------------------- compile

func (st *dsStep) compile() error {
	st.nVar = st.autoVar("_N_")
	st.eVar = st.autoVar("_ERROR_")
	if err := st.parseHeader(); err != nil {
		return err
	}
	for _, b := range st.ds.Body {
		if len(b.Datalines) > 0 || dsIsDatalinesKw(b.Keyword()) {
			st.datalines = b.Datalines
			st.dataLine = b.Line + 1
		}
	}
	// FIRST./LAST. variables for BY statements
	for _, b := range st.ds.Body {
		if b.Keyword() == "BY" {
			for _, t := range b.Args() {
				if t.Kind == TIdent && !t.Is("DESCENDING", "NOTSORTED", "GROUPFORMAT") {
					st.autoVar("FIRST." + t.Text)
					st.autoVar("LAST." + t.Text)
				}
			}
		}
	}
	p := &dsParser{st: st, stmts: st.ds.Body}
	// Compiler pass 1: declaration and input statements, so that variables
	// get their types/lengths from SET/INPUT/LENGTH before they are used.
	for _, b := range st.ds.Body {
		switch b.Keyword() {
		case "SET", "MERGE", "UPDATE", "INFILE", "INPUT", "BY", "WHERE",
			"LENGTH", "LABEL", "FORMAT", "INFORMAT", "ATTRIB", "RETAIN", "ARRAY":
			if _, err := p.stmt(b, b.Keyword()); err != nil {
				return err
			}
		}
	}
	if err := st.openSources(); err != nil {
		return err
	}
	// pass 2: the full body
	body, err := p.block(false)
	if err != nil {
		return err
	}
	st.body = body
	// finalize types and lengths
	for _, v := range st.vars {
		if !v.typed {
			v.typed = true
		}
		if v.Char {
			if v.Len <= 0 {
				v.Len = 8
			}
			v.blank = strings.Repeat(" ", v.Len)
		} else {
			v.Len = 8
		}
	}
	st.pdv = make([]Value, len(st.vars))
	for _, v := range st.vars {
		st.pdv[v.slot] = v.missing()
		if v.init != nil {
			st.pdv[v.slot] = st.convertInit(v, *v.init)
		}
		if !v.retain && !v.fromData && !v.auto {
			st.resetSlots = append(st.resetSlots, v.slot)
			st.resetVals = append(st.resetVals, v.missing())
		}
	}
	for _, a := range st.arrays {
		a.finalize()
	}
	st.pdv[st.nVar.slot] = Value{Num: 0}
	st.pdv[st.eVar.slot] = Value{Num: 0}
	return st.buildOutputs()
}

func (st *dsStep) convertInit(v *dsVar, x Value) Value {
	if v.Char {
		s := x.Str
		if !x.Char {
			s = dsNumToChar(x.Num, v.Len)
		}
		return Value{Char: true, Str: dsFit(s, v.Len)}
	}
	if x.Char {
		f, _ := dsCharToNum(x.Str)
		return Value{Num: f}
	}
	return x
}

func dsIsDatalinesKw(kw string) bool {
	switch kw {
	case "DATALINES", "CARDS", "LINES", "DATALINES4", "CARDS4", "LINES4":
		return true
	}
	return false
}

// parseHeader parses the DATA statement.
func (st *dsStep) parseHeader() error {
	toks := st.ds.Header.Args()
	toks, _ = SplitSlash(toks)
	c := NewCursor(toks)
	if c.AtEnd() {
		n := 1
		if v, ok := st.s.Ext["dsDataN"].(int); ok {
			n = v + 1
		}
		st.s.Ext["dsDataN"] = n
		name := fmt.Sprintf("DATA%d", n)
		st.outs = append(st.outs, &dsOut{ref: &DSRef{Name: name, Opts: &Opts{}, Line: st.line}, name: name})
		return nil
	}
	for !c.AtEnd() {
		ref, err := ParseDSRef(c)
		if err != nil {
			return dsErrf(st.line, "%v", err)
		}
		if strings.EqualFold(ref.Name, "_NULL_") {
			st.null = true
			continue
		}
		st.outs = append(st.outs, &dsOut{ref: ref, name: ref.Name})
	}
	if st.null && len(st.outs) > 0 {
		st.null = false
	}
	return nil
}

func (st *dsStep) findOut(name string) *dsOut {
	n := NormName(name)
	for _, o := range st.outs {
		if NormName(o.name) == n {
			return o
		}
	}
	return nil
}

// expandNames expands a KEEP/DROP style list against the PDV (non-auto vars).
func (st *dsStep) expandNames(toks []Token, line int, warn bool) []*dsVar {
	var out []*dsVar
	c := NewCursor(toks)
	for !c.AtEnd() {
		t := c.Next()
		if t.Kind != TIdent {
			continue
		}
		if c.Peek().IsOp(":") { // prefix list
			c.Next()
			p := strings.ToUpper(t.Text)
			for _, v := range st.vars {
				if !v.auto && strings.HasPrefix(strings.ToUpper(v.Name), p) {
					out = append(out, v)
				}
			}
			continue
		}
		if c.Peek().IsOp("--") && c.PeekN(1).Kind == TIdent {
			c.Next()
			e := c.Next()
			a, b := st.lookup(t.Text), st.lookup(e.Text)
			if a != nil && b != nil {
				for k := a.slot; k <= b.slot; k++ {
					if !st.vars[k].auto {
						out = append(out, st.vars[k])
					}
				}
			}
			continue
		}
		names := []string{t.Text}
		if c.Peek().IsOp("-") && c.PeekN(1).Kind == TIdent {
			if r, err := NumberedRange(t.Text, c.PeekN(1).Text); err == nil {
				c.P += 2
				names = r
			}
		}
		for _, n := range names {
			switch strings.ToUpper(n) {
			case "_ALL_":
				for _, v := range st.vars {
					if !v.auto {
						out = append(out, v)
					}
				}
				continue
			case "_NUMERIC_", "_CHARACTER_", "_CHAR_":
				char := strings.ToUpper(n) != "_NUMERIC_"
				for _, v := range st.vars {
					if !v.auto && v.Char == char {
						out = append(out, v)
					}
				}
				continue
			}
			v := st.lookup(n)
			if v == nil {
				if warn {
					st.s.Log.Warn(line, fmt.Sprintf("The variable %s in the DROP, KEEP, or RENAME list has never been referenced.", n))
				}
				continue
			}
			out = append(out, v)
		}
	}
	return out
}

// buildOutputs determines the variables written to each output data set.
func (st *dsStep) buildOutputs() error {
	keep := map[*dsVar]bool{}
	drop := map[*dsVar]bool{}
	hasKeep := len(st.keepToks) > 0
	for _, k := range st.keepToks {
		for _, v := range st.expandNames(k, st.line, true) {
			keep[v] = true
		}
	}
	for _, k := range st.dropToks {
		for _, v := range st.expandNames(k, st.line, true) {
			drop[v] = true
		}
	}
	rename := map[*dsVar]string{}
	for _, r := range st.renames {
		v := st.lookup(r[0])
		if v == nil {
			st.s.Log.Warn(st.line, fmt.Sprintf("The variable %s in the DROP, KEEP, or RENAME list has never been referenced.", r[0]))
			continue
		}
		rename[v] = r[1]
	}
	for _, o := range st.outs {
		var sel []*dsVar
		for _, v := range st.vars {
			if v.auto || drop[v] || (hasKeep && !keep[v]) {
				continue
			}
			sel = append(sel, v)
		}
		if o.ref.Opts != nil {
			if op, ok := o.ref.Opts.Get("KEEP"); ok {
				ks := map[*dsVar]bool{}
				for _, v := range st.expandNames(op.Inner(), o.ref.Line, true) {
					ks[v] = true
				}
				var s2 []*dsVar
				for _, v := range sel {
					if ks[v] {
						s2 = append(s2, v)
					}
				}
				sel = s2
			}
			if op, ok := o.ref.Opts.Get("DROP"); ok {
				ds := map[*dsVar]bool{}
				for _, v := range st.expandNames(op.Inner(), o.ref.Line, true) {
					ds[v] = true
				}
				var s2 []*dsVar
				for _, v := range sel {
					if !ds[v] {
						s2 = append(s2, v)
					}
				}
				sel = s2
			}
		}
		optRename := map[string]string{}
		if o.ref.Opts != nil {
			if op, ok := o.ref.Opts.Get("RENAME"); ok {
				c := NewCursor(op.Inner())
				for !c.AtEnd() {
					from := c.Next()
					if !c.AcceptOp("=") {
						return dsErrf(o.ref.Line, "Syntax error in RENAME= option.")
					}
					to := c.Next()
					optRename[strings.ToUpper(from.Text)] = to.Text
				}
			}
		}
		for _, v := range sel {
			name := v.Name
			if r, ok := rename[v]; ok {
				name = r
			}
			if r, ok := optRename[strings.ToUpper(name)]; ok {
				name = r
			}
			o.slots = append(o.slots, v.slot)
			o.vars = append(o.vars, &Var{Name: name, Char: v.Char, Length: v.Len, Format: v.Format, Informat: v.Informat, Label: v.Label})
		}
	}
	return nil
}

// compileNotes writes compile-time notes (conversions, uninitialized variables).
func (st *dsStep) compileNotes() {
	if len(st.convC2N) > 0 {
		st.s.Log.Note(st.line, "Character values have been converted to numeric values at the places given by: (Line).\n      "+dsLineList(st.convC2N))
	}
	if len(st.convN2C) > 0 {
		st.s.Log.Note(st.line, "Numeric values have been converted to character values at the places given by: (Line).\n      "+dsLineList(st.convN2C))
	}
	for _, v := range st.vars {
		if !v.assigned && !v.auto {
			st.s.Log.Note(st.line, fmt.Sprintf("Variable %s is uninitialized.", v.Name))
		}
	}
}

func dsLineList(lines []int) string {
	seen := map[int]bool{}
	var parts []string
	for _, l := range lines {
		if !seen[l] {
			seen[l] = true
			parts = append(parts, strconv.Itoa(l))
		}
	}
	return strings.Join(parts, "   ")
}

// ---------------------------------------------------------------- run

func (st *dsStep) run() {
	for _, f := range st.infiles {
		if f.path != "" {
			st.s.Log.Note(st.line, fmt.Sprintf("The infile '%s' is:\n      Filename=%s", f.display, f.path))
		}
	}
	n := 0
	for {
		n++
		st.pdv[st.nVar.slot] = Value{Num: float64(n)}
		st.pdv[st.eVar.slot] = Value{Num: 0}
		for i, sl := range st.resetSlots {
			st.pdv[sl] = st.resetVals[i]
		}
		for _, f := range st.infiles {
			f.iterStart()
		}
		st.readIter = false
		ctl := st.execBlock(st.body)
		if st.fatal {
			ctl = dsAbort
		}
		if (ctl == dsNext || ctl == dsReturn) && !st.hasOutput {
			st.output(nil)
		}
		st.endIteration()
		switch ctl {
		case dsStop, dsEOF:
			return
		case dsAbort:
			st.stopErr = true
			return
		}
		if st.nReaders == 0 {
			return
		}
		if !st.readIter {
			st.s.Log.Note(st.line, "DATA STEP stopped due to looping.")
			return
		}
	}
}

// endIteration prints pending invalid-data notes and the PDV dump when _ERROR_ is set.
func (st *dsStep) endIteration() {
	if st.pdv[st.eVar.slot].Num != 1 && len(st.pendNotes) == 0 {
		return
	}
	if st.errDumps >= dsMaxErrors {
		if !st.errLimit {
			st.errLimit = true
			st.s.Log.Warn(st.line, "Limit set by ERRORS= option reached.  Further errors of this type will not be printed.")
		}
		st.pendNotes, st.pendRule = nil, nil
		return
	}
	for _, m := range st.pendNotes {
		st.s.Log.Note(st.line, m)
	}
	if f := st.pendRule; f != nil {
		f.logRule(st)
	}
	st.pendNotes, st.pendRule = nil, nil
	if st.pdv[st.eVar.slot].Num == 1 {
		st.dumpPDV()
	}
}

func (st *dsStep) dumpPDV() {
	st.errDumps++
	st.s.Log.Put(st.allValues(true))
}

// allValues renders "name=value" for all variables (PUT _ALL_ format).
func (st *dsStep) allValues(withAuto bool) string {
	var b strings.Builder
	for _, v := range st.vars {
		if v == st.nVar || v == st.eVar || (v.auto && !withAuto) {
			continue
		}
		if b.Len() > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(v.Name)
		b.WriteByte('=')
		b.WriteString(st.listValue(v, st.pdv[v.slot]))
	}
	if withAuto {
		if b.Len() > 0 {
			b.WriteByte(' ')
		}
		fmt.Fprintf(&b, "_ERROR_=%s _N_=%s", formatBest(st.pdv[st.eVar.slot].Num, 12), formatBest(st.pdv[st.nVar.slot].Num, 12))
	}
	return b.String()
}

// listValue formats a value for list-style PUT output.
func (st *dsStep) listValue(v *dsVar, x Value) string {
	if x.Char {
		s := strings.TrimRight(x.Str, " ")
		if v != nil && v.Format != "" {
			s = strings.TrimRight(FormatByName(x, v.Format), " ")
		}
		return s
	}
	if math.IsNaN(x.Num) {
		return "."
	}
	if v != nil && v.Format != "" {
		return strings.TrimSpace(FormatByName(x, v.Format))
	}
	return formatBest(x.Num, 12)
}

func (st *dsStep) execBlock(nodes []dsNode) dsCtl {
	for _, n := range nodes {
		if c := n.exec(st); c != dsNext {
			return c
		}
		if st.fatal {
			return dsAbort
		}
	}
	return dsNext
}

// track attributes missing-value and invalid-operation counts to a line.
func (st *dsStep) track(line, miss0, inv0 int) {
	c := st.ctx
	if c.MissingOps != miss0 {
		if _, ok := st.missAt[line]; !ok {
			st.missOrder = append(st.missOrder, line)
		}
		st.missAt[line] += c.MissingOps - miss0
	}
	if c.InvalidOps != inv0 {
		if _, ok := st.invAt[line]; !ok {
			st.invOrder = append(st.invOrder, line)
		}
		st.invAt[line] += c.InvalidOps - inv0
		st.note(fmt.Sprintf("Invalid argument or division by zero at line %d.", line))
		st.setError()
	}
}

// output writes the PDV to the given outputs (nil = all).
func (st *dsStep) output(targets []*dsOut) {
	if targets == nil {
		targets = st.outs
	}
	for _, o := range targets {
		n := len(o.slots)
		if len(o.slab) < n {
			k := 4096
			if n*k < 65536 {
				k = 65536 / max(n, 1)
			}
			o.slab = make([]Value, n*k)
		}
		row := o.slab[:n:n]
		o.slab = o.slab[n:]
		for j, sl := range o.slots {
			x := st.pdv[sl]
			if x.Char {
				x.Str = strings.TrimRight(x.Str, " ")
			}
			row[j] = x
		}
		o.rows = append(o.rows, row)
	}
}

// finish writes log notes and stores the output data sets.
func (st *dsStep) finish() {
	log := st.s.Log
	if st.lostCard {
		log.Note(st.line, "LOST CARD.")
	}
	if st.wentNewNote {
		log.Note(st.line, "SAS went to a new line when INPUT statement reached past the end of a line.")
	}
	if len(st.missOrder) > 0 {
		var parts []string
		for _, l := range st.missOrder {
			parts = append(parts, fmt.Sprintf("%d at line %d", st.missAt[l], l))
		}
		log.Note(st.line, "Missing values were generated as a result of performing an operation on missing values.\n      Each place is given by: (Number of times) at (Line).\n      "+strings.Join(parts, "   "))
	}
	if len(st.invOrder) > 0 {
		var parts []string
		for _, l := range st.invOrder {
			parts = append(parts, fmt.Sprintf("%d at line %d", st.invAt[l], l))
		}
		log.Note(st.line, "Mathematical operations could not be performed at the following places. The results of the operations have been set to missing values.\n      Each place is given by: (Number of times) at (Line).\n      "+strings.Join(parts, "   "))
	}
	for _, f := range st.infiles {
		f.finishNotes(st)
	}
	for _, s := range st.sets {
		for _, src := range s.srcs {
			msg := fmt.Sprintf("There were %d observations read from the data set %s.", src.read, src.display)
			if src.whereText != "" {
				msg += "\n      WHERE " + src.whereText + ";"
			}
			log.Note(st.line, msg)
		}
	}
	if len(st.printLines) > 0 {
		st.s.Out.BeginProc("")
		st.s.Out.AddText(&Text{Text: strings.Join(st.printLines, "\n"), Pre: true})
		st.s.Out.EndProc()
	}
	if st.stopErr {
		log.Note(st.line, "The SAS System stopped processing this step because of errors.")
	}
	for _, o := range st.outs {
		d := &Dataset{Name: NormName(o.name), Vars: o.vars, Rows: o.rows}
		if o.ref.Opts != nil {
			if op, ok := o.ref.Opts.Get("LABEL"); ok {
				d.Label = op.Str()
			}
		}
		if st.stopErr {
			log.Warn(st.line, fmt.Sprintf("The data set %s may be incomplete.  When this step was stopped there were %d observations and %d variables.", DisplayName(o.name), len(d.Rows), len(d.Vars)))
		}
		if err := st.s.WriteDS(&DSRef{Name: o.name, Line: st.line}, d); err != nil {
			log.Error(st.line, err.Error())
		}
	}
}

// sortedKeys returns map keys in ascending order (deterministic notes).
func dsSortedInts(m map[int]int) []int {
	var ks []int
	for k := range m {
		ks = append(ks, k)
	}
	sort.Ints(ks)
	return ks
}
