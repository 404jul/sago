package sas

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// PROC LIFETEST (lt prefix): product-limit (Kaplan-Meier) survival
// estimates with Greenwood standard errors, quartile estimates with
// LOGLOG-transformed confidence limits, the restricted mean survival
// time, and the k-sample tests of equality over strata (log-rank,
// Wilcoxon, -2Log(LR)). ODS tables: ProductLimitEstimates,
// QuartileEstimates, HomogeneityTests.

// ltObs is one valid observation.
type ltObs struct {
	t    float64
	evt  bool
	str  string
	strV Value
}

// ltRow is one observed time of the product-limit table.
type ltRow struct {
	t           float64
	y, dd, c    int
	s, se       float64
	hasS, hasSE bool
}

func init() { RegisterProc("LIFETEST", ltLifetest) }

// ltEmit prepends the _Proc_/_Run_ columns SAS's ODS OUTPUT tables carry.
func ltEmit(s *Session, t *Table) {
	t.Cols = append([]Col{
		{Name: "_Proc_", Label: "", Char: true, Hidden: true},
		{Name: "_Run_", Label: "", Format: "4.", Hidden: true},
	}, t.Cols...)
	for i := range t.Rows {
		t.Rows[i] = append([]Value{StrV("Lifetest"), NumV(1)}, t.Rows[i]...)
	}
	s.Out.AddTable(t)
}

func ltLifetest(s *Session, p *ProcStep) error {
	d, err := s.ProcData(p)
	if err != nil {
		return err
	}
	tst := p.First("TIME")
	if tst == nil || len(tst.Args()) == 0 {
		return fmt.Errorf("The TIME statement is required.")
	}
	// TIME time*censor(list)
	parts := strings.SplitN(TokText(tst.Args()), "*", 2)
	if len(parts) != 2 {
		return fmt.Errorf("TIME statement must be of the form time*censor(list).")
	}
	timeVar := strings.TrimSpace(parts[0])
	censSpec := strings.TrimSpace(parts[1])
	ti := d.Index(timeVar)
	if ti < 0 {
		return fmt.Errorf("Variable %s not found.", strings.ToUpper(timeVar))
	}
	censVals := map[float64]bool{}
	censName := censSpec
	if k := strings.Index(censSpec, "("); k >= 0 {
		censName = strings.TrimSpace(censSpec[:k])
		list := strings.Trim(censSpec[k:], "()")
		for _, f := range strings.FieldsFunc(list, func(r rune) bool { return r == ',' || r == ' ' }) {
			if v, ok := parseNumber(f); ok {
				censVals[v] = true
			}
		}
	}
	ci := d.Index(censName)
	if ci < 0 {
		return fmt.Errorf("Variable %s not found.", strings.ToUpper(censName))
	}
	alpha := p.Opts.Num("ALPHA", 0.05)

	strVar := ""
	si := -1
	if st := p.First("STRATA"); st != nil && len(st.Args()) > 0 {
		strVar = st.Args()[0].Text
		si = d.Index(strVar)
		if si < 0 {
			return fmt.Errorf("Variable %s not found.", strings.ToUpper(strVar))
		}
	}

	var rows []ltObs
	strSet := map[string]bool{}
	var strOrder []string
	for _, r := range d.Rows {
		if r[ti].IsMissing() || r[ci].IsMissing() {
			continue
		}
		o := ltObs{t: r[ti].Num, evt: !censVals[r[ci].Num]}
		if strVar != "" {
			if r[si].IsMissing() {
				continue
			}
			o.strV = r[si]
			o.str = FormatValue(r[si], nil)
			if !strSet[o.str] {
				strSet[o.str] = true
				strOrder = append(strOrder, o.str)
			}
		}
		rows = append(rows, o)
	}
	sort.Strings(strOrder)
	if strVar == "" {
		strOrder = []string{""}
	}
	var samples [][]ltObs
	for _, str := range strOrder {
		var sd []ltObs
		for _, o := range rows {
			if strVar == "" || o.str == str {
				sd = append(sd, o)
			}
		}
		if len(sd) > 0 {
			samples = append(samples, sd)
		}
	}
	if len(samples) == 0 {
		return fmt.Errorf("No valid observations after applying the TIME statement.")
	}
	strOrder = strOrder[:len(samples)]

	var sv *Var
	if strVar != "" {
		sv = d.Vars[si]
	}
	var cs *Table
	if strVar == "" {
		cs = ltCensoredRow(nil, nil, Missing, samples[0])
	}
	for i, sd := range samples {
		krows, events := ltKM(sd)
		var strVal Value
		if strVar != "" {
			strVal = sd[0].strV
		}
		ltPLTable(s, d.Vars[ti], sv, strVal, i, krows)
		ltSummary(s, d.Vars[ti], sv, strVal, i, krows, events, alpha, ltRestricted(sd))
		if strVar != "" {
			cs = ltCensoredRow(cs, sv, strVal, sd)
		}
	}
	if strVar == "" && cs != nil {
		ltEmit(s, cs)
	}
	if strVar != "" && cs != nil {
		// total row: control_var = '-'
		tot, failed, cens := 0, 0, 0
		for _, o := range rows {
			tot++
			if o.evt {
				failed++
			} else {
				cens++
			}
		}
		row := []any{StrV("-"), Missing}
		if sv != nil {
			if sv.Char {
				row = append(row, StrV(""))
			} else {
				row = append(row, Missing)
			}
		}
		row = append(row, NumV(float64(tot)), NumV(float64(failed)), NumV(float64(cens)),
			NumV(100 * float64(cens) / float64(tot)))
		cs.AddRow(row...)
		ltEmit(s, cs)
	}
	if strVar != "" && len(samples) >= 2 {
		ltHomogeneity(s, strVar, rows, strOrder[:len(samples)])
	}
	return nil
}

