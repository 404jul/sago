package sas

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

func init() {
	RegisterProc("GLM", func(s *Session, p *ProcStep) error { return glRun(s, p, false) })
	RegisterProc("ANOVA", func(s *Session, p *ProcStep) error { return glRun(s, p, true) })
}

type glCtx struct {
	s       *Session
	p       *ProcStep
	d       *Dataset
	anova   bool
	noprint bool
	plots   bool
	alpha   float64
	classes []*lmClass
	terms   []lmTerm
	mopts   *Opts
	noint   bool
	deps    []string
	all     [][]Value
	rows    [][]Value
	wIdx    int
	des     *lmDesign
	// per dependent
	dep  string
	fit  *lmFit
	X    [][]float64
	y, w []float64
}

func glRun(s *Session, p *ProcStep, anova bool) error {
	d, err := s.ProcData(p)
	if err != nil {
		return err
	}
	g := &glCtx{s: s, p: p, d: d, anova: anova, noprint: p.Opts.Has("NOPRINT"), plots: lmPlotsWanted(s, p.Opts), alpha: p.Opts.Num("ALPHA", 0.05), wIdx: -1}
	var classNames []string
	for _, st := range p.Stmts("CLASS", "CLASSES") {
		before, _ := SplitSlash(st.Args())
		names, err := ExpandVarList(d, before)
		if err != nil {
			return err
		}
		classNames = append(classNames, names...)
	}
	mst := p.First("MODEL")
	if mst == nil {
		return fmt.Errorf("No MODEL statement was specified.")
	}
	before, after := SplitSlash(mst.Args())
	eq := -1
	for i, t := range before {
		if t.IsOp("=") {
			eq = i
			break
		}
	}
	if eq < 0 {
		return fmt.Errorf("Syntax error in MODEL statement: expecting '='.")
	}
	g.deps, err = ExpandVarList(d, before[:eq])
	if err != nil {
		return err
	}
	for i, dp := range g.deps {
		v := d.Var(dp)
		if v.Char {
			return fmt.Errorf("Variable %s in list does not match type prescribed for this list.", strings.ToUpper(dp))
		}
		g.deps[i] = v.Name
	}
	g.terms, err = lmParseEffects(before[eq+1:])
	if err != nil {
		return err
	}
	isClass := map[string]bool{}
	for _, c := range classNames {
		isClass[strings.ToUpper(c)] = true
	}
	for i := range g.terms {
		for j, v := range g.terms[i].Vars {
			if dv := d.Var(v); dv != nil {
				g.terms[i].Vars[j] = dv.Name
			} else {
				return fmt.Errorf("Variable %s not found.", strings.ToUpper(v))
			}
		}
		for j, v := range g.terms[i].Nest {
			if dv := d.Var(v); dv != nil {
				g.terms[i].Nest[j] = dv.Name
			}
		}
	}
	g.mopts = ParseOpts(after)
	g.noint = g.mopts.Has("NOINT")
	if st := p.First("WEIGHT"); st != nil && len(st.Args()) > 0 {
		g.wIdx = d.Index(st.Args()[0].Text)
	}
	// variables required
	need := map[int]bool{}
	for _, dp := range g.deps {
		need[d.Index(dp)] = true
	}
	for _, c := range classNames {
		need[d.Index(c)] = true
	}
	for _, t := range g.terms {
		for _, v := range t.all() {
			need[d.Index(v)] = true
		}
	}
	by, groups, err := s.ProcBy(p, d)
	if err != nil {
		return err
	}
	var outDS *Dataset
	var outSpec *lmOutSpec
	if st := p.First("OUTPUT"); st != nil {
		outSpec, err = lmParseOutput(st, g.alpha)
		if err != nil {
			return err
		}
	}
	for _, grp := range groups {
		if by != nil && !g.noprint {
			s.Out.SetBy(by.Vars, grp.Keys)
		}
		g.all = grp.Rows
		g.rows = nil
		for _, r := range grp.Rows {
			ok := true
			for i := range need {
				if r[i].IsMissing() {
					ok = false
					break
				}
			}
			if g.wIdx >= 0 && (r[g.wIdx].IsMissing() || r[g.wIdx].Num <= 0) {
				ok = false
			}
			if ok {
				g.rows = append(g.rows, r)
			}
		}
		g.classes = nil
		for _, c := range classNames {
			lc, err := lmBuildClass(d, c, g.rows)
			if err != nil {
				return err
			}
			g.classes = append(g.classes, lc)
		}
		g.des, err = lmNewDesign(d, g.terms, g.classes, !g.noint, g.rows)
		if err != nil {
			return err
		}
		g.header()
		var depFits []*lmFit
		for _, dp := range g.deps {
			if err := g.runDep(dp); err != nil {
				return err
			}
			depFits = append(depFits, g.fit)
		}
		for _, st := range p.Stmts("MEANS") {
			for i, dp := range g.deps {
				g.dep, g.fit = dp, depFits[i]
				if err := g.means(st); err != nil {
					return err
				}
			}
		}
		for _, st := range p.Stmts("LSMEANS") {
			for i, dp := range g.deps {
				g.dep, g.fit = dp, depFits[i]
				g.lsmeans(st)
			}
		}
		if outSpec != nil {
			od := g.output(outSpec, depFits)
			if outDS == nil {
				outDS = od
			} else {
				outDS.Rows = append(outDS.Rows, od.Rows...)
			}
		}
	}
	if outDS != nil {
		return s.WriteDS(outSpec.Ref, outDS)
	}
	return nil
}

