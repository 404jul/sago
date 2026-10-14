package sas

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

// ---------------------------------------------------------------- effects

// lmMaxPoints is SAS's default PLOTS(MAXPOINTS=5000) limit.
const lmMaxPoints = 5000

// lmTerm is a model effect: crossed variables plus optional nesting variables.
type lmTerm struct {
	Vars []string
	Nest []string
}

func (t lmTerm) Name() string {
	s := strings.Join(t.Vars, "*")
	if len(t.Nest) > 0 {
		s += "(" + strings.Join(t.Nest, " ") + ")"
	}
	return s
}

func (t lmTerm) all() []string { return append(append([]string(nil), t.Vars...), t.Nest...) }

// lmParseEffects parses the right-hand side of a MODEL statement
// (a b a*b a|b|c@2 a(b) x*x).
func lmParseEffects(toks []Token) ([]lmTerm, error) {
	var out []lmTerm
	c := NewCursor(toks)
	factor := func() (lmTerm, error) {
		t := c.Next()
		if t.Kind != TIdent {
			return lmTerm{}, fmt.Errorf("Syntax error in MODEL effects near %q.", t.Text)
		}
		term := lmTerm{Vars: []string{t.Text}}
		if c.Peek().IsOp("(") {
			g, _ := c.Group()
			for _, x := range g {
				if x.Kind == TIdent {
					term.Nest = append(term.Nest, x.Text)
				}
			}
		}
		return term, nil
	}
	for !c.AtEnd() {
		f, err := factor()
		if err != nil {
			return nil, err
		}
		bar := []lmTerm{f}
		cur := f
		isBar := false
		for c.Peek().IsOp("*", "|") {
			op := c.Next()
			g, err := factor()
			if err != nil {
				return nil, err
			}
			if op.Text == "*" {
				if isBar {
					last := &bar[len(bar)-1]
					last.Vars = append(last.Vars, g.Vars...)
					last.Nest = append(last.Nest, g.Nest...)
				} else {
					cur.Vars = append(cur.Vars, g.Vars...)
					cur.Nest = append(cur.Nest, g.Nest...)
					bar[0] = cur
				}
			} else {
				isBar = true
				bar = append(bar, g)
			}
		}
		maxN := 1 << 30
		if c.Peek().IsOp("@") {
			c.Next()
			n := c.Next()
			maxN = int(n.Num)
		}
		if !isBar {
			out = append(out, cur)
			continue
		}
		// bar expansion: a b a*b c a*c b*c a*b*c ...
		var exp []lmTerm
		for _, b := range bar {
			k := len(exp)
			exp = append(exp, b)
			for i := range k {
				t := lmTerm{Vars: append(append([]string(nil), exp[i].Vars...), b.Vars...), Nest: append(append([]string(nil), exp[i].Nest...), b.Nest...)}
				exp = append(exp, t)
			}
		}
		for _, t := range exp {
			if len(t.Vars) <= maxN {
				out = append(out, t)
			}
		}
	}
	return out, nil
}

// ---------------------------------------------------------------- class variables

type lmClass struct {
	Name   string
	Var    *Var
	Idx    int
	Levels []string
	Raw    []Value
	pos    map[string]int
}

func lmLevelText(v *Var, x Value) string {
	return strings.TrimSpace(FormatByName(x, v.Format))
}

// lmBuildClass collects the sorted levels of a class variable over rows.
func lmBuildClass(d *Dataset, name string, rows [][]Value) (*lmClass, error) {
	i := d.Index(name)
	if i < 0 {
		return nil, fmt.Errorf("Variable %s not found.", strings.ToUpper(name))
	}
	v := d.Vars[i]
	c := &lmClass{Name: v.Name, Var: v, Idx: i, pos: map[string]int{}}
	seen := map[string]Value{}
	for _, r := range rows {
		x := r[i]
		if x.IsMissing() {
			continue
		}
		k := lmLevelText(v, x)
		if _, ok := seen[k]; !ok {
			seen[k] = x
		}
	}
	for k := range seen {
		c.Levels = append(c.Levels, k)
	}
	byFmt := v.Format != "" && !v.Char
	sort.Slice(c.Levels, func(a, b int) bool {
		if byFmt {
			return c.Levels[a] < c.Levels[b]
		}
		return Compare(seen[c.Levels[a]], seen[c.Levels[b]]) < 0
	})
	for j, k := range c.Levels {
		c.pos[k] = j
		c.Raw = append(c.Raw, seen[k])
	}
	return c, nil
}

func (c *lmClass) level(r []Value) int {
	x := r[c.Idx]
	if x.IsMissing() {
		return -1
	}
	if p, ok := c.pos[lmLevelText(c.Var, x)]; ok {
		return p
	}
	return -1
}

// ---------------------------------------------------------------- design

// lmDesign maps data rows to design columns for a list of effects.
type lmDesign struct {
	Terms   []lmTerm
	Int     bool
	Classes map[string]*lmClass // upper name
	cont    map[string]int      // upper name -> column index in data
	// overparameterized (dummy) columns
	ColTerm []int    // -1 = intercept
	ColName []string // "Intercept", "Weight", "Group 1", "A*B 1 2"
	colKey  [][]int  // class level combination per column (for dummy coding)
	// effect (sum-to-zero) coding columns
	ECTerm []int
}

func (m *lmDesign) isClass(name string) bool { _, ok := m.Classes[strings.ToUpper(name)]; return ok }

