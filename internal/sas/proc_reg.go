package sas

import (
	"fmt"
	"sort"
	"math"
	"strings"
)

func init() { RegisterProc("REG", rgRun) }

// rgModel is a parsed MODEL statement of PROC REG.
type rgModel struct {
	Label string
	Deps  []string
	Regs  []string
	Opts  *Opts
	Tests []*Stmt
	Outs  []*Stmt
	Line  int
}

func rgParse(s *Session, p *ProcStep, d *Dataset) ([]*rgModel, error) {
	var models []*rgModel
	n := 0
	for _, st := range p.Body {
		switch st.Keyword() {
		case "MODEL":
			n++
			args := st.Args()
			before, after := SplitSlash(args)
			eq := -1
			for i, t := range before {
				if t.IsOp("=") {
					eq = i
					break
				}
			}
			if eq < 0 {
				return nil, fmt.Errorf("Syntax error in MODEL statement: expecting '='.")
			}
			deps, err := ExpandVarList(d, before[:eq])
			if err != nil {
				return nil, err
			}
			regs, err := ExpandVarList(d, before[eq+1:])
			if err != nil {
				return nil, err
			}
			for _, v := range append(append([]string(nil), deps...), regs...) {
				if dv := d.Var(v); dv == nil || dv.Char {
					return nil, fmt.Errorf("Variable %s in list does not match type prescribed for this list.", strings.ToUpper(v))
				}
			}
			lbl := st.Label
			if lbl == "" {
				lbl = fmt.Sprintf("MODEL%d", n)
			}
			for i, r := range regs {
				regs[i] = d.Var(r).Name
			}
			for i, r := range deps {
				deps[i] = d.Var(r).Name
			}
			models = append(models, &rgModel{Label: lbl, Deps: deps, Regs: regs, Opts: ParseOpts(after), Line: st.Line})
		case "TEST":
			if len(models) == 0 {
				return nil, fmt.Errorf("TEST statement must follow a MODEL statement.")
			}
			m := models[len(models)-1]
			m.Tests = append(m.Tests, st)
		case "OUTPUT":
			if len(models) == 0 {
				return nil, fmt.Errorf("OUTPUT statement must follow a MODEL statement.")
			}
			m := models[len(models)-1]
			m.Outs = append(m.Outs, st)
		}
	}
	if len(models) == 0 {
		return nil, fmt.Errorf("No MODEL statement was specified.")
	}
	return models, nil
}

func rgRun(s *Session, p *ProcStep) error {
	d, err := s.ProcData(p)
	if err != nil {
		return err
	}
	models, err := rgParse(s, p, d)
	if err != nil {
		return err
	}
	noprint := p.Opts.Has("NOPRINT")
	plots := lmPlotsWanted(s, p.Opts)
	alphaP := p.Opts.Num("ALPHA", 0.05)
	wIdx := -1
	if st := p.First("WEIGHT"); st != nil && len(st.Args()) > 0 {
		wIdx = d.Index(st.Args()[0].Text)
		if wIdx < 0 {
			return fmt.Errorf("Variable %s not found.", strings.ToUpper(st.Args()[0].Text))
		}
	}
	var idIdx []int
	if st := p.First("ID"); st != nil {
		names, err := ExpandVarList(d, st.Args())
		if err != nil {
			return err
		}
		idIdx, _ = d.Indices(names)
	}
	// all model variables (listwise deletion across all models)
	used := map[int]bool{}
	for _, m := range models {
		for _, v := range append(append([]string(nil), m.Deps...), m.Regs...) {
			used[d.Index(v)] = true
		}
	}
	by, groups, err := s.ProcBy(p, d)
	if err != nil {
		return err
	}
	outs := map[*Stmt]*Dataset{}
	var outest *Dataset
	for _, g := range groups {
		if by != nil && !noprint {
			s.Out.SetBy(by.Vars, g.Keys)
		}
		complete := func(r []Value) bool {
			for i := range used {
				if r[i].IsMissing() {
					return false
				}
			}
			if wIdx >= 0 && (r[wIdx].IsMissing() || r[wIdx].Num <= 0) {
				return false
			}
			return true
		}
		var rows [][]Value
		for _, r := range g.Rows {
			if complete(r) {
				rows = append(rows, r)
			}
		}
		for _, m := range models {
			for _, dep := range m.Deps {
				rc := &rgCtx{s: s, p: p, d: d, m: m, dep: dep, all: g.Rows, rows: rows, wIdx: wIdx, idIdx: idIdx,
					noprint: noprint, plots: plots, alpha: m.Opts.Num("ALPHA", alphaP), by: by, keys: g.Keys}
				if err := rc.run(); err != nil {
					return err
				}
				for _, st := range m.Outs {
					od, err := rc.output(st)
					if err != nil {
						return err
					}
					if prev := outs[st]; prev != nil {
						od = rgAppendRows(prev, od)
					}
					outs[st] = od
				}
				if p.Opts.Has("OUTEST") {
					outest = rc.outest(outest)
				}
			}
		}
	}
	for _, m := range models {
		for _, st := range m.Outs {
			sp, err := lmParseOutput(st, 0.05)
			if err != nil {
				return err
			}
			if od := outs[st]; od != nil {
				if err := s.WriteDS(sp.Ref, od); err != nil {
					return err
				}
			}
		}
	}
	if outest != nil {
		op, _ := p.Opts.Get("OUTEST")
		ref, err := DSRefFromOpt(op)
		if err != nil {
			return err
		}
		if err := s.WriteDS(ref, outest); err != nil {
			return err
		}
	}
	return nil
}