func (g *glCtx) table(t *Table) {
	if !g.noprint {
		g.s.Out.AddTable(t)
	}
}

func (g *glCtx) header() {
	if len(g.classes) > 0 {
		t := &Table{ODSName: "ClassLevels", Caption: "Class Level Information", RowHeader: 1,
			Cols: []Col{{Name: "Class", Label: "Class", Char: true}, {Name: "Levels", Label: "Levels"}, {Name: "Values", Label: "Values", Char: true}}}
		for _, c := range g.classes {
			t.AddRow(c.Name, len(c.Levels), strings.Join(c.Levels, " "))
		}
		g.table(t)
	}
	t := &Table{ODSName: "NObs", RowHeader: 1, Cols: []Col{{Name: "Label", Char: true}, {Name: "N"}}}
	t.AddRow("Number of Observations Read", len(g.all))
	t.AddRow("Number of Observations Used", len(g.rows))
	g.table(t)
}

func (g *glCtx) runDep(dep string) error {
	g.dep = dep
	di := g.d.Index(dep)
	g.X, g.y, g.w = nil, nil, nil
	for _, r := range g.rows {
		g.X = append(g.X, g.des.Row(r))
		g.y = append(g.y, r[di].Num)
		if g.wIdx >= 0 {
			g.w = append(g.w, r[g.wIdx].Num)
		}
	}
	f := lmFitModel(g.X, g.y, g.w, !g.noint, g.des.ColTerm, len(g.terms))
	g.fit = f
	dv := g.d.Var(dep)
	if !g.noprint {
		txt := "Dependent Variable: " + dep
		if dv.Label != "" {
			txt += " " + dv.Label
		}
		g.s.Out.AddText(&Text{Text: txt, Pre: true})
	}
	hid := Col{Name: "Dependent", Char: true, Hidden: true}
	at := &Table{ODSName: "OverallANOVA", RowHeader: 2, Cols: []Col{hid, {Name: "Source", Label: "Source", Char: true}, {Name: "DF", Label: "DF"},
		{Name: "SS", Label: "Sum of Squares"}, {Name: "MS", Label: "Mean Square"}, {Name: "FValue", Label: "F Value", Format: lmFmtF}, {Name: "ProbF", Label: "Pr > F", Format: lmFmtP}}}
	F := lmDiv(f.SSM/float64(f.DFM), f.MSE)
	at.AddRow(dep, "Model", f.DFM, f.SSM, f.SSM/float64(f.DFM), F, lmFProb(F, f.DFM, f.DFE))
	at.AddRow(dep, "Error", f.DFE, f.SSE, f.MSE, nil, nil)
	tot := "Corrected Total"
	if g.noint {
		tot = "Uncorrected Total"
	}
	at.AddRow(dep, tot, f.DFM+f.DFE, f.TSS, nil, nil, nil)
	g.table(at)
	r2 := 1 - f.SSE/f.TSS
	rmse := math.Sqrt(f.MSE)
	ft := &Table{ODSName: "FitStatistics", Cols: []Col{hid, {Name: "RSquare", Label: "R-Square", Format: "8.6"}, {Name: "CV", Label: "Coeff Var", Format: "8.6"},
		{Name: "RootMSE", Label: "Root MSE"}, {Name: "DepMean", Label: dep + " Mean"}}}
	ft.AddRow(dep, r2, 100*rmse/f.YMean, rmse, f.YMean)
	g.table(ft)
	// SS tables
	types := []int{}
	for _, k := range []string{"SS1", "SS2", "SS3"} {
		if g.mopts.Has(k) {
			types = append(types, int(k[2]-'0'))
		}
	}
	if len(types) == 0 {
		types = []int{1, 3}
	}
	if g.anova {
		types = []int{1}
	}
	if len(g.terms) > 0 {
		for _, ty := range types {
			g.ssTable(ty)
		}
	}
	if err := g.contrastTable(); err != nil {
		return err
	}
	if g.mopts.Has("SOLUTION") || (len(g.classes) == 0 && !g.anova) {
		g.paramTable()
	}
	if g.plots {
		g.diagPlots()
	}
	return nil
}

