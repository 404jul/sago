/*---------------------------------------------------------------------
  Problem 10 - random numbers and sampling

  Q1. The RANUNI generator (seed = mod(397204094*seed, 2**31-1), scaled
      to (0,1)) must reproduce the documented SAS stream bit-exactly,
      continuing across calls and observations. RANNOR (Box-Muller on
      the same stream) is checked by recomputing the formula.
  Q2. RAND('NORMAL') with a fixed CALL STREAMINIT seed: 4000 draws
      should have mean near 0 and standard deviation near 1.
  Q3. PROC SURVEYSELECT: simple random sample (n = 6 of 20, OUTHITS),
      systematic sample (n = 5), and unrestricted random selection with
      replacement (n = 25, REPS = 2). Fixed seeds make every run
      reproducible; the OUT= data sets carry SelectionProb and
      SamplingWeight (SRS) and NumberHits/ExpectedHits (URS).

  Expected results (verified against the documented RANUNI recurrence
  in Python):
    seed 12345 -> 0.36292445351, 0.74519471300, 0.83105867302, ...
    RANNOR first draw = sqrt(-2*ln(u2))*cos(2*pi*u3) with u2,u3 the
    next two uniforms.
    SRS: 6 distinct plots, SelectionProb 0.3, SamplingWeight 3.3333;
    URS: 25 draws per replicate, NumberHits summing to 25.
---------------------------------------------------------------------*/

title 'Problem 10: random numbers and sampling';

data uniforms;
  do i = 1 to 8;
    u = ranuni(12345);
    z = rannor(12345);
    output;
  end;
  drop i;
run;

title2 'Q1: RANUNI and RANNOR streams (seed 12345)';
proc print data=uniforms noobs;
run;

title2 'Q2: RAND NORMAL moments with fixed seed';
data normal;
  call streaminit(7);
  do i = 1 to 4000;
    z = rand('NORMAL');
    output;
  end;
  drop i;
run;

proc means data=normal n mean std min max;
  var z;
run;

title2 'Q3: SURVEYSELECT on 20 plots';
data plots;
  do plot = 1 to 20;
    yield = 48 + plot * 0.25;
    output;
  end;
run;

proc surveyselect data=plots method=srs n=6 seed=12345 outhits out=srs;
  id plot yield;
run;

proc surveyselect data=plots method=sys n=5 seed=777 out=sys;
  id plot;
run;

proc surveyselect data=plots method=urs n=25 reps=2 seed=42 outhits out=urs;
  id plot;
run;

title3 'SRS sample';
proc print data=srs noobs;
run;
title3 'SYS sample';
proc print data=sys noobs;
run;
title3 'URS sample (first 12 rows)';
proc print data=urs noobs obs=12;
run;

title;
