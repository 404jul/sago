package sas

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// sqlRunner holds per-PROC SQL execution state.
type sqlRunner struct {
	s       *Session
	line    int
	ctx     *EvalCtx
	err     error // first runtime error raised during evaluation
	inobs   int   // INOBS= (-1 = none)
	warned  map[string]bool
	tempSeq int
}

func (r *sqlRunner) fail(err error) {
	if r.err == nil {
		r.err = err
	}
}

func (r *sqlRunner) note(msg string) { r.s.Log.Note(r.line, msg) }

// sqlOutCol describes a result column.
type sqlOutCol struct {
	name     string
	label    string
	hasLabel bool
	named    bool // alias or column name (unnamed expressions print a blank header)
	format   string
	informat string
	char     bool
	length   int
}

func (c *sqlOutCol) header() string {
	if c.hasLabel {
		return c.label
	}
	if c.named {
		return c.name
	}
	return ""
}

type sqlResult struct {
	cols []*sqlOutCol
	rows [][]Value
}

// sqlQPlan is a compiled query-expression.
type sqlQPlan struct {
	cols  []*sqlOutCol
	core  *sqlCorePlan
	op    string
	all   bool
	l, r  *sqlQPlan
	lmap  []int // result column -> left column (-1 = missing)
	rmap  []int
	order []sqlSortKey
}

type sqlSortKey struct {
	item int // result column index, or -1 for an extra key
	key  int // index into the extra keys
	desc bool
}

func (q *sqlQPlan) run() (*sqlResult, error) {
	if q.core != nil {
		return q.core.run()
	}
	lr, err := q.l.run()
	if err != nil {
		return nil, err
	}
	rr, err := q.r.run()
	if err != nil {
		return nil, err
	}
	proj := func(rows [][]Value, m []int) [][]Value {
		out := make([][]Value, len(rows))
		for i, row := range rows {
			nr := make([]Value, len(m))
			for j, k := range m {
				if k < 0 {
					nr[j] = MissingOf(q.cols[j].char)
				} else {
					nr[j] = row[k]
				}
			}
			out[i] = nr
		}
		return out
	}
	L, R := proj(lr.rows, q.lmap), proj(rr.rows, q.rmap)
	var rows [][]Value
	switch q.op {
	case "OUTER UNION":
		rows = append(L, R...)
	case "UNION":
		rows = append(L, R...)
		if !q.all {
			rows = sqlDistinct(rows)
		}
	case "EXCEPT", "INTERSECT":
		counts := map[string]int{}
		var buf []byte
		for _, row := range R {
			buf = sqlRowKey(buf[:0], row)
			counts[string(buf)]++
		}
		if !q.all {
			L = sqlDistinct(L)
		}
		for _, row := range L {
			buf = sqlRowKey(buf[:0], row)
			n := counts[string(buf)]
			in := n > 0
			if q.all && in {
				counts[string(buf)] = n - 1
			}
			if in == (q.op == "INTERSECT") {
				rows = append(rows, row)
			}
		}
	}
	sqlFixTypes(q.cols, rows)
	if len(q.order) > 0 {
		sort.SliceStable(rows, func(a, b int) bool {
			for _, k := range q.order {
				c := Compare(rows[a][k.item], rows[b][k.item])
				if k.desc {
					c = -c
				}
				if c != 0 {
					return c < 0
				}
			}
			return false
		})
	}
	return &sqlResult{cols: q.cols, rows: rows}, nil
}

func sqlRowKey(buf []byte, row []Value) []byte {
	for _, v := range row {
		buf = sqlAppendKey(buf, v)
	}
	return buf
}

func sqlCmpRow(a, b []Value) int {
	for i := range a {
		if c := Compare(a[i], b[i]); c != 0 {
			return c
		}
	}
	return 0
}

// sqlDistinct sorts rows by all columns and removes duplicates (SAS returns
// DISTINCT / UNION results in sorted order).
func sqlDistinct(rows [][]Value) [][]Value {
	sort.SliceStable(rows, func(a, b int) bool { return sqlCmpRow(rows[a], rows[b]) < 0 })
	out := rows[:0]
	for i, row := range rows {
		if i == 0 || sqlCmpRow(row, out[len(out)-1]) != 0 {
			out = append(out, row)
		}
	}
	return out
}