func (g *glCtx) ssTable(ty int) {
	f := g.fit
	label := map[int]string{1: "Type I SS", 2: "Type II SS", 3: "Type III SS"}[ty]
	if g.anova {
		label = "Anova SS"
	}
	t := &Table{ODSName: "ModelANOVA", RowHeader: 2, Cols: []Col{{Name: "Dependent", Char: true, Hidden: true}, {Name: "HypothesisType", Hidden: true},
		{Name: "Source", Label: "Source", Char: true}, {Name: "DF", Label: "DF"}, {Name: "SS", Label: label}, {Name: "MS", Label: "Mean Square"},
		{Name: "FValue", Label: "F Value", Format: lmFmtF}, {Name: "ProbF", Label: "Pr > F", Format: lmFmtP}}}
	var ec *lmCross
	if ty != 1 {
		var EX [][]float64
		if g.noint {
			EX = g.X
		} else {
			for _, r := range g.rows {
				EX = append(EX, g.des.ECRow(r))
			}
		}
		ec = lmNewCross(EX, g.y, g.w, !g.noint)
	}
	ecTerm := g.des.ECTerm
	if g.noint {
		ecTerm = g.des.ColTerm
	}
	for ti, term := range g.terms {
		var ss float64
		var df int
		switch ty {
		case 1:
			ss, df = f.SeqSS[ti], f.SeqDF[ti]
		case 3:
			drop := map[int]bool{}
			for k, tt := range ecTerm {
				if tt == ti {
					drop[k] = true
				}
			}
			ss, df = lmPartialSS(ec, drop, nil)
		case 2:
			drop := map[int]bool{}
			keep := map[int]bool{}
			for k, tt := range ecTerm {
				switch {
				case tt == ti:
					drop[k] = true
				case tt < 0 || !g.contains(g.terms[tt], term):
					keep[k] = true
				}
			}
			ss, df = lmPartialSS(ec, drop, keep)
		}
		if ss < 0 && ss > -1e-9*f.TSS {
			ss = 0
		}
		ms := math.NaN()
		F := math.NaN()
		if df > 0 {
			ms = ss / float64(df)
			F = ms / f.MSE
		}
		t.AddRow(g.dep, ty, term.Name(), df, ss, ms, F, lmFProb(F, df, f.DFE))
	}
	g.table(t)
}

// contains reports whether effect a contains effect b (SAS definition).
func (g *glCtx) contains(a, b lmTerm) bool {
	if a.Name() == b.Name() {
		return false
	}
	cont := func(t lmTerm) string {
		var c []string
		for _, v := range t.all() {
			if !g.des.isClass(v) {
				c = append(c, strings.ToUpper(v))
			}
		}
		sort.Strings(c)
		return strings.Join(c, "*")
	}
	cls := func(t lmTerm) map[string]bool {
		m := map[string]bool{}
		for _, v := range t.all() {
			if g.des.isClass(v) {
				m[strings.ToUpper(v)] = true
			}
		}
		return m
	}
	if cont(a) != cont(b) {
		return false
	}
	ca, cb := cls(a), cls(b)
	if len(ca) <= len(cb) {
		return false
	}
	for v := range cb {
		if !ca[v] {
			return false
		}
	}
	return true
}

func (g *glCtx) paramTable() {
	f := g.fit
	singular := false
	for _, a := range f.Aliased {
		singular = singular || a
	}
	clp := g.mopts.Has("CLPARM")
	t := &Table{ODSName: "ParameterEstimates", RowHeader: 2, Cols: []Col{{Name: "Dependent", Char: true, Hidden: true}, {Name: "Parameter", Label: "Parameter", Char: true},
		{Name: "Estimate", Label: "Estimate"}, {Name: "Biased", Label: "", Char: true}, {Name: "StdErr", Label: "Standard Error"},
		{Name: "tValue", Label: "t Value", Format: lmFmtT}, {Name: "Probt", Label: "Pr > |t|", Format: lmFmtP}}}
	if clp {
		t.Cols = append(t.Cols, Col{Name: "LowerCL", Label: "Lower"}, Col{Name: "UpperCL", Label: "Upper"})
		t.Spans = []Span{{Label: fmt.Sprintf("%g%% Confidence Limits", 100*(1-g.alpha)), From: 5, To: 6}}
	}
	tc := TInv(1-g.alpha/2, float64(f.DFE))
	for j, name := range g.des.ColName {
		b := ""
		if singular {
			ti := g.des.ColTerm[j]
			if ti < 0 || len(g.des.termClasses(g.terms[ti])) > 0 {
				b = "B"
			}
		}
		if f.Aliased[j] {
			row := []any{g.dep, name, 0.0, b, nil, nil, nil}
			if clp {
				row = append(row, nil, nil)
			}
			t.AddRow(row...)
			continue
		}
		se := math.Sqrt(f.G[j][j] * f.MSE)
		tv := f.Beta[j] / se
		row := []any{g.dep, name, f.Beta[j], b, se, tv, TTwoSided(tv, float64(f.DFE))}
		if clp {
			row = append(row, f.Beta[j]-tc*se, f.Beta[j]+tc*se)
		}
		t.AddRow(row...)
	}
	if singular {
		t.Notes = append(t.Notes, "Note: The X'X matrix has been found to be singular, and a generalized inverse was used to solve the normal equations. Terms whose estimates are followed by the letter 'B' are not uniquely estimable.")
	}
	g.table(t)
}

// ---------------------------------------------------------------- MEANS

type glLevel struct {
	key  string
	keys []string
	ys   []float64
	mean float64
	n    int
}

