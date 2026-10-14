package sas

import (
	"math"
	"testing"
)

// FREQ statistics pinned against scipy.stats reference values
// (chi2_contingency, Mann-Whitney/Kruskal in proc_npar1way_test.go).

func fqTestCarsTypeDriveTrain(t *testing.T) ([][]float64, []*fqDim) {
	t.Helper()
	d := loadSashelp("CARS")
	dims, counts, _ := fqTabulate(d, []string{"Type", "DriveTrain"}, -1, false)
	tbl := make([][]float64, len(dims[0].Levels))
	for i := range tbl {
		tbl[i] = make([]float64, len(dims[1].Levels))
		for j := range tbl[i] {
			tbl[i][j] = counts[fqKey(i)+","+fqKey(j)]
		}
	}
	return tbl, dims
}

func TestFqCarsTypeDriveTrain(t *testing.T) {
	tbl, dims := fqTestCarsTypeDriveTrain(t)
	stats, fisher := fqChiSq(tbl, fqScores(dims[0]), fqScores(dims[1]))
	// scipy: chi2_contingency
	want := map[string][2]float64{
		"Chi-Square":                  {185.6709835236498, ChiSqSF(185.6709835236498, 10)},
		"Likelihood Ratio Chi-Square": {187.55952746813148, ChiSqSF(187.55952746813148, 10)},
		"Mantel-Haenszel Chi-Square":  {15.715697693753226, ChiSqSF(15.715697693753226, 1)},
		"Phi Coefficient":             {math.Sqrt(185.6709835236498 / 428), math.NaN()},
		"Contingency Coefficient":     {math.Sqrt(185.6709835236498 / (185.6709835236498 + 428)), math.NaN()},
		"Cramer's V":                  {math.Sqrt(185.6709835236498 / (428 * 2)), math.NaN()},
	}
	seen := map[string]bool{}
	for _, st := range stats {
		w, ok := want[st.name]
		if !ok {
			t.Fatalf("unexpected statistic %q", st.name)
		}
		seen[st.name] = true
		if math.Abs(st.value-w[0]) > 1e-9*max(1, math.Abs(w[0])) {
			t.Errorf("%s value = %.12g, want %.12g", st.name, st.value, w[0])
		}
		if !math.IsNaN(w[1]) && math.Abs(st.prob-w[1]) > 1e-12 {
			t.Errorf("%s prob = %.12g, want %.12g", st.name, st.prob, w[1])
		}
	}
	for name := range want {
		if !seen[name] {
			t.Errorf("statistic %q missing", name)
		}
	}
	if fisher != nil {
		t.Errorf("Fisher's exact should only be reported for 2x2 (got rows)")
	}
	// scipy fisher via simulation is not exact for RxC; check 2x2 below
}

func TestFqFisher22(t *testing.T) {
	// SAS documentation example: 2x2 table [[23,15],[19,3]]
	tbl := [][]float64{{23, 15}, {19, 3}}
	f := fqFisher22(tbl)
	if f == nil {
		t.Fatal("nil fisher")
	}
	// scipy.stats.fisher_exact([[23,15],[19,3]]) and hypergeom pmf sums
	for name, want := range map[string]float64{
		"prob":  0.025756778145045494,
		"left":  0.03203138542600327,
		"right": 0.9937253927190424,
		"two":   0.0438825609395726,
	} {
		got := map[string]float64{"prob": f.prob, "left": f.left, "right": f.right, "two": f.two}[name]
		if math.Abs(got-want) > 1e-12 {
			t.Errorf("fisher %s = %.15g, want %.15g", name, got, want)
		}
	}
}

