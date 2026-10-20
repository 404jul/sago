package sas

import (
	"fmt"
	"strings"
)

// DATA step: declaration statements (LENGTH/RETAIN/FORMAT/ATTRIB/ARRAY),
// CALL, PUT, ERROR, and SET/MERGE/INFILE/INPUT compile hooks (ds prefix).

// parseIO compiles SET/MERGE/INFILE/INPUT/UPDATE/MODIFY.
func (p *dsParser) parseIO(s *Stmt, kw string) (dsNode, error) {
	st := p.st
	switch kw {
	case "INFILE":
		if n, ok := st.ioCache[s]; ok {
			return n, nil
		}
		f, err := dsNewInfile(st, s)
		if err != nil {
			return nil, err
		}
		st.ioCache[s] = dsFuncNode{fn: func(*dsStep) dsCtl { return dsNext }}
		st.infiles = append(st.infiles, f)
		st.curInfile = f
		return nil, nil
	case "SET", "MERGE", "UPDATE":
		if n, ok := st.ioCache[s]; ok {
			return n, nil
		}

		sset := &dsSetStmt{st: st, kind: kw, line: s.Line, endSlot: -1, nobsSlot: -1}
		st.ioCache[s] = sset
		c := NewCursor(s.Args())
		for !c.AtEnd() {
			if t := c.Peek(); t.Kind == TIdent && c.PeekN(1).IsOp("=") && c.PeekN(2).Kind == TIdent &&
				t.Is("END", "NOBS", "POINT", "IN", "ERROR") {
				key := strings.ToUpper(t.Text)
				c.P += 2
				val := c.Next().Text
				sset.opts = append(sset.opts, [2]string{key, val})
				continue
			}
			ref, err := ParseDSRef(c)
			if err != nil {
				return nil, dsErrf(s.Line, "%v", err)
			}
			sset.refs = append(sset.refs, ref)
		}
		st.sets = append(st.sets, sset)
		st.byPending = sset
		return sset, nil
	case "MODIFY":
		return nil, dsErrf(s.Line, "The MODIFY statement is not supported by sago.")
	case "INPUT":
		if n, ok := st.ioCache[s]; ok {
			return n, nil
		}
		n, err := p.parseInput(s)
		if err != nil {
			return nil, err
		}
		st.ioCache[s] = n
		return n, nil
	}
	return nil, dsErrf(s.Line, "Unexpected statement %s.", kw)
}

// ---------------------------------------------------------------- LENGTH/LABEL/FORMAT/ATTRIB

func (p *dsParser) parseLength(s *Stmt) (dsNode, error) {
	c := NewCursor(s.Args())
	charNext := false
	for !c.AtEnd() {
		t := c.Next()
		if t.Kind != TIdent {
			continue
		}
		// names may be ranges a1-a5
		names := []string{t.Text}
		if c.Peek().IsOp("-") && c.PeekN(1).Kind == TIdent {
			if r, err := NumberedRange(t.Text, c.PeekN(1).Text); err == nil {
				c.P += 2
				names = r
			}
		}
		length := 0
		char := charNext
		charNext = false
		// optional $ and length
		for !c.AtEnd() {
			t2 := c.Peek()
			if t2.IsOp("$") {
				char = true
				c.Next()
				continue
			}
			if t2.Kind == TNumber {
				length = int(t2.Num)
				c.Next()
				continue
			}
			if t2.Kind == TIdent && strings.Contains(t2.Text, ".") {
				// informat determines type/length ($15. => char)
				f := ParseFormat(t2.Text)
				if f != nil {
					if f.Char {
						char = true
						if !f.HasW {
							length = 8
						} else if length == 0 {
							length = f.W
						}
					}
					c.Next()
					continue
				}
				break
			}
			break
		}
		for _, n := range names {
			v := p.st.variable(n)
			if length == 0 && char {
				length = 8
			}
			if err := p.st.define(v, char, length, s.Line); err != nil {
				return nil, err
			}
			if char {
				v.lenSet = true
				if length == 0 {
					v.Len = 8
				} else {
					v.Len = length
				}
			}
			v.assigned = true
		}
	}
	return nil, nil
}