func (g *glCtx) levelsOf(cs []*lmClass) []*glLevel {
	di := g.d.Index(g.dep)
	m := map[string]*glLevel{}
	var order [][]int
	for _, r := range g.rows {
		idx := make([]int, len(cs))
		var ks []string
		for j, c := range cs {
			idx[j] = c.level(r)
			ks = append(ks, c.Levels[idx[j]])
		}
		k := strings.Join(ks, "\x00")
		lv := m[k]
		if lv == nil {
			lv = &glLevel{key: k, keys: ks}
			m[k] = lv
			order = append(order, idx)
		}
		lv.ys = append(lv.ys, r[di].Num)
	}
	sort.Slice(order, func(a, b int) bool {
		for j := range order[a] {
			if order[a][j] != order[b][j] {
				return order[a][j] < order[b][j]
			}
		}
		return false
	})
	var out []*glLevel
	for _, idx := range order {
		var ks []string
		for j, c := range cs {
			ks = append(ks, c.Levels[idx[j]])
		}
		lv := m[strings.Join(ks, "\x00")]
		lv.n = len(lv.ys)
		lv.mean = Mean(lv.ys)
		out = append(out, lv)
	}
	return out
}

func (g *glCtx) means(st *Stmt) error {
	before, after := SplitSlash(st.Args())
	effs, err := lmParseEffects(before)
	if err != nil {
		return err
	}
	o := ParseOpts(after)
	alpha := o.Num("ALPHA", g.alpha)
	for _, e := range effs {
		var cs []*lmClass
		for _, v := range e.all() {
			c, ok := g.des.Classes[strings.ToUpper(v)]
			if !ok {
				return fmt.Errorf("Effects used in the MEANS statement must have appeared previously in the MODEL statement.")
			}
			cs = append(cs, c)
		}
		lvls := g.levelsOf(cs)
		// HOVTEST (one-way models only)
		if op, ok := o.Get("HOVTEST"); ok && len(cs) == 1 {
			method := "LEVENE"
			if op.Eq {
				method = strings.ToUpper(op.Str())
			}
			g.hovtest(e, lvls, method, o)
		}
		if len(cs) == 1 {
			if o.Has("WELCH") {
				g.welch(e, lvls)
			}
			for _, k := range []string{"TUKEY", "BON", "SCHEFFE", "LSD", "T", "SIDAK", "DUNNETT"} {
				if o.Has(k) {
					ctl := ""
					cldiff, lines := o.Has("CLDIFF"), o.Has("LINES")
					if k == "DUNNETT" {
						// control level: explicit ('level') or SAS's default last level
						if op, ok := o.Get("DUNNETT"); ok {
							for _, t := range op.Inner() {
								if t.Kind == TIdent || t.Kind == TString {
									ctl = t.Text
								}
							}
						}
						if ctl == "" && len(lvls) > 0 {
							ctl = lvls[len(lvls)-1].keys[0]
						}
						cldiff, lines = true, false
					}
					g.multComp(e, lvls, k, alpha, cldiff, lines, ctl)
				}
			}
		}
		// Means table
		t := &Table{ODSName: "Means", RowHeader: len(cs)}
		for _, c := range cs {
			t.Cols = append(t.Cols, Col{Name: c.Name, Label: "Level of " + c.Name, Char: true})
		}
		t.Cols = append(t.Cols, Col{Name: "N", Label: "N"}, Col{Name: "Mean", Label: "Mean"}, Col{Name: "SD", Label: "Std Dev"})
		t.Spans = []Span{{Label: g.dep, From: len(cs) + 1, To: len(cs) + 2}}
		for _, lv := range lvls {
			row := []any{}
			for _, k := range lv.keys {
				row = append(row, k)
			}
			sd := math.NaN()
			if lv.n > 1 {
				sd = StdDev(lv.ys)
			}
			row = append(row, lv.n, lv.mean, sd)
			t.AddRow(row...)
		}
		g.table(t)
	}
	return nil
}

