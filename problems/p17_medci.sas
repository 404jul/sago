/* Problem 17 - Distribution-free confidence interval for the median.
   Order-statistic method: choose the largest rank k with
   P(Bin(n,1/2) <= k-1) <= alpha/2, then the CI is (x_(k), x_(n-k+1)).
   Also reports the sign test for H0: median = 0 (from UNIVARIATE). */
data reaction;
  input t @@;
  datalines;
0.42 -0.51 0.33 -0.24 0.18 -0.62 0.55 -0.39 0.21 -0.47
0.28 -0.15 0.61 -0.33 0.09 -0.55 0.37 -0.28 0.16 -0.41
;

proc univariate data=reaction loccount mu0=0;
  var t;
run;

proc sort data=reaction; by t; run;

%let N = 20;

data medci;
  array x(&N) _temporary_;
  if _n_ = 1 then do;
    do i = 1 to &N;
      set reaction point=i nobs=nobs;
      x(i) = t;
    end;
  end;
  /* largest k with P(Bin(n,0.5) <= k-1) <= alpha/2 */
  k = 0;
  do j = 1 to &N;
    if probbnml(0.5, &N, j-1) <= 0.025 then k = j;
  end;
  lo_rank = k;
  hi_rank = &N - k + 1;
  ci_lo = x(lo_rank);
  ci_hi = x(hi_rank);
  output;
  stop;
  drop i j t;
run;

proc print data=medci noobs; run;
