package sas

import (
	"math"
	"testing"
)

// TestGLMTukeyKramer pins the Tukey-Kramer multiple-comparison machinery
// against scipy.stats.studentized_range / statsmodels pairwise_tukeyhsd
// for the unbalanced one-way design in examples/anova_test.sas
// (machines A-D, n = 12/9/7/14).
func TestGLMTukeyKramer(t *testing.T) {
	// studentized range quantile: qcrit(0.95, k=4, df=38) = 3.799250
	q := lmQTukey(0.95, 1, 4, 38)
	if math.Abs(q-3.799250) > 1e-4 {
		t.Errorf("lmQTukey = %.6f, want 3.799250", q)
	}
	if p := lmPTukey(q, 1, 4, 38); math.Abs(p-0.95) > 1e-6 {
		t.Errorf("lmPTukey round trip = %.8f, want 0.95", p)
	}

	s := NewSession(".")
	s.Out.BeginProc("")
	s.Out.SelectNone = true
	s.Out.ODSOutputs["CLDIFFS"] = "_cld_"
	s.RunProgram(`
data tensile;
  input machine $ strength @@;
  datalines;
A 52.3 A 54.1 A 53.8 A 55.0 A 52.9 A 54.6
A 53.2 A 55.3 A 54.8 A 53.5 A 54.2 A 53.9
B 56.8 B 58.2 B 57.5 B 55.9 B 57.9 B 58.6
B 56.4 B 57.2 B 58.0
C 50.1 C 51.8 C 49.6 C 52.4 C 50.9 C 51.2 C 49.9
D 53.4 D 52.8 D 54.5 D 53.1 D 52.2 D 54.0
D 53.7 D 52.5 D 54.3 D 53.6 D 52.9 D 54.8
D 53.3 D 52.6
;
proc glm data=tensile;
  class machine;
  model strength = machine;
  means machine / tukey cldiff;
run;
`)
	s.Out.EndProc()
	ds := s.Dataset("_CLD_")
	if ds == nil {
		t.Fatal("no CLDiffs capture")
	}
	means := map[string]float64{"A": 53.96666666666667, "B": 57.38888888888889, "C": 50.84285714285714, "D": 53.40714285714286}
	ns := map[string]int{"A": 12, "B": 9, "C": 7, "D": 14}
	mse := 29.641984127 / 38
	row := func(cmp string) []Value {
		for _, r := range ds.Rows {
			if len(r) >= 5 && r[0].Str == cmp {
				return r
			}
		}
		return nil
	}
	// statsmodels: B - A meandiff 3.4222, CI [2.376, 4.4685], reject
	r := row("B - A")
	if r == nil {
		t.Fatal("missing B - A comparison")
	}
	d := means["B"] - means["A"]
	h := q / math.Sqrt2 * math.Sqrt(mse*(1.0/float64(ns["B"])+1.0/float64(ns["A"])))
	if math.Abs(r[1].Num-d) > 1e-6 || math.Abs(r[2].Num-(d-h)) > 1e-6 || math.Abs(r[3].Num-(d+h)) > 1e-6 {
		t.Errorf("B - A = [%v, %v, %v], want %.6f [%.6f, %.6f]", r[1].Num, r[2].Num, r[3].Num, d, d-h, d+h)
	}
	if r[4].Str != "***" {
		t.Errorf("B - A should be significant, got %q", r[4].Str)
	}
	// A - D is not significant: statsmodels CI [-1.4929, 0.3739]
	r = row("A - D")
	if r == nil {
		t.Fatal("missing A - D comparison")
	}
	d = means["A"] - means["D"]
	h = q / math.Sqrt2 * math.Sqrt(mse*(1.0/float64(ns["A"])+1.0/float64(ns["D"])))
	if math.Abs(r[1].Num-d) > 1e-6 || math.Abs(r[2].Num-(d-h)) > 1e-6 || math.Abs(r[3].Num-(d+h)) > 1e-6 {
		t.Errorf("A - D = [%v, %v, %v], want %.6f [%.6f, %.6f]", r[1].Num, r[2].Num, r[3].Num, d, d-h, d+h)
	}
	if r[4].Str != "" {
		t.Errorf("A - D should not be significant, got %q", r[4].Str)
	}
	// 12 ordered pairwise comparisons total
	if len(ds.Rows) != 12 {
		t.Errorf("expected 12 comparisons, got %d", len(ds.Rows))
	}
}

