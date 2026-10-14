package sas

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// PROC NPAR1WAY (np prefix): Wilcoxon rank-sum, Kruskal-Wallis, median,
// van der Waerden, Savage, Siegel-Tukey, Ansari-Bradley, Klotz, Mood and
// Conover score tests, plus EDF statistics. ODS tables: WilcoxonScores,
// WilcoxonTest, KruskalWallisTest, MedianScores, MedianTest, ANOVAScores,
// ANOVATest, VWScores, VWTest, SavageScores, SavageAnalysis, SavageTest,
// STScores, STAnalysis, STTest, ABScores, ABAnalysis, ABTest, KlotzScores,
// KlotzAnalysis, KlotzTest, MoodScores, MoodAnalysis, MoodTest,
// ConoverScores, ConoverAnalysis, ConoverTest, FPPlacements, FPTest,
// DSCF.

func init() { RegisterProc("NPAR1WAY", npNpar1way) }

func npNpar1way(s *Session, p *ProcStep) error {
	d, err := s.ProcData(p)
	if err != nil {
		return err
	}
	st := p.First("CLASS")
	if st == nil || len(st.Args()) == 0 {
		return fmt.Errorf("The CLASS statement is required.")
	}
	ci := d.Index(st.Args()[0].Text)
	if ci < 0 {
		return fmt.Errorf("Variable %s not found.", strings.ToUpper(st.Args()[0].Text))
	}
	var vars []string
	if vst := p.First("VAR"); vst != nil {
		vars, err = ExpandVarList(d, vst.Args())
		if err != nil {
			return err
		}
	} else {
		for _, v := range d.Vars {
			if !v.Char && !strings.EqualFold(v.Name, d.Vars[ci].Name) {
				vars = append(vars, v.Name)
			}
		}
	}
	want := map[string]bool{}
	anyAnalysis := false
	var headerToks []Token
	if p.Header != nil && len(p.Header.Toks) > 2 {
		headerToks = npOptionToks(p.Header.Toks[2:])
	}
	for _, t := range headerToks {
		switch strings.ToUpper(t.Text) {
		case "WILCOXON":
			want["WILCOXON"], anyAnalysis = true, true
		case "MEDIAN":
			want["MEDIAN"], anyAnalysis = true, true
		case "ANOVA":
			want["ANOVA"], anyAnalysis = true, true
		case "VW":
			want["VW"], anyAnalysis = true, true
		case "SAVAGE":
			want["SAVAGE"], anyAnalysis = true, true
		case "HL":
			// HL invokes WILCOXON and adds location-shift estimation
			want["WILCOXON"], want["HL"], anyAnalysis = true, true, true
		case "FP":
			want["FP"], anyAnalysis = true, true
		case "DSCF":
			want["DSCF"], anyAnalysis = true, true
		case "ST":
			want["ST"], anyAnalysis = true, true
		case "CONOVER":
			want["CONOVER"], anyAnalysis = true, true
		case "PERM":
			anyAnalysis = true // accepted but not implemented
		case "MOOD":
			want["MOOD"], anyAnalysis = true, true
		case "KLOTZ":
			want["KLOTZ"], anyAnalysis = true, true
		case "AB":
			want["AB"], anyAnalysis = true, true
		case "EDF", "KS":
			want["EDF"], anyAnalysis = true, true
		case "D":
			// D invokes EDF and adds the one-sided statistics.
			want["EDF"], want["D"], anyAnalysis = true, true, true
		}
	}
	if !anyAnalysis {
		want["WILCOXON"], want["MEDIAN"], want["VW"], want["ANOVA"] = true, true, true, true
		want["SAVAGE"], want["EDF"] = true, true
	}
	exact := false
	exactHL := false
	alpha := p.Opts.Num("ALPHA", 0.05)
	if st := p.First("EXACT"); st != nil {
		for _, t := range st.Args() {
			if t.Is("WILCOXON") {
				exact = true
			}
			if t.Is("HL") {
				exactHL = true
			}
		}
	}
	by, groups, err := s.ProcBy(p, d)
	if err != nil {
		return err
	}
	for _, g := range groups {
		sub := d.Subset(g.Rows)
		if by != nil {
			s.Out.SetBy(by.Vars, g.Keys)
		}
		for _, name := range vars {
			if err := npOneVar(s, sub, d.Var(name), ci, want, exact, exactHL, alpha); err != nil {
				return err
			}
		}
	}
	return nil
}

// npObs returns class levels (sorted), the observations, and the 1-based
// source row numbers of the observations used.
func npObs(d *Dataset, v *Var, ci int) (classes []Value, x []float64, cls []int, rowno []int) {
	seen := map[string]int{}
	for ri, row := range d.Rows {
		c := row[ci]
		y := row[d.Index(v.Name)]
		if c.IsMissing() || y.IsMissing() {
			continue
		}
		k := FormatValue(c, nil)
		idx, ok := seen[k]
		if !ok {
			idx = len(classes)
			seen[k] = idx
			classes = append(classes, c)
		}
		x = append(x, y.Num)
		cls = append(cls, idx)
		rowno = append(rowno, ri+1)
	}
	order := make([]int, len(classes))
	for i := range order {
		order[i] = i
	}
	SortIdxByValue(classes, order)
	remap := make([]int, len(classes))
	for newIdx, oldIdx := range order {
		remap[oldIdx] = newIdx
	}
	outCls := make([]int, len(cls))
	for i, c := range cls {
		outCls[i] = remap[c]
	}
	outClasses := make([]Value, len(classes))
	for i, o := range order {
		outClasses[i] = classes[o]
	}
	return outClasses, x, outCls, rowno
}

// SortIdxByValue sorts idx in place by the corresponding values.
func SortIdxByValue(vals []Value, idx []int) {
	for i := 1; i < len(idx); i++ {
		for j := i; j > 0 && Compare(vals[idx[j]], vals[idx[j-1]]) < 0; j-- {
			idx[j], idx[j-1] = idx[j-1], idx[j]
		}
	}
}

// npOptionToks drops NAME=value assignments from the proc options, so values
// such as the data set name in data=d are not mistaken for analysis options.
func npOptionToks(toks []Token) []Token {
	var out []Token
	for i := 0; i < len(toks); i++ {
		if toks[i].Kind == TIdent && i+1 < len(toks) && toks[i+1].IsOp("=") {
			i++ // skip "="
			if i+1 < len(toks) && toks[i+1].Kind != TOp {
				i++ // skip the single-token value
			}
			continue
		}
		out = append(out, toks[i])
	}
	return out
}

