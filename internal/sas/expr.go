package sas

import (
	"fmt"
	"math"
	"strings"
)

// Expr is a parsed SAS expression.
type Expr interface {
	Eval(c *EvalCtx) Value
}

// EvalCtx supplies variable values and hooks during evaluation.
type EvalCtx struct {
	Sess *Session
	Line int
	// Get returns a variable's value (ok=false: unknown variable -> numeric missing).
	Get func(name string) (Value, bool)
	// Hook intercepts calls before the function table (arrays, DIM, RNG ...).
	// Return handled=false to fall through.
	Hook func(c *EvalCtx, call *Call, args []Expr) (v Value, handled bool)
	// ExpandOf expands an OF variable list (e.g. OF x1-x5, OF arr[*]) into values.
	ExpandOf func(toks []Token) []Value
	// State stores per-call-site state (e.g. random number streams).
	State map[*Call]any

	MissingOps int // "Missing values were generated" counter
	InvalidOps int // invalid argument / division by zero counter
	ConvNotes  int // char<->num conversion counter
}

func (c *EvalCtx) get(name string) Value {
	if c.Get != nil {
		if v, ok := c.Get(name); ok {
			return v
		}
	}
	return Missing
}

// ---------------------------------------------------------------- nodes

type NumLit struct{ V float64 }
type StrLit struct{ S string }
type VarRef struct{ Name string }

// Call is a function call or array reference: name(args) / name{args} / name[args].
type Call struct {
	Name    string // upper case
	Args    []Expr
	Braces  bool // written with {} or []
	OfToks  []Token
	HasOf   bool
	RawName string
}
type Unary struct {
	Op string
	X  Expr
}
type Binary struct {
	Op   string
	L, R Expr
}
type InExpr struct {
	X    Expr
	List []Expr
	Rng  [][2]Expr // numeric ranges lo:hi
	Not  bool
}

// Special predicate nodes (WHERE syntax, also allowed in DATA step for convenience).
type BetweenExpr struct {
	X, Lo, Hi Expr
	Not       bool
}
type IsMissingExpr struct {
	X   Expr
	Not bool
}
type LikeExpr struct {
	X   Expr
	Pat Expr
	Not bool
}
type ContainsExpr struct {
	X, Y Expr
	Not  bool
}

func (n *NumLit) Eval(*EvalCtx) Value   { return NumV(n.V) }
func (n *StrLit) Eval(*EvalCtx) Value   { return StrV(n.S) }
func (n *VarRef) Eval(c *EvalCtx) Value { return c.get(n.Name) }

func (n *Unary) Eval(c *EvalCtx) Value {
	x := n.X.Eval(c)
	switch n.Op {
	case "-":
		f := toNum(c, x)
		if math.IsNaN(f) {
			return Missing
		}
		return NumV(-f)
	case "+":
		return NumV(toNum(c, x))
	case "NOT":
		return BoolV(!x.Truth())
	}
	return Missing
}

func toNum(c *EvalCtx, v Value) float64 {
	if v.Char {
		c.ConvNotes++
		return v.AsNum()
	}
	return v.Num
}

// numToCharAuto is SAS's automatic numeric->character conversion (BEST12. right-aligned).
func numToCharAuto(f float64) string {
	s := formatBest(f, 12)
	if len(s) < 12 {
		s = strings.Repeat(" ", 12-len(s)) + s
	}
	return s
}

func (n *Binary) Eval(c *EvalCtx) Value {
	switch n.Op {
	case "AND":
		l := n.L.Eval(c)
		if !l.Truth() {
			// SAS evaluates both sides but result is false
			return BoolV(false)
		}
		return BoolV(n.R.Eval(c).Truth())
	case "OR":
		l := n.L.Eval(c)
		if l.Truth() {
			return BoolV(true)
		}
		return BoolV(n.R.Eval(c).Truth())
	}
	l, r := n.L.Eval(c), n.R.Eval(c)
	switch n.Op {
	case "||":
		ls, rs := l.Str, r.Str
		if !l.Char {
			ls = numToCharAuto(l.Num)
			c.ConvNotes++
		}
		if !r.Char {
			rs = numToCharAuto(r.Num)
			c.ConvNotes++
		}
		return StrV(ls + rs)
	case "=", "NE", "<", "<=", ">", ">=", "=:":
		return BoolV(compareOp(c, n.Op, l, r))
	}
	a, b := toNum(c, l), toNum(c, r)
	switch n.Op {
	case "MIN":
		if math.IsNaN(a) || (!math.IsNaN(b) && b < a) {
			return NumV(b)
		}
		return NumV(a)
	case "MAX":
		if math.IsNaN(a) || (!math.IsNaN(b) && b > a) {
			return NumV(b)
		}
		return NumV(a)
	}
	if math.IsNaN(a) || math.IsNaN(b) {
		c.MissingOps++
		return Missing
	}
	switch n.Op {
	case "+":
		return NumV(a + b)
	case "-":
		return NumV(a - b)
	case "*":
		return NumV(a * b)
	case "/":
		if b == 0 {
			c.InvalidOps++
			return Missing
		}
		return NumV(a / b)
	case "**":
		v := math.Pow(a, b)
		if math.IsNaN(v) || math.IsInf(v, 0) {
			c.InvalidOps++
			return Missing
		}
		return NumV(v)
	}
	return Missing
}