// sqlFixTypes widens character lengths to fit values and switches a column to
// character if a function of unknown type returned character values.
func sqlFixTypes(cols []*sqlOutCol, rows [][]Value) {
	for j, c := range cols {
		for _, row := range rows {
			v := row[j]
			if !v.Char {
				continue
			}
			if !c.char && strings.TrimSpace(v.Str) != "" {
				c.char = true
				c.length = 0
			}
			if c.char && len(strings.TrimRight(v.Str, " ")) > c.length {
				c.length = len(strings.TrimRight(v.Str, " "))
			}
		}
		if c.char && c.length < 1 {
			c.length = 1
		}
	}
}

// ---------------------------------------------------------------- compile

// compileQuery compiles a query-expression; parent is the enclosing query's
// scope for correlated subqueries. corr reports outer references.
func (r *sqlRunner) compileQuery(q *sqlQuery, parent *sqlScope) (*sqlQPlan, bool, error) {
	if q.Core != nil {
		cp, err := r.compileCore(q.Core, q.OrderBy, parent)
		if err != nil {
			return nil, false, err
		}
		return &sqlQPlan{cols: cp.cols, core: cp}, cp.correlated, nil
	}
	l, c1, err := r.compileQuery(q.L, parent)
	if err != nil {
		return nil, false, err
	}
	rp, c2, err := r.compileQuery(q.R, parent)
	if err != nil {
		return nil, false, err
	}
	p := &sqlQPlan{op: q.Op, all: q.All, l: l, r: rp}
	copyCol := func(c *sqlOutCol) *sqlOutCol { cc := *c; return &cc }
	switch {
	case q.Op == "OUTER UNION" && !q.Corr:
		for i, c := range l.cols {
			p.cols = append(p.cols, copyCol(c))
			p.lmap = append(p.lmap, i)
			p.rmap = append(p.rmap, -1)
		}
		for i, c := range rp.cols {
			p.cols = append(p.cols, copyCol(c))
			p.lmap = append(p.lmap, -1)
			p.rmap = append(p.rmap, i)
		}
	case q.Corr:
		used := map[int]bool{}
		for i, c := range l.cols {
			k := -1
			for j, d := range rp.cols {
				if strings.EqualFold(c.name, d.name) {
					k = j
					break
				}
			}
			if k < 0 && q.Op != "OUTER UNION" {
				continue
			}
			if k >= 0 {
				used[k] = true
				if c.char != rp.cols[k].char {
					return nil, false, fmt.Errorf("Column %s from the first contributor of %s is not the same type as its counterpart from the second.", c.name, q.Op)
				}
			}
			nc := copyCol(c)
			if k >= 0 && rp.cols[k].length > nc.length {
				nc.length = rp.cols[k].length
			}
			p.cols = append(p.cols, nc)
			p.lmap = append(p.lmap, i)
			p.rmap = append(p.rmap, k)
		}
		if q.Op == "OUTER UNION" {
			for j, d := range rp.cols {
				if !used[j] {
					p.cols = append(p.cols, copyCol(d))
					p.lmap = append(p.lmap, -1)
					p.rmap = append(p.rmap, j)
				}
			}
		}
	default:
		n := len(l.cols)
		if len(rp.cols) < n {
			n = len(rp.cols)
		}
		if len(l.cols) != len(rp.cols) {
			r.warnOnce(fmt.Sprintf("A table has been extended with null columns to perform the %s set operation.", q.Op))
			if len(rp.cols) > n {
				n = len(rp.cols)
			}
		}
		for i := range n {
			var c *sqlOutCol
			li, ri := i, i
			if i >= len(l.cols) {
				li = -1
				c = copyCol(rp.cols[i])
			} else {
				c = copyCol(l.cols[i])
			}
			if i >= len(rp.cols) {
				ri = -1
			} else if li >= 0 {
				if c.char != rp.cols[i].char {
					return nil, false, fmt.Errorf("Column %d from the first contributor of %s is not the same type as its counterpart from the second.", i+1, q.Op)
				}
				if rp.cols[i].length > c.length {
					c.length = rp.cols[i].length
				}
			}
			p.cols = append(p.cols, c)
			p.lmap = append(p.lmap, li)
			p.rmap = append(p.rmap, ri)
		}
	}
	for _, o := range q.OrderBy {
		idx := -1
		switch x := o.Expr.(type) {
		case *sqlNumN:
			if x.V == math.Trunc(x.V) && x.V >= 1 && int(x.V) <= len(p.cols) {
				idx = int(x.V) - 1
			}
		case *sqlNameN:
			for i, c := range p.cols {
				if x.Qual == "" && strings.EqualFold(c.name, x.Name) {
					idx = i
					break
				}
			}
		}
		if idx < 0 {
			return nil, false, fmt.Errorf("ORDER BY items must appear in the select list if the query contains a set operator (%s).", sqlKey(o.Expr))
		}
		p.order = append(p.order, sqlSortKey{item: idx, desc: o.Desc})
	}
	return p, c1 || c2, nil
}

