/*---------------------------------------------------------------------
  Problem 15 - DSCF all-pairs multiple comparisons

  The Dwass-Steel-Critchlow-Fligner procedure tests all r(r-1)/2 pairs
  of class levels after a significant Kruskal-Wallis-type result. Each
  pair is ranked WITHIN the pair; the two-sample standardized Wilcoxon
  z (tie-corrected, no continuity correction) is scaled by sqrt(2), and
  the two-sided p-value is the upper tail of the studentized range
  distribution with k = number of classes and df = infinity.

  Q1. Four dose groups with tied values: all six pairwise comparisons.
      Statistics and p-values verified against an independent
      implementation (R PMCMRplus::dscfAllPairsTest).
  Q2. Three groups with heavier ties: cross-pair ties make the
      tie-corrected variance matter.

  Expected: pairs involving the highest dose are significant; pairs of
  adjacent low doses are not.
---------------------------------------------------------------------*/

title 'Problem 15: DSCF multiple comparisons';

title2 'Q1: Four dose groups';
data doses;
  input dose $ resp @@;
  datalines;
A 12.1 A 13.4 A 11.9 A 12.8 A 13.1 A 12.5 A 11.6 A 12.2
B 13.8 B 15.2 B 14.1 B 13.5 B 14.8 B 15.0 B 13.9
C 15.6 C 17.1 C 16.3 C 15.9 C 16.8 C 17.4 C 16.0
D 18.2 D 20.5 D 19.1 D 18.7 D 20.1 D 19.6 D 18.9 D 20.8
;

proc npar1way data=doses dscf;
  class dose;
  var resp;
run;

title2 'Q2: Three groups with ties';
data tied;
  input grp $ score @@;
  datalines;
G1 3 G1 5 G1 4 G1 3 G1 6 G1 5 G1 4
G2 6 G2 8 G2 7 G2 6 G2 5 G2 8 G2 7
G3 9 G3 8 G3 10 G3 9 G3 7 G3 9 G3 8
;

proc npar1way data=tied dscf;
  class grp;
  var score;
run;

title;
