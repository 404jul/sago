/* SET/MERGE semantics: value retention in one-to-many merges, IN=,
   interleaving, LAG inside IF, sort order of missing values. */
data a;
  input id x;
  datalines;
1 10
1 11
2 20
3 30
;

data b;
  input id y;
  datalines;
1 100
2 200
2 201
4 400
;

data m;
  merge a(in=ina) b(in=inb);
  by id;
  fa = ina;
  fb = inb;
run;

data inter;
  set a b;
  by id;
run;

data lagged;
  set a;
  if mod(_n_, 2) = 0 then pl = lag(x);
run;

data s;
  input k;
  datalines;
3
.
1
-2
;

proc sort data=s out=sorted;
  by k;
run;
