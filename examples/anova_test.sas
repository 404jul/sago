/*---------------------------------------------------------------------
  Problem 4 - one-way ANOVA and multiple comparisons

  Q1. Tensile strength of fibre from four production machines with an
      unbalanced design (n = 12/9/7/14). One-way ANOVA F test, then
      pairwise comparisons with Tukey-Kramer, Bonferroni and Scheffe
      simultaneous confidence limits (CLDIFF form, the SAS default for
      unbalanced designs), plus Levene's homogeneity-of-variance test.

  Q2. Balanced design (3 coating lines, n = 5) requesting the Tukey
      grouping LINES display.

  Expected results (verified against scipy/statsmodels):
    Q1: F = 76.0914 (p ~ 4e-16), MSE = 0.780052 on 38 df.
        Tukey qcrit = 3.79925; e.g. B - A: 3.4222 [2.3760, 4.4685] ***,
        A - D: 0.5595 [-0.3739, 1.4929] not significant; all comparisons
        involving B or C significant, A vs D not.
        Bonferroni t crit 2.783474; Scheffe sqrt(3*F) = 2.924931.
        Levene F = 0.2839 (p = 0.8367) - variances homogeneous.
    Q2: line 3 (A) separates from lines 1 and 2 (B); lines 1 and 2 share
        a letter (means 10.68 vs 11.06, Tukey gap < half-width).
---------------------------------------------------------------------*/

title 'Problem 4: one-way ANOVA and multiple comparisons';

data tensile;
  input machine $ strength @@;
  datalines;
A 52.3 A 54.1 A 53.8 A 55.0 A 52.9 A 54.6
A 53.2 A 55.3 A 54.8 A 53.5 A 54.2 A 53.9
B 56.8 B 58.2 B 57.5 B 55.9 B 57.9 B 58.6
B 56.4 B 57.2 B 58.0
C 50.1 C 51.8 C 49.6 C 52.4 C 50.9 C 51.2 C 49.9
D 53.4 D 52.8 D 54.5 D 53.1 D 52.2 D 54.0
D 53.7 D 52.5 D 54.3 D 53.6 D 52.9 D 54.8
D 53.3 D 52.6
;

data coating;
  input line $ coat @@;
  datalines;
1 10.2 1 10.8 1 10.5 1 11.0 1 10.9
2 11.1 2 10.9 2 11.3 2 10.8 2 11.2
3 12.3 3 12.9 3 12.6 3 13.1 3 12.7
;

title2 'Q1: unbalanced one-way ANOVA with Tukey, Bonferroni, Scheffe';
proc glm data=tensile;
  class machine;
  model strength = machine;
  means machine / tukey cldiff bon scheffe hovtest;
run;

title2 'Q2: balanced design with Tukey grouping lines';
proc glm data=coating;
  class line;
  model coat = line;
  means line / tukey;
run;

title;
