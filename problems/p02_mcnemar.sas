/* Problem 2 - Agreement of two binary diagnostic tests on the SAME
   specimens (paired design). 2x2 table of test A x test B.
   McNemar's test uses only discordant pairs; kappa measures agreement
   beyond chance. Data as a weighted table. */
data diag;
  length testA $ 4 testB $ 4;
  input testA $ testB $ count;
  datalines;
pos pos 30
pos neg 18
neg pos 4
neg neg 35
;

proc freq data=diag;
  weight count;
  tables testA*testB / agree;
  exact mcnemar;
run;