func rgAppendRows(a, b *Dataset) *Dataset {
	a.Rows = append(a.Rows, b.Rows...)
	return a
}

type rgCtx struct {
	s       *Session
	p       *ProcStep
	d       *Dataset
	m       *rgModel
	dep     string
	all     [][]Value
	rows    [][]Value
	wIdx    int
	idIdx   []int
	noprint bool
	plots   bool
	alpha   float64
	by      *ByInfo
	keys    []Value
	regs    []string
	fit     *lmFit
	X       [][]float64
	y, w    []float64
	noint   bool
	fitSet  map[*Value]bool
}

func (rc *rgCtx) xrow(r []Value) []float64 {
	var x []float64
	if !rc.noint {
		x = append(x, 1)
	}
	for _, v := range rc.regs {
		f := r[rc.d.Index(v)].Num
		if math.IsNaN(f) {
			return nil
		}
		x = append(x, f)
	}
	return x
}

func (rc *rgCtx) build(regs []string) {
	rc.regs = regs
	rc.X, rc.y, rc.w = nil, nil, nil
	di := rc.d.Index(rc.dep)
	for _, r := range rc.rows {
		rc.X = append(rc.X, rc.xrow(r))
		rc.y = append(rc.y, r[di].Num)
		if rc.wIdx >= 0 {
			rc.w = append(rc.w, r[rc.wIdx].Num)
		}
	}
	ct := make([]int, len(regs))
	if !rc.noint {
		ct = append([]int{-1}, ct...)
	}
	for i := range ct {
		if !rc.noint {
			ct[i] = i - 1
		} else {
			ct[i] = i
		}
	}
	rc.fit = lmFitModel(rc.X, rc.y, rc.w, !rc.noint, ct, len(regs))
}

func (rc *rgCtx) table(t *Table) {
	if !rc.noprint {
		rc.s.Out.AddTable(t)
	}
}

func (rc *rgCtx) hidden() []Col {
	return []Col{{Name: "Model", Char: true, Hidden: true}, {Name: "Dependent", Char: true, Hidden: true}}
}

func (rc *rgCtx) pre(cells ...any) []any {
	return append([]any{rc.m.Label, rc.dep}, cells...)
}

func (rc *rgCtx) run() error {
	o := rc.m.Opts
	rc.noint = o.Has("NOINT")
	regs := rc.m.Regs
	if sel := strings.ToUpper(o.Str("SELECTION", "NONE")); sel != "NONE" {
		var err error
		regs, err = rc.selection(sel)
		if err != nil {
			return err
		}
	}
	rc.build(regs)
	f := rc.fit
	dv := rc.d.Var(rc.dep)
	if !rc.noprint {
		hdr := "Model: " + rc.m.Label + "\nDependent Variable: " + rc.dep
		if dv.Label != "" {
			hdr += " " + dv.Label
		}
		rc.s.Out.AddText(&Text{Text: hdr, Pre: true})
	}
	// NObs
	nt := &Table{ODSName: "NObs", Cols: append(rc.hidden(), Col{Name: "Label", Label: "", Char: true}, Col{Name: "N", Label: ""}), RowHeader: 3}
	nt.AddRow(rc.pre("Number of Observations Read", len(rc.all))...)
	nt.AddRow(rc.pre("Number of Observations Used", len(rc.rows))...)
	if miss := len(rc.all) - len(rc.rows); miss > 0 {
		nt.AddRow(rc.pre("Number of Observations with Missing Values", miss)...)
	}
	rc.table(nt)
	// ANOVA (+ lack of fit)
	dfm, dfe := f.DFM, f.DFE
	at := &Table{ODSName: "ANOVA", Caption: "Analysis of Variance", Cols: append(rc.hidden(),
		Col{Name: "Source", Label: "Source", Char: true}, Col{Name: "DF", Label: "DF"}, Col{Name: "SS", Label: "Sum of Squares"},
		Col{Name: "MS", Label: "Mean Square"}, Col{Name: "FValue", Label: "F Value", Format: lmFmtF}, Col{Name: "ProbF", Label: "Pr > F", Format: lmFmtP}), RowHeader: 3}
	F := lmDiv(f.SSM/float64(dfm), f.MSE)
	at.AddRow(rc.pre("Model", dfm, f.SSM, f.SSM/float64(dfm), F, lmFProb(F, dfm, dfe))...)
	at.AddRow(rc.pre("Error", dfe, f.SSE, f.MSE, nil, nil)...)
	if o.Has("LACKFIT") {
		at.ODSName = "LackFitANOVA"
		pe, dfpe := rc.pureError()
		if dfpe > 0 && dfe-dfpe > 0 {
			lof := f.SSE - pe
			dfl := dfe - dfpe
			Fl := (lof / float64(dfl)) / (pe / float64(dfpe))
			at.AddRow(rc.pre("  Lack of Fit", dfl, lof, lof/float64(dfl), Fl, lmFProb(Fl, dfl, dfpe))...)
			at.AddRow(rc.pre("  Pure Error", dfpe, pe, pe/float64(dfpe), nil, nil)...)
		} else {
			rc.s.Log.Warn(rc.m.Line, "The LACKFIT option is ignored because there are no replicates (or no degrees of freedom for lack of fit).")
		}
	}
	tot := "Corrected Total"
	if rc.noint {
		tot = "Uncorrected Total"
	}
	at.AddRow(rc.pre(tot, dfm+dfe, f.TSS, nil, nil, nil)...)
	if rc.noint {
		at.Notes = append(at.Notes, "Note: No intercept in model. R-Square is redefined.")
	}
	rc.table(at)
	// Fit statistics
	rmse := math.Sqrt(f.MSE)
	r2 := 1 - f.SSE/f.TSS
	n := f.SumW
	adj := 1 - (1-r2)*(n-1)/float64(dfe)
	if rc.noint {
		adj = 1 - (1-r2)*n/float64(dfe)
	}
	cv := 100 * rmse / f.YMean
	ft := &Table{ODSName: "FitStatistics", Cols: append(rc.hidden(),
		Col{Name: "Label1", Char: true}, Col{Name: "cValue1", Char: true}, Col{Name: "nValue1", Hidden: true},
		Col{Name: "Label2", Char: true}, Col{Name: "cValue2", Char: true}, Col{Name: "nValue2", Hidden: true}), RowHeader: 3}
	// cValue strings carry SAS's display widths: 11.5 for the fit stats,
	// 7.4 for the R-square family
	ft.AddRow(rc.pre("Root MSE", fmt.Sprintf("%11.5f", rmse), rmse, "R-Square", fmt.Sprintf("%7.4f", r2), r2)...)
	ft.AddRow(rc.pre("Dependent Mean", fmt.Sprintf("%11.5f", f.YMean), f.YMean, "Adj R-Sq", fmt.Sprintf("%7.4f", adj), adj)...)
	ft.AddRow(rc.pre("Coeff Var", fmt.Sprintf("%11.5f", cv), cv, "", "", float64(0))...)
	rc.table(ft)
	rc.paramTable()
	if o.Has("COLLIN", "COLLINOINT") {
		rc.collin()
	}
	for i, st := range rc.m.Tests {
		if err := rc.test(st, i+1); err != nil {
			return err
		}
	}
	if o.Has("P", "R", "CLM", "CLI", "INFLUENCE") {
		rc.outputStats()
	}
	if rc.plots {
		rc.diagPlots()
	}
	return nil
}