func (r *sqlRunner) warnOnce(msg string) {
	if r.warned == nil {
		r.warned = map[string]bool{}
	}
	if !r.warned[msg] {
		r.warned[msg] = true
		r.s.Log.Warn(r.line, msg)
	}
}

// sqlSrc is one FROM-list table with its join specification.
type sqlSrc struct {
	rows   [][]Value
	start  int
	width  int
	join   string
	hashL  []Expr // equi-join keys evaluated on the left part
	hashR  []Expr // ... and on the right part (row placed at its offset)
	conds  []Expr // remaining join conditions on the combined row
	sawCnd bool
}

type sqlCorePlan struct {
	r          *sqlRunner
	env        *sqlEnv
	scope      *sqlScope
	srcs       []*sqlSrc
	where      []Expr
	isAgg      bool
	groupExprs []Expr
	aggs       []*sqlAggSpec
	items      []Expr
	cols       []*sqlOutCol
	having     Expr
	order      []sqlSortKey
	extraKeys  []Expr
	distinct   bool
	remerge    bool
	correlated bool
	nullRow    []Value
}

func sqlMember(name string) (member, full string) {
	full = NormName(name)
	member = full
	if i := strings.LastIndex(full, "."); i >= 0 {
		member = full[i+1:]
	}
	if !strings.Contains(full, ".") {
		full = "WORK." + full
	}
	return
}

func sqlSplitAnd(n sqlNode, out []sqlNode) []sqlNode {
	if b, ok := n.(*sqlBinN); ok && b.Op == "AND" {
		out = sqlSplitAnd(b.L, out)
		return sqlSplitAnd(b.R, out)
	}
	return append(out, n)
}

