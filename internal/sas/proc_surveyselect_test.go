package sas

import "testing"

// TestBootstrapMedian pins the bootstrap pipeline (SURVEYSELECT URS
// with a fixed seed -> expand NumberHits -> BY-replicate medians ->
// percentile CI): deterministic across runs, per-replicate draws sum
// to n, and the 2.5/50/97.5 percentiles match an independent numpy
// bootstrap (discrete-median percentiles are RNG-stable here).
func TestBootstrapMedian(t *testing.T) {
	run := func() (lo, med, hi float64) {
		s := NewSession(".")
		s.Out.SelectNone = true
		s.RunProgram(`data sample;
  input x @@;
  datalines;
2.31 0.55 4.72 1.03 0.28 8.94 1.77 0.66 3.40 0.91
12.06 2.05 0.44 1.58 5.21 0.37 2.84 1.12 0.73 6.65
1.95 0.52 3.09 0.86 2.42 0.61 1.31 4.08 0.97 1.70
;
proc surveyselect data=sample out=boot seed=20261014
  method=urs sampsize=30 reps=1000 noprint;
run;
data bootexp;
  set boot;
  do i = 1 to numberhits;
    output;
  end;
  drop i numberhits expectedhits;
run;
proc sort data=bootexp; by replicate; run;
proc means data=bootexp noprint;
  by replicate;
  var x;
  output out=bmed (keep=replicate median) median=median;
run;
proc univariate data=bmed noprint;
  var median;
  output out=bci pctlpts=2.5 50 97.5 pctlpre=pm pctlname=l med u;
run;`)
		bci := s.Dataset("BCI")
		if bci == nil || len(bci.Rows) != 1 {
			t.Fatal("no BCI")
		}
		return bci.Rows[0][0].Num, bci.Rows[0][1].Num, bci.Rows[0][2].Num
	}
	lo, med, hi := run()
	uvClose(t, "p2.5", lo, 0.94, 1e-9)
	uvClose(t, "p50", med, 1.64, 1e-9)
	uvClose(t, "p97.5", hi, 2.365, 1e-9)
	lo2, med2, hi2 := run()
	if lo != lo2 || med != med2 || hi != hi2 {
		t.Error("bootstrap not deterministic under fixed seed")
	}
}
