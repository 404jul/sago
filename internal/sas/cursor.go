package sas

import (
	"fmt"
	"strconv"
	"strings"
)

// Cursor walks a token slice.
type Cursor struct {
	T []Token
	P int
}

func NewCursor(t []Token) *Cursor { return &Cursor{T: t} }

func (c *Cursor) AtEnd() bool { return c.P >= len(c.T) }

// Peek returns the current token (zero Token at end).
func (c *Cursor) Peek() Token { return c.PeekN(0) }

func (c *Cursor) PeekN(k int) Token {
	if c.P+k < len(c.T) && c.P+k >= 0 {
		return c.T[c.P+k]
	}
	return Token{Kind: TOp, Text: ""}
}

func (c *Cursor) Next() Token {
	t := c.Peek()
	if !c.AtEnd() {
		c.P++
	}
	return t
}

// AcceptKw consumes the identifier if it matches one of words.
func (c *Cursor) AcceptKw(words ...string) bool {
	if c.Peek().Is(words...) {
		c.P++
		return true
	}
	return false
}

// AcceptOp consumes the operator if it matches.
func (c *Cursor) AcceptOp(ops ...string) bool {
	if c.Peek().IsOp(ops...) {
		c.P++
		return true
	}
	return false
}

func (c *Cursor) ExpectOp(op string) error {
	if !c.AcceptOp(op) {
		return fmt.Errorf("expected '%s' but found '%s'", op, c.Peek().Text)
	}
	return nil
}

func (c *Cursor) ExpectIdent() (string, error) {
	t := c.Peek()
	if t.Kind != TIdent {
		return "", fmt.Errorf("expected a name but found '%s'", t.Text)
	}
	c.P++
	return t.Text, nil
}

// Rest returns the remaining tokens.
func (c *Cursor) Rest() []Token { return c.T[c.P:] }

// Group consumes a balanced parenthesized group starting at '(' and returns
// the tokens inside (exclusive). Returns nil, false if not at '('.
func (c *Cursor) Group() ([]Token, bool) {
	if !c.Peek().IsOp("(") {
		return nil, false
	}
	depth := 0
	start := c.P
	for !c.AtEnd() {
		t := c.Next()
		if t.IsOp("(") {
			depth++
		} else if t.IsOp(")") {
			depth--
			if depth == 0 {
				return c.T[start+1 : c.P-1], true
			}
		}
	}
	return c.T[start+1:], true
}

// SplitSlash splits tokens at the first top-level '/' (outside parentheses).
// after is nil if there is no slash.
func SplitSlash(toks []Token) (before, after []Token) {
	depth := 0
	for i, t := range toks {
		switch {
		case t.IsOp("("):
			depth++
		case t.IsOp(")"):
			depth--
		case t.IsOp("/") && depth == 0:
			return toks[:i], toks[i+1:]
		}
	}
	return toks, nil
}

// ---------------------------------------------------------------- Options

// Opt is a "key", "key=value" or "key=(group)" option.
type Opt struct {
	Key string  // upper case
	Val []Token // value tokens (for groups: including the parentheses)
	Eq  bool
}

// Str returns the value as text (string literal unquoted, name, or number text).
func (o Opt) Str() string {
	if len(o.Val) == 0 {
		return ""
	}
	if len(o.Val) == 1 {
		return o.Val[0].Text
	}
	return TokText(o.Val)
}

// Inner returns the tokens of a parenthesized value without the parentheses.
func (o Opt) Inner() []Token {
	if len(o.Val) >= 2 && o.Val[0].IsOp("(") && o.Val[len(o.Val)-1].IsOp(")") {
		return o.Val[1 : len(o.Val)-1]
	}
	return o.Val
}

type Opts struct {
	List []Opt
}

// ParseOpts parses option tokens: flags, key=value, key=(...), key=name(...)
// (dataset reference with options), key=-number.
func ParseOpts(toks []Token) *Opts {
	o := &Opts{}
	c := NewCursor(toks)
	for !c.AtEnd() {
		t := c.Next()
		if t.Kind != TIdent {
			// stray token: keep as positional flag
			o.List = append(o.List, Opt{Key: t.Upper(), Val: []Token{t}})
			continue
		}
		op := Opt{Key: t.Upper()}
		if c.Peek().IsOp("=") {
			c.Next()
			op.Eq = true
			op.Val = parseOptValue(c)
		} else if c.Peek().IsOp("(") {
			// flag suboptions, e.g. CHISQ(LRCHI) or EXACT(MC)
			start := c.P
			c.Group()
			op.Val = c.T[start:c.P]
		}
		o.List = append(o.List, op)
	}
	return o
}

