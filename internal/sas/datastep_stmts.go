package sas

import (
	"fmt"
	"strings"
)

// DATA step statement parser and statement nodes (ds prefix).

type dsParser struct {
	st     *dsStep
	stmts  []*Stmt
	i      int
	lastIf *dsIf
	inLoop int
}

// block parses statements until RUN/QUIT (top level) or END (inside DO).
func (p *dsParser) block(inDo bool) ([]dsNode, error) {
	var out []dsNode
	p.lastIf = nil
	for p.i < len(p.stmts) {
		s := p.stmts[p.i]
		kw := s.Keyword()
		switch kw {
		case "RUN", "QUIT":
			if inDo {
				return out, dsErrf(s.Line, "RUN statement is not valid inside a DO group.")
			}
			return out, nil
		case "END":
			if inDo {
				p.i++
				return out, nil
			}
			return out, dsErrf(s.Line, "END statement does not match a DO statement.")
		case "ELSE":
			if n := p.lastIf; n != nil && !n.hasEl {
				p.i++
				els, err := p.elseBranch(s)
				if err != nil {
					return out, err
				}
				n.els, n.hasEl = els, true
				continue
			}
			return out, dsErrf(s.Line, "ELSE statement does not match a previous IF statement.")
		}
		p.i++
		node, err := p.stmt(s, kw)
		if err != nil {
			return out, err
		}
		if node != nil {
			out = append(out, node)
		}
	}
	if inDo {
		return out, dsErrf(p.st.line, "DO group is missing an END statement.")
	}
	return out, nil
}

func (p *dsParser) stmt(s *Stmt, kw string) (dsNode, error) {
	st := p.st
	switch kw {
	case "":
		return nil, nil
	case "IF":
		return p.parseIf(s)
	case "ELSE":
		// attached in parseIf via lastIf
		return nil, nil
	case "DO":
		return p.parseDo(s)
	case "SELECT":
		return p.parseSelect(s)
	case "OUTPUT":
		return p.parseOutput(s)
	case "DELETE":
		return dsFuncNode{fn: func(st *dsStep) dsCtl { return dsDelete }}, nil
	case "STOP":
		return dsFuncNode{fn: func(st *dsStep) dsCtl { return dsStop }}, nil
	case "RETURN":
		return dsFuncNode{fn: func(st *dsStep) dsCtl { return dsReturn }}, nil
	case "LEAVE":
		return dsFuncNode{fn: func(st *dsStep) dsCtl { return dsLeave }}, nil
	case "CONTINUE":
		return dsFuncNode{fn: func(st *dsStep) dsCtl { return dsContinue }}, nil
	case "ABORT":
		return dsFuncNode{fn: func(st *dsStep) dsCtl {
			st.runtimeError("The ABORT statement was executed.")
			return dsAbort
		}}, nil
	case "ERROR":
		return p.parseError(s)
	case "PUT":
		return p.parsePut(s)
	case "CALL":
		return p.parseCall(s)
	case "RETAIN":
		return p.parseRetain(s)
	case "KEEP":
		st.keepToks = append(st.keepToks, s.Args())
		return nil, nil
	case "DROP":
		st.dropToks = append(st.dropToks, s.Args())
		return nil, nil
	case "RENAME":
		st.renames = append(st.renames, dsParseRenames(s.Args())...)
		return nil, nil
	case "LENGTH":
		return p.parseLength(s)
	case "LABEL":
		p.parseLabels(s)
		return nil, nil
	case "FORMAT", "INFORMAT":
		p.parseFormats(s, kw == "INFORMAT")
		return nil, nil
	case "ATTRIB":
		return p.parseAttrib(s)
	case "ARRAY":
		return p.parseArray(s)
	case "WHERE":
		e, err := ParseWhere(s.Args())
		if err != nil {
			return nil, dsErrf(s.Line, "%v", err)
		}
		st.wheres = append(st.wheres, e)
		return nil, nil
	case "BY":
		if st.byPending == nil {
			return nil, dsErrf(s.Line, "BY statement does not follow a SET or MERGE statement.")
		}
		st.byPending.by = s.Args()
		return nil, nil
	case "MERGE", "SET", "INFILE", "INPUT", "UPDATE", "MODIFY":
		return p.parseIO(s, kw)
	case "DATALINES", "CARDS", "LINES", "DATALINES4", "CARDS4", "LINES4":
		return nil, nil
	}
	// sum statement: var + expr;
	if len(s.Toks) >= 3 && s.Toks[0].Kind == TIdent && s.Toks[1].IsOp("+") && !s.Toks[2].IsOp("+") {
		v := st.variable(s.Toks[0].Text)
		if !v.typed {
			st.numeric(v, s.Line)
		}
		v.retain = true
		v.assigned = true
		if v.init == nil {
			zero := NumV(0)
			v.init = &zero
		}
		e, err := p.expr(s.Toks[2:], s.Line)
		if err != nil {
			return nil, err
		}
		return &dsSumNode{v: v, expr: e, line: s.Line}, nil
	}
	// assignment
	if node, ok, err := p.parseAssign(s); err != nil {
		return nil, err
	} else if ok {
		return node, nil
	}
	return nil, dsErrf(s.Line, "Statement %s is not valid or it is used out of proper order.", kw)
}

