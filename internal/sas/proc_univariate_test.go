package sas

import "testing"

// TestDsMedianCI pins the distribution-free median CI built in a DATA
// step (order statistics + PROBBNML): n=20, k=6 since
// P(Bin(20,.5)<=5)=0.0207 <= .025, CI = (x_(6), x_(15)) = (-0.39, 0.28).
func TestDsMedianCI(t *testing.T) {
	s := NewSession(".")
	s.Out.SelectNone = true
	s.RunProgram(`data reaction;
  input t @@;
  datalines;
0.42 -0.51 0.33 -0.24 0.18 -0.62 0.55 -0.39 0.21 -0.47
0.28 -0.15 0.61 -0.33 0.09 -0.55 0.37 -0.28 0.16 -0.41
;
proc sort data=reaction; by t; run;
%let N = 20;
data medci;
  array x(&N) _temporary_;
  if _n_ = 1 then do;
    do i = 1 to &N;
      set reaction point=i nobs=nobs;
      x(i) = t;
    end;
  end;
  k = 0;
  do j = 1 to &N;
    if probbnml(0.5, &N, j-1) <= 0.025 then k = j;
  end;
  lo_rank = k;
  hi_rank = &N - k + 1;
  ci_lo = x(lo_rank);
  ci_hi = x(hi_rank);
  output;
  stop;
  drop i j t;
run;`)
	d := s.Dataset("MEDCI")
	if d == nil || len(d.Rows) != 1 {
		t.Fatal("no MEDCI output")
	}
	get := func(col string) float64 {
		i := d.Index(col)
		if i < 0 {
			t.Fatalf("no column %s", col)
		}
		return d.Rows[0][i].Num
	}
	if get("LO_RANK") != 6 || get("HI_RANK") != 15 {
		t.Errorf("ranks = %v/%v, want 6/15", get("LO_RANK"), get("HI_RANK"))
	}
	if get("CI_LO") != -0.39 || get("CI_HI") != 0.28 {
		t.Errorf("CI = (%v, %v), want (-0.39, 0.28)", get("CI_LO"), get("CI_HI"))
	}
}

// TestDsUnderscoreN pins _N_ semantics with a streaming SET: _N_ = 1 on
// the first row (previously clobbered by the END= zero-value sentinel:
// every plain SET wrote its last-flag into _N_'s slot 0). Also pins
// END= and `if last then output;`.
func TestDsUnderscoreN(t *testing.T) {
	s := NewSession(".")
	s.RunProgram(`data rates;
  input g $ ev @@;
  datalines;
a 14
b 6
;
data r1;
  set rates;
  x = _n_;
run;
data r2;
  set rates end=last;
  if last then output;
run;`)
	r1 := s.Dataset("R1")
	if r1 == nil || len(r1.Rows) != 2 {
		t.Fatal("no R1")
	}
	i := r1.Index("X")
	if r1.Rows[0][i].Num != 1 || r1.Rows[1][i].Num != 2 {
		t.Errorf("_N_ = %v, %v; want 1, 2", r1.Rows[0][i], r1.Rows[1][i])
	}
	r2 := s.Dataset("R2")
	if r2 == nil || len(r2.Rows) != 1 {
		t.Fatalf("END= if-last-output rows = %d, want 1", len(r2.Rows))
	}
	if r2.Rows[0][r2.Index("EV")].Num != 6 {
		t.Error("END= kept the wrong row")
	}
}

// TestDiagnosticEval pins the diagnostic-accuracy pipeline (weighted
// 2x2, BETAINV Clopper-Pearson intervals) against scipy.stats.beta.ppf:
// sens=0.76 CI (0.6183, 0.8694), spec=0.9467 CI (0.8976, 0.9767).
func TestDiagnosticEval(t *testing.T) {
	s := NewSession(".")
	s.Out.SelectNone = true
	s.RunProgram(`data dx;
  length gold $ 7 test $ 4;
  input gold $ test $ count;
  datalines;
disease pos 38
disease neg 12
healthy pos 8
healthy neg 142
;
data eval;
  set dx end=last;
  retain tp fn fp tn 0;
  if gold = 'disease' and test = 'pos' then tp = count;
  else if gold = 'disease' and test = 'neg' then fn = count;
  else if gold = 'healthy' and test = 'pos' then fp = count;
  else if gold = 'healthy' and test = 'neg' then tn = count;
  if last then do;
    n1 = tp + fn;
    n0 = fp + tn;
    sens = tp / n1;
    spec = tn / n0;
    sens_lo = betainv(0.025, tp, n1 - tp + 1);
    sens_hi = betainv(0.975, tp + 1, n1 - tp);
    spec_lo = betainv(0.025, tn, n0 - tn + 1);
    spec_hi = betainv(0.975, tn + 1, n0 - tn);
    keep sens spec sens_lo sens_hi spec_lo spec_hi;
    output;
  end;
run;`)
	d := s.Dataset("EVAL")
	if d == nil || len(d.Rows) != 1 {
		t.Fatal("no EVAL output")
	}
	get := func(col string) float64 {
		i := d.Index(col)
		if i < 0 {
			t.Fatalf("no column %s", col)
		}
		return d.Rows[0][i].Num
	}
	uvClose(t, "sens", get("SENS"), 0.76, 1e-12)
	uvClose(t, "spec", get("SPEC"), 0.9466666666666667, 1e-12)
	uvClose(t, "sens lo", get("SENS_LO"), 0.6183092518959619, 1e-9)
	uvClose(t, "sens hi", get("SENS_HI"), 0.8693900838025427, 1e-9)
	uvClose(t, "spec lo", get("SPEC_LO"), 0.8976181039766762, 1e-9)
	uvClose(t, "spec hi", get("SPEC_HI"), 0.97669618980353, 1e-9)
}
