/*---------------------------------------------------------------------
  Problem 12 - Savage, Siegel-Tukey and Conover scores

  Q1. Exact-docs check: the MICE data from the SAS/STAT NPAR1WAY
      chapter (Example 84.5). Savage scores (exponential order
      statistics, centered; ties averaged over consecutive positions)
      must reproduce the published output exactly:
        sums  -3.367980 / 0.095618 / 3.272362,  SD 1.634555,
        chi-square 5.5047, DF 2, p = 0.0638.
  Q2. Two-sample Gossypol subset (Dose 0 vs 0.04, n = 16/11): Savage
      must reproduce Figure 84.12: S = -1.8346 for the first class
      (.04), Z = -0.7638, one-sided Pr < Z = 0.2225, two-sided 0.4450.
  Q3. Full 5-sample Gossypol analysis: Savage one-way chi-square must
      reproduce Figure 84.5 (39.4908, DF 4, p < .0001); Conover and
      Siegel-Tukey scale analyses on the same data.
  Q4. Siegel-Tukey and Conover two-sample tests (dose 0 vs 0.10, where
      higher gossypol doses shrink weight gain and change spread).

  All numbers verified against a from-scratch Python implementation
  that reproduces the published SAS figures above to 6+ decimals.
---------------------------------------------------------------------*/

title 'Problem 12: Savage, Siegel-Tukey and Conover scores';

title2 'Q1: Mice survival (Savage)';
data mice;
  input treatment $ days @@;
  datalines;
1 1 1 1 1 3 1 3 1 4
2 3 2 4 2 4 2 4 2 15
3 4 3 4 3 10 3 10 3 26
;

proc npar1way data=mice savage;
  class treatment;
  var days;
run;

title2 'Q2: Gossypol 0 vs 0.04 (two-sample Savage)';
data g2;
  input dose $ gain @@;
  datalines;
0 228 0 229 0 218 0 216 0 224 0 208 0 235 0 229 0 233 0 219
0 224 0 220 0 232 0 200 0 208 0 232
.04 186 .04 229 .04 220 .04 208 .04 228 .04 198 .04 222 .04 273
.04 216 .04 198 .04 213
;

proc npar1way data=g2 savage;
  class dose;
  var gain;
run;

title2 'Q3: Full Gossypol (5-dose Savage, Conover, ST)';
data gossypol;
  input dose $ n @@;
  datalines;
0 16 .04 11 .07 12 .10 17 .13 11
;
data gossypol;
  set gossypol;
  do i = 1 to n;
    input gain @@;
    output;
  end;
  drop i n;
  datalines;
228 229 218 216 224 208 235 229 233 219 224 220 232 200 208 232
186 229 220 208 228 198 222 273 216 198 213
179 193 183 180 143 204 114 188 178 134 208 196
130 87 135 116 118 165 151 59 126 64 78 94 150 160 122 110 178
154 130 130 118 118 104 112 134 98 100 104
;

proc npar1way data=gossypol savage;
  class dose;
  var gain;
run;

proc npar1way data=gossypol conover;
  class dose;
  var gain;
run;

proc npar1way data=gossypol st;
  class dose;
  var gain;
run;

title2 'Q4: Dose 0 vs 0.10 (two-sample ST and Conover)';
data g4;
  set gossypol;
  if dose in ('0', '.10');
run;

proc npar1way data=g4 st;
  class dose;
  var gain;
run;

proc npar1way data=g4 conover;
  class dose;
  var gain;
run;

title;
