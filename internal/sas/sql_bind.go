package sas

import (
	"fmt"
	"math"
	"strings"
)

// ---------------------------------------------------------------- bound nodes

// sqlEnv holds the current row (and group aggregates) a bound query reads.
type sqlEnv struct {
	row []Value
	agg []Value
}

// sqlColIdxN is an unbound reference to a column by position (from "*").
type sqlColIdxN struct{ I int }

type sqlColE struct {
	env *sqlEnv
	i   int
}

func (e *sqlColE) Eval(*EvalCtx) Value { return e.env.row[e.i] }

type sqlAggE struct {
	env *sqlEnv
	k   int
}

func (e *sqlAggE) Eval(*EvalCtx) Value { return e.env.agg[e.k] }

type sqlCaseE struct {
	op    Expr
	conds []Expr
	vals  []Expr
	els   Expr
	char  bool
}

func (e *sqlCaseE) Eval(c *EvalCtx) Value {
	if e.op != nil {
		v := e.op.Eval(c)
		for i, w := range e.conds {
			if compareOp(c, "=", v, w.Eval(c)) {
				return e.vals[i].Eval(c)
			}
		}
	} else {
		for i, w := range e.conds {
			if w.Eval(c).Truth() {
				return e.vals[i].Eval(c)
			}
		}
	}
	if e.els != nil {
		return e.els.Eval(c)
	}
	return MissingOf(e.char)
}

// sqlCoalesceE is SQL COALESCE: first non-missing argument, either type.
type sqlCoalesceE struct {
	args []Expr
	char bool
}

func (e *sqlCoalesceE) Eval(c *EvalCtx) Value {
	for _, a := range e.args {
		if v := a.Eval(c); !v.IsMissing() {
			return v
		}
	}
	return MissingOf(e.char)
}

// sqlSubq is a compiled subquery with result caching when uncorrelated.
type sqlSubq struct {
	r          *sqlRunner
	plan       *sqlQPlan
	correlated bool
	res        *sqlResult
	set        map[string]bool
	setHasMiss bool
}

func (q *sqlSubq) result() *sqlResult {
	if q.res != nil && !q.correlated {
		return q.res
	}
	res, err := q.plan.run()
	if err != nil {
		q.r.fail(err)
		res = &sqlResult{cols: q.plan.cols}
	}
	q.res = res
	q.set = nil
	return res
}

func (q *sqlSubq) keySet() map[string]bool {
	res := q.result()
	if q.set != nil {
		return q.set
	}
	q.set = make(map[string]bool, len(res.rows))
	var buf []byte
	for _, row := range res.rows {
		buf = sqlAppendKey(buf[:0], row[0])
		q.set[string(buf)] = true
	}
	return q.set
}

type sqlScalarE struct{ q *sqlSubq }

func (e *sqlScalarE) Eval(*EvalCtx) Value {
	res := e.q.result()
	switch len(res.rows) {
	case 0:
		return MissingOf(res.cols[0].char)
	case 1:
		return res.rows[0][0]
	}
	e.q.r.fail(fmt.Errorf("Subquery evaluated to more than one row."))
	return res.rows[0][0]
}

type sqlInSubE struct {
	x   Expr
	q   *sqlSubq
	not bool
	buf []byte
}

func (e *sqlInSubE) Eval(c *EvalCtx) Value {
	set := e.q.keySet()
	e.buf = sqlAppendKey(e.buf[:0], e.x.Eval(c))
	return BoolV(set[string(e.buf)] != e.not)
}

type sqlExistsE struct {
	q   *sqlSubq
	not bool
}

func (e *sqlExistsE) Eval(*EvalCtx) Value {
	return BoolV((len(e.q.result().rows) > 0) != e.not)
}

type sqlQuantE struct {
	op  string
	x   Expr
	all bool
	q   *sqlSubq
}