// ltKM computes the product-limit estimates for one sample over all
// observed times.
func ltKM(d []ltObs) ([]ltRow, int) {
	tset := map[float64]bool{}
	for _, o := range d {
		tset[o.t] = true
	}
	tl := make([]float64, 0, len(tset))
	for t := range tset {
		tl = append(tl, t)
	}
	sort.Float64s(tl)
	S := 1.0
	cum := 0.0
	events := 0
	var out []ltRow
	for _, t := range tl {
		Y, dd, cc := 0, 0, 0
		for _, o := range d {
			if o.t >= t {
				Y++
			}
			if o.t == t {
				if o.evt {
					dd++
				} else {
					cc++
				}
			}
		}
		events += dd
		r := ltRow{t: float64(t), y: Y, dd: dd, c: cc, s: S}
		if dd > 0 {
			r.hasS = true
			if Y-dd > 0 {
				S *= 1 - float64(dd)/float64(Y)
				cum += float64(dd) / (float64(Y) * float64(Y-dd))
			} else {
				S = 0
			}
			r.s = S
			if S > 0 {
				r.se = S * math.Sqrt(cum)
				r.hasSE = true
			}
		}
		out = append(out, r)
	}
	return out, events
}

func ltPLTable(s *Session, v *Var, strVar *Var, strVal Value, si int, rows []ltRow) {
	cap := "Product-Limit Survival Estimates"
	if strVar != nil {
		cap = fmt.Sprintf("%s\nStratum %d: %s = %s", cap, si+1, strVar.Name, FormatValue(strVal, nil))
	}
	t := &Table{ODSName: "ProductLimitEstimates", Caption: cap, MissDot: true}
	t.Cols = []Col{
		{Name: "STRATUM", Label: "Stratum", Format: "3."},
	}
	if strVar != nil {
		t.Cols = append(t.Cols, Col{Name: strVar.Name, Label: strVar.Name, Char: strVar.Char, Format: strVar.Format})
	}
	t.Cols = append(t.Cols,
		Col{Name: v.Name, Label: v.Name, Format: "10.3"},
		Col{Name: "Censor", Label: "Censor", Format: "3."},
		Col{Name: "Survival", Label: "Survival", Format: "8.4"},
		Col{Name: "Failure", Label: "Failure", Format: "8.4"},
		Col{Name: "StdErr", Label: "Survival\nStandard\nError", Format: "8.4"},
		Col{Name: "Failed", Label: "Number\nFailed", Format: "6."},
		Col{Name: "Left", Label: "Number\nLeft", Format: "6."})
	t.RowHeader = len(t.Cols) - 7
	N := 0
	for _, r := range rows {
		N += r.dd + r.c
	}
	// SAS's listing: one row per observation, the t=0 origin row first; at a
	// tie group the deaths come first (only the last carries the updated
	// curve), then the censored observations.
	row := []any{NumV(float64(si + 1))}
	if strVar != nil {
		row = append(row, strVal)
	}
	row = append(row, NumV(0), Missing, NumV(1), NumV(0), NumV(0), NumV(0), NumV(float64(N)))
	t.AddRow(row...)
	failed := 0.0
	left := N
	for _, r := range rows {
		for j := 0; j < r.dd; j++ {
			row := []any{NumV(float64(si + 1))}
			if strVar != nil {
				row = append(row, strVal)
			}
			row = append(row, NumV(r.t), NumV(0))
			if j == r.dd-1 {
				if r.hasS {
					row = append(row, NumV(r.s), NumV(1-r.s))
				} else {
					row = append(row, Missing, Missing)
				}
				if r.hasSE {
					row = append(row, NumV(r.se))
				} else {
					row = append(row, Missing)
				}
			} else {
				row = append(row, Missing, Missing, Missing)
			}
			failed++
			left--
			row = append(row, NumV(failed), NumV(float64(left)))
			t.AddRow(row...)
		}
		for j := 0; j < r.c; j++ {
			row := []any{NumV(float64(si + 1))}
			if strVar != nil {
				row = append(row, strVal)
			}
			row = append(row, NumV(r.t), NumV(1), Missing, Missing, Missing, NumV(failed))
			left--
			row = append(row, NumV(float64(left)))
			t.AddRow(row...)
		}
	}
	ltEmit(s, t)
}

