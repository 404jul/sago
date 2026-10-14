/* Problem 3 - Stratified inference: Cochran-Mantel-Haenszel.
   Three centers (strata), treatment vs binary response within each.
   Question: does treatment reduce the event rate after adjusting for
   center? CMH chi-square, MH common odds ratio with Robins-Breslow-
   Greenland confidence limits. */
data trial;
  length center $ 8 trt $ 8 resp $ 8;
  input center $ trt $ resp $ count;
  datalines;
north trt event 12
north trt nonevent 8
north ctl event 8
north ctl nonevent 12
mid trt event 10
mid trt nonevent 6
mid ctl event 6
mid ctl nonevent 9
south trt event 7
south trt nonevent 9
south ctl event 4
south ctl nonevent 12
;

proc freq data=trial;
  weight count;
  tables center*trt*resp / cmh;
run;
