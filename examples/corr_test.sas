/*---------------------------------------------------------------------
  Problem 7 - correlation: Pearson, Spearman, Kendall, and partial

  Pharmacology-style data: 24 subjects receive an escalating dose; the
  response is a saturating (monotone but nonlinear) function of dose,
  and lab temperature drifts with dose. Because the dose-response curve
  flattens, the Pearson correlation understates the strong monotone
  association that Spearman and Kendall capture. Temperature is then
  partialed out: PROC CORR with a PARTIAL variable computes partial
  Pearson and partial Spearman correlations.

  Q1. Full Pearson / Spearman / Kendall tau-b matrix of dose, effect,
      temperature.
  Q2. Partial Pearson and partial Spearman of dose and effect
      controlling for temperature (df = n - 3 for the t p-values).

  Expected results (verified against scipy.stats):
    Pearson(effect,dose) = 0.9320 but Spearman = 0.9991 and Kendall
    tau-b = 0.9928 - the monotone association is near-perfect while the
    linear one is diluted by the saturating curve.
    Partial Pearson(effect,dose | temp) = 0.7707 vs raw 0.9320;
    partial Spearman(effect,dose | temp) = 0.9900.
---------------------------------------------------------------------*/

title 'Problem 7: correlation analysis';

data dose;
  input dose effect temp @@;
  datalines;
 0.5  6.1 18.4
 0.8  7.9 17.0
 1.0  8.9 19.6
 1.2 11.1 18.3
 1.5 12.6 20.1
 1.8 14.4 18.9
 2.0 15.7 20.9
 2.4 18.0 19.5
 2.8 20.3 21.6
 3.2 22.1 20.3
 3.8 24.9 22.9
 4.5 27.2 21.4
 5.2 31.7 24.0
 6.0 31.6 22.6
 7.0 34.7 25.0
 8.5 38.0 23.8
10.0 41.6 26.1
12.0 44.9 24.7
15.0 49.1 27.2
18.0 52.6 25.9
22.0 56.7 28.2
27.0 60.7 26.9
33.0 64.8 29.3
40.0 68.9 28.1
;

title2 'Q1: full correlation matrix (three measures)';
proc corr data=dose pearson spearman kendall;
  var dose effect temp;
run;

title2 'Q2: partial correlations controlling for temperature';
proc corr data=dose pearson spearman;
  var dose effect;
  partial temp;
run;

title;