// dsFuncNode runs a closure.
type dsFuncNode struct {
	fn func(st *dsStep) dsCtl
}

func (n dsFuncNode) exec(st *dsStep) dsCtl { return n.fn(st) }

// ---------------------------------------------------------------- expressions

// dsExpr wraps an expression with compile-time type tracking.
type dsExpr struct {
	e    Expr
	line int
}

func (p *dsParser) expr(toks []Token, line int) (*dsExpr, error) {
	e, err := ParseExpr(toks)
	if err != nil {
		return nil, dsErrf(line, "%v", err)
	}
	// compile-time type resolution for variables and functions
	st := p.st
	dsTypeExpr(st, e, line)
	st.collectConv(e, line)
	return &dsExpr{e: e, line: line}, nil
}

func (x *dsExpr) eval(st *dsStep) Value {
	miss0, inv0 := st.ctx.MissingOps, st.ctx.InvalidOps
	st.ctx.Line = x.line
	v := x.e.Eval(st.ctx)
	st.track(x.line, miss0, inv0)
	return v
}

// dsTypeExpr fixes variable types implied by expression context.
func dsTypeExpr(st *dsStep, e Expr, line int) {
	switch n := e.(type) {
	case *VarRef:
		v := st.variable(n.Name)
		if !v.typed {
			// numeric by default when used as a value
			st.numeric(v, line)
		}
	case *Unary:
		dsTypeExpr(st, n.X, line)
	case *Binary:
		dsTypeExpr(st, n.L, line)
		dsTypeExpr(st, n.R, line)
	case *InExpr:
		dsTypeExpr(st, n.X, line)
	case *Call:
		if n.HasOf {
			dsTypeOfList(st, n.OfToks, line)
		}
		for _, a := range n.Args {
			dsTypeExpr(st, a, line)
		}
		// functions that require numeric args and return numeric
		dsFuncType(st, n.Name, len(n.Args), line)
	}
}

func dsTypeOfList(st *dsStep, toks []Token, line int) {
	for _, n := range VarNames(toks) {
		if a := st.arrays[strings.ToUpper(n)]; a != nil {
			for _, v := range a.vars {
				st.numeric(v, line) // may be char; refine at finalize
			}
			continue
		}
		if st.lookup(n) == nil {
			st.numeric(st.variable(n), line)
		}
	}
}

// dsFuncType declares return types of known functions.
func dsFuncType(st *dsStep, name string, nargs int, line int) {
	switch name {
	case "CAT", "CATS", "CATT", "CATX", "UPCASE", "LOWCASE", "PROPCASE", "TRIM", "TRIMN",
		"STRIP", "LEFT", "RIGHT", "SUBSTR", "SUBSTRN", "SCAN", "COMPRESS", "TRANWRD",
		"TRANSTRN", "TRANSLATE", "REVERSE", "REPEAT", "PUT", "PUTN", "PUTC", "IFC",
		"BYTE", "COALESCEC":
		// string-returning; args stay as written
	case "SYMGET":
	default:
		// numeric function: numeric args
	}
}

// ---------------------------------------------------------------- IF / DO / SELECT