func npOneVar(s *Session, d *Dataset, v *Var, ci int, want map[string]bool, exact, exactHL bool, alpha float64) error {
	if v == nil {
		return fmt.Errorf("variable not found")
	}
	classes, x, cls, rowno := npObs(d, v, ci)
	k := len(classes)
	if k < 2 {
		s.Log.Warn(0, fmt.Sprintf("NPAR1WAY: fewer than two class levels with data for variable %s.", v.Name))
		return nil
	}
	n := len(x)
	ranks := Ranks(x)
	nip := make([]float64, k)
	for _, c := range cls {
		nip[c]++
	}
	classVar := d.Vars[ci]

	if want["WILCOXON"] {
		sums := make([]float64, k)
		for i, r := range ranks {
			sums[cls[i]] += r
		}
		tieF := npTieFactor(ranks)
		t := &Table{
			ODSName: "WilcoxonScores",
			Caption: fmt.Sprintf("Wilcoxon Scores (Rank Sums) for Variable %s\nClassified by Variable %s", v.Name, classVar.Name),
			MissDot: true,
		}
		t.Cols = []Col{
			{Name: "Class", Label: classVar.Name, Char: classVar.Char},
			{Name: "N", Label: "N", Format: "6."},
			{Name: "SumOfScores", Label: "Sum of\nScores", Format: "12.4"},
			{Name: "ExpectedUnderH0", Label: "Expected\nUnder H0", Format: "12.4"},
			{Name: "StdDevUnderH0", Label: "Std Dev\nUnder H0", Format: "10.4"},
			{Name: "MeanScore", Label: "Mean\nScore", Format: "10.4"},
		}
		t.RowHeader = 1
		sds := make([]float64, k)
		for i := range k {
			e := nip[i] * (float64(n) + 1) / 2
			sds[i] = math.Sqrt(npVarScore(nip[i], float64(n), tieF))
			t.AddRow(classes[i], nip[i], sums[i], e, sds[i], e/nip[i])
		}
		s.Out.AddTable(t)
		if k == 2 {
			npWilcoxonTest(s, v, classVar, classes, nip, sums, sds, float64(n), exact, x, cls)
		} else {
			npKWTest(s, v, classes, nip, sums, float64(n), tieF)
		}
	}
	if want["MEDIAN"] {
		npMedianTest(s, v, classVar, classes, x, cls, nip)
	}
	if want["ANOVA"] {
		npAnova(s, v, classVar, classes, x, cls, nip)
	}
	if want["VW"] {
		npVW(s, v, classVar, classes, x, cls, ranks, nip)
	}
	if want["EDF"] {
		npEDF(s, v, classVar, classes, x, cls, rowno, nip, want["D"])
	}
	if want["MOOD"] {
		npScale(s, "MOOD", "Mood", "Mood", v, classVar, classes, x, cls, ranks, nip)
	}
	if want["KLOTZ"] {
		npScale(s, "KLOTZ", "Klotz", "Klotz", v, classVar, classes, x, cls, ranks, nip)
	}
	if want["AB"] {
		npScale(s, "AB", "AB", "Ansari-Bradley", v, classVar, classes, x, cls, ranks, nip)
	}
	if want["SAVAGE"] {
		npScale(s, "SAVAGE", "Savage", "Savage", v, classVar, classes, x, cls, ranks, nip)
	}
	if want["ST"] {
		npScale(s, "ST", "ST", "Siegel-Tukey", v, classVar, classes, x, cls, ranks, nip)
	}
	if want["CONOVER"] {
		npScale(s, "CONOVER", "Conover", "Conover", v, classVar, classes, x, cls, ranks, nip)
	}
	if want["HL"] {
		if k == 2 {
			npHL(s, v, classVar, classes, x, cls, alpha, exactHL)
		} else {
			s.Log.Warn(0, "NPAR1WAY: the HL option requires two class levels.")
		}
	}
	if want["FP"] {
		if k == 2 {
			npFP(s, v, classVar, classes, x, cls)
		} else {
			s.Log.Warn(0, "NPAR1WAY: the FP option requires two class levels.")
		}
	}
	if want["DSCF"] {
		if k > 2 {
			npDSCF(s, v, classVar, classes, x, cls)
		} else {
			s.Log.Warn(0, "NPAR1WAY: the DSCF option requires more than two class levels.")
		}
	}
	return nil
}

// npDSCF: Dwass-Steel-Critchlow-Fligner all-pairs multiple comparison
// analysis. Each pair is ranked within the pair; the two-sample
// standardized Wilcoxon z (tie-corrected, no continuity correction) is
// scaled by sqrt(2) and the two-sided p-value is the upper tail of the
// studentized range distribution with k = number of classes and df = Inf.
func npDSCF(s *Session, v *Var, classVar *Var, classes []Value, x []float64, cls []int) {
	k := len(classes)
	type cmp struct {
		i, j int
		z    float64
	}
	var cmps []cmp
	for i := 0; i < k; i++ {
		for j := i + 1; j < k; j++ {
			var xs, ys []float64
			for t, c := range cls {
				if c == i {
					xs = append(xs, x[t])
				} else if c == j {
					ys = append(ys, x[t])
				}
			}
			pooled := append(append([]float64{}, xs...), ys...)
			n1, n2 := len(xs), len(ys)
			N := float64(n1 + n2)
			ranks := Ranks(pooled)
			sum := 0.0
			for t := 0; t < n1; t++ {
				sum += ranks[t]
			}
			tieF := npTieFactor(ranks)
			e := float64(n1) * (N + 1) / 2
			vr := npVarScore(float64(n1), N, tieF)
			if vr == 0 {
				continue
			}
			cmps = append(cmps, cmp{i, j, (sum - e) / math.Sqrt(vr)})
		}
	}
	t := &Table{ODSName: "DSCF", Caption: "Pairwise Two-Sided Multiple Comparison Analysis", MissDot: true}
	t.Cols = []Col{
		{Name: "Comparison", Label: "Comparison", Char: true},
		{Name: "WilcoxonZ", Label: "Two-Sample\nWilcoxon Z", Format: "8.4"},
		{Name: "DSCF", Label: "DSCF\nStatistic", Format: "8.4"},
		{Name: "ProbDSCF", Label: "Two-Sided\nDSCF Pr > DSCF", Format: "PVALUE6.4"},
	}
	t.RowHeader = 1
	for _, c := range cmps {
		d := math.Sqrt2 * math.Abs(c.z)
		p := 1 - lmPTukey(d, 1, float64(k), 1e15)
		t.AddRow(fmt.Sprintf("%s - %s", classes[c.i].Str, classes[c.j].Str), c.z, math.Sqrt2*c.z, p)
	}
	s.Out.AddTable(t)
}

