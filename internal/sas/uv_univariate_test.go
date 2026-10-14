package sas

import (
	"math"
	"sort"
	"testing"
)

func uvClose(t *testing.T, name string, got, want, tol float64) {
	t.Helper()
	if math.Abs(got-want) > tol*math.Max(1, math.Abs(want)) {
		t.Errorf("%s = %.12g, want %.12g", name, got, want)
	}
}

// References: scipy 1.15 (shapiro, kstest, cramervonmises, anderson,
// numpy percentile methods) and the SAS formulas for the signed rank t
// approximation and table-interpolated EDF p-values.
func TestUVNormalityAndLocation(t *testing.T) {
	y30 := []float64{1.668, 1.337, 1.146, 1.746, 1.109, 2.382, 1.806, 1.629, 0.464, 0.24, 0.507, 1.877, 1.605, 0.369, 1.863, 0.825, 0.826, 3.089, 1.527, 1.75, 1.767, 0.361, 1.826, 5.813, 0.548, 2.528, 2.713, 2.685, 0.938, 1.137}
	x := append([]float64(nil), y30...)
	sort.Float64s(x)
	w, pw := uvShapiroWilk(x)
	uvClose(t, "W", w, 0.8382899892506581, 1e-10)
	uvClose(t, "Pr<W", pw, 0.0003559815646486467, 1e-8)
	tests := uvNormalityTests(x)
	uvClose(t, "D", tests[1].Stat, 0.2014675595255565, 1e-12)
	uvClose(t, "W-Sq", tests[2].Stat, 0.14293521266831846, 1e-12)
	uvClose(t, "A-Sq", tests[3].Stat, 1.0054279953010905, 1e-12)
	a := 1.0054279953010905 * (1 + 0.75/30 + 2.25/900)
	uvClose(t, "Pr>A-Sq", tests[3].P, 0.025+(a-0.873)/(1.035-0.873)*(0.01-0.025), 1e-12)
	if tests[1].Sign != "<" || tests[1].P != 0.01 {
		t.Errorf("Pr > D = %s%v, want <0.01", tests[1].Sign, tests[1].P)
	}
	s, ps := uvSignedRank(x, 2)
	uvClose(t, "S", s, -127.5, 1e-12)
	uvClose(t, "Pr>=|S|", ps, 0.006432806498339361, 1e-9)
	_, pm := uvSignTest(x, 2)
	uvClose(t, "Pr>=|M|", pm, 0.0014309063553810122, 1e-10)
	for _, c := range []struct {
		p    float64
		want [5]float64 // PCTLDEF=1,3,4,5 (index 1 unused)
	}{
		{10, [5]float64{0.369, 0, 0.369, 0.3785, 0.4165}},
		{33.333, [5]float64{1.1089828999999998, 0, 1.109, 1.11833044, 1.109}},
		{50, [5]float64{1.605, 0, 1.605, 1.617, 1.617}},
		{97.5, [5]float64{3.77, 0, 5.813, 5.813, 5.813}},
	} {
		for k, def := range []int{1, 3, 4, 5} {
			idx := []int{0, 2, 3, 4}[k]
			uvClose(t, "pctl", uvPctl(x, nil, c.p, def), c.want[idx], 1e-9)
		}
	}
	// Exact signed rank (n <= 20, no ties): scipy wilcoxon(method='exact').
	z8 := []float64{0.8624447963157468, -0.1255920840343272, 0.6691532407894528, 1.2188436051712233, 0.3829295827134724, -0.8757211434228455, -1.5143186317046384, 1.7533841175163727}
	_, pz := uvSignedRank(z8, 0)
	uvClose(t, "exact Pr>=|S|", pz, 0.546875, 1e-12)
	sort.Float64s(z8)
	w8, p8 := uvShapiroWilk(z8)
	uvClose(t, "W8", w8, 0.9675070838071979, 1e-10)
	uvClose(t, "Pr<W8", p8, 0.8777598459775224, 1e-8)
}

// SAS documentation (SASHELP.CLASS Height): Moments and Shapiro-Wilk.
func TestUVClassHeightMoments(t *testing.T) {
	h := []float64{69, 56.5, 65.3, 62.8, 63.5, 57.3, 59.8, 62.5, 62.5, 59, 51.3, 64.3, 56.3, 66.5, 72, 64.8, 67, 57.5, 66.5}
	sort.Float64s(h)
	m := uvComputeMoments(&uvSample{X: h})
	uvClose(t, "mean", m.Mean, 62.3368421, 1e-8)
	uvClose(t, "std", m.Std, 5.12707525, 1e-8)
	uvClose(t, "skew", m.Skew, -0.2596696, 1e-6)
	uvClose(t, "kurt", m.Kurt, -0.1389692, 1e-6)
	uvClose(t, "css", m.CSS, 473.164211, 1e-8)
	uvClose(t, "cv", m.CV, 8.22479143, 1e-8)
	w, p := uvShapiroWilk(h)
	uvClose(t, "W", w, 0.9790827558837575, 1e-10)
	uvClose(t, "p", p, 0.9311831451236767, 1e-8)
}

// OUTPUT OUT= with CLASS, repeated keywords, PCTLPTS/PCTLPRE/PCTLNAME
// (numpy averaged_inverted_cdf = PCTLDEF=5, scipy ttest_1samp).
func TestUVOutput(t *testing.T) {
	s := NewSession(t.TempDir())
	s.RunProgram(`proc univariate data=sashelp.class noprint; var height weight; class sex;
 output out=o2 median=mh t=th tw pctlpts=33.333 pctlpre=a_ b_; run;
 proc univariate data=sashelp.class noprint; var height;
 output out=o pctlpts=2.5 97.5 pctlpre=Pct95_ pctlname=Lower Upper; run;`)
	o2 := s.Dataset("O2")
	if o2 == nil || len(o2.Rows) != 2 {
		t.Fatalf("O2 missing or wrong rows")
	}
	get := func(d *Dataset, r int, v string) float64 { return d.Rows[r][d.Index(v)].Num }
	uvClose(t, "mh M", get(o2, 1, "mh"), 64.15, 1e-12)
	uvClose(t, "th F", get(o2, 0, "th"), 36.22056669177154, 1e-10)
	uvClose(t, "b_33_33 M", get(o2, 1, "b_33_33"), 99.5, 1e-12)
	if o2.Index("tw") < 0 || o2.Index("a_33_33") < 0 || o2.Index("Sex") != 0 {
		t.Errorf("unexpected O2 variables")
	}
	o := s.Dataset("O")
	uvClose(t, "lower", get(o, 0, "Pct95_Lower"), 51.3, 1e-12)
	uvClose(t, "upper", get(o, 0, "Pct95_Upper"), 72, 1e-12)
}
