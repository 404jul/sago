/*---------------------------------------------------------------------
  Problem 17 - Cox proportional hazards regression (PROC PHREG)

  Q1. Exact-docs check: the Exposed rat data (same as the PHREG
      chapter getting-started example) with the Group covariate,
      BRESLOW ties. Published output:
        -2 LOG L 204.317 (without) / 201.438 (with covariates)
        LR 2.8784, Score 3.0001, Wald 2.9254
        Estimate -0.59590, SE 0.34840, Hazard Ratio 0.551
  Q2. Two covariates (group and sex): multi-parameter Newton-Raphson.
  Q3. Ties-heavy data with TIES=EFRON vs the BRESLOW default.

  All values cross-checked against R survival::coxph (independent
  implementation).
---------------------------------------------------------------------*/

title 'Problem 17: Cox regression';

title2 'Q1: Rat data, one covariate';
data rats;
  input days status group @@;
  datalines;
143 1 0 164 1 0 188 1 0 188 1 0 190 1 0 192 1 0
206 1 0 209 1 0 213 1 0 216 1 0 220 1 0 227 1 0
230 1 0 234 1 0 246 1 0 265 1 0 304 1 0 216 0 0
244 0 0 142 1 1 156 1 1 163 1 1 198 1 1 205 1 1
232 1 1 232 1 1 233 1 1 233 1 1 233 1 1 233 1 1
239 1 1 240 1 1 261 1 1 280 1 1 280 1 1 296 1 1
296 1 1 323 1 1 204 0 1 344 0 1
;

proc phreg data=rats;
  model days*status(0) = group;
run;

title2 'Q2: Two covariates (treatment and sex)';
data exposed;
  input days status treatment sex @@;
  datalines;
179 1 1 2 378 0 1 1 256 1 1 1 355 1 1 1 262 1 1 2
319 1 1 2 256 1 1 1 256 1 1 1 255 1 1 2 171 1 1 1
224 0 1 1 325 1 1 2 225 1 1 1 325 1 1 2 287 1 1 2
217 1 1 1 319 1 1 2 255 1 1 1 264 1 1 2 256 1 1 1
237 0 0 1 291 1 0 2 156 1 0 1 323 1 0 2 270 1 0 2
253 1 0 2 257 1 0 2 206 1 0 1 242 1 0 2 206 1 0 1
157 1 0 1 237 1 0 2 249 1 0 2 211 1 0 1 180 1 0 1
229 1 0 1 226 1 0 1 234 1 0 1 268 0 0 2 209 1 0 1
;

proc phreg data=exposed;
  model days*status(0) = treatment sex;
run;

title2 'Q3: Ties with EFRON handling';
data ties;
  input months status x @@;
  datalines;
1 1 0 2 1 1 2 0 0 3 1 1 3 0 0 4 1 1
4 0 0 5 1 1 5 0 0 6 1 1 6 0 0 7 1 1
1 1 1 2 0 0 3 0 0 4 0 0 5 1 1 6 1 1
;

proc phreg data=ties;
  model months*status(0) = x / ties=efron;
run;

proc phreg data=ties;
  model months*status(0) = x / ties=breslow;
run;

title;