func parseOptValue(c *Cursor) []Token {
	start := c.P
	t := c.Peek()
	switch {
	case t.IsOp("("):
		c.Group()
		return c.T[start:c.P]
	case t.IsOp("-", "+") && c.PeekN(1).Kind == TNumber:
		c.P += 2
		v := c.T[start+1]
		if t.Text == "-" {
			v.Num = -v.Num
			v.Text = "-" + v.Text
		}
		return []Token{v}
	case t.IsOp("$"):
		// format value like $15.
		c.Next()
		if !c.AtEnd() {
			n := c.Next()
			n.Text = "$" + n.Text
			n.Kind = TIdent
			return []Token{n}
		}
		return []Token{t}
	}
	c.Next()
	if t.Kind == TIdent && c.Peek().IsOp("(") {
		c.Group()
	}
	return c.T[start:c.P]
}

// Get returns the last option with the given key (upper/lower insensitive).
func (o *Opts) Get(keys ...string) (Opt, bool) {
	for i := len(o.List) - 1; i >= 0; i-- {
		for _, k := range keys {
			if o.List[i].Key == strings.ToUpper(k) {
				return o.List[i], true
			}
		}
	}
	return Opt{}, false
}

// Has reports whether a flag or key is present.
func (o *Opts) Has(keys ...string) bool {
	_, ok := o.Get(keys...)
	return ok
}

// Str returns a key's value as text.
func (o *Opts) Str(key string, def string) string {
	if op, ok := o.Get(key); ok && op.Eq {
		return op.Str()
	}
	return def
}

// Num returns a key's numeric value.
func (o *Opts) Num(key string, def float64) float64 {
	if op, ok := o.Get(key); ok && op.Eq && len(op.Val) == 1 {
		t := op.Val[0]
		if t.Kind == TNumber {
			return t.Num
		}
		if f, err := strconv.ParseFloat(t.Text, 64); err == nil {
			return f
		}
	}
	return def
}

// ---------------------------------------------------------------- Dataset refs

// DSRef is a data set reference with data set options, e.g. iris(obs=5 where=(x>1)).
type DSRef struct {
	Name string // as written, e.g. "work.iris", "sashelp.class", "_null_"
	Opts *Opts
	Line int
}

func (r *DSRef) String() string { return r.Name }

// ParseDSRef parses "name" or "name(options)" at the cursor.
func ParseDSRef(c *Cursor) (*DSRef, error) {
	t := c.Peek()
	if t.Kind != TIdent && t.Kind != TString {
		return nil, fmt.Errorf("expected a data set name but found '%s'", t.Text)
	}
	c.Next()
	ref := &DSRef{Name: t.Text, Line: t.Line, Opts: &Opts{}}
	if g, ok := c.Group(); ok {
		ref.Opts = parseDSOpts(g)
	}
	return ref, nil
}

// parseDSOpts parses data set options. KEEP=/DROP= lists may be written
// without parentheses (keep = a b c); they run until the next "name=".
func parseDSOpts(toks []Token) *Opts {
	o := &Opts{}
	c := NewCursor(toks)
	for !c.AtEnd() {
		t := c.Peek()
		if t.Is("KEEP", "DROP") && c.PeekN(1).IsOp("=") && !c.PeekN(2).IsOp("(") {
			c.P += 2
			start := c.P
			for !c.AtEnd() && !(c.Peek().Kind == TIdent && c.PeekN(1).IsOp("=")) {
				c.Next()
			}
			o.List = append(o.List, Opt{Key: t.Upper(), Val: c.T[start:c.P], Eq: true})
			continue
		}
		// one regular option
		start := c.P
		c.Next()
		if c.Peek().IsOp("=") {
			c.Next()
			parseOptValue(c)
		}
		o.List = append(o.List, ParseOpts(c.T[start:c.P]).List...)
	}
	return o
}