func (m *lmDesign) termClasses(t lmTerm) []*lmClass {
	var cs []*lmClass
	for _, v := range t.all() {
		if c, ok := m.Classes[strings.ToUpper(v)]; ok {
			cs = append(cs, c)
		}
	}
	return cs
}

func (m *lmDesign) termCont(t lmTerm, r []Value) float64 {
	p := 1.0
	for _, v := range t.all() {
		if m.isClass(v) {
			continue
		}
		p *= r[m.cont[strings.ToUpper(v)]].Num
	}
	return p
}

// lmNewDesign prepares the design; rows are the complete-case rows used to
// determine observed level combinations.
func lmNewDesign(d *Dataset, terms []lmTerm, classes []*lmClass, intercept bool, rows [][]Value) (*lmDesign, error) {
	m := &lmDesign{Terms: terms, Int: intercept, Classes: map[string]*lmClass{}, cont: map[string]int{}}
	for _, c := range classes {
		m.Classes[strings.ToUpper(c.Name)] = c
	}
	for _, t := range terms {
		for _, v := range t.all() {
			if m.isClass(v) {
				continue
			}
			i := d.Index(v)
			if i < 0 {
				return nil, fmt.Errorf("Variable %s not found.", strings.ToUpper(v))
			}
			if d.Vars[i].Char {
				return nil, fmt.Errorf("Variable %s in list does not match type prescribed for this list.", strings.ToUpper(v))
			}
			m.cont[strings.ToUpper(v)] = i
		}
	}
	if intercept {
		m.ColTerm = append(m.ColTerm, -1)
		m.ColName = append(m.ColName, "Intercept")
		m.colKey = append(m.colKey, nil)
		m.ECTerm = append(m.ECTerm, -1)
	}
	for ti, t := range terms {
		cs := m.termClasses(t)
		if len(cs) == 0 {
			m.ColTerm = append(m.ColTerm, ti)
			m.ColName = append(m.ColName, t.Name())
			m.colKey = append(m.colKey, nil)
			m.ECTerm = append(m.ECTerm, ti)
			continue
		}
		// observed combinations, sorted lexicographically by level index
		seen := map[string][]int{}
		for _, r := range rows {
			key := make([]int, len(cs))
			ok := true
			for j, c := range cs {
				key[j] = c.level(r)
				if key[j] < 0 {
					ok = false
				}
			}
			if ok {
				seen[fmt.Sprint(key)] = key
			}
		}
		var keys [][]int
		for _, k := range seen {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(a, b int) bool {
			for j := range keys[a] {
				if keys[a][j] != keys[b][j] {
					return keys[a][j] < keys[b][j]
				}
			}
			return false
		})
		for _, k := range keys {
			parts := []string{t.Name()}
			for j, c := range cs {
				parts = append(parts, c.Levels[k[j]])
			}
			m.ColTerm = append(m.ColTerm, ti)
			m.ColName = append(m.ColName, strings.Join(parts, " "))
			m.colKey = append(m.colKey, k)
		}
		// effect coding: product over crossed class vars of (L-1) columns,
		// nested class vars as full indicators
		n := 1
		for _, v := range t.Vars {
			if c, ok := m.Classes[strings.ToUpper(v)]; ok {
				n *= len(c.Levels) - 1
			}
		}
		for _, v := range t.Nest {
			if c, ok := m.Classes[strings.ToUpper(v)]; ok {
				n *= len(c.Levels)
			}
		}
		for range n {
			m.ECTerm = append(m.ECTerm, ti)
		}
	}
	return m, nil
}

// Row returns the overparameterized design row (nil if any value is missing).
func (m *lmDesign) Row(r []Value) []float64 {
	x := make([]float64, 0, len(m.ColTerm))
	var lv map[*lmClass]int
	for ci, ti := range m.ColTerm {
		if ti < 0 {
			x = append(x, 1)
			continue
		}
		t := m.Terms[ti]
		cv := m.termCont(t, r)
		if math.IsNaN(cv) {
			return nil
		}
		key := m.colKey[ci]
		if key == nil {
			x = append(x, cv)
			continue
		}
		if lv == nil {
			lv = map[*lmClass]int{}
		}
		on := true
		for j, c := range m.termClasses(t) {
			l, ok := lv[c]
			if !ok {
				l = c.level(r)
				lv[c] = l
			}
			if l < 0 {
				return nil
			}
			if l != key[j] {
				on = false
			}
		}
		if on {
			x = append(x, cv)
		} else {
			x = append(x, 0)
		}
	}
	return x
}

// ECRow returns the effect-coded (full rank for complete designs) row.
func (m *lmDesign) ECRow(r []Value) []float64 {
	var x []float64
	if m.Int {
		x = append(x, 1)
	}
	for _, t := range m.Terms {
		cv := m.termCont(t, r)
		vals := []float64{cv}
		for _, v := range t.Vars {
			c, ok := m.Classes[strings.ToUpper(v)]
			if !ok {
				continue
			}
			l := c.level(r)
			L := len(c.Levels)
			code := make([]float64, L-1)
			for j := range code {
				switch {
				case l == j:
					code[j] = 1
				case l == L-1:
					code[j] = -1
				}
			}
			vals = lmKron(vals, code)
		}
		for _, v := range t.Nest {
			c, ok := m.Classes[strings.ToUpper(v)]
			if !ok {
				continue
			}
			code := make([]float64, len(c.Levels))
			if l := c.level(r); l >= 0 {
				code[l] = 1
			}
			vals = lmKron(vals, code)
		}
		x = append(x, vals...)
	}
	return x
}