func ltSummary(s *Session, v *Var, sv *Var, strVal Value, si int, rows []ltRow, events int, alpha float64, restricted bool) {
	cap := "Summary Statistics of Survival Times"
	t := &Table{ODSName: "Quartiles", Caption: cap, MissDot: true}
	t.Cols = []Col{
		{Name: "STRATUM", Label: "Stratum", Format: "3."},
	}
	if sv != nil {
		t.Cols = append(t.Cols, Col{Name: sv.Name, Label: sv.Name, Char: sv.Char, Format: sv.Format})
	}
	t.Cols = append(t.Cols,
		Col{Name: "Percent", Label: "Percent", Format: "3."},
		Col{Name: "Estimate", Label: v.Name, Format: "10.3"},
		Col{Name: "Transform", Label: "", Char: true},
		Col{Name: "LowerLimit", Label: fmt.Sprintf("%.0f%% Confidence\nLimits", 100*(1-alpha)), Format: "10.3"},
		Col{Name: "UpperLimit", Label: "", Format: "10.3"})
	t.RowHeader = len(t.Cols) - 5
	head := []any{NumV(float64(si + 1))}
	if sv != nil {
		head = append(head, strVal)
	}
	type ept struct {
		t, s, se float64
		hasSE    bool
	}
	var ev []ept
	for _, r := range rows {
		if r.dd > 0 {
			ev = append(ev, ept{r.t, r.s, r.se, r.hasSE})
		}
	}
	z := NormInv(1 - alpha/2)
	_ = z
	bounds := func(pp ept) (lo, hi float64) {
		if pp.s <= 0 {
			return 0, 0
		}
		if !pp.hasSE {
			return pp.s, pp.s
		}
		th := math.Log(-math.Log(pp.s))
		sth := pp.se / (pp.s * math.Abs(math.Log(pp.s)))
		return math.Exp(-math.Exp(th + z*sth)), math.Exp(-math.Exp(th - z*sth))
	}
	for _, pct := range []float64{75, 50, 25} {
		p := pct
		lvl := 1 - p/100
		q, lo, hi := Missing, Missing, Missing
		for i, pp := range ev {
			if pp.s < lvl {
				if i > 0 && ev[i-1].s == lvl {
					q = NumV((ev[i-1].t + pp.t) / 2)
				} else {
					q = NumV(pp.t)
				}
				break
			}
		}
		for _, pp := range ev {
			slo, _ := bounds(pp)
			if lo.IsMissing() && slo < lvl {
				lo = NumV(pp.t)
				break
			}
		}
		for _, pp := range ev {
			if pp.s <= 0 {
				break // the curve reached 0: the upper limit is undefined
			}
			_, shi := bounds(pp)
			if hi.IsMissing() && shi < lvl {
				hi = NumV(pp.t)
				break
			}
		}
		row := append(append([]any{}, head...), NumV(pct))
		row = append(row, q, "LOGLOG", lo, hi)
		t.AddRow(row...)
	}
	// restricted mean over the event-time grid with the Greenwood-type
	// standard error (finite-population corrected, as in SAS)
	D := len(ev)
	mu := 0.0
	Sprev, tprev := 1.0, 0.0
	for _, pp := range ev {
		mu += Sprev * (pp.t - tprev)
		Sprev = pp.s
		tprev = pp.t
	}
	A := make([]float64, D)
	for i := range ev {
		a := 0.0
		for j := i; j < D; j++ {
			tj1 := ev[j].t
			if j+1 < D {
				tj1 = ev[j+1].t
			}
			a += ev[j].s * (tj1 - ev[j].t)
		}
		A[i] = a
	}
	varv := 0.0
	for i := 0; i < D-1; i++ {
		Y, dd := 0, 0
		for _, r := range rows {
			if r.t >= ev[i].t {
				Y += r.dd + r.c
			}
			if r.t == ev[i].t {
				dd = r.dd
			}
		}
		if Y-dd > 0 {
			varv += float64(dd) * A[i] * A[i] / (float64(Y) * float64(Y-dd))
		}
	}
	se := 0.0
	if events > 1 {
		se = math.Sqrt(varv * float64(events) / float64(events-1))
	}
	ltEmit(s, t)

	mt := &Table{ODSName: "Means", Caption: cap, MissDot: true}
	mt.Cols = []Col{
		{Name: "STRATUM", Label: "Stratum", Format: "3."},
	}
	if sv != nil {
		mt.Cols = append(mt.Cols, Col{Name: sv.Name, Label: sv.Name, Char: sv.Char, Format: sv.Format})
	}
	mt.Cols = append(mt.Cols,
		Col{Name: "Mean", Label: "Mean", Format: "10.3"},
		Col{Name: "StdErr", Label: "Standard\nError", Format: "10.3"},
		Col{Name: "Bias", Label: "Biasing", Char: true})
	mt.RowHeader = len(mt.Cols) - 3
	mrow := []any{NumV(float64(si + 1))}
	if sv != nil {
		mrow = append(mrow, strVal)
	}
	bias := "none"
	if restricted {
		bias = "restricted to L where L = max_event_time < max_censored_time"
	}
	mrow = append(mrow, NumV(mu), NumV(se), bias)
	mt.AddRow(mrow...)
	ltEmit(s, mt)
}

