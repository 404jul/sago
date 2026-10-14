/* Problem 10 - Bartlett's test for homogeneity of variance.
   Three production lines with unequal sample sizes; Bartlett's
   chi-square test of H0: equal within-group variances (sensitive to
   nonnormality - complement to the robust Levene test). */
data batches;
  length line $ 5;
  input line $ yield @@;
  datalines;
line1 51.2 line1 49.8 line1 50.6 line1 50.9 line1 49.5
line1 51.4 line1 50.2 line1 49.9 line1 50.8
line2 52.6 line2 47.9 line2 50.3 line2 54.1 line2 48.8
line2 51.7 line2 49.4 line2 53.2 line2 47.2 line2 52.5
line2 50.8
line3 53.8 line3 46.5 line3 51.2 line3 55.4 line3 45.9
line3 52.6 line3 48.1 line3 54.7
;

proc glm data=batches;
  class line;
  model yield = line;
  means line / hovtest=bartlett;
run;