func lmKron(a, b []float64) []float64 {
	out := make([]float64, 0, len(a)*len(b))
	for _, x := range a {
		for _, y := range b {
			out = append(out, x*y)
		}
	}
	return out
}

// ---------------------------------------------------------------- sweep engine

// lmCross is the crossproducts matrix [X'WX X'Wy; y'WX y'Wy]; with an
// intercept (column 0) it is stored already swept on the intercept, computed
// from centered data for accuracy.
type lmCross struct {
	A     [][]float64
	P     int // number of X columns
	Int   bool
	SumW  float64
	N     int
	Means []float64 // weighted means of X columns and y (last)
	UDiag []float64 // diagonal of uncorrected X'WX
}

func lmNewCross(X [][]float64, y, w []float64, intercept bool) *lmCross {
	n := len(y)
	p := 0
	if n > 0 {
		p = len(X[0])
	} else if len(X) > 0 {
		p = len(X[0])
	}
	m := p + 1
	cr := &lmCross{P: p, Int: intercept, N: n, Means: make([]float64, m), UDiag: make([]float64, p)}
	wt := func(r int) float64 {
		if w == nil {
			return 1
		}
		return w[r]
	}
	col := func(r, j int) float64 {
		if j == p {
			return y[r]
		}
		return X[r][j]
	}
	for r := range n {
		cr.SumW += wt(r)
		for j := range m {
			cr.Means[j] += wt(r) * col(r, j)
		}
		for j := range p {
			cr.UDiag[j] += wt(r) * X[r][j] * X[r][j]
		}
	}
	if cr.SumW > 0 {
		for j := range m {
			cr.Means[j] /= cr.SumW
		}
	}
	A := make([][]float64, m)
	for i := range A {
		A[i] = make([]float64, m)
	}
	ctr := make([]float64, m)
	if intercept {
		copy(ctr, cr.Means)
		ctr[0] = 0
	}
	for r := range n {
		wr := wt(r)
		for i := range m {
			if intercept && i == 0 {
				continue
			}
			xi := (col(r, i) - ctr[i]) * wr
			for j := i; j < m; j++ {
				if intercept && j == 0 {
					continue
				}
				A[i][j] += xi * (col(r, j) - ctr[j])
			}
		}
	}
	for i := range m {
		for j := range i {
			A[i][j] = A[j][i]
		}
	}
	if intercept && cr.SumW > 0 {
		A[0][0] = 1 / cr.SumW
		for j := 1; j < m; j++ {
			A[0][j] = cr.Means[j]
			A[j][0] = -cr.Means[j]
		}
	}
	cr.A = A
	return cr
}

// lmSwept is the result of sweeping a set of columns.
type lmSwept struct {
	A       [][]float64
	Aliased []bool
	In      []bool
	Rank    int
	SSE     float64
}

// sweep sweeps the given columns (in order) of a copy of the crossproducts.
func (cr *lmCross) sweep(cols []int) *lmSwept {
	m := cr.P + 1
	a := make([][]float64, m)
	for i := range a {
		a[i] = append([]float64(nil), cr.A[i]...)
	}
	s := &lmSwept{A: a, Aliased: make([]bool, cr.P), In: make([]bool, cr.P)}
	if cr.Int && cr.P > 0 {
		s.In[0] = true
		s.Rank = 1
	}
	for _, k := range cols {
		if cr.Int && k == 0 {
			continue
		}
		s.In[k] = true
		orig := cr.A[k][k]
		if orig <= 0 || a[k][k] <= 1e-9*orig || math.Abs(a[k][k]) < 1e-300 {
			s.Aliased[k] = true
			continue
		}
		Sweep(a, k)
		s.Rank++
	}
	s.SSE = a[cr.P][cr.P]
	if s.SSE < 0 {
		s.SSE = 0
	}
	return s
}

// ---------------------------------------------------------------- fit

// lmFit is a fitted linear model.
type lmFit struct {
	Cross   *lmCross
	Sw      *lmSwept
	Beta    []float64
	G       [][]float64 // generalized inverse of X'WX (zeros for aliased)
	Aliased []bool
	Rank    int
	SSE     float64
	DFE     int
	MSE     float64
	DFM     int
	SSM     float64
	TSS     float64 // corrected (or uncorrected for NOINT) total
	YMean   float64
	N       int
	SumW    float64
	// sequential (Type I) SS per term index, with DF
	SeqSS []float64
	SeqDF []int
}