func (r *sqlRunner) compileCore(q *sqlCore, orderBy []sqlOrderN, parent *sqlScope) (*sqlCorePlan, error) {
	p := &sqlCorePlan{r: r, env: &sqlEnv{}, distinct: q.Distinct}
	sc := &sqlScope{env: p.env, parent: parent}
	p.scope = sc
	// ---- FROM
	for _, tr := range q.From {
		var d *Dataset
		var member, full string
		if tr.Sub != nil {
			sub, _, err := r.compileQuery(tr.Sub, nil)
			if err != nil {
				return nil, err
			}
			res, err := sub.run()
			if err != nil {
				return nil, err
			}
			d = sqlResultDataset(res, "")
		} else {
			var err error
			d, err = r.s.ReadDS(tr.Ref)
			if err != nil {
				return nil, err
			}
			member, full = sqlMember(tr.Ref.Name)
			if r.inobs >= 0 && len(d.Rows) > r.inobs {
				d.Rows = d.Rows[:r.inobs]
				r.s.Log.Warn(r.line, fmt.Sprintf("Only %d records were read from %s due to INOBS= option.", r.inobs, full))
			}
		}
		qual := strings.ToUpper(tr.Alias)
		if qual == "" {
			qual = member
		}
		src := &sqlSrc{rows: d.Rows, start: len(sc.cols), width: len(d.Vars), join: tr.Join}
		for _, v := range d.Vars {
			sc.cols = append(sc.cols, sqlRelCol{qual: qual, member: member, full: full, v: v})
		}
		p.srcs = append(p.srcs, src)
	}
	p.nullRow = make([]Value, len(sc.cols))
	for i, c := range sc.cols {
		p.nullRow[i] = MissingOf(c.v.Char)
	}
	b := &sqlBinder{r: r, scope: sc}
	// ---- select items
	var items []*sqlItemB
	for _, it := range q.Items {
		if it.Star {
			n := 0
			for i, c := range sc.cols {
				if it.StarQual != "" && !sqlQualMatch(it.StarQual, &sc.cols[i]) {
					continue
				}
				items = append(items, &sqlItemB{expr: &sqlColIdxN{I: i}, name: c.v.Name})
				n++
			}
			if n == 0 && it.StarQual != "" {
				return nil, fmt.Errorf("Could not expand %s.*, correlation name not found.", it.StarQual)
			}
			continue
		}
		ib := &sqlItemB{it: it, expr: it.Expr, name: it.Alias}
		if ib.name == "" {
			if nm, ok := it.Expr.(*sqlNameN); ok {
				ib.name = nm.Name
				if i, err := sqlLookup(sc, nm); err == nil && i >= 0 {
					ib.name = sc.cols[i].v.Name
				}
			}
		}
		items = append(items, ib)
	}
	b.items = items
	// ---- summary query?
	for _, it := range items {
		if sqlHasAgg(it.expr) {
			p.isAgg = true
		}
	}
	if q.Having != nil && sqlHasAgg(q.Having) {
		p.isAgg = true
	}
	for _, o := range orderBy {
		if sqlHasAgg(o.Expr) {
			p.isAgg = true
		}
	}
	// ---- GROUP BY (resolve positions / aliases to item expressions)
	var groupNodes []sqlNode
	for _, g := range q.GroupBy {
		n := g
		switch x := g.(type) {
		case *sqlNumN:
			k := int(x.V)
			if x.V != math.Trunc(x.V) || k < 1 || k > len(items) {
				return nil, fmt.Errorf("The GROUP BY position %v is not valid.", x.V)
			}
			n = items[k-1].expr
		case *sqlNameN:
			if i, err := sqlLookup(sc, x); err == nil && i < 0 && x.Qual == "" {
				if it := b.findItem(x.Name); it != nil {
					n = it.expr
				}
			}
		case *sqlCalcN:
			if it := b.findItem(x.Name); it != nil {
				n = it.expr
			}
		}
		if sqlHasAgg(n) {
			return nil, fmt.Errorf("Summary functions are not allowed in the GROUP BY clause.")
		}
		groupNodes = append(groupNodes, n)
	}
	if len(groupNodes) > 0 && !p.isAgg {
		r.s.Log.Warn(r.line, "A GROUP BY clause has been transformed into an ORDER BY clause because neither the SELECT clause nor the optional HAVING clause of the associated table-expression referenced a summary function.")
		if len(orderBy) == 0 {
			for _, g := range groupNodes {
				orderBy = append(orderBy, sqlOrderN{Expr: g})
			}
		}
		groupNodes = nil
	}
	b.groupCols = map[int]bool{}
	b.groupKeys = map[string]bool{}
	for _, g := range groupNodes {
		t, err := b.bind(g, sqlModeRow)
		if err != nil {
			return nil, err
		}
		if ce, ok := t.e.(*sqlColE); ok && ce.env == p.env {
			b.groupCols[ce.i] = true
		}
		if _, ok := g.(*sqlNameN); !ok {
			b.groupKeys[sqlKey(g)] = true
		}
		p.groupExprs = append(p.groupExprs, t.e)
	}
	// ---- WHERE (conjuncts may be pushed into joins)
	if q.Where != nil {
		laterOuter := make([]bool, len(p.srcs)+1)
		for k := len(p.srcs) - 1; k >= 0; k-- {
			j := p.srcs[k].join
			laterOuter[k] = laterOuter[k+1] || j == "RIGHT" || j == "FULL"
		}
		for _, cj := range sqlSplitAnd(q.Where, nil) {
			b.used, b.hasSub = map[int]bool{}, false
			t, err := b.bind(cj, sqlModeRow)
			if err != nil {
				return nil, err
			}
			used, hasSub := b.used, b.hasSub
			b.used = nil
			k := sqlLastSrc(p.srcs, used)
			if k >= 1 && !hasSub && !laterOuter[k] && (p.srcs[k].join == "," || p.srcs[k].join == "CROSS" || p.srcs[k].join == "INNER") {
				if err := p.addJoinCond(b, k, cj, t.e); err != nil {
					return nil, err
				}
				continue
			}
			p.where = append(p.where, t.e)
		}
	}
	// ---- ON conditions
	for k, src := range p.srcs {
		if k == 0 || src.join == "," || src.join == "CROSS" {
			continue
		}
		b.scope.width = src.start + src.width
		for _, cj := range sqlSplitAnd(q.From[k].On, nil) {
			b.used, b.hasSub = map[int]bool{}, false
			t, err := b.bind(cj, sqlModeRow)
			b.used = nil
			if err != nil {
				b.scope.width = 0
				return nil, err
			}
			if err := p.addJoinCond(b, k, cj, t.e); err != nil {
				b.scope.width = 0
				return nil, err
			}
		}
		b.scope.width = 0
	}
	for k, src := range p.srcs {
		if k > 0 && len(src.hashL) == 0 && (src.join == "," || src.join == "CROSS") {
			r.note("The execution of this query involves performing one or more Cartesian product joins that can not be optimized.")
			break
		}
	}
	// ---- items
	mode := sqlModeRow
	if p.isAgg {
		mode = sqlModeGroup
	}
	for _, it := range items {
		t, err := b.bindItem(it, mode)
		if err != nil {
			return nil, err
		}
		p.items = append(p.items, t.e)
		p.cols = append(p.cols, r.outCol(it, t))
	}
	// ---- HAVING
	if q.Having != nil {
		b.aliasFallback = true
		t, err := b.bind(q.Having, mode)
		b.aliasFallback = false
		if err != nil {
			return nil, err
		}
		p.having = t.e
	}
	// ---- ORDER BY
	extraNote := false
	for _, o := range orderBy {
		idx := -1
		switch x := o.Expr.(type) {
		case *sqlNumN:
			if x.V == math.Trunc(x.V) && x.V >= 1 && int(x.V) <= len(items) {
				idx = int(x.V) - 1
			} else {
				return nil, fmt.Errorf("ORDER BY item %v is not a valid select list position.", x.V)
			}
		case *sqlNameN:
			if x.Qual == "" {
				for i, it := range items {
					if it.it != nil && it.it.Alias != "" && strings.EqualFold(it.it.Alias, x.Name) {
						idx = i
						break
					}
				}
			}
		case *sqlCalcN:
			if it := b.findItem(x.Name); it != nil {
				for i := range items {
					if items[i] == it {
						idx = i
					}
				}
			}
		}
		if idx < 0 {
			key := sqlKey(o.Expr)
			for i, it := range items {
				if _, isIdx := it.expr.(*sqlColIdxN); isIdx {
					if nm, ok := o.Expr.(*sqlNameN); ok {
						if j, err := sqlLookup(sc, nm); err == nil && j == it.expr.(*sqlColIdxN).I {
							idx = i
							break
						}
					}
					continue
				}
				if sqlKey(it.expr) == key {
					idx = i
					break
				}
				if nm, ok := o.Expr.(*sqlNameN); ok && it.name != "" {
					if inm, ok := it.expr.(*sqlNameN); ok {
						a, err1 := sqlLookup(sc, nm)
						c, err2 := sqlLookup(sc, inm)
						if err1 == nil && err2 == nil && a >= 0 && a == c {
							idx = i
							break
						}
					}
				}
			}
		}
		if idx >= 0 {
			p.order = append(p.order, sqlSortKey{item: idx, desc: o.Desc})
			continue
		}
		b.aliasFallback = true
		t, err := b.bind(o.Expr, mode)
		b.aliasFallback = false
		if err != nil {
			return nil, err
		}
		p.order = append(p.order, sqlSortKey{item: -1, key: len(p.extraKeys), desc: o.Desc})
		p.extraKeys = append(p.extraKeys, t.e)
		extraNote = true
	}
	if extraNote {
		r.note("The query as specified involves ordering by an item that doesn't appear in its SELECT clause.")
	}
	p.aggs = b.aggs
	p.remerge = p.isAgg && b.remerge
	p.correlated = b.correlated
	if p.remerge {
		r.note("The query requires remerging summary statistics back with the original data.")
	}
	return p, nil
}