func (rc *rgCtx) pureError() (float64, int) {
	type cell struct {
		sw, swy, swyy float64
	}
	cells := map[string]*cell{}
	for i, x := range rc.X {
		k := fmt.Sprint(x)
		c := cells[k]
		if c == nil {
			c = &cell{}
			cells[k] = c
		}
		wi := 1.0
		if rc.w != nil {
			wi = rc.w[i]
		}
		c.sw += wi
		c.swy += wi * rc.y[i]
	}
	pe := 0.0
	for i, x := range rc.X {
		c := cells[fmt.Sprint(x)]
		wi := 1.0
		if rc.w != nil {
			wi = rc.w[i]
		}
		e := rc.y[i] - c.swy/c.sw
		pe += wi * e * e
	}
	return pe, len(rc.X) - len(cells)
}

func (rc *rgCtx) paramNames() []string {
	var names []string
	if !rc.noint {
		names = append(names, "Intercept")
	}
	return append(names, rc.regs...)
}

func (rc *rgCtx) paramTable() {
	f := rc.fit
	o := rc.m.Opts
	names := rc.paramNames()
	hasLabel := false
	for _, v := range rc.regs {
		if rc.d.Var(v).Label != "" {
			hasLabel = true
		}
	}
	cols := append(rc.hidden(), Col{Name: "Variable", Label: "Variable", Char: true})
	if hasLabel {
		cols = append(cols, Col{Name: "Label", Label: "Label", Char: true})
	}
	cols = append(cols, Col{Name: "DF", Label: "DF"}, Col{Name: "Estimate", Label: "Parameter Estimate"}, Col{Name: "StdErr", Label: "Standard Error"},
		Col{Name: "tValue", Label: "t Value", Format: lmFmtT}, Col{Name: "Probt", Label: "Pr > |t|", Format: lmFmtP})
	t := &Table{ODSName: "ParameterEstimates", Caption: "Parameter Estimates", RowHeader: 3}
	clb := o.Has("CLB")
	pct := fmt.Sprintf("%g%%", 100*(1-rc.alpha))
	if o.Has("SS1") {
		cols = append(cols, Col{Name: "SS1", Label: "Type I SS"})
	}
	if o.Has("SS2") {
		cols = append(cols, Col{Name: "SS2", Label: "Type II SS"})
	}
	if o.Has("STB") {
		cols = append(cols, Col{Name: "StandardizedEst", Label: "Standardized Estimate"})
	}
	if o.Has("TOL") {
		cols = append(cols, Col{Name: "Tolerance", Label: "Tolerance"})
	}
	if o.Has("VIF") {
		cols = append(cols, Col{Name: "VarianceInflation", Label: "Variance Inflation"})
	}
	if clb {
		from := len(cols) - 2
		cols = append(cols, Col{Name: "LowerCL", Label: "Lower"}, Col{Name: "UpperCL", Label: "Upper"})
		t.Spans = []Span{{Label: pct + " Confidence Limits", From: from, To: from + 1}}
	}
	t.Cols = cols
	tcrit := TInv(1-rc.alpha/2, float64(f.DFE))
	sdY := math.Sqrt(f.Cross.A[f.Cross.P][f.Cross.P] / (f.SumW - 1))
	if rc.noint {
		sdY = math.Sqrt(rc.centered(len(names)) / (f.SumW - 1))
	}
	seq := 0
	for j, nm := range names {
		row := rc.pre(nm)
		if hasLabel {
			lbl := ""
			if nm == "Intercept" {
				lbl = "Intercept"
			} else {
				lbl = rc.d.Var(nm).Label
			}
			row = append(row, lbl)
		}
		isInt := nm == "Intercept" && !rc.noint && j == 0
		if f.Aliased[j] {
			row = append(row, 0, 0, nil, nil, nil)
		} else {
			b := f.Beta[j]
			se := math.Sqrt(f.G[j][j] * f.MSE)
			tv := b / se
			row = append(row, 1, b, se, tv, TTwoSided(tv, float64(f.DFE)))
		}
		if o.Has("SS1") {
			if isInt {
				row = append(row, f.SumW*f.YMean*f.YMean)
			} else {
				row = append(row, f.SeqSS[seq])
			}
		}
		if o.Has("SS2") {
			if isInt && false {
				row = append(row, nil)
			} else if f.Aliased[j] {
				row = append(row, nil)
			} else {
				row = append(row, f.Beta[j]*f.Beta[j]/f.G[j][j])
			}
		}
		var vif float64
		if isInt {
			vif = 0
		} else if rc.noint {
			vif = f.G[j][j] * f.Cross.UDiag[j]
		} else {
			vif = f.G[j][j] * f.Cross.A[j][j]
		}
		if o.Has("STB") {
			if isInt {
				row = append(row, 0)
			} else {
				sdx := math.Sqrt(rc.colCSS(j) / (f.SumW - 1))
				row = append(row, f.Beta[j]*sdx/sdY)
			}
		}
		if o.Has("TOL") {
			if isInt {
				row = append(row, nil)
			} else {
				row = append(row, 1/vif)
			}
		}
		if o.Has("VIF") {
			row = append(row, vif)
		}
		if clb {
			if f.Aliased[j] {
				row = append(row, nil, nil)
			} else {
				se := math.Sqrt(f.G[j][j] * f.MSE)
				row = append(row, f.Beta[j]-tcrit*se, f.Beta[j]+tcrit*se)
			}
		}
		if !isInt {
			seq++
		}
		t.AddRow(row...)
	}
	for _, a := range f.Aliased {
		if a {
			t.Notes = append(t.Notes, "Note: Model is not full rank. Least-squares solutions for the parameters are not unique. Some statistics will be misleading. A reported DF of 0 or B means that the estimate is biased.")
			break
		}
	}
	rc.table(t)
}

