/*---------------------------------------------------------------------
  Problem 14 - Fligner-Policello test (robust medians, unequal shapes)

  The FP test compares medians assuming each class distribution is
  symmetric, WITHOUT requiring equal variances (unlike Wilcoxon).
  Placements: P(X_i) = #{Y_j < X_i} + 0.5 #{Y_j = X_i};
  z = (sumP(Y) - sumP(X)) / (2 sqrt(Vx + Vy + Pbar_x Pbar_y)),
  where X is the reference class (the larger sample by default).

  Q1. Equal medians (0), unequal spreads (sd 1 vs 4): FP should NOT
      reject, demonstrating robustness to the variance difference that
      can distort the Wilcoxon test.
  Q2. Shifted medians (0 vs 2) with unequal spreads: FP should reject.
  Q3. Ties present (integer data): placements use half credit; both
      classes same size (reference class = second in sorted order).

  All values verified against a from-scratch Python implementation of
  the documented formulas (SAS/STAT NPAR1WAY, Fligner-Policello Test).
---------------------------------------------------------------------*/

title 'Problem 14: Fligner-Policello test';

title2 'Q1: Equal medians, unequal spreads';
data fp1;
  input group $ y @@;
  datalines;
A -1.2 A 0.4 A 1.0 A -0.5 A -1.3 A -0.1 A 0.5 A 1.1
A -1.2 A -1.3 A 0.7 A 0.5
B 6.1 B 3.9 B -0.4 B -1.1 B 10.3 B -1.2 B -3.1 B -4.6
B 0.4 B 0.6 B -1.4 B -0.2 B 1.1 B 2.8 B 4.4 B 0.8
B -7.2 B 2.8 B -5.7 B -0.7 B -5.9 B 0.0 B -3.1 B 0.9
B 13.2 B -0.2 B 3.4 B -5.3
;

proc npar1way data=fp1 fp wilcoxon;
  class group;
  var y;
run;

title2 'Q2: Shifted medians with unequal spreads';
data fp2;
  input group $ y @@;
  datalines;
A -0.8  A  0.3  A -0.2  A  0.9  A -1.1  A  0.6  A -0.5  A  0.1
B  1.2  B  6.4  B -2.7  B  4.1  B  8.3  B -0.4  B  3.5  B  2.8
B  5.2  B -1.6  B  4.9  B  1.7
;

proc npar1way data=fp2 fp;
  class group;
  var y;
run;

title2 'Q3: Ties and equal sample sizes';
data fp3;
  input group $ y @@;
  datalines;
A 2 A 4 A 4 A 6 A 3 A 5
B 5 B 7 B 6 B 8 B 5 B 9
;

proc npar1way data=fp3 fp;
  class group;
  var y;
run;

title;