func (g *glCtx) hovtest(e lmTerm, lvls []*glLevel, method string, o *Opts) {
	if method == "BARTLETT" {
		// Bartlett's chi-square test on the group variances
		var sl2, sldf []float64
		ssp, dfp, n := 0.0, 0.0, 0
		for _, lv := range lvls {
			ss := 0.0
			for _, y := range lv.ys {
				ss += (y - lv.mean) * (y - lv.mean)
			}
			ssp += ss
			dfp += float64(len(lv.ys) - 1)
			n += len(lv.ys)
			sl2 = append(sl2, ss/float64(len(lv.ys)-1))
			sldf = append(sldf, float64(len(lv.ys)-1))
		}
		sp2 := ssp / dfp
		num := dfp * math.Log(sp2)
		invSum := 0.0
		for i := range sl2 {
			num -= sldf[i] * math.Log(sl2[i])
			invSum += 1 / sldf[i]
		}
		corr := 1 + (invSum-1/dfp)/(3*float64(len(lvls)-1))
		chi := num / corr
		t := &Table{ODSName: "HOVFTest", Caption: "Bartlett's Test for Homogeneity of " + g.dep + " Variance", RowHeader: 1,
			Cols: []Col{{Name: "Source", Label: "Source", Char: true}, {Name: "DF", Label: "DF"},
				{Name: "ChiSq", Label: "Chi-Square", Format: lmFmtF}, {Name: "ProbChiSq", Label: "Pr > ChiSq", Format: lmFmtP}}}
		t.AddRow(e.Name(), float64(len(lvls)-1), chi, ChiSqSF(chi, float64(len(lvls)-1)))
		g.table(t)
		return
	}
	var z [][]float64
	caption := ""
	absLev := false
	if op, ok := o.Get("HOVTEST"); ok && op.Eq {
		absLev = strings.Contains(strings.ToUpper(TokText(op.Val)), "ABS")
	}
	for _, lv := range lvls {
		var zz []float64
		switch method {
		case "BF":
			med := Percentile(Sorted(lv.ys), 50)
			for _, y := range lv.ys {
				zz = append(zz, math.Abs(y-med))
			}
		default:
			for _, y := range lv.ys {
				dv := y - lv.mean
				if absLev {
					zz = append(zz, math.Abs(dv))
				} else {
					zz = append(zz, dv*dv)
				}
			}
		}
		z = append(z, zz)
	}
	switch method {
	case "BF":
		caption = "Brown and Forsythe's Test for Homogeneity of " + g.dep + " Variance\nANOVA of Absolute Deviations from Group Medians"
	default:
		method = "LEVENE"
		if absLev {
			caption = "Levene's Test for Homogeneity of " + g.dep + " Variance\nANOVA of Absolute Deviations from Group Means"
		} else {
			caption = "Levene's Test for Homogeneity of " + g.dep + " Variance\nANOVA of Squared Deviations from Group Means"
		}
	}
	ssb, ssw, n, tot := 0.0, 0.0, 0, 0.0
	for _, zz := range z {
		for _, v := range zz {
			tot += v
			n++
		}
	}
	gm := tot / float64(n)
	for _, zz := range z {
		m := Mean(zz)
		ssb += float64(len(zz)) * (m - gm) * (m - gm)
		for _, v := range zz {
			ssw += (v - m) * (v - m)
		}
	}
	k := len(z)
	dfb, dfw := k-1, n-k
	F := (ssb / float64(dfb)) / (ssw / float64(dfw))
	t := &Table{ODSName: "HOVFTest", Caption: caption, RowHeader: 1, Cols: []Col{{Name: "Source", Label: "Source", Char: true}, {Name: "DF", Label: "DF"},
		{Name: "SS", Label: "Sum of Squares"}, {Name: "MS", Label: "Mean Square"}, {Name: "FValue", Label: "F Value", Format: lmFmtF}, {Name: "ProbF", Label: "Pr > F", Format: lmFmtP}}}
	t.AddRow(e.Name(), dfb, ssb, ssb/float64(dfb), F, lmFProb(F, dfb, dfw))
	t.AddRow("Error", dfw, ssw, ssw/float64(dfw), nil, nil)
	g.table(t)
}

// welch computes Welch's variance-weighted one-way ANOVA:
// w_i = n_i/s_i^2; F = A/(1+2B) with
// A = sum w_i (ybar_i - ybar_w)^2/(k-1),
// B = 2(k-2)/(k^2-1) * sum (1-w_i/W)^2/(n_i-1), denominator df
// = (k^2-1)/(3 sum (1-w_i/W)^2/(n_i-1)).
func (g *glCtx) welch(e lmTerm, lvls []*glLevel) {
	k := float64(len(lvls))
	var w, wsum, ywNum float64
	for _, lv := range lvls {
		variance := Variance(lv.ys)
		wi := float64(lv.n) / variance
		w += wi
		wsum += wi
		ywNum += wi * lv.mean
	}
	ybarW := ywNum / wsum
	A := 0.0
	bSum := 0.0
	for _, lv := range lvls {
		variance := Variance(lv.ys)
		wi := float64(lv.n) / variance
		A += wi * (lv.mean - ybarW) * (lv.mean - ybarW)
		d := 1 - wi/wsum
		bSum += d * d / float64(lv.n-1)
	}
	A /= (k - 1)
	cor := 2 * (k - 2) / (k*k - 1) * bSum
	F := A / (1 + cor)
	dfDen := (k*k - 1) / (3 * bSum)
	t := &Table{ODSName: "WelchTest", Caption: "Welch's ANOVA for " + g.dep, RowHeader: 1,
		Cols: []Col{{Name: "Source", Label: "Source", Char: true}, {Name: "DF", Label: "DF", Format: "8.4"},
			{Name: "FValue", Label: "F Value", Format: lmFmtF}, {Name: "ProbF", Label: "Pr > F", Format: lmFmtP}}}
	t.AddRow(e.Name(), k-1, F, FSF(F, k-1, dfDen))
	t.AddRow("Error", dfDen, nil, nil)
	g.table(t)
}

// contrastTable evaluates CONTRAST statements. Supported: a single
// class effect with one coefficient per sorted level (the one-way
// form SS = (sum c_i*ybar_i)^2 / sum c_i^2/n_i).
func (g *glCtx) contrastTable() error {
	cts := g.p.Stmts("CONTRAST")
	if len(cts) == 0 {
		return nil
	}
	if len(g.terms) != 1 {
		return fmt.Errorf("CONTRAST is supported for one-way models only.")
	}
	ct := &Table{ODSName: "Contrasts", RowHeader: 2, Cols: []Col{
		{Name: "Dependent", Char: true, Hidden: true},
		{Name: "Label", Label: "Contrast", Char: true}, {Name: "DF", Label: "DF"},
		{Name: "SS", Label: "Contrast SS"}, {Name: "MS", Label: "Mean Square"},
		{Name: "FValue", Label: "F Value", Format: lmFmtF}, {Name: "ProbF", Label: "Pr > F", Format: lmFmtP}}}
	for _, st := range cts {
		label, effect, coefs, err := glParseContrast(st.Args())
		if err != nil {
			return err
		}
		c, ok := g.des.Classes[strings.ToUpper(effect)]
		if !ok {
			return fmt.Errorf("Effects used in the CONTRAST statement must have appeared previously in the MODEL statement.")
		}
		lvls := g.levelsOf([]*lmClass{c})
		if len(coefs) != len(lvls) {
			return fmt.Errorf("CONTRAST requires %d coefficients for %d levels of %s.", len(lvls), len(lvls), strings.ToUpper(effect))
		}
		lc, den := 0.0, 0.0
		for i, lv := range lvls {
			lc += coefs[i] * lv.mean
			den += coefs[i] * coefs[i] / float64(lv.n)
		}
		ss := lc * lc / den
		F := ss / g.fit.MSE
		ct.AddRow(g.dep, label, 1, ss, ss, F, lmFProb(F, 1, g.fit.DFE))
	}
	g.table(ct)
	return nil
}