// npFP: Fligner-Policello test of equal medians (Fligner and Policello
// 1981). Placements count the observations of the other class below each
// observation (ties count 0.5). SAS's statistic uses the denominator
// 2*sqrt(Vx + Vy + Pbar_x*Pbar_y); the reference class X (larger sample;
// ties -> second class) is subtracted: difference = sumP(Y) - sumP(X).
func npFP(s *Session, v *Var, classVar *Var, classes []Value, x []float64, cls []int) {
	ni := [2]int{}
	for _, c := range cls {
		ni[c]++
	}
	// reference class X: the larger sample; if equal, the second class
	xi := 1
	if ni[0] > ni[1] {
		xi = 0
	}
	yi := 1 - xi
	var X, Y []float64
	for i, c := range cls {
		if c == xi {
			X = append(X, x[i])
		} else {
			Y = append(Y, x[i])
		}
	}
	place := func(a float64, b []float64) float64 {
		p := 0.0
		for _, v := range b {
			if v < a {
				p++
			} else if v == a {
				p += 0.5
			}
		}
		return p
	}
	PX := make([]float64, len(X))
	PY := make([]float64, len(Y))
	sumX, sumY := 0.0, 0.0
	for i, xx := range X {
		PX[i] = place(xx, Y)
		sumX += PX[i]
	}
	for j, yy := range Y {
		PY[j] = place(yy, X)
		sumY += PY[j]
	}
	nx, ny := float64(len(X)), float64(len(Y))
	meanX, meanY := sumX/nx, sumY/ny
	vx, vy := 0.0, 0.0
	for _, p := range PX {
		vx += (p - meanX) * (p - meanX)
	}
	for _, q := range PY {
		vy += (q - meanY) * (q - meanY)
	}
	diff := sumY - sumX
	z := diff / (2 * math.Sqrt(vx+vy+meanX*meanY))
	tp := &Table{
		ODSName: "FPPlacements",
		Caption: fmt.Sprintf("Fligner-Policello Placements for Variable %s\nClassified by Variable %s", v.Name, classVar.Name),
		MissDot: true,
	}
	tp.Cols = []Col{
		{Name: "Class", Label: classVar.Name, Char: true},
		{Name: "N", Label: "N", Format: "6."},
		{Name: "SumOfPlacements", Label: "Sum of\nPlacements", Format: "12.4"},
		{Name: "AvgPlacement", Label: "Average\nPlacement", Format: "12.4"},
		{Name: "StdDev", Label: "Std Dev\nof Placements", Format: "12.4"},
	}
	tp.RowHeader = 1
	sdX := math.Sqrt(vx / (nx - 1))
	sdY := math.Sqrt(vy / (ny - 1))
	stats := map[int][3]float64{xi: {nx, sumX, sdX}, yi: {ny, sumY, sdY}}
	means := map[int]float64{xi: meanX, yi: meanY}
	for ci := 0; ci < 2; ci++ {
		st := stats[ci]
		tp.AddRow(classes[ci], st[0], st[1], means[ci], st[2])
	}
	s.Out.AddTable(tp)

	t2 := &Table{ODSName: "FPTest", Caption: "Fligner-Policello Test", MissDot: true}
	t2.Cols = []Col{{Name: "Label", Label: "", Char: true}, {Name: "Value", Label: "", Format: "12.4"}}
	t2.RowHeader = 1
	t2.AddRow("Difference", diff)
	t2.AddRow("Statistic (Z)", z)
	if z > 0 {
		t2.AddRow("One-Sided Pr > Z", npPStr(NormSF(z)))
	} else {
		t2.AddRow("One-Sided Pr < Z", npPStr(NormCDF(z)))
	}
	t2.AddRow("Two-Sided Pr > |Z|", npPStr(2*NormSF(math.Abs(z))))
	s.Out.AddTable(t2)
}

// npHL: Hodges-Lehmann estimation of the location shift between two
// samples with asymptotic (Moses) and, if requested, exact confidence
// limits. The reference class X (larger sample; ties -> second class) is
// subtracted: shift = Y - X.
func npHL(s *Session, v *Var, classVar *Var, classes []Value, x []float64, cls []int, alpha float64, exactHL bool) {
	ni := [2]int{}
	for _, c := range cls {
		ni[c]++
	}
	// reference class X: the larger sample; if equal, the second class
	xi := 1
	if ni[0] > ni[1] {
		xi = 0
	}
	yi := 1 - xi
	var X, Y []float64
	for i, c := range cls {
		if c == xi {
			X = append(X, x[i])
		} else {
			Y = append(Y, x[i])
		}
	}
	n1, n2 := len(Y), len(X)
	m := n1 * n2
	diffs := make([]float64, 0, m)
	for _, y := range Y {
		for _, xx := range X {
			diffs = append(diffs, y-xx)
		}
	}
	sort.Float64s(diffs)
	est := diffs[m/2]
	if m%2 == 0 {
		est = (diffs[m/2-1] + diffs[m/2]) / 2
	}
	z := NormInv(1 - alpha/2)
	// asymptotic (Moses) limits from the tie-corrected Wilcoxon variance
	pooled := make([]float64, 0, n1+n2)
	pooled = append(pooled, X...)
	pooled = append(pooled, Y...)
	tieF := npTieFactor(Ranks(pooled))
	E := float64(n1*n2) / 2
	varr := (float64(n1*n2) / 12) * (float64(n1+n2) + 1) * tieF
	C := int(math.Floor(E - z*math.Sqrt(varr)))
	if C < 1 {
		C = 1
	}
	if C > m/2 {
		C = m / 2
	}
	L, U := diffs[C-1], diffs[m-C]
	t := &Table{ODSName: "HodgesLehmann", Caption: "Hodges-Lehmann Estimation", MissDot: true}
	t.Cols = []Col{
		{Name: "Type", Label: "Type", Char: true},
		{Name: "LowerCL", Label: fmt.Sprintf("%.0f%% Confidence\nLimits", 100*(1-alpha)), Format: "8.4"},
		{Name: "UpperCL", Label: "", Format: "8.4"},
		{Name: "Midpoint", Label: "Interval\nMidpoint", Format: "8.4"},
		{Name: "ASE", Label: "Asymptotic\nStandard Error", Format: "8.4"},
	}
	t.RowHeader = 1
	t.AddRow(fmt.Sprintf("Location Shift (%d - %d)", yi+1, xi+1), est, Missing, Missing, Missing)
	t.AddRow("Asymptotic (Moses)", L, U, (L+U)/2, (U-L)/(2*z))
	if exactHL {
		// exact conditional distribution of the Mann-Whitney statistic
		N := n1 + n2
		all := make([]float64, N)
		copy(all[:n2], X)
		copy(all[n2:], Y)
		nComb := 1
		for i := 0; i < n1; i++ {
			nComb = nComb * (N - i) / (i + 1)
		}
		if nComb <= 5000000 {
			dist := map[float64]float64{}
			idx := make([]int, n1)
			for i := range idx {
				idx[i] = i
			}
			for {
				M := 0.0
				in := make([]bool, N)
				for _, i := range idx {
					in[i] = true
				}
				for _, a := range idx {
					for b := 0; b < N; b++ {
						if in[b] {
							continue
						}
						if all[a] > all[b] {
							M++
						} else if all[a] == all[b] {
							M += 0.5
						}
					}
				}
				dist[M]++
				i := n1 - 1
				for i >= 0 && idx[i] == N-n1+i {
					i--
				}
				if i < 0 {
					break
				}
				idx[i]++
				for j := i + 1; j < n1; j++ {
					idx[j] = idx[j-1] + 1
				}
			}
			keys := make([]float64, 0, len(dist))
			for k := range dist {
				keys = append(keys, k)
			}
			sort.Float64s(keys)
			tot := 0.0
			for _, k := range keys {
				tot += dist[k]
			}
			cum := 0.0
			cL := keys[0]
			for _, k := range keys {
				cum += dist[k] / tot
				if cum <= alpha/2+1e-12 {
					cL = k
				}
			}
			Cl := int(math.Ceil(cL - 1e-9))
			if Cl < 1 {
				Cl = 1
			}
			eL, eU := diffs[Cl], diffs[m-Cl-1]
			t.AddRow("Exact", eL, eU, (eL+eU)/2, Missing)
		}
	}
	s.Out.AddTable(t)
}

