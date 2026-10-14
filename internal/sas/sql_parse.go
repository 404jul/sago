package sas

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// ---------------------------------------------------------------- SQL AST

// sqlNode is an unbound SQL expression node.
type sqlNode interface{}

type sqlNameN struct {
	Qual, Name string
}
type sqlNumN struct{ V float64 }
type sqlStrN struct{ S string }
type sqlCallN struct {
	Name     string // upper case
	Args     []sqlNode
	Distinct bool
	Star     bool // COUNT(*)
}
type sqlUnN struct {
	Op string // "-", "+", "NOT"
	X  sqlNode
}
type sqlBinN struct {
	Op   string // AND OR || + - * / ** = NE < <= > >=
	L, R sqlNode
}
type sqlInN struct {
	X    sqlNode
	List []sqlNode
	Sub  *sqlQuery
	Not  bool
}
type sqlBetweenN struct {
	X, Lo, Hi sqlNode
	Not       bool
}
type sqlLikeN struct {
	X, Pat sqlNode
	Not    bool
}
type sqlContainsN struct {
	X, Y sqlNode
	Not  bool
}
type sqlIsMissN struct {
	X   sqlNode
	Not bool
}
type sqlWhenN struct{ Cond, Val sqlNode }
type sqlCaseN struct {
	Operand sqlNode
	Whens   []sqlWhenN
	Else    sqlNode
}
type sqlCalcN struct{ Name string }
type sqlSubN struct{ Q *sqlQuery }
type sqlExistsN struct {
	Q   *sqlQuery
	Not bool
}
type sqlQuantN struct {
	Op  string
	X   sqlNode
	All bool
	Q   *sqlQuery
}

// sqlSelItem is one object-item of a SELECT clause.
type sqlSelItem struct {
	Star     bool
	StarQual string
	Expr     sqlNode
	Alias    string
	Label    string
	HasLabel bool
	Format   string
	Informat string
	Length   int
}

// sqlTabRef is one FROM-list entry.
type sqlTabRef struct {
	Ref   *DSRef
	Sub   *sqlQuery
	Alias string
	Join  string // "" (first), "," , INNER, LEFT, RIGHT, FULL, CROSS
	On    sqlNode
}

type sqlOrderN struct {
	Expr sqlNode
	Desc bool
}

// sqlCore is a single table-expression (SELECT ... FROM ... WHERE ...).
type sqlCore struct {
	Distinct bool
	Items    []*sqlSelItem
	From     []*sqlTabRef
	Where    sqlNode
	GroupBy  []sqlNode
	Having   sqlNode
}

// sqlQuery is a query-expression: a core or a set operation, plus ORDER BY.
type sqlQuery struct {
	Core    *sqlCore
	Op      string // UNION, EXCEPT, INTERSECT, OUTER UNION
	All     bool
	Corr    bool
	L, R    *sqlQuery
	OrderBy []sqlOrderN
}

// ---------------------------------------------------------------- parser

type sqlParser struct {
	c *Cursor
}

func sqlSyntaxErr(t Token) error {
	if t.Text == "" {
		return fmt.Errorf("Syntax error, statement ended unexpectedly.")
	}
	return fmt.Errorf("Syntax error near '%s'.", t.Text)
}

func (p *sqlParser) expectKw(kw string) error {
	if !p.c.AcceptKw(kw) {
		return fmt.Errorf("Syntax error, expecting %s near '%s'.", kw, p.c.Peek().Text)
	}
	return nil
}

func (p *sqlParser) expectOp(op string) error {
	if !p.c.AcceptOp(op) {
		if p.c.AtEnd() {
			return fmt.Errorf("Syntax error, expecting '%s' but the statement ended.", op)
		}
		return fmt.Errorf("Syntax error, expecting '%s' near '%s'.", op, p.c.Peek().Text)
	}
	return nil
}

