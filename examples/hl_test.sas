/*---------------------------------------------------------------------
  Problem 13 - Hodges-Lehmann location shift estimation (HL)

  Q1. Exact-docs check: the REACT data from the SAS/STAT NPAR1WAY
      chapter (Examples 84.3/84.4). With ALPHA=.02 and EXACT HL, the
      published output is
        Location Shift (2 - 1) = 0.35
        Asymptotic (Moses) 98% limits 0.00 / 0.82, midpoint 0.41,
          ASE 0.1762
        Exact limits 0.00 / 1.33, midpoint 0.6650
  Q2. Asymmetric shift with distinct subgroup sizes and no ties: the
      exact limits must bracket the asymptotic ones conservatively
      (wider or equal), and the midpoint of the exact interval is a
      valid shift estimate.
  Q3. ALPHA=.05 default level on the same data (limits must be no
      wider than the 98% limits).

  All values verified against a from-scratch Python implementation that
  reproduces the published Output 84.4.1 exactly.
---------------------------------------------------------------------*/

title 'Problem 13: Hodges-Lehmann estimation';

title2 'Q1: React data (published example)';
data react;
  input stim time @@;
  datalines;
1 1.94 1 1.94 1 2.92 1 2.92 1 2.92 1 2.92 1 3.27
1 3.27 1 3.27 1 3.27 1 3.70 1 3.70 1 3.74
2 3.27 2 3.27 2 3.27 2 3.70 2 3.70 2 3.74
;

proc npar1way hl alpha=.02 data=react;
  class stim;
  var time;
  exact hl;
run;

title2 'Q2: Unequal sizes, no ties';
data shift;
  input group $ y @@;
  datalines;
A 10.2 A 11.5 A  9.8 A 10.7 A 12.1 A 11.0 A 10.5
B 14.9 B 16.2 B 13.8 B 15.5 B 17.0 B 14.2 B 15.9 B 16.8
B 13.5 B 15.1
;

proc npar1way hl alpha=.02 data=shift;
  class group;
  var y;
  exact hl;
run;

title2 'Q3: Default 95% level';
proc npar1way hl data=shift;
  class group;
  var y;
run;

title;