// TestGLMDunnett pins Dunnett's multiple-comparison-vs-control critical
// value and simultaneous confidence limits (balanced one-way, 3 vs
// control, df=20). The critical value comes from the deterministic
// equicorrelated max-|t| quadrature: d(3,20,.05) = 2.5403496 (the k=1
// case collapses to the exact t quantile to 13 digits; scipy.stats.
// dunnett's randomized QMC agrees to ~6e-4).
func TestGLMDunnett(t *testing.T) {
	s := NewSession(".")
	s.Out.SelectNone = true
	s.RunProgram(`data yield;
  length trt $ 5;
  input trt $ yield @@;
  datalines;
ctrl 42.1 ctrl 39.6 ctrl 41.3 ctrl 43.2 ctrl 40.8 ctrl 41.9
trtA 45.6 trtA 44.1 trtA 46.3 trtA 47.0 trtA 45.2 trtA 46.8
trtB 40.2 trtB 41.5 trtB 39.8 trtB 42.1 trtB 40.9 trtB 41.6
trtC 48.5 trtC 47.2 trtC 49.1 trtC 50.3 trtC 48.0 trtC 47.8
;
ods output CLDiffsInfo=_ci_ CLDiffs=_cd_;
proc glm data=yield;
  class trt;
  model yield = trt;
  means trt / dunnett('ctrl');
run;`)
	ci := s.Dataset("_CI_")
	if ci == nil || len(ci.Rows) != 5 {
		t.Fatal("no CLDiffsInfo output")
	}
	uvClose(t, "crit", ci.Rows[3][2].Num, 2.5403496099416524, 1e-9)
	uvClose(t, "msd", ci.Rows[4][2].Num, 2.5403496099416524*math.Sqrt(1.1729166666666662/3), 1e-9)
	cd := s.Dataset("_CD_")
	if cd == nil || len(cd.Rows) != 3 {
		t.Fatalf("no CLDiffs output (%v rows)", len(cd.Rows))
	}
	// rows sorted by mean desc: trtC - ctrl, trtA - ctrl, trtB - ctrl
	uvClose(t, "diff C", cd.Rows[0][1].Num, 7.0, 1e-12)
	uvClose(t, "lo C", cd.Rows[0][2].Num, 7.0-2.5403496099416524*math.Sqrt(1.1729166666666662/3), 1e-9)
	uvClose(t, "hi C", cd.Rows[0][3].Num, 7.0+2.5403496099416524*math.Sqrt(1.1729166666666662/3), 1e-9)
	uvClose(t, "diff A", cd.Rows[1][1].Num, 4.35, 1e-12)
	uvClose(t, "diff B", cd.Rows[2][1].Num, -0.46666666666666856, 1e-9)
	if cd.Rows[0][4].Str != "***" || cd.Rows[1][4].Str != "***" || cd.Rows[2][4].Str == "***" {
		t.Errorf("significance stars wrong: %v %v %v", cd.Rows[0][4], cd.Rows[1][4], cd.Rows[2][4])
	}
}

// TestGLMBartlett pins Bartlett's homogeneity-of-variance test against
// scipy.stats.bartlett (3 groups, unequal n).
func TestGLMBartlett(t *testing.T) {
	s := NewSession(".")
	s.Out.SelectNone = true
	s.RunProgram(`data batches;
  length line $ 5;
  input line $ yield @@;
  datalines;
line1 51.2 line1 49.8 line1 50.6 line1 50.9 line1 49.5
line1 51.4 line1 50.2 line1 49.9 line1 50.8
line2 52.6 line2 47.9 line2 50.3 line2 54.1 line2 48.8
line2 51.7 line2 49.4 line2 53.2 line2 47.2 line2 52.5
line2 50.8
line3 53.8 line3 46.5 line3 51.2 line3 55.4 line3 45.9
line3 52.6 line3 48.1 line3 54.7
;
ods output HOVFTest=_bt_;
proc glm data=batches;
  class line;
  model yield = line;
  means line / hovtest=bartlett;
run;`)
	bt := s.Dataset("_BT_")
	if bt == nil || len(bt.Rows) != 1 {
		t.Fatal("no Bartlett output")
	}
	uvClose(t, "chi2", bt.Rows[0][2].Num, 16.188818003400165, 1e-9)
	uvClose(t, "p", bt.Rows[0][3].Num, 0.0003052409779425303, 1e-12)
}