type dsIf struct {
	cond  *dsExpr
	then  []dsNode
	els   []dsNode
	hasEl bool
	line  int
}

func (n *dsIf) exec(st *dsStep) dsCtl {
	if n.cond.eval(st).Truth() {
		if c := st.execBlock(n.then); c != dsNext {
			return c
		}
		return dsNext
	}
	if n.hasEl {
		if c := st.execBlock(n.els); c != dsNext {
			return c
		}
	}
	return dsNext
}

func (p *dsParser) parseIf(s *Stmt) (dsNode, error) {
	toks := s.Args()
	// split at THEN (splitKw returns everything before, and after)
	condToks, thenToks := splitKw(toks, "THEN")
	if len(thenToks) == 0 {
		// subsetting IF (no THEN)
		e, err := p.expr(condToks, s.Line)
		if err != nil {
			return nil, err
		}
		return dsFuncNode{fn: func(st *dsStep) dsCtl {
			if !e.eval(st).Truth() {
				return dsDelete
			}
			return dsNext
		}}, nil
	}
	var then []dsNode
	pendingDo := false
	if len(thenToks) == 1 && thenToks[0].Is("DO") {
		pendingDo = true
	} else if len(thenToks) >= 1 && thenToks[0].Is("DO") {
		// iterative do inside if (rare): treat as do with spec
		node, err := p.parseDo(&Stmt{Toks: append([]Token{{Kind: TIdent, Text: "DO", Line: s.Line}}, thenToks[1:]...), Line: s.Line})
		if err != nil {
			return nil, err
		}
		then = []dsNode{node}
	} else {
		node, err := p.embedded(thenToks, s.Line)
		if err != nil {
			return nil, err
		}
		then = []dsNode{node}
	}
	n := &dsIf{cond: nil, then: then, line: s.Line}
	e, err := p.expr(condToks, s.Line)
	if err != nil {
		return nil, err
	}
	n.cond = e
	p.lastIf = n
	if pendingDo {
		body, err := p.block(true)
		if err != nil {
			return nil, err
		}
		n.then = body
		// an ELSE may follow the END
		p.attachElse(n)
	}
	return n, nil
}

// attachElse consumes a following ELSE statement for the given IF.
func (p *dsParser) attachElse(n *dsIf) {
	if p.i < len(p.stmts) && p.stmts[p.i].Keyword() == "ELSE" {
		es := p.stmts[p.i]
		p.i++
		els, err := p.elseBranch(es)
		if err != nil {
			return
		}
		n.els, n.hasEl = els, true
	}
}

func (p *dsParser) elseBranch(es *Stmt) ([]dsNode, error) {
	toks := es.Args()
	if len(toks) == 1 && toks[0].Is("DO") {
		return p.block(true)
	}
	if len(toks) >= 1 && toks[0].Is("IF") {
		node, err := p.parseIf(&Stmt{Toks: append([]Token{{Kind: TIdent, Text: "IF", Line: es.Line}}, toks[1:]...), Line: es.Line})
		if err != nil {
			return nil, err
		}
		return []dsNode{node}, nil
	}
	node, err := p.embedded(toks, es.Line)
	if err != nil {
		return nil, err
	}
	return []dsNode{node}, nil
}

// splitKw splits tokens at the first identifier kw (returned second).
func splitKw(toks []Token, kw string) (before, after []Token) {
	for i, t := range toks {
		if t.Is(kw) {
			return toks[:i], toks[i+1:]
		}
	}
	return toks, nil
}