func glParseContrast(toks []Token) (string, string, []float64, error) {
	c := NewCursor(toks)
	if c.AtEnd() {
		return "", "", nil, fmt.Errorf("CONTRAST requires a label and an effect.")
	}
	t := c.Next()
	if t.Kind != TString && t.Kind != TIdent {
		return "", "", nil, fmt.Errorf("CONTRAST label must be a string.")
	}
	label := t.Text
	if c.AtEnd() || c.Peek().Kind != TIdent {
		return "", "", nil, fmt.Errorf("CONTRAST requires an effect after the label.")
	}
	effect := c.Next().Text
	var coefs []float64
	sign := 1.0
	for !c.AtEnd() {
		t := c.Next()
		switch {
		case t.IsOp("-"):
			sign = -1
		case t.IsOp("+"):
			sign = 1
		case t.Kind == TNumber:
			coefs = append(coefs, sign*t.Num)
			sign = 1
		case t.Kind == TIdent:
			return "", "", nil, fmt.Errorf("CONTRAST supports a single effect.")
		}
	}
	return label, effect, coefs, nil
}

func (g *glCtx) multComp(e lmTerm, lvls []*glLevel, method string, alpha float64, cldiff, lines bool, control string) {
	f := g.fit
	k := len(lvls)
	if k < 2 {
		return
	}
	mse, dfe := f.MSE, float64(f.DFE)
	balanced := true
	for _, lv := range lvls {
		if lv.n != lvls[0].n {
			balanced = false
		}
	}
	if !cldiff && !lines {
		lines = balanced
		cldiff = !balanced
	}
	var name, critLabel string
	var crit float64
	var hw func(ni, nj int) float64
	se := func(ni, nj int) float64 { return math.Sqrt(mse * (1/float64(ni) + 1/float64(nj))) }
	m := float64(k * (k - 1) / 2)
	switch method {
	case "TUKEY":
		name, critLabel = "Tukey's Studentized Range (HSD) Test for "+g.dep, "Critical Value of Studentized Range"
		crit = lmQTukey(1-alpha, 1, float64(k), dfe)
		hw = func(ni, nj int) float64 { return crit / math.Sqrt2 * se(ni, nj) }
	case "BON":
		name, critLabel = "Bonferroni (Dunn) t Tests for "+g.dep, "Critical Value of t"
		crit = TInv(1-alpha/(2*m), dfe)
		hw = func(ni, nj int) float64 { return crit * se(ni, nj) }
	case "SIDAK":
		name, critLabel = "Sidak t Tests for "+g.dep, "Critical Value of t"
		crit = TInv(1-(1-math.Pow(1-alpha, 1/m))/2, dfe)
		hw = func(ni, nj int) float64 { return crit * se(ni, nj) }
	case "SCHEFFE":
		name, critLabel = "Scheffe's Test for "+g.dep, "Critical Value of F"
		crit = FInv(1-alpha, float64(k-1), dfe)
		hw = func(ni, nj int) float64 { return math.Sqrt(float64(k-1)*crit) * se(ni, nj) }
	case "DUNNETT":
		name, critLabel = "Dunnett's t Tests for "+g.dep, "Critical Value of Dunnett's t"
		crit = lmDunnettCrit(1-alpha, k-1, dfe)
		hw = func(ni, nj int) float64 { return crit * se(ni, nj) }
	default:
		name, critLabel = "t Tests (LSD) for "+g.dep, "Critical Value of t"
		crit = TInv(1-alpha/2, dfe)
		hw = func(ni, nj int) float64 { return crit * se(ni, nj) }
	}
	odsInfo := "CLDiffsInfo"
	if lines {
		odsInfo = "MCLinesInfo"
	}
	info := &Table{ODSName: odsInfo, Caption: name, RowHeader: 1, Cols: []Col{{Name: "Label1", Char: true}, {Name: "cValue1", Char: true}, {Name: "nValue1", Hidden: true}}}
	info.AddRow("Alpha", fmt.Sprintf("%g", alpha), alpha)
	info.AddRow("Error Degrees of Freedom", fmt.Sprintf("%d", f.DFE), dfe)
	info.AddRow("Error Mean Square", strings.TrimSpace(FormatByName(NumV(mse), "BEST8.")), mse)
	info.AddRow(critLabel, fmt.Sprintf("%.5f", crit), crit)
	if balanced {
		msd := hw(lvls[0].n, lvls[0].n)
		lbl := "Minimum Significant Difference"
		if method == "LSD" || method == "T" {
			lbl = "Least Significant Difference"
		}
		info.AddRow(lbl, strings.TrimSpace(FormatByName(NumV(msd), "BEST8.")), msd)
	}
	g.table(info)
	sorted := append([]*glLevel(nil), lvls...)
	sort.SliceStable(sorted, func(a, b int) bool { return sorted[a].mean > sorted[b].mean })
	if cldiff {
		pct := fmt.Sprintf("%g%%", 100*(1-alpha))
		simul := "Simultaneous "
		if method == "LSD" || method == "T" {
			simul = ""
		}
		t := &Table{ODSName: "CLDiffs", Caption: name, RowHeader: 1, Cols: []Col{{Name: "Comparison", Label: e.Name() + " Comparison", Char: true},
			{Name: "Difference", Label: "Difference Between Means"}, {Name: "LowerCL", Label: "Lower"}, {Name: "UpperCL", Label: "Upper"}, {Name: "Significance", Label: "", Char: true}},
			Spans: []Span{{Label: simul + pct + " Confidence Limits", From: 2, To: 3}}}
		t.Notes = []string{fmt.Sprintf("Comparisons significant at the %g level are indicated by ***.", alpha)}
		for _, a := range sorted {
			for _, b := range sorted {
				if a == b {
					continue
				}
				if control != "" && b.keys[0] != control {
					continue // Dunnett: comparisons against the control only
				}
				diff := a.mean - b.mean
				h := hw(a.n, b.n)
				sig := ""
				if diff-h > 0 || diff+h < 0 {
					sig = "***"
				}
				t.AddRow(a.keys[0]+" - "+b.keys[0], diff, diff-h, diff+h, sig)
			}
		}
		g.table(t)
	}
	if lines {
		// letter groupings over means sorted descending
		type rng struct{ i, j int }
		var rs []rng
		maxJ := -1
		for i := range sorted {
			j := i
			for j+1 < k && sorted[i].mean-sorted[j+1].mean < hw(sorted[i].n, sorted[j+1].n) {
				j++
			}
			if j > maxJ {
				rs = append(rs, rng{i, j})
				maxJ = j
			}
		}
		letters := make([][]string, k)
		for ri, r := range rs {
			l := string(rune('A' + ri%26))
			for x := r.i; x <= r.j; x++ {
				letters[x] = append(letters[x], l)
			}
		}
		t := &Table{ODSName: "MCLines", Caption: name, RowHeader: 0, Cols: []Col{{Name: "Grouping", Label: method + " Grouping", Char: true},
			{Name: "Mean", Label: "Mean"}, {Name: "N", Label: "N"}, {Name: "Level", Label: e.Name(), Char: true}}}
		t.Cols[0].Label = map[string]string{"TUKEY": "Tukey", "BON": "Bon", "SCHEFFE": "Scheffe", "LSD": "t", "T": "t", "SIDAK": "Sidak"}[method] + " Grouping"
		t.Notes = []string{"Means with the same letter are not significantly different."}
		for i, lv := range sorted {
			t.AddRow(strings.Join(letters[i], " "), lv.mean, lv.n, lv.keys[0])
		}
		g.table(t)
	}
}