func (p *dsParser) parseLabels(s *Stmt) {
	c := NewCursor(s.Args())
	for !c.AtEnd() {
		name := c.Next()
		if !c.AcceptOp("=") {
			continue
		}
		v := c.Next()
		if p.st.lookup(name.Text) == nil {
			p.st.variable(name.Text)
		}
		if vv := p.st.lookup(name.Text); vv != nil {
			vv.Label = v.Text
		}
	}
}

func (p *dsParser) parseFormats(s *Stmt, informat bool) {
	var pending []string
	c := NewCursor(s.Args())
	for !c.AtEnd() {
		t := c.Next()
		text := ""
		if t.IsOp("$") && !c.AtEnd() {
			text = "$" + c.Next().Text
		} else if t.Kind == TNumber || (t.Kind == TIdent && strings.Contains(t.Text, ".")) {
			text = t.Text
		}
		if text != "" {
			for _, n := range pending {
				if v := p.st.lookup(n); v != nil {
					if informat {
						v.Informat = text
					} else {
						v.Format = text
					}
				}
			}
			pending = nil
			continue
		}
		if t.Kind == TIdent {
			if p.st.lookup(t.Text) == nil {
				p.st.variable(t.Text)
			}
			pending = append(pending, t.Text)
		}
	}
}

func (p *dsParser) parseAttrib(s *Stmt) (dsNode, error) {
	c := NewCursor(s.Args())
	var pending []string
	for !c.AtEnd() {
		t := c.Next()
		if t.Kind == TIdent && c.Peek().IsOp("=") {
			key := strings.ToUpper(t.Text)
			c.Next()
			switch key {
			case "FORMAT", "INFORMAT":
				val := c.Next().Text
				for _, n := range pending {
					if v := p.st.lookup(n); v != nil {
						if key == "FORMAT" {
							v.Format = val
						} else {
							v.Informat = val
						}
					}
				}
			case "LABEL":
				val := c.Next()
				if val.Kind == TIdent && c.Peek().IsOp("=") {
					continue
				}
				for _, n := range pending {
					if v := p.st.lookup(n); v != nil {
						v.Label = val.Text
					}
				}
			case "LENGTH":
				val := c.Next()
				char := false
				if c.Peek().IsOp("$") {
					char = true
					c.Next()
				}
				length := 8
				if c.Peek().Kind == TNumber {
					length = int(c.Next().Num)
				} else if strings.HasSuffix(val.Text, "$") {
					char = true
					fmt.Sscanf(val.Text, "$%d", &length)
				}
				for _, n := range pending {
					v := p.st.variable(n)
					if err := p.st.define(v, char, length, s.Line); err != nil {
						return nil, err
					}
				}
			}
			continue
		}
		if t.Kind == TIdent {
			if strings.Contains(t.Text, ".") || t.Text == "$" {
				continue
			}
			if p.st.lookup(t.Text) == nil {
				p.st.variable(t.Text)
			}
			pending = append(pending, t.Text)
		}
	}
	return nil, nil
}

// ---------------------------------------------------------------- ARRAY

type dsArray struct {
	st    *dsStep
	name  string
	vars  []*dsVar
	temp  bool
	inits []Value
	bound int
}

func (a *dsArray) finalize() {
	a.bound = len(a.vars)
}

func (a *dsArray) elem(st *dsStep, i int) (*dsVar, bool) {
	if i < 1 || i > a.bound {
		return nil, false
	}
	return a.vars[i-1], true
}

