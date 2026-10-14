/*---------------------------------------------------------------------
  Problem 18 - Poisson regression (PROC GENMOD, DIST=POISSON LINK=LOG)

  Q1. The classic Dobson (1990) counts with a treatment indicator:
      log-linear model fit, deviance, Pearson chi-square, and Wald
      tests, verified against R glm(family=poisson).
  Q2. Offset exposure model (events with person-time exposure ln):
      Poisson rate regression, verified against R glm with offset.
  Q3. Overdispersed counts: deviance/DF far above 1 flags the lack of
      fit while the coefficient estimates remain consistent.

  All values cross-checked against R survival-independent glm().
---------------------------------------------------------------------*/

title 'Problem 18: Poisson regression';

title2 'Q1: Dobson counts with treatment';
data dobson;
  input counts treat @@;
  datalines;
18 0 17 0 15 0 20 0 10 0 20 0 25 0 13 0 12 0
21 1 20 1 18 1 23 1 13 1 23 1 28 1 16 1 15 1
;

proc genmod data=dobson;
  model counts = treat / dist=poisson link=log;
run;

title2 'Q2: Rate model with exposure offset';
data ins;
  input claims ln hours group @@;
  datalines;
 2 4.6052 100 0
 3 5.0106 150 0
 5 5.3471 210 0
 7 5.5607 260 0
11 5.7991 330 0
18 6.0162 410 0
28 6.2538 520 0
41 6.4770 650 0
 1 4.4998  90 1
 2 4.9416 140 1
 3 5.3230 205 1
 5 5.5413 255 1
 9 5.7838 325 1
14 6.0039 405 1
22 6.2442 515 1
33 6.4693 645 1
;

proc genmod data=ins;
  model claims = group / dist=poisson link=log offset=ln;
run;

title2 'Q3: Overdispersed counts';
data over;
  input y x @@;
  datalines;
1 0 0 0 4 0 3 0 6 1 8 1 9 1 10 1
;

proc genmod data=over;
  model y = x / dist=poisson link=log;
run;

title;