func TestFqOneWayGOF(t *testing.T) {
	// iris species counts 50/50/50 with testp 20/50/30 -> X2 = 22.2222
	dims := []*fqDim{
		{Name: "Species", Char: true, Levels: []Value{StrV("Setosa"), StrV("Versicolor"), StrV("Virginica")}},
	}
	counts := map[string]float64{fqKey(0): 50, fqKey(1): 50, fqKey(2): 50}
	fqOneWayCapture = nil
	sess := NewSession(".")
	sess.Out.BeginProc("")
	sess.Out.SelectNone = true
	sess.Out.ODSOutputs["ONEWAYCHISQ"] = "_chisq_"
	_, err := fqOneWay(sess, "Species", dims[0], counts, 0, ParseOpts(mustToks("/ chisq testp=(20,50,30)")), false, false, 0.05)
	if err != nil {
		t.Fatal(err)
	}
	sess.Out.EndProc()
	ds := sess.Dataset("_chisq_")
	if ds == nil {
		t.Fatal("no ODS OUTPUT OneWayChiSq captured")
	}
	// SAS vertical layout: rows Chi-Square / DF / Pr > ChiSq (label in col 0)
	if ds.Rows[0][0].Str != "Chi-Square" || ds.Rows[1][0].Str != "DF" || ds.Rows[2][0].Str != "Pr > ChiSq" {
		t.Fatalf("unexpected OneWayChiSq rows: %v", ds.Rows)
	}
	got := ds.Rows[0][1].Num
	if math.Abs(got-22.22222222222222) > 1e-9 {
		t.Errorf("GOF X2 = %.12g, want 22.22222222222222", got)
	}
}

func mustToks(s string) []Token {
	toks, _ := Tokenize(s, 1)
	return toks
}

var fqOneWayCapture *Dataset // reserved hook (unused)

// TestFqOneWayGOFHairColor pins the one-way goodness-of-fit test against
// SAS/STAT 9.2 PROC FREQ Example 35.3 (children's hair color): Region 1
// Chi-Square 7.7602 (Pr > ChiSq 0.1008), Region 2 Chi-Square 21.3824
// (Pr > ChiSq 0.0003). Levels sort alphabetically, so the TESTP list is
// the documented (30 12 30 25 3) permuted to (3 25 30 30 12).
func TestFqOneWayGOFHairColor(t *testing.T) {
	dm := &fqDim{Name: "Hair", Char: true, Levels: []Value{StrV("black"), StrV("dark"), StrV("fair"), StrV("medium"), StrV("red")}}
	for _, tc := range []struct {
		counts map[string]float64
		x2, p  float64
	}{
		{map[string]float64{"0": 3, "1": 65, "2": 76, "3": 83, "4": 19}, 7.7602, 0.1008},
		{map[string]float64{"0": 19, "1": 117, "2": 152, "3": 134, "4": 94}, 21.3824, 0.0003},
	} {
		sess := NewSession(".")
		sess.Out.BeginProc("")
		sess.Out.SelectNone = true
		sess.Out.ODSOutputs["ONEWAYCHISQ"] = "_chisq_"
		if _, err := fqOneWay(sess, "Hair", dm, tc.counts, 0, ParseOpts(mustToks("/ nocum chisq testp=(3 25 30 30 12)")), false, false, 0.05); err != nil {
			t.Fatal(err)
		}
		sess.Out.EndProc()
		ds := sess.Dataset("_chisq_")
		if ds == nil {
			t.Fatal("no ODS OUTPUT OneWayChiSq captured")
		}
		if math.Abs(ds.Rows[0][1].Num-tc.x2) > 5e-5 {
			t.Errorf("Chi-Square = %.6f, want %.4f", ds.Rows[0][1].Num, tc.x2)
		}
		if ds.Rows[1][1].Num != 4 {
			t.Errorf("DF = %v, want 4", ds.Rows[1][1].Num)
		}
		if math.Abs(ds.Rows[2][1].Num-tc.p) > 5e-5 {
			t.Errorf("Pr > ChiSq = %.6f, want %.4f", ds.Rows[2][1].Num, tc.p)
		}
	}
}

