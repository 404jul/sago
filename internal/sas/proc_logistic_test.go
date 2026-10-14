package sas

import "testing"

// TestLogistic pins PROC LOGISTIC against statsmodels Logit (ML fit,
// Newton-Raphson with step halving): parameters, SEs, Wald tests,
// AIC/SC, global LR/Score/Wald tests, odds ratios with Wald limits,
// and rank association (c, Somers' D). DESCENDING models P(chd=1);
// without it sago models P(lowest level), flipping coefficient signs.
func TestLogistic(t *testing.T) {
	s := NewSession(".")
	s.Out.SelectNone = true
	s.RunProgram(`data chd;
  input chd age ldl @@;
  datalines;
1 64 189 0 53 171 0 39 158 0 67 166 1 57 183 1 50 240 1 56 259
1 62 208 1 55 242 0 48 184 0 53 125 1 72 172 0 49 131 1 65 166
0 53 212 1 63 225 1 59 161 1 43 206 1 68 170 1 62 186 1 70 169
1 66 255 1 62 124 0 45 191 1 68 214 1 57 200 0 59 153 0 42 253
1 65 224 1 65 220 1 67 189 1 53 179 1 67 198 0 71 170 1 63 170
0 56 207 0 60 127 1 45 263 0 42 257 1 66 161 0 43 138 1 63 233
1 64 135 1 42 201 0 42 146
;
ods output ParameterEstimates=_pe_ FitStatistics=_fs_ GlobalTests=_gt_
           OddsRatios=_or_ Association=_as_ ResponseProfile=_rp_;
proc logistic data=chd descending;
  model chd = age ldl;
run;`)
	cell := func(name string, r int, col string) float64 {
		d := s.Dataset(name)
		if d == nil {
			t.Fatalf("no %s output", name)
		}
		i := d.Index(col)
		if i < 0 {
			t.Fatalf("no column %s in %s", col, name)
		}
		return d.Rows[r][i].Num
	}
	pe := "_PE_"
	uvClose(t, "b0", cell(pe, 0, "Estimate"), -13.11333126761254, 1e-8)
	uvClose(t, "b1", cell(pe, 1, "Estimate"), 0.15459960086922223, 1e-10)
	uvClose(t, "b2", cell(pe, 2, "Estimate"), 0.026714650812597718, 1e-12)
	uvClose(t, "se0", cell(pe, 0, "StdErr"), 4.223240271852228, 1e-8)
	uvClose(t, "se1", cell(pe, 1, "StdErr"), 0.050670205335861467, 1e-10)
	uvClose(t, "se2", cell(pe, 2, "StdErr"), 0.011145291632908236, 1e-10)
	uvClose(t, "p2", cell(pe, 2, "ProbChiSq"), 0.016532406037763504, 1e-10)
	uvClose(t, "aic", cell("_FS_", 0, "InterceptCovariates"), 46.812014073725955, 1e-8)
	uvClose(t, "sc", cell("_FS_", 1, "InterceptCovariates"), 52.232001543036915, 1e-8)
	uvClose(t, "-2ll", cell("_FS_", 2, "InterceptCovariates"), 40.812014073725955, 1e-8)
	uvClose(t, "lr", cell("_GT_", 0, "ChiSq"), 17.761612758678567, 1e-8)
	uvClose(t, "lr p", cell("_GT_", 0, "ProbChiSq"), 0.00013903200780839296, 1e-12)
	uvClose(t, "score", cell("_GT_", 1, "ChiSq"), 15.718368680433311, 1e-8)
	uvClose(t, "wald", cell("_GT_", 2, "ChiSq"), 15.054524811042825, 1e-8)
	uvClose(t, "or age", cell("_OR_", 0, "Estimate"), 1.1671905254591108, 1e-10)
	uvClose(t, "or age lo", cell("_OR_", 0, "LowerCL"), 1.0568447554720122, 1e-10)
	uvClose(t, "or age hi", cell("_OR_", 0, "UpperCL"), 1.2890575608837314, 1e-10)
	uvClose(t, "or ldl", cell("_OR_", 1, "Estimate"), 1.027074686018109, 1e-10)
	uvClose(t, "c", cell("_AS_", 7, "nValue1"), 0.8491379310344828, 1e-10)
	uvClose(t, "somers", cell("_AS_", 4, "nValue1"), 0.6982758620689655, 1e-10)
	uvClose(t, "pairs", cell("_AS_", 3, "nValue1"), 464, 1e-9)
	rp := s.Dataset("_RP_")
	if rp == nil || len(rp.Rows) != 2 {
		t.Fatal("no ResponseProfile output")
	}
	if rp.Rows[0][0].Num != 1 || rp.Rows[0][1].Num != 1 {
		t.Errorf("descending: Ordered Value 1 should be chd=1, got %v/%v", rp.Rows[0][0], rp.Rows[0][1])
	}

	s2 := NewSession(".")
	s2.Out.SelectNone = true
	s2.RunProgram(`data chd;
  input chd age ldl @@;
  datalines;
1 64 189 0 53 171 0 39 158 0 67 166 1 57 183 1 50 240 1 56 259
1 62 208 1 55 242 0 48 184 0 53 125 1 72 172 0 49 131 1 65 166
0 53 212 1 63 225 1 59 161 1 43 206 1 68 170 1 62 186 1 70 169
1 66 255 1 62 124 0 45 191 1 68 214 1 57 200 0 59 153 0 42 253
1 65 224 1 65 220 1 67 189 1 53 179 1 67 198 0 71 170 1 63 170
0 56 207 0 60 127 1 45 263 0 42 257 1 66 161 0 43 138 1 63 233
1 64 135 1 42 201 0 42 146
;
ods output ParameterEstimates=_pe2_;
proc logistic data=chd;
  model chd = age ldl;
run;`)
	pe2 := s2.Dataset("_PE2_")
	if pe2 == nil {
		t.Fatal("no default-direction output")
	}
	i := pe2.Index("Estimate")
	uvClose(t, "flip intercept", pe2.Rows[0][i].Num, 13.11333126761254, 1e-8)
	uvClose(t, "flip age", pe2.Rows[1][i].Num, -0.15459960086922223, 1e-10)
	uvClose(t, "flip ldl", pe2.Rows[2][i].Num, -0.026714650812597718, 1e-12)
}