// sqlLastSrc returns the index of the last source whose columns are used.
func sqlLastSrc(srcs []*sqlSrc, used map[int]bool) int {
	k := 0
	for i := range used {
		for j := len(srcs) - 1; j > k; j-- {
			if i >= srcs[j].start {
				k = j
				break
			}
		}
	}
	return k
}

// addJoinCond attaches a condition to join step k, using it as a hash key
// when it is an equality between the left tables and the new table.
func (p *sqlCorePlan) addJoinCond(b *sqlBinder, k int, n sqlNode, e Expr) error {
	src := p.srcs[k]
	src.sawCnd = true
	if eq, ok := n.(*sqlBinN); ok && eq.Op == "=" {
		side := func(x sqlNode) (Expr, bool, int, error) {
			b.used, b.hasSub = map[int]bool{}, false
			t, err := b.bind(x, sqlModeRow)
			used, hasSub := b.used, b.hasSub
			b.used = nil
			if err != nil || hasSub || len(used) == 0 {
				return nil, false, 0, err
			}
			s := 0 // 1 = left only, 2 = right only, 3 = both
			for i := range used {
				if i >= src.start && i < src.start+src.width {
					s |= 2
				} else if i < src.start {
					s |= 1
				} else {
					s |= 3
				}
			}
			return t.e, t.char && t.sure, s, nil
		}
		le, lc, ls, err := side(eq.L)
		if err != nil {
			return err
		}
		re, rc, rs, err := side(eq.R)
		if err != nil {
			return err
		}
		if le != nil && re != nil && lc == rc {
			if ls == 1 && rs == 2 {
				src.hashL, src.hashR = append(src.hashL, le), append(src.hashR, re)
				return nil
			}
			if ls == 2 && rs == 1 {
				src.hashL, src.hashR = append(src.hashL, re), append(src.hashR, le)
				return nil
			}
		}
	}
	src.conds = append(src.conds, e)
	return nil
}