// lmFitModel fits all columns. colTerm maps columns to term indices (-1 = intercept).
func lmFitModel(X [][]float64, y, w []float64, intercept bool, colTerm []int, nterms int) *lmFit {
	cr := lmNewCross(X, y, w, intercept)
	p := cr.P
	f := &lmFit{Cross: cr, N: len(y), SumW: cr.SumW, YMean: cr.Means[p], SeqSS: make([]float64, nterms), SeqDF: make([]int, nterms)}
	// sequential sweep for Type I
	m := p + 1
	a := make([][]float64, m)
	for i := range a {
		a[i] = append([]float64(nil), cr.A[i]...)
	}
	f.Aliased = make([]bool, p)
	rank := 0
	if intercept && p > 0 {
		rank = 1
	}
	prev := a[p][p]
	for k := range p {
		if intercept && k == 0 {
			continue
		}
		orig := cr.A[k][k]
		if orig <= 0 || a[k][k] <= 1e-9*orig || math.Abs(a[k][k]) < 1e-300 {
			f.Aliased[k] = true
		} else {
			Sweep(a, k)
			rank++
			if t := colTerm[k]; t >= 0 {
				f.SeqDF[t]++
			}
		}
		last := k == p-1 || colTerm[k+1] != colTerm[k]
		if last && colTerm[k] >= 0 {
			cur := math.Max(a[p][p], 0)
			f.SeqSS[colTerm[k]] = prev - cur
			prev = cur
		}
	}
	f.Sw = &lmSwept{A: a, Aliased: f.Aliased, Rank: rank, SSE: math.Max(a[p][p], 0)}
	f.Rank = rank
	f.SSE = f.Sw.SSE
	f.Beta = make([]float64, p)
	f.G = make([][]float64, p)
	for i := range p {
		f.G[i] = make([]float64, p)
		if f.Aliased[i] {
			continue
		}
		f.Beta[i] = a[i][p]
		for j := range p {
			if !f.Aliased[j] {
				f.G[i][j] = a[i][j]
			}
		}
	}
	f.DFE = int(math.Round(cr.SumW)) - rank
	if w == nil {
		f.DFE = f.N - rank
	}
	if f.DFE > 0 {
		f.MSE = f.SSE / float64(f.DFE)
	} else {
		f.MSE = math.NaN()
	}
	if intercept {
		f.TSS = cr.A[p][p]
		f.DFM = rank - 1
	} else {
		f.TSS = cr.A[p][p]
		f.DFM = rank
	}
	f.SSM = f.TSS - f.SSE
	return f
}

// xGx returns x' G x.
func (f *lmFit) xGx(x []float64) float64 {
	s := 0.0
	for i := range x {
		if x[i] == 0 {
			continue
		}
		for j := range x {
			s += x[i] * f.G[i][j] * x[j]
		}
	}
	return s
}

func (f *lmFit) predict(x []float64) float64 {
	s := 0.0
	for i, b := range f.Beta {
		s += b * x[i]
	}
	return s
}

// lmPartialSS returns SSE(without cols in drop) - SSE(full) and DF difference.
func lmPartialSS(cr *lmCross, drop map[int]bool, keep map[int]bool) (float64, int) {
	var full, red []int
	for k := range cr.P {
		if keep != nil && !keep[k] && !drop[k] {
			continue
		}
		full = append(full, k)
		if !drop[k] {
			red = append(red, k)
		}
	}
	a := cr.sweep(full)
	b := cr.sweep(red)
	return b.SSE - a.SSE, a.Rank - b.Rank
}

// lmTestL tests L beta = c: returns SS, numerator DF.
func (f *lmFit) testL(L [][]float64, c []float64) (float64, int) {
	q := len(L)
	if q == 0 {
		return math.NaN(), 0
	}
	lb := make([]float64, q)
	for i := range L {
		lb[i] = f.predict(L[i]) - c[i]
	}
	M := make([][]float64, q+1)
	for i := range M {
		M[i] = make([]float64, q+1)
	}
	for i := range q {
		for j := range q {
			s := 0.0
			for a := range L[i] {
				if L[i][a] == 0 {
					continue
				}
				for b := range L[j] {
					s += L[i][a] * f.G[a][b] * L[j][b]
				}
			}
			M[i][j] = s
		}
		M[i][q] = lb[i]
		M[q][i] = lb[i]
	}
	rank := 0
	for k := range q {
		orig := M[k][k]
		if orig <= 0 || M[k][k] <= 1e-9*orig {
			continue
		}
		Sweep(M, k)
		rank++
	}
	// lb' M^- lb = -(swept corner change)
	return -M[q][q], rank
}

// ---------------------------------------------------------------- per-observation statistics

// lmObsStats holds observation-level statistics (NaN when not computable).
type lmObsStats struct {
	Pred, StdP, StdI, Resid, StdR, Student, RStudent, CookD, H, Press, Dffits, LCL, UCL, LCLM, UCLM float64
}

func (f *lmFit) obsStats(x []float64, y, w, alpha float64) lmObsStats {
	nan := math.NaN()
	o := lmObsStats{nan, nan, nan, nan, nan, nan, nan, nan, nan, nan, nan, nan, nan, nan, nan}
	if x == nil || math.IsNaN(w) {
		return o
	}
	if w <= 0 {
		w = 1
	}
	q := f.xGx(x)
	o.Pred = f.predict(x)
	o.StdP = math.Sqrt(q * f.MSE)
	o.StdI = math.Sqrt(f.MSE * (1/w + q))
	t := TInv(1-alpha/2, float64(f.DFE))
	o.LCLM, o.UCLM = o.Pred-t*o.StdP, o.Pred+t*o.StdP
	o.LCL, o.UCL = o.Pred-t*o.StdI, o.Pred+t*o.StdI
	o.H = w * q
	if math.IsNaN(y) {
		return o
	}
	o.Resid = y - o.Pred
	o.StdR = math.Sqrt(f.MSE * (1/w - q))
	o.Student = o.Resid / o.StdR
	p := float64(f.Rank)
	o.CookD = o.Student * o.Student * o.H / ((1 - o.H) * p)
	o.Press = o.Resid / (1 - o.H)
	dfe := float64(f.DFE)
	s2i := (dfe*f.MSE - w*o.Resid*o.Resid/(1-o.H)) / (dfe - 1)
	o.RStudent = o.Resid / math.Sqrt(s2i*(1/w-q))
	o.Dffits = o.RStudent * math.Sqrt(o.H/(1-o.H))
	return o
}

