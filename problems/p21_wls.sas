/* Problem 21 - Weighted least squares.
   Calibration points whose measurement variance grows with x;
   weights are the inverse variances (w = 1/x). WLS coefficients and
   their standard errors differ from OLS and are efficient here. */
data het;
  input x y w @@;
  datalines;
1  4.95 1.00 1  5.31 1.00 1  4.72 1.00
2  8.08 0.50 2  7.86 0.50 2  8.31 0.50
4  14.02 0.25 4  13.79 0.25 4  14.45 0.25
8  26.31 0.125 8  25.78 0.125 8  26.85 0.125
;

proc reg data=het;
  weight w;
  model y = x / clb;
run;