func (p *dsParser) parseArray(s *Stmt) (dsNode, error) {
	c := NewCursor(s.Args())
	name, err := c.ExpectIdent()
	if err != nil {
		return nil, dsErrf(s.Line, "ARRAY statement requires a name.")
	}
	dims := 1
	size := -1
	if c.Peek().IsOp("{", "[", "(") {
		g, _ := c.Group()
		if len(g) == 1 && g[0].Kind == TNumber {
			size = int(g[0].Num)
		} else if len(g) > 0 {
			dims = len(g) // multi-dim treated as flat
			if len(g) == 1 && g[0].Kind == TIdent {
				if v := p.st.lookup(g[0].Text); v != nil {
					_ = v // dynamic bound: resolved at runtime via DIM
				}
			}
		}
	}
	a := &dsArray{st: p.st, name: strings.ToUpper(name)}
	// $ / length / _temporary_
	char := false
	length := 0
	for !c.AtEnd() {
		t := c.Peek()
		if t.IsOp("$") {
			char = true
			c.Next()
			continue
		}
		if t.Kind == TNumber {
			length = int(t.Num)
			c.Next()
			continue
		}
		if t.Kind == TIdent && strings.Contains(t.Text, ".") {
			if f := ParseFormat(t.Text); f != nil && f.Char {
				char = true
				if length == 0 && f.HasW {
					length = f.W
				}
			}
			c.Next()
			continue
		}
		if t.Is("_TEMPORARY_") {
			a.temp = true
			c.Next()
			continue
		}
		break
	}
	// element variables
	var names []string
	for !c.AtEnd() {
		t := c.Next()
		if t.Kind != TIdent {
			continue
		}
		if c.Peek().IsOp("-") && c.PeekN(1).Kind == TIdent {
			if r, err := NumberedRange(t.Text, c.PeekN(1).Text); err == nil {
				c.P += 2
				names = append(names, r...)
				continue
			}
		}
		names = append(names, t.Text)
	}
	if len(names) == 0 {
		if size <= 0 {
			size = 1
		}
		for i := 1; i <= size; i++ {
			names = append(names, fmt.Sprintf("%s%d", name, i))
		}
	}
	for _, n := range names {
		v := p.st.variable(n)
		if !v.typed {
			if err := p.st.define(v, char, length, s.Line); err != nil {
				return nil, err
			}
		}
		if !v.auto {
			v.assigned = true
		}
		a.vars = append(a.vars, v)
	}
	if size > 0 && len(a.vars) != size {
		return nil, dsErrf(s.Line, "Too few variables defined for array %s (%d).", strings.ToUpper(name), len(a.vars))
	}
	// initial values
	if c.Peek().IsOp("(") {
		g, _ := c.Group()
		for _, t := range g {
			if t.Kind == TNumber {
				a.inits = append(a.inits, NumV(t.Num))
			} else if t.Kind == TString {
				a.inits = append(a.inits, StrV(t.Text))
			} else if t.Kind == TIdent {
				a.inits = append(a.inits, StrV(t.Text))
			}
		}
	}
	_ = dims
	p.st.arrays[strings.ToUpper(name)] = a
	// initial values are set once at compile (like _temporary_ arrays in SAS retain behavior)
	if len(a.inits) > 0 {
		for i, v := range a.vars {
			if i < len(a.inits) {
				v.init = &a.inits[i]
				v.retain = true
			}
		}
	}
	return nil, nil
}

// ---------------------------------------------------------------- RETAIN

func (p *dsParser) parseRetain(s *Stmt) (dsNode, error) {
	st := p.st
	toks := s.Args()
	if len(toks) == 0 {
		return nil, nil
	}
	// trailing initial values: single value or (list)
	var inits []Value
	if toks[len(toks)-1].IsOp(")") {
		// find matching '('
		depth := 0
		idx := -1
		for i := len(toks) - 1; i >= 0; i-- {
			switch {
			case toks[i].IsOp(")"):
				depth++
			case toks[i].IsOp("("):
				depth--
				if depth == 0 {
					idx = i
				}
			}
			if idx >= 0 {
				break
			}
		}
		if idx >= 0 {
			for _, t := range toks[idx+1 : len(toks)-1] {
				switch t.Kind {
				case TNumber:
					inits = append(inits, NumV(t.Num))
				case TString:
					inits = append(inits, StrV(t.Text))
				}
			}
			toks = toks[:idx]
		}
	} else if len(toks) >= 2 && (toks[len(toks)-1].Kind == TNumber || toks[len(toks)-1].Kind == TString) && !toks[len(toks)-2].IsOp("=") {
		t := toks[len(toks)-1]
		if t.Kind == TNumber {
			inits = append(inits, NumV(t.Num))
		} else {
			inits = append(inits, StrV(t.Text))
		}
		toks = toks[:len(toks)-1]
	}
	var names []string
	c := NewCursor(toks)
	for !c.AtEnd() {
		t := c.Next()
		if t.Kind != TIdent {
			continue
		}
		if c.Peek().IsOp("-") && c.PeekN(1).Kind == TIdent {
			if r, err := NumberedRange(t.Text, c.PeekN(1).Text); err == nil {
				c.P += 2
				names = append(names, r...)
				continue
			}
		}
		names = append(names, t.Text)
	}
	for i, n := range names {
		v := st.variable(n)
		v.retain = true
		v.assigned = true
		if !v.typed && len(inits) > 0 {
			st.define(v, inits[0].Char, 0, s.Line)
		} else if !v.typed {
			st.numeric(v, s.Line)
		}
		if len(inits) == 1 {
			v.init = &inits[0]
		} else if i < len(inits) {
			v.init = &inits[i]
		}
	}
	return nil, nil
}