func (e *sqlQuantE) Eval(c *EvalCtx) Value {
	x := e.x.Eval(c)
	for _, row := range e.q.result().rows {
		ok := compareOp(c, e.op, x, row[0])
		if e.all && !ok {
			return BoolV(false)
		}
		if !e.all && ok {
			return BoolV(true)
		}
	}
	return BoolV(e.all)
}

// ---------------------------------------------------------------- scopes

type sqlRelCol struct {
	qual   string // table alias, or member name when no alias
	member string // member name, e.g. CLASS
	full   string // libref-qualified name, e.g. SASHELP.CLASS
	v      *Var
}

type sqlScope struct {
	cols   []sqlRelCol
	env    *sqlEnv
	parent *sqlScope
	// width limits visible columns (ON clauses of a join see only the tables so far).
	width int
}

type sqlTyped struct {
	e      Expr
	char   bool
	sure   bool
	length int
	col    *sqlRelCol // non-nil for a plain column reference
}

const (
	sqlModeRow    = iota // WHERE / ON / non-summary SELECT
	sqlModeGroup         // SELECT / HAVING / ORDER BY of a summary query
	sqlModeAggArg        // inside a summary function
)

type sqlItemB struct {
	it   *sqlSelItem
	expr sqlNode
	name string // alias or column name ("" if unnamed)
	busy bool
}

type sqlAggSpec struct {
	fn       string
	arg      Expr
	char     bool
	distinct bool
	star     bool
}

type sqlBinder struct {
	r         *sqlRunner
	scope     *sqlScope
	items     []*sqlItemB
	aggs      []*sqlAggSpec
	groupCols map[int]bool
	groupKeys map[string]bool
	remerge   bool
	// correlated is set when a name resolves in an enclosing query.
	correlated bool
	used       map[int]bool
	hasSub     bool
	// aliasFallback resolves unknown names to SELECT aliases (HAVING, ORDER BY).
	aliasFallback bool
}

// sqlSummaryFuncs are PROC SQL summary functions (aggregate with one argument).
var sqlSummaryFuncs = map[string]bool{
	"AVG": true, "MEAN": true, "COUNT": true, "FREQ": true, "N": true, "CSS": true, "CV": true,
	"MAX": true, "MIN": true, "NMISS": true, "PRT": true, "RANGE": true, "STD": true, "STDDEV": true,
	"STDERR": true, "SUM": true, "SUMWGT": true, "T": true, "USS": true, "VAR": true, "MEDIAN": true,
}

func sqlIsAgg(c *sqlCallN) bool {
	if !sqlSummaryFuncs[c.Name] {
		return false
	}
	return c.Star || len(c.Args) == 1
}

// sqlHasAgg reports whether n contains a summary function (not descending into subqueries).
func sqlHasAgg(n sqlNode) bool {
	found := false
	sqlWalk(n, func(x sqlNode) {
		if c, ok := x.(*sqlCallN); ok && sqlIsAgg(c) {
			found = true
		}
	})
	return found
}

func sqlWalk(n sqlNode, f func(sqlNode)) {
	if n == nil {
		return
	}
	f(n)
	switch x := n.(type) {
	case *sqlCallN:
		for _, a := range x.Args {
			sqlWalk(a, f)
		}
	case *sqlUnN:
		sqlWalk(x.X, f)
	case *sqlBinN:
		sqlWalk(x.L, f)
		sqlWalk(x.R, f)
	case *sqlInN:
		sqlWalk(x.X, f)
		for _, a := range x.List {
			sqlWalk(a, f)
		}
	case *sqlBetweenN:
		sqlWalk(x.X, f)
		sqlWalk(x.Lo, f)
		sqlWalk(x.Hi, f)
	case *sqlLikeN:
		sqlWalk(x.X, f)
		sqlWalk(x.Pat, f)
	case *sqlContainsN:
		sqlWalk(x.X, f)
		sqlWalk(x.Y, f)
	case *sqlIsMissN:
		sqlWalk(x.X, f)
	case *sqlCaseN:
		sqlWalk(x.Operand, f)
		for _, w := range x.Whens {
			sqlWalk(w.Cond, f)
			sqlWalk(w.Val, f)
		}
		sqlWalk(x.Else, f)
	case *sqlQuantN:
		sqlWalk(x.X, f)
	}
}