// colCSS returns the corrected sum of squares of design column j.
func (rc *rgCtx) colCSS(j int) float64 {
	if !rc.noint {
		return rc.fit.Cross.A[j][j]
	}
	m := 0.0
	for _, x := range rc.X {
		m += x[j]
	}
	m /= float64(len(rc.X))
	s := 0.0
	for _, x := range rc.X {
		s += (x[j] - m) * (x[j] - m)
	}
	return s
}

func (rc *rgCtx) centered(int) float64 {
	return CSS(rc.y)
}

func (rc *rgCtx) collin() {
	// eigenvalues of the scaled (unit-length columns) X'X
	f := rc.fit
	p := len(rc.paramNames())
	M := make([][]float64, p)
	for i := range M {
		M[i] = make([]float64, p)
	}
	for _, x := range rc.X {
		for i := range p {
			for j := range p {
				M[i][j] += x[i] * x[j]
			}
		}
	}
	sc := make([]float64, p)
	for i := range p {
		sc[i] = math.Sqrt(M[i][i])
	}
	for i := range p {
		for j := range p {
			M[i][j] /= sc[i] * sc[j]
		}
	}
	vals, vecs := lmJacobiEigen(M)
	_ = f
	t := &Table{ODSName: "CollinDiag", Caption: "Collinearity Diagnostics", RowHeader: 3}
	t.Cols = append(rc.hidden(), Col{Name: "Number", Label: "Number"}, Col{Name: "Eigenvalue", Label: "Eigenvalue"}, Col{Name: "ConditionIndex", Label: "Condition Index"})
	names := rc.paramNames()
	from := len(t.Cols) - 2
	for _, nm := range names {
		t.Cols = append(t.Cols, Col{Name: "Pct_" + nm, Label: nm, Format: "8.5"})
	}
	t.Spans = []Span{{Label: "Proportion of Variation", From: from + 1, To: from + len(names)}}
	phi := make([][]float64, p) // phi[k][j] = v_jk^2 / lambda_k
	tot := make([]float64, p)
	for k := range p {
		phi[k] = make([]float64, p)
		for j := range p {
			phi[k][j] = vecs[j][k] * vecs[j][k] / vals[k]
			tot[j] += phi[k][j]
		}
	}
	for k := range p {
		row := rc.pre(k+1, vals[k], math.Sqrt(vals[0]/vals[k]))
		for j := range p {
			row = append(row, phi[k][j]/tot[j])
		}
		t.AddRow(row...)
	}
	rc.table(t)
}

