package sas

import (
	"fmt"
	"html"
	"math"
	"sort"
	"strconv"
	"strings"
)

func init() { RegisterProc("CORR", crRun) }

type crProc struct {
	s                          *Session
	d                          *Dataset
	vars, with, partial        []int
	pearson, spearman, kendall bool
	nosimple, noprob, nomiss   bool
	cov, csscp, nocorr         bool
	outs                       map[string]*DSRef // "PEARSON"/"SPEARMAN"/"KENDALL" -> OUT ref
	outDS                      map[string]*Dataset
}

// crMatrix is a computed coefficient matrix (rows x cols variables).
type crMatrix struct {
	R, P, N [][]float64
}

func crRun(s *Session, p *ProcStep) error {
	d, err := s.ProcData(p)
	if err != nil {
		return err
	}
	o := p.Opts
	c := &crProc{s: s, d: d, outs: map[string]*DSRef{}, outDS: map[string]*Dataset{}}
	c.spearman = o.Has("SPEARMAN")
	c.kendall = o.Has("KENDALL")
	c.spearman = c.spearman || o.Has("OUTS")
	c.kendall = c.kendall || o.Has("OUTK")
	c.pearson = o.Has("PEARSON", "OUTP", "OUT") || !(c.spearman || c.kendall || o.Has("HOEFFDING"))
	c.nosimple, c.noprob, c.nomiss = o.Has("NOSIMPLE"), o.Has("NOPROB"), o.Has("NOMISS")
	c.cov, c.csscp, c.nocorr = o.Has("COV"), o.Has("CSSCP"), o.Has("NOCORR")
	if o.Has("HOEFFDING") {
		s.Log.Warn(p.Line, "The HOEFFDING option is not supported by sago and is ignored.")
	}
	if o.Has("ALPHA") {
		s.Log.Warn(p.Line, "Cronbach's coefficient alpha (ALPHA option) is not supported by sago and is ignored.")
	}
	for key, method := range map[string]string{"OUTP": "PEARSON", "OUT": "PEARSON", "OUTS": "SPEARMAN", "OUTK": "KENDALL"} {
		if op, ok := o.Get(key); ok {
			ref, err := DSRefFromOpt(op)
			if err != nil {
				return err
			}
			c.outs[method] = ref
		}
	}
	list := func(kw string) ([]int, error) {
		st := p.First(kw)
		if st == nil {
			return nil, nil
		}
		names, err := ExpandVarList(d, st.Args())
		if err != nil {
			return nil, err
		}
		idx, err := d.Indices(names)
		if err != nil {
			return nil, err
		}
		for _, i := range idx {
			if d.Vars[i].Char {
				return nil, fmt.Errorf("Variable %s in list does not match type prescribed for this list.", d.Vars[i].Name)
			}
		}
		return idx, nil
	}
	if c.with, err = list("WITH"); err != nil {
		return err
	}
	if c.partial, err = list("PARTIAL"); err != nil {
		return err
	}
	if c.vars, err = list("VAR"); err != nil {
		return err
	}
	if c.vars == nil {
		used := map[int]bool{}
		for _, i := range append(append([]int{}, c.with...), c.partial...) {
			used[i] = true
		}
		if st := p.First("BY"); st != nil {
			if b, err := ParseBy(d, st.Args()); err == nil {
				for _, i := range b.Idx {
					used[i] = true
				}
			}
		}
		for _, kw := range []string{"FREQ", "WEIGHT"} {
			if idx, err := list(kw); err == nil {
				for _, i := range idx {
					used[i] = true
				}
			}
		}
		for i, v := range d.Vars {
			if !v.Char && !used[i] {
				c.vars = append(c.vars, i)
			}
		}
	}
	if len(c.vars) == 0 {
		return fmt.Errorf("No numeric variables to analyze.")
	}
	if c.partial != nil {
		c.nomiss = true
	}
	by, groups, err := s.ProcBy(p, d)
	if err != nil {
		return err
	}
	for _, g := range groups {
		if by != nil {
			s.Out.SetBy(by.Vars, g.Keys)
		}
		c.group(g.Rows, by, g.Keys)
	}
	if by != nil {
		s.Out.SetBy(nil, nil)
	}
	for _, method := range []string{"PEARSON", "SPEARMAN", "KENDALL"} {
		ref := c.outs[method]
		if ref == nil || c.outDS[method] == nil {
			continue
		}
		ds := c.outDS[method]
		ds.Name = ref.Name
		ds.Label = map[string]string{"PEARSON": "Pearson Correlation Matrix", "SPEARMAN": "Spearman Correlation Matrix", "KENDALL": "Kendall Correlation Matrix"}[method]
		if err := s.WriteDS(ref, ds); err != nil {
			return err
		}
	}
	return nil
}