// TestGLMWelch pins Welch's variance-weighted one-way ANOVA against
// statsmodels anova_oneway(use_var='unequal', welch_correction=True):
// F=34.7166 with Satterthwaite denominator df=11.0598.
func TestGLMWelch(t *testing.T) {
	s := NewSession(".")
	s.Out.SelectNone = true
	s.RunProgram(`data labs;
  length lab $ 5;
  input lab $ result @@;
  datalines;
east 10.2 east 9.8 east 10.5 east 9.9 east 10.1 east 10.3
east 9.7 east 10.4
west 13.8 west 10.9 west 14.5 west 11.4 west 13.2 west 12.8
west 11.1 west 14.1 west 10.5 west 13.5
south 18.9 south 13.6 south 19.4 south 14.2 south 18.1
south 13.9 south 19.0 south 14.8
;
ods output WelchTest=_wt_;
proc glm data=labs;
  class lab;
  model result = lab;
  means lab / welch;
run;`)
	wt := s.Dataset("_WT_")
	if wt == nil || len(wt.Rows) != 2 {
		t.Fatal("no WelchTest output")
	}
	uvClose(t, "F", wt.Rows[0][2].Num, 34.716589915757474, 1e-9)
	uvClose(t, "p", wt.Rows[0][3].Num, 1.7106392077351043e-05, 1e-14)
	uvClose(t, "df denom", wt.Rows[1][1].Num, 11.059834604732904, 1e-9)
}

// TestDsSumStmt pins the sum statement as a THEN action (valid SAS,
// previously rejected by sago): if cond then acc + x;
func TestDsSumStmt(t *testing.T) {
	s := NewSession(".")
	s.RunProgram(`data t;
  sm = 0;
  do i = 1 to 5;
    if i > 3 then sm + i;
  end;
run;`)
	d := s.Dataset("T")
	if d == nil || len(d.Rows) == 0 {
		t.Fatal("no T output")
	}
	i := d.Index("sm")
	if d.Rows[len(d.Rows)-1][i].Num != 9 {
		t.Errorf("sm = %v, want 9", d.Rows[len(d.Rows)-1][i])
	}
}

// TestEsd pins the generalized ESD outlier scan (Rosner critical
// values via TINV) implemented in pure DATA step
// (problems/p14_esd.sas); references computed with scipy.stats.t.ppf.
func TestEsd(t *testing.T) {
	s := NewSession(".")
	s.Out.SelectNone = true
	s.RunProgram(`data chem;
  input y @@;
  datalines;
5.29 5.31 5.33 5.35 5.32 5.34 5.30 5.28 5.36 5.33
5.31 5.34 5.29 5.32 5.35 5.30 7.95 8.12
;
%let N = 18;
%let R = 3;
%let alpha = 0.05;
proc sort data=chem; by y; run;
data esd;
  array yy(&N) _temporary_;
  array mask(&N) 8;
  if _n_ = 1 then do;
    do i = 1 to &N;
      set chem point=i nobs=nobs;
      yy(i) = y;
      mask(i) = 1;
    end;
  end;
  nleft = &N;
  do step = 1 to &R;
    sm = 0;
    do i = 1 to &N;
      if mask(i) then sm + yy(i);
    end;
    mean = sm / nleft;
    ss = 0;
    do i = 1 to &N;
      if mask(i) then ss + (yy(i)-mean)**2;
    end;
    sd = sqrt(ss / (nleft-1));
    gmax = 0;
    imax = 0;
    do i = 1 to &N;
      if mask(i) then do;
        g = abs(yy(i)-mean) / sd;
        if g > gmax then do; gmax = g; imax = i; end;
      end;
    end;
    p = 1 - &alpha / (2 * nleft);
    tcrit = tinv(p, nleft - 2);
    lam = (nleft * tcrit) / sqrt((nleft - 2 + tcrit**2) * (nleft + 1));
    outlier_val = yy(imax);
    keepflag = (gmax > lam);
    output;
    mask(imax) = 0;
    nleft + (-1);
  end;
  stop;
run;`)
	d := s.Dataset("ESD")
	if d == nil || len(d.Rows) != 3 {
		t.Fatal("no ESD output")
	}
	cell := func(r int, col string) float64 {
		i := d.Index(col)
		if i < 0 {
			t.Fatalf("no column %s", col)
		}
		return d.Rows[r][i].Num
	}
	uvClose(t, "esd1", cell(0, "gmax"), 2.8430293849, 1e-8)
	uvClose(t, "lam1", cell(0, "lam"), 2.7326933632, 1e-8)
	uvClose(t, "esd2", cell(1, "gmax"), 3.8779498538, 1e-8)
	uvClose(t, "lam2", cell(1, "lam"), 2.7052811787, 1e-8)
	uvClose(t, "esd3", cell(2, "gmax"), 1.6514456477, 1e-8)
	uvClose(t, "lam3", cell(2, "lam"), 2.6757061437, 1e-8)
	if cell(0, "keepflag") != 1 || cell(1, "keepflag") != 1 || cell(2, "keepflag") != 0 {
		t.Error("keepflag sequence wrong (want 2 outliers)")
	}
}