// sqlReserved words never taken as implicit aliases.
var sqlReserved = map[string]bool{
	"FROM": true, "INTO": true, "FORMAT": true, "LABEL": true, "LENGTH": true, "INFORMAT": true,
	"TRANSCODE": true, "WHERE": true, "GROUP": true, "HAVING": true, "ORDER": true, "UNION": true,
	"EXCEPT": true, "INTERSECT": true, "OUTER": true, "AS": true, "ON": true, "JOIN": true,
	"INNER": true, "LEFT": true, "RIGHT": true, "FULL": true, "CROSS": true, "NATURAL": true,
	"ASC": true, "DESC": true, "AND": true, "OR": true, "NOT": true, "WHEN": true, "THEN": true,
	"ELSE": true, "END": true, "SET": true, "VALUES": true, "SELECT": true,
}

func (p *sqlParser) parseQuery() (*sqlQuery, error) {
	q, err := p.parseSetExpr()
	if err != nil {
		return nil, err
	}
	if p.c.Peek().Is("ORDER") && p.c.PeekN(1).Is("BY") {
		p.c.Next()
		p.c.Next()
		for {
			e, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			o := sqlOrderN{Expr: e}
			if p.c.AcceptKw("DESC") {
				o.Desc = true
			} else {
				p.c.AcceptKw("ASC")
			}
			q.OrderBy = append(q.OrderBy, o)
			if !p.c.AcceptOp(",") {
				break
			}
		}
	}
	return q, nil
}

func (p *sqlParser) parseSetExpr() (*sqlQuery, error) {
	l, err := p.parseSetTerm()
	if err != nil {
		return nil, err
	}
	for {
		var op string
		switch {
		case p.c.Peek().Is("UNION"), p.c.Peek().Is("EXCEPT"):
			op = p.c.Next().Upper()
		case p.c.Peek().Is("OUTER") && p.c.PeekN(1).Is("UNION"):
			p.c.Next()
			p.c.Next()
			op = "OUTER UNION"
		default:
			return l, nil
		}
		q := &sqlQuery{Op: op, L: l}
		p.parseSetMods(q)
		r, err := p.parseSetTerm()
		if err != nil {
			return nil, err
		}
		q.R = r
		l = q
	}
}

func (p *sqlParser) parseSetMods(q *sqlQuery) {
	for {
		switch {
		case p.c.AcceptKw("ALL"):
			q.All = true
		case p.c.AcceptKw("CORR", "CORRESPONDING"):
			q.Corr = true
		default:
			return
		}
	}
}

func (p *sqlParser) parseSetTerm() (*sqlQuery, error) {
	l, err := p.parseSetFactor()
	if err != nil {
		return nil, err
	}
	for p.c.Peek().Is("INTERSECT") {
		p.c.Next()
		q := &sqlQuery{Op: "INTERSECT", L: l}
		p.parseSetMods(q)
		r, err := p.parseSetFactor()
		if err != nil {
			return nil, err
		}
		q.R = r
		l = q
	}
	return l, nil
}

func (p *sqlParser) parseSetFactor() (*sqlQuery, error) {
	if p.c.Peek().IsOp("(") && p.c.PeekN(1).Is("SELECT") {
		p.c.Next()
		q, err := p.parseQuery()
		if err != nil {
			return nil, err
		}
		if err := p.expectOp(")"); err != nil {
			return nil, err
		}
		return q, nil
	}
	core, err := p.parseCore()
	if err != nil {
		return nil, err
	}
	return &sqlQuery{Core: core}, nil
}

func (p *sqlParser) parseCore() (*sqlCore, error) {
	if err := p.expectKw("SELECT"); err != nil {
		return nil, err
	}
	core := &sqlCore{}
	if p.c.AcceptKw("DISTINCT", "UNIQUE") {
		core.Distinct = true
	}
	for {
		it, err := p.parseSelItem()
		if err != nil {
			return nil, err
		}
		core.Items = append(core.Items, it)
		if !p.c.AcceptOp(",") {
			break
		}
	}
	if p.c.Peek().Is("INTO") {
		return nil, fmt.Errorf("The INTO clause is not supported by sago (macro variables are resolved before execution).")
	}
	if p.c.AcceptKw("FROM") {
		if err := p.parseFrom(core); err != nil {
			return nil, err
		}
	}
	if p.c.AcceptKw("WHERE") {
		e, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		core.Where = e
	}
	if p.c.Peek().Is("GROUP") && p.c.PeekN(1).Is("BY") {
		p.c.Next()
		p.c.Next()
		for {
			e, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			core.GroupBy = append(core.GroupBy, e)
			if !p.c.AcceptOp(",") {
				break
			}
		}
	}
	if p.c.AcceptKw("HAVING") {
		e, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		core.Having = e
	}
	return core, nil
}

