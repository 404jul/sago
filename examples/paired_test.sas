/*---------------------------------------------------------------------
  Problem 5 - paired designs: before/after inference

  Twelve subjects complete a six-week relaxation programme; the
  measurement is a 0-60 anxiety score taken at baseline and at the end.
  Subject 4's score is unchanged (a zero difference, which the
  nonparametric tests drop), and the +0.2/-0.2 pair of small differences
  creates tied absolute ranks.

  Q1. Paired t test: is the mean change different from zero?
      PROC TTEST with the PAIRED statement (after - baseline).
  Q2. Distribution-free confirmation: the difference variable is run
      through PROC UNIVARIATE with MU0=0, producing the sign test (M)
      and the Wilcoxon signed-rank test (S, exact for n <= 20).

  Expected results (verified against scipy.stats):
    Q1: mean difference -1.166667 (SD 1.452480), t = -2.7825, df 11,
        p = 0.0178, 95% CI [-2.0895, -0.2438].
    Q2: Sign M = -2.5, Pr >= |M| = 0.2266 (3 positives vs 8 negatives,
        one zero dropped); Signed Rank S = -23.5 (T+ = 9.5 with tied
        midranks), Pr >= |S| = 0.0342 - the exact conditional
        distribution given the tie pattern (70 of 2048 sign flips);
        scipy's tie-less exact table would give 0.0322.
---------------------------------------------------------------------*/

title 'Problem 5: paired designs';

data anxiety;
  input subject baseline after;
  diff = after - baseline;
  datalines;
 1 52.1 49.8
 2 48.6 48.9
 3 55.0 51.7
 4 50.4 50.4
 5 47.9 45.2
 6 53.3 53.5
 7 49.5 47.0
 8 51.2 51.0
 9 50.8 52.0
10 49.1 48.2
11 53.7 52.1
12 48.3 46.1
;

title2 'Q1: paired t test';
proc ttest data=anxiety;
  paired after*baseline;
run;

title2 'Q2: sign and signed-rank tests on the differences';
proc univariate data=anxiety mu0=0 loccount;
  var diff;
run;

title;