// crCols returns the numeric columns of rows for the given variables.
func crCols(rows [][]Value, idx []int) [][]float64 {
	out := make([][]float64, len(idx))
	for k, i := range idx {
		col := make([]float64, len(rows))
		for r, row := range rows {
			col[r] = row[i].Num
		}
		out[k] = col
	}
	return out
}

func (c *crProc) group(rows [][]Value, by *ByInfo, byKeys []Value) {
	all := append(append(append([]int{}, c.vars...), c.with...), c.partial...)
	if c.nomiss {
		var keep [][]Value
		for _, r := range rows {
			ok := true
			for _, i := range all {
				if math.IsNaN(r[i].Num) {
					ok = false
					break
				}
			}
			if ok {
				keep = append(keep, r)
			}
		}
		rows = keep
	}
	out := c.s.Out
	d := c.d
	names := func(idx []int) string {
		var n []string
		for _, i := range idx {
			n = append(n, d.Vars[i].Name)
		}
		return strings.Join(n, " ")
	}
	vi := &Table{ODSName: "VarInformation", Cols: []Col{{Name: "Label1", Label: " ", Char: true}, {Name: "cValue1", Label: " ", Char: true}}}
	plural := func(n int, w string) string {
		if n == 1 {
			return fmt.Sprintf("%d %s Variable:", n, w)
		}
		return fmt.Sprintf("%d %s Variables:", n, w)
	}
	if c.with != nil {
		vi.AddRow(strings.TrimSpace(strings.ReplaceAll(plural(len(c.with), "With"), "  ", " ")), names(c.with))
	}
	if c.partial != nil {
		vi.AddRow(plural(len(c.partial), "Partial"), names(c.partial))
	}
	vi.AddRow(strings.ReplaceAll(plural(len(c.vars), ""), "  ", " "), names(c.vars))
	out.AddTable(vi)

	cols := crCols(rows, all)
	nv, nw := len(c.vars), len(c.with)
	colOf := func(k int) []float64 { return cols[k] }

	// Partial statistics (listwise rows).
	var pcss [][]float64 // partial CSSCP over vars+with
	k := len(c.partial)
	n := float64(len(rows))
	if k > 0 {
		pcss = crPartialCSS(cols, nv+nw, k)
	}

	if !c.nosimple {
		c.simpleStats(all, cols, pcss, n, k)
	}

	rowIdx, colIdx := c.matrixAxes()
	if c.csscp && c.pearson {
		c.addCrossTable("Csscp", "Corrected SSCP Matrix", rowIdx, colIdx, cols, pcss, n, false)
	}
	if c.cov && c.pearson {
		c.addCrossTable("Cov", "Covariance Matrix", rowIdx, colIdx, cols, pcss, n, true)
	}
	if c.pearson && !c.nocorr {
		m := c.pearsonMatrix(rowIdx, colIdx, colOf, pcss, n, k)
		c.addMatrix("Pearson", m, rowIdx, colIdx, k, true)
		c.addOut("PEARSON", m, rowIdx, colIdx, cols, by, byKeys)
	}
	if c.spearman {
		rk := make([][]float64, len(cols))
		if c.nomiss {
			for i, col := range cols {
				rk[i] = Ranks(col)
			}
		}
		var m *crMatrix
		if k > 0 {
			pr := crPartialCSS(rk, nv+nw, k)
			m = c.partialMatrix(pr, rowIdx, colIdx, n, k)
		} else {
			m = c.pairMatrix(rowIdx, colIdx, cols, func(x, y []float64) float64 {
				return crPearson(Ranks(x), Ranks(y))
			}, crTProb)
		}
		c.addMatrix("Spearman", m, rowIdx, colIdx, k, true)
		c.addOut("SPEARMAN", m, rowIdx, colIdx, cols, by, byKeys)
	}
	if c.kendall {
		var m *crMatrix
		if k > 0 {
			// Partial tau-b from the tau-b matrix of all variables.
			tot := len(cols)
			tau := make([][]float64, tot)
			for i := range tot {
				tau[i] = make([]float64, tot)
				for j := range tot {
					if i == j {
						tau[i][j] = 1
					} else {
						tau[i][j], _ = crKendall(cols[i], cols[j])
					}
				}
			}
			pt := crPartialFromCorr(tau, nv+nw, k)
			m = c.partialMatrix(pt, rowIdx, colIdx, n, k)
			for i := range m.P {
				for j := range m.P[i] {
					m.P[i][j] = math.NaN()
				}
			}
		} else {
			m = c.pairMatrix(rowIdx, colIdx, cols, func(x, y []float64) float64 {
				t, _ := crKendall(x, y)
				return t
			}, func(x, y []float64, r, nn float64) float64 {
				_, p := crKendall(x, y)
				return p
			})
		}
		c.addMatrix("Kendall", m, rowIdx, colIdx, k, k == 0)
		c.addOut("KENDALL", m, rowIdx, colIdx, cols, by, byKeys)
	}
}