// npTieFactor returns 1 - sum(t^3-t)/(N^3-N).
func npTieFactor(ranks []float64) float64 {
	N := float64(len(ranks))
	cnt := map[float64]float64{}
	for _, r := range ranks {
		cnt[r]++
	}
	ties := 0.0
	for _, c := range cnt {
		if c > 1 {
			ties += c*c*c - c
		}
	}
	return 1 - ties/(N*N*N-N)
}

// npVarScore is the variance of one group's Wilcoxon score sum under H0:
// (n1*n2/12) * (N+1) * (1 - sum(t^3-t)/(N^3-N)).
func npVarScore(n1, N, tieFactor float64) float64 {
	n2 := N - n1
	return (n1 * n2 / 12) * (N + 1) * tieFactor
}

func npWilcoxonTest(s *Session, v *Var, classVar *Var, classes []Value, nip, sums, sd []float64, N float64, exact bool, x []float64, cls []int) {
	i, j := 0, 1
	if nip[1] < nip[0] || (nip[1] == nip[0] && Compare(classes[1], classes[0]) < 0) {
		i, j = 1, 0
	}
	_ = j
	si := sums[i]
	ei := nip[i] * (N + 1) / 2
	sdi := sd[i]
	if sdi == 0 {
		return
	}
	zc := (math.Abs(si-ei) - 0.5) / sdi
	zt := (si - ei) / sdi
	one := NormSF(zc)
	two := 2 * NormSF(zc)
	oneT := TSF(zt, N-1)
	twoT := TTwoSided(zt, N-1)
	t := &Table{
		ODSName: "WilcoxonTest",
		Caption: fmt.Sprintf("Wilcoxon Two-Sample Test\nStatistic (S) for Variable %s", v.Name),
		MissDot: true,
	}
	t.Cols = []Col{{Name: "Statistic", Label: "Statistic", Char: true}, {Name: "Value", Label: "Value", Format: "12.4"}}
	t.RowHeader = 1
	t.AddRow("Statistic (S)", si)
	t.AddRow("Normal Approximation Z", zc)
	t.AddRow("One-Sided Pr > Z", one)
	t.AddRow("Two-Sided Pr > |Z|", two)
	t.AddRow("t Approximation", zt)
	t.AddRow("One-Sided Pr > Z (t)", oneT)
	t.AddRow("Two-Sided Pr > |Z| (t)", twoT)
	if exact {
		pe := npExactWilcoxon(x, cls, i)
		if !math.IsNaN(pe) {
			t.AddRow("Exact Test One-Sided Pr > S", pe)
			t.AddRow("Exact Test Two-Sided Pr > |S|", math.Min(1, 2*pe))
		}
	}
	s.Out.AddTable(t)
}

// npExactWilcoxon computes P(S >= obs) for the rank sum of group g by
// enumerating subset sums (integer ranks doubled for mid-rank ties).
func npExactWilcoxon(x []float64, cls []int, g int) float64 {
	n := len(x)
	if n > 34 {
		return math.NaN()
	}
	n1 := 0
	rs := make([]int, n)
	for i, r := range Ranks(x) {
		rs[i] = int(math.Round(r * 2))
		if cls[i] == g {
			n1++
		}
	}
	target := 0
	for i := range cls {
		if cls[i] == g {
			target += rs[i]
		}
	}
	maxSum := 0
	for _, r := range rs {
		maxSum += r
	}
	// dp[k][s] = number of ways to pick k values from processed rows summing to s
	dp := make([][]float64, n1+1)
	for i := range dp {
		dp[i] = make([]float64, maxSum+1)
	}
	dp[0][0] = 1
	for i := range n {
		for k := min(n1, i+1); k >= 1; k-- {
			for s := maxSum; s >= rs[i]; s-- {
				dp[k][s] += dp[k-1][s-rs[i]]
			}
		}
	}
	ge := 0.0
	for s := target; s <= maxSum; s++ {
		ge += dp[n1][s]
	}
	den := 1.0
	for i := 0; i < n1; i++ {
		den /= float64(i + 1)
	}
	for i := n1; i < n; i++ {
		den /= float64(i - n1 + 1)
	}
	p := ge * den
	if p < 0 {
		p = 0
	}
	if p > 1 {
		p = 1
	}
	return p
}