// DSRefFromOpt converts an option value (DATA=x(...)) into a DSRef.
func DSRefFromOpt(op Opt) (*DSRef, error) {
	return ParseDSRef(NewCursor(op.Val))
}

// ---------------------------------------------------------------- Variable lists

// ExpandVarList expands a variable list against a dataset: names, x1-x5,
// a--b, _ALL_, _NUMERIC_, _CHARACTER_. Returns actual variable names. Unknown
// names produce an error.
func ExpandVarList(d *Dataset, toks []Token) ([]string, error) {
	var out []string
	c := NewCursor(toks)
	for !c.AtEnd() {
		t := c.Next()
		if t.Kind != TIdent {
			return out, fmt.Errorf("unexpected '%s' in variable list", t.Text)
		}
		switch strings.ToUpper(t.Text) {
		case "_ALL_":
			for _, v := range d.Vars {
				out = append(out, v.Name)
			}
			continue
		case "_NUMERIC_":
			for _, v := range d.Vars {
				if !v.Char {
					out = append(out, v.Name)
				}
			}
			continue
		case "_CHARACTER_", "_CHAR_":
			for _, v := range d.Vars {
				if v.Char {
					out = append(out, v.Name)
				}
			}
			continue
		}
		if c.Peek().IsOp("--") && c.PeekN(1).Kind == TIdent {
			c.Next()
			end := c.Next()
			i, j := d.Index(t.Text), d.Index(end.Text)
			if i < 0 || j < 0 {
				return out, fmt.Errorf("variable %s or %s not found", t.Text, end.Text)
			}
			for k := i; k <= j; k++ {
				out = append(out, d.Vars[k].Name)
			}
			continue
		}
		if c.Peek().IsOp("-") && c.PeekN(1).Kind == TIdent {
			c.Next()
			end := c.Next()
			names, err := NumberedRange(t.Text, end.Text)
			if err != nil {
				return out, err
			}
			for _, n := range names {
				i := d.Index(n)
				if i < 0 {
					return out, fmt.Errorf("variable %s not found", n)
				}
				out = append(out, d.Vars[i].Name)
			}
			continue
		}
		i := d.Index(t.Text)
		if i < 0 {
			return out, fmt.Errorf("variable %s not found", strings.ToUpper(t.Text))
		}
		out = append(out, d.Vars[i].Name)
	}
	return out, nil
}

// NumberedRange expands x1 - x5 into x1..x5.
func NumberedRange(a, b string) ([]string, error) {
	pa, na := splitTrailingNum(a)
	pb, nb := splitTrailingNum(b)
	if na < 0 || nb < 0 || !strings.EqualFold(pa, pb) || nb < na {
		return nil, fmt.Errorf("invalid variable range %s-%s", a, b)
	}
	var out []string
	for k := na; k <= nb; k++ {
		out = append(out, pa+strconv.Itoa(k))
	}
	return out, nil
}

func splitTrailingNum(s string) (string, int) {
	i := len(s)
	for i > 0 && isDigit(s[i-1]) {
		i--
	}
	if i == len(s) {
		return s, -1
	}
	n, _ := strconv.Atoi(s[i:])
	return s[:i], n
}

// VarNames returns raw names from a token list (no dataset validation),
// expanding numbered ranges x1-x3.
func VarNames(toks []Token) []string {
	var out []string
	c := NewCursor(toks)
	for !c.AtEnd() {
		t := c.Next()
		if t.Kind != TIdent {
			continue
		}
		if c.Peek().IsOp("-") && c.PeekN(1).Kind == TIdent {
			if names, err := NumberedRange(t.Text, c.PeekN(1).Text); err == nil {
				c.P += 2
				out = append(out, names...)
				continue
			}
		}
		out = append(out, t.Text)
	}
	return out
}

// Indices maps variable names to column indices (error if missing).
func (d *Dataset) Indices(names []string) ([]int, error) {
	idx := make([]int, len(names))
	for i, n := range names {
		j := d.Index(n)
		if j < 0 {
			return nil, fmt.Errorf("variable %s not found", strings.ToUpper(n))
		}
		idx[i] = j
	}
	return idx, nil
}