// TestGLMContrast pins the one-way CONTRAST statement (orthogonal
// polynomial decomposition) against the closed form
// SS = (sum c_i*ybar_i)^2 / sum c_i^2/n_i, cross-checked with a
// polynomial-coded regression in statsmodels (t^2 == F).
func TestGLMContrast(t *testing.T) {
	s := NewSession(".")
	s.Out.SelectNone = true
	s.RunProgram(`data dose;
  input dose resp @@;
  datalines;
1 12.1 1 11.6 1 12.8 1 11.9 1 12.4 1 12.2 1 11.7 1 12.6
2 15.3 2 14.8 2 16.1 2 15.0 2 15.6 2 15.2 2 14.9 2 15.8
3 16.9 3 16.2 3 17.5 3 16.6 3 17.1 3 16.8 3 16.4 3 17.3
4 16.4 4 15.7 4 17.0 4 16.1 4 16.6 4 16.3 4 15.9 4 16.8
;
ods output Contrasts=_ct_;
proc glm data=dose;
  class dose;
  model resp = dose;
  contrast 'linear'    dose -3 -1 1 3;
  contrast 'quadratic' dose 1 -1 -1 1;
  contrast 'cubic'     dose -1 3 -3 1;
run;`)
	ct := s.Dataset("_CT_")
	if ct == nil || len(ct.Rows) != 3 {
		t.Fatal("no Contrasts output")
	}
	uvClose(t, "linear SS", ct.Rows[0][3].Num, 79.24225000000004, 1e-9)
	uvClose(t, "linear F", ct.Rows[0][5].Num, 403.59854479308746, 1e-7)
	uvClose(t, "linear p", ct.Rows[0][6].Num, 3.6069760942863806e-18, 1e-30)
	uvClose(t, "quad SS", ct.Rows[1][3].Num, 27.011249999999958, 1e-9)
	uvClose(t, "quad F", ct.Rows[1][5].Num, 137.5743519781715, 1e-7)
	uvClose(t, "cubic SS", ct.Rows[2][3].Num, 0.0490000000000004, 1e-9)
	uvClose(t, "cubic F", ct.Rows[2][5].Num, 0.24956798544793257, 1e-9)
	uvClose(t, "cubic p", ct.Rows[2][6].Num, 0.6212825277740431, 1e-12)
}