// npKWTest computes the Kruskal-Wallis chi-square with tie correction.
func npKWTest(s *Session, v *Var, classes []Value, nip []float64, sums []float64, N float64, tieFactor float64) {
	h := 12 / (N * (N + 1))
	t := 0.0
	for i := range sums {
		t += sums[i] * sums[i] / nip[i]
	}
	h *= t
	h -= 3 * (N + 1)
	h /= tieFactor
	df := float64(len(classes)) - 1
	tt := &Table{ODSName: "KruskalWallisTest", Caption: fmt.Sprintf("Kruskal-Wallis Test\nChi-Square Approximation for Variable %s", v.Name), MissDot: true}
	tt.Cols = []Col{{Name: "ChiSquare", Label: "Chi-Square", Format: "10.4"}, {Name: "DF", Label: "DF", Format: "3."}, {Name: "Prob", Label: "Pr > ChiSq", Format: "PVALUE6.4"}}
	tt.AddRow(h, df, ChiSqSF(h, df))
	s.Out.AddTable(tt)
}

// npMedianTest: scores = 1 above median, 0.5 at median, 0 below.
func npMedianTest(s *Session, v *Var, classVar *Var, classes []Value, x []float64, cls []int, nip []float64) {
	k := len(classes)
	med := Percentile(Sorted(x), 50)
	scores := make([]float64, len(x))
	for i, val := range x {
		switch {
		case val > med:
			scores[i] = 1
		case val == med:
			scores[i] = 0.5
		}
	}
	m := Sum(scores)
	N := float64(len(x))
	sums := make([]float64, k)
	for i, sc := range scores {
		sums[cls[i]] += sc
	}
	t := &Table{
		ODSName: "MedianScores",
		Caption: fmt.Sprintf("Median Scores (Number of Values Above the Median) for Variable %s\nClassified by Variable %s", v.Name, classVar.Name),
		MissDot: true,
	}
	t.Cols = []Col{
		{Name: "Class", Label: classVar.Name, Char: true},
		{Name: "N", Label: "N", Format: "6."},
		{Name: "SumOfScores", Label: "Sum of\nScores", Format: "12.4"},
		{Name: "ExpectedUnderH0", Label: "Expected\nUnder H0", Format: "12.4"},
		{Name: "StdDevUnderH0", Label: "Std Dev\nUnder H0", Format: "10.4"},
		{Name: "MeanScore", Label: "Median\nScore", Format: "10.4"},
	}
	t.RowHeader = 1
	chi2 := 0.0
	for i := range k {
		e := nip[i] * m / N
		varr := nip[i] * (N - nip[i]) * m * (N - m) / (N * N * (N - 1))
		sd := math.Sqrt(varr)
		d := sums[i] - e
		if varr > 0 {
			chi2 += d * d / varr
		}
		t.AddRow(classes[i], nip[i], sums[i], e, sd, sums[i]/nip[i])
	}
	s.Out.AddTable(t)
	df := float64(k - 1)
	tt := &Table{ODSName: "MedianTest", Caption: fmt.Sprintf("Median One-Way Analysis\nChi-Square Test for Variable %s", v.Name), MissDot: true}
	tt.Cols = []Col{{Name: "Statistic", Label: "Chi-Square", Char: true}, {Name: "DF", Label: "DF", Format: "3."}, {Name: "Value", Label: "Value", Format: "8.4"}, {Name: "Prob", Label: "Pr > ChiSq", Format: "PVALUE6.4"}}
	tt.AddRow("Chi-Square", df, chi2, ChiSqSF(chi2, df))
	s.Out.AddTable(tt)
}

// npAnova: raw values, one-way F test.
func npAnova(s *Session, v *Var, classVar *Var, classes []Value, x []float64, cls []int, nip []float64) {
	k := len(classes)
	N := float64(len(x))
	t := &Table{
		ODSName: "ANOVAScores",
		Caption: fmt.Sprintf("ANOVA Scores (Raw Data Values) for Variable %s\nClassified by Variable %s", v.Name, classVar.Name),
		MissDot: true,
	}
	t.Cols = []Col{
		{Name: "Class", Label: classVar.Name, Char: true},
		{Name: "N", Label: "N", Format: "6."},
		{Name: "SumOfScores", Label: "Sum of\nScores", Format: "12.4"},
		{Name: "MeanScore", Label: "Mean\nScore", Format: "10.4"},
		{Name: "UnderH0MeanScore", Label: "Under H0\nMean Score", Format: "10.4"},
		{Name: "UnderH0StdDev", Label: "Under H0\nStd Deviation", Format: "10.4"},
	}
	t.RowHeader = 1
	meanAll := Mean(x)
	sdAll := math.Sqrt(CSS(x) / (N - 1))
	for i := range k {
		var s float64
		for j := range x {
			if cls[j] == i {
				s += x[j]
			}
		}
		t.AddRow(classes[i], nip[i], s, s/nip[i], meanAll, sdAll)
	}
	s.Out.AddTable(t)
	gm := Mean(x)
	ssb, ssw := 0.0, 0.0
	for i := range k {
		var s float64
		for j := range x {
			if cls[j] == i {
				s += x[j]
			}
		}
		ssb += nip[i] * (s/nip[i] - gm) * (s/nip[i] - gm)
	}
	ssw = CSS(x) - ssb
	dfb, dfw := float64(k)-1, N-float64(k)
	f := (ssb / dfb) / (ssw / dfw)
	t2 := &Table{ODSName: "ANOVATest", Caption: fmt.Sprintf("Analysis of Variance for Variable %s\nClassified by Variable %s", v.Name, classVar.Name), MissDot: true}
	t2.Cols = []Col{{Name: "Source", Label: "Source", Char: true}, {Name: "DF", Label: "DF", Format: "4."}, {Name: "SumOfSquares", Label: "Sum of\nSquares", Format: "14.4"}, {Name: "MeanSquare", Label: "Mean\nSquare", Format: "14.4"}, {Name: "FValue", Label: "F Value", Format: "8.4"}, {Name: "ProbF", Label: "Pr > F", Format: "PVALUE6.4"}}
	t2.AddRow("Between Groups", dfb, ssb, ssb/dfb, f, FSF(f, dfb, dfw))
	t2.AddRow("Within Groups", dfw, ssw, ssw/dfw, Missing, Missing)
	s.Out.AddTable(t2)
}

