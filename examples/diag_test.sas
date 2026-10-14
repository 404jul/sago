/*---------------------------------------------------------------------
  Problem 8 - regression diagnostics and influence

  A calibration curve (n = 15) with two planted anomalies: the
  observation at x = 12 is an outlier in the y direction (moderate
  leverage, large residual), and the endpoint x = 15 combines high
  leverage with a large residual - the classic influential point.

  Q1. REG with P R INFLUENCE: internally/externally studentized
      residuals, Cook's D, leverage (hat diagonal), DFFITS, and the
      PRESS decomposition.
  Q2. OUTPUT OUT= capturing the same statistics as variables.

  Decision rule being illustrated: |RStudent| > 2 flags suspicious
  residuals, |DFFITS| > 2/sqrt(p/n) = 0.730 flags influential points.

  Expected results (verified against statsmodels OLSInfluence):
    x = 12: RStudent 2.7140, Cook's D 0.349, DFFITS 1.0202;
    x = 15: leverage h 0.2417, RStudent 2.8936, DFFITS 1.6335 - both
    flagged; all other |RStudent| < 1.6, |DFFITS| < 0.75.
    PRESS (sum of squared deleted residuals) = 101.8695.
---------------------------------------------------------------------*/

title 'Problem 8: regression diagnostics';

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
         rstudent=rstud cookd=cook h=lev dffits=dffits press=delres;
run;

title2 'diagnostics table';
proc print data=diagout;
  var x y yhat resid stud rstud cook lev dffits delres;
run;

title;
