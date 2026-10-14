package sas

import (
	"math"
	"testing"
)

// TestGLMTukeyKramer pins the Tukey-Kramer multiple-comparison machinery
// against scipy.stats.studentized_range / statsmodels pairwise_tukeyhsd
// for the unbalanced one-way design in examples/anova_test.sas
// (machines A-D, n = 12/9/7/14).
func TestGLMTukeyKramer(t *testing.T) {
	// studentized range quantile: qcrit(0.95, k=4, df=38) = 3.799250
	q := lmQTukey(0.95, 1, 4, 38)
	if math.Abs(q-3.799250) > 1e-4 {
		t.Errorf("lmQTukey = %.6f, want 3.799250", q)
	}
	if p := lmPTukey(q, 1, 4, 38); math.Abs(p-0.95) > 1e-6 {
		t.Errorf("lmPTukey round trip = %.8f, want 0.95", p)
	}

	s := NewSession(".")
	s.Out.BeginProc("")
	s.Out.SelectNone = true
	s.Out.ODSOutputs["CLDIFFS"] = "_cld_"
	s.RunProgram(`
data tensile;
  input machine $ strength @@;
  datalines;
A 52.3 A 54.1 A 53.8 A 55.0 A 52.9 A 54.6
A 53.2 A 55.3 A 54.8 A 53.5 A 54.2 A 53.9
B 56.8 B 58.2 B 57.5 B 55.9 B 57.9 B 58.6
B 56.4 B 57.2 B 58.0
C 50.1 C 51.8 C 49.6 C 52.4 C 50.9 C 51.2 C 49.9
D 53.4 D 52.8 D 54.5 D 53.1 D 52.2 D 54.0
D 53.7 D 52.5 D 54.3 D 53.6 D 52.9 D 54.8
D 53.3 D 52.6
;
proc glm data=tensile;
  class machine;
  model strength = machine;
  means machine / tukey cldiff;
run;
`)
	s.Out.EndProc()
	ds := s.Dataset("_CLD_")
	if ds == nil {
		t.Fatal("no CLDiffs capture")
	}
	means := map[string]float64{"A": 53.96666666666667, "B": 57.38888888888889, "C": 50.84285714285714, "D": 53.40714285714286}
	ns := map[string]int{"A": 12, "B": 9, "C": 7, "D": 14}
	mse := 29.641984127 / 38
	row := func(cmp string) []Value {
		for _, r := range ds.Rows {
			if len(r) >= 5 && r[0].Str == cmp {
				return r
			}
		}
		return nil
	}
	// statsmodels: B - A meandiff 3.4222, CI [2.376, 4.4685], reject
	r := row("B - A")
	if r == nil {
		t.Fatal("missing B - A comparison")
	}
	d := means["B"] - means["A"]
	h := q / math.Sqrt2 * math.Sqrt(mse*(1.0/float64(ns["B"])+1.0/float64(ns["A"])))
	if math.Abs(r[1].Num-d) > 1e-6 || math.Abs(r[2].Num-(d-h)) > 1e-6 || math.Abs(r[3].Num-(d+h)) > 1e-6 {
		t.Errorf("B - A = [%v, %v, %v], want %.6f [%.6f, %.6f]", r[1].Num, r[2].Num, r[3].Num, d, d-h, d+h)
	}
	if r[4].Str != "***" {
		t.Errorf("B - A should be significant, got %q", r[4].Str)
	}
	// A - D is not significant: statsmodels CI [-1.4929, 0.3739]
	r = row("A - D")
	if r == nil {
		t.Fatal("missing A - D comparison")
	}
	d = means["A"] - means["D"]
	h = q / math.Sqrt2 * math.Sqrt(mse*(1.0/float64(ns["A"])+1.0/float64(ns["D"])))
	if math.Abs(r[1].Num-d) > 1e-6 || math.Abs(r[2].Num-(d-h)) > 1e-6 || math.Abs(r[3].Num-(d+h)) > 1e-6 {
		t.Errorf("A - D = [%v, %v, %v], want %.6f [%.6f, %.6f]", r[1].Num, r[2].Num, r[3].Num, d, d-h, d+h)
	}
	if r[4].Str != "" {
		t.Errorf("A - D should not be significant, got %q", r[4].Str)
	}
	// 12 ordered pairwise comparisons total
	if len(ds.Rows) != 12 {
		t.Errorf("expected 12 comparisons, got %d", len(ds.Rows))
	}
}
