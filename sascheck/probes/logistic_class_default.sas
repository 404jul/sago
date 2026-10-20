/* PROC LOGISTIC CLASS without PARAM= (SAS default is PARAM=EFFECT). */
data trial;
  input trt $ dose resp count;
  datalines;
A 1 1 12
A 1 0 18
A 2 1 15
A 2 0 10
B 1 1 8
B 1 0 22
B 2 1 11
B 2 0 14
C 1 1 20
C 1 0 9
C 2 1 24
C 2 0 6
;

proc logistic data=trial descending;
  freq count;
  class trt;
  model resp = trt dose;
run;