func (b *sqlBinder) findItem(name string) *sqlItemB {
	for _, it := range b.items {
		if it.it != nil && it.it.Alias != "" && strings.EqualFold(it.it.Alias, name) {
			return it
		}
	}
	for _, it := range b.items {
		if it.name != "" && strings.EqualFold(it.name, name) {
			return it
		}
	}
	return nil
}

func (b *sqlBinder) bindItem(it *sqlItemB, mode int) (sqlTyped, error) {
	if it.busy {
		return sqlTyped{}, fmt.Errorf("The CALCULATED reference to %s is circular.", it.name)
	}
	it.busy = true
	defer func() { it.busy = false }()
	return b.bind(it.expr, mode)
}

func sqlQualMatch(qual string, c *sqlRelCol) bool {
	return strings.EqualFold(qual, c.qual) || strings.EqualFold(qual, c.member) || strings.EqualFold(qual, c.full)
}

// lookup finds a column in a scope: index, or -1; error on ambiguity.
func sqlLookup(sc *sqlScope, n *sqlNameN) (int, error) {
	found := -1
	w := sc.width
	if w == 0 || w > len(sc.cols) {
		w = len(sc.cols)
	}
	for i := range w {
		c := &sc.cols[i]
		if !strings.EqualFold(c.v.Name, n.Name) {
			continue
		}
		if n.Qual != "" && !sqlQualMatch(n.Qual, c) {
			continue
		}
		if found >= 0 {
			if n.Qual == "" && !strings.EqualFold(sc.cols[found].qual, c.qual) {
				return -1, fmt.Errorf("Ambiguous reference, column %s is in more than one table.", n.Name)
			}
			continue
		}
		found = i
	}
	return found, nil
}

func (b *sqlBinder) colTyped(sc *sqlScope, i int) sqlTyped {
	c := &sc.cols[i]
	return sqlTyped{e: &sqlColE{env: sc.env, i: i}, char: c.v.Char, sure: true, length: c.v.Length, col: c}
}

func sqlColDisplay(n *sqlNameN) string {
	if n.Qual != "" {
		return n.Qual + "." + n.Name
	}
	return n.Name
}

func (b *sqlBinder) bindName(n *sqlNameN, mode int) (sqlTyped, error) {
	i, err := sqlLookup(b.scope, n)
	if err != nil {
		return sqlTyped{}, err
	}
	if i >= 0 {
		if b.used != nil {
			b.used[i] = true
		}
		if mode == sqlModeGroup && !b.groupCols[i] {
			b.remerge = true
		}
		return b.colTyped(b.scope, i), nil
	}
	if b.aliasFallback && n.Qual == "" {
		if it := b.findItem(n.Name); it != nil {
			return b.bindItem(it, mode)
		}
	}
	for sc := b.scope.parent; sc != nil; sc = sc.parent {
		j, err := sqlLookup(sc, n)
		if err != nil {
			return sqlTyped{}, err
		}
		if j >= 0 {
			b.correlated = true
			return b.colTyped(sc, j), nil
		}
	}
	return sqlTyped{}, fmt.Errorf("The following columns were not found in the contributing tables: %s.", sqlColDisplay(n))
}

var sqlOpWords = map[string]string{
	"=": "equals (=)", "NE": "not equals (^=)", "<": "less than (<)", "<=": "less than or equal (<=)",
	">": "greater than (>)", ">=": "greater than or equal (>=)", "+": "addition (+)", "-": "subtraction (-)",
	"*": "multiplication (*)", "/": "division (/)", "**": "exponentiation (**)",
}

func sqlNum(e Expr) sqlTyped { return sqlTyped{e: e, sure: true, length: 8} }