// lmJacobiEigen returns eigenvalues (descending) and eigenvectors (columns) of a symmetric matrix.
func lmJacobiEigen(a0 [][]float64) ([]float64, [][]float64) {
	n := len(a0)
	a := make([][]float64, n)
	v := make([][]float64, n)
	for i := range n {
		a[i] = append([]float64(nil), a0[i]...)
		v[i] = make([]float64, n)
		v[i][i] = 1
	}
	for sweep := 0; sweep < 100; sweep++ {
		off := 0.0
		for i := range n {
			for j := i + 1; j < n; j++ {
				off += a[i][j] * a[i][j]
			}
		}
		if off < 1e-30 {
			break
		}
		for p := range n {
			for q := p + 1; q < n; q++ {
				if math.Abs(a[p][q]) < 1e-300 {
					continue
				}
				theta := (a[q][q] - a[p][p]) / (2 * a[p][q])
				t := 1 / (math.Abs(theta) + math.Sqrt(theta*theta+1))
				if theta < 0 {
					t = -t
				}
				c := 1 / math.Sqrt(t*t+1)
				s := t * c
				for k := range n {
					akp, akq := a[k][p], a[k][q]
					a[k][p] = c*akp - s*akq
					a[k][q] = s*akp + c*akq
				}
				for k := range n {
					apk, aqk := a[p][k], a[q][k]
					a[p][k] = c*apk - s*aqk
					a[q][k] = s*apk + c*aqk
				}
				for k := range n {
					vkp, vkq := v[k][p], v[k][q]
					v[k][p] = c*vkp - s*vkq
					v[k][q] = s*vkp + c*vkq
				}
			}
		}
	}
	idx := make([]int, n)
	for i := range idx {
		idx[i] = i
	}
	for i := range n {
		for j := i + 1; j < n; j++ {
			if a[idx[j]][idx[j]] > a[idx[i]][idx[i]] {
				idx[i], idx[j] = idx[j], idx[i]
			}
		}
	}
	vals := make([]float64, n)
	vecs := make([][]float64, n)
	for i := range n {
		vecs[i] = make([]float64, n)
	}
	for k, c := range idx {
		vals[k] = a[c][c]
		for i := range n {
			vecs[i][k] = v[i][c]
		}
	}
	return vals, vecs
}

// test runs a TEST statement.
func (rc *rgCtx) test(st *Stmt, num int) error {
	args, _ := SplitSlash(st.Args())
	names := rc.paramNames()
	f := rc.fit
	var L [][]float64
	var C []float64
	// split by commas into equations; each equation may chain '='
	var eqs [][]Token
	cur := []Token{}
	depth := 0
	for _, t := range args {
		if t.IsOp("(") {
			depth++
		} else if t.IsOp(")") {
			depth--
		}
		if t.IsOp(",") && depth == 0 {
			eqs = append(eqs, cur)
			cur = []Token{}
			continue
		}
		cur = append(cur, t)
	}
	eqs = append(eqs, cur)
	for _, eq := range eqs {
		var sides [][]Token
		part := []Token{}
		for _, t := range eq {
			if t.IsOp("=") {
				sides = append(sides, part)
				part = []Token{}
				continue
			}
			part = append(part, t)
		}
		sides = append(sides, part)
		if len(sides) == 1 {
			sides = append(sides, nil)
		}
		lin := make([][]float64, len(sides))
		cons := make([]float64, len(sides))
		for i, sd := range sides {
			if len(sd) == 0 {
				lin[i] = make([]float64, len(names))
				continue
			}
			e, err := ParseExpr(sd)
			if err != nil {
				return fmt.Errorf("TEST statement: %v", err)
			}
			for _, v := range ExprVars(e) {
				found := false
				for _, nm := range names {
					if strings.EqualFold(nm, v) {
						found = true
					}
				}
				if !found {
					return fmt.Errorf("Variable %s is not a regressor in the model.", strings.ToUpper(v))
				}
			}
			vals := make([]float64, len(names))
			eval := func() float64 {
				ctx := &EvalCtx{Sess: rc.s, Get: func(n string) (Value, bool) {
					for j, nm := range names {
						if strings.EqualFold(nm, n) {
							return NumV(vals[j]), true
						}
					}
					return Missing, false
				}}
				return e.Eval(ctx).Num
			}
			c0 := eval()
			lin[i] = make([]float64, len(names))
			for j := range names {
				vals[j] = 1
				lin[i][j] = eval() - c0
				vals[j] = 0
			}
			cons[i] = c0
		}
		for i := 0; i+1 < len(sides); i++ {
			row := make([]float64, len(names))
			for j := range names {
				row[j] = lin[i][j] - lin[i+1][j]
			}
			L = append(L, row)
			C = append(C, cons[i+1]-cons[i])
		}
	}
	ss, q := f.testL(L, C)
	label := st.Label
	if label == "" {
		label = fmt.Sprintf("Test%d", num)
	}
	t := &Table{ODSName: "TestANOVA", Caption: fmt.Sprintf("Test %s Results for Dependent Variable %s", label, rc.dep), RowHeader: 4,
		Cols: append(rc.hidden(), Col{Name: "Test", Char: true, Hidden: true}, Col{Name: "Source", Label: "Source", Char: true}, Col{Name: "DF", Label: "DF"},
			Col{Name: "MS", Label: "Mean Square"}, Col{Name: "FValue", Label: "F Value", Format: lmFmtF}, Col{Name: "ProbF", Label: "Pr > F", Format: lmFmtP})}
	ms := ss / float64(q)
	F := ms / f.MSE
	t.AddRow(rc.pre(label, "Numerator", q, ms, F, lmFProb(F, q, f.DFE))...)
	t.AddRow(rc.pre(label, "Denominator", f.DFE, f.MSE, nil, nil)...)
	rc.table(t)
	return nil
}

