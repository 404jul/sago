/*---------------------------------------------------------------------
  Problem 3 - linear model inference: PROC REG

  A chemical process ran 13 batches; yield (%) is modelled against
  reactor temperature (C) and feed concentration (%). Concentration
  tracks temperature closely, so the two predictors are strongly
  collinear. Several temperature levels were replicated, which supports
  a formal lack-of-fit test in the simple model.

  Q1. Simple regression yield = temp: is temperature related to yield?
      ANOVA F test, slope t test, 95% CLB for both parameters, and a
      lack-of-fit test using the replicated batches.
  Q2. Multiple regression yield = temp conc: with collinear predictors
      the joint F test is highly significant while the individual t
      tests are not - demonstrated with the TEST statement and VIF.
  Q3. Prediction: OUTPUT OUT= with fitted values, residuals, 95%
      confidence limits for the mean (L95M/U95M) and 95% prediction
      limits for a new observation (L95/U95).

  Expected results (verified against statsmodels OLS):
    Q1: slope 0.094571 (SE 0.007027, t 13.46, p<.0001), R-Square 0.9427,
        lack-of-fit F 0.54 (p=0.7744, df 7/4) - no lack of fit.
    Q2: model F 82.4189 (p<.0001); temp t = 1.51 (p=0.1610),
        conc t = -0.11 (p=0.9175); VIF for both = 83.1.
    Q3: first fitted value 53.6893, residual 0.3107.
---------------------------------------------------------------------*/

title 'Problem 3: linear model inference';

data process;
  input temp conc yield;
  datalines;
150 1.1 54.0
150 1.2 53.5
155 1.4 53.9
160 1.5 54.6
160 1.6 55.2
165 1.8 55.1
170 1.9 55.4
175 2.1 56.0
175 2.2 55.3
180 2.3 56.6
185 2.5 57.0
190 2.6 57.4
190 2.8 57.9
;

title2 'Q1: simple regression with lack-of-fit test';
proc reg data=process;
  model yield = temp / clb lackfit;
run;

title2 'Q2: multiple regression with collinear predictors';
proc reg data=process;
  model yield = temp conc / clb vif;
  test temp, conc;
  output out=pred predicted=yhat residual=resid
         l95=lo95 u95=hi95 l95m=lo95m u95m=hi95m;
run;

title2 'Q3: fitted values and prediction limits';
proc print data=pred;
  var temp conc yield yhat resid lo95 hi95 lo95m hi95m;
run;

title;
