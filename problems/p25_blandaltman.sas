/* Problem 25 - Method comparison: Bland-Altman agreement.
   Two assays on the same 20 specimens. Bias = mean(d), agreement
   limits = mean(d) +/- 1.96 * sd(d), plus the paired t test for zero
   bias and the correlation of differences vs means (proportional
   bias check). */
data meth;
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

proc means data=ba n mean std t prt;
  var d;
run;

proc corr data=ba nosummary;
  var m;
  with d;
run;

proc print data=ba noobs;
  var ref value d m;
run;