// ---------------------------------------------------------------- PUT / ERROR

type dsPutItem struct {
	lit     string // literal text (already includes trailing spaces)
	name    string // variable (x= form when eq)
	eq      bool
	format  string // explicit format (x date9., s $10.)
	expr    *dsExpr
	col     int  // @n
	rel     int  // +n
	blank   bool // / new line or _all_
	all     bool
	newline bool
}

type dsPutNode struct {
	items []dsPutItem
	line  int
}

func (n *dsPutNode) exec(st *dsStep) dsCtl {
	if st.putLine == nil {
		st.putLine = []byte{}
		st.putCol = 0
	}
	appendStr := func(s string) {
		st.putLine = append(st.putLine, s...)
		st.putCol += len([]rune(s))
	}
	for _, it := range n.items {
		switch {
		case it.newline:
			st.flushPut(n)
		case it.col > 0:
			for st.putCol < it.col {
				appendStr(" ")
			}
		case it.rel != 0:
			if it.rel > 0 {
				for i := 0; i < it.rel; i++ {
					appendStr(" ")
				}
			} else {
				st.putCol += it.rel
				if st.putCol < 0 {
					st.putCol = 0
				}
				if len(st.putLine) > st.putCol {
					st.putLine = st.putLine[:st.putCol]
				}
			}
		case it.all:
			appendStr(st.allValues(true))
			appendStr(" ")
		case it.name != "":
			v := st.lookup(it.name)
			if v == nil {
				continue
			}
			if it.eq {
				appendStr(v.Name + "=")
			}
			x := st.pdv[v.slot]
			if it.format != "" {
				// formatted output occupies exactly the format width: no
				// list separator after it
				appendStr(st.putFormatted(x, it.format))
			} else {
				appendStr(st.listValue(v, x))
				appendStr(" ")
			}
		case it.expr != nil:
			x := it.expr.eval(st)
			if x.Char {
				appendStr(strings.TrimRight(x.Str, " "))
			} else if !x.IsMissing() {
				appendStr(formatBest(x.Num, 12))
			}
			appendStr(" ")
		default:
			appendStr(it.lit)
		}
	}
	st.flushPut(n)
	return dsNext
}

func (st *dsStep) flushPut(n *dsPutNode) {
	if len(st.putLine) == 0 {
		return
	}
	line := strings.TrimRight(string(st.putLine), " ")
	if st.putFile == "PRINT" {
		st.printLines = append(st.printLines, line)
	} else {
		st.s.Log.Put(line)
	}
	st.putLine = st.putLine[:0]
	st.putCol = 0
}

// putFormatted renders a value under an explicit PUT format: character
// formats left-justify in the width, numeric formats right-justify.
func (st *dsStep) putFormatted(x Value, format string) string {
	s := FormatByName(x, format)
	f := ParseFormat(format)
	if f == nil || !f.HasW || len(s) >= f.W {
		return s
	}
	if f.Char {
		return s + strings.Repeat(" ", f.W-len(s))
	}
	return strings.Repeat(" ", f.W-len(s)) + s
}