// matrixAxes returns indices (into the vars+with+partial column list) of
// matrix rows and columns: WITH x VAR, or VAR x VAR.
func (c *crProc) matrixAxes() ([]int, []int) {
	nv := len(c.vars)
	var cols, rows []int
	for i := range nv {
		cols = append(cols, i)
	}
	if c.with != nil {
		for i := range c.with {
			rows = append(rows, nv+i)
		}
	} else {
		rows = cols
	}
	return rows, cols
}

func (c *crProc) varAt(k int) *Var {
	all := append(append(append([]int{}, c.vars...), c.with...), c.partial...)
	return c.d.Vars[all[k]]
}

// crPartialCSS returns the partial CSSCP matrix of the first m columns after
// removing the linear effect of the last k columns: Sxx - Sxz Szz^-1 Szx.
func crPartialCSS(cols [][]float64, m, k int) [][]float64 {
	t := len(cols)
	css := make([][]float64, t)
	means := make([]float64, t)
	for i := range t {
		means[i] = Mean(cols[i])
	}
	for i := range t {
		css[i] = make([]float64, t)
	}
	for i := range t {
		for j := i; j < t; j++ {
			s := 0.0
			for r := range cols[i] {
				s += (cols[i][r] - means[i]) * (cols[j][r] - means[j])
			}
			css[i][j], css[j][i] = s, s
		}
	}
	return crPartialFromCorr(css, m, k)
}

// crPartialFromCorr partials out the last k of the first m+k rows/cols of a
// symmetric matrix by sweeping the controlling variables.
func crPartialFromCorr(a [][]float64, m, k int) [][]float64 {
	t := m + k
	w := make([][]float64, t)
	for i := range t {
		w[i] = append([]float64(nil), a[i][:t]...)
	}
	for z := m; z < t; z++ {
		if w[z][z] > 1e-12*math.Abs(a[z][z]) && w[z][z] != 0 {
			Sweep(w, z)
		}
	}
	out := make([][]float64, m)
	for i := range m {
		out[i] = append([]float64(nil), w[i][:m]...)
	}
	return out
}

func crPearson(x, y []float64) float64 {
	mx, my := Mean(x), Mean(y)
	sxy, sxx, syy := 0.0, 0.0, 0.0
	for i := range x {
		dx, dy := x[i]-mx, y[i]-my
		sxy += dx * dy
		sxx += dx * dx
		syy += dy * dy
	}
	return sxy / math.Sqrt(sxx*syy)
}