// npScaleScores returns the per-observation scores for the named linear
// rank test: MOOD, KLOTZ, AB (Ansari-Bradley), SAVAGE, ST (Siegel-Tukey)
// or CONOVER. SAVAGE and ST score the sorted positions and average the
// scores of tied observations; CONOVER squares the midranks of the
// absolute deviations from the class means.
func npScaleScores(name string, ranks []float64, x []float64, cls []int, k int) []float64 {
	n := len(ranks)
	s := make([]float64, n)
	switch name {
	case "MOOD":
		for i, r := range ranks {
			s[i] = (r - (float64(n)+1)/2) * (r - (float64(n)+1)/2)
		}
	case "KLOTZ":
		for i, r := range ranks {
			z := NormInv(r / float64(n+1))
			s[i] = z * z
		}
	case "AB":
		for i, r := range ranks {
			s[i] = (float64(n)+1)/2 - math.Abs(r-(float64(n)+1)/2)
		}
	case "SAVAGE", "ST":
		order := make([]int, n)
		for i := range order {
			order[i] = i
		}
		sort.Slice(order, func(a, b int) bool { return x[order[a]] < x[order[b]] })
		pat := make([]float64, n)
		if name == "SAVAGE" {
			H := make([]float64, n+1)
			for t := 1; t <= n; t++ {
				H[t] = H[t-1] + 1/float64(t)
			}
			// a(p) = sum_{j=n-p+1}^{n} 1/j - 1 for position p
			for t := 1; t <= n; t++ {
				pat[t-1] = H[n] - H[n-t] - 1
			}
		} else {
			// Siegel-Tukey: scores increase toward the middle in the
			// order a(1)=1, a(n)=2, a(n-1)=3, a(2)=4, a(3)=5, a(n-2)=6,...
			sc := 1.0
			assigned := 0
			for t := 0; assigned < n; t++ {
				for _, idx := range [4]int{2 * t, n - 1 - 2*t, n - 2 - 2*t, 2*t + 1} {
					if idx >= 0 && idx < n && pat[idx] == 0 {
						pat[idx] = sc
						sc++
						assigned++
					}
				}
			}
		}
		// average the scores of tied observations (consecutive sorted
		// slots t; pat is indexed by sorted slot, not observation)
		copy := make([]float64, n)
		for i := 0; i < n; {
			j := i
			for j < n && x[order[j]] == x[order[i]] {
				j++
			}
			sum := 0.0
			for t := i; t < j; t++ {
				sum += pat[t]
			}
			for t := i; t < j; t++ {
				copy[order[t]] = sum / float64(j-i)
			}
			i = j
		}
		return copy
	case "CONOVER":
		means := make([]float64, k)
		cnt := make([]float64, k)
		for i := range x {
			means[cls[i]] += x[i]
			cnt[cls[i]]++
		}
		for i := range means {
			if cnt[i] > 0 {
				means[i] /= cnt[i]
			}
		}
		dev := make([]float64, n)
		for i := range x {
			dev[i] = math.Abs(x[i] - means[cls[i]])
		}
		for i, r := range Ranks(dev) {
			s[i] = r * r
		}
	}
	return s
}

// npPStr renders a p-value as SAS NPAR1WAY does in the test tables:
// four decimals with leading zero, floored at <.0001.
func npPStr(p float64) Value {
	if p < 0.00005 {
		return StrV("<.0001")
	}
	return StrV(fmt.Sprintf("%.4f", p))
}

// npScale: generic linear rank test with the given scores (MOOD, KLOTZ,
// AB, SAVAGE, ST, CONOVER). Renders the scores table, the one-way ANOVA
// chi-square analysis and, for two samples, the standardized Z test.
// Siegel-Tukey uses a continuity correction by default (SAS CORRECT=NO
// suppresses it); the other score types do not.
func npScale(s *Session, name, odsPrefix, title string, v *Var, classVar *Var, classes []Value, x []float64, cls []int, ranks []float64, nip []float64) {
	k := len(classes)
	n := len(x)
	scores := npScaleScores(name, ranks, x, cls, k)
	sums := make([]float64, k)
	for i := range scores {
		sums[cls[i]] += scores[i]
	}
	t := &Table{
		ODSName: odsPrefix + "Scores",
		Caption: fmt.Sprintf("%s Scores for Variable %s\nClassified by Variable %s", title, v.Name, classVar.Name),
		MissDot: true,
	}
	if name == "SAVAGE" {
		t.Caption = fmt.Sprintf("%s Scores (Exponential) for Variable %s\nClassified by Variable %s", title, v.Name, classVar.Name)
	}
	t.Cols = []Col{
		{Name: "Class", Label: classVar.Name, Char: true},
		{Name: "N", Label: "N", Format: "6."},
		{Name: "SumOfScores", Label: "Sum of\nScores", Format: "12.4"},
		{Name: "ExpectedUnderH0", Label: "Expected\nUnder H0", Format: "12.4"},
		{Name: "StdDevUnderH0", Label: "Std Dev\nUnder H0", Format: "10.4"},
		{Name: "MeanScore", Label: "Mean\nScore", Format: "10.4"},
	}
	t.RowHeader = 1
	N := float64(n)
	meanScore := Mean(scores)
	// General linear rank statistic variance under H0 (tie-corrected):
	// V_g = n_g(N-n_g)/(N(N-1)) * sum (s_i - sbar)^2.
	css := 0.0
	for _, sc := range scores {
		css += (sc - meanScore) * (sc - meanScore)
	}
	for i := range k {
		e := nip[i] * meanScore
		vg := css / (N - 1) * (nip[i] * (N - nip[i]) / N)
		t.AddRow(classes[i], nip[i], sums[i], e, math.Sqrt(vg), sums[i]/nip[i])
	}
	s.Out.AddTable(t)
	// one-way ANOVA statistic: chi-square with k-1 df
	Q := (N - 1) / css * func() float64 {
		q := 0.0
		for i := range k {
			q += sums[i] * sums[i] / nip[i]
		}
		return q
	}()
	ta := &Table{ODSName: odsPrefix + "Analysis", Caption: fmt.Sprintf("%s One-Way Analysis", title), MissDot: true}
	ta.Cols = []Col{{Name: "Label", Label: "", Char: true}, {Name: "Value", Label: "", Format: "12.4"}}
	ta.RowHeader = 1
	ta.AddRow("Chi-Square", Q)
	ta.AddRow("DF", float64(k-1))
	ta.AddRow("Pr > Chi-Square", npPStr(ChiSqSF(Q, float64(k-1))))
	s.Out.AddTable(ta)
	if k != 2 {
		return
	}
	var i int
	if nip[1] < nip[0] || (nip[1] == nip[0] && Compare(classes[1], classes[0]) < 0) {
		i = 1
	}
	si := sums[i]
	e := nip[i] * meanScore
	vr := css / (N - 1) * (nip[i] * (N - nip[i]) / N)
	if vr == 0 {
		return
	}
	z := (si - e) / math.Sqrt(vr)
	if name == "ST" {
		// continuity correction toward zero
		if z > 0 {
			z = (si - e - 0.5) / math.Sqrt(vr)
		} else {
			z = (si - e + 0.5) / math.Sqrt(vr)
		}
	}
	t2 := &Table{ODSName: odsPrefix + "Test", Caption: fmt.Sprintf("%s Two-Sample Test", title), MissDot: true}
	t2.Cols = []Col{{Name: "Label", Label: "", Char: true}, {Name: "Value", Label: "", Format: "12.4"}}
	t2.RowHeader = 1
	t2.AddRow("Statistic (S)", si)
	t2.AddRow("Z", z)
	if z < 0 {
		t2.AddRow("One-Sided Pr < Z", npPStr(NormCDF(z)))
	} else {
		t2.AddRow("One-Sided Pr > Z", npPStr(NormSF(z)))
	}
	t2.AddRow("Two-Sided Pr > |Z|", npPStr(2*NormSF(math.Abs(z))))
	s.Out.AddTable(t2)
}

