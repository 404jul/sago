package sas

import (
	"math"
	"testing"
)

// TestRegDiagnostics pins the REG observation diagnostics (R INFLUENCE
// and OUTPUT OUT=) against statsmodels OLSInfluence values on the
// examples/diag_test.sas calibration data (n = 15, planted y-outlier at
// x = 12 and high-leverage endpoint x = 15).
func TestRegDiagnostics(t *testing.T) {
	s := NewSession(".")
	s.Out.BeginProc("")
	s.Out.SelectNone = true
	s.Out.ODSOutputs["OUTPUTSTATISTICS"] = "_os_"
	s.Out.ODSOutputs["RESIDUALSTATISTICS"] = "_rs_"
	s.RunProgram(`
data calib;
  input x y @@;
  datalines;
 1  5.1
 2  6.9
 3  8.8
 4 11.2
 5 12.9
 6 15.1
 7 16.8
 8 19.2
 9 21.1
10 22.8
11 25.0
12 33.9
13 29.2
14 30.7
15 40.6
;
proc reg data=calib;
  model y = x / p r influence;
  output out=diagout predicted=yhat residual=resid student=stud
         rstudent=rstud cookd=cook h=lev dffits=dff press=delres;
run;
`)
	s.Out.EndProc()
	// ODS OutputStatistics: Obs, DepVar, Pred, StdP, Resid, StdR, Student,
	// CookD, RStudent, H, DFFITS, Resid(hidden R flag adds none)
	os := s.Dataset("_OS_")
	if os == nil {
		t.Fatal("no OutputStatistics capture")
	}
	// statsmodels OLSInfluence ground truth
	want := map[int][6]float64{
		// x: {RStudent, CookD, H, DFFITS, Student, press resid}
		11: {2.714031, 0.349350, 0.123810, 1.020216, 2.223654, 5.476190},
		14: {2.893638, 0.851335, 0.241667, 1.633513, 2.311464, 6.137440},
	}
	idx := map[string]int{}
	for i, v := range os.Vars {
		idx[v.Name] = i
	}
	n := 0
	for _, r := range os.Rows {
		xv := r[idx["Dependent Variable"]]
		_ = xv
		// find x via the observation's predicted value mapping: rows are
		// in input order, so row 11 is x=12 and row 14 is x=15 (0-based)
		if n == 11 || n == 14 {
			w := want[n]
			if math.Abs(r[idx["RStudent"]].Num-w[0]) > 1e-5 {
				t.Errorf("row %d RStudent = %v, want %v", n, r[idx["RStudent"]].Num, w[0])
			}
			if math.Abs(r[idx["CooksD"]].Num-w[1]) > 1e-5 {
				t.Errorf("row %d Cook's D = %v, want %v", n, r[idx["CooksD"]].Num, w[1])
			}
			if math.Abs(r[idx["HatDiagonal"]].Num-w[2]) > 1e-5 {
				t.Errorf("row %d H = %v, want %v", n, r[idx["HatDiagonal"]].Num, w[2])
			}
			if math.Abs(r[idx["DFFITS"]].Num-w[3]) > 1e-5 {
				t.Errorf("row %d DFFITS = %v, want %v", n, r[idx["DFFITS"]].Num, w[3])
			}
		}
		n++
	}
	if n != 15 {
		t.Errorf("OutputStatistics rows = %d, want 15", n)
	}
	// ResidualStatistics: Sum of Residuals ~ 0, PRESS = 101.869537
	rs := s.Dataset("_RS_")
	if rs == nil {
		t.Fatal("no ResidualStatistics capture")
	}
	valIdx := rs.Index("nValue1")
	if valIdx < 0 {
		t.Fatal("no nValue1 column in ResidualStatistics")
	}
	if math.Abs(rs.Rows[0][valIdx].Num) > 1e-9 {
		t.Errorf("Sum of Residuals = %v, want 0", rs.Rows[0][valIdx].Num)
	}
	if math.Abs(rs.Rows[2][valIdx].Num-101.869537) > 1e-4 {
		t.Errorf("PRESS = %v, want 101.869537", rs.Rows[2][valIdx].Num)
	}
	// OUTPUT OUT= data set carries the renamed variables
	o := s.Dataset("DIAGOUT")
	if o == nil {
		t.Fatal("WORK.DIAGOUT not created")
	}
	for _, v := range []string{"yhat", "resid", "stud", "rstud", "cook", "lev", "dff", "delres"} {
		if o.Index(v) < 0 {
			t.Errorf("variable %s missing from WORK.DIAGOUT", v)
		}
	}
	// spot check the deleted residual at x = 12: e/(1-h) = 4.802381/0.876190
	i12 := o.Index("delres")
	if math.Abs(o.Rows[11][i12].Num-5.480978) > 1e-5 {
		t.Errorf("deleted residual at x=12 = %v, want 5.480978", o.Rows[11][i12].Num)
	}
}