func compareOp(c *EvalCtx, op string, l, r Value) bool {
	var cmp int
	if l.Char && r.Char {
		ls, rs := strings.TrimRight(l.Str, " "), strings.TrimRight(r.Str, " ")
		if op == "=:" {
			m := len(ls)
			if len(rs) < m {
				m = len(rs)
			}
			ls, rs = ls[:m], rs[:m]
			op = "="
		}
		cmp = strings.Compare(ls, rs)
	} else {
		if op == "=:" {
			op = "="
		}
		cmp = Compare(NumV(toNum(c, l)), NumV(toNum(c, r)))
	}
	switch op {
	case "=":
		return cmp == 0
	case "NE":
		return cmp != 0
	case "<":
		return cmp < 0
	case "<=":
		return cmp <= 0
	case ">":
		return cmp > 0
	case ">=":
		return cmp >= 0
	}
	return false
}

func (n *InExpr) Eval(c *EvalCtx) Value {
	x := n.X.Eval(c)
	found := false
	for _, e := range n.List {
		if compareOp(c, "=", x, e.Eval(c)) {
			found = true
			break
		}
	}
	if !found {
		for _, rg := range n.Rng {
			f := toNum(c, x)
			lo, hi := toNum(c, rg[0].Eval(c)), toNum(c, rg[1].Eval(c))
			if f >= lo && f <= hi {
				found = true
				break
			}
		}
	}
	return BoolV(found != n.Not)
}

func (n *BetweenExpr) Eval(c *EvalCtx) Value {
	x := n.X.Eval(c)
	in := compareOp(c, ">=", x, n.Lo.Eval(c)) && compareOp(c, "<=", x, n.Hi.Eval(c))
	return BoolV(in != n.Not)
}

func (n *IsMissingExpr) Eval(c *EvalCtx) Value {
	return BoolV(n.X.Eval(c).IsMissing() != n.Not)
}

func (n *ContainsExpr) Eval(c *EvalCtx) Value {
	x, y := n.X.Eval(c), n.Y.Eval(c)
	return BoolV(strings.Contains(x.AsStr(), strings.TrimRight(y.AsStr(), " ")) != n.Not)
}

func (n *LikeExpr) Eval(c *EvalCtx) Value {
	x := strings.TrimRight(n.X.Eval(c).AsStr(), " ")
	p := strings.TrimRight(n.Pat.Eval(c).AsStr(), " ")
	return BoolV(likeMatch(x, p) != n.Not)
}

func likeMatch(s, p string) bool {
	if p == "" {
		return s == ""
	}
	switch p[0] {
	case '%':
		for i := 0; i <= len(s); i++ {
			if likeMatch(s[i:], p[1:]) {
				return true
			}
		}
		return false
	case '_':
		return s != "" && likeMatch(s[1:], p[1:])
	}
	return s != "" && s[0] == p[0] && likeMatch(s[1:], p[1:])
}

func (n *Call) Eval(c *EvalCtx) Value {
	if c.Hook != nil {
		if v, ok := c.Hook(c, n, n.Args); ok {
			return v
		}
	}
	var args []Value
	if n.HasOf {
		if c.ExpandOf != nil {
			args = c.ExpandOf(n.OfToks)
		} else {
			for _, nm := range VarNames(n.OfToks) {
				args = append(args, c.get(nm))
			}
		}
	}
	for _, a := range n.Args {
		args = append(args, a.Eval(c))
	}
	f, ok := Funcs[n.Name]
	if !ok {
		if c.Sess != nil {
			c.Sess.Log.Error(c.Line, fmt.Sprintf("The function %s is unknown, or cannot be accessed.", n.Name))
		}
		return Missing
	}
	return f(c, n, args)
}

