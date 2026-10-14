/* Problem 8 - Equivalence testing with TOST (two one-sided tests).
   Two formulations of a drug; are their mean AUCs equivalent within
   +/- 2 units? Rejection regions: H0: muA-muB <= -2 vs H0: muA-muB >= 2.
   Includes the 90% confidence interval for the difference that TOST
   inverts. */
data bio;
  length form $ 1;
  input form $ auc @@;
  datalines;
A 102.3 A 98.7 A 101.1 A 99.4 A 100.8 A 97.9 A 101.6 A 99.2
A 100.3 A 98.5 A 102.0 A 99.8
B 99.4 B 100.9 B 97.8 B 101.2 B 98.6 B 100.1 B 99.0 B 101.5
B 98.2 B 100.4 B 99.7 B 100.7
;

proc ttest data=bio TOST(-2, 2);
  class form;
  var auc;
run;