// TestFqBinomial pins the one-way BINOMIAL statistics (n=60, x=3 defectives,
// H0 p=0.1, ALPHA=.05, weighted FREQ) against scipy.stats:
// beta.ppf / norm.cdf / binom.cdf references.
func TestFqBinomial(t *testing.T) {
	s := NewSession(".")
	s.Out.SelectNone = true
	s.RunProgram(`data qc;
  length status $ 9;
  input status $ count;
  datalines;
defective 3
ok 57
;
ods output BinomialProportion=_bp_ PropTest=_pt_ BinomialTest=_et_;
proc freq data=qc alpha=0.05;
  weight count;
  tables status / binomial(level='defective' p=0.1);
  exact binomial;
run;`)
	bp := s.Dataset("_BP_")
	if bp == nil {
		t.Fatal("no BinomialProportion output")
	}
	cell := func(d *Dataset, r int, col string) float64 {
		i := d.Index(col)
		if i < 0 {
			t.Fatalf("no column %s in %s", col, d.Name)
		}
		return d.Rows[r][i].Num
	}
	uvClose(t, "proportion", cell(bp, 0, "Proportion"), 0.05, 1e-15)
	uvClose(t, "ase", cell(bp, 0, "ASE"), 0.028136571693556888, 1e-15)
	uvClose(t, "wald lo", cell(bp, 0, "LowerCL"), -0.005146667167800649, 1e-15)
	uvClose(t, "wald hi", cell(bp, 0, "UpperCL"), 0.10514666716780066, 1e-15)
	uvClose(t, "cp lo", cell(bp, 0, "LowerCLExact"), 0.010432329644348225, 1e-10)
	uvClose(t, "cp hi", cell(bp, 0, "UpperCLExact"), 0.13924324672107277, 1e-10)
	pt := s.Dataset("_PT_")
	if pt == nil {
		t.Fatal("no PropTest output")
	}
	uvClose(t, "ase0", cell(pt, 0, "nValue"), 0.03872983346207417, 1e-15)
	uvClose(t, "z", cell(pt, 1, "nValue"), -1.2909944487358056, 1e-15)
	uvClose(t, "one-sided", cell(pt, 2, "nValue"), 0.09835280122947343, 1e-15)
	uvClose(t, "two-sided", cell(pt, 3, "nValue"), 0.19670560245894686, 1e-15)
	et := s.Dataset("_ET_")
	if et == nil {
		t.Fatal("no BinomialTest output")
	}
	uvClose(t, "exact one-sided", cell(et, 0, "nValue"), 0.13739856941965792, 1e-10)
	uvClose(t, "exact two-sided", cell(et, 1, "nValue"), 0.27479713883931584, 1e-10)
}

// TestFqAgree pins the AGREE statistics: McNemar's test, exact McNemar,
// simple kappa (Fleiss-Cohen-Everitt ASE). The 4x4 table is SAS's
// published Agreement Study example (SkinCondition, Figure 28.6:
// kappa 0.3449, ASE 0.0724, CL (0.2030, 0.4868), ASE0 0.0612, Z 5.6366);
// the 2x2 references are scipy.stats (binom.sf/chi2.sf/norm.sf).
func TestFqAgree(t *testing.T) {
	s := NewSession(".")
	s.Out.SelectNone = true
	s.RunProgram(`data skin;
  length derm1 $ 8 derm2 $ 8;
  input derm1 $ derm2 $ count;
  datalines;
terrible terrible 10
terrible     poor 4
terrible marginal 1
terrible    clear 0
poor     terrible 5
poor         poor 10
poor     marginal 12
poor        clear 2
marginal terrible 2
marginal     poor 4
marginal marginal 12
marginal    clear 5
clear    terrible 0
clear        poor 2
clear    marginal 6
clear       clear 13
;
data diag;
  length testA $ 4 testB $ 4;
  input testA $ testB $ count;
  datalines;
pos pos 30
pos neg 18
neg pos 4
neg neg 35
;
ods output KappaStatistics=_ks_ KappaTest=_kt_ McNemarsTest=_mt_;
proc freq data=skin order=data;
  weight count;
  tables derm1*derm2 / agree;
  test kappa;
run;
ods output KappaStatistics=_ks2_ KappaTest=_kt2_ McNemarsTest=_mt2_;
proc freq data=diag;
  weight count;
  tables testA*testB / agree;
  exact mcnemar;
run;`)
	cell := func(d *Dataset, r int, col string) float64 {
		i := d.Index(col)
		if i < 0 {
			t.Fatalf("no column %s in %s", col, d.Name)
		}
		return d.Rows[r][i].Num
	}
	ks := s.Dataset("_KS_")
	if ks == nil {
		t.Fatal("no KappaStatistics output")
	}
	uvClose(t, "skin kappa", cell(ks, 0, "Kappa"), 0.3448753462603878, 1e-12)
	uvClose(t, "skin ase", cell(ks, 0, "ASE"), 0.07239668737198354, 1e-12)
	uvClose(t, "skin cl lo", cell(ks, 0, "LowerCL"), 0.2029804464112943, 1e-12)
	uvClose(t, "skin cl hi", cell(ks, 0, "UpperCL"), 0.4867702461094813, 1e-12)
	kt := s.Dataset("_KT_")
	if kt == nil {
		t.Fatal("no KappaTest output")
	}
	uvClose(t, "skin ase0", cell(kt, 0, "nValue"), 0.06118471912810536, 1e-12)
	uvClose(t, "skin z", cell(kt, 1, "nValue"), 5.636625470786355, 1e-12)
	mt := s.Dataset("_MT2_")
	if mt == nil {
		t.Fatal("no McNemarsTest output")
	}
	uvClose(t, "p02 S", cell(mt, 0, "nValue"), 8.909090909090908, 1e-12)
	uvClose(t, "p02 pr>s", cell(mt, 2, "nValue"), 0.0028375448887923154, 1e-12)
	uvClose(t, "p02 exact", cell(mt, 3, "nValue"), 0.004343509674072266, 1e-12)
	ks2 := s.Dataset("_KS2_")
	uvClose(t, "p02 kappa", cell(ks2, 0, "Kappa"), 0.5054263565891473, 1e-12)
	uvClose(t, "p02 ase", cell(ks2, 0, "ASE"), 0.0867209906934256, 1e-12)
	uvClose(t, "p02 cl lo", cell(ks2, 0, "LowerCL"), 0.33545633812639986, 1e-12)
	uvClose(t, "p02 cl hi", cell(ks2, 0, "UpperCL"), 0.6753963750518946, 1e-12)
	kt2 := s.Dataset("_KT2_")
	uvClose(t, "p02 ase0", cell(kt2, 0, "nValue"), 0.10176295741265602, 1e-12)
	uvClose(t, "p02 z", cell(kt2, 1, "nValue"), 4.966702712261078, 1e-12)
}

