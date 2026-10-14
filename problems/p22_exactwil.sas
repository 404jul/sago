/* Problem 22 - Exact Wilcoxon rank-sum test (small samples with ties).
   With n1=7, n2=8 the normal approximation is shaky; the exact
   permutation distribution of the rank sum (mid-ranks for ties)
   gives the reference p-values. */
data drug;
  length group $ 4;
  input group $ relief @@;
  datalines;
new 4.1 new 4.7 new 3.9 new 5.2 new 4.4 new 3.6 new 5.0
old 2.8 old 3.5 old 4.1 old 2.6 old 3.1 old 3.9 old 2.4 old 3.3
;

proc npar1way data=drug wilcoxon;
  class group;
  var relief;
  exact wilcoxon;
run;
