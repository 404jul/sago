/*---------------------------------------------------------------------
  Problem 16 - survival analysis (PROC LIFETEST)

  Q1. Exact-docs check: the Exposed rat data from the SAS/STAT
      LIFETEST chapter getting-started example (time Days*Status(0),
      strata Treatment). The published output is
        Placebo median 235.5, quartile CIs [156,229] [206,253]
        [237,323], mean 235.156 SE 10.211
        Test of Equality over Treatment: Log-Rank 5.6485 (p 0.0175),
        Wilcoxon 5.0312 (0.0249), -2Log(LR) 0.1983 (0.6561)
  Q2. Single sample without a STRATA statement: product-limit
      estimates and summary only (no equality tests).
  Q3. Heavy censoring at late times: the KM curve retains events after
      censorings, and the equality tests remain well-defined.

  All values cross-checked against R survival 3.8.6 (survfit,
  survdiff) and the published figures.
---------------------------------------------------------------------*/

title 'Problem 16: survival analysis';

title2 'Q1: Rat survival under two treatments';
data exposed;
  input days status treatment @@;
  datalines;
179 1 1 378 0 1 256 1 1 355 1 1 262 1 1 319 1 1
256 1 1 256 1 1 255 1 1 171 1 1 224 0 1 325 1 1
225 1 1 325 1 1 287 1 1 217 1 1 319 1 1 255 1 1
264 1 1 256 1 1 237 0 0 291 1 0 156 1 0 323 1 0
270 1 0 253 1 0 257 1 0 206 1 0 242 1 0 206 1 0
157 1 0 237 1 0 249 1 0 211 1 0 180 1 0 229 1 0
226 1 0 234 1 0 268 0 0 209 1 0
;

proc lifetest data=exposed alpha=.05;
  time days*status(0);
  strata treatment;
run;

title2 'Q2: Single sample';
proc lifetest data=exposed;
  where treatment = 1;
  time days*status(0);
run;

title2 'Q3: Heavy late censoring';
data heavy;
  input group $ months*dead(0) @@;
  datalines;
A 2 1 A 4 1 A 5 1 A 7 1 A 9 1 A 10 1
B 2 1 B 3 1 B 5 1 B 8 1 B 9 1 B 12 1
A 6 0 A 11 0 A 12 0 A 13 0
B 4 0 B 10 0 B 14 0 B 15 0
;

proc lifetest data=heavy;
  time months*dead(0);
  strata group;
run;

title;
