/* Problem 5 - Binary logistic regression (PROC LOGISTIC).
   Coronary heart disease (chd=1) modeled from age and LDL.
   DESCENDING makes the event (chd=1) the modeled level; without it
   PROC LOGISTIC models the lowest ordered level (chd=0).
   Newton-Raphson ML fit: estimates, Wald tests, odds ratios,
   global tests (LR/Score/Wald), AIC/SC, and rank association (c). */
data chd;
  input chd age ldl @@;
  datalines;
1 64 189 0 53 171 0 39 158 0 67 166 1 57 183 1 50 240 1 56 259
1 62 208 1 55 242 0 48 184 0 53 125 1 72 172 0 49 131 1 65 166
0 53 212 1 63 225 1 59 161 1 43 206 1 68 170 1 62 186 1 70 169
1 66 255 1 62 124 0 45 191 1 68 214 1 57 200 0 59 153 0 42 253
1 65 224 1 65 220 1 67 189 1 53 179 1 67 198 0 71 170 1 63 170
0 56 207 0 60 127 1 45 263 0 42 257 1 66 161 0 43 138 1 63 233
1 64 135 1 42 201 0 42 146
;

proc logistic data=chd descending;
  model chd = age ldl;
run;