func (b *sqlBinder) bind(n sqlNode, mode int) (sqlTyped, error) {
	if mode == sqlModeGroup && len(b.groupKeys) > 0 {
		if _, isName := n.(*sqlNameN); !isName && b.groupKeys[sqlKey(n)] {
			return b.bind(n, sqlModeRow)
		}
	}
	switch x := n.(type) {
	case *sqlNumN:
		return sqlNum(&NumLit{V: x.V}), nil
	case *sqlStrN:
		l := len(x.S)
		if l == 0 {
			l = 1
		}
		return sqlTyped{e: &StrLit{S: x.S}, char: true, sure: true, length: l}, nil
	case *sqlNameN:
		return b.bindName(x, mode)
	case *sqlColIdxN:
		if b.used != nil {
			b.used[x.I] = true
		}
		if mode == sqlModeGroup && !b.groupCols[x.I] {
			b.remerge = true
		}
		return b.colTyped(b.scope, x.I), nil
	case *sqlCalcN:
		it := b.findItem(x.Name)
		if it == nil {
			return sqlTyped{}, fmt.Errorf("The following columns were not found as CALCULATED references in the immediate query: %s.", x.Name)
		}
		return b.bindItem(it, mode)
	case *sqlCallN:
		return b.bindCall(x, mode)
	case *sqlUnN:
		t, err := b.bind(x.X, mode)
		if err != nil {
			return t, err
		}
		if x.Op == "NOT" {
			return sqlNum(&Unary{Op: "NOT", X: t.e}), nil
		}
		if t.sure && t.char {
			return t, fmt.Errorf("Expression using negation (-) requires numeric types.")
		}
		return sqlNum(&Unary{Op: x.Op, X: t.e}), nil
	case *sqlBinN:
		l, err := b.bind(x.L, mode)
		if err != nil {
			return l, err
		}
		r, err := b.bind(x.R, mode)
		if err != nil {
			return r, err
		}
		switch x.Op {
		case "AND", "OR":
			return sqlNum(&Binary{Op: x.Op, L: l.e, R: r.e}), nil
		case "||":
			ln, rn := l.length, r.length
			if !l.char {
				ln = 12
			}
			if !r.char {
				rn = 12
			}
			return sqlTyped{e: &Binary{Op: "||", L: l.e, R: r.e}, char: true, sure: true, length: ln + rn}, nil
		case "=", "NE", "<", "<=", ">", ">=":
			if l.sure && r.sure && l.char != r.char {
				return l, fmt.Errorf("Expression using %s has components that are of different data types.", sqlOpWords[x.Op])
			}
			return sqlNum(&Binary{Op: x.Op, L: l.e, R: r.e}), nil
		default:
			if (l.sure && l.char) || (r.sure && r.char) {
				return l, fmt.Errorf("Expression using %s requires numeric types.", sqlOpWords[x.Op])
			}
			return sqlNum(&Binary{Op: x.Op, L: l.e, R: r.e}), nil
		}
	case *sqlInN:
		t, err := b.bind(x.X, mode)
		if err != nil {
			return t, err
		}
		if x.Sub != nil {
			q, cols, err := b.bindSub(x.Sub)
			if err != nil {
				return t, err
			}
			if len(cols) != 1 {
				return t, fmt.Errorf("A subquery used with IN must return exactly one column.")
			}
			if t.sure && t.char != cols[0].char {
				return t, fmt.Errorf("Expression using IN has components that are of different data types.")
			}
			return sqlNum(&sqlInSubE{x: t.e, q: q, not: x.Not}), nil
		}
		in := &InExpr{X: t.e, Not: x.Not}
		for _, a := range x.List {
			at, err := b.bind(a, mode)
			if err != nil {
				return at, err
			}
			if t.sure && at.sure && at.char != t.char {
				return t, fmt.Errorf("Expression using IN has components that are of different data types.")
			}
			in.List = append(in.List, at.e)
		}
		return sqlNum(in), nil
	case *sqlBetweenN:
		t, err := b.bind(x.X, mode)
		if err != nil {
			return t, err
		}
		lo, err := b.bind(x.Lo, mode)
		if err != nil {
			return t, err
		}
		hi, err := b.bind(x.Hi, mode)
		if err != nil {
			return t, err
		}
		if t.sure && ((lo.sure && lo.char != t.char) || (hi.sure && hi.char != t.char)) {
			return t, fmt.Errorf("Expression using BETWEEN has components that are of different data types.")
		}
		return sqlNum(&BetweenExpr{X: t.e, Lo: lo.e, Hi: hi.e, Not: x.Not}), nil
	case *sqlLikeN:
		t, err := b.bind(x.X, mode)
		if err != nil {
			return t, err
		}
		p, err := b.bind(x.Pat, mode)
		if err != nil {
			return t, err
		}
		if (t.sure && !t.char) || (p.sure && !p.char) {
			return t, fmt.Errorf("LIKE operator requires character operands.")
		}
		return sqlNum(&LikeExpr{X: t.e, Pat: p.e, Not: x.Not}), nil
	case *sqlContainsN:
		t, err := b.bind(x.X, mode)
		if err != nil {
			return t, err
		}
		y, err := b.bind(x.Y, mode)
		if err != nil {
			return t, err
		}
		if (t.sure && !t.char) || (y.sure && !y.char) {
			return t, fmt.Errorf("CONTAINS operator requires character operands.")
		}
		return sqlNum(&ContainsExpr{X: t.e, Y: y.e, Not: x.Not}), nil
	case *sqlIsMissN:
		t, err := b.bind(x.X, mode)
		if err != nil {
			return t, err
		}
		return sqlNum(&IsMissingExpr{X: t.e, Not: x.Not}), nil
	case *sqlCaseN:
		return b.bindCase(x, mode)
	case *sqlSubN:
		q, cols, err := b.bindSub(x.Q)
		if err != nil {
			return sqlTyped{}, err
		}
		if len(cols) != 1 {
			return sqlTyped{}, fmt.Errorf("A subquery used as an expression must return exactly one column.")
		}
		return sqlTyped{e: &sqlScalarE{q: q}, char: cols[0].char, sure: true, length: cols[0].length}, nil
	case *sqlExistsN:
		q, _, err := b.bindSub(x.Q)
		if err != nil {
			return sqlTyped{}, err
		}
		return sqlNum(&sqlExistsE{q: q, not: x.Not}), nil
	case *sqlQuantN:
		t, err := b.bind(x.X, mode)
		if err != nil {
			return t, err
		}
		q, cols, err := b.bindSub(x.Q)
		if err != nil {
			return t, err
		}
		if len(cols) != 1 {
			return t, fmt.Errorf("A subquery used with ANY or ALL must return exactly one column.")
		}
		if t.sure && t.char != cols[0].char {
			return t, fmt.Errorf("Expression using %s has components that are of different data types.", sqlOpWords[x.Op])
		}
		return sqlNum(&sqlQuantE{op: x.Op, x: t.e, all: x.All, q: q}), nil
	}
	return sqlTyped{}, fmt.Errorf("Unsupported expression.")
}

