package sas

import (
	"math"
	"testing"
)

// TestGenmodPoisson pins PROC GENMOD Poisson regression against
// R glm(family=poisson) on the same data (Dobson counts with a
// treatment indicator).
func TestGenmodPoisson(t *testing.T) {
	s := NewSession(".")
	s.RunProgram(`
data dobson;
  input counts treat @@;
  datalines;
18 0 17 0 15 0 20 0 10 0 20 0 25 0 13 0 12 0
21 1 20 1 18 1 23 1 13 1 23 1 28 1 16 1 15 1
;
ods output GOF=_gof_ ParameterEstimates=_pe_;
proc genmod data=dobson;
  model counts = treat / dist=poisson link=log;
run;
`)
	check := func(t *testing.T, name string, got, want, tol float64) {
		t.Helper()
		if math.Abs(got-want) > tol {
			t.Errorf("%s = %.6f, want %.6f (tol %g)", name, got, want, tol)
		}
	}
	gof := s.Dataset("_GOF_")
	if gof == nil {
		t.Fatal("no GOF capture")
	}
	check(t, "deviance", gof.Rows[0][2].Num, 19.5165, 2e-3)
	check(t, "deviance DF", gof.Rows[0][1].Num, 16, 1e-9)
	check(t, "pearson", gof.Rows[2][2].Num, 19.5092, 2e-3)
	check(t, "full logL", gof.Rows[5][2].Num, -52.1747, 5e-3)
	check(t, "AIC", gof.Rows[6][2].Num, 108.3493, 5e-3)
	pe := s.Dataset("_PE_")
	if pe == nil {
		t.Fatal("no ParameterEstimates capture")
	}
	check(t, "intercept", pe.Rows[0][2].Num, 2.813411, 5e-4)
	check(t, "intercept se", pe.Rows[0][3].Num, 0.08164965, 5e-4)
	check(t, "treat", pe.Rows[1][2].Num, 0.1655144, 5e-4)
	check(t, "treat se", pe.Rows[1][3].Num, 0.1109792, 5e-4)
}

// TestGenmodOffset pins the exposure-offset rate model against
// R glm(claims ~ group + offset(log(hours)), poisson).
func TestGenmodOffset(t *testing.T) {
	s := NewSession(".")
	s.RunProgram(`
data ins;
  input claims ln hours group @@;
  datalines;
 2 4.6052 100 0
 3 5.0106 150 0
 5 5.3471 210 0
 7 5.5607 260 0
11 5.7991 330 0
18 6.0162 410 0
28 6.2538 520 0
41 6.4770 650 0
 1 4.4998  90 1
 2 4.9416 140 1
 3 5.3230 205 1
 5 5.5413 255 1
 9 5.7838 325 1
14 6.0039 405 1
22 6.2442 515 1
33 6.4693 645 1
;
ods output ParameterEstimates=_pe_;
proc genmod data=ins;
  model claims = group / dist=poisson link=log offset=ln;
run;
`)
	pe := s.Dataset("_PE_")
	if pe == nil {
		t.Fatal("no ParameterEstimates capture")
	}
	check := func(t *testing.T, name string, got, want, tol float64) {
		t.Helper()
		if math.Abs(got-want) > tol {
			t.Errorf("%s = %.6f, want %.6f (tol %g)", name, got, want, tol)
		}
	}
	check(t, "intercept", pe.Rows[0][2].Num, -3.129807, 2e-3)
	check(t, "group", pe.Rows[1][2].Num, -0.2371013, 2e-3)
	check(t, "group se", pe.Rows[1][3].Num, 0.1411791, 2e-3)
}