// ltRestricted reports whether the largest observed time is censored after
// the largest event time (SAS then reports the mean as restricted).
func ltRestricted(sd []ltObs) bool {
	maxE, maxC := math.Inf(-1), math.Inf(-1)
	for _, o := range sd {
		if o.evt {
			maxE = math.Max(maxE, o.t)
		} else {
			maxC = math.Max(maxC, o.t)
		}
	}
	return maxC > maxE
}

// ltCensoredRow appends one stratum line to the CensoredSummary table
// (created on first use).
func ltCensoredRow(cs *Table, sv *Var, strVal Value, sd []ltObs) *Table {
	if cs == nil {
		cs = &Table{ODSName: "CensoredSummary", Caption: "Summary of the Number of Censored and Uncensored Values", MissDot: true}
		if sv == nil {
			// no STRATA: SAS's reduced single-row layout
			cs.Cols = []Col{
				{Name: "Total", Label: "Total", Format: "8."},
				{Name: "Failed", Label: "Failed", Format: "8."},
				{Name: "Censored", Label: "Censored", Format: "8."},
				{Name: "PctCens", Label: "Percent\nCensored", Format: "8."}}
		} else {
			cs.Cols = []Col{
				{Name: "control_var", Label: "", Char: true, Hidden: true},
				{Name: "Stratum", Label: "Stratum", Format: "3."},
			}
			cs.Cols = append(cs.Cols, Col{Name: sv.Name, Label: sv.Name, Char: sv.Char, Format: sv.Format})
			cs.Cols = append(cs.Cols,
				Col{Name: "Total", Label: "Total", Format: "8."},
				Col{Name: "Failed", Label: "Failed", Format: "8."},
				Col{Name: "Censored", Label: "Censored", Format: "8."},
				Col{Name: "PctCens", Label: "Percent\nCensored", Format: "8."})
		}
		cs.RowHeader = len(cs.Cols) - 5
	}
	tot, failed, cens := 0, 0, 0
	for _, o := range sd {
		tot++
		if o.evt {
			failed++
		} else {
			cens++
		}
	}
	var row []any
	if sv != nil {
		row = []any{StrV(""), NumV(float64(len(cs.Rows) + 1))}
		if sv.Char {
			row = append(row, StrV(strings.TrimRight(strVal.Str, " ")))
		} else {
			row = append(row, strVal)
		}
	} else {
		row = []any{}
	}
	row = append(row, NumV(float64(tot)), NumV(float64(failed)), NumV(float64(cens)),
		NumV(100*float64(cens)/float64(tot)))
	cs.AddRow(row...)
	return cs
}

