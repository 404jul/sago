/* Problem 15 - Orthogonal polynomial trend decomposition.
   Response across four equally spaced dose levels (n=8 each),
   partitioned into linear, quadratic, and cubic contrasts via the
   GLM CONTRAST statement. F = contrast SS / MSE. */
data dose;
  input dose resp @@;
  datalines;
1 12.1 1 11.6 1 12.8 1 11.9 1 12.4 1 12.2 1 11.7 1 12.6
2 15.3 2 14.8 2 16.1 2 15.0 2 15.6 2 15.2 2 14.9 2 15.8
3 16.9 3 16.2 3 17.5 3 16.6 3 17.1 3 16.8 3 16.4 3 17.3
4 16.4 4 15.7 4 17.0 4 16.1 4 16.6 4 16.3 4 15.9 4 16.8
;

proc glm data=dose;
  class dose;
  model resp = dose;
  contrast 'linear'    dose -3 -1 1 3;
  contrast 'quadratic' dose 1 -1 -1 1;
  contrast 'cubic'     dose -1 3 -3 1;
run;
