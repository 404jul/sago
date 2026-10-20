package sas

import (
	"math"
	"testing"
)

// NPAR1WAY pinned against scipy.stats (mannwhitneyu, kruskal).

func npSetup(t *testing.T, groups map[string][]float64, procs string, capture ...string) (*Session, []*Stmt) {
	t.Helper()
	d := NewDataset("t")
	d.Vars = []*Var{{Name: "g", Char: true, Length: 1}, {Name: "y"}}
	names := make([]string, 0, len(groups))
	for g := range groups {
		names = append(names, g)
	}
	SortIdxByValueStr(names)
	for _, g := range names {
		for _, x := range groups[g] {
			d.Rows = append(d.Rows, []Value{StrV(g), NumV(x)})
		}
	}
	s := NewSession(".")
	s.SetDataset("t", d)
	s.Out.BeginProc("")
	s.Out.SelectNone = true
	for _, c := range capture {
		s.Out.ODSOutputs[c] = "_" + c + "_"
	}
	toks, _ := Tokenize("npar1way data=t "+procs, 1)
	body, _ := Tokenize("class g; var y;", 2)
	return s, []*Stmt{&Stmt{Toks: toks}, &Stmt{Toks: body[:2]}, &Stmt{Toks: body[3:5]}}
}

func SortIdxByValueStr(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

func npRowNums(t *testing.T, d *Dataset) map[string][]float64 {
	t.Helper()
	out := map[string][]float64{}
	if d == nil {
		t.Fatal("dataset not captured")
	}
	for _, r := range d.Rows {
		key := r[0].Str
		if len(r) > 3 {
			key = r[3].Str
		}
		if len(r) > 4 && !r[4].IsMissing() {
			out[key] = append(out[key], r[4].Num)
			continue
		}
		out[key] = append(out[key], r[1].Num)
	}
	return out
}

func TestNpWilcoxonTwoSample(t *testing.T) {
	// scipy: mannwhitneyu(A, B, alternative='two-sided', method='asymptotic')
	gA := []float64{6, 8, 7, 9, 10, 6, 8, 7, 9, 8}
	gB := []float64{11, 13, 10, 12, 14, 11, 13, 12, 15, 13}
	s, stmts := npSetup(t, map[string][]float64{"A": gA, "B": gB}, "wilcoxon", "WILCOXONTEST", "WILCOXONSCORES")
	body := []*Stmt{stmts[1], stmts[2]}
	p := &ProcStep{Name: "NPAR1WAY", Opts: &Opts{}, Body: body}
	if err := npNpar1way(s, p); err != nil {
		t.Fatal(err)
	}
	s.Out.EndProc()
	wt := s.Dataset("_WILCOXONTEST_")
	// ODS layout: Variable, Statistic, Z, Prob1, tProb1 (single row);
	// Z is the signed continuity-corrected statistic.
	iZ := wt.Index("Z")
	got := wt.Rows[0][wt.Index("Statistic")].Num
	// scipy mannwhitneyu: U=0.5 => S = U + n1(n1+1)/2 = 55.5 (mid-rank ties)
	if math.Abs(got-55.5) > 1e-9 {
		t.Errorf("S = %v, want 55.5", got)
	}
	// Z = signed (|S-E|-0.5)/sd with tie correction; S < E here
	wantZ := -3.7237021157035413
	if math.Abs(wt.Rows[0][iZ].Num-wantZ) > 1e-9 {
		t.Errorf("Z = %.12f, want %.12f", wt.Rows[0][iZ].Num, wantZ)
	}
	ties := 0.0
	all := append(append([]float64{}, gA...), gB...)
	cnt := map[float64]int{}
	for _, v := range all {
		cnt[v]++
	}
	for _, c := range cnt {
		ties += float64(c*c*c - c)
	}
	N := float64(len(all))
	sd2 := (10 * 10 / 12.0) * (N + 1) * (1 - ties/(N*N*N-N))
	// the t approximation uses the continuity-corrected statistic, df N-1
	wantT := (math.Abs(55.5-105) - 0.5) / math.Sqrt(sd2)
	iT := wt.Index("tProb1")
	wantTp := TTwoSided(wantT, N-1) / 2
	if math.Abs(wt.Rows[0][iT].Num-wantTp) > 1e-9 {
		t.Errorf("t one-sided p = %.12g, want %.12g", wt.Rows[0][iT].Num, wantTp)
	}
}

func TestNpKruskal(t *testing.T) {
	// scipy.stats.kruskal on three groups
	a := []float64{22, 25, 27, 24, 25, 23}
	b := []float64{31, 33, 30, 35, 32, 34}
	c := []float64{41, 39, 43, 40, 44, 42}
	s, stmts := npSetup(t, map[string][]float64{"a": a, "b": b, "c": c}, "wilcoxon", "KRUSKALWALLISTEST")
	body := []*Stmt{stmts[1], stmts[2]}
	p := &ProcStep{Name: "NPAR1WAY", Opts: &Opts{}, Body: body}
	if err := npNpar1way(s, p); err != nil {
		t.Fatal(err)
	}
	s.Out.EndProc()
	kw := s.Dataset("_KRUSKALWALLISTEST_")
	if kw == nil {
		t.Fatal("no KW capture")
	}
	// scipy.stats.kruskal(...) = 15.173553719008256, p = 0.0005071129188713245
	// ODS layout: Variable, ChiSquare, DF, Prob
	got := kw.Rows[0][3].Num
	if math.Abs(got-15.173553719008256) > 1e-9 {
		t.Errorf("KW H = %.12g, want 15.173553719008256", got)
	}
	if math.Abs(kw.Rows[0][5].Num-0.0005071129188713245) > 1e-13 {
		t.Errorf("KW p = %.14g, want 0.0005071129188713245", kw.Rows[0][5].Num)
	}
}

// TestNpKSEDF pins the EDF (Kolmogorov-Smirnov, Cramer-von Mises, Kuiper)
// tests against SAS/STAT 14.2 PROC NPAR1WAY output for the Gossypol data
// (Halverson & Sherwood 1930): Figure 84.6 (all five dose levels) and
// Figure 84.13 (two-sample subset, doses 0 and 0.04).
func TestNpKSEDF(t *testing.T) {
	gossypol := map[float64][]float64{
		0.00: {228, 229, 218, 216, 224, 208, 235, 229, 233, 219, 224, 220, 232, 200, 208, 232},
		0.04: {186, 229, 220, 208, 228, 198, 222, 273, 216, 198, 213},
		0.07: {179, 193, 183, 180, 143, 204, 114, 188, 178, 134, 208, 196},
		0.10: {130, 87, 135, 116, 118, 165, 151, 59, 126, 64, 78, 94, 150, 160, 122, 110, 178},
		0.13: {154, 130, 130, 118, 118, 104, 112, 134, 98, 100, 104},
	}
	run := func(t *testing.T, doses []float64, capture ...string) *Session {
		t.Helper()
		d := NewDataset("g")
		d.Vars = []*Var{{Name: "Dose"}, {Name: "Gain"}}
		for _, dose := range doses {
			for _, v := range gossypol[dose] {
				d.Rows = append(d.Rows, []Value{NumV(dose), NumV(v)})
			}
		}
		s := NewSession(".")
		s.SetDataset("g", d)
		s.Out.BeginProc("")
		for _, c := range capture {
			s.Out.ODSOutputs[c] = "_" + c + "_"
		}
		body, _ := Tokenize("class dose; var gain;", 2)
		p := &ProcStep{Name: "NPAR1WAY", Opts: &Opts{}, Body: []*Stmt{{Toks: body[:2]}, {Toks: body[3:5]}}}
		if err := npNpar1way(s, p); err != nil {
			t.Fatal(err)
		}
		s.Out.EndProc()
		return s
	}
	check := func(t *testing.T, name string, got, want float64, tol float64) {
		t.Helper()
		if math.IsNaN(got) || math.Abs(got-want) > tol {
			t.Errorf("%s = %v, want %v (tol %g)", name, got, want, tol)
		}
	}
	notes := func(s *Session, ods string) []string {
		t.Helper()
		for _, it := range s.Out.cur.Items {
			if tb, ok := it.(*Table); ok && tb.ODSName == ods {
				return tb.Notes
			}
		}
		t.Fatalf("table %s not found in output items", ods)
		return nil
	}

	all := []float64{0.00, 0.04, 0.07, 0.10, 0.13}
	s := run(t, all, "KSSTATS", "KSTEST", "CVMSTATS", "CVMTEST")
	ks := s.Dataset("_KSSTATS_")
	if ks == nil {
		t.Fatal("no KSSTATS capture")
	}
	F178 := 32.0 / 67 // pooled EDF at the maximum-deviation value 178
	wantEDF := []float64{0, 0, 1.0 / 3, 1, 1}
	wantDev := []float64{
		-math.Sqrt(16) * F178,
		-math.Sqrt(11) * F178,
		math.Sqrt(12) * (1.0/3 - F178),
		math.Sqrt(17) * (1 - F178),
		math.Sqrt(11) * (1 - F178),
	}
	for i := range 5 {
		check(t, "EDF at max", ks.Rows[i][2].Num, wantEDF[i], 1e-12)
		check(t, "dev at max", ks.Rows[i][3].Num, wantDev[i], 1e-6)
	}
	// Figure 84.6: Total EDF 0.477612; obs 36; value 178; KS 0.457928; KSa 3.748300
	check(t, "Total EDF at max", ks.Rows[5][2].Num, F178, 1e-12)
	if n := notes(s, "KSStats"); n[0] != "Maximum Deviation Occurred at Observation 36" || n[1] != "Value of Gain at Maximum = 178" {
		t.Errorf("notes = %v", n)
	}
	// Kiefer sum of squared class deviations at 178
	ssq := 16*F178*F178 + 11*F178*F178 + 12*(1.0/3-F178)*(1.0/3-F178) + 17*(1-F178)*(1-F178) + 11*(1-F178)*(1-F178)
	kt := s.Dataset("_KSTEST_")
	check(t, "KS", kt.Rows[0][1].Num, math.Sqrt(ssq/67), 1e-12)
	check(t, "KS vs figure", kt.Rows[0][1].Num, 0.457928, 5e-6)
	check(t, "KSa", kt.Rows[0][3].Num, math.Sqrt(ssq), 1e-12)
	check(t, "KSa vs figure", kt.Rows[0][3].Num, 3.748300, 5e-6)

	// Cramer-von Mises (Figure 84.6): contributions, CM 0.093508, CMa 6.265003
	cm := s.Dataset("_CVMSTATS_")
	cma := 0.0
	for i, want := range []float64{2.165210, 0.918280, 0.348227, 1.497542, 1.335745} {
		check(t, "CvM contribution", cm.Rows[i][2].Num, want, 5e-6)
		cma += cm.Rows[i][2].Num
	}
	ct := s.Dataset("_CVMTEST_")
	check(t, "CM", ct.Rows[0][1].Num, cma/67, 1e-9)
	check(t, "CMa", ct.Rows[0][3].Num, cma, 1e-9)
	check(t, "CM vs figure", ct.Rows[0][1].Num, 0.093508, 5e-6)
	check(t, "CMa vs figure", ct.Rows[0][3].Num, 6.265003, 5e-6)

	// Two-sample subset (Figure 84.13)
	s2 := run(t, []float64{0.00, 0.04}, "KSSTATS", "KSTEST", "CVMSTATS", "CVMTEST", "KUIPERSTATS", "KUIPERTEST")
	ks2 := s2.Dataset("_KSSTATS_")
	F216 := 10.0 / 27
	check(t, "2-sample EDF at max", ks2.Rows[0][2].Num, 0.25, 1e-12)
	check(t, "2-sample EDF at max", ks2.Rows[1][2].Num, 6.0/11, 1e-12)
	check(t, "2-sample dev", ks2.Rows[0][3].Num, math.Sqrt(16)*(0.25-F216), 1e-6)
	check(t, "2-sample dev", ks2.Rows[1][3].Num, math.Sqrt(11)*(6.0/11-F216), 1e-6)
	check(t, "2-sample Total EDF", ks2.Rows[2][2].Num, F216, 1e-12)
	if n := notes(s2, "KSStats"); n[0] != "Maximum Deviation Occurred at Observation 4" || n[1] != "Value of Gain at Maximum = 216" {
		t.Errorf("2-sample notes = %v", n)
	}
	kt2 := s2.Dataset("_KSTEST_")
	// D = |4/16 - 6/11| = 13/44; KSa = D sqrt(16*11/27); Pr > KSa 0.6199
	D := 13.0 / 44
	KSa2 := D * math.Sqrt(16*11.0/27)
	check(t, "2-sample KS", kt2.Rows[0][1].Num, KSa2/math.Sqrt(27), 1e-12)
	check(t, "2-sample Pr > KSa", kt2.Rows[0][3].Num, 0.6199, 5e-5)
	check(t, "2-sample KSa", kt2.Rows[1][1].Num, KSa2, 1e-12)
	check(t, "2-sample D", kt2.Rows[2][1].Num, D, 1e-12)
	check(t, "2-sample CvM", s2.Dataset("_CVMSTATS_").Rows[0][2].Num, 0.098638, 5e-6)
	check(t, "2-sample CvM", s2.Dataset("_CVMSTATS_").Rows[1][2].Num, 0.143474, 5e-6)
	check(t, "2-sample CM", s2.Dataset("_CVMTEST_").Rows[0][1].Num, 0.008967, 5e-6)
	check(t, "2-sample CMa", s2.Dataset("_CVMTEST_").Rows[0][3].Num, 0.242112, 5e-6)
	ku := s2.Dataset("_KUIPERSTATS_")
	check(t, "Kuiper dev 0", ku.Rows[0][2].Num, 0.090909, 5e-6)
	check(t, "Kuiper dev .04", ku.Rows[1][2].Num, 0.295455, 5e-6)
	kut := s2.Dataset("_KUIPERTEST_")
	check(t, "Kuiper K", kut.Rows[0][1].Num, 0.386364, 5e-6)
	check(t, "Kuiper Pr > Ka", kut.Rows[0][3].Num, 0.8383, 5e-5)
	check(t, "Kuiper Ka", kut.Rows[1][1].Num, 0.986440, 5e-6)
}

// TestNpScaleTests pins the Mood, Klotz and Ansari-Bradley two-sample
// scale tests: score sums, the general (tie-corrected) linear rank
// variance V = n1*n2/(N*(N-1)) * sum (s - sbar)^2 -- NOT the Wilcoxon
// shortcut -- and the standardized Z.
func TestNpScaleTests(t *testing.T) {
	s := NewSession(".")
	s.RunProgram(`
data spread;
  input group $ resp @@;
  datalines;
A  9.2  A 10.1  A  9.8  A 10.3  A  9.5  A 10.6
A  9.9  A 10.2  A  9.4  A 10.5  A  9.7  A 10.0
B  5.1  B 15.2  B  7.3  B 12.9  B  6.0  B 14.1
B  8.2  B 11.5  B  4.9  B 13.6  B  7.0  B 12.0
;
ods output MoodScores=_moodscores_ MoodTest=_moodtest_;
proc npar1way data=spread mood;
  class group; var resp; run;
ods output KlotzScores=_klotzscores_ KlotzTest=_klotztest_;
proc npar1way data=spread klotz;
  class group; var resp; run;
ods output ABScores=_abscores_ ABTest=_abtest_;
proc npar1way data=spread ab;
  class group; var resp; run;
`)
	check := func(t *testing.T, name string, got, want, tol float64) {
		t.Helper()
		if math.Abs(got-want) > tol {
			t.Errorf("%s = %.9f, want %.9f (tol %g)", name, got, want, tol)
		}
	}
	type vals struct{ sum, e, sd, z float64 }
	want := map[string]vals{
		"_MOODSCORES_":  {143, 575, 106.957936, 0},
		"_KLOTZSCORES_": {1.552862, 9.392289, 2.256467, 0},
		"_ABSCORES_":    {114, 78, 8.637633, 0},
	}
	for ods, w := range want {
		st := s.Dataset(ods)
		if st == nil {
			t.Errorf("%s: dataset not found", ods)
			continue
		}
		iSum := st.Index("SumOfScores")
		check(t, ods+" sum A", st.Rows[0][iSum].Num, w.sum, 2e-6)
		check(t, ods+" E A", st.Rows[0][iSum+1].Num, w.e, 2e-6)
		check(t, ods+" SD A", st.Rows[0][iSum+2].Num, w.sd, 2e-6)
	}
	for ods, z := range map[string]float64{
		"_MOODTEST_":  -4.038970997,
		"_KLOTZTEST_": -3.474204238,
		"_ABTEST_":    4.167808701,
	} {
		zt := s.Dataset(ods)
		if zt == nil {
			t.Errorf("%s: dataset not found", ods)
			continue
		}
		// single-row ODS layout with a Z column (older stacked layout has
		// Z in row 1 column 1)
		iZ := zt.Index("Z")
		got := zt.Rows[0][1].Num
		if iZ >= 0 {
			got = zt.Rows[0][iZ].Num
		}
		check(t, ods+" Z", got, z, 2e-6)
	}
}

// TestNpSavage pins the Savage score analysis against the published
// SAS/STAT NPAR1WAY chapter: Example 84.5 (Mice, three samples) and the
// two-sample Gossypol subset (Figure 84.12: S=-1.8346, Z=-0.7638).
func TestNpSavage(t *testing.T) {
	s := NewSession(".")
	s.RunProgram(`
data mice;
  input treatment $ days @@;
  datalines;
1 1 1 1 1 3 1 3 1 4
2 3 2 4 2 4 2 4 2 15
3 4 3 4 3 10 3 10 3 26
;
ods output SavageScores=_ss_ SavageAnalysis=_sa_;
proc npar1way data=mice savage;
  class treatment; var days; run;
data g2;
  input dose $ gain @@;
  datalines;
0 228 0 229 0 218 0 216 0 224 0 208 0 235 0 229 0 233 0 219
0 224 0 220 0 232 0 200 0 208 0 232
.04 186 .04 229 .04 220 .04 208 .04 228 .04 198 .04 222 .04 273
.04 216 .04 198 .04 213
;
ods output SavageScores=_gs_ SavageTest=_gt_;
proc npar1way data=g2 savage;
  class dose; var gain; run;
`)
	check := func(t *testing.T, name string, got, want, tol float64) {
		t.Helper()
		if math.Abs(got-want) > tol {
			t.Errorf("%s = %.9f, want %.9f (tol %g)", name, got, want, tol)
		}
	}
	// Mice sums (published: -3.367980, 0.095618, 3.272362)
	ss := s.Dataset("_SS_")
	if ss == nil {
		t.Fatal("no SavageScores capture")
	}
	sums := []float64{-3.36798016798017, 0.095618270618274, 3.2723618973619}
	iSum := ss.Index("SumOfScores")
	for i, want := range sums {
		check(t, "mice S", ss.Rows[i][iSum].Num, want, 2e-9)
		check(t, "mice SD", ss.Rows[i][iSum+2].Num, 1.63455486646275, 2e-9)
		check(t, "mice mean", ss.Rows[i][iSum+3].Num, want/5, 2e-9)
	}
	sa := s.Dataset("_SA_")
	if sa == nil {
		t.Fatal("no SavageAnalysis capture")
	}
	// ODS layout with _Proc_/_Run_ first: Variable, ChiSquare, DF, Prob
	check(t, "mice chi2", sa.Rows[0][3].Num, 5.50466266705125, 2e-9)
	// two-sample: S from the smaller class (.04, n=11); ODS single-row
	gt := s.Dataset("_GT_")
	if gt == nil {
		t.Fatal("no SavageTest capture")
	}
	check(t, "g2 S", gt.Rows[0][3].Num, -1.83455385708751, 2e-9)
	check(t, "g2 Z", gt.Rows[0][4].Num, -0.763812212423011, 2e-9)
}

// TestNpHodgesLehmann pins the HL location-shift estimation against the
// published SAS Example 84.4 (React data, ALPHA=.02, EXACT HL):
// estimate 0.35; Moses limits 0.00/0.82, midpoint 0.41, ASE 0.1762;
// exact limits 0.00/1.33, midpoint 0.6650.
func TestNpHodgesLehmann(t *testing.T) {
	s := NewSession(".")
	s.RunProgram(`
data react;
  input stim time @@;
  datalines;
1 1.94 1 1.94 1 2.92 1 2.92 1 2.92 1 2.92 1 3.27
1 3.27 1 3.27 1 3.27 1 3.70 1 3.70 1 3.74
2 3.27 2 3.27 2 3.27 2 3.70 2 3.70 2 3.74
;
ods output HodgesLehmann=_hl_;
proc npar1way hl alpha=.02 data=react;
  class stim; var time; exact hl; run;
`)
	hl := s.Dataset("_HL_")
	if hl == nil {
		t.Fatal("no HodgesLehmann capture")
	}
	check := func(t *testing.T, name string, got, want, tol float64) {
		t.Helper()
		if math.Abs(got-want) > tol {
			t.Errorf("%s = %.9f, want %.9f (tol %g)", name, got, want, tol)
		}
	}
	// ODS rows: 0 = Asymptotic (Moses), 1 = Exact
	// columns: _Proc_, _Run_, Variable, Type, Shift, LowerCL, UpperCL,
	// Midpoint, StdErr (published values rounded to 4 decimals)
	check(t, "shift", hl.Rows[0][4].Num, 0.35, 5e-9)
	check(t, "moses L", hl.Rows[0][5].Num, 0, 5e-9)
	check(t, "moses U", hl.Rows[0][6].Num, 0.82, 5e-9)
	check(t, "moses mid", hl.Rows[0][7].Num, 0.41, 5e-9)
	check(t, "moses ASE", hl.Rows[0][8].Num, 0.1762, 5e-5)
	check(t, "exact L", hl.Rows[1][5].Num, 0, 5e-9)
	check(t, "exact U", hl.Rows[1][6].Num, 1.33, 5e-9)
	check(t, "exact mid", hl.Rows[1][7].Num, 0.665, 5e-9)
}

// TestNpFlignerPolicello pins the FP placement test on a fully
// hand-checked example (equal sample sizes, ties -> half credit,
// reference class X = the second class): placement sums 3.5 / 32.5,
// difference -29, z = -4.35681284677437.
func TestNpFlignerPolicello(t *testing.T) {
	s := NewSession(".")
	s.RunProgram(`
data fp3;
  input group $ y @@;
  datalines;
A 2 A 4 A 4 A 6 A 3 A 5
B 5 B 7 B 6 B 8 B 5 B 9
;
ods output FPPlacements=_fpp_ FPTest=_fpt_;
proc npar1way data=fp3 fp;
  class group; var y; run;
`)
	fp := s.Dataset("_FPP_")
	if fp == nil {
		t.Fatal("no FPPlacements capture")
	}
	check := func(t *testing.T, name string, got, want, tol float64) {
		t.Helper()
		if math.Abs(got-want) > tol {
			t.Errorf("%s = %.9f, want %.9f (tol %g)", name, got, want, tol)
		}
	}
	// rows in sorted class order: A first, B second; _Proc_/_Run_ lead
	check(t, "A sum", fp.Rows[0][5].Num, 3.5, 1e-12)
	check(t, "A mean", fp.Rows[0][6].Num, 0.583333333333333, 1e-12)
	check(t, "A sd", fp.Rows[0][7].Num, 1.02062072615966, 1e-12)
	check(t, "B sum", fp.Rows[1][5].Num, 32.5, 1e-12)
	check(t, "B mean", fp.Rows[1][6].Num, 5.41666666666667, 1e-12)
	check(t, "B sd", fp.Rows[1][7].Num, 0.735980072193987, 1e-12)
	ft := s.Dataset("_FPT_")
	if ft == nil {
		t.Fatal("no FPTest capture")
	}
	// single-row ODS layout: _Proc_, _Run_, Variable, Statistic, Z, Prob1
	check(t, "diff", ft.Rows[0][3].Num, -29, 1e-12)
	check(t, "z", ft.Rows[0][4].Num, -4.35681284677437, 1e-12)
}

// TestNpDSCF pins the Dwass-Steel-Critchlow-Fligner all-pairs analysis
// against an independent implementation (R PMCMRplus::dscfAllPairsTest,
// which uses the same pairwise-rank / studentized-range construction).
func TestNpDSCF(t *testing.T) {
	s := NewSession(".")
	s.RunProgram(`
data doses;
  input dose $ resp @@;
  datalines;
A 12.1 A 13.4 A 11.9 A 12.8 A 13.1 A 12.5 A 11.6 A 12.2
B 13.8 B 15.2 B 14.1 B 13.5 B 14.8 B 15.0 B 13.9
C 15.6 C 17.1 C 16.3 C 15.9 C 16.8 C 17.4 C 16.0
D 18.2 D 20.5 D 19.1 D 18.7 D 20.1 D 19.6 D 18.9 D 20.8
;
ods output DSCF=_dscf_;
proc npar1way data=doses dscf;
  class dose; var resp; run;
`)
	dt := s.Dataset("_DSCF_")
	if dt == nil {
		t.Fatal("no DSCF capture")
	}
	if len(dt.Rows) != 6 {
		t.Fatalf("DSCF rows = %d, want 6", len(dt.Rows))
	}
	check := func(t *testing.T, name string, got, want, tol float64) {
		t.Helper()
		if math.Abs(got-want) > tol {
			t.Errorf("%s = %.9f, want %.9f (tol %g)", name, got, want, tol)
		}
	}
	// R: A-B 4.582576 p 0.00655441; A-D 4.752708 p 0.00432534;
	//     B-C 4.427189 p 0.00945263; ODS cols with _Proc_/_Run_ first:
	//     Variable, Comparison, WilcoxonZ, DSCFValue, PValue
	iZ, iD, iP := dt.Index("WilcoxonZ"), dt.Index("DSCFValue"), dt.Index("PValue")
	check(t, "AB z", dt.Rows[0][iZ].Num, -3.240370, 2e-5)
	check(t, "AB dscf", dt.Rows[0][iD].Num, 4.582576, 2e-5)
	check(t, "AB p", dt.Rows[0][iP].Num, 0.00655441, 2e-6)
	check(t, "AD dscf", dt.Rows[2][iD].Num, 4.752708, 2e-5)
	check(t, "AD p", dt.Rows[2][iP].Num, 0.00432534, 2e-6)
	check(t, "BC dscf", dt.Rows[3][iD].Num, 4.427189, 2e-5)
	check(t, "BC p", dt.Rows[3][iP].Num, 0.00945263, 2e-6)
}

// TestNpExactWilcoxon pins the exact rank-sum permutation p-value with
// mid-rank ties (n1=7, n2=8): one-sided P(S>=80) = 12/6435 =
// 0.0018648018648018648 (scipy.stats.mannwhitneyu exact, greater);
// two-sided = 2 x one-sided, SAS convention. The normal approximation
// with tie correction is pinned for contrast.
func TestNpExactWilcoxon(t *testing.T) {
	s := NewSession(".")
	s.Out.SelectNone = true
	s.RunProgram(`data drug;
  length group $ 4;
  input group $ relief @@;
  datalines;
new 4.1 new 4.7 new 3.9 new 5.2 new 4.4 new 3.6 new 5.0
old 2.8 old 3.5 old 4.1 old 2.6 old 3.1 old 3.9 old 2.4 old 3.3
;
ods output WilcoxonTest=_wt_;
proc npar1way data=drug wilcoxon;
  class group;
  var relief;
  exact wilcoxon;
run;`)
	wt := s.Dataset("_WT_")
	if wt == nil {
		t.Fatal("no WilcoxonTest output")
	}
	uvClose(t, "S", wt.Rows[0][wt.Index("Statistic")].Num, 80, 1e-12)
	uvClose(t, "Z", wt.Rows[0][wt.Index("Z")].Num, 2.7244660125909883, 1e-9)
	uvClose(t, "normal one-sided", wt.Rows[0][wt.Index("Prob1")].Num, 0.0032202779436714913, 1e-9)
	uvClose(t, "exact one-sided", wt.Rows[0][wt.Index("Prob1E")].Num, 0.0018648018648018648, 1e-12)
	uvClose(t, "exact two-sided", wt.Rows[0][wt.Index("Prob2E")].Num, 0.0037296037296037296, 1e-12)
}
