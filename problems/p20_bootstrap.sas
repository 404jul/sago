/* Problem 20 - Bootstrap percentile confidence interval for a median.
   Resample n=30 observations with replacement 1000 times
   (SURVEYSELECT URS, fixed seed), compute each replicate's median,
   and read the 2.5/50/97.5 percentiles of the bootstrap distribution. */
data sample;
  input x @@;
  datalines;
2.31 0.55 4.72 1.03 0.28 8.94 1.77 0.66 3.40 0.91
12.06 2.05 0.44 1.58 5.21 0.37 2.84 1.12 0.73 6.65
1.95 0.52 3.09 0.86 2.42 0.61 1.31 4.08 0.97 1.70
;

proc surveyselect data=sample out=boot seed=20261014
  method=urs sampsize=30 reps=1000 noprint;
run;

data bootexp;
  set boot;
  do i = 1 to numberhits;
    output;
  end;
  drop i numberhits expectedhits;
run;

proc sort data=bootexp; by replicate; run;

proc means data=bootexp noprint;
  by replicate;
  var x;
  output out=bmed (keep=replicate median) median=median;
run;

proc univariate data=bmed noprint;
  var median;
  output out=bci pctlpts=2.5 50 97.5 pctlpre=pm pctlname=l med u;
run;

proc print data=bci noobs; run;