// npVW: van der Waerden (normal) scores.
func npVW(s *Session, v *Var, classVar *Var, classes []Value, x []float64, cls []int, ranks []float64, nip []float64) {
	k := len(classes)
	n := len(x)
	vw := make([]float64, n)
	for i := range vw {
		vw[i] = NormInv(ranks[i] / float64(n+1))
	}
	sums := make([]float64, k)
	for i := range vw {
		sums[cls[i]] += vw[i]
	}
	tieF := npTieFactor(vw)
	t := &Table{
		ODSName: "VWScores",
		Caption: fmt.Sprintf("Van der Waerden Scores (Normal Scores) for Variable %s\nClassified by Variable %s", v.Name, classVar.Name),
		MissDot: true,
	}
	t.Cols = []Col{
		{Name: "Class", Label: classVar.Name, Char: true},
		{Name: "N", Label: "N", Format: "6."},
		{Name: "SumOfScores", Label: "Sum of\nScores", Format: "12.4"},
		{Name: "ExpectedUnderH0", Label: "Expected\nUnder H0", Format: "12.4"},
		{Name: "StdDevUnderH0", Label: "Std Dev\nUnder H0", Format: "10.4"},
	}
	t.RowHeader = 1
	N := float64(n)
	for i := range k {
		e := 0.0
		t.AddRow(classes[i], nip[i], sums[i], e, math.Sqrt(npVarScore(nip[i], N, tieF)))
	}
	s.Out.AddTable(t)
	if k == 2 {
		varr := npVarScore(nip[0], N, tieF)
		z := sums[0] / math.Sqrt(varr)
		t2 := &Table{ODSName: "VWTest", Caption: fmt.Sprintf("Van der Waerden Two-Sample Test\nStatistic (S) = Z for Variable %s", v.Name), MissDot: true}
		t2.Cols = []Col{{Name: "Statistic", Label: "Statistic", Char: true}, {Name: "Z", Label: "Z", Format: "8.4"}, {Name: "OneSidedPr", Label: "One-Sided Pr > Z", Format: "PVALUE6.4"}, {Name: "TwoSidedPr", Label: "Two-Sided Pr > |Z|", Format: "PVALUE6.4"}}
		t2.AddRow("Z", z, NormSF(z), 2*NormSF(math.Abs(z)))
		s.Out.AddTable(t2)
	}
}

// npKSAsymP returns Pr(D > d) for the two-sample Kolmogorov-Smirnov test
// computed by SAS from the asymptotic series (Hodges 1957)
//
//	2 sum_{i>=1} (-1)^(i-1) exp(-2 i^2 z^2),  z = d sqrt(n1 n2 / n).
func npKSAsymP(z float64) float64 {
	if z <= 0 {
		return 1
	}
	sum := 0.0
	for i := range 100 {
		t := math.Exp(-2 * float64((i+1)*(i+1)) * z * z)
		if i%2 == 0 {
			sum += t
		} else {
			sum -= t
		}
		if t < 1e-17 {
			break
		}
	}
	p := 2 * sum
	if p > 1 {
		p = 1
	}
	if p < 0 {
		p = 0
	}
	return p
}

// npKuiperP returns the asymptotic p-value for the scaled Kuiper statistic
// (Owen 1962): 2 sum_{k>=1} (4 k^2 v^2 - 1) exp(-2 k^2 v^2).
func npKuiperP(v float64) float64 {
	if v <= 0.3 {
		return 1
	}
	sum := 0.0
	for k := range 200 {
		kv := float64((k+1)*(k+1)) * v * v
		t := (4*kv - 1) * math.Exp(-2*kv)
		sum += t
		if math.Abs(t) < 1e-17 {
			break
		}
	}
	p := 2 * sum
	if p > 1 {
		p = 1
	}
	if p < 0 {
		p = 1
	}
	return p
}