// crTProb is the two-sided p-value of r with n-2 degrees of freedom.
func crTProb(_, _ []float64, r, n float64) float64 {
	return crRProb(r, n-2)
}

func crRProb(r, df float64) float64 {
	if math.IsNaN(r) || df <= 0 {
		return math.NaN()
	}
	if math.Abs(r) >= 1 {
		return 0
	}
	return TTwoSided(r*math.Sqrt(df/(1-r*r)), df)
}

// crKendall computes Kendall's tau-b and its two-sided p-value from the
// asymptotic normal distribution of S with the tie-corrected variance.
func crKendall(x, y []float64) (float64, float64) {
	n := len(x)
	s := 0.0
	for i := range n {
		for j := i + 1; j < n; j++ {
			a := math.Copysign(1, x[i]-x[j])
			if x[i] == x[j] {
				a = 0
			}
			b := math.Copysign(1, y[i]-y[j])
			if y[i] == y[j] {
				b = 0
			}
			s += a * b
		}
	}
	fn := float64(n)
	n0 := fn * (fn - 1) / 2
	ties := func(v []float64) (t1, t2, t3 float64) {
		c := append([]float64(nil), v...)
		sort.Float64s(c)
		for i := 0; i < len(c); {
			j := i
			for j < len(c) && c[j] == c[i] {
				j++
			}
			t := float64(j - i)
			t1 += t * (t - 1) / 2
			t2 += t * (t - 1) * (2*t + 5)
			t3 += t * (t - 1) * (t - 2)
			i = j
		}
		return
	}
	n1, vt, ut := ties(x)
	n2, vu, uu := ties(y)
	tau := s / math.Sqrt((n0-n1)*(n0-n2))
	v0 := fn * (fn - 1) * (2*fn + 5)
	v := (v0-vt-vu)/18 + (2*n1*2*n2)/(2*fn*(fn-1)) + ut*uu/(9*fn*(fn-1)*(fn-2))
	if v <= 0 || n < 3 {
		return tau, math.NaN()
	}
	z := s / math.Sqrt(v)
	return tau, 2 * NormSF(math.Abs(z))
}

// pairMatrix computes a pairwise-complete coefficient matrix.
func (c *crProc) pairMatrix(rowIdx, colIdx []int, cols [][]float64, coef func(x, y []float64) float64, prob func(x, y []float64, r, n float64) float64) *crMatrix {
	m := crNewMatrix(len(rowIdx), len(colIdx))
	for a, ri := range rowIdx {
		for b, ci := range colIdx {
			var x, y []float64
			for r := range cols[ri] {
				if !math.IsNaN(cols[ri][r]) && !math.IsNaN(cols[ci][r]) {
					x = append(x, cols[ri][r])
					y = append(y, cols[ci][r])
				}
			}
			nn := float64(len(x))
			m.N[a][b] = nn
			if ri == ci {
				m.R[a][b] = 1
				if nn < 2 {
					m.R[a][b] = math.NaN()
				}
				continue
			}
			if nn < 2 {
				continue
			}
			m.R[a][b] = coef(x, y)
			if nn > 2 {
				m.P[a][b] = prob(x, y, m.R[a][b], nn)
			}
		}
	}
	return m
}

func crNewMatrix(r, c int) *crMatrix {
	m := &crMatrix{}
	for range r {
		rr, pp, nn := make([]float64, c), make([]float64, c), make([]float64, c)
		for j := range c {
			rr[j], pp[j] = math.NaN(), math.NaN()
		}
		m.R, m.P, m.N = append(m.R, rr), append(m.P, pp), append(m.N, nn)
	}
	return m
}

func (c *crProc) pearsonMatrix(rowIdx, colIdx []int, colOf func(int) []float64, pcss [][]float64, n float64, k int) *crMatrix {
	if k > 0 {
		return c.partialMatrix(pcss, rowIdx, colIdx, n, k)
	}
	cols := make([][]float64, 0)
	for i := range len(c.vars) + len(c.with) {
		cols = append(cols, colOf(i))
	}
	return c.pairMatrix(rowIdx, colIdx, cols, crPearson, crTProb)
}

