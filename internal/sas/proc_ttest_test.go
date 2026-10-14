package sas

import "testing"

// TestTTOST pins the TOST equivalence analysis (two one-sided tests at
// margins -2/+2) against statsmodels ttost_ind: pooled and
// Satterthwaite t statistics, p-values, overall p, and the 90%
// equivalence confidence limits.
func TestTTOST(t *testing.T) {
	s := NewSession(".")
	s.Out.SelectNone = true
	s.RunProgram(`data bio;
  length form $ 1;
  input form $ auc @@;
  datalines;
A 102.3 A 98.7 A 101.1 A 99.4 A 100.8 A 97.9 A 101.6 A 99.2
A 100.3 A 98.5 A 102.0 A 99.8
B 99.4 B 100.9 B 97.8 B 101.2 B 98.6 B 100.1 B 99.0 B 101.5
B 98.2 B 100.4 B 99.7 B 100.7
;
ods output TOSTTests=_tt_;
proc ttest data=bio TOST(-2, 2);
  class form;
  var auc;
run;`)
	tt := s.Dataset("_TT_")
	if tt == nil || len(tt.Rows) != 2 {
		t.Fatal("no TOSTTests output")
	}
	uvClose(t, "pooled tLow", tt.Rows[0][1].Num, 4.302756825039934, 1e-10)
	uvClose(t, "pooled pLow", tt.Rows[0][2].Num, 0.00014400131457688474, 1e-12)
	uvClose(t, "pooled tUp", tt.Rows[0][3].Num, -3.047148071825472, 1e-10)
	uvClose(t, "pooled overall", tt.Rows[0][5].Num, 0.00295532654477484, 1e-12)
	uvClose(t, "pooled lcl90", tt.Rows[0][6].Num, -0.5928457649772276, 1e-9)
	uvClose(t, "pooled ucl90", tt.Rows[0][7].Num, 1.2761790983105363, 1e-9)
	uvClose(t, "sat pLow", tt.Rows[1][2].Num, 0.00015258119559728421, 1e-12)
	uvClose(t, "sat overall", tt.Rows[1][5].Num, 0.0030235382135535413, 1e-12)
	uvClose(t, "sat lcl90", tt.Rows[1][6].Num, -0.5941027783342866, 1e-9)
	uvClose(t, "sat ucl90", tt.Rows[1][7].Num, 1.2774361116675954, 1e-9)
}
