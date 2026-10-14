/* Problem 1 - Exact inference on a binomial proportion.
   A QC line samples n=60 assemblies; 3 are defective.
   H0: p = 0.10 (spec limit). Want the exact binomial test
   (one- and two-sided) and exact (Clopper-Pearson) vs Wald
   confidence limits, computed from a weighted frequency table. */
data qc;
  length status $ 9;
  input status $ count;
  datalines;
defective 3
ok 57
;

proc freq data=qc alpha=0.05;
  weight count;
  tables status / binomial(level='defective' p=0.1);
  exact binomial;
run;