// TestLogisticClass pins CLASS predictors with PARAM=REF coding
// (REF='B'): reference-coded dummies for A and C plus a continuous
// dose, against statsmodels Logit with Treatment(reference='B').
func TestLogisticClass(t *testing.T) {
	s := NewSession(".")
	s.Out.SelectNone = true
	s.RunProgram(`data trial;
  input resp trt $ dose @@;
  datalines;
1 A 6.3 1 A 9.0 1 A 7.8 1 A 2.3 1 A 3.0 1 A 8.7 0 A 0.1 1 A 8.2 0 A 8.0 1 A 4.7 0 A 3.0 1 A 2.8 0 A 2.5 0 A 4.5 1 A 5.0 0 A 5.5 0 A 10.0 0 A 7.9 1 A 6.2 1 A 9.9
1 B 2.2 0 B 1.6 1 B 6.1 1 B 0.4 0 B 0.4 0 B 5.1 0 B 4.7 1 B 9.2 1 B 6.3 1 B 5.1 1 B 5.0 0 B 2.5 0 B 0.1 0 B 1.9 1 B 6.9 0 B 2.0 1 B 3.7 0 B 0.0 1 B 8.3 1 B 1.5
0 C 2.7 1 C 8.8 1 C 5.1 1 C 8.5 0 C 6.4 1 C 7.4 0 C 0.9 0 C 5.4 1 C 5.1 1 C 8.7 0 C 3.6 0 C 6.0 1 C 0.6 0 C 3.9 1 C 3.2 0 C 1.5 0 C 8.2 1 C 3.8 1 C 9.8 0 C 5.9
;
ods output ParameterEstimates=_pe_ OddsRatios=_or_;
proc logistic data=trial descending;
  class trt(ref='B') / param=ref;
  model resp = trt dose;
run;`)
	pe := s.Dataset("_PE_")
	if pe == nil || len(pe.Rows) != 4 {
		t.Fatal("no ParameterEstimates")
	}
	cellP := func(r, c int) float64 { return pe.Rows[r][c].Num }
	uvClose(t, "b0", cellP(0, 2), -0.7524383388631448, 1e-9)
	uvClose(t, "bA", cellP(1, 2), -0.35675781024249864, 1e-9)
	uvClose(t, "bC", cellP(2, 2), -0.6803348879086123, 1e-9)
	uvClose(t, "bdose", cellP(3, 2), 0.2712296612830339, 1e-9)
	uvClose(t, "seA", cellP(1, 3), 0.7169852289114169, 1e-9)
	uvClose(t, "p dose", cellP(3, 5), 0.0121, 1e-4)
	or := s.Dataset("_OR_")
	if or == nil || len(or.Rows) != 3 {
		t.Fatal("no OddsRatios")
	}
	uvClose(t, "or A", or.Rows[0][1].Num, 0.6999419959906857, 1e-9)
	uvClose(t, "or C", or.Rows[1][1].Num, 0.5064473608659033, 1e-9)
	uvClose(t, "or dose", or.Rows[2][1].Num, 1.3115762540834563, 1e-9)
}