// sqlStarQual reports whether the cursor is at "qual.*" and returns qual.
func (p *sqlParser) starQual() (string, bool) {
	t := p.c.Peek()
	if t.Kind == TIdent && strings.HasSuffix(t.Text, ".") && p.c.PeekN(1).IsOp("*") {
		return strings.TrimSuffix(t.Text, "."), true
	}
	return "", false
}

func (p *sqlParser) parseSelItem() (*sqlSelItem, error) {
	if p.c.Peek().IsOp("*") {
		p.c.Next()
		return &sqlSelItem{Star: true}, nil
	}
	if q, ok := p.starQual(); ok {
		p.c.Next()
		p.c.Next()
		return &sqlSelItem{Star: true, StarQual: q}, nil
	}
	e, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	it := &sqlSelItem{Expr: e}
	if err := p.parseColMods(it, true); err != nil {
		return nil, err
	}
	return it, nil
}

// parseColMods parses [AS alias] ['label'] {FORMAT=|LABEL=|LENGTH=|INFORMAT=|TRANSCODE=}.
func (p *sqlParser) parseColMods(it *sqlSelItem, allowAlias bool) error {
	if allowAlias {
		if p.c.AcceptKw("AS") {
			t := p.c.Next()
			if t.Kind != TIdent && t.Kind != TString {
				return sqlSyntaxErr(t)
			}
			it.Alias = t.Text
		} else if t := p.c.Peek(); t.Kind == TIdent && !sqlReserved[t.Upper()] && !p.c.PeekN(1).IsOp("=") {
			p.c.Next()
			it.Alias = t.Text
		}
	}
	for {
		t := p.c.Peek()
		switch {
		case t.Kind == TString:
			p.c.Next()
			it.Label, it.HasLabel = t.Text, true
		case t.Is("FORMAT", "INFORMAT") && p.c.PeekN(1).IsOp("="):
			p.c.Next()
			p.c.Next()
			f, err := p.parseFmtSpec()
			if err != nil {
				return err
			}
			if t.Is("FORMAT") {
				it.Format = f
			} else {
				it.Informat = f
			}
		case t.Is("LABEL") && p.c.PeekN(1).IsOp("="):
			p.c.Next()
			p.c.Next()
			v := p.c.Next()
			if v.Kind != TString {
				return fmt.Errorf("Syntax error, expecting a quoted string after LABEL= near '%s'.", v.Text)
			}
			it.Label, it.HasLabel = v.Text, true
		case t.Is("LENGTH") && p.c.PeekN(1).IsOp("="):
			p.c.Next()
			p.c.Next()
			v := p.c.Next()
			if v.Kind != TNumber || v.Num < 1 {
				return fmt.Errorf("Syntax error, invalid LENGTH= value '%s'.", v.Text)
			}
			it.Length = int(v.Num)
		case t.Is("TRANSCODE") && p.c.PeekN(1).IsOp("="):
			p.c.Next()
			p.c.Next()
			p.c.Next()
		default:
			return nil
		}
	}
}

// parseFmtSpec reads a format name such as best12. 6.4 $15. dollar10.2.
func (p *sqlParser) parseFmtSpec() (string, error) {
	t := p.c.Next()
	text := t.Text
	if t.IsOp("$") {
		n := p.c.Next()
		text = "$" + n.Text
	}
	if ParseFormat(text) == nil {
		return "", fmt.Errorf("Invalid format specification '%s'.", text)
	}
	return text, nil
}

var sqlJoinKw = map[string]bool{"INNER": true, "LEFT": true, "RIGHT": true, "FULL": true, "CROSS": true, "JOIN": true}

