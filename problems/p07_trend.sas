/* Problem 7 - Cochran-Armitage test for trend in proportions.
   Four ordered dose groups; is the adverse-event rate increasing
   with dose? Trend test uses dose values as scores (numeric table
   scores). Equivalent to the score test for slope in a logistic
   regression of the response on dose. */
data tox;
  length adverse $ 3;
  input dose adverse $ count;
  datalines;
0 no 48
0 yes 2
1 no 46
1 yes 4
5 no 42
5 yes 8
20 no 36
20 yes 14
;

proc freq data=tox;
  weight count;
  tables dose*adverse / trend nopercent norow nocol;
run;
