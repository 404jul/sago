package sas

import (
	"math"
	"testing"
)

// TestGLMTwoWayTypeISSIII pins the unbalanced two-way ANOVA:
// Type I and Type III sums of squares (SAS sigma-restricted definition,
// reproduced by statsmodels anova_lm typ=3 with Sum coding) and
// LSMEANS as equally weighted cell means.
func TestGLMTwoWayTypeISSIII(t *testing.T) {
	s := NewSession(".")
	s.Out.BeginProc("")
	s.Out.SelectNone = true
	s.Out.ODSOutputs["MODELANOVA"] = "_ss_"
	s.Out.ODSOutputs["LSMEANS"] = "_lsm_"
	s.RunProgram(`
data crop;
  input fert $ irrig $ yield @@;
  datalines;
N Low 51.8 N Low 52.4 N Low 51.9 N Low 52.8 N Low 52.1 N Low 52.3
N High 54.9 N High 55.4 N High 54.7 N High 55.2
P Low 53.6 P Low 54.1 P Low 53.9 P Low 54.5
P High 59.8 P High 60.3 P High 59.6 P High 60.8 P High 60.1 P High 59.9
K Low 49.8 K Low 50.3 K Low 49.5 K Low 50.6 K Low 50.1
K High 58.7 K High 59.2 K High 58.9 K High 59.6 K High 59.1
;
proc glm data=crop;
  class fert irrig;
  model yield = fert irrig fert*irrig;
  lsmeans fert;
run;
`)
	s.Out.EndProc()
	ss := s.Dataset("_SS_")
	if ss == nil {
		t.Fatal("no ModelANOVA capture")
	}
	// columns: Dependent(hidden), HypothesisType(hidden), Source, DF, SS, MS, F, ProbF
	row := func(hty float64, src string) []Value {
		for _, r := range ss.Rows {
			if len(r) >= 8 && r[1].Num == hty && r[2].Str == src {
				return r
			}
		}
		return nil
	}
	type want struct {
		ty        float64
		src       string
		df, ss, f float64
	}
	for _, w := range []want{
		// Type I (sequential); values from statsmodels anova_lm typ=1
		{1, "fert", 2, 98.584666667, 339.345795},
		{1, "irrig", 1, 264.48268493, 1820.792018},
		{1, "fert*irrig", 2, 47.176148402, 162.388616},
		// Type III (sigma-restricted; statsmodels typ=3 with Sum coding)
		{3, "fert", 2, 60.005573059, 206.549757},
		{3, "irrig", 1, 260.71189414, 1794.832565},
		{3, "fert*irrig", 2, 47.176148402, 162.388616},
	} {
		r := row(w.ty, w.src)
		if r == nil {
			t.Fatalf("missing row type %v %s", w.ty, w.src)
		}
		if r[3].Num != w.df {
			t.Errorf("type %v %s DF = %v, want %v", w.ty, w.src, r[3].Num, w.df)
		}
		if math.Abs(r[4].Num-w.ss) > 1e-6 {
			t.Errorf("type %v %s SS = %v, want %v", w.ty, w.src, r[4].Num, w.ss)
		}
		if math.Abs(r[6].Num-w.f) > 1e-3 {
			t.Errorf("type %v %s F = %v, want %v", w.ty, w.src, r[6].Num, w.f)
		}
	}
	// Type I and Type III must differ for the main effects under unbalance
	if math.Abs(row(1, "fert")[4].Num-row(3, "fert")[4].Num) < 1e-6 {
		t.Error("Type I SS == Type III SS for fert (expected difference under unbalance)")
	}
	if math.Abs(row(1, "fert*irrig")[4].Num-row(3, "fert*irrig")[4].Num) > 1e-9 {
		t.Error("interaction SS should be identical across types")
	}
	// LSMEANS: equally weighted cell means
	lsm := s.Dataset("_LSM_")
	if lsm == nil {
		t.Fatal("no LSMeans capture")
	}
	wantLSM := map[string]float64{"K": 54.58, "N": 53.633333333, "P": 57.054166667}
	for _, r := range lsm.Rows {
		if len(r) >= 2 {
			want, ok := wantLSM[r[0].Str]
			if !ok {
				t.Errorf("unexpected LSMEAN level %q", r[0].Str)
				continue
			}
			if math.Abs(r[1].Num-want) > 1e-8 {
				t.Errorf("LSMEAN %s = %v, want %v", r[0].Str, r[1].Num, want)
			}
		}
	}
}
