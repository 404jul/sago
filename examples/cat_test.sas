/*---------------------------------------------------------------------
  Problem 2 - categorical inference: chi-square tests and Fisher's exact

  Q1. Goodness of fit. Mendel's classic F2 pea experiment counts the
      four seed phenotypes; under independent assortment of two genes
      the null hypothesis is the 9:3:3:1 ratio. TESTP gives the null
      percentages. NOTE: PROC FREQ matches the TESTP list to the levels
      in the order they appear in the table (alphabetical here), NOT the
      data order - the classic TESTP pitfall. The levels sort as
      round_green, round_yellow, wrinkled_green, wrinkled_yellow, so the
      null percentages (18.75 56.25 6.25 18.75) are given in that order.

  Q2. Two-by-two association with sparse cells: a new treatment vs
      control with 23 patients. CHISQ gives Pearson, continuity-adjusted
      and Mantel-Haenszel chi-square plus phi; Fisher's exact test is
      reported automatically for 2x2 tables and is the trustworthy test
      here (two cells have expected counts below 5).

  Q3. Larger two-way table (3x3): disease severity vs response, testing
      general association (Pearson, likelihood ratio) and linear
      association on table scores (Mantel-Haenszel), with Cramer's V.

  Expected results (verified against scipy.stats):
    Q1: Chi-Square 0.4700 (DF 3, Pr > ChiSq 0.9254); with LRCHI also
        Likelihood Ratio Chi-Square 0.4754 - famously good fit.
    Q2: Pearson 7.3396 (p=0.0067), Continuity Adj. 5.2357 (p=0.0221),
        Fisher two-sided 0.0123; treatment and outcome are associated.
    Q3: Pearson 41.0297, LR 44.0760 (DF 4), MH 37.9639 (p<.0001),
        Cramer's V 0.4135.
---------------------------------------------------------------------*/

title 'Problem 2: categorical inference';

data peas;
  length phenotype $15;
  input phenotype $ count;
  datalines;
round_green 108
round_yellow 315
wrinkled_green 32
wrinkled_yellow 101
;

data trial;
  length treatment $8 outcome $12;
  input treatment $ outcome $ count;
  datalines;
new healed 10
new not_healed 2
control healed 3
control not_healed 8
;

data severity;
  length severity $9 response $8;
  input severity $ response $ count;
  datalines;
mild full 24
mild partial 13
mild none 3
moderate full 8
moderate partial 18
moderate none 12
severe full 2
severe partial 15
severe none 25
;

title2 'Q1: Mendel 9:3:3:1 goodness-of-fit';
proc freq data=peas;
  tables phenotype / nocum chisq(lrchi) testp=(18.75 56.25 6.25 18.75);
  weight count;
run;

title2 'Q2: treatment vs outcome (sparse 2x2)';
proc freq data=trial;
  tables treatment*outcome / chisq expected nopercent;
  weight count;
run;

title2 'Q3: severity vs response (3x3)';
proc freq data=severity;
  tables severity*response / chisq nopercent;
  weight count;
run;

title;
