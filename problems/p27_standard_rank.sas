/* Problem 27 - Standardization and ranking.
   PROC STANDARD produces z-scores (MEAN=0 STD=1); PROC RANK gives
   tied-mean ranks, percentile ranks (PERCENT) and decile groups
   (GROUPS=10) for the same data. */
data scores;
  input id $ height weight @@;
  datalines;
a 168 62.4 b 172 71.2 c 159 48.9 d 181 88.5 e 175 66.0
f 163 53.7 g 177 79.3 h 169 60.1 i 185 92.8 j 158 51.5
;

proc standard data=scores mean=0 std=1 out=zsc;
  var height weight;
run;

proc print data=zsc noobs; run;

proc rank data=scores out=rk descending;
  var weight;
  ranks wrank wpct wdec;
run;

proc rank data=rk percent;
  var weight;
  ranks wpct;
run;

proc rank data=rk groups=10;
  var weight;
  ranks wdec;
run;

proc print data=rk noobs; run;