// lmOutKeys lists OUTPUT statement keywords and their aliases.
var lmOutKeys = map[string]string{
	"P": "P", "PREDICTED": "P", "PRED": "P", "R": "R", "RESIDUAL": "R", "RSTUDENT": "RSTUDENT",
	"STUDENT": "STUDENT", "COOKD": "COOKD", "H": "H", "STDP": "STDP", "STDR": "STDR", "STDI": "STDI",
	"LCL": "LCL", "UCL": "UCL", "LCLM": "LCLM", "UCLM": "UCLM", "PRESS": "PRESS", "DFFITS": "DFFITS",
	// SAS's alpha-suffixed synonyms: L95=/U95= for prediction limits,
	// L95M=/U95M= for confidence limits (same for 90/95/99).
	"L95": "LCL", "U95": "UCL", "L90": "LCL", "U90": "UCL", "L99": "LCL", "U99": "UCL",
	"L95M": "LCLM", "U95M": "UCLM", "L90M": "LCLM", "U90M": "UCLM", "L99M": "LCLM", "U99M": "UCLM",
}

func (o lmObsStats) get(k string) float64 {
	switch k {
	case "P":
		return o.Pred
	case "R":
		return o.Resid
	case "RSTUDENT":
		return o.RStudent
	case "STUDENT":
		return o.Student
	case "COOKD":
		return o.CookD
	case "H":
		return o.H
	case "STDP":
		return o.StdP
	case "STDR":
		return o.StdR
	case "STDI":
		return o.StdI
	case "LCL":
		return o.LCL
	case "UCL":
		return o.UCL
	case "LCLM":
		return o.LCLM
	case "UCLM":
		return o.UCLM
	case "PRESS":
		return o.Press
	case "DFFITS":
		return o.Dffits
	}
	return math.NaN()
}

// lmOutSpec is a parsed OUTPUT statement.
type lmOutSpec struct {
	Ref   *DSRef
	Items [][2]string // key, variable name (for each dependent: names[depIndex])
	Names map[string][]string
	Alpha float64
}

func lmParseOutput(st *Stmt, alpha float64) (*lmOutSpec, error) {
	sp := &lmOutSpec{Names: map[string][]string{}, Alpha: alpha}
	c := NewCursor(st.Args())
	for !c.AtEnd() {
		t := c.Next()
		if t.Kind != TIdent || !c.AcceptOp("=") {
			continue
		}
		k := t.Upper()
		if k == "OUT" {
			start := c.P
			c.Next()
			if c.Peek().IsOp("(") {
				c.Group()
			}
			r, err := DSRefFromOpt(Opt{Key: "OUT", Val: c.T[start:c.P], Eq: true})
			if err != nil {
				return nil, err
			}
			sp.Ref = r
			continue
		}
		if k == "ALPHA" {
			sp.Alpha = c.Next().Num
			continue
		}
		key, ok := lmOutKeys[k]
		if !ok {
			return nil, fmt.Errorf("Syntax error in OUTPUT statement: %s is not a valid statistic keyword.", k)
		}
		var names []string
		if c.Peek().IsOp("(") {
			g, _ := c.Group()
			for _, x := range g {
				if x.Kind == TIdent {
					names = append(names, x.Text)
				}
			}
		} else {
			for c.Peek().Kind == TIdent && !c.PeekN(1).IsOp("=") {
				names = append(names, c.Next().Text)
			}
		}
		sp.Items = append(sp.Items, [2]string{key, ""})
		sp.Names[key] = names
	}
	if sp.Ref == nil {
		sp.Ref = &DSRef{Name: lmNextDataN(), Line: st.Line}
	}
	return sp, nil
}

var lmDataN = 0

func lmNextDataN() string {
	lmDataN++
	return "DATA" + strconv.Itoa(lmDataN)
}

// ---------------------------------------------------------------- formatting helpers

const (
	lmFmtP = "PVALUE6.4"
	lmFmtF = "8.2"
	lmFmtT = "7.2"
)

// lmNum formats a number like SAS's BEST-ish displays with d significant decimals.
func lmFmtFit(x float64, d int) string {
	if math.IsNaN(x) {
		return "."
	}
	return strconv.FormatFloat(x, 'f', d, 64)
}

// lmFitDec returns SAS-like decimals for Root MSE / Dependent Mean (5 decimals, fewer for large values).
func lmFitDec(x float64) string {
	if math.IsNaN(x) {
		return "."
	}
	a := math.Abs(x)
	d := 5
	for a >= 100 && d > 0 {
		a /= 10
		d--
	}
	return strconv.FormatFloat(x, 'f', d, 64)
}

func lmFProb(F float64, d1, d2 int) float64 {
	if math.IsNaN(F) || d1 <= 0 || d2 <= 0 {
		return math.NaN()
	}
	return FSF(F, float64(d1), float64(d2))
}

func lmDiv(a, b float64) float64 {
	if b == 0 {
		return math.NaN()
	}
	return a / b
}

// lmGraphicsOn reports whether ODS graphics are enabled.
func lmGraphicsOn(s *Session) bool {
	if v, ok := s.Ext["odsgraphics"]; ok {
		if b, ok := v.(bool); ok {
			return b
		}
	}
	return true
}

// lmPlotsWanted parses PLOTS= on the PROC statement.
func lmPlotsWanted(s *Session, o *Opts) bool {
	if !lmGraphicsOn(s) || o.Has("NOPRINT") {
		return false
	}
	if op, ok := o.Get("PLOTS", "PLOT"); ok && op.Eq {
		txt := strings.ToUpper(TokText(op.Val))
		if strings.Contains(txt, "NONE") && !strings.Contains(txt, "ONLY") {
			return false
		}
	}
	return true
}