// TestFqCMH pins the 3-way CMH analysis: stratum crosstabs, the
// Cochran-Mantel-Haenszel statistic, the Mantel-Haenszel common odds
// ratio with Robins-Breslow-Greenland limits, and the Woolf logit
// estimate (statsmodels StratifiedTable; levels sort alphabetically so
// row1 = ctl, col1 = event).
func TestFqCMH(t *testing.T) {
	s := NewSession(".")
	s.Out.SelectNone = true
	s.RunProgram(`data trial;
  length center $ 8 trt $ 8 resp $ 8;
  input center $ trt $ resp $ count;
  datalines;
north trt event 12
north trt nonevent 8
north ctl event 8
north ctl nonevent 12
mid trt event 10
mid trt nonevent 6
mid ctl event 6
mid ctl nonevent 9
south trt event 7
south trt nonevent 9
south ctl event 4
south ctl nonevent 12
;
ods output CMH=_cmh_ CommonRelRisk=_crr_;
proc freq data=trial;
  weight count;
  tables center*trt*resp / cmh;
run;`)
	cmh := s.Dataset("_CMH_")
	if cmh == nil || len(cmh.Rows) != 3 {
		t.Fatal("no CMH output")
	}
	uvClose(t, "cmh stat", cmh.Rows[0][2].Num, 4.27676419929838, 1e-10)
	uvClose(t, "cmh p", cmh.Rows[0][3].Num, 0.038636835749846554, 1e-12)
	crr := s.Dataset("_CRR_")
	if crr == nil || len(crr.Rows) != 2 {
		t.Fatal("no CommonRelRisk output")
	}
	uvClose(t, "MH or", crr.Rows[0][2].Num, 0.4257443237035074, 1e-12)
	uvClose(t, "MH lo", crr.Rows[0][3].Num, 0.19050786049791144, 1e-12)
	uvClose(t, "MH hi", crr.Rows[0][4].Num, 0.9514475082131534, 1e-12)
	uvClose(t, "logit or", crr.Rows[1][2].Num, 0.44816728349048035, 1e-12)
	uvClose(t, "logit lo", crr.Rows[1][3].Num, 0.20585418870690675, 1e-12)
	uvClose(t, "logit hi", crr.Rows[1][4].Num, 0.975709628513853, 1e-12)
}

