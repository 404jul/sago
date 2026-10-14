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
	t   float64
	evt bool
	str string
}

// ltRow is one observed time of the product-limit table.
type ltRow struct {
	t           float64
	y, dd, c    int
	s, se       float64
	hasS, hasSE bool
}

func init() { RegisterProc("LIFETEST", ltLifetest) }

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

	for i, sd := range samples {
		krows, events := ltKM(sd)
		str := ""
		if strVar != "" {
			str = strOrder[i]
		}
		ltPLTable(s, d.Vars[ti], strVar, str, i, krows)
		ltSummary(s, strVar, str, krows, events, alpha)
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

func ltPLTable(s *Session, v *Var, strVar, str string, si int, rows []ltRow) {
	cap := "Product-Limit Survival Estimates"
	if strVar != "" {
		cap = fmt.Sprintf("%s\nStratum %d: %s = %s", cap, si+1, strVar, str)
	}
	t := &Table{ODSName: "ProductLimitEstimates", Caption: cap, MissDot: true}
	t.Cols = []Col{
		{Name: "Time", Label: v.Name, Format: "10.3"},
		{Name: "Type", Label: "", Char: true},
		{Name: "Survival", Label: "Survival", Format: "8.4"},
		{Name: "Failure", Label: "Failure", Format: "8.4"},
		{Name: "SurvivalStdErr", Label: "Survival\nStandard\nError", Format: "8.4"},
		{Name: "NumberFailed", Label: "Number\nFailed", Format: "6."},
		{Name: "NumberLeft", Label: "Number\nLeft", Format: "6."},
	}
	t.RowHeader = 1
	for i, r := range rows {
		left := 0
		for j := i + 1; j < len(rows); j++ {
			left += rows[j].dd + rows[j].c
		}
		failed := 0
		for j := 0; j <= i; j++ {
			failed += rows[j].dd
		}
		mk := ""
		if r.c > 0 {
			mk = "*"
		}
		var sv, fv, se Value = Missing, Missing, Missing
		if r.hasS {
			sv = NumV(r.s)
			fv = NumV(1 - r.s)
		}
		if r.hasSE {
			se = NumV(r.se)
		}
		t.AddRow(NumV(r.t), mk, sv, fv, se, failed, left)
	}
	for _, r := range rows {
		if r.c > 0 {
			t.Notes = append(t.Notes, "The marked survival times are censored observations.")
			break
		}
	}
	s.Out.AddTable(t)
}

func ltSummary(s *Session, strVar, str string, rows []ltRow, events int, alpha float64) {
	cap := "Summary Statistics of Survival Times"
	if strVar != "" {
		cap = fmt.Sprintf("%s for %s = %s", cap, strVar, str)
	}
	t := &Table{ODSName: "QuartileEstimates", Caption: cap, MissDot: true}
	t.Cols = []Col{
		{Name: "Percent", Label: "Percent", Char: true},
		{Name: "Estimate", Label: "Point\nEstimate", Format: "10.3"},
		{Name: "LowerCL", Label: fmt.Sprintf("%.0f%% Confidence\nLimits", 100*(1-alpha)), Format: "10.3"},
		{Name: "UpperCL", Label: "", Format: "10.3"},
	}
	t.RowHeader = 1
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
	for _, pc := range []string{"75", "50", "25"} {
		p, _ := parseNumber(pc)
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
			_, shi := bounds(pp)
			if hi.IsMissing() && shi < lvl {
				hi = NumV(pp.t)
				break
			}
		}
		t.AddRow(pc, q, lo, hi)
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
	t.AddRow("Mean", NumV(mu), Missing, Missing)
	t.AddRow("Standard Error", NumV(se), Missing, Missing)
	s.Out.AddTable(t)
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
	run := func(wfn func(float64) float64) float64 {
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
		return chi2
	}
	t := &Table{ODSName: "HomogeneityTests", Caption: fmt.Sprintf("Test of Equality over %s", strVar), MissDot: true}
	t.Cols = []Col{
		{Name: "Test", Label: "Test", Char: true},
		{Name: "ChiSquare", Label: "Chi-Square", Format: "10.4"},
		{Name: "DF", Label: "DF", Format: "4."},
		{Name: "ProbChiSq", Label: "Pr > Chi-Square", Format: "PVALUE6.4"},
	}
	t.RowHeader = 1
	lr := run(func(y float64) float64 { return 1 })
	wi := run(func(y float64) float64 { return y })
	df := len(K) - 1
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
	s.Out.AddTable(t)
}