// ---------------------------------------------------------------- studentized range distribution
// Port of the algorithm of Copenhaver & Holland (1988) as implemented in R (ptukey.c / qtukey.c).

func lmWprob(w, rr, cc float64) float64 {
	const (
		nleg   = 12
		ihalf  = 6
		C1     = -30.0
		C3     = 60.0
		bb     = 8.0
		wlar   = 3.0
		wincr1 = 2.0
		wincr2 = 3.0
	)
	xleg := [ihalf]float64{0.981560634246719250690549090149, 0.904117256370474856678465866119, 0.769902674194304687036893833213, 0.587317954286617447296702418941, 0.367831498998180193752691536644, 0.125233408511468915472441369464}
	aleg := [ihalf]float64{0.047175336386511827194615961485, 0.106939325995318430960254718194, 0.160078328543346226334652529543, 0.203167426723065921749064455810, 0.233492536538354808760849898925, 0.249147045813402785000562436043}
	qsqz := w * 0.5
	if qsqz >= bb {
		return 1
	}
	prW := 2*NormCDF(qsqz) - 1
	if prW >= 1 {
		prW = 1
	} else {
		prW = math.Pow(prW, cc)
	}
	wincr := wincr2
	if w > wlar {
		wincr = wincr1
	}
	blb := qsqz
	binc := (bb - qsqz) / wincr
	bub := blb + binc
	einsum := 0.0
	cc1 := cc - 1
	for wi := 1; float64(wi) <= wincr; wi++ {
		elsum := 0.0
		a := 0.5 * (bub + blb)
		b := 0.5 * (bub - blb)
		for jj := 1; jj <= nleg; jj++ {
			var j int
			var xx float64
			if ihalf < jj {
				j = nleg - jj + 1
				xx = xleg[j-1]
			} else {
				j = jj
				xx = -xleg[j-1]
			}
			c := b * xx
			ac := a + c
			qexpo := ac * ac
			if qexpo > C3 {
				break
			}
			pplus := 2 * NormCDF(ac)
			pminus := 2 * NormCDF(ac-w)
			rinsum := pplus*0.5 - pminus*0.5
			if rinsum >= math.Exp(C1/cc1) {
				rinsum = aleg[j-1] * math.Exp(-0.5*qexpo) * math.Pow(rinsum, cc1)
				elsum += rinsum
			}
		}
		elsum *= (2 * b) * cc / math.Sqrt(2*math.Pi)
		einsum += elsum
		blb = bub
		bub += binc
	}
	prW += einsum
	if prW <= math.Exp(C1/rr) {
		return 0
	}
	prW = math.Pow(prW, rr)
	if prW >= 1 {
		return 1
	}
	return prW
}

// lmPTukey is P(Q <= q) for the studentized range with cc means, df degrees
// of freedom and rr ranges (1).
func lmPTukey(q, rr, cc, df float64) float64 {
	const (
		nlegq  = 16
		ihalfq = 8
		eps1   = -30.0
		eps2   = 1.0e-14
		dhaf   = 100.0
		dquar  = 800.0
		deigh  = 5000.0
		dlarg  = 25000.0
	)
	xlegq := [ihalfq]float64{0.989400934991649932596154173450, 0.944575023073232576077988415535, 0.865631202387831743880467897712, 0.755404408355003033895101194847, 0.617876244402643748446671764049, 0.458016777657227386342419442984, 0.281603550779258913230460501460, 0.950125098376374401853193354250e-1}
	alegq := [ihalfq]float64{0.271524594117540948517805724560e-1, 0.622535239386478928628438369944e-1, 0.951585116824927848099251076022e-1, 0.124628971255533872052476282192, 0.149595988816576732081501730547, 0.169156519395002538189312079030, 0.182603415044923588866763667969, 0.189450610455068496285396723208}
	if math.IsNaN(q) || math.IsNaN(df) {
		return math.NaN()
	}
	if q <= 0 {
		return 0
	}
	if df < 2 || rr < 1 || cc < 2 {
		return math.NaN()
	}
	if math.IsInf(q, 1) {
		return 1
	}
	if df > dlarg {
		return lmWprob(q, rr, cc)
	}
	f2 := df * 0.5
	lg, _ := math.Lgamma(f2)
	f2lf := f2*math.Log(df) - df*math.Ln2 - lg
	f21 := f2 - 1
	ff4 := df * 0.25
	var ulen float64
	switch {
	case df <= dhaf:
		ulen = 1
	case df <= dquar:
		ulen = 0.5
	case df <= deigh:
		ulen = 0.25
	default:
		ulen = 0.125
	}
	f2lf += math.Log(ulen)
	ans := 0.0
	for i := 1; i <= 50; i++ {
		otsum := 0.0
		twa1 := float64(2*i-1) * ulen
		for jj := 1; jj <= nlegq; jj++ {
			var j int
			var t1 float64
			if ihalfq < jj {
				j = jj - ihalfq - 1
				t1 = f2lf + f21*math.Log(twa1+xlegq[j]*ulen) - (xlegq[j]*ulen+twa1)*ff4
			} else {
				j = jj - 1
				t1 = f2lf + f21*math.Log(twa1-xlegq[j]*ulen) + (xlegq[j]*ulen-twa1)*ff4
			}
			if t1 >= eps1 {
				var qsqz float64
				if ihalfq < jj {
					qsqz = q * math.Sqrt((xlegq[j]*ulen+twa1)*0.5)
				} else {
					qsqz = q * math.Sqrt((-(xlegq[j]*ulen)+twa1)*0.5)
				}
				otsum += lmWprob(qsqz, rr, cc) * alegq[j] * math.Exp(t1)
			}
		}
		if float64(i)*ulen >= 1.0 && otsum <= eps2 {
			break
		}
		ans += otsum
	}
	if ans > 1 {
		ans = 1
	}
	return ans
}