func (p *sqlParser) parseFrom(core *sqlCore) error {
	tr, err := p.parseTabRef()
	if err != nil {
		return err
	}
	core.From = append(core.From, tr)
	for {
		join := ""
		switch {
		case p.c.AcceptOp(","):
			join = ","
		case p.c.Peek().Is("INNER", "CROSS"):
			join = p.c.Next().Upper()
			if err := p.expectKw("JOIN"); err != nil {
				return err
			}
		case p.c.Peek().Is("LEFT", "RIGHT", "FULL"):
			join = p.c.Next().Upper()
			p.c.AcceptKw("OUTER")
			if err := p.expectKw("JOIN"); err != nil {
				return err
			}
		case p.c.Peek().Is("JOIN"):
			p.c.Next()
			join = "INNER"
		default:
			return nil
		}
		tr, err := p.parseTabRef()
		if err != nil {
			return err
		}
		tr.Join = join
		if join == "INNER" || join == "LEFT" || join == "RIGHT" || join == "FULL" {
			if err := p.expectKw("ON"); err != nil {
				return err
			}
			e, err := p.parseExpr()
			if err != nil {
				return err
			}
			tr.On = e
		}
		core.From = append(core.From, tr)
	}
}

func (p *sqlParser) parseTabRef() (*sqlTabRef, error) {
	tr := &sqlTabRef{}
	if p.c.Peek().IsOp("(") {
		p.c.Next()
		q, err := p.parseQuery()
		if err != nil {
			return nil, err
		}
		if err := p.expectOp(")"); err != nil {
			return nil, err
		}
		tr.Sub = q
	} else {
		ref, err := ParseDSRef(p.c)
		if err != nil {
			return nil, err
		}
		tr.Ref = ref
	}
	if p.c.AcceptKw("AS") {
		t := p.c.Next()
		if t.Kind != TIdent {
			return nil, sqlSyntaxErr(t)
		}
		tr.Alias = t.Text
	} else if t := p.c.Peek(); t.Kind == TIdent && !sqlReserved[t.Upper()] && !sqlJoinKw[t.Upper()] {
		p.c.Next()
		tr.Alias = t.Text
	}
	return tr, nil
}

// ---------------------------------------------------------------- expressions

func (p *sqlParser) parseExpr() (sqlNode, error) { return p.parseOr() }

func (p *sqlParser) parseOr() (sqlNode, error) {
	l, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	for p.c.Peek().Is("OR") || p.c.Peek().IsOp("|", "!") {
		p.c.Next()
		r, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		l = &sqlBinN{Op: "OR", L: l, R: r}
	}
	return l, nil
}

func (p *sqlParser) parseAnd() (sqlNode, error) {
	l, err := p.parseNot()
	if err != nil {
		return nil, err
	}
	for p.c.Peek().Is("AND") || p.c.Peek().IsOp("&") {
		p.c.Next()
		r, err := p.parseNot()
		if err != nil {
			return nil, err
		}
		l = &sqlBinN{Op: "AND", L: l, R: r}
	}
	return l, nil
}

func (p *sqlParser) isNotTok(k int) bool {
	t := p.c.PeekN(k)
	return t.Is("NOT") || (t.IsOp("^", "~", "¬") && !p.c.PeekN(k+1).IsOp("="))
}

func (p *sqlParser) parseNot() (sqlNode, error) {
	if p.isNotTok(0) {
		p.c.Next()
		if p.c.Peek().Is("EXISTS") {
			e, err := p.parsePredicate()
			if err != nil {
				return nil, err
			}
			if ex, ok := e.(*sqlExistsN); ok {
				ex.Not = !ex.Not
				return ex, nil
			}
			return &sqlUnN{Op: "NOT", X: e}, nil
		}
		x, err := p.parseNot()
		if err != nil {
			return nil, err
		}
		return &sqlUnN{Op: "NOT", X: x}, nil
	}
	return p.parsePredicate()
}