func (b *sqlBinder) bindSub(q *sqlQuery) (*sqlSubq, []*sqlOutCol, error) {
	b.hasSub = true
	plan, corr, err := b.r.compileQuery(q, b.scope)
	if err != nil {
		return nil, nil, err
	}
	if corr {
		b.correlated = true
	}
	// a correlated reference to this scope may need any column
	if b.used != nil && corr {
		for i := range b.scope.cols {
			b.used[i] = true
		}
	}
	return &sqlSubq{r: b.r, plan: plan, correlated: corr}, plan.cols, nil
}

func (b *sqlBinder) bindCase(x *sqlCaseN, mode int) (sqlTyped, error) {
	ce := &sqlCaseE{}
	var op sqlTyped
	if x.Operand != nil {
		var err error
		op, err = b.bind(x.Operand, mode)
		if err != nil {
			return op, err
		}
		ce.op = op.e
	}
	res := sqlTyped{}
	first := true
	add := func(t sqlTyped, idx int) error {
		if first {
			res.char, res.sure, res.length = t.char, t.sure, t.length
			first = false
			return nil
		}
		if t.sure && res.sure && t.char != res.char {
			return fmt.Errorf("Result of WHEN clause %d is not the same data type as the preceding results.", idx)
		}
		if !res.sure && t.sure {
			res.char, res.sure = t.char, true
		}
		if t.length > res.length {
			res.length = t.length
		}
		return nil
	}
	for i, w := range x.Whens {
		c, err := b.bind(w.Cond, mode)
		if err != nil {
			return c, err
		}
		if x.Operand != nil && op.sure && c.sure && op.char != c.char {
			return c, fmt.Errorf("Expression using equals (=) has components that are of different data types.")
		}
		v, err := b.bind(w.Val, mode)
		if err != nil {
			return v, err
		}
		if err := add(v, i+1); err != nil {
			return v, err
		}
		ce.conds = append(ce.conds, c.e)
		ce.vals = append(ce.vals, v.e)
	}
	if x.Else != nil {
		v, err := b.bind(x.Else, mode)
		if err != nil {
			return v, err
		}
		if err := add(v, len(x.Whens)+1); err != nil {
			return v, err
		}
		ce.els = v.e
	}
	ce.char = res.char
	res.e = ce
	if !res.char {
		res.length = 8
	}
	return res, nil
}

