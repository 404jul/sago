package sas

import (
	"math"
	"testing"
)

// TestPhregRats pins PROC PHREG against the published SAS/STAT PHREG
// chapter getting-started example (Rats data, BRESLOW): estimate
// -0.59590, SE 0.34840, tests LR 2.8784 / Score 3.0001 / Wald 2.9254,
// -2 LOG L 204.317 / 201.438.
func TestPhregRats(t *testing.T) {
	s := NewSession(".")
	s.RunProgram(`
data rats;
  input days status group @@;
  datalines;
143 1 0 164 1 0 188 1 0 188 1 0 190 1 0 192 1 0
206 1 0 209 1 0 213 1 0 216 1 0 220 1 0 227 1 0
230 1 0 234 1 0 246 1 0 265 1 0 304 1 0 216 0 0
244 0 0 142 1 1 156 1 1 163 1 1 198 1 1 205 1 1
232 1 1 232 1 1 233 1 1 233 1 1 233 1 1 233 1 1
239 1 1 240 1 1 261 1 1 280 1 1 280 1 1 296 1 1
296 1 1 323 1 1 204 0 1 344 0 1
;
ods output FitStatistics=_fs_ GlobalTests=_gt_ ParameterEstimates=_pe_;
proc phreg data=rats;
  model days*status(0) = group;
run;
`)
	check := func(t *testing.T, name string, got, want, tol float64) {
		t.Helper()
		if math.Abs(got-want) > tol {
			t.Errorf("%s = %.6f, want %.6f (tol %g)", name, got, want, tol)
		}
	}
	fs := s.Dataset("_FS_")
	if fs == nil {
		t.Fatal("no FitStatistics capture")
	}
	check(t, "-2LL without", fs.Rows[0][1].Num, 204.317, 5e-4)
	check(t, "-2LL with", fs.Rows[0][2].Num, 201.438, 5e-4)
	gt := s.Dataset("_GT_")
	if gt == nil {
		t.Fatal("no GlobalTests capture")
	}
	check(t, "LR", gt.Rows[0][1].Num, 2.8784, 5e-4)
	check(t, "Score", gt.Rows[1][1].Num, 3.0001, 5e-4)
	check(t, "Wald", gt.Rows[2][1].Num, 2.9254, 1e-3)
	pe := s.Dataset("_PE_")
	if pe == nil {
		t.Fatal("no ParameterEstimates capture")
	}
	check(t, "estimate", pe.Rows[0][2].Num, -0.59590, 5e-6)
	check(t, "se", pe.Rows[0][3].Num, 0.34840, 5e-6)
	check(t, "hazard ratio", pe.Rows[0][6].Num, 0.551, 5e-4)
}

// TestPhregEfronTies pins the Efron ties handling against R coxph on
// tie-heavy data: coefficient 1.700273, SE 1.068178.
func TestPhregEfronTies(t *testing.T) {
	s := NewSession(".")
	s.RunProgram(`
data ties;
  input months status x @@;
  datalines;
1 1 0 2 1 1 2 0 0 3 1 1 3 0 0 4 1 1
4 0 0 5 1 1 5 0 0 6 1 1 6 0 0 7 1 1
1 1 1 2 0 0 3 0 0 4 0 0 5 1 1 6 1 1
;
ods output ParameterEstimates=_pe_;
proc phreg data=ties ties=efron;
  model months*status(0) = x;
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
	check(t, "efron estimate", pe.Rows[0][2].Num, 1.700273, 2e-5)
	check(t, "efron se", pe.Rows[0][3].Num, 1.068178, 2e-5)
}
