package sas

import "testing"

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