func (rc *rgCtx) outputStats() {
	o := rc.m.Opts
	f := rc.fit
	di := rc.d.Index(rc.dep)
	pct := fmt.Sprintf("%g%%", 100*(1-rc.alpha))
	t := &Table{ODSName: "OutputStatistics", Caption: "Output Statistics", RowHeader: 3}
	cols := append(rc.hidden())
	for _, i := range rc.idIdx {
		cols = append(cols, Col{Name: rc.d.Vars[i].Name, Label: rc.d.Vars[i].Name, Char: rc.d.Vars[i].Char, Format: rc.d.Vars[i].Format})
	}
	cols = append(cols, Col{Name: "DepVar", Label: "Dependent Variable"}, Col{Name: "PredictedValue", Label: "Predicted Value"}, Col{Name: "StdErrMeanPredict", Label: "Std Error Mean Predict"})
	if o.Has("CLM") {
		from := len(cols) - 2
		cols = append(cols, Col{Name: "LowerCLMean", Label: "Lower"}, Col{Name: "UpperCLMean", Label: "Upper"})
		t.Spans = append(t.Spans, Span{Label: pct + " CL Mean", From: from, To: from + 1})
	}
	if o.Has("CLI") {
		from := len(cols) - 2
		cols = append(cols, Col{Name: "LowerCL", Label: "Lower"}, Col{Name: "UpperCL", Label: "Upper"})
		t.Spans = append(t.Spans, Span{Label: pct + " CL Predict", From: from, To: from + 1})
	}
	cols = append(cols, Col{Name: "Residual", Label: "Residual"})
	rOpt := o.Has("R")
	if rOpt {
		cols = append(cols, Col{Name: "StdErrResidual", Label: "Std Error Residual"}, Col{Name: "StudentResidual", Label: "Student Residual", Format: "7.3"}, Col{Name: "CooksD", Label: "Cook's D", Format: "7.3"})
	}
	if o.Has("INFLUENCE") {
		cols = append(cols, Col{Name: "RStudent", Label: "RStudent", Format: "8.4"}, Col{Name: "HatDiagonal", Label: "Hat Diag H", Format: "8.4"}, Col{Name: "DFFITS", Label: "DFFITS", Format: "8.4"})
	}
	t.Cols = cols
	sumR, sumR2, press := 0.0, 0.0, 0.0
	for _, r := range rc.all {
		x := rc.xrow(r)
		w := 1.0
		if rc.wIdx >= 0 {
			w = r[rc.wIdx].Num
		}
		y := r[di].Num
		var st lmObsStats
		inFit := rc.inFit(r)
		if inFit {
			st = f.obsStats(x, y, w, rc.alpha)
		} else {
			st = f.obsStats(x, math.NaN(), w, rc.alpha)
		}
		row := rc.pre()
		for _, k := range rc.idIdx {
			row = append(row, r[k])
		}
		row = append(row, y, st.Pred, st.StdP)
		if o.Has("CLM") {
			row = append(row, st.LCLM, st.UCLM)
		}
		if o.Has("CLI") {
			row = append(row, st.LCL, st.UCL)
		}
		row = append(row, st.Resid)
		if rOpt {
			row = append(row, st.StdR, st.Student, st.CookD)
		}
		if o.Has("INFLUENCE") {
			row = append(row, st.RStudent, st.H, st.Dffits)
		}
		t.AddRow(row...)
		if inFit {
			sumR += st.Resid
			sumR2 += st.Resid * st.Resid
			press += st.Press * st.Press
		}
	}
	rc.table(t)
	rs := &Table{ODSName: "ResidualStatistics", Caption: "", RowHeader: 3, Cols: append(rc.hidden(),
		Col{Name: "Label1", Char: true}, Col{Name: "cValue1", Char: true}, Col{Name: "nValue1"})}
	rs.AddRow(rc.pre("Sum of Residuals", fmt.Sprintf("%11.0f", sumR), sumR)...)
	rs.AddRow(rc.pre("Sum of Squared Residuals", fmt.Sprintf("%11.5f", sumR2), sumR2)...)
	rs.AddRow(rc.pre("Predicted Residual SS (PRESS)", fmt.Sprintf("%11.5f", press), press)...)
	rc.table(rs)
}

func (rc *rgCtx) inFit(r []Value) bool {
	if rc.fitSet == nil {
		rc.fitSet = map[*Value]bool{}
		for _, q := range rc.rows {
			rc.fitSet[&q[0]] = true
		}
	}
	return rc.fitSet[&r[0]]
}

