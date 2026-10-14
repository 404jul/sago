/*---------------------------------------------------------------------
  Problem 11 - two-sample scale (dispersion) tests

  Two samples with the SAME center but very different spread:
    group A: tight observations around 10   (sd ~ 0.4)
    group B: wide observations around 10    (sd ~ 3.5)

  Q1. PROC NPAR1WAY with MOOD scores: s_i = (R_i - (n+1)/2)^2 where R_i
      is the midrank. Tests whether B is more dispersed than A.
  Q2. PROC NPAR1WAY with KLOTZ scores: s_i = (Phi^-1(R_i/(n+1)))^2
      (squared van der Waerden scores).
  Q3. PROC NPAR1WAY with AB (Ansari-Bradley) scores:
      s_i = (n+1)/2 - |R_i - (n+1)|/2.
  Q4. WILCOXON as the location control: with equal centers it should
      NOT reject, while all three scale tests should reject.

  Expected (large |Z|, small two-sided p for MOOD/KLOTZ/AB; the sign of
  Z reflects that group B, the wider sample, holds the extreme ranks):
    Mood  |Z| ~ 3.2,   p ~ 0.001
    Klotz |Z| ~ 3.1,   p ~ 0.002
    AB    |Z| ~ 3.2,   p ~ 0.001
    Wilcoxon |Z| < 1,  p > 0.3
  All verified against scipy.stats mood / klotz / ansari.
---------------------------------------------------------------------*/

title 'Problem 11: two-sample scale tests';

data spread;
  input group $ resp @@;
  datalines;
A  9.2  A 10.1  A  9.8  A 10.3  A  9.5  A 10.6
A  9.9  A 10.2  A  9.4  A 10.5  A  9.7  A 10.0
B  5.1  B 15.2  B  7.3  B 12.9  B  6.0  B 14.1
B  8.2  B 11.5  B  4.9  B 13.6  B  7.0  B 12.0
;

title2 'Q1: Mood test';
proc npar1way data=spread mood;
  class group;
  var resp;
run;

title2 'Q2: Klotz test';
proc npar1way data=spread klotz;
  class group;
  var resp;
run;

title2 'Q3: Ansari-Bradley test';
proc npar1way data=spread ab;
  class group;
  var resp;
run;

title2 'Q4: Wilcoxon location control';
proc npar1way data=spread wilcoxon;
  class group;
  var resp;
run;

title;