// sqlCharFuncs lists character-valued functions; value = how the result
// length is derived: "arg" (length of first argument), "fmt" (format width
// of the second argument), or "" (200, the SAS default).
var sqlCharFuncs = map[string]string{
	"UPCASE": "arg", "LOWCASE": "arg", "PROPCASE": "arg", "TRIM": "arg", "TRIMN": "arg", "STRIP": "arg",
	"LEFT": "arg", "RIGHT": "arg", "SUBSTR": "arg", "SUBSTRN": "arg", "COMPRESS": "arg", "COMPBL": "arg",
	"TRANSLATE": "arg", "REVERSE": "arg", "DEQUOTE": "arg", "KSUBSTR": "arg", "KUPCASE": "arg", "KLOWCASE": "arg",
	"PUT": "fmt", "PUTN": "fmt", "PUTC": "fmt", "INPUTC": "fmt",
	"SCAN": "", "TRANWRD": "", "TRANSTRN": "", "CAT": "", "CATS": "", "CATT": "", "CATX": "", "CATQ": "",
	"REPEAT": "", "COALESCEC": "", "IFC": "", "CHOOSEC": "", "QUOTE": "", "BYTE": "", "CHAR": "",
	"FIRST": "", "VVALUE": "", "VNAME": "", "SYMGET": "", "UUIDGEN": "", "DATETIME_TEXT": "",
}