// embedded parses a single statement from tokens (THEN/ELSE branch).
func (p *dsParser) embedded(toks []Token, line int) (dsNode, error) {
	if len(toks) == 0 {
		return nil, dsErrf(line, "Expected a statement.")
	}
	s := &Stmt{Toks: toks, Line: line}
	kw := s.Keyword()
	switch kw {
	case "DELETE":
		return dsFuncNode{fn: func(st *dsStep) dsCtl { return dsDelete }}, nil
	case "STOP":
		return dsFuncNode{fn: func(st *dsStep) dsCtl { return dsStop }}, nil
	case "RETURN":
		return dsFuncNode{fn: func(st *dsStep) dsCtl { return dsReturn }}, nil
	case "LEAVE":
		return dsFuncNode{fn: func(st *dsStep) dsCtl { return dsLeave }}, nil
	case "CONTINUE":
		return dsFuncNode{fn: func(st *dsStep) dsCtl { return dsContinue }}, nil
	case "OUTPUT":
		return p.parseOutput(s)
	case "CALL":
		return p.parseCall(s)
	case "ERROR":
		return p.parseError(s)
	case "PUT":
		return p.parsePut(s)
	}
	// sum statement: var + expr; (valid as a THEN/ELSE action in SAS)
	if st := p.st; st != nil {
		if len(toks) >= 3 && toks[0].Kind == TIdent && toks[1].IsOp("+") && !toks[2].IsOp("+") {
			v := st.variable(toks[0].Text)
			if !v.typed {
				st.numeric(v, line)
			}
			v.retain = true
			v.assigned = true
			if v.init == nil {
				zero := NumV(0)
				v.init = &zero
			}
			e, err := p.expr(toks[2:], line)
			if err != nil {
				return nil, err
			}
			return &dsSumNode{v: v, expr: e, line: line}, nil
		}
	}
	if node, ok, err := p.parseAssign(s); err != nil {
		return nil, err
	} else if ok {
		return node, nil
	}
	return nil, dsErrf(line, "Statement is not valid in this context.")
}

type dsDo struct {
	varv  *dsVar
	items []dsDoItem
	while *dsExpr
	until *dsExpr
	body  []dsNode
	line  int
}

type dsDoItem struct {
	start, to, by *dsExpr
	simple        *dsExpr // value list item without TO
}

func (n *dsDo) exec(st *dsStep) dsCtl {
	// start/to/by are evaluated once at loop entry (SAS semantics)
	var vals []float64
	for _, it := range n.items {
		if it.simple != nil {
			vals = append(vals, it.simple.eval(st).AsNum())
			continue
		}
		cur := it.start.eval(st).AsNum()
		if it.to == nil {
			vals = append(vals, cur)
			continue
		}
		to := it.to.eval(st).AsNum()
		by := 1.0
		if it.by != nil {
			by = it.by.eval(st).AsNum()
		}
		if by == 0 {
			by = 1
		}
		for c := cur; (by > 0 && c <= to+1e-12) || (by < 0 && c >= to-1e-12); c += by {
			vals = append(vals, c)
			if len(vals) > 1e8 {
				st.runtimeError("DO loop exceeded 1e8 iterations.")
				return dsAbort
			}
		}
	}
	if n.varv != nil && len(vals) > 0 {
		st.store(n.varv, NumV(vals[0]))
	}
	idx := 0
	for len(vals) > 0 {
		if n.while != nil && !n.while.eval(st).Truth() {
			return dsNext
		}
		c := st.execBlock(n.body)
		if c == dsLeave {
			return dsNext
		}
		if c != dsNext && c != dsContinue {
			return c
		}
		if n.until != nil && n.until.eval(st).Truth() {
			return dsNext
		}
		idx++
		if idx >= len(vals) {
			return dsNext
		}
		if n.varv != nil {
			st.store(n.varv, NumV(vals[idx]))
		}
	}
	return dsNext
}

func (p *dsParser) parseDo(s *Stmt) (dsNode, error) {
	n := &dsDo{line: s.Line}
	toks := s.Args()
	if len(toks) > 0 {
		// iterative DO: var = list [WHILE(expr)] [UNTIL(expr)]
		if toks[0].Kind != TIdent {
			return nil, dsErrf(s.Line, "Syntax error in DO statement.")
		}
		v := toks[0].Text
		rest := toks[1:]
		if len(rest) == 0 || !rest[0].IsOp("=") {
			return nil, dsErrf(s.Line, "Syntax error in DO statement, expected '='.")
		}
		rest = rest[1:]
		// split at WHILE/UNTIL
		var spec, trailer []Token
		for i, t := range rest {
			if t.Is("WHILE", "UNTIL") {
				spec, trailer = rest[:i], rest[i:]
				break
			}
		}
		if spec == nil {
			spec = rest
		}
		n.varv = p.st.variable(v)
		p.st.numeric(n.varv, s.Line)
		n.varv.assigned = true
		items, err := p.parseDoItems(spec, s.Line)
		if err != nil {
			return nil, err
		}
		n.items = items
		c := NewCursor(trailer)
		for !c.AtEnd() {
			if c.AcceptKw("WHILE") {
				g, _ := c.Group()
				e, err := p.expr(g, s.Line)
				if err != nil {
					return nil, err
				}
				n.while = e
			} else if c.AcceptKw("UNTIL") {
				g, _ := c.Group()
				e, err := p.expr(g, s.Line)
				if err != nil {
					return nil, err
				}
				n.until = e
			} else {
				c.Next()
			}
		}
	}
	body, err := p.block(true)
	if err != nil {
		return nil, err
	}
	n.body = body
	// set var to first value before entering (SAS semantics)
	return n, nil
}