func (r *sqlRunner) outCol(it *sqlItemB, t sqlTyped) *sqlOutCol {
	c := &sqlOutCol{char: t.char, length: t.length}
	if !c.char {
		c.length = 8
	}
	if t.col != nil {
		c.format, c.informat = t.col.v.Format, t.col.v.Informat
		if t.col.v.Label != "" {
			c.label, c.hasLabel = t.col.v.Label, true
		}
	}
	if it.name != "" {
		c.name, c.named = it.name, true
	} else {
		r.tempSeq++
		c.name = fmt.Sprintf("_TEMA%03d", r.tempSeq)
	}
	if s := it.it; s != nil {
		if s.Format != "" {
			c.format = s.Format
		}
		if s.Informat != "" {
			c.informat = s.Informat
		}
		if s.HasLabel {
			c.label, c.hasLabel = s.Label, true
		}
		if s.Length > 0 {
			if c.char {
				c.length = s.Length
			}
		}
	}
	return c
}

// ---------------------------------------------------------------- run

type sqlGroup struct {
	keys []Value
	rows []int32
	agg  []Value
}

func (p *sqlCorePlan) source() ([][]Value, error) {
	ctx := p.r.ctx
	env := p.env
	if len(p.srcs) == 0 {
		return [][]Value{{}}, nil
	}
	rows := p.srcs[0].rows
	for k := 1; k < len(p.srcs); k++ {
		rows = p.join(rows, p.srcs[k])
		if p.r.err != nil {
			return nil, p.r.err
		}
	}
	if len(p.where) == 0 {
		return rows, nil
	}
	out := make([][]Value, 0, len(rows)/2+1)
outer:
	for _, row := range rows {
		env.row = row
		for _, w := range p.where {
			if !w.Eval(ctx).Truth() {
				continue outer
			}
		}
		out = append(out, row)
	}
	return out, p.r.err
}

func (p *sqlCorePlan) join(left [][]Value, src *sqlSrc) [][]Value {
	ctx, env := p.r.ctx, p.env
	wL := src.start
	w := wL + src.width
	tmp := make([]Value, w)
	nullR := p.nullRow[wL:w]
	right := src.rows
	var out [][]Value
	emit := func(l, r []Value) {
		row := make([]Value, w)
		copy(row, l)
		copy(row[wL:], r)
		out = append(out, row)
	}
	matches := func() bool {
		env.row = tmp
		for _, c := range src.conds {
			if !c.Eval(ctx).Truth() {
				return false
			}
		}
		return true
	}
	var rmatched []bool
	if src.join == "RIGHT" || src.join == "FULL" {
		rmatched = make([]bool, len(right))
	}
	var ht map[string][]int32
	var buf []byte
	if len(src.hashL) > 0 {
		ht = make(map[string][]int32, len(right))
		for j, rr := range right {
			copy(tmp[wL:], rr)
			env.row = tmp
			buf = buf[:0]
			for _, e := range src.hashR {
				buf = sqlAppendKey(buf, e.Eval(ctx))
			}
			ht[string(buf)] = append(ht[string(buf)], int32(j))
		}
	}
	for _, l := range left {
		copy(tmp, l)
		any := false
		try := func(j int) {
			copy(tmp[wL:], right[j])
			if matches() {
				any = true
				if rmatched != nil {
					rmatched[j] = true
				}
				emit(l, right[j])
			}
		}
		if ht != nil {
			env.row = tmp
			buf = buf[:0]
			for _, e := range src.hashL {
				buf = sqlAppendKey(buf, e.Eval(ctx))
			}
			for _, j := range ht[string(buf)] {
				try(int(j))
			}
		} else {
			for j := range right {
				try(j)
			}
		}
		if !any && (src.join == "LEFT" || src.join == "FULL") {
			emit(l, nullR)
		}
	}
	if rmatched != nil {
		nullL := p.nullRow[:wL]
		for j, m := range rmatched {
			if !m {
				emit(nullL, right[j])
			}
		}
	}
	return out
}