// partialMatrix converts a partial CSSCP (or correlation) matrix to partial
// correlations with p-values on n-2-k degrees of freedom.
func (c *crProc) partialMatrix(pc [][]float64, rowIdx, colIdx []int, n float64, k int) *crMatrix {
	m := crNewMatrix(len(rowIdx), len(colIdx))
	for a, ri := range rowIdx {
		for b, ci := range colIdx {
			m.N[a][b] = n
			if pc[ri][ri] <= 0 || pc[ci][ci] <= 0 {
				continue
			}
			r := pc[ri][ci] / math.Sqrt(pc[ri][ri]*pc[ci][ci])
			if ri == ci {
				m.R[a][b] = 1
				continue
			}
			m.R[a][b] = r
			m.P[a][b] = crRProb(r, n-2-float64(k))
		}
	}
	return m
}

func (c *crProc) simpleStats(all []int, cols [][]float64, pcss [][]float64, n float64, k int) {
	d := c.d
	hasLabel := false
	for _, i := range all {
		if d.Vars[i].Label != "" {
			hasLabel = true
		}
	}
	np := c.spearman || c.kendall
	tb := &Table{ODSName: "SimpleStats", Caption: "Simple Statistics", RowHeader: 1}
	tb.Cols = []Col{{Name: "Variable", Label: "Variable", Char: true}, {Name: "NObs", Label: "N"}, {Name: "Mean", Label: "Mean", Format: "BEST10."},
		{Name: "StdDev", Label: "Std Dev", Format: "BEST10."}}
	if c.pearson {
		tb.Cols = append(tb.Cols, Col{Name: "Sum", Label: "Sum", Format: "BEST10."})
	}
	if np {
		tb.Cols = append(tb.Cols, Col{Name: "Median", Label: "Median", Format: "BEST10."})
	}
	tb.Cols = append(tb.Cols, Col{Name: "Min", Label: "Minimum", Format: "BEST10."}, Col{Name: "Max", Label: "Maximum", Format: "BEST10."})
	partialCols := k > 0 && c.pearson
	if partialCols {
		tb.Cols = append(tb.Cols, Col{Name: "PartialVar", Label: "Partial Variance", Format: "BEST10."}, Col{Name: "PartialStdDev", Label: "Partial Std Dev", Format: "BEST10."})
	}
	if hasLabel {
		tb.Cols = append(tb.Cols, Col{Name: "Label", Label: "Label", Char: true})
	}
	for j, i := range all {
		x := Sorted(cols[j])
		row := []any{d.Vars[i].Name, len(x), Mean(x), StdDev(x)}
		if c.pearson {
			row = append(row, Sum(x))
		}
		if np {
			row = append(row, Percentile(x, 50))
		}
		mn, mx := math.NaN(), math.NaN()
		if len(x) > 0 {
			mn, mx = x[0], x[len(x)-1]
		}
		row = append(row, mn, mx)
		if partialCols {
			if j < len(pcss) {
				v := pcss[j][j] / (n - float64(k) - 1)
				row = append(row, v, math.Sqrt(v))
			} else {
				row = append(row, nil, nil)
			}
		}
		if hasLabel {
			row = append(row, d.Vars[i].Label)
		}
		tb.AddRow(row...)
	}
	c.s.Out.AddTable(tb)
}