func (p *sqlParser) compareOp() (string, bool) {
	t := p.c.Peek()
	if t.Kind == TOp {
		switch t.Text {
		case "=":
			return "=", true
		case "^=", "~=", "¬=", "<>":
			return "NE", true
		case "<", "<=", ">", ">=":
			return t.Text, true
		case "^", "~", "¬":
			if p.c.PeekN(1).IsOp("=") {
				p.c.Next()
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

func (p *sqlParser) parseSubqueryParen() (*sqlQuery, error) {
	if err := p.expectOp("("); err != nil {
		return nil, err
	}
	q, err := p.parseQuery()
	if err != nil {
		return nil, err
	}
	if err := p.expectOp(")"); err != nil {
		return nil, err
	}
	return q, nil
}

func (p *sqlParser) parsePredicate() (sqlNode, error) {
	if p.c.AcceptKw("EXISTS") {
		q, err := p.parseSubqueryParen()
		if err != nil {
			return nil, err
		}
		return &sqlExistsN{Q: q}, nil
	}
	l, err := p.parseConcat()
	if err != nil {
		return nil, err
	}
	not := false
	if p.isNotTok(0) && p.c.PeekN(1).Is("IN", "BETWEEN", "LIKE", "CONTAINS") {
		p.c.Next()
		not = true
	}
	t := p.c.Peek()
	switch {
	case t.Is("IN"):
		p.c.Next()
		if err := p.expectOp("("); err != nil {
			return nil, err
		}
		in := &sqlInN{X: l, Not: not}
		if p.c.Peek().Is("SELECT") {
			q, err := p.parseQuery()
			if err != nil {
				return nil, err
			}
			in.Sub = q
			if err := p.expectOp(")"); err != nil {
				return nil, err
			}
			return in, nil
		}
		for !p.c.AcceptOp(")") {
			if p.c.AtEnd() {
				return nil, fmt.Errorf("Syntax error, unterminated IN list.")
			}
			if p.c.AcceptOp(",") {
				continue
			}
			e, err := p.parseConcat()
			if err != nil {
				return nil, err
			}
			in.List = append(in.List, e)
		}
		return in, nil
	case t.Is("BETWEEN"):
		p.c.Next()
		lo, err := p.parseConcat()
		if err != nil {
			return nil, err
		}
		if !p.c.AcceptKw("AND") && !p.c.AcceptOp("&") {
			return nil, fmt.Errorf("Syntax error, expecting AND in BETWEEN near '%s'.", p.c.Peek().Text)
		}
		hi, err := p.parseConcat()
		if err != nil {
			return nil, err
		}
		return &sqlBetweenN{X: l, Lo: lo, Hi: hi, Not: not}, nil
	case t.Is("LIKE"):
		p.c.Next()
		pat, err := p.parseConcat()
		if err != nil {
			return nil, err
		}
		return &sqlLikeN{X: l, Pat: pat, Not: not}, nil
	case t.Is("CONTAINS") || t.IsOp("?"):
		p.c.Next()
		y, err := p.parseConcat()
		if err != nil {
			return nil, err
		}
		return &sqlContainsN{X: l, Y: y, Not: not}, nil
	case t.Is("IS"):
		p.c.Next()
		n := p.c.AcceptKw("NOT")
		if !p.c.AcceptKw("MISSING", "NULL") {
			return nil, fmt.Errorf("Syntax error, expecting MISSING or NULL after IS near '%s'.", p.c.Peek().Text)
		}
		return &sqlIsMissN{X: l, Not: n}, nil
	}
	if not {
		return nil, sqlSyntaxErr(p.c.Peek())
	}
	op, ok := p.compareOp()
	if !ok {
		return l, nil
	}
	p.c.Next()
	if p.c.Peek().Is("ANY", "ALL", "SOME") && p.c.PeekN(1).IsOp("(") && p.c.PeekN(2).Is("SELECT") {
		all := p.c.Next().Is("ALL")
		q, err := p.parseSubqueryParen()
		if err != nil {
			return nil, err
		}
		return &sqlQuantN{Op: op, X: l, All: all, Q: q}, nil
	}
	r, err := p.parseConcat()
	if err != nil {
		return nil, err
	}
	return &sqlBinN{Op: op, L: l, R: r}, nil
}

func (p *sqlParser) parseConcat() (sqlNode, error) {
	l, err := p.parseAdd()
	if err != nil {
		return nil, err
	}
	for p.c.Peek().IsOp("||", "!!") || (p.c.Peek().IsOp("|") && p.c.PeekN(1).IsOp("|")) {
		if p.c.Peek().IsOp("|") {
			p.c.Next()
		}
		p.c.Next()
		r, err := p.parseAdd()
		if err != nil {
			return nil, err
		}
		l = &sqlBinN{Op: "||", L: l, R: r}
	}
	return l, nil
}

func (p *sqlParser) parseAdd() (sqlNode, error) {
	l, err := p.parseMul()
	if err != nil {
		return nil, err
	}
	for {
		t := p.c.Peek()
		if !t.IsOp("+", "-", "--") {
			return l, nil
		}
		p.c.Next()
		r, err := p.parseMul()
		if err != nil {
			return nil, err
		}
		if t.Text == "--" { // "a--b" lexes as one token: a - (-b)
			l = &sqlBinN{Op: "-", L: l, R: &sqlUnN{Op: "-", X: r}}
			continue
		}
		l = &sqlBinN{Op: t.Text, L: l, R: r}
	}
}

func (p *sqlParser) parseMul() (sqlNode, error) {
	l, err := p.parseUnary()
	if err != nil {
		return nil, err
	}
	for p.c.Peek().IsOp("*", "/") {
		op := p.c.Next().Text
		r, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		l = &sqlBinN{Op: op, L: l, R: r}
	}
	return l, nil
}

func (p *sqlParser) parseUnary() (sqlNode, error) {
	t := p.c.Peek()
	if t.IsOp("-", "+") {
		p.c.Next()
		x, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		if n, ok := x.(*sqlNumN); ok {
			if t.Text == "-" {
				return &sqlNumN{V: -n.V}, nil
			}
			return n, nil
		}
		return &sqlUnN{Op: t.Text, X: x}, nil
	}
	base, err := p.parsePrimary()
	if err != nil {
		return nil, err
	}
	if p.c.Peek().IsOp("**") {
		p.c.Next()
		r, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return &sqlBinN{Op: "**", L: base, R: r}, nil
	}
	return base, nil
}

func sqlSplitName(text string) *sqlNameN {
	if i := strings.LastIndex(text, "."); i > 0 && i < len(text)-1 {
		return &sqlNameN{Qual: text[:i], Name: text[i+1:]}
	}
	return &sqlNameN{Name: text}
}

func (p *sqlParser) parsePrimary() (sqlNode, error) {
	t := p.c.Peek()
	switch t.Kind {
	case TNumber:
		p.c.Next()
		if t.Text == "." {
			return &sqlNumN{V: math.NaN()}, nil
		}
		return &sqlNumN{V: t.Num}, nil
	case TString:
		p.c.Next()
		return &sqlStrN{S: t.Text}, nil
	case TIdent:
		switch {
		case t.Is("CASE"):
			p.c.Next()
			return p.parseCase()
		case t.Is("CALCULATED") && p.c.PeekN(1).Kind == TIdent:
			p.c.Next()
			return &sqlCalcN{Name: p.c.Next().Text}, nil
		}
		p.c.Next()
		if p.c.Peek().IsOp("(") {
			return p.parseCall(t)
		}
		return sqlSplitName(t.Text), nil
	case TOp:
		if t.IsOp("(") {
			p.c.Next()
			if p.c.Peek().Is("SELECT") {
				q, err := p.parseQuery()
				if err != nil {
					return nil, err
				}
				if err := p.expectOp(")"); err != nil {
					return nil, err
				}
				return &sqlSubN{Q: q}, nil
			}
			e, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			if err := p.expectOp(")"); err != nil {
				return nil, err
			}
			return e, nil
		}
	}
	return nil, sqlSyntaxErr(t)
}

func (p *sqlParser) parseCase() (sqlNode, error) {
	cs := &sqlCaseN{}
	if !p.c.Peek().Is("WHEN") {
		op, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		cs.Operand = op
	}
	for p.c.AcceptKw("WHEN") {
		cond, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		if err := p.expectKw("THEN"); err != nil {
			return nil, err
		}
		val, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		cs.Whens = append(cs.Whens, sqlWhenN{Cond: cond, Val: val})
	}
	if len(cs.Whens) == 0 {
		return nil, fmt.Errorf("Syntax error, expecting WHEN near '%s'.", p.c.Peek().Text)
	}
	if p.c.AcceptKw("ELSE") {
		e, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		cs.Else = e
	}
	if err := p.expectKw("END"); err != nil {
		return nil, err
	}
	return cs, nil
}

var sqlFmtArgFuncs = map[string]bool{"PUT": true, "INPUT": true, "PUTN": true, "PUTC": true, "INPUTN": true, "INPUTC": true}

func (p *sqlParser) parseCall(name Token) (sqlNode, error) {
	p.c.Next() // (
	call := &sqlCallN{Name: name.Upper()}
	if p.c.Peek().IsOp("*") && p.c.PeekN(1).IsOp(")") {
		p.c.Next()
		p.c.Next()
		call.Star = true
		return call, nil
	}
	if p.c.AcceptOp(")") {
		return call, nil
	}
	if p.c.AcceptKw("DISTINCT") {
		call.Distinct = true
	} else {
		p.c.AcceptKw("ALL")
	}
	for i := 0; ; i++ {
		if i == 1 && sqlFmtArgFuncs[call.Name] && p.c.Peek().Kind != TString {
			start := p.c.P
			for !p.c.AtEnd() && !p.c.Peek().IsOp(",", ")") {
				p.c.Next()
			}
			var sb strings.Builder
			for _, t := range p.c.T[start:p.c.P] {
				sb.WriteString(t.Text)
			}
			call.Args = append(call.Args, &sqlStrN{S: sb.String()})
		} else {
			e, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			call.Args = append(call.Args, e)
		}
		if p.c.AcceptOp(",") {
			continue
		}
		if err := p.expectOp(")"); err != nil {
			return nil, err
		}
		return call, nil
	}
}

// sqlKey renders a canonical text of an expression (used to match
// SELECT/ORDER BY expressions against GROUP BY expressions).
func sqlKey(n sqlNode) string {
	var b strings.Builder
	sqlKeyTo(&b, n)
	return b.String()
}

func sqlKeyTo(b *strings.Builder, n sqlNode) {
	switch x := n.(type) {
	case *sqlNameN:
		if x.Qual != "" {
			b.WriteString(strings.ToUpper(x.Qual) + ".")
		}
		b.WriteString(strings.ToUpper(x.Name))
	case *sqlNumN:
		b.WriteString(strconv.FormatFloat(x.V, 'g', -1, 64))
	case *sqlStrN:
		b.WriteString(strconv.Quote(x.S))
	case *sqlCallN:
		b.WriteString(x.Name + "(")
		if x.Distinct {
			b.WriteString("DISTINCT ")
		}
		if x.Star {
			b.WriteString("*")
		}
		for i, a := range x.Args {
			if i > 0 {
				b.WriteString(",")
			}
			sqlKeyTo(b, a)
		}
		b.WriteString(")")
	case *sqlUnN:
		b.WriteString("(" + x.Op + " ")
		sqlKeyTo(b, x.X)
		b.WriteString(")")
	case *sqlBinN:
		b.WriteString("(")
		sqlKeyTo(b, x.L)
		b.WriteString(" " + x.Op + " ")
		sqlKeyTo(b, x.R)
		b.WriteString(")")
	case *sqlCalcN:
		b.WriteString("CALCULATED " + strings.ToUpper(x.Name))
	case *sqlCaseN:
		b.WriteString("CASE ")
		if x.Operand != nil {
			sqlKeyTo(b, x.Operand)
		}
		for _, w := range x.Whens {
			b.WriteString(" WHEN ")
			sqlKeyTo(b, w.Cond)
			b.WriteString(" THEN ")
			sqlKeyTo(b, w.Val)
		}
		if x.Else != nil {
			b.WriteString(" ELSE ")
			sqlKeyTo(b, x.Else)
		}
		b.WriteString(" END")
	default:
		// predicates and subqueries: unique per node
		fmt.Fprintf(b, "<%p>", n)
	}
}