// TestGLMSimpleEffects pins a two-way interaction decomposition:
// overall drug*sex F and the BY-group simple effects of drug within
// each sex (statsmodels anova_lm + manual one-way F per sex).
func TestGLMSimpleEffects(t *testing.T) {
	s := NewSession(".")
	s.Out.SelectNone = true
	s.RunProgram(`data trial;
  length drug $ 1 sex $ 1;
  input drug $ sex $ resp @@;
  datalines;
A F 21.4 A F 19.8 A F 22.6 A F 20.9 A F 21.8 A F 20.5 A F 22.1 A F 21.0
B F 24.6 B F 23.1 B F 25.2 B F 24.0 B F 23.8 B F 25.5 B F 24.4 B F 23.5
C F 26.1 C F 24.9 C F 26.8 C F 25.6 C F 26.4 C F 25.2 C F 27.0 C F 25.9
A M 20.2 A M 21.5 A M 19.6 A M 20.8 A M 22.0 A M 19.9 A M 21.2 A M 20.5
B M 21.9 B M 20.6 B M 22.5 B M 21.1 B M 22.8 B M 20.3 B M 21.6 B M 22.2
C M 21.4 C M 22.7 C M 20.8 C M 22.1 C M 20.5 C M 23.0 C M 21.7 C M 20.9
;
proc sort data=trial; by sex; run;
ods output ModelANOVA=_ma_;
proc glm data=trial;
  class drug sex;
  model resp = drug|sex;
run;
ods output ModelANOVA=_sb_;
proc glm data=trial;
  by sex;
  class drug;
  model resp = drug;
run;`)
	ma := s.Dataset("_MA_")
	if ma == nil {
		t.Fatal("no ModelANOVA")
	}
	// columns: Dependent, HypothesisType, Source, DF, SS, MS, FValue, Prob
	var Fint float64
	for _, r := range ma.Rows {
		if len(r) > 6 && r[2].Str == "drug*sex" && r[1].Num == 3 {
			Fint = r[6].Num
		}
	}
	uvClose(t, "interaction F", Fint, 19.9696, 1e-4)
	sb := s.Dataset("_SB_")
	if sb == nil || len(sb.Rows) < 4 {
		t.Fatal("no BY-group ModelANOVA rows")
	}
	iSrc, iHyp, iF := sb.Index("Source"), sb.Index("HypothesisType"), sb.Index("FValue")
	var fF, fM float64
	nby := 0
	for _, r := range sb.Rows {
		if r[iSrc].Str == "drug" && r[iHyp].Num == 3 {
			if r[iF].Num > 50 {
				fF = r[iF].Num
				nby++
			} else if r[iF].Num > 1 {
				fM = r[iF].Num
				nby++
			}
		}
	}
	if nby < 2 {
		t.Fatalf("expected 2 BY simple-effect rows, found %d", nby)
	}
	uvClose(t, "F within females", fF, 67.13394495412835, 1e-4)
	uvClose(t, "F within males", fM, 2.925058004640379, 1e-4)
}

// TestGLMReshape pins the wide->long ARRAY transposition (output in a
// DO loop) and the subject-block repeated-measures ANOVA
// (statsmodels anova_lm typ=3 on the long data).
func TestGLMReshape(t *testing.T) {
	s := NewSession(".")
	s.Out.SelectNone = true
	s.RunProgram(`data wide;
  input subj $ t1 t2 t3 @@;
  datalines;
s01 4.2 3.8 3.1 s02 5.5 5.0 4.6 s03 3.9 3.7 3.5
s04 6.1 5.4 5.5 s05 4.8 4.1 3.7 s06 5.2 5.1 4.4
s07 4.5 3.9 3.9 s08 5.8 5.6 4.8
;
data long;
  set wide;
  array ts{3} t1-t3;
  do time = 1 to 3;
    score = ts{time};
    output;
  end;
  keep subj time score;
run;
ods output ModelANOVA=_ma_;
proc glm data=long;
  class subj time;
  model score = subj time;
run;`)
	lg := s.Dataset("LONG")
	if lg == nil || len(lg.Rows) != 24 {
		t.Fatalf("LONG rows = %d, want 24", len(lg.Rows))
	}
	ma := s.Dataset("_MA_")
	if ma == nil {
		t.Fatal("no ModelANOVA")
	}
	iSrc, iHyp, iSS, iF := ma.Index("Source"), ma.Index("HypothesisType"), ma.Index("SS"), ma.Index("FValue")
	var stS, tmS, stF, tmF float64
	for _, r := range ma.Rows {
		if r[iHyp].Num != 3 {
			continue
		}
		switch r[iSrc].Str {
		case "subj":
			stS, stF = r[iSS].Num, r[iF].Num
		case "time":
			tmS, tmF = r[iSS].Num, r[iF].Num
		}
	}
	uvClose(t, "subj SS", stS, 12.252916666666667, 1e-9)
	uvClose(t, "subj F", stF, 44.488654, 1e-4)
	uvClose(t, "time SS", tmS, 2.6425, 1e-9)
	uvClose(t, "time F", tmF, 33.580938, 1e-4)
}