// addCrossTable prints the CSSCP or covariance matrix (pairwise or partial).
func (c *crProc) addCrossTable(ods, caption string, rowIdx, colIdx []int, cols [][]float64, pcss [][]float64, n float64, cov bool) {
	tb := &Table{ODSName: ods, RowHeader: 1}
	tb.Cols = []Col{{Name: "Variable", Label: " ", Char: true}}
	for _, ci := range colIdx {
		tb.Cols = append(tb.Cols, Col{Name: c.varAt(ci).Name, Label: c.varAt(ci).Name, Format: "BEST10."})
	}
	k := float64(len(c.partial))
	dfs := map[float64]bool{}
	for _, ri := range rowIdx {
		row := []any{c.varAt(ri).Name}
		for _, ci := range colIdx {
			var v, nn float64
			if pcss != nil {
				v, nn = pcss[ri][ci], n
			} else {
				var x, y []float64
				for r := range cols[ri] {
					if !math.IsNaN(cols[ri][r]) && !math.IsNaN(cols[ci][r]) {
						x = append(x, cols[ri][r])
						y = append(y, cols[ci][r])
					}
				}
				mx, my := Mean(x), Mean(y)
				for r := range x {
					v += (x[r] - mx) * (y[r] - my)
				}
				nn = float64(len(x))
			}
			if cov {
				df := nn - 1 - k
				dfs[df] = true
				v /= df
			}
			row = append(row, v)
		}
		tb.AddRow(row...)
	}
	tb.Caption = caption
	if pcss != nil {
		tb.Caption = strings.Replace(caption, " Matrix", "", 1)
		tb.Caption = "Partial " + tb.Caption + " Matrix"
	}
	if cov && len(dfs) == 1 {
		for df := range dfs {
			tb.Caption += ", DF = " + strconv.FormatFloat(df, 'f', -1, 64)
		}
	}
	c.s.Out.AddTable(tb)
}

// addMatrix prints a correlation matrix (r over p-value over N) and captures
// it under the SAS ODS name (PearsonCorr, SpearmanCorr, KendallCorr,
// PartialPearsonCorr, ...).
func (c *crProc) addMatrix(method string, m *crMatrix, rowIdx, colIdx []int, k int, probs bool) {
	ods := method + "Corr"
	label := map[string]string{"Pearson": "Pearson", "Spearman": "Spearman", "Kendall": "Kendall Tau b"}[method]
	stat := map[string]string{"Pearson": "|r|", "Spearman": "|r|", "Kendall": "|tau|"}[method]
	h0 := map[string]string{"Pearson": "Rho=0", "Spearman": "Rho=0", "Kendall": "Tau=0"}[method]
	var caption []string
	if k > 0 {
		ods = "Partial" + ods
		if method == "Kendall" {
			label = "Kendall Partial Tau b"
		} else {
			label += " Partial"
		}
		h0 = "Partial " + h0
	}
	sameN := true
	n0 := math.NaN()
	for i := range m.N {
		for j := range m.N[i] {
			if math.IsNaN(n0) {
				n0 = m.N[i][j]
			} else if m.N[i][j] != n0 {
				sameN = false
			}
		}
	}
	head := label + " Correlation Coefficients"
	if sameN {
		head += fmt.Sprintf(", N = %d", int(n0))
	}
	caption = append(caption, head)
	showP := probs && !c.noprob
	if showP {
		caption = append(caption, "Prob > "+stat+" under H0: "+h0)
	}
	if !sameN {
		caption = append(caption, "Number of Observations")
	}

	hasLabel := false
	for _, ri := range rowIdx {
		if c.varAt(ri).Label != "" {
			hasLabel = true
		}
	}
	tb := &Table{ODSName: ods, Caption: strings.Join(caption, " / ")}
	tb.Cols = []Col{{Name: "Variable", Label: "Variable", Char: true}}
	if hasLabel {
		tb.Cols = append(tb.Cols, Col{Name: "Label", Label: "Label", Char: true})
	}
	for _, ci := range colIdx {
		tb.Cols = append(tb.Cols, Col{Name: c.varAt(ci).Name, Label: c.varAt(ci).Name, Format: "8.5"})
	}
	if showP {
		for _, ci := range colIdx {
			tb.Cols = append(tb.Cols, Col{Name: "P" + c.varAt(ci).Name, Format: "PVALUE6.4"})
		}
	}
	if !sameN {
		for _, ci := range colIdx {
			tb.Cols = append(tb.Cols, Col{Name: "N" + c.varAt(ci).Name})
		}
	}
	for a, ri := range rowIdx {
		row := []any{c.varAt(ri).Name}
		if hasLabel {
			row = append(row, c.varAt(ri).Label)
		}
		for b := range colIdx {
			row = append(row, m.R[a][b])
		}
		if showP {
			for b := range colIdx {
				row = append(row, m.P[a][b])
			}
		}
		if !sameN {
			for b := range colIdx {
				row = append(row, m.N[a][b])
			}
		}
		tb.AddRow(row...)
	}

	var b strings.Builder
	esc := html.EscapeString
	b.WriteString("<table class=\"sas\"><caption>")
	for i, l := range caption {
		if i > 0 {
			b.WriteString("<br>")
		}
		b.WriteString(esc(l))
	}
	b.WriteString("</caption><thead><tr><th></th>")
	for _, ci := range colIdx {
		fmt.Fprintf(&b, "<th>%s</th>", esc(c.varAt(ci).Name))
	}
	b.WriteString("</tr></thead><tbody>")
	for a, ri := range rowIdx {
		v := c.varAt(ri)
		fmt.Fprintf(&b, "<tr><th>%s", esc(v.Name))
		if v.Label != "" {
			fmt.Fprintf(&b, "<br>%s", esc(v.Label))
		}
		b.WriteString("</th>")
		for bb := range colIdx {
			b.WriteString("<td>")
			b.WriteString(esc(crFmt(m.R[a][bb], "8.5")))
			if showP {
				b.WriteString("<br>" + esc(crFmt(m.P[a][bb], "PVALUE6.4")))
			}
			if !sameN {
				b.WriteString("<br>" + esc(crFmt(m.N[a][bb], "")))
			}
			b.WriteString("</td>")
		}
		b.WriteString("</tr>")
	}
	b.WriteString("</tbody></table>\n")
	c.s.Out.AddHTML(&HTML{HTML: b.String(), Table: tb})
}