// TestFqTrend pins the Cochran-Armitage trend test (R x 2, numeric
// dose scores; SAS convention: proportion = first column). Reference:
// T = sum n_i1 (r_i - rbar) / sqrt(p1(1-p1) s^2), cross-checked as the
// logistic-regression score test for slope at the null (equal to ~1e-15).
func TestFqTrend(t *testing.T) {
	s := NewSession(".")
	s.Out.SelectNone = true
	s.RunProgram(`data tox;
  length adverse $ 3;
  input dose adverse $ count;
  datalines;
0 no 48
0 yes 2
1 no 46
1 yes 4
5 no 42
5 yes 8
20 no 36
20 yes 14
;
ods output TrendTest=_tt_;
proc freq data=tox;
  weight count;
  tables dose*adverse / trend;
run;`)
	tt := s.Dataset("_TT_")
	if tt == nil || len(tt.Rows) != 3 {
		t.Fatal("no TrendTest output")
	}
	uvClose(t, "Z", tt.Rows[0][1].Num, -3.610135786019725, 1e-12)
	uvClose(t, "one-sided", tt.Rows[1][1].Num, 0.0001530183764515162, 1e-12)
	uvClose(t, "two-sided", tt.Rows[2][1].Num, 0.0003060367529030324, 1e-12)
}

// TestFqWtKappa pins weighted kappa (4x4 SkinCondition table) against
// the SAS formulas: Cicchetti-Allison (default) and Fleiss-Cohen
// (WT=FC) weights, with Fleiss-Cohen-Everitt ASEs and H0 tests.
// CA: kw=0.5081600715403529 ase=0.06550838103476854;
// FC: kw=0.6607228915662658 ase=0.061642832416999624.
func TestFqWtKappa(t *testing.T) {
	s := NewSession(".")
	s.Out.SelectNone = true
	s.RunProgram(`data skin;
  length derm1 $ 8 derm2 $ 8;
  input derm1 $ derm2 $ count;
  datalines;
terrible terrible 10
terrible     poor 4
terrible marginal 1
terrible    clear 0
poor     terrible 5
poor         poor 10
poor     marginal 12
poor        clear 2
marginal terrible 2
marginal     poor 4
marginal marginal 12
marginal    clear 5
clear    terrible 0
clear        poor 2
clear    marginal 6
clear       clear 13
;
ods output KappaStatistics=_ks_ WtkapTest=_wt_;
proc freq data=skin order=data;
  weight count;
  tables derm1*derm2 / agree;
run;
ods output KappaStatistics=_ksf_ WtkapTest=_wtf_;
proc freq data=skin order=data;
  weight count;
  tables derm1*derm2 / agree(wt=fc);
run;`)
	cell := func(name string, r int, col int) float64 {
		d := s.Dataset(name)
		if d == nil {
			t.Fatalf("no %s", name)
		}
		return d.Rows[r][col].Num
	}
	uvClose(t, "CA kw", cell("_KS_", 1, 2), 0.5081600715403529, 1e-12)
	uvClose(t, "CA ase", cell("_KS_", 1, 3), 0.06550838103476854, 1e-12)
	uvClose(t, "CA lo", cell("_KS_", 1, 4), 0.37976600402667987, 1e-12)
	uvClose(t, "CA hi", cell("_KS_", 1, 5), 0.6365541390540259, 1e-12)
	uvClose(t, "CA ase0", cell("_WT_", 0, 1), 0.07389666803458192, 1e-12)
	uvClose(t, "CA z", cell("_WT_", 1, 1), 6.87663036853767, 1e-12)
	uvClose(t, "CA p", cell("_WT_", 3, 1), 6.128485125141004e-12, 1e-24)
	uvClose(t, "FC kw", cell("_KSF_", 1, 2), 0.6607228915662658, 1e-12)
	uvClose(t, "FC ase", cell("_KSF_", 1, 3), 0.061642832416999624, 1e-12)
	uvClose(t, "FC ase0", cell("_WTF_", 0, 1), 0.10649518215348137, 1e-12)
	uvClose(t, "FC p", cell("_WTF_", 3, 1), 5.495784437256681e-10, 1e-22)
}
