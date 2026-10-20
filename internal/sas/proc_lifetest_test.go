package sas

import (
	"math"
	"testing"
)

// TestLifetestRats pins PROC LIFETEST against the published SAS/STAT
// LIFETEST chapter getting-started example (Exposed rats data):
// placebo quartiles with LOGLOG limits, mean/SE 235.156/10.211, and
// the equality tests Log-Rank 5.6485, Wilcoxon 5.0312, -2Log(LR)
// 0.1983.
func TestLifetestRats(t *testing.T) {
	s := NewSession(".")
	s.RunProgram(`
data exposed;
  input days status treatment @@;
  datalines;
179 1 1 378 0 1 256 1 1 355 1 1 262 1 1 319 1 1
256 1 1 256 1 1 255 1 1 171 1 1 224 0 1 325 1 1
225 1 1 325 1 1 287 1 1 217 1 1 319 1 1 255 1 1
264 1 1 256 1 1 237 0 0 291 1 0 156 1 0 323 1 0
270 1 0 253 1 0 257 1 0 206 1 0 242 1 0 206 1 0
157 1 0 237 1 0 249 1 0 211 1 0 180 1 0 229 1 0
226 1 0 234 1 0 268 0 0 209 1 0
;
ods output Quartiles=_qt_ Means=_means_ ProductLimitEstimates=_pl_;
proc lifetest data=exposed alpha=.05;
  where treatment = 0;
  time days*status(0);
run;
ods output HomTests=_ht_ HomStats=_hstats_;
proc lifetest data=exposed alpha=.05;
  time days*status(0);
  strata treatment;
run;
`)
	pl := s.Dataset("_PL_")
	if pl == nil {
		t.Fatal("no ProductLimitEstimates capture")
	}
	check := func(t *testing.T, name string, got, want, tol float64) {
		t.Helper()
		if math.Abs(got-want) > tol {
			t.Errorf("%s = %.6f, want %.6f (tol %g)", name, got, want, tol)
		}
	}
	// ODS layout: _Proc_, _Run_, STRATUM, treatment, days, Censor,
	// Survival, Failure, StdErr, Failed, Left. Placebo (treatment=0):
	// time 206 has two deaths (S drops 0.85 -> 0.75).
	found206 := false
	iSurv := pl.Index("Survival")
	iStd := pl.Index("StdErr")
	iTime := pl.Index("days")
	iFailed := pl.Index("Failed")
	for _, r := range pl.Rows {
		if r[iTime].Num == 206 && r[iFailed].Num == 5 {
			found206 = true
			check(t, "S(206)", r[iSurv].Num, 0.75, 1e-9)
			check(t, "SE(206)", r[iStd].Num, 0.0968, 5e-5)
		}
	}
	if !found206 {
		t.Error("placebo time-206 row not found in PL estimates")
	}
	qt := s.Dataset("_QT_")
	if qt == nil {
		t.Fatal("no Quartiles capture")
	}
	// ODS layout: _Proc_, _Run_, STRATUM, treatment, Percent, days,
	// Transform, LowerLimit, UpperLimit
	var median, medLo, medHi float64
	haveMedian := false
	iPct := qt.Index("Percent")
	iEst := qt.Index("Estimate")
	iLo := qt.Index("LowerLimit")
	iHi := qt.Index("UpperLimit")
	for _, r := range qt.Rows {
		if r[iPct].Num == 50 {
			median, medLo, medHi = r[iEst].Num, r[iLo].Num, r[iHi].Num
			haveMedian = true
		}
	}
	if !haveMedian {
		t.Fatal("no median row in Quartiles")
	}
	check(t, "median", median, 235.5, 5e-6)
	check(t, "median LCL", medLo, 206, 5e-6)
	check(t, "median UCL", medHi, 253, 5e-6)
	mn := s.Dataset("_MEANS_")
	if mn == nil {
		t.Fatal("no Means capture")
	}
	check(t, "mean SE", mn.Rows[0][mn.Index("StdErr")].Num, 10.211, 5e-4)
	ht := s.Dataset("_HT_")
	if ht == nil {
		t.Fatal("no HomogeneityTests capture")
	}
	iTest := ht.Index("Test")
	iChi := ht.Index("ChiSq")
	for _, r := range ht.Rows {
		switch r[iTest].Str {
		case "Log-Rank":
			check(t, "log-rank", r[iChi].Num, 5.6485, 5e-5)
		case "Wilcoxon":
			check(t, "wilcoxon", r[iChi].Num, 5.0312, 5e-5)
		case "-2Log(LR)":
			check(t, "-2Log(LR)", r[iChi].Num, 0.1983, 5e-5)
		}
	}
}