// ---------------------------------------------------------------- LSMEANS

func (g *glCtx) lsmeans(st *Stmt) {
	before, after := SplitSlash(st.Args())
	effs, err := lmParseEffects(before)
	if err != nil {
		return
	}
	o := ParseOpts(after)
	f := g.fit
	// column means for pure continuous terms, variable means for covariates
	colMean := make([]float64, len(g.des.ColTerm))
	for _, x := range g.X {
		for j := range x {
			colMean[j] += x[j] / float64(len(g.X))
		}
	}
	varMean := map[string]float64{}
	for name, idx := range g.des.cont {
		s := 0.0
		for _, r := range g.rows {
			s += r[idx].Num
		}
		varMean[name] = s / float64(len(g.rows))
	}
	for _, e := range effs {
		var cs []*lmClass
		ok := true
		for _, v := range e.all() {
			c, isC := g.des.Classes[strings.ToUpper(v)]
			if !isC {
				ok = false
			}
			cs = append(cs, c)
		}
		if !ok {
			continue
		}
		var others []*lmClass
		for _, c := range g.classes {
			in := false
			for _, x := range cs {
				in = in || x == c
			}
			if !in {
				others = append(others, c)
			}
		}
		t := &Table{ODSName: "LSMeans", Caption: "Least Squares Means", RowHeader: len(cs)}
		for _, c := range cs {
			t.Cols = append(t.Cols, Col{Name: c.Name, Label: c.Name, Char: true})
		}
		t.Cols = append(t.Cols, Col{Name: "LSMean", Label: g.dep + " LSMEAN"})
		stderr := o.Has("STDERR")
		if stderr {
			t.Cols = append(t.Cols, Col{Name: "StdErr", Label: "Standard Error"}, Col{Name: "Probt", Label: "Pr > |t|", Format: lmFmtP})
		}
		for _, lv := range g.levelsOf(cs) {
			L := make([]float64, len(g.des.ColTerm))
			combos := lmCombos(others)
			for _, combo := range combos {
				lvl := map[*lmClass]int{}
				for j, c := range cs {
					lvl[c] = c.pos[lv.keys[j]]
				}
				for j, c := range others {
					lvl[c] = combo[j]
				}
				for col, ti := range g.des.ColTerm {
					var v float64
					switch {
					case ti < 0:
						v = 1
					case g.des.colKey[col] == nil:
						v = colMean[col]
					default:
						v = 1
						for j, c := range g.des.termClasses(g.terms[ti]) {
							if lvl[c] != g.des.colKey[col][j] {
								v = 0
							}
						}
						for _, nm := range g.terms[ti].all() {
							if !g.des.isClass(nm) {
								v *= varMean[strings.ToUpper(nm)]
							}
						}
					}
					L[col] += v / float64(len(combos))
				}
			}
			row := []any{}
			for _, k := range lv.keys {
				row = append(row, k)
			}
			est := f.predict(L)
			row = append(row, est)
			if stderr {
				se := math.Sqrt(f.xGx(L) * f.MSE)
				row = append(row, se, TTwoSided(est/se, float64(f.DFE)))
			}
			t.AddRow(row...)
		}
		g.table(t)
	}
}