func (p *dsParser) parsePut(s *Stmt) (dsNode, error) {
	n := &dsPutNode{line: s.Line}
	c := NewCursor(s.Args())
	if c.Peek().Is("PRINT", "LOG") {
		f := strings.ToUpper(c.Next().Text)
		p.st.putFile = f
	}
	for !c.AtEnd() {
		t := c.Next()
		switch {
		case t.IsOp("/"):
			n.items = append(n.items, dsPutItem{newline: true})
		case t.IsOp("@"):
			if c.Peek().Kind == TNumber {
				n.items = append(n.items, dsPutItem{col: int(c.Next().Num)})
			} else if c.Peek().Kind == TIdent {
				// @var: dynamic column; evaluate at runtime via expr
				e, err := p.expr([]Token{c.Next()}, s.Line)
				if err != nil {
					return nil, err
				}
				n.items = append(n.items, dsPutItem{})
				n.items[len(n.items)-1].expr = e
			}
		case t.IsOp("+"):
			if c.Peek().Kind == TNumber {
				n.items = append(n.items, dsPutItem{rel: int(c.Next().Num)})
			}
		case t.Kind == TString:
			n.items = append(n.items, dsPutItem{lit: t.Text})
		case t.Kind == TNumber:
			// format width after a variable is ignored in list output
		case t.Kind == TIdent:
			if t.Is("_ALL_") {
				n.items = append(n.items, dsPutItem{all: true})
				continue
			}
			// variable with optional = and trailing format
			eq := c.Peek().IsOp("=")
			if eq {
				c.Next()
			}
			if f, ok := putFormatToken(c); ok {
				n.items = append(n.items, dsPutItem{name: t.Text, eq: eq, format: f})
				continue
			}
			n.items = append(n.items, dsPutItem{name: t.Text, eq: eq})
		}
	}
	return n, nil
}

// putFormatToken consumes a format token after a PUT item: w.d / namew.
// (one token), or $w. / $namew. (a '$' op followed by the width token).
func putFormatToken(c *Cursor) (string, bool) {
	dollar := false
	if c.Peek().IsOp("$") && (c.PeekN(1).Kind == TIdent || c.PeekN(1).Kind == TNumber) &&
		strings.Contains(c.PeekN(1).Text, ".") {
		dollar = true
		c.Next()
	} else if !(c.Peek().Kind == TIdent || c.Peek().Kind == TNumber) ||
		!strings.Contains(c.Peek().Text, ".") {
		return "", false
	}
	tok := c.Next()
	f := "$" + tok.Text
	if !dollar {
		f = tok.Text
	}
	if ParseFormat(f) == nil {
		return "", false
	}
	return f, true
}

func (p *dsParser) parseError(s *Stmt) (dsNode, error) {
	put, err := p.parsePut(s)
	if err != nil {
		return nil, err
	}
	return dsFuncNode{fn: func(st *dsStep) dsCtl {
		st.setError()
		_ = put.exec(st)
		return dsNext
	}}, nil
}

// ---------------------------------------------------------------- CALL

type dsCallNode struct {
	name string
	args []Token // raw tokens; last arg may be an lvalue for output routines
	line int
}