// parseDoItems parses "0.1, 0.5, 0.7" or "1 to 10 by 2, 20, 30 to 40".
func (p *dsParser) parseDoItems(toks []Token, line int) ([]dsDoItem, error) {
	var items []dsDoItem
	// split at top-level commas
	var parts [][]Token
	depth := 0
	cur := []Token{}
	for _, t := range toks {
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
	for _, part := range parts {
		// find TO / BY at this level
		var startT, toT, byT []Token
		rest := part
		i := 0
		for i < len(rest) {
			if rest[i].Is("TO") {
				startT, rest = rest[:i], rest[i+1:]
				break
			}
			i++
		}
		if startT == nil && rest != nil {
			startT = rest
			rest = nil
		}
		j := 0
		for j < len(rest) {
			if rest[j].Is("BY") {
				toT, rest = rest[:j], rest[j+1:]
				break
			}
			j++
		}
		if toT == nil && rest != nil {
			toT = rest
			rest = nil
		}
		byT = rest
		var e0, e1, e2 *dsExpr
		var err error
		if e0, err = p.expr(startT, line); err != nil {
			return nil, err
		}
		it := dsDoItem{start: e0}
		if toT != nil {
			if e1, err = p.expr(toT, line); err != nil {
				return nil, err
			}
			it.to = e1
		}
		if byT != nil {
			if e2, err = p.expr(byT, line); err != nil {
				return nil, err
			}
			it.by = e2
		}
		if it.to == nil && it.by == nil {
			it.simple = e0
			it.start = nil
		}
		items = append(items, it)
	}
	return items, nil
}

type dsSelect struct {
	val       *dsExpr // nil = searched
	whens     []dsWhen
	otherwise []dsNode
	line      int
}
type dsWhen struct {
	vals []*dsExpr
	body []dsNode
}

func (n *dsSelect) exec(st *dsStep) dsCtl {
	var x Value
	if n.val != nil {
		x = n.val.eval(st)
	}
	for _, w := range n.whens {
		match := false
		for _, v := range w.vals {
			if n.val != nil {
				if Equal(x, v.eval(st)) {
					match = true
				}
			} else if v.eval(st).Truth() {
				match = true
			}
			if match {
				break
			}
		}
		if match {
			if c := st.execBlock(w.body); c != dsNext {
				return c
			}
			return dsNext
		}
	}
	if n.otherwise != nil {
		if c := st.execBlock(n.otherwise); c != dsNext {
			return c
		}
	}
	return dsNext
}

func (p *dsParser) parseSelect(s *Stmt) (dsNode, error) {
	n := &dsSelect{line: s.Line}
	toks := s.Args()
	if len(toks) > 0 {
		e, err := p.expr(toks, s.Line)
		if err != nil {
			return nil, err
		}
		n.val = e
	}
	for p.i < len(p.stmts) {
		ws := p.stmts[p.i]
		kw := ws.Keyword()
		switch kw {
		case "WHEN":
			p.i++
			toks := ws.Args()
			var vals []*dsExpr
			var body []dsNode
			if g, ok := NewCursor(toks).Group(); false {
				_ = g
				_ = ok
			}
			c := NewCursor(toks)
			if c.Peek().IsOp("(") {
				g, _ := c.Group()
				// value list separated by commas
				depth, cur := 0, []Token{}
				var parts [][]Token
				for _, t := range g {
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
				for _, pt := range parts {
					e, err := p.expr(pt, ws.Line)
					if err != nil {
						return nil, err
					}
					vals = append(vals, e)
				}
			} else {
				e, err := p.expr(toks, ws.Line)
				if err != nil {
					return nil, err
				}
				vals = append(vals, e)
			}
			// body: remainder tokens or following DO..END
			rest := c.Rest()
			if len(rest) == 1 && rest[0].Is("DO") {
				b, err := p.block(true)
				if err != nil {
					return nil, err
				}
				body = b
			} else if len(rest) > 0 {
				nd, err := p.embedded(rest, ws.Line)
				if err != nil {
					return nil, err
				}
				body = []dsNode{nd}
			}
			n.whens = append(n.whens, dsWhen{vals: vals, body: body})
		case "OTHERWISE":
			p.i++
			rest := ws.Args()
			if len(rest) == 0 {
				continue
			}
			if len(rest) == 1 && rest[0].Is("DO") {
				b, err := p.block(true)
				if err != nil {
					return nil, err
				}
				n.otherwise = b
			} else {
				nd, err := p.embedded(rest, ws.Line)
				if err != nil {
					return nil, err
				}
				n.otherwise = []dsNode{nd}
			}
		case "END":
			p.i++
			return n, nil
		default:
			return nil, dsErrf(ws.Line, "Expecting WHEN or OTHERWISE in SELECT statement.")
		}
	}
	return nil, dsErrf(s.Line, "SELECT group is missing an END statement.")
}

// ---------------------------------------------------------------- simple statements

type dsOutputNode struct {
	targets []string
	line    int
}

func (n *dsOutputNode) exec(st *dsStep) dsCtl {
	var ts []*dsOut
	if len(n.targets) > 0 {
		for _, name := range n.targets {
			o := st.findOut(name)
			if o == nil {
				st.runtimeError(fmt.Sprintf("Data set %s is not one of the output data sets of this step.", DisplayName(name)))
				return dsAbort
			}
			ts = append(ts, o)
		}
	}
	st.output(ts)
	st.hasOutput = true
	return dsNext
}

func (p *dsParser) parseOutput(s *Stmt) (dsNode, error) {
	n := &dsOutputNode{line: s.Line}
	// SAS: the presence of an OUTPUT statement (compile time) suppresses
	// the implicit end-of-iteration output for the whole step
	if p.st != nil {
		p.st.hasOutput = true
	}
	for _, t := range s.Args() {
		if t.Kind == TIdent {
			n.targets = append(n.targets, t.Text)
		}
	}
	return n, nil
}

type dsAssignNode struct {
	v    *dsVar
	arr  *dsArray
	idx  *dsExpr
	expr *dsExpr
	line int
}

func (n *dsAssignNode) exec(st *dsStep) dsCtl {
	x := n.expr.eval(st)
	if n.arr != nil {
		i := int(n.idx.eval(st).AsNum())
		v, ok := n.arr.elem(st, i)
		if !ok {
			st.runtimeError(fmt.Sprintf("Array subscript %d out of range for array %s.", i, n.arr.name))
			return dsAbort
		}
		st.store(v, x)
		return dsNext
	}
	st.store(n.v, x)
	return dsNext
}

type dsSumNode struct {
	v    *dsVar
	expr *dsExpr
	line int
}

func (n *dsSumNode) exec(st *dsStep) dsCtl {
	cur := st.pdv[n.v.slot].AsNum()
	add := n.expr.eval(st).AsNum()
	if math_IsNaN(cur) {
		cur = 0
	}
	if !math_IsNaN(add) {
		cur += add
	}
	st.pdv[n.v.slot] = NumV(cur)
	return dsNext
}

// math_IsNaN avoids importing math here.
func math_IsNaN(f float64) bool { return f != f }

func dsParseRenames(toks []Token) [][2]string {
	var out [][2]string
	c := NewCursor(toks)
	for !c.AtEnd() {
		from := c.Next()
		if !c.AcceptOp("=") {
			continue
		}
		to := c.Next()
		out = append(out, [2]string{from.Text, to.Text})
	}
	return out
}

func (p *dsParser) parseAssign(s *Stmt) (dsNode, bool, error) {
	// the statement's first token is the assignment target (or an array ref)
	toks := s.Toks
	if len(toks) < 3 {
		return nil, false, nil
	}
	// find top-level '='
	depth := 0
	eq := -1
	for i, t := range toks {
		switch {
		case t.IsOp("("):
			depth++
		case t.IsOp(")"):
			depth--
		case t.IsOp("=") && depth == 0:
			eq = i
		}
		if eq >= 0 {
			break
		}
	}
	if eq < 1 {
		return nil, false, nil
	}
	lhs, rhs := toks[:eq], toks[eq+1:]
	e, err := p.expr(rhs, s.Line)
	if err != nil {
		return nil, true, err
	}
	if lhs[0].Kind != TIdent {
		return nil, false, nil
	}
	if len(lhs) == 1 {
		v := p.st.variable(lhs[0].Text)
		p.st.defineFromValue(v, e, s.Line)
		v.assigned = true
		if ec, _ := p.st.cType(e.e); ec != v.Char {
			if v.Char {
				p.st.convN2C = append(p.st.convN2C, s.Line)
			} else {
				p.st.convC2N = append(p.st.convC2N, s.Line)
			}
		}
		return &dsAssignNode{v: v, expr: e, line: s.Line}, true, nil
	}
	// array element target: arr{expr}
	name := lhs[0].Text
	a := p.st.arrays[strings.ToUpper(name)]
	if a == nil {
		// substr target: substr(var,pos)=expr
		if strings.EqualFold(name, "SUBSTR") || strings.EqualFold(name, "SUBSTRN") {
			return p.parseSubstrAssign(lhs, e, s)
		}
		return nil, true, dsErrf(s.Line, "Array %s is not defined.", strings.ToUpper(name))
	}
	if len(lhs) < 4 || !lhs[1].IsOp("{", "[", "(") {
		return nil, true, dsErrf(s.Line, "Syntax error in array reference.")
	}
	inner := lhs[2 : len(lhs)-1]
	idx, err := p.expr(inner, s.Line)
	if err != nil {
		return nil, true, err
	}
	for _, v := range a.vars {
		v.assigned = true
	}
	return &dsAssignNode{arr: a, idx: idx, expr: e, line: s.Line}, true, nil
}

func (p *dsParser) parseSubstrAssign(lhs []Token, e *dsExpr, s *Stmt) (dsNode, bool, error) {
	// lhs = [substr ( var , pos [, len] )] with possible inner nesting for pos
	st := p.st
	c := NewCursor(lhs[2 : len(lhs)-1])
	vname, err := c.ExpectIdent()
	if err != nil {
		return nil, true, err
	}
	if !c.AcceptOp(",") {
		return nil, true, dsErrf(s.Line, "Syntax error in SUBSTR pseudo-variable.")
	}
	var posT []Token
	depth := 0
	for !c.AtEnd() {
		t := c.Peek()
		if t.IsOp("(") {
			depth++
		}
		if t.IsOp(")") {
			depth--
		}
		if t.IsOp(",") && depth == 0 {
			break
		}
		posT = append(posT, c.Next())
	}
	pos, err := p.expr(posT, s.Line)
	if err != nil {
		return nil, true, err
	}
	v := st.variable(vname)
	if !v.typed {
		v.Char = true
		v.typed = true
	}
	if !v.Char {
		return nil, true, dsErrf(s.Line, "SUBSTR pseudo-variable requires a character variable.")
	}
	v.assigned = true
	return dsFuncNode{fn: func(st *dsStep) dsCtl {
		x := e.eval(st)
		p := int(pos.eval(st).AsNum())
		sv := strings.TrimRight(st.pdv[v.slot].Str, " ")
		if p < 1 || p > len(sv) {
			st.runtimeError(fmt.Sprintf("Invalid SUBSTR position %d for variable %s.", p, v.Name))
			return dsAbort
		}
		end := len(sv)
		val := strings.TrimRight(x.AsStr(), " ")
		if p-1+len(val) < end {
			end = p - 1 + len(val)
		}
		st.store(v, StrV(sv[:p-1]+val+sv[end:]))
		return dsNext
	}}, true, nil
}

// defineFromValue fixes a variable's type and length from the expression
// being assigned at its first reference (SAS sets character lengths at
// compile time from the first assignment; LENGTH etc. have already won).
func (st *dsStep) defineFromValue(v *dsVar, e *dsExpr, line int) {
	if v.typed {
		return
	}
	char, l := st.cType(e.e)
	if char {
		st.define(v, true, l, line)
	} else {
		st.numeric(v, line)
	}
}

// cType is the compile-time type and length of an expression, following the
// DATA step compiler: string literals keep their exact length (trailing
// blanks included), || concatenates operand lengths (numeric operands
// contribute the 12 characters of the automatic BEST12. conversion), most
// string functions return their argument's length, the CAT* family and
// friends default to 200, and a first reference to an unknown variable is
// numeric.
func (st *dsStep) cType(e Expr) (bool, int) {
	switch n := e.(type) {
	case *StrLit:
		return true, max(len(n.S), 1)
	case *NumLit:
		return false, 8
	case *VarRef:
		if v := st.lookup(n.Name); v != nil && v.Char {
			return true, v.Len
		}
		return false, 8
	case *Unary:
		return false, 8
	case *Binary:
		if n.Op == "||" {
			lc, ll := st.cType(n.L)
			rc, lr := st.cType(n.R)
			if !lc {
				ll = 12
			}
			if !rc {
				lr = 12
			}
			return true, min(ll+lr, 32767)
		}
		return false, 8
	case *Call:
		return st.callCType(n)
	}
	return false, 8
}

func (st *dsStep) callCType(n *Call) (bool, int) {
	first := 8
	if len(n.Args) > 0 {
		if c, l := st.cType(n.Args[0]); c {
			first = l
		}
	}
	switch n.Name {
	case "TRIM", "TRIMN", "LEFT", "RIGHT", "UPCASE", "LOWCASE", "PROPCASE",
		"STRIP", "REVERSE", "COMPRESS", "TRANSLATE", "TRANSTRN", "SOUNDEX",
		"SUBSTR", "QUOTE", "COALESCEC":
		return true, first
	case "CAT", "CATS", "CATT", "CATX", "CATQ", "SCAN", "TRANWRD", "REPEAT",
		"SUBSTRN", "SYMGET", "RESOLVE", "IFC", "CHOOSEC", "PATHNAME":
		return true, 200
	case "BYTE":
		return true, 1
	case "PUT", "PUTN", "PUTC":
		if len(n.Args) >= 2 {
			if lit, ok := n.Args[1].(*StrLit); ok {
				if f := ParseFormat(lit.S); f != nil && f.HasW {
					return true, f.W
				}
			}
		}
		return true, 200
	}
	return false, 8
}

// collectConv records the conversion places a step reports at compile time:
// numeric operands of || convert to character; the character side of an
// arithmetic operation or mixed-type comparison converts to numeric; and an
// assignment whose value type differs from the variable converts too.
func (st *dsStep) collectConv(e Expr, line int) {
	switch n := e.(type) {
	case *Unary:
		if n.Op == "-" || n.Op == "+" {
			if c, _ := st.cType(n.X); c {
				st.convC2N = append(st.convC2N, line)
			}
		}
		st.collectConv(n.X, line)
	case *Binary:
		st.collectConv(n.L, line)
		st.collectConv(n.R, line)
		switch n.Op {
		case "||":
			if c, _ := st.cType(n.L); !c {
				st.convN2C = append(st.convN2C, line)
			}
			if c, _ := st.cType(n.R); !c {
				st.convN2C = append(st.convN2C, line)
			}
		case "=", "NE", "<", "<=", ">", ">=", "=:", "><", "<>",
			"+", "-", "*", "/", "**", "MIN", "MAX":
			lc, _ := st.cType(n.L)
			rc, _ := st.cType(n.R)
			if lc != rc {
				st.convC2N = append(st.convC2N, line)
			}
		}
	case *Call:
		for _, a := range n.Args {
			st.collectConv(a, line)
		}
	case *InExpr:
		st.collectConv(n.X, line)
		for _, v := range n.List {
			st.collectConv(v, line)
		}
	case *LikeExpr:
		st.collectConv(n.X, line)
		st.collectConv(n.Pat, line)
	case *ContainsExpr:
		st.collectConv(n.X, line)
		st.collectConv(n.Y, line)
	}
}
