package sas

import (
	"strings"
	"testing"
)

// References: pandas/scipy pearsonr, spearmanr, kendalltau(method='asymptotic'),
// partial correlation via OLS residuals (numpy lstsq), SASHELP.CARS/CLASS.
func TestCRCorr(t *testing.T) {
	tb := ttTables(t, `PROC CORR DATA=sashelp.CARS; VAR MPG_CITY WEIGHT CYLINDERS; RUN;
PROC CORR DATA=sashelp.CARS; VAR MPG_CITY WEIGHT; PARTIAL HORSEPOWER ENGINESIZE; RUN;
proc corr data=sashelp.class spearman kendall; var height weight; run;
proc corr data=sashelp.class; var height weight; partial age; run;`)
	p := tb["PearsonCorr"][0]
	uvClose(t, "r mpg weight", ttCell(p, 0, "Weight"), -0.73797, 1e-5)
	uvClose(t, "r mpg cyl", ttCell(p, 0, "Cylinders"), -0.6844016161073332, 1e-10)
	uvClose(t, "p mpg cyl", ttCell(p, 0, "PCylinders"), 3.754824598040849e-60, 1e-8)
	uvClose(t, "N mpg cyl", ttCell(p, 0, "NCylinders"), 426, 0)
	pp := tb["PartialPearsonCorr"][0]
	uvClose(t, "partial r", ttCell(pp, 0, "Weight"), -0.41651064728475745, 1e-10)
	uvClose(t, "partial p", ttCell(pp, 0, "PWeight"), 2.637759493201387e-19, 1e-6)
	ss := tb["SimpleStats"][1]
	uvClose(t, "partial var", ttCell(ss, 0, "PartialVar"), 12.680765381777078, 1e-10)
	sp := tb["SpearmanCorr"][0]
	uvClose(t, "spearman", ttCell(sp, 0, "Weight"), 0.8557611049465413, 1e-10)
	uvClose(t, "spearman p", ttCell(sp, 0, "PWeight"), 2.9825951631700135e-06, 1e-8)
	kd := tb["KendallCorr"][0]
	uvClose(t, "tau-b", ttCell(kd, 0, "Weight"), 0.7142983684930493, 1e-10)
	uvClose(t, "tau-b p", ttCell(kd, 0, "PWeight"), 2.5127836528434916e-05, 1e-8)
	pc := tb["PartialPearsonCorr"][1]
	uvClose(t, "partial age", ttCell(pc, 0, "Weight"), 0.7046717891175065, 1e-10)
	uvClose(t, "partial age p", ttCell(pc, 0, "PWeight"), 0.0010932571580663563, 1e-8)
}

// TestFisherZFuncs pins the DATA-step Fisher-z pipeline used for
// correlation CIs (problems/p09_fisherz.sas): ATANH/TANH/PROBIT/
// PROBNORM on the CORR OUTP= value. Reference: scipy.stats.pearsonr
// + arctanh/tanh transforms (n=24, r=-0.987663070349961).
func TestFisherZFuncs(t *testing.T) {
	s := NewSession(".")
	s.Out.SelectNone = true
	s.RunProgram(`data stress;
  input anxiety performance @@;
  datalines;
32 60 28 68 35 55 25 74 40 51 30 66 33 58 27 71
38 54 29 69 31 63 26 73 36 57 34 59 24 75 39 52
37 56 23 77 41 49 30 65 28 70 35 56 33 61 26 72
;
proc corr data=stress nosummary noprint outp=pc;
  var anxiety;
  with performance;
run;
data fisherz;
  set pc;
  if _type_ = 'CORR';
  r = anxiety;
  n = 24;
  z = atanh(r);
  se = 1 / sqrt(n - 3);
  zq = probit(0.975);
  ci_lo = tanh(z - zq*se);
  ci_hi = tanh(z + zq*se);
  z0 = atanh(0.5);
  zstat = (z - z0) * sqrt(n - 3);
  p2 = 2 * probnorm(-abs(zstat));
run;
proc print data=fisherz noobs; run;`)
	d := s.Dataset("FISHERZ")
	if d == nil || len(d.Rows) != 1 {
		t.Fatal("no FISHERZ output")
	}
	get := func(col string) float64 {
		i := d.Index(col)
		if i < 0 {
			t.Fatalf("no column %s", col)
		}
		return d.Rows[0][i].Num
	}
	uvClose(t, "r", get("R"), -0.987663070349961, 1e-12)
	uvClose(t, "z", get("Z"), -2.541058858210045, 1e-12)
	uvClose(t, "ci_lo", get("CI_LO"), -0.9947367367119085, 1e-12)
	uvClose(t, "ci_hi", get("CI_HI"), -0.9712197755457361, 1e-12)
	uvClose(t, "zstat", get("ZSTAT"), -14.161831549200734, 1e-9)
	uvClose(t, "p2", get("P2"), 1.5782640901830867e-45, 1e-57)
}

// TestBlandAltman pins the method-comparison pipeline (merge + rename,
// difference/mean variables, MEANS t test, CORR of differences vs
// means) against scipy references.
func TestBlandAltman(t *testing.T) {
	s := NewSession(".")
	s.Out.SelectNone = true
	s.RunProgram(`data meth;
  input new @@;
  datalines;
5.2 6.1 7.8 4.9 8.3 6.6 5.5 7.2 9.1 6.0
5.8 7.5 8.8 5.1 6.9 7.9 5.4 8.5 6.3 7.0
;
data ref;
  input ref @@;
  datalines;
5.4 6.3 7.4 5.1 8.9 6.2 5.9 6.8 9.4 6.4
6.1 7.0 9.2 5.6 7.3 7.5 5.9 8.0 6.8 7.4
;
data ba;
  merge ref meth (rename=(new=value));
  d = value - ref;
  m = (value + ref) / 2;
run;
ods output Summary=_sm_;
proc means data=ba n mean std t prt;
  var d;
run;`)
	sm := s.Dataset("_SM_")
	if sm == nil || len(sm.Rows) != 1 {
		t.Fatal("no Summary output")
	}
	find := func(suffix string) int {
		for i, v := range sm.Vars {
			if strings.HasSuffix(v.Name, suffix) {
				return i
			}
		}
		t.Fatalf("Summary column %s missing; have %v", suffix, func() []string {
			ns := []string{}
			for _, v := range sm.Vars {
				ns = append(ns, v.Name)
			}
			return ns
		}())
		return -1
	}
	iM, iS, iT, iP := find("_Mean"), find("_StdDev"), find("_t"), find("_Probt")
	if iM < 0 || iS < 0 || iT < 0 || iP < 0 {
		t.Fatalf("Summary columns missing: %v", sm.Vars)
	}
	uvClose(t, "mean d", sm.Rows[0][iM].Num, -0.13499999999999995, 1e-9)
	uvClose(t, "std d", sm.Rows[0][iS].Num, 0.3963982580024392, 1e-9)
	uvClose(t, "t", sm.Rows[0][iT].Num, -1.5230600582539087, 1e-9)
	uvClose(t, "p", sm.Rows[0][iP].Num, 0.14421465765181157, 1e-9)
}
