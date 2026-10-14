/* Problem 6 - Multiple comparisons against a control (Dunnett).
   Four fertilizer regimes, one a control; n=6 plots each.
   Which treatments beat the control, with simultaneous 95%
   confidence limits (Dunnett's method)? */
data yield;
  length trt $ 5;
  input trt $ yield @@;
  datalines;
ctrl 42.1 ctrl 39.6 ctrl 41.3 ctrl 43.2 ctrl 40.8 ctrl 41.9
trtA 45.6 trtA 44.1 trtA 46.3 trtA 47.0 trtA 45.2 trtA 46.8
trtB 40.2 trtB 41.5 trtB 39.8 trtB 42.1 trtB 40.9 trtB 41.6
trtC 48.5 trtC 47.2 trtC 49.1 trtC 50.3 trtC 48.0 trtC 47.8
;

proc glm data=yield;
  class trt;
  model yield = trt;
  means trt / dunnett('ctrl') alpha=0.05;
run;