// output builds the OUTPUT OUT= data set for this BY group.
func (rc *rgCtx) output(st *Stmt) (*Dataset, error) {
	sp, err := lmParseOutput(st, rc.alpha)
	if err != nil {
		return nil, err
	}
	od := &Dataset{Name: sp.Ref.Name}
	for _, v := range rc.d.Vars {
		od.Vars = append(od.Vars, v.Clone())
	}
	depIdx := 0
	for i, dp := range rc.m.Deps {
		if dp == rc.dep {
			depIdx = i
		}
	}
	type outVar struct {
		key string
		col int
	}
	var ovs []outVar
	// SAS writes OUTPUT statistics in its canonical order (P, R, STUDENT,
	// COOKD, H, PRESS, RSTUDENT, DFFITS, COVRATIO, DFBETAS), not statement
	// order, and labels the columns.
	rank := map[string]int{"P": 0, "R": 1, "STUDENT": 2, "COOKD": 3, "H": 4, "PRESS": 5, "RSTUDENT": 6, "DFFITS": 7, "COVRATIO": 8}
	labels := map[string]string{
		"P":        "Predicted Value of " + rc.dep,
		"R":        "Residual",
		"STUDENT":  "Studentized Residual",
		"COOKD":    "Cook's D Influence Statistic",
		"H":        "Leverage",
		"PRESS":    "Residual without Current Observation",
		"RSTUDENT": "Studentized Residual without Current Obs",
		"DFFITS":   "Standard Influence on Predicted Value",
		"COVRATIO": "Covariance Ratio",
	}
	items := make([]string, len(sp.Items))
	for i, it := range sp.Items {
		items[i] = it[0]
	}
	sort.SliceStable(items, func(a, b int) bool {
		ra, okA := rank[items[a]]
		if !okA {
			ra = 99
		}
		rb, okB := rank[items[b]]
		if !okB {
			rb = 99
		}
		return ra < rb
	})
	for _, key := range items {
		names := sp.Names[key]
		if depIdx >= len(names) {
			continue
		}
		col := od.AddVar(&Var{Name: names[depIdx], Length: 8, Label: labels[key]})
		ovs = append(ovs, outVar{key, col})
	}
	in := map[*Value]bool{}
	for _, r := range rc.rows {
		in[&r[0]] = true
	}
	di := rc.d.Index(rc.dep)
	for _, r := range rc.all {
		nr := make([]Value, len(od.Vars))
		copy(nr, r)
		for i := len(r); i < len(nr); i++ {
			nr[i] = Missing
		}
		x := rc.xrow(r)
		w := 1.0
		if rc.wIdx >= 0 {
			w = r[rc.wIdx].Num
		}
		y := r[di].Num
		if !in[&r[0]] {
			y = math.NaN()
		}
		stt := rc.fit.obsStats(x, y, w, sp.Alpha)
		for _, ov := range ovs {
			nr[ov.col] = NumV(stt.get(ov.key))
		}
		od.Rows = append(od.Rows, nr)
	}
	return od, nil
}

func (rc *rgCtx) outest(prev *Dataset) *Dataset {
	if prev == nil {
		prev = &Dataset{Name: "OUTEST"}
		if rc.by != nil {
			for _, v := range rc.by.Vars {
				prev.Vars = append(prev.Vars, v.Clone())
			}
		}
		prev.Vars = append(prev.Vars, &Var{Name: "_MODEL_", Char: true, Length: 8}, &Var{Name: "_TYPE_", Char: true, Length: 8},
			&Var{Name: "_DEPVAR_", Char: true, Length: 8}, &Var{Name: "_RMSE_", Length: 8}, &Var{Name: "Intercept", Length: 8})
	}
	for _, v := range append(append([]string(nil), rc.m.Regs...), rc.dep) {
		prev.AddVar(&Var{Name: v, Length: 8, Label: rc.d.Var(v).Label})
	}
	row := make([]Value, len(prev.Vars))
	for i := range row {
		row[i] = Missing
	}
	if rc.by != nil {
		copy(row, rc.keys)
	}
	row[prev.Index("_MODEL_")] = StrV(rc.m.Label)
	row[prev.Index("_TYPE_")] = StrV("PARMS")
	row[prev.Index("_DEPVAR_")] = StrV(rc.dep)
	row[prev.Index("_RMSE_")] = NumV(math.Sqrt(rc.fit.MSE))
	for j, nm := range rc.paramNames() {
		row[prev.Index(nm)] = NumV(rc.fit.Beta[j])
	}
	row[prev.Index(rc.dep)] = NumV(-1)
	prev.Rows = append(prev.Rows, row)
	return prev
}

func (rc *rgCtx) diagPlots() {
	f := rc.fit
	if len(rc.y) > lmMaxPoints {
		rc.s.Log.Notef(rc.m.Line, "The number of observations (%d) exceeds the MAXPOINTS limit of %d; observation-level plots are suppressed.", len(rc.y), lmMaxPoints)
		return
	}
	dd := lmDiagCollect(f, rc.X, rc.y, rc.w, rc.dep)
	rc.s.Out.AddGraph(lmDiagPanel(dd))
	// residuals by regressors
	for _, v := range rc.regs {
		j := rc.d.Index(v)
		var xs []float64
		for _, r := range rc.rows {
			xs = append(xs, r[j].Num)
		}
		c := NewChart("Residual by Regressors for " + rc.dep)
		c.XLabel, c.YLabel = v, "Residual"
		c.Layers = []Layer{&RefLine{Axis: 'y', Value: 0, Color: "#888"}, &Scatter{X: xs, Y: dd.Resid, Color: PaletteColor(0), Marker: "circle"}}
		c.Width, c.Height = 480, 360
		rc.s.Out.AddGraph(&Graph{ODSName: "ResidualPlot", Caption: "Residual by " + v, SVG: c.SVG()})
	}
	if len(rc.regs) == 1 && !rc.noint {
		j := rc.d.Index(rc.regs[0])
		var xs []float64
		for _, r := range rc.rows {
			xs = append(xs, r[j].Num)
		}
		rc.s.Out.AddGraph(lmFitPlot(f, xs, rc.y, rc.regs[0], rc.dep, rc.alpha, func(x float64) []float64 { return []float64{1, x} }))
	}
}