// Func is a SAS function implementation.
type Func func(c *EvalCtx, call *Call, args []Value) Value

// Funcs is the function table (upper-case names). Components register more in init().
var Funcs = map[string]Func{}

// ---------------------------------------------------------------- parser

// ExprParser parses expressions from tokens.
type ExprParser struct {
	C *Cursor
	// NEisLtGt: treat "<>" as not-equal (WHERE / SQL) instead of MAX (DATA step).
	NEisLtGt bool
	// NoEqualCompare: stop at top-level '=' (used when parsing "x = expr" targets elsewhere).
	NoEqualCompare bool
}

// ParseExpr parses a full expression from tokens; all tokens must be consumed.
func ParseExpr(toks []Token) (Expr, error) {
	p := &ExprParser{C: NewCursor(toks)}
	e, err := p.Parse()
	if err != nil {
		return nil, err
	}
	if !p.C.AtEnd() {
		return nil, fmt.Errorf("syntax error near '%s'", p.C.Peek().Text)
	}
	return e, nil
}

// ParseWhere parses a WHERE expression ("<>" means not-equal).
func ParseWhere(toks []Token) (Expr, error) {
	p := &ExprParser{C: NewCursor(toks), NEisLtGt: true}
	e, err := p.Parse()
	if err != nil {
		return nil, err
	}
	if !p.C.AtEnd() {
		return nil, fmt.Errorf("syntax error near '%s'", p.C.Peek().Text)
	}
	return e, nil
}

func (p *ExprParser) Parse() (Expr, error) { return p.parseOr() }

func (p *ExprParser) parseOr() (Expr, error) {
	l, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	for p.C.Peek().Is("OR") || p.C.Peek().IsOp("|", "!") {
		p.C.Next()
		r, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		l = &Binary{Op: "OR", L: l, R: r}
	}
	return l, nil
}

func (p *ExprParser) parseAnd() (Expr, error) {
	l, err := p.parseCompare()
	if err != nil {
		return nil, err
	}
	for p.C.Peek().Is("AND") || p.C.Peek().IsOp("&") {
		p.C.Next()
		r, err := p.parseCompare()
		if err != nil {
			return nil, err
		}
		l = &Binary{Op: "AND", L: l, R: r}
	}
	return l, nil
}

func (p *ExprParser) compareOp() (string, bool) {
	t := p.C.Peek()
	if t.Kind == TOp {
		switch t.Text {
		case "=":
			if p.NoEqualCompare {
				return "", false
			}
			return "=", true
		case "^=", "~=", "¬=":
			return "NE", true
		case "<", "<=", ">", ">=", "=:":
			return t.Text, true
		case "<>":
			if p.NEisLtGt {
				return "NE", true
			}
		case "^", "~", "¬":
			if p.C.PeekN(1).IsOp("=") {
				p.C.Next()
				return "NE", true
			}
		}
		return "", false
	}
	switch t.Upper() {
	case "EQ":
		return "=", true
	case "NE":
		return "NE", true
	case "LT":
		return "<", true
	case "LE":
		return "<=", true
	case "GT":
		return ">", true
	case "GE":
		return ">=", true
	}
	return "", false
}

func (p *ExprParser) parseCompare() (Expr, error) {
	l, err := p.parseConcat()
	if err != nil {
		return nil, err
	}
	var result Expr
	for {
		// NOT IN / IN / BETWEEN / IS / LIKE / CONTAINS
		not := false
		save := p.C.P
		if p.C.Peek().Is("NOT") || p.C.Peek().IsOp("^", "~", "¬") {
			if nx := p.C.PeekN(1); nx.Is("IN", "BETWEEN", "LIKE", "CONTAINS") {
				p.C.Next()
				not = true
			}
		}
		t := p.C.Peek()
		switch {
		case t.Is("IN"):
			p.C.Next()
			in, err := p.parseInList(l, not)
			if err != nil {
				return nil, err
			}
			return andChain(result, in), nil
		case t.Is("BETWEEN"):
			p.C.Next()
			lo, err := p.parseConcat()
			if err != nil {
				return nil, err
			}
			if !p.C.AcceptKw("AND") && !p.C.AcceptOp("&") {
				return nil, fmt.Errorf("expected AND in BETWEEN")
			}
			hi, err := p.parseConcat()
			if err != nil {
				return nil, err
			}
			return andChain(result, &BetweenExpr{X: l, Lo: lo, Hi: hi, Not: not}), nil
		case t.Is("LIKE"):
			p.C.Next()
			pat, err := p.parseConcat()
			if err != nil {
				return nil, err
			}
			return andChain(result, &LikeExpr{X: l, Pat: pat, Not: not}), nil
		case t.Is("CONTAINS") || t.IsOp("?"):
			p.C.Next()
			y, err := p.parseConcat()
			if err != nil {
				return nil, err
			}
			return andChain(result, &ContainsExpr{X: l, Y: y, Not: not}), nil
		case t.Is("IS"):
			p.C.Next()
			n := p.C.AcceptKw("NOT")
			if !p.C.AcceptKw("MISSING", "NULL") {
				return nil, fmt.Errorf("expected MISSING or NULL after IS")
			}
			return andChain(result, &IsMissingExpr{X: l, Not: n}), nil
		}
		p.C.P = save
		op, ok := p.compareOp()
		if !ok {
			break
		}
		p.C.Next()
		r, err := p.parseConcat()
		if err != nil {
			return nil, err
		}
		result = andChain(result, &Binary{Op: op, L: l, R: r})
		l = r // chained comparisons a < b < c
	}
	if result == nil {
		return l, nil
	}
	return result, nil
}