func (p *sqlCorePlan) groups(rows [][]Value) []*sqlGroup {
	if len(p.groupExprs) == 0 {
		g := &sqlGroup{rows: make([]int32, len(rows))}
		for i := range rows {
			g.rows[i] = int32(i)
		}
		return []*sqlGroup{g}
	}
	ctx, env := p.r.ctx, p.env
	idx := map[string]int32{}
	var groups []*sqlGroup
	kv := make([]Value, len(p.groupExprs))
	var buf []byte
	for i, row := range rows {
		env.row = row
		buf = buf[:0]
		for j, e := range p.groupExprs {
			kv[j] = e.Eval(ctx)
			buf = sqlAppendKey(buf, kv[j])
		}
		gi, ok := idx[string(buf)]
		if !ok {
			gi = int32(len(groups))
			idx[string(buf)] = gi
			groups = append(groups, &sqlGroup{keys: append([]Value(nil), kv...)})
		}
		g := groups[gi]
		g.rows = append(g.rows, int32(i))
	}
	sort.SliceStable(groups, func(a, b int) bool { return sqlCmpRow(groups[a].keys, groups[b].keys) < 0 })
	return groups
}

type sqlOutRow struct {
	vals []Value
	keys []Value
}

func (p *sqlCorePlan) run() (*sqlResult, error) {
	rows, err := p.source()
	if err != nil {
		return nil, err
	}
	ctx, env := p.r.ctx, p.env
	var out []sqlOutRow
	emit := func() {
		if p.having != nil && !p.having.Eval(ctx).Truth() {
			return
		}
		vals := make([]Value, len(p.items))
		for i, e := range p.items {
			vals[i] = e.Eval(ctx)
		}
		var keys []Value
		if len(p.extraKeys) > 0 {
			keys = make([]Value, len(p.extraKeys))
			for i, e := range p.extraKeys {
				keys[i] = e.Eval(ctx)
			}
		}
		out = append(out, sqlOutRow{vals, keys})
	}
	if !p.isAgg {
		for _, row := range rows {
			env.row = row
			emit()
		}
	} else {
		groups := p.groups(rows)
		var fbuf []float64
		for _, g := range groups {
			g.agg = make([]Value, len(p.aggs))
			for k, a := range p.aggs {
				g.agg[k], fbuf = a.compute(p, rows, g.rows, fbuf)
			}
			env.agg = g.agg
			if p.remerge {
				for _, ri := range g.rows {
					env.row = rows[ri]
					emit()
				}
				continue
			}
			if len(g.rows) > 0 {
				env.row = rows[g.rows[0]]
			} else {
				env.row = p.nullRow
			}
			emit()
		}
	}
	if p.r.err != nil {
		return nil, p.r.err
	}
	if p.distinct {
		sort.SliceStable(out, func(a, b int) bool { return sqlCmpRow(out[a].vals, out[b].vals) < 0 })
		d := out[:0]
		for i, o := range out {
			if i == 0 || sqlCmpRow(o.vals, d[len(d)-1].vals) != 0 {
				d = append(d, o)
			}
		}
		out = d
	}
	if len(p.order) > 0 {
		sort.SliceStable(out, func(a, b int) bool {
			for _, k := range p.order {
				var c int
				if k.item >= 0 {
					c = Compare(out[a].vals[k.item], out[b].vals[k.item])
				} else {
					c = Compare(out[a].keys[k.key], out[b].keys[k.key])
				}
				if k.desc {
					c = -c
				}
				if c != 0 {
					return c < 0
				}
			}
			return false
		})
	}
	res := &sqlResult{cols: p.cols, rows: make([][]Value, len(out))}
	for i, o := range out {
		res.rows[i] = o.vals
	}
	sqlFixTypes(p.cols, res.rows)
	return res, nil
}

