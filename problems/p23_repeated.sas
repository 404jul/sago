/* Problem 23 - Repeated measures via long-format reshaping.
   Pain scores for 8 subjects at 3 visits (wide layout). The DATA
   step transposes to long format with an ARRAY; the visit effect is
   then tested with subject as a blocking factor (the univariate
   approach to repeated measures). */
data wide;
  input subj $ t1 t2 t3 @@;
  datalines;
s01 4.2 3.8 3.1 s02 5.5 5.0 4.6 s03 3.9 3.7 3.5
s04 6.1 5.4 5.5 s05 4.8 4.1 3.7 s06 5.2 5.1 4.4
s07 4.5 3.9 3.9 s08 5.8 5.6 4.8
;

data long;
  set wide;
  array ts{3} t1-t3;
  do time = 1 to 3;
    score = ts{time};
    output;
  end;
  keep subj time score;
run;

proc glm data=long;
  class subj time;
  model score = subj time;
run;