func andChain(prev, next Expr) Expr {
	if prev == nil {
		return next
	}
	return &Binary{Op: "AND", L: prev, R: next}
}

func (p *ExprParser) parseInList(x Expr, not bool) (Expr, error) {
	if !p.C.AcceptOp("(") {
		return nil, fmt.Errorf("expected '(' after IN")
	}
	in := &InExpr{X: x, Not: not}
	for !p.C.AcceptOp(")") {
		if p.C.AtEnd() {
			return nil, fmt.Errorf("unterminated IN list")
		}
		if p.C.AcceptOp(",") {
			continue
		}
		e, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		if p.C.AcceptOp(":") {
			hi, err := p.parseUnary()
			if err != nil {
				return nil, err
			}
			in.Rng = append(in.Rng, [2]Expr{e, hi})
			continue
		}
		in.List = append(in.List, e)
	}
	return in, nil
}

func (p *ExprParser) parseConcat() (Expr, error) {
	l, err := p.parseAdd()
	if err != nil {
		return nil, err
	}
	for p.C.Peek().IsOp("||", "!!") || (p.C.Peek().IsOp("|") && p.C.PeekN(1).IsOp("|")) {
		if p.C.Peek().IsOp("|") {
			p.C.Next()
		}
		p.C.Next()
		r, err := p.parseAdd()
		if err != nil {
			return nil, err
		}
		l = &Binary{Op: "||", L: l, R: r}
	}
	return l, nil
}

func (p *ExprParser) parseAdd() (Expr, error) {
	l, err := p.parseMul()
	if err != nil {
		return nil, err
	}
	for p.C.Peek().IsOp("+", "-") {
		op := p.C.Next().Text
		r, err := p.parseMul()
		if err != nil {
			return nil, err
		}
		l = &Binary{Op: op, L: l, R: r}
	}
	return l, nil
}

func (p *ExprParser) parseMul() (Expr, error) {
	l, err := p.parseUnary()
	if err != nil {
		return nil, err
	}
	for p.C.Peek().IsOp("*", "/") {
		op := p.C.Next().Text
		r, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		l = &Binary{Op: op, L: l, R: r}
	}
	return l, nil
}

// Group I: prefix + - NOT, **, >< (min), <> (max); right-to-left.
func (p *ExprParser) parseUnary() (Expr, error) {
	t := p.C.Peek()
	if t.IsOp("-", "+") {
		p.C.Next()
		x, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		if n, ok := x.(*NumLit); ok && t.Text == "-" {
			return &NumLit{V: -n.V}, nil
		}
		return &Unary{Op: t.Text, X: x}, nil
	}
	if t.Is("NOT") || (t.IsOp("^", "~", "¬") && !p.C.PeekN(1).IsOp("=")) {
		p.C.Next()
		x, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return &Unary{Op: "NOT", X: x}, nil
	}
	base, err := p.parsePrimary()
	if err != nil {
		return nil, err
	}
	switch {
	case p.C.Peek().IsOp("**"):
		p.C.Next()
		r, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return &Binary{Op: "**", L: base, R: r}, nil
	case p.C.Peek().IsOp("><"):
		p.C.Next()
		r, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return &Binary{Op: "MIN", L: base, R: r}, nil
	case p.C.Peek().IsOp("<>") && !p.NEisLtGt:
		p.C.Next()
		r, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return &Binary{Op: "MAX", L: base, R: r}, nil
	}
	return base, nil
}

