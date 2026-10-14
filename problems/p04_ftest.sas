/* Problem 4 - Comparing two variances (F test).
   Two filling machines; is the fill volume of machine A more
   consistent than machine B? Two-sided F test of H0: s2A = s2B and
   the exact 95% CI for the variance ratio via F quantiles, built
   with UNIVARIATE OUTPUT OUT= and a MERGE. Levene's test (GLM)
   as a robust cross-check. */
data fills;
  length machine $ 1;
  input machine $ ml @@;
  datalines;
A 500.8 A 501.2 A 499.6 A 500.4 A 500.9 A 499.1 A 501.7 A 500.2
A 499.9 A 500.6 A 501.1 A 499.4 A 500.7 A 499.7 A 501.4
B 502.1 B 497.8 B 500.5 B 503.2 B 498.4 B 501.6 B 496.9 B 504.0
B 499.3 B 502.8 B 497.2 B 500.1 B 503.7 B 498.8 B 496.5 B 501.9
B 504.4 B 497.6 B 500.3 B 503.0 B 498.1
;

proc sort data=fills; by machine; run;

proc univariate data=fills noprint;
  by machine;
  var ml;
  output out=stats n=n var=var mean=mean;
run;

data ftest;
  merge stats(where=(machine='A') rename=(var=va n=nA mean=meanA))
        stats(where=(machine='B') rename=(var=vb n=nB mean=meanB));
  f = va / vb;
  df1 = nA - 1;
  df2 = nB - 1;
  p = 2 * min(probf(f, df1, df2), 1 - probf(f, df1, df2));
  lo = f / finv(0.975, df1, df2);
  hi = f / finv(0.025, df1, df2);
run;

proc print data=ftest noobs; run;

proc glm data=fills;
  class machine;
  model ml = machine;
  means machine / hovtest;
run;