// npEDF computes the empirical distribution function tests (SAS EDF option):
// the k-sample Kolmogorov-Smirnov and Cramer-von Mises statistics, and for
// two-sample data the Smirnov D, the one-sided D+/D- (when withD) and the
// Kuiper test. Layout and formulas follow the SAS/STAT NPAR1WAY
// documentation ("Tests Based on the Empirical Distribution Function").
func npEDF(s *Session, v *Var, classVar *Var, classes []Value, x []float64, cls []int, rowno []int, nip []float64, withD bool) {
	k := len(classes)
	n := len(x)
	N := float64(n)
	// pooled distinct values and tie counts
	sx := Sorted(x)
	var vals []float64
	var ties []float64
	for i, val := range sx {
		if i > 0 && val == sx[i-1] {
			ties[len(ties)-1]++
		} else {
			vals = append(vals, val)
			ties = append(ties, 1)
		}
	}
	m := len(vals)
	// class EDFs F_i and pooled EDF F at each distinct value
	fi := make([][]float64, k)
	fpool := make([]float64, m)
	for i := range k {
		fi[i] = make([]float64, m)
		// sorted class-i values, merged against the pooled grid
		si := make([]float64, 0, int(nip[i]))
		for idx := range n {
			if cls[idx] == i {
				si = append(si, x[idx])
			}
		}
		si = Sorted(si)
		cum := 0
		for j := range m {
			for cum < len(si) && si[cum] <= vals[j] {
				cum++
			}
			fi[i][j] = float64(cum) / nip[i]
			fpool[j] += nip[i] * fi[i][j]
		}
	}
	for j := range fpool {
		fpool[j] /= N
	}
	// point of maximum pooled deviation: argmax_j sum_i n_i (F_i - F)^2
	maxSum := math.Inf(-1)
	jmax := 0
	for j := range m {
		sum := 0.0
		for i := range k {
			d := fi[i][j] - fpool[j]
			sum += nip[i] * d * d
		}
		if sum > maxSum {
			maxSum, jmax = sum, j
		}
	}
	ks := math.Sqrt(maxSum / N)
	ksa := math.Sqrt(N) * ks

	stats := &Table{
		ODSName: "KSStats",
		Caption: fmt.Sprintf("Kolmogorov-Smirnov Test for Variable %s\nClassified by Variable %s", v.Name, classVar.Name),
	}
	stats.Cols = []Col{
		{Name: "Class", Label: classVar.Name, Char: classVar.Char},
		{Name: "N", Label: "N", Format: "6."},
		{Name: "EDFAtMaximum", Label: "EDF at\nMaximum", Format: "10.6"},
		{Name: "DevFromMeanAtMax", Label: "Deviation from Mean\nat Maximum", Format: "10.6"},
	}
	stats.RowHeader = 1
	for i := range k {
		dev := math.Sqrt(nip[i]) * (fi[i][jmax] - fpool[jmax])
		stats.AddRow(classes[i], nip[i], fi[i][jmax], dev)
	}
	stats.AddRow("Total", n, fpool[jmax], Missing)
	// first source observation attaining the maximum-deviation value
	obs := 0
	for idx := range n {
		if x[idx] == vals[jmax] && (obs == 0 || rowno[idx] < obs) {
			obs = rowno[idx]
		}
	}
	stats.Notes = []string{
		fmt.Sprintf("Maximum Deviation Occurred at Observation %d", obs),
		fmt.Sprintf("Value of %s at Maximum = %g", v.Name, vals[jmax]),
	}
	s.Out.AddTable(stats)

	if k == 2 {
		n1, n2 := nip[0], nip[1]
		dplus, dminus := math.Inf(-1), math.Inf(-1)
		for j := range m {
			d := fi[0][j] - fi[1][j]
			dplus = math.Max(dplus, d)
			dminus = math.Max(dminus, -d)
		}
		D := math.Max(dplus, dminus)
		kt := &Table{
			ODSName: "KSTest",
			Caption: "Kolmogorov-Smirnov Two-Sample\nTest (Asymptotic)",
		}
		kt.Cols = []Col{
			{Name: "Label1", Label: "", Char: true},
			{Name: "Value1", Label: "", Format: "10.6"},
			{Name: "Label2", Label: "", Char: true},
			{Name: "Value2", Label: "", Format: "PVALUE6.4"},
		}
		scale := math.Sqrt(n1 * n2 / N)
		kt.AddRow("KS", ks, "Pr > KSa", npKSAsymP(ksa))
		kt.AddRow("KSa", ksa, "", Missing)
		kt.AddRow("D", D, "", Missing)
		if withD {
			kt.AddRow("D+", dplus, "Pr > D+", npKSAsymP(dplus*scale))
			kt.AddRow("D-", dminus, "Pr > D-", npKSAsymP(dminus*scale))
		}
		s.Out.AddTable(kt)
	} else {
		kt := &Table{
			ODSName: "KSTest",
			Caption: "Kolmogorov-Smirnov\nStatistics (Asymptotic)",
		}
		kt.Cols = []Col{
			{Name: "Label1", Label: "", Char: true},
			{Name: "Value1", Label: "", Format: "10.6"},
			{Name: "Label2", Label: "", Char: true},
			{Name: "Value2", Label: "", Format: "10.6"},
		}
		kt.AddRow("KS", ks, "KSa", ksa)
		s.Out.AddTable(kt)
	}

	// Cramer-von Mises: CMa = sum_i (n_i/n) sum_j t_j (F_i - F)^2
	cvm := &Table{
		ODSName: "CVMStats",
		Caption: fmt.Sprintf("Cramer-von Mises Test for Variable %s\nClassified by Variable %s", v.Name, classVar.Name),
	}
	cvm.Cols = []Col{
		{Name: "Class", Label: classVar.Name, Char: classVar.Char},
		{Name: "N", Label: "N", Format: "6."},
		{Name: "SummedDeviation", Label: "Summed Deviation\nfrom Mean", Format: "10.6"},
	}
	cvm.RowHeader = 1
	cma := 0.0
	for i := range k {
		sum := 0.0
		for j := range m {
			d := fi[i][j] - fpool[j]
			sum += ties[j] * d * d
		}
		contrib := nip[i] / N * sum
		cma += contrib
		cvm.AddRow(classes[i], nip[i], contrib)
	}
	s.Out.AddTable(cvm)
	ct := &Table{ODSName: "CVMTest", Caption: "Cramer-von Mises Statistics\n(Asymptotic)"}
	ct.Cols = []Col{
		{Name: "Label1", Label: "", Char: true},
		{Name: "Value1", Label: "", Format: "10.6"},
		{Name: "Label2", Label: "", Char: true},
		{Name: "Value2", Label: "", Format: "10.6"},
	}
	ct.AddRow("CM", cma/N, "CMa", cma)
	s.Out.AddTable(ct)

	// Kuiper test (two-sample only)
	if k == 2 {
		ku := &Table{
			ODSName: "KuiperStats",
			Caption: fmt.Sprintf("Kuiper Test for Variable %s\nClassified by Variable %s", v.Name, classVar.Name),
		}
		ku.Cols = []Col{
			{Name: "Class", Label: classVar.Name, Char: classVar.Char},
			{Name: "N", Label: "N", Format: "6."},
			{Name: "DevFromMean", Label: "Deviation\nfrom Mean", Format: "10.6"},
		}
		ku.RowHeader = 1
		d1, d2 := math.Inf(-1), math.Inf(-1)
		for j := range m {
			d := fi[0][j] - fi[1][j]
			d1 = math.Max(d1, d)
			d2 = math.Max(d2, -d)
		}
		ku.AddRow(classes[0], nip[0], math.Abs(d1))
		ku.AddRow(classes[1], nip[1], math.Abs(d2))
		s.Out.AddTable(ku)
		scale := math.Sqrt(nip[0] * nip[1] / N)
		ka := (d1 + d2) * scale
		kut := &Table{ODSName: "KuiperTest", Caption: "Kuiper Two-Sample Test\n(Asymptotic)"}
		kut.Cols = []Col{
			{Name: "Label1", Label: "", Char: true},
			{Name: "Value1", Label: "", Format: "10.6"},
			{Name: "Label2", Label: "", Char: true},
			{Name: "Value2", Label: "", Format: "PVALUE6.4"},
		}
		kut.AddRow("K", d1+d2, "Pr > Ka", npKuiperP(ka))
		kut.AddRow("Ka", ka, "", Missing)
		s.Out.AddTable(kut)
	}
}