func crFmt(v float64, f string) string {
	if math.IsNaN(v) {
		return ""
	}
	return strings.TrimSpace(FormatByName(NumV(v), f))
}

// addOut appends TYPE=CORR rows (MEAN, STD, N, CORR) for an OUTP=/OUTS=/OUTK= data set.
func (c *crProc) addOut(method string, m *crMatrix, rowIdx, colIdx []int, cols [][]float64, by *ByInfo, byKeys []Value) {
	if c.outs[method] == nil {
		return
	}
	ds := c.outDS[method]
	if ds == nil {
		ds = NewDataset("")
		if by != nil {
			for _, v := range by.Vars {
				ds.AddVar(v.Clone())
			}
		}
		ds.AddVar(&Var{Name: "_TYPE_", Char: true, Length: 8})
		ds.AddVar(&Var{Name: "_NAME_", Char: true, Length: 32})
		for _, ci := range colIdx {
			v := c.varAt(ci)
			ds.AddVar(&Var{Name: v.Name, Length: 8, Label: v.Label, Format: v.Format})
		}
		c.outDS[method] = ds
	}
	add := func(typ, name string, vals []float64) {
		row := append([]Value(nil), byKeys...)
		row = append(row, StrV(typ), StrV(name))
		for _, v := range vals {
			row = append(row, NumV(v))
		}
		ds.Rows = append(ds.Rows, row)
	}
	stat := func(f func(x []float64) float64) []float64 {
		var out []float64
		for _, ci := range colIdx {
			out = append(out, f(NonMissing(cols[ci])))
		}
		return out
	}
	if method == "PEARSON" && c.cov && len(c.partial) == 0 {
		for _, ri := range rowIdx {
			var vals []float64
			for _, ci := range colIdx {
				x, y := cols[ri], cols[ci]
				var xs, ys []float64
				for r := range x {
					if !math.IsNaN(x[r]) && !math.IsNaN(y[r]) {
						xs, ys = append(xs, x[r]), append(ys, y[r])
					}
				}
				mx, my := Mean(xs), Mean(ys)
				s := 0.0
				for r := range xs {
					s += (xs[r] - mx) * (ys[r] - my)
				}
				vals = append(vals, s/float64(len(xs)-1))
			}
			add("COV", c.varAt(ri).Name, vals)
		}
	}
	add("MEAN", "", stat(Mean))
	add("STD", "", stat(StdDev))
	add("N", "", stat(func(x []float64) float64 { return float64(len(x)) }))
	typ := "CORR"
	for a, ri := range rowIdx {
		add(typ, c.varAt(ri).Name, m.R[a])
	}
}