func (p *ExprParser) parsePrimary() (Expr, error) {
	t := p.C.Peek()
	switch t.Kind {
	case TNumber:
		p.C.Next()
		return &NumLit{V: t.Num}, nil
	case TString:
		p.C.Next()
		return &StrLit{S: t.Text}, nil
	case TIdent:
		p.C.Next()
		nx := p.C.Peek()
		if nx.IsOp("(", "{", "[") {
			return p.parseCall(t)
		}
		return &VarRef{Name: t.Text}, nil
	case TOp:
		if t.IsOp("(") {
			p.C.Next()
			e, err := p.Parse()
			if err != nil {
				return nil, err
			}
			if err := p.C.ExpectOp(")"); err != nil {
				return nil, err
			}
			return e, nil
		}
	}
	if t.Text == "" {
		return nil, fmt.Errorf("unexpected end of expression")
	}
	return nil, fmt.Errorf("syntax error near '%s'", t.Text)
}

var closeOf = map[string]string{"(": ")", "{": "}", "[": "]"}

func (p *ExprParser) parseCall(name Token) (Expr, error) {
	open := p.C.Next().Text
	closer := closeOf[open]
	call := &Call{Name: strings.ToUpper(name.Text), RawName: name.Text, Braces: open != "("}
	// OF var-list
	if p.C.Peek().Is("OF") && (p.C.PeekN(1).Kind == TIdent) && !p.C.PeekN(1).IsOp("(") {
		p.C.Next()
		start := p.C.P
		depth := 0
		for !p.C.AtEnd() {
			t := p.C.Peek()
			if (t.IsOp(")", "}", "]")) && depth == 0 {
				break
			}
			if t.IsOp("(", "{", "[") {
				depth++
			} else if t.IsOp(")", "}", "]") {
				depth--
			}
			p.C.Next()
		}
		call.HasOf = true
		call.OfToks = append([]Token(nil), p.C.T[start:p.C.P]...)
		if err := p.C.ExpectOp(closer); err != nil {
			return nil, err
		}
		return call, nil
	}
	if p.C.AcceptOp(closer) {
		return call, nil
	}
	argIdx := 0
	for {
		// format arguments for PUT/INPUT family are raw format specs
		if argIdx == 1 && (call.Name == "PUT" || call.Name == "INPUT" || call.Name == "PUTN" || call.Name == "PUTC" || call.Name == "INPUTN" || call.Name == "INPUTC") && p.C.Peek().Kind != TString {
			start := p.C.P
			for !p.C.AtEnd() && !p.C.Peek().IsOp(",", closer) {
				p.C.Next()
			}
			var sb strings.Builder
			for _, t := range p.C.T[start:p.C.P] {
				sb.WriteString(t.Text)
			}
			call.Args = append(call.Args, &StrLit{S: sb.String()})
		} else if p.C.Peek().IsOp("*") && p.C.PeekN(1).IsOp(closer) && call.Braces {
			p.C.Next()
			call.Args = append(call.Args, &StrLit{S: "*"})
		} else {
			e, err := p.Parse()
			if err != nil {
				return nil, err
			}
			call.Args = append(call.Args, e)
		}
		argIdx++
		if p.C.AcceptOp(",") {
			continue
		}
		if err := p.C.ExpectOp(closer); err != nil {
			return nil, err
		}
		return call, nil
	}
}

// Vars returns variable names referenced by an expression.
func ExprVars(e Expr) []string {
	var out []string
	var walk func(Expr)
	walk = func(e Expr) {
		switch n := e.(type) {
		case *VarRef:
			out = append(out, n.Name)
		case *Call:
			if n.HasOf {
				out = append(out, VarNames(n.OfToks)...)
			}
			for _, a := range n.Args {
				walk(a)
			}
		case *Unary:
			walk(n.X)
		case *Binary:
			walk(n.L)
			walk(n.R)
		case *InExpr:
			walk(n.X)
		case *BetweenExpr:
			walk(n.X)
		case *IsMissingExpr:
			walk(n.X)
		case *LikeExpr:
			walk(n.X)
		case *ContainsExpr:
			walk(n.X)
		}
	}
	walk(e)
	return out
}

// RowCtx builds an evaluation context over a dataset row.
func RowCtx(s *Session, d *Dataset, row []Value) *EvalCtx {
	return &EvalCtx{Sess: s, Get: func(name string) (Value, bool) {
		if i := d.Index(name); i >= 0 {
			return row[i], true
		}
		return Missing, false
	}}
}