func (n *dsCallNode) exec(st *dsStep) dsCtl {
	switch strings.ToUpper(n.name) {
	case "STREAMINIT":
		e, err := ParseExpr(n.args)
		if err == nil {
			st.rng.streamInit(e.Eval(st.ctx).AsNum())
		}
		return dsNext
	case "MISSING":
		if len(n.args) >= 2 && n.args[0].Kind == TIdent {
			v := st.lookup(n.args[0].Text)
			if v != nil {
				st.pdv[v.slot] = v.missing()
			}
		}
		return dsNext
	}
	// routines of the form name(seed..., out) or name(out)
	name := strings.ToUpper(n.name)
	argExprs := n.args
	lvalue := -1
	if len(argExprs) > 0 {
		lv := argExprs[len(argExprs)-1]
		if lv.Kind == TIdent && !strings.Contains(lv.Text, "(") {
			if v := st.lookup(lv.Text); v != nil {
				lvalue = v.slot
				argExprs = argExprs[:len(argExprs)-1]
			}
		}
	}
	vals := make([]Value, 0, len(argExprs))
	for _, a := range argExprs {
		e, err := ParseExpr([]Token{a})
		if err != nil {
			// try full expression (rare)
			continue
		}
		vals = append(vals, e.Eval(st.ctx))
	}
	switch {
	case strings.HasPrefix(name, "RANUNI"), strings.HasPrefix(name, "RANNOR"),
		strings.HasPrefix(name, "RANBIN"), strings.HasPrefix(name, "RANPOI"),
		strings.HasPrefix(name, "RANEXP"), strings.HasPrefix(name, "RANGAM"),
		strings.HasPrefix(name, "RANTBL"), name == "UNIFORM", name == "NORMAL":
		if len(vals) == 0 {
			return dsNext
		}
		seed := vals[0].AsNum()
		// SAS stream state: a seed VARIABLE holds its own updated state
		// (written back below); a seed CONSTANT shares one per-constant
		// stream across the DATA step.
		varSeed := len(n.args) > 0 && n.args[0].Kind == TIdent
		var tmp float64
		sp := &tmp
		if !varSeed {
			key := fmt.Sprintf("const:%d", int64(seed))
			sp = st.rng.stream(key, seed)
		} else {
			*sp = seed
		}
		var r float64
		switch name {
		case "RANUNI", "UNIFORM":
			r = st.rng.ranUni(sp)
		case "RANNOR", "NORMAL":
			r = st.rng.rannor(sp)
		case "RANEXP":
			r = st.rng.ranexp(sp)
		default:
			var args []float64
			for _, v := range vals[1:] {
				args = append(args, v.AsNum())
			}
			r = st.rng.ranFamily(name, sp, args)
		}
		seed = *sp
		// update seed variable if the first argument is a variable
		if len(n.args) > 0 && n.args[0].Kind == TIdent {
			if v := st.lookup(n.args[0].Text); v != nil {
				st.pdv[v.slot] = NumV(seed)
			}
		}
		if lvalue >= 0 {
			st.pdv[lvalue] = NumV(r)
		}
	case name == "SYMPUT" || name == "SYMPUTX":
		if len(vals) >= 2 {
			key := strings.ToUpper(strings.TrimSpace(vals[0].AsStr()))
			val := strings.TrimSpace(vals[1].AsStr())
			if name == "SYMPUT" {
				val = vals[1].AsStr()
			}
			st.s.Macros[key] = val
		}
	case name == "RANPERK" || name == "RANPERM":
		// not needed for the target programs
	}
	return dsNext
}

func (p *dsParser) parseCall(s *Stmt) (dsNode, error) {
	toks := s.Args()
	if len(toks) == 0 || toks[0].Kind != TIdent {
		return nil, dsErrf(s.Line, "Syntax error in CALL statement.")
	}
	name := toks[0].Text
	var args []Token
	if len(toks) > 2 && toks[1].IsOp("(") && toks[len(toks)-1].IsOp(")") {
		args = toks[2 : len(toks)-1]
	} else if len(toks) > 1 {
		args = toks[1:]
	}
	// split at top-level commas
	var parts [][]Token
	depth, cur := 0, []Token{}
	for _, t := range args {
		switch {
		case t.IsOp("("):
			depth++
			cur = append(cur, t)
		case t.IsOp(")"):
			depth--
			cur = append(cur, t)
		case t.IsOp(",") && depth == 0:
			parts = append(parts, cur)
			cur = nil
		default:
			cur = append(cur, t)
		}
	}
	if len(cur) > 0 {
		parts = append(parts, cur)
	}
	flat := []Token{}
	for i, part := range parts {
		if i > 0 {
			flat = append(flat, Token{Kind: TOp, Text: ","})
		}
		flat = append(flat, part...)
	}
	// evaluate is done per part at runtime; store parts as separate arg groups
	n := &dsCallNode{name: name, line: s.Line}
	// flatten but remember parts: we re-split at exec time via commas
	n.args = flat
	// type-fix variables used as lvalues
	for _, t := range parts {
		if len(t) == 1 && t[0].Kind == TIdent {
			v := p.st.lookup(t[0].Text)
			if v == nil {
				v = p.st.variable(t[0].Text)
				p.st.numeric(v, s.Line)
			}
			v.assigned = true
		}
	}
	return n, nil
}