func lmCombos(cs []*lmClass) [][]int {
	out := [][]int{{}}
	for _, c := range cs {
		var nx [][]int
		for _, o := range out {
			for l := range c.Levels {
				nx = append(nx, append(append([]int(nil), o...), l))
			}
		}
		out = nx
	}
	return out
}

// ---------------------------------------------------------------- OUTPUT and plots

func (g *glCtx) output(sp *lmOutSpec, fits []*lmFit) *Dataset {
	od := &Dataset{Name: sp.Ref.Name}
	for _, v := range g.d.Vars {
		od.Vars = append(od.Vars, v.Clone())
	}
	type ov struct {
		key string
		dep int
		col int
	}
	var ovs []ov
	for _, it := range sp.Items {
		for di, nm := range sp.Names[it[0]] {
			if di < len(g.deps) {
				ovs = append(ovs, ov{it[0], di, od.AddVar(&Var{Name: nm, Length: 8})})
			}
		}
	}
	in := map[*Value]bool{}
	for _, r := range g.rows {
		in[&r[0]] = true
	}
	for _, r := range g.all {
		nr := make([]Value, len(od.Vars))
		copy(nr, r)
		for i := len(r); i < len(nr); i++ {
			nr[i] = Missing
		}
		x := g.des.Row(r)
		w := 1.0
		if g.wIdx >= 0 {
			w = r[g.wIdx].Num
		}
		for _, o := range ovs {
			y := r[g.d.Index(g.deps[o.dep])].Num
			if !in[&r[0]] {
				y = math.NaN()
			}
			nr[o.col] = NumV(fits[o.dep].obsStats(x, y, w, sp.Alpha).get(o.key))
		}
		od.Rows = append(od.Rows, nr)
	}
	return od
}

func (g *glCtx) diagPlots() {
	f := g.fit
	if len(g.y) > lmMaxPoints {
		g.s.Log.Notef(g.p.Line, "The number of observations (%d) exceeds the MAXPOINTS limit of %d; observation-level plots are suppressed.", len(g.y), lmMaxPoints)
		return
	}
	dd := lmDiagCollect(f, g.X, g.y, g.w, g.dep)
	g.s.Out.AddGraph(lmDiagPanel(dd))
	if len(g.terms) == 1 && len(g.terms[0].all()) == 1 {
		v := g.terms[0].Vars[0]
		if c, ok := g.des.Classes[strings.ToUpper(v)]; ok {
			lvls := g.levelsOf([]*lmClass{c})
			var boxes []BoxStat
			var cats []string
			for i, lv := range lvls {
				s := Sorted(lv.ys)
				q1, q3 := Percentile(s, 25), Percentile(s, 75)
				iqr := q3 - q1
				lo, hi := q1, q3
				var outl []float64
				for _, y := range s {
					if y < q1-1.5*iqr || y > q3+1.5*iqr {
						outl = append(outl, y)
						continue
					}
					lo, hi = math.Min(lo, y), math.Max(hi, y)
				}
				boxes = append(boxes, BoxStat{Pos: float64(i), Q1: q1, Median: Percentile(s, 50), Q3: q3, Mean: lv.mean, WhiskLo: lo, WhiskHi: hi, Outliers: outl})
				cats = append(cats, lv.keys[0])
			}
			ch := NewChart("Distribution of " + g.dep)
			ch.XLabel, ch.YLabel, ch.XCats = c.Name, g.dep, cats
			ch.Layers = []Layer{&Boxes{Boxes: boxes, Color: PaletteColor(0)}}
			g.s.Out.AddGraph(&Graph{ODSName: "BoxPlot", Caption: "Distribution of " + g.dep, SVG: ch.SVG()})
		} else if !g.noint {
			j := g.d.Index(v)
			var xs []float64
			for _, r := range g.rows {
				xs = append(xs, r[j].Num)
			}
			g.s.Out.AddGraph(lmFitPlot(f, xs, g.y, v, g.dep, g.alpha, func(x float64) []float64 { return []float64{1, x} }))
		}
	}
}