// TestLogisticInteraction pins bar-notation interaction terms
// (model resp = trt|marker -> trt + marker + trt*marker) against
// statsmodels Logit with an explicit product column.
func TestLogisticInteraction(t *testing.T) {
	s := NewSession(".")
	s.Out.SelectNone = true
	s.RunProgram(`data study;
  input resp trt marker @@;
  datalines;
0 1 7.8 0 1 7.2 0 0 5.0 0 0 5.3 1 0 5.7 0 0 0.9 0 0 0.7 0 1 7.1 1 0 6.6 1 0 3.6
0 0 1.8 0 1 0.7 1 0 6.1 0 1 2.5 0 0 4.9 1 0 3.2 0 1 3.5 0 1 3.4 1 1 2.4 0 1 4.9
0 0 4.2 1 1 1.2 0 0 6.0 0 1 6.6 0 1 4.3 1 0 5.6 1 0 5.5 0 1 4.8 0 1 2.3 0 1 3.2
1 0 2.8 1 0 3.0 1 1 3.5 1 0 2.7 1 1 0.6 1 0 2.2 0 1 5.3 0 1 3.0 0 1 6.0 1 0 7.1
1 1 3.0 0 1 3.2 0 1 1.9 0 0 1.0 1 0 4.4 0 1 6.9 1 0 6.2 0 0 3.4 1 1 3.8 0 1 3.7
0 1 4.3 0 1 0.2 1 0 4.6 1 0 1.9 1 0 1.3 0 1 0.7 1 0 7.9 0 1 6.9 0 1 5.0 0 0 0.2
1 1 3.0 0 0 5.1 0 1 0.9 0 0 4.8 0 0 1.8 1 0 7.5 1 0 2.7 1 0 1.0 1 1 0.3 0 1 1.4
1 0 5.7 1 1 1.2 1 0 3.4 1 0 2.7 0 1 7.4 0 1 7.2 0 1 3.0 0 1 2.8 1 0 4.2 0 0 7.4
;
ods output ParameterEstimates=_pe_ GlobalTests=_gt_;
proc logistic data=study descending;
  model resp = trt|marker;
run;`)
	pe := s.Dataset("_PE_")
	if pe == nil || len(pe.Rows) != 4 {
		t.Fatal("no ParameterEstimates")
	}
	iE, iSE := pe.Index("Estimate"), pe.Index("StdErr")
	uvClose(t, "b0", pe.Rows[0][iE].Num, -0.23816807540898433, 1e-9)
	uvClose(t, "btrt", pe.Rows[1][iE].Num, 0.6264296640492144, 1e-9)
	uvClose(t, "bmarker", pe.Rows[2][iE].Num, 0.19200511646438398, 1e-9)
	uvClose(t, "bint", pe.Rows[3][iE].Num, -0.7299994694054018, 1e-9)
	uvClose(t, "seint", pe.Rows[3][iSE].Num, 0.2951117129841631, 1e-9)
	gt := s.Dataset("_GT_")
	if gt == nil {
		t.Fatal("no GlobalTests")
	}
	iChi := gt.Index("ChiSq")
	uvClose(t, "LR", gt.Rows[0][iChi].Num, 21.60875323062882, 1e-7)
}