func lmQTukeyInit(p, c, v float64) float64 {
	const (
		p0 = 0.322232421088
		q0 = 0.993484626060e-01
		p1 = -1.0
		q1 = 0.588581570495
		p2 = -0.342242088547
		q2 = 0.531103462366
		p3 = -0.204231210125
		q3 = 0.103537752850
		p4 = -0.453642210148e-04
		q4 = 0.38560700634e-02
		c1 = 0.8832
		c2 = 0.2368
		c3 = 1.214
		c4 = 1.208
		c5 = 1.4142
		vm = 120.0
	)
	ps := 0.5 - 0.5*p
	yi := math.Sqrt(math.Log(1.0 / (ps * ps)))
	t := yi + ((((yi*p4+p3)*yi+p2)*yi+p1)*yi+p0)/((((yi*q4+q3)*yi+q2)*yi+q1)*yi+q0)
	if v < vm {
		t += (t*t*t + t) / v / 4.0
	}
	q := c1 - c2*t
	if v < vm {
		q += -c3/v + c4*t/v
	}
	return t * (q*math.Log(c-1.0) + c5)
}

// lmQTukey is the p quantile of the studentized range (secant iteration).
func lmQTukey(p, rr, cc, df float64) float64 {
	if p <= 0 {
		return 0
	}
	if p >= 1 {
		return math.Inf(1)
	}
	x0 := lmQTukeyInit(p, cc, df)
	v0 := lmPTukey(x0, rr, cc, df) - p
	var x1 float64
	if v0 > 0 {
		x1 = math.Max(0, x0-1)
	} else {
		x1 = x0 + 1
	}
	v1 := lmPTukey(x1, rr, cc, df) - p
	ans := x1
	for iter := 1; iter < 100; iter++ {
		if v1 == v0 {
			break
		}
		ans = x1 - v1*(x1-x0)/(v1-v0)
		v0 = v1
		x0 = x1
		if ans < 0 {
			ans = 0
			v1 = -p
		}
		v1 = lmPTukey(ans, rr, cc, df) - p
		x1 = ans
		if math.Abs(x1-x0) < 1e-10 {
			break
		}
	}
	return ans
}

// ---------------------------------------------------------------- diagnostics plots

// lmDiagData holds the data for regression diagnostics plots.
type lmDiagData struct {
	Dep              string
	Pred, Resid, RSt []float64
	Obs, Cook, H     []float64
	Y                []float64
	N, P             int
}

func lmDiagCollect(f *lmFit, X [][]float64, y, w []float64, dep string) *lmDiagData {
	d := &lmDiagData{Dep: dep, N: f.N, P: f.Rank}
	for i := range y {
		wi := 1.0
		if w != nil {
			wi = w[i]
		}
		o := f.obsStats(X[i], y[i], wi, 0.05)
		d.Pred = append(d.Pred, o.Pred)
		d.Resid = append(d.Resid, o.Resid)
		d.RSt = append(d.RSt, o.RStudent)
		d.Cook = append(d.Cook, o.CookD)
		d.H = append(d.H, o.H)
		d.Obs = append(d.Obs, float64(i+1))
		d.Y = append(d.Y, y[i])
	}
	return d
}

// lmPanel composes charts in a grid into one SVG.
func lmPanel(charts []*Chart, cols int, cw, ch float64) string {
	rows := (len(charts) + cols - 1) / cols
	W, H := cw*float64(cols), ch*float64(rows)
	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %s %s" width="%s" height="%s">`, f2(W), f2(H), f2(W), f2(H))
	for i, c := range charts {
		c.Width, c.Height = cw, ch
		svg := c.SVG()
		x := float64(i%cols) * cw
		y := float64(i/cols) * ch
		svg = strings.Replace(svg, `<svg xmlns="http://www.w3.org/2000/svg" `, fmt.Sprintf(`<svg x="%s" y="%s" `, f2(x), f2(y)), 1)
		b.WriteString(svg)
	}
	b.WriteString("</svg>")
	return b.String()
}

func lmQQ(r []float64) ([]float64, []float64) {
	x := NonMissing(r)
	sort.Float64s(x)
	n := float64(len(x))
	q := make([]float64, len(x))
	for i := range x {
		q[i] = NormInv((float64(i+1) - 0.375) / (n + 0.25))
	}
	return q, x
}

