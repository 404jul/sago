/* Problem 19 - Logistic regression with a CLASS predictor.
   Reference coding (PARAM=REF) with REF='B': coefficients are
   log-odds ratios of A and C versus B, alongside a continuous
   dose effect. Odds ratios with Wald limits. */
data trial;
  input resp trt $ dose @@;
  datalines;
1 A 6.3 1 A 9.0 1 A 7.8 1 A 2.3 1 A 3.0 1 A 8.7 0 A 0.1 1 A 8.2
0 A 8.0 1 A 4.7 0 A 3.0 1 A 2.8 0 A 2.5 0 A 4.5 1 A 5.0 0 A 5.5
0 A 10.0 0 A 7.9 1 A 6.2 1 A 9.9 1 B 2.2 0 B 1.6 1 B 6.1 1 B 0.4
0 B 0.4 0 B 5.1 0 B 4.7 1 B 9.2 1 B 6.3 1 B 5.1 1 B 5.0 0 B 2.5
0 B 0.1 0 B 1.9 1 B 6.9 0 B 2.0 1 B 3.7 0 B 0.0 1 B 8.3 1 B 1.5
0 C 2.7 1 C 8.8 1 C 5.1 1 C 8.5 0 C 6.4 1 C 7.4 0 C 0.9 0 C 5.4
1 C 5.1 1 C 8.7 0 C 3.6 0 C 6.0 1 C 0.6 0 C 3.9 1 C 3.2 0 C 1.5
0 C 8.2 1 C 3.8 1 C 9.8 0 C 5.9
;

proc logistic data=trial descending;
  class trt(ref='B') / param=ref;
  model resp = trt dose;
run;
