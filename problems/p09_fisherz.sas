/* Problem 9 - Fisher z inference for a correlation coefficient.
   Pearson r for anxiety vs performance (n=28). From the CORR OUTP=
   data set: Fisher z transform, 95% CI for rho (tanh back-transform),
   and the Wald test of H0: rho = 0.5. */
data stress;
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
  r = anxiety;              /* the correlation value lives in the VAR column */
  n = 24;
  z = atanh(r);
  se = 1 / sqrt(n - 3);
  zq = probit(0.975);
  ci_lo = tanh(z - zq*se);
  ci_hi = tanh(z + zq*se);
  z0 = atanh(0.5);
  zstat = (z - z0) * sqrt(n - 3);
  p2 = 2 * probnorm(-abs(zstat));
  keep r z ci_lo ci_hi zstat p2;
run;

proc print data=fisherz noobs; run;