// lmDiagPanel builds the "Fit Diagnostics" panel.
func lmDiagPanel(d *lmDiagData) *Graph {
	mk := func(title, xl, yl string) *Chart {
		c := NewChart(title)
		c.XLabel, c.YLabel = xl, yl
		return c
	}
	c1 := mk("", "Predicted Value", "Residual")
	c1.Layers = []Layer{&RefLine{Axis: 'y', Value: 0, Color: "#888"}, &Scatter{X: d.Pred, Y: d.Resid, Color: PaletteColor(0), Marker: "circle"}}
	c2 := mk("", "Predicted Value", "RStudent")
	c2.Layers = []Layer{&RefLine{Axis: 'y', Value: 2, Color: "#888"}, &RefLine{Axis: 'y', Value: -2, Color: "#888"}, &Scatter{X: d.Pred, Y: d.RSt, Color: PaletteColor(0), Marker: "circle"}}
	c3 := mk("", "Leverage", "RStudent")
	c3.Layers = []Layer{&RefLine{Axis: 'x', Value: 2 * float64(d.P) / float64(d.N), Color: "#888"}, &Scatter{X: d.H, Y: d.RSt, Color: PaletteColor(0), Marker: "circle"}}
	qx, qy := lmQQ(d.Resid)
	c4 := mk("", "Quantile", "Residual")
	sd := StdDev(NonMissing(d.Resid))
	c4.Layers = []Layer{&Scatter{X: qx, Y: qy, Color: PaletteColor(0), Marker: "circle"}}
	if len(qx) > 1 {
		c4.Layers = append(c4.Layers, &Line{X: []float64{qx[0], qx[len(qx)-1]}, Y: []float64{qx[0] * sd, qx[len(qx)-1] * sd}, Color: "#A23A2E"})
	}
	c5 := mk("", "Predicted Value", d.Dep)
	lo, hi := minmax(d.Pred, NonMissing(d.Y))
	c5.Layers = []Layer{&Line{X: []float64{lo, hi}, Y: []float64{lo, hi}, Color: "#888"}, &Scatter{X: d.Pred, Y: d.Y, Color: PaletteColor(0), Marker: "circle"}}
	c6 := mk("", "Observation", "Cook's D")
	var x0, x1, y0, y1 []float64
	for i := range d.Cook {
		if !math.IsNaN(d.Cook[i]) {
			x0 = append(x0, d.Obs[i])
			x1 = append(x1, d.Obs[i])
			y0 = append(y0, 0)
			y1 = append(y1, d.Cook[i])
		}
	}
	c6.Layers = []Layer{&RefLine{Axis: 'y', Value: 4 / float64(d.N), Color: "#888"}, &Scatter{X: x1, Y: y1, Color: PaletteColor(0), Marker: "circle", Size: 3}}
	c7 := mk("", "Residual", "Percent")
	r := NonMissing(d.Resid)
	if len(r) > 0 {
		lo, hi := minmax(r)
		nb := int(math.Ceil(math.Log2(float64(len(r))) + 1))
		if nb < 1 {
			nb = 1
		}
		wd := (hi - lo) / float64(nb)
		if wd == 0 {
			wd = 1
		}
		var b0, b1, h0, h1 []float64
		for k := range nb {
			cnt := 0
			for _, v := range r {
				j := int((v - lo) / wd)
				if j >= nb {
					j = nb - 1
				}
				if j == k {
					cnt++
				}
			}
			b0 = append(b0, lo+float64(k)*wd)
			b1 = append(b1, lo+float64(k+1)*wd)
			h0 = append(h0, 0)
			h1 = append(h1, 100*float64(cnt)/float64(len(r)))
		}
		c7.Layers = []Layer{&Bars{X0: b0, X1: b1, Y0: h0, Y1: h1, Color: "#A6B4D6", Outline: "#445694"}}
		var nx, ny []float64
		for k := 0; k <= 60; k++ {
			v := lo + (hi-lo)*float64(k)/60
			nx = append(nx, v)
			ny = append(ny, 100*wd*NormPDF(v/sd)/sd)
		}
		c7.Layers = append(c7.Layers, &Line{X: nx, Y: ny, Color: "#A23A2E"})
	}
	g := &Graph{ODSName: "DiagnosticsPanel", Caption: "Fit Diagnostics for " + d.Dep,
		SVG: lmPanel([]*Chart{c1, c2, c3, c4, c5, c6, c7}, 3, 330, 260)}
	return g
}

// lmFitPlot draws data, fitted line and CLM/CLI bands for one regressor.
func lmFitPlot(f *lmFit, xs, ys []float64, xname, dep string, alpha float64, rowOf func(x float64) []float64) *Graph {
	lo, hi := minmax(xs)
	var gx, p, lm, um, li, ui []float64
	for k := 0; k <= 100; k++ {
		x := lo + (hi-lo)*float64(k)/100
		o := f.obsStats(rowOf(x), math.NaN(), 1, alpha)
		gx = append(gx, x)
		p = append(p, o.Pred)
		lm = append(lm, o.LCLM)
		um = append(um, o.UCLM)
		li = append(li, o.LCL)
		ui = append(ui, o.UCL)
	}
	c := NewChart("Fit Plot for " + dep)
	c.XLabel, c.YLabel = xname, dep
	pc := fmt.Sprintf("%g%%", 100*(1-alpha))
	c.Layers = []Layer{
		&Band{X: gx, Lo: lm, Hi: um, Color: "#B9CFE7", Opacity: 0.7},
		&Scatter{X: xs, Y: ys, Color: PaletteColor(0), Marker: "circle"},
		&Line{X: gx, Y: p, Color: "#A23A2E", Width: 2},
		&Line{X: gx, Y: li, Color: "#445694", Dash: true},
		&Line{X: gx, Y: ui, Color: "#445694", Dash: true},
	}
	c.Legend = []LegendEntry{{Label: "Fit", Color: "#A23A2E", Kind: "line"}, {Label: pc + " Confidence Limits", Color: "#B9CFE7", Kind: "fill"}, {Label: pc + " Prediction Limits", Color: "#445694", Kind: "line", Dash: true}}
	return &Graph{ODSName: "FitPlot", Caption: "Fit Plot", SVG: c.SVG()}
}