func ltHomogeneity(s *Session, strVar string, rows []ltObs, strOrder []string) {
	K := strOrder
	tset := map[float64]bool{}
	for _, o := range rows {
		if o.evt {
			tset[o.t] = true
		}
	}
	evl := make([]float64, 0, len(tset))
	for t := range tset {
		evl = append(evl, t)
	}
	sort.Float64s(evl)
	atRisk := func(t float64, str string) int {
		n := 0
		for _, o := range rows {
			if o.t >= t && o.str == str {
				n++
			}
		}
		return n
	}
	dead := func(t float64, str string) int {
		n := 0
		for _, o := range rows {
			if o.t == t && o.evt && o.str == str {
				n++
			}
		}
		return n
	}
	run := func(wfn func(float64) float64) (float64, []float64, [][]float64) {
		k := len(K)
		vv := make([]float64, k)
		V := make([][]float64, k)
		for i := range V {
			V[i] = make([]float64, k)
		}
		for _, t := range evl {
			Y := 0
			d := 0
			for _, o := range rows {
				if o.t >= t {
					Y++
				}
				if o.t == t && o.evt {
					d++
				}
			}
			if Y <= 1 || d == 0 {
				continue
			}
			w := wfn(float64(Y))
			yk := make([]float64, k)
			for gi, g := range K {
				yk[gi] = float64(atRisk(t, g))
			}
			for gi := range K {
				dk := float64(dead(t, K[gi]))
				vv[gi] += w * (dk - yk[gi]*float64(d)/float64(Y))
				for hj := range K {
					if gi == hj {
						V[gi][hj] += w * w * float64(d) * float64(Y-d) * yk[gi] * (float64(Y) - yk[gi]) / (float64(Y) * float64(Y) * (float64(Y) - 1))
					} else {
						V[gi][hj] += w * w * float64(d) * float64(Y-d) * yk[gi] * yk[hj] / (float64(Y) * float64(Y) * (float64(Y) - 1))
					}
				}
			}
		}
		m := k - 1
		A := make([][]float64, m)
		b := make([]float64, m)
		for i := 0; i < m; i++ {
			A[i] = append([]float64{}, V[i][:m]...)
			b[i] = vv[i]
		}
		for i := 0; i < m; i++ {
			for j := i + 1; j < m; j++ {
				f := A[j][i] / A[i][i]
				for l := i; l < m; l++ {
					A[j][l] -= f * A[i][l]
				}
				b[j] -= f * b[i]
			}
		}
		x := make([]float64, m)
		for i := m - 1; i >= 0; i-- {
			sv := b[i]
			for l := i + 1; l < m; l++ {
				sv -= A[i][l] * x[l]
			}
			x[i] = sv / A[i][i]
		}
		chi2 := 0.0
		for i := 0; i < m; i++ {
			chi2 += vv[i] * x[i]
		}
		return chi2, vv, V
	}
	// the run closure also returns the per-group score sums and the
	// covariance matrix, feeding the HomStats and HomCov ODS tables
	lr, lrV, lrC := run(func(y float64) float64 { return 1 })
	wi, wiV, wiC := run(func(y float64) float64 { return y })
	df := len(K) - 1
	strVal := func(g string) Value {
		for _, o := range rows {
			if o.str == g {
				return o.strV
			}
		}
		return Missing
	}
	hs := &Table{ODSName: "HomStats", Caption: fmt.Sprintf("Log-Rank and Wilcoxon Statistics over %s", strVar), MissDot: true}
	hs.Cols = []Col{
		Col{Name: strVar, Label: strVar, Char: true},
		Col{Name: "LogRank", Label: "Log-Rank", Format: "10.3"},
		Col{Name: "Wilcoxon", Label: "Wilcoxon", Format: "10.3"}}
	for gi, g := range K {
		sv := strVal(g)
		if sv.Char {
			hs.AddRow(StrV(strings.TrimRight(sv.Str, " ")), NumV(lrV[gi]), NumV(wiV[gi]))
		} else {
			hs.AddRow(FormatValue(sv, nil), NumV(lrV[gi]), NumV(wiV[gi]))
		}
	}
	ltEmit(s, hs)
	covTable := func(name string, C [][]float64) *Table {
		ct := &Table{ODSName: name, Caption: "", MissDot: true}
		ct.Cols = []Col{{Name: strVar, Label: strVar, Char: true}}
		for _, g := range K {
			ct.Cols = append(ct.Cols, Col{Name: FormatValue(strVal(g), nil), Label: FormatValue(strVal(g), nil), Format: "10.3"})
		}
		for gi, g := range K {
			row := []any{StrV(FormatValue(strVal(g), nil))}
			for hj := range K {
				row = append(row, NumV(C[gi][hj]))
			}
			ct.AddRow(row...)
		}
		return ct
	}
	ltEmit(s, covTable("LogrankHomCov", lrC))
	ltEmit(s, covTable("WilcoxonHomCov", wiC))
	t := &Table{ODSName: "HomTests", Caption: fmt.Sprintf("Test of Equality over %s", strVar), MissDot: true}
	t.Cols = []Col{
		{Name: "Test", Label: "Test", Char: true},
		{Name: "ChiSq", Label: "Chi-Square", Format: "10.4"},
		{Name: "DF", Label: "DF", Format: "4."},
		{Name: "ProbChiSq", Label: "Pr > Chi-Square", Format: "PVALUE6.4"},
	}
	t.RowHeader = 1
	t.AddRow("Log-Rank", lr, df, ChiSqSF(lr, float64(df)))
	t.AddRow("Wilcoxon", wi, df, ChiSqSF(wi, float64(df)))
	// -2Log(LR): exponential likelihood ratio (SAS sign convention)
	Dtot, Ttot := 0, 0.0
	for _, o := range rows {
		Ttot += o.t
		if o.evt {
			Dtot++
		}
	}
	logL := float64(Dtot) * math.Log(float64(Dtot)/Ttot)
	for _, g := range K {
		Dg, Tg := 0, 0.0
		for _, o := range rows {
			if o.str == g {
				Tg += o.t
				if o.evt {
					Dg++
				}
			}
		}
		if Dg > 0 {
			logL -= float64(Dg) * math.Log(float64(Dg)/Tg)
		}
	}
	lr2 := -2 * logL
	t.AddRow("-2Log(LR)", lr2, df, ChiSqSF(lr2, float64(df)))
	ltEmit(s, t)
}
