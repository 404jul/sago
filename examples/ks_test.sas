/*---------------------------------------------------------------------
  Problem 1 - Kolmogorov-Smirnov tests

  A reference lab (A) and a field lab (B) each assayed the same 30
  control samples; B's instrument is suspected of reading low. The
  three-sample part compares batch yields across three suppliers.

  Q1. Do the two labs' assay distributions differ?
      -> two-sample KS test (PROC NPAR1WAY EDF, plus the one-sided
         D+/D- statistics via the D option), with the Cramer-von Mises
         and Kuiper EDF statistics as backup.

  Q2. Do the three suppliers' yield distributions differ?
      -> k-sample KS and Cramer-von Mises statistics (Kiefer 1959).

  Q3. Is the field lab's assay distribution consistent with normality?
      -> one-sample KS test against a fitted normal
         (PROC UNIVARIATE NORMAL), as a check before using parametric
         methods on lab B's data.

  Expected results (verified against scipy.stats and the SAS/STAT
  NPAR1WAY documentation):
    Q1: D = 0.366667 (max deviation at value 6.0), KSa = 1.420094,
        Pr > KSa = 0.0354 -> reject at 0.05: lab B reads low.
        Kuiper Ka = 1.549193, Pr > Ka = 0.1416.
    Q2: KS = 0.194489, KSa = 1.010592; CMa = 0.408265.
    Q3: D = 0.024816, Pr > D > 0.150 -> normality not rejected.
---------------------------------------------------------------------*/

title 'Problem 1: Kolmogorov-Smirnov tests';

data assay;
  input lab $ value @@;
  datalines;
A 4.9 A 5.5 A 5.8 A 6.1 A 6.3 A 6.4
A 6.6 A 6.7 A 6.8 A 6.9 A 7.0 A 7.1
A 7.2 A 7.3 A 7.4 A 7.6 A 7.7 A 7.8
A 7.9 A 8.0 A 8.1 A 8.2 A 8.3 A 8.4
A 8.6 A 8.7 A 8.9 A 9.2 A 9.5 A 10.1
B 2.2 B 3.1 B 3.6 B 3.9 B 4.2 B 4.5
B 4.7 B 4.9 B 5.1 B 5.3 B 5.5 B 5.6
B 5.8 B 6.0 B 6.1 B 6.3 B 6.4 B 6.6
B 6.8 B 6.9 B 7.1 B 7.3 B 7.5 B 7.7
B 7.9 B 8.2 B 8.5 B 8.8 B 9.3 B 10.2
;

data supplier;
  input supplier $ yield @@;
  datalines;
S1 42.1 S1 43.5 S1 44.2 S1 45.0 S1 45.8 S1 46.9 S1 48.2 S1 49.1
S2 40.8 S2 41.9 S2 43.0 S2 43.8 S2 44.6 S2 45.5 S2 46.3 S2 47.4 S2 48.8
S3 39.5 S3 40.2 S3 41.1 S3 41.9 S3 42.6 S3 43.4 S3 44.1 S3 45.2 S3 46.0 S3 47.1
;

title2 'Q1: two labs - two-sample EDF tests';
proc npar1way edf data=assay;
  class lab;
  var value;
run;

title2 'Q1: one-sided Kolmogorov-Smirnov statistics (D option)';
proc npar1way d data=assay;
  class lab;
  var value;
run;

title2 'Q2: three suppliers - k-sample EDF statistics';
proc npar1way edf data=supplier;
  class supplier;
  var yield;
run;

title2 'Q3: is lab B consistent with a normal distribution?';
data labb;
  set assay;
  if lab = 'B';
run;

proc univariate normal data=labb;
  var value;
  qqplot value / normal(mu=6.2 sigma=1.8859);
run;

title;