// selection performs FORWARD/BACKWARD/STEPWISE selection, returning the chosen regressors.
func (rc *rgCtx) selection(method string) ([]string, error) {
	o := rc.m.Opts
	cand := rc.m.Regs
	var sle, sls float64
	switch method {
	case "FORWARD", "F":
		method, sle = "FORWARD", o.Num("SLENTRY", o.Num("SLE", 0.5))
	case "BACKWARD", "B":
		method, sls = "BACKWARD", o.Num("SLSTAY", o.Num("SLS", 0.1))
	case "STEPWISE":
		sle, sls = o.Num("SLENTRY", o.Num("SLE", 0.15)), o.Num("SLSTAY", o.Num("SLS", 0.15))
	default:
		rc.s.Log.Warn(rc.m.Line, "SELECTION="+method+" is not supported; the full model is fitted.")
		return cand, nil
	}
	// full model MSE for C(p)
	rc.build(cand)
	fullMSE := rc.fit.MSE
	n := float64(len(rc.y))
	r2 := func(regs []string) (float64, *lmFit) {
		rc.build(regs)
		return 1 - rc.fit.SSE/rc.fit.TSS, rc.fit
	}
	in := []string{}
	if method == "BACKWARD" {
		in = append(in, cand...)
	}
	t := &Table{ODSName: "SelectionSummary", Caption: "Summary of " + strings.Title(strings.ToLower(method)) + " Selection", RowHeader: 3,
		Cols: append(rc.hidden(), Col{Name: "Step", Label: "Step"}, Col{Name: "VarEntered", Label: "Variable Entered", Char: true}, Col{Name: "VarRemoved", Label: "Variable Removed", Char: true},
			Col{Name: "NumVarsIn", Label: "Number Vars In"}, Col{Name: "PartialRSquare", Label: "Partial R-Square", Format: "8.4"}, Col{Name: "ModelRSquare", Label: "Model R-Square", Format: "8.4"},
			Col{Name: "Cp", Label: "C(p)", Format: "8.4"}, Col{Name: "FValue", Label: "F Value", Format: lmFmtF}, Col{Name: "ProbF", Label: "Pr > F", Format: lmFmtP})}
	if method == "BACKWARD" {
		t.Cols = append(t.Cols[:3], t.Cols[4:]...)
	}
	contains := func(l []string, v string) bool {
		for _, x := range l {
			if x == v {
				return true
			}
		}
		return false
	}
	without := func(l []string, v string) []string {
		var o []string
		for _, x := range l {
			if x != v {
				o = append(o, x)
			}
		}
		return o
	}
	step := 0
	base, _ := r2(in)
	for iter := 0; iter < 4*len(cand)+4; iter++ {
		changed := false
		if method != "BACKWARD" {
			best, bestP, bestF, bestR := "", 2.0, 0.0, 0.0
			for _, v := range cand {
				if contains(in, v) {
					continue
				}
				r, ff := r2(append(append([]string(nil), in...), v))
				F := (r - base) * ff.TSS / ff.MSE
				pv := lmFProb(F, 1, ff.DFE)
				if pv < bestP {
					best, bestP, bestF, bestR = v, pv, F, r
				}
			}
			if best != "" && bestP <= sle {
				in = append(in, best)
				step++
				_, ff := r2(in)
				cp := ff.SSE/fullMSE - n + 2*float64(ff.Rank)
				t.AddRow(rc.pre(step, best, "", len(in), bestR-base, bestR, cp, bestF, bestP)...)
				base = bestR
				changed = true
			}
		}
		if method != "FORWARD" {
			worst, worstP, worstF, worstR := "", -1.0, 0.0, 0.0
			for _, v := range in {
				r, _ := r2(without(in, v))
				_, ff := r2(in)
				F := (base - r) * ff.TSS / ff.MSE
				pv := lmFProb(F, 1, ff.DFE)
				if pv > worstP {
					worst, worstP, worstF, worstR = v, pv, F, r
				}
			}
			if worst != "" && worstP > sls {
				in = without(in, worst)
				step++
				_, ff := r2(in)
				cp := ff.SSE/fullMSE - n + 2*float64(ff.Rank)
				if method == "BACKWARD" {
					t.AddRow(rc.pre(step, worst, len(in), base-worstR, worstR, cp, worstF, worstP)...)
				} else {
					t.AddRow(rc.pre(step, "", worst, len(in), base-worstR, worstR, cp, worstF, worstP)...)
				}
				base = worstR
				changed = true
			}
		}
		if !changed {
			break
		}
	}
	rc.table(t)
	return in, nil
}