func (b *sqlBinder) bindCall(x *sqlCallN, mode int) (sqlTyped, error) {
	if sqlIsAgg(x) {
		switch mode {
		case sqlModeRow:
			return sqlTyped{}, fmt.Errorf("Summary functions are restricted to the SELECT and HAVING clauses only.")
		case sqlModeAggArg:
			return sqlTyped{}, fmt.Errorf("Summary functions nested in this way are not supported.")
		}
		spec := &sqlAggSpec{fn: x.Name, distinct: x.Distinct, star: x.Star}
		res := sqlNum(nil)
		if !x.Star {
			saveUsed := b.used
			t, err := b.bind(x.Args[0], sqlModeAggArg)
			b.used = saveUsed
			if err != nil {
				return t, err
			}
			spec.arg, spec.char = t.e, t.char && t.sure
			switch spec.fn {
			case "COUNT", "FREQ", "N", "NMISS":
			case "MIN", "MAX":
				if spec.char {
					res = sqlTyped{char: true, sure: true, length: t.length}
				}
			default:
				if spec.char {
					return t, fmt.Errorf("The %s summary function requires a numeric argument.", strings.ToUpper(x.Name[:1])+strings.ToLower(x.Name[1:]))
				}
			}
		} else if x.Name != "COUNT" && x.Name != "FREQ" && x.Name != "N" {
			return res, fmt.Errorf("Syntax error: only COUNT, FREQ and N accept (*).")
		}
		b.aggs = append(b.aggs, spec)
		res.e = &sqlAggE{env: b.scope.env, k: len(b.aggs) - 1}
		return res, nil
	}
	if x.Star {
		return sqlTyped{}, fmt.Errorf("Syntax error near '*' in function %s.", x.Name)
	}
	args := make([]sqlTyped, len(x.Args))
	for i, a := range x.Args {
		t, err := b.bind(a, mode)
		if err != nil {
			return t, err
		}
		args[i] = t
	}
	if x.Name == "COALESCE" && len(args) > 0 {
		ce := &sqlCoalesceE{char: args[0].char}
		res := sqlTyped{char: args[0].char, sure: args[0].sure}
		for _, a := range args {
			if a.sure && res.sure && a.char != res.char {
				return a, fmt.Errorf("The COALESCE function requires its arguments to be of the same data type.")
			}
			if a.char && a.length > res.length {
				res.length = a.length
			}
			ce.args = append(ce.args, a.e)
		}
		if !res.char {
			res.length = 8
		}
		ce.char = res.char
		res.e = ce
		return res, nil
	}
	if _, ok := Funcs[x.Name]; !ok {
		return sqlTyped{}, fmt.Errorf("Function %s could not be located.", x.Name)
	}
	call := &Call{Name: x.Name, RawName: x.Name}
	for _, a := range args {
		call.Args = append(call.Args, a.e)
	}
	rule, isChar := sqlCharFuncs[x.Name]
	if x.Name == "INPUT" && len(x.Args) > 1 {
		if s, ok := x.Args[1].(*sqlStrN); ok && strings.HasPrefix(strings.TrimSpace(s.S), "$") {
			isChar, rule = true, "fmt"
		}
	}
	if x.Name == "COALESCEC" || x.Name == "IFC" || x.Name == "CHOOSEC" {
		rule = ""
	}
	if !isChar {
		return sqlNum(call), nil
	}
	length := 200
	switch rule {
	case "arg":
		if len(args) > 0 && args[0].char {
			length = args[0].length
		}
	case "fmt":
		if len(x.Args) > 1 {
			if s, ok := x.Args[1].(*sqlStrN); ok {
				if f := ParseFormat(s.S); f != nil && f.HasW {
					length = f.W
				} else if f != nil && !f.HasW {
					length = 12
				}
			}
		}
	}
	if x.Name == "BYTE" {
		length = 1
	}
	return sqlTyped{e: call, char: true, sure: true, length: length}, nil
}

// ---------------------------------------------------------------- keys

// sqlAppendKey appends a hashable encoding of v with SAS equality semantics
// (trailing blanks ignored, all numeric missing values equal).
func sqlAppendKey(b []byte, v Value) []byte {
	if v.Char {
		s := strings.TrimRight(v.Str, " ")
		b = append(b, 'C')
		n := len(s)
		b = append(b, byte(n>>24), byte(n>>16), byte(n>>8), byte(n))
		return append(b, s...)
	}
	f := v.Num
	if math.IsNaN(f) {
		return append(b, 'M')
	}
	if f == 0 {
		f = 0 // fold -0
	}
	u := math.Float64bits(f)
	return append(b, 'N', byte(u>>56), byte(u>>48), byte(u>>40), byte(u>>32), byte(u>>24), byte(u>>16), byte(u>>8), byte(u))
}