// compute evaluates a summary function over the rows of one group.
func (a *sqlAggSpec) compute(p *sqlCorePlan, rows [][]Value, idx []int32, xs []float64) (Value, []float64) {
	if a.star {
		return NumV(float64(len(idx))), xs
	}
	ctx, env := p.r.ctx, p.env
	nmiss := 0
	if a.char {
		var vals []string
		for _, ri := range idx {
			env.row = rows[ri]
			v := a.arg.Eval(ctx)
			s := strings.TrimRight(v.AsStr(), " ")
			if s == "" {
				nmiss++
				continue
			}
			vals = append(vals, s)
		}
		if a.distinct {
			sort.Strings(vals)
			u := vals[:0]
			for i, s := range vals {
				if i == 0 || s != u[len(u)-1] {
					u = append(u, s)
				}
			}
			vals = u
		}
		switch a.fn {
		case "COUNT", "FREQ", "N":
			return NumV(float64(len(vals))), xs
		case "NMISS":
			return NumV(float64(nmiss)), xs
		}
		if len(vals) == 0 {
			return StrV(""), xs
		}
		best := vals[0]
		for _, s := range vals[1:] {
			if (a.fn == "MIN") == (s < best) {
				best = s
			}
		}
		return StrV(best), xs
	}
	xs = xs[:0]
	for _, ri := range idx {
		env.row = rows[ri]
		v := a.arg.Eval(ctx)
		f := v.Num
		if v.Char {
			if strings.TrimSpace(v.Str) == "" {
				f = math.NaN()
			} else {
				f = v.AsNum()
			}
		}
		if math.IsNaN(f) {
			nmiss++
			continue
		}
		xs = append(xs, f)
	}
	if a.distinct {
		sort.Float64s(xs)
		u := xs[:0]
		for i, f := range xs {
			if i == 0 || f != u[len(u)-1] {
				u = append(u, f)
			}
		}
		xs = u
	}
	n := float64(len(xs))
	switch a.fn {
	case "COUNT", "FREQ", "N":
		return NumV(n), xs
	case "NMISS":
		return NumV(float64(nmiss)), xs
	case "SUMWGT":
		if n == 0 {
			return Missing, xs
		}
		return NumV(n), xs
	}
	if len(xs) == 0 {
		return Missing, xs
	}
	var r float64
	switch a.fn {
	case "SUM":
		r = Sum(xs)
	case "AVG", "MEAN":
		r = Mean(xs)
	case "MIN":
		r = xs[0]
		for _, f := range xs {
			r = math.Min(r, f)
		}
	case "MAX":
		r = xs[0]
		for _, f := range xs {
			r = math.Max(r, f)
		}
	case "RANGE":
		lo, hi := xs[0], xs[0]
		for _, f := range xs {
			lo, hi = math.Min(lo, f), math.Max(hi, f)
		}
		r = hi - lo
	case "USS":
		for _, f := range xs {
			r += f * f
		}
	case "CSS":
		r = CSS(xs)
	case "MEDIAN":
		s := append([]float64(nil), xs...)
		sort.Float64s(s)
		r = Percentile(s, 50)
	case "VAR", "STD", "STDDEV", "STDERR", "CV", "T", "PRT":
		if len(xs) < 2 {
			return Missing, xs
		}
		v := Variance(xs)
		sd := math.Sqrt(v)
		switch a.fn {
		case "VAR":
			r = v
		case "STD", "STDDEV":
			r = sd
		case "STDERR":
			r = sd / math.Sqrt(n)
		case "CV":
			r = 100 * sd / Mean(xs)
		case "T", "PRT":
			if sd == 0 {
				return Missing, xs
			}
			t := Mean(xs) / (sd / math.Sqrt(n))
			if a.fn == "T" {
				r = t
			} else {
				r = TTwoSided(t, n-1)
			}
		}
	default:
		return Missing, xs
	}
	if math.IsNaN(r) || math.IsInf(r, 0) {
		return Missing, xs
	}
	return NumV(r), xs
}

// sqlResultDataset converts a query result into a data set (CREATE TABLE).
// Duplicate column names keep the first occurrence (SAS warns).
func sqlResultDataset(res *sqlResult, name string) *Dataset {
	d := NewDataset(name)
	var keep []int
	seen := map[string]bool{}
	for i, c := range res.cols {
		u := strings.ToUpper(c.name)
		if seen[u] {
			continue
		}
		seen[u] = true
		keep = append(keep, i)
		v := &Var{Name: c.name, Char: c.char, Length: c.length, Format: c.format, Informat: c.informat}
		if c.hasLabel {
			v.Label = c.label
		}
		if !v.Char {
			v.Length = 8
		}
		d.Vars = append(d.Vars, v)
	}
	d.Rows = make([][]Value, len(res.rows))
	for r, row := range res.rows {
		nr := make([]Value, len(keep))
		for j, i := range keep {
			v := row[i]
			if d.Vars[j].Char {
				if !v.Char {
					v = StrV(strings.TrimSpace(v.AsStr()))
				}
				if len(v.Str) > d.Vars[j].Length {
					v.Str = v.Str[:d.Vars[j].Length]
				}
			} else if v.Char {
				v = NumV(v.AsNum())
			}
			nr[j] = v
		}
		d.Rows[r] = nr
	}
	return d
}
