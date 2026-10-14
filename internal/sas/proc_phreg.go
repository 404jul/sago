package sas

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// PROC PHREG (ph prefix): Cox proportional hazards regression by
// maximum partial likelihood (Newton-Raphson). TIES=BRESLOW (default)
// and TIES=EFRON. ODS tables: FitStatistics, GlobalTests,
// ParameterEstimates.

func init() { RegisterProc("PHREG", phPhreg) }

type phObs struct {
	t   float64
	evt bool
	x   []float64
}

func phPhreg(s *Session, p *ProcStep) error {
	d, err := s.ProcData(p)
	if err != nil {
		return err
	}
	mst := p.First("MODEL")
	if mst == nil || len(mst.Args()) == 0 {
		return fmt.Errorf("The MODEL statement is required.")
	}
	// MODEL time*censor(list) = x1 x2 ...;
	txt := TokText(mst.Args())
	k := strings.Index(txt, "=")
	if k < 0 {
		return fmt.Errorf("MODEL statement must be of the form time*censor(list)=effects.")
	}
	lhs := strings.TrimSpace(txt[:k])
	rhs := strings.TrimSpace(txt[k+1:])
	if rhs == "" {
		return fmt.Errorf("No explanatory variables on the MODEL statement.")
	}
	parts := strings.SplitN(lhs, "*", 2)
	if len(parts) != 2 {
		return fmt.Errorf("MODEL statement must be of the form time*censor(list)=effects.")
	}
	timeVar := strings.TrimSpace(parts[0])
	censSpec := strings.TrimSpace(parts[1])
	ti := d.Index(timeVar)
	if ti < 0 {
		return fmt.Errorf("Variable %s not found.", strings.ToUpper(timeVar))
	}
	censVals := map[float64]bool{}
	censName := censSpec
	if q := strings.Index(censSpec, "("); q >= 0 {
		censName = strings.TrimSpace(censSpec[:q])
		list := strings.Trim(censSpec[q:], "()")
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
	var names []string
	for _, f := range strings.Fields(rhs) {
		names = append(names, f)
	}
	cols := make([]int, len(names))
	for i, n := range names {
		cols[i] = d.Index(n)
		if cols[i] < 0 {
			return fmt.Errorf("Variable %s not found.", strings.ToUpper(n))
		}
	}
	ties := "BRESLOW"
	if p.Opts.Has("TIES") {
		ties = strings.ToUpper(p.Opts.Str("TIES", "BRESLOW"))
	}
	if ties != "BRESLOW" && ties != "EFRON" {
		return fmt.Errorf("TIES=%s is not supported; use BRESLOW or EFRON.", ties)
	}

	var obs []phObs
	for _, r := range d.Rows {
		if r[ti].IsMissing() || r[ci].IsMissing() {
			continue
		}
		skip := false
		x := make([]float64, len(cols))
		for i, c := range cols {
			if r[c].IsMissing() {
				skip = true
				break
			}
			x[i] = r[c].Num
		}
		if skip {
			continue
		}
		obs = append(obs, phObs{t: r[ti].Num, evt: !censVals[r[ci].Num], x: x})
	}
	if len(obs) < 2 {
		return fmt.Errorf("There are fewer than 2 valid observations for the Cox regression.")
	}
	nvar := len(names)

	logL0 := phLogL0(obs)
	var beta []float64
	if ties == "EFRON" {
		beta = phNewton(obs, nvar, phEfron)
	} else {
		beta = phNewton(obs, nvar, phBreslow)
	}
	ll, _, info := phBreslow(obs, nvar, beta)
	if ties == "EFRON" {
		ll, _, info = phEfron(obs, nvar, beta)
	}
	npar := float64(nvar)
	minus2 := -2 * ll
	aic := minus2 + 2*npar
	// SAS PHREG bases SBC on the number of events
	nEvents := 0
	for _, o := range obs {
		if o.evt {
			nEvents++
		}
	}
	sbc := minus2 + npar*math.Log(float64(nEvents))

	// Wald chi-square per parameter: SEs are the square roots of the
	// diagonal of the INVERSE information matrix.
	inv := phMatInv(info)
	sd := make([]float64, nvar)
	wald := make([]float64, nvar)
	hr := make([]float64, nvar)
	for i := 0; i < nvar; i++ {
		sd[i] = math.Sqrt(math.Abs(inv[i][i]))
		wald[i] = beta[i] * beta[i] / inv[i][i]
		hr[i] = math.Exp(beta[i])
	}
	// global tests
	lrTest := -2 * (ll - logL0)
	scoreChi := 0.0
	{
		// score(0)' I(0)^-1 score(0)
		_, s0, i0 := phBreslow(obs, nvar, make([]float64, nvar))
		if ties == "EFRON" {
			_, s0, i0 = phEfron(obs, nvar, make([]float64, nvar))
		}
		A := make([][]float64, nvar)
		b := append([]float64{}, s0...)
		for r := range A {
			A[r] = append([]float64{}, i0[r]...)
		}
		for piv := 0; piv < nvar; piv++ {
			for r := piv + 1; r < nvar; r++ {
				fq := A[r][piv] / A[piv][piv]
				for l := piv; l < nvar; l++ {
					A[r][l] -= fq * A[piv][l]
				}
				b[r] -= fq * b[piv]
			}
		}
		z := make([]float64, nvar)
		for r := nvar - 1; r >= 0; r-- {
			sv := b[r]
			for l := r + 1; l < nvar; l++ {
				sv -= A[r][l] * z[l]
			}
			z[r] = sv / A[r][r]
		}
		for i := 0; i < nvar; i++ {
			scoreChi += s0[i] * z[i]
		}
	}
	waldG := 0.0
	for i := 0; i < nvar; i++ {
		for j := 0; j < nvar; j++ {
			waldG += beta[i] * beta[j] * info[i][j]
		}
	}
	df := float64(nvar)

	t := &Table{ODSName: "FitStatistics", Caption: "Model Fit Statistics", MissDot: true}
	t.Cols = []Col{
		{Name: "Criterion", Label: "Criterion", Char: true},
		{Name: "WithoutCovariates", Label: "Without\nCovariates", Format: "10.3"},
		{Name: "WithCovariates", Label: "With\nCovariates", Format: "10.3"},
	}
	t.RowHeader = 1
	t.AddRow("-2 LOG L", logL0*-2, minus2)
	t.AddRow("AIC", logL0*-2, aic)
	t.AddRow("SBC", logL0*-2, sbc)
	s.Out.AddTable(t)

	t2 := &Table{ODSName: "GlobalTests", Caption: "Testing Global Null Hypothesis: BETA=0", MissDot: true}
	t2.Cols = []Col{
		{Name: "Test", Label: "Test", Char: true},
		{Name: "ChiSquare", Label: "Chi-Square", Format: "10.4"},
		{Name: "DF", Label: "DF", Format: "4."},
		{Name: "ProbChiSq", Label: "Pr > ChiSq", Format: "PVALUE6.4"},
	}
	t2.RowHeader = 1
	t2.AddRow("Likelihood Ratio", math.Abs(lrTest), df, ChiSqSF(math.Abs(lrTest), df))
	t2.AddRow("Score", scoreChi, df, ChiSqSF(scoreChi, df))
	t2.AddRow("Wald", waldG, df, ChiSqSF(waldG, df))
	s.Out.AddTable(t2)

	t3 := &Table{ODSName: "ParameterEstimates", Caption: "Analysis of Maximum Likelihood Estimates", MissDot: true}
	t3.Cols = []Col{
		{Name: "Parameter", Label: "Parameter", Char: true},
		{Name: "DF", Label: "DF", Format: "4."},
		{Name: "Estimate", Label: "Parameter\nEstimate", Format: "10.5"},
		{Name: "StdErr", Label: "Standard\nError", Format: "10.5"},
		{Name: "ChiSquare", Label: "Chi-Square", Format: "8.4"},
		{Name: "ProbChiSq", Label: "Pr > ChiSq", Format: "PVALUE6.4"},
		{Name: "HazardRatio", Label: "Hazard\nRatio", Format: "10.3"},
	}
	t3.RowHeader = 1
	for i, nme := range names {
		t3.AddRow(nme, 1.0, beta[i], sd[i], wald[i], ChiSqSF(wald[i], 1), hr[i])
	}
	s.Out.AddTable(t3)
	return nil
}

// phLogL0 returns the log partial likelihood at beta = 0.
func phLogL0(obs []phObs) float64 {
	ll := 0.0
	for _, t := range phEventTimes(obs) {
		Y := 0
		d := 0
		for _, o := range obs {
			if o.t >= t {
				Y++
			}
			if o.t == t && o.evt {
				d++
			}
		}
		if d > 0 {
			ll -= float64(d) * math.Log(float64(Y))
		}
	}
	return ll
}

func phEventTimes(obs []phObs) []float64 {
	set := map[float64]bool{}
	for _, o := range obs {
		if o.evt {
			set[o.t] = true
		}
	}
	var out []float64
	for t := range set {
		out = append(out, t)
	}
	sort.Float64s(out)
	return out
}

// phBreslow computes logL, score and information for the Breslow ties
// handling at the given beta.
func phBreslow(obs []phObs, nvar int, beta []float64) (float64, []float64, [][]float64) {
	ll := 0.0
	score := make([]float64, nvar)
	info := make([][]float64, nvar)
	for i := range info {
		info[i] = make([]float64, nvar)
	}
	for _, t := range phEventTimes(obs) {
		var s0, s0d float64
		s1 := make([]float64, nvar)
		s1d := make([]float64, nvar)
		s2 := make([][]float64, nvar)
		s2d := make([][]float64, nvar)
		for i := range s2 {
			s2[i] = make([]float64, nvar)
			s2d[i] = make([]float64, nvar)
		}
		var sumxD []float64
		dd := 0
		for _, o := range obs {
			if o.t < t {
				continue
			}
			eta := 0.0
			for i := 0; i < nvar; i++ {
				eta += beta[i] * o.x[i]
			}
			w := math.Exp(eta)
			s0 += w
			for i := 0; i < nvar; i++ {
				s1[i] += w * o.x[i]
				for j := 0; j < nvar; j++ {
					s2[i][j] += w * o.x[i] * o.x[j]
				}
			}
			if o.t == t && o.evt {
				dd++
				s0d += w
				for i := 0; i < nvar; i++ {
					s1d[i] += w * o.x[i]
					for j := 0; j < nvar; j++ {
						s2d[i][j] += w * o.x[i] * o.x[j]
					}
				}
				if sumxD == nil {
					sumxD = make([]float64, nvar)
				}
				for i := 0; i < nvar; i++ {
					sumxD[i] += o.x[i]
				}
			}
		}
		if dd == 0 {
			continue
		}
		ll += sumxD[nvar-1]*0 + dot(sumxD, beta) - float64(dd)*math.Log(s0)
		for i := 0; i < nvar; i++ {
			score[i] += sumxD[i] - float64(dd)*s1[i]/s0
			for j := 0; j < nvar; j++ {
				info[i][j] += float64(dd) * (s2[i][j]/s0 - s1[i]*s1[j]/(s0*s0))
			}
		}
		_ = s1d
		_ = s2d
		_ = s0d
	}
	return ll, score, info
}

// phEfron computes logL, score and information for the Efron ties
// handling at the given beta.
func phEfron(obs []phObs, nvar int, beta []float64) (float64, []float64, [][]float64) {
	ll := 0.0
	score := make([]float64, nvar)
	info := make([][]float64, nvar)
	for i := range info {
		info[i] = make([]float64, nvar)
	}
	for _, t := range phEventTimes(obs) {
		var s0 float64
		s1 := make([]float64, nvar)
		s2 := make([][]float64, nvar)
		for i := range s2 {
			s2[i] = make([]float64, nvar)
		}
		var s0d float64
		s1d := make([]float64, nvar)
		s2d := make([][]float64, nvar)
		for i := range s2d {
			s2d[i] = make([]float64, nvar)
		}
		var sumxD []float64
		dd := 0
		for _, o := range obs {
			if o.t < t {
				continue
			}
			eta := 0.0
			for i := 0; i < nvar; i++ {
				eta += beta[i] * o.x[i]
			}
			w := math.Exp(eta)
			s0 += w
			for i := 0; i < nvar; i++ {
				s1[i] += w * o.x[i]
				for j := 0; j < nvar; j++ {
					s2[i][j] += w * o.x[i] * o.x[j]
				}
			}
			if o.t == t && o.evt {
				dd++
				s0d += w
				for i := 0; i < nvar; i++ {
					s1d[i] += w * o.x[i]
					for j := 0; j < nvar; j++ {
						s2d[i][j] += w * o.x[i] * o.x[j]
					}
				}
				if sumxD == nil {
					sumxD = make([]float64, nvar)
				}
				for i := 0; i < nvar; i++ {
					sumxD[i] += o.x[i]
				}
			}
		}
		if dd == 0 {
			continue
		}
		ll += dot(sumxD, beta)
		df := float64(dd)
		for l := 0.0; l < df; l++ {
			f := l / df
			a0 := s0 - f*s0d
			ll -= math.Log(a0)
			a1 := make([]float64, nvar)
			a2 := make([][]float64, nvar)
			for i := range a2 {
				a2[i] = make([]float64, nvar)
			}
			for i := 0; i < nvar; i++ {
				a1[i] = s1[i] - f*s1d[i]
				for j := 0; j < nvar; j++ {
					a2[i][j] = s2[i][j] - f*s2d[i][j]
				}
			}
			for i := 0; i < nvar; i++ {
				score[i] -= a1[i] / a0
				for j := 0; j < nvar; j++ {
					info[i][j] += a2[i][j]/a0 - a1[i]*a1[j]/(a0*a0)
				}
			}
		}
		for i := 0; i < nvar; i++ {
			score[i] += sumxD[i]
		}
	}
	return ll, score, info
}

func dot(a, b []float64) float64 {
	s := 0.0
	for i := range a {
		s += a[i] * b[i]
	}
	return s
}

// phNewton runs the Newton-Raphson iteration to maximize the partial
// log-likelihood, starting at beta = 0 with step halving.
func phNewton(obs []phObs, nvar int, f func([]phObs, int, []float64) (float64, []float64, [][]float64)) []float64 {
	beta := make([]float64, nvar)
	ll, score, info := f(obs, nvar, beta)
	for iter := 0; iter < 50; iter++ {
		// Newton step: beta += info^-1 score
		step := make([]float64, nvar)
		A := make([][]float64, nvar)
		b := append([]float64{}, score...)
		for i := range A {
			A[i] = append([]float64{}, info[i]...)
		}
		sing := false
		for piv := 0; piv < nvar; piv++ {
			if math.Abs(A[piv][piv]) < 1e-12 {
				sing = true
				break
			}
			for r := piv + 1; r < nvar; r++ {
				fq := A[r][piv] / A[piv][piv]
				for l := piv; l < nvar; l++ {
					A[r][l] -= fq * A[piv][l]
				}
				b[r] -= fq * b[piv]
			}
		}
		if sing {
			break
		}
		for i := nvar - 1; i >= 0; i-- {
			sv := b[i]
			for l := i + 1; l < nvar; l++ {
				sv -= A[i][l] * step[l]
			}
			step[i] = sv / A[i][i]
		}
		// step halving
		halves := 0
		var nll float64
		var nsc []float64
		var ninf [][]float64
		for {
			nb := make([]float64, nvar)
			for i := range beta {
				nb[i] = beta[i] + step[i]
			}
			nll, nsc, ninf = f(obs, nvar, nb)
			if nll >= ll-1e-12 || halves >= 20 {
				beta = nb
				break
			}
			for i := range step {
				step[i] /= 2
			}
			halves++
		}
		if math.Abs(nll-ll) < 1e-8*math.Abs(ll)+1e-10 {
			ll = nll
			break
		}
		ll, score, info = nll, nsc, ninf
	}
	return beta
}

// phMatInv inverts a small symmetric positive-definite matrix by
// Gauss-Jordan elimination.
func phMatInv(m [][]float64) [][]float64 {
	n := len(m)
	a := make([][]float64, n)
	for i := range a {
		a[i] = append([]float64{}, m[i]...)
		a[i] = append(a[i], make([]float64, n)...)
		a[i][n+i] = 1
	}
	for c := 0; c < n; c++ {
		piv := a[c][c]
		if piv == 0 {
			return m
		}
		for j := 0; j < 2*n; j++ {
			a[c][j] /= piv
		}
		for r := 0; r < n; r++ {
			if r == c {
				continue
			}
			f := a[r][c]
			for j := 0; j < 2*n; j++ {
				a[r][j] -= f * a[c][j]
			}
		}
	}
	out := make([][]float64, n)
	for i := range out {
		out[i] = append([]float64{}, a[i][n:]...)
	}
	return out
}
