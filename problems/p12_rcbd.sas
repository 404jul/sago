/* Problem 12 - Randomized complete block design.
   Three fertilizer treatments randomized within four field blocks
   (one plot per block-treatment). Blocks absorb soil gradients;
   treatment effects tested against within-block error. */
data field;
  input block $ trt $ yield @@;
  datalines;
b1 a 12.3 b1 b 15.1 b1 c 17.8
b2 a 11.9 b2 b 14.6 b2 c 17.2
b3 a 13.4 b3 b 16.0 b3 c 18.9
b4 a 12.8 b4 b 15.5 b4 c 18.1
;

proc glm data=field;
  class block trt;
  model yield = block trt;
run;
