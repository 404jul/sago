/* Problem 18 - Comparing two Poisson rates.
   Adverse events per exposure time: treated 14 events / 2.5 years,
   control 6 events / 1.8 years. Conditional exact test (X1 | total
   events ~ Binomial(N, T1/(T1+T2))), pooled and unpooled normal
   approximations, and a 95% CI for the rate ratio. */
data rates;
  length group $ 7;
  input group $ events time;
  datalines;
treated 14 2.5
control 6 1.8
;

data rateres;
  set rates end=last;
  retain x1 x2 t1 t2 0;
  if _n_ = 1 then do;
    x1 = events; t1 = time;
  end;
  else do;
    x2 = events; t2 = time;
  end;
  if last then do;
    r1 = x1 / t1;
    r2 = x2 / t2;
    rr = r1 / r2;
    /* conditional exact test */
    ntot = x1 + x2;
    p0 = t1 / (t1 + t2);
    pleft = probbnml(p0, ntot, x1);
    pright = 1 - probbnml(p0, ntot, x1 - 1);
    pexact = min(1, 2 * min(pleft, pright));
    /* pooled normal approximation */
    zpooled = (r1 - r2) / sqrt((ntot / (t1 + t2)) * (1/t1 + 1/t2));
    ppooled = 2 * probnorm(-abs(zpooled));
    /* unpooled Wald */
    zwald = (r1 - r2) / sqrt(x1 / t1**2 + x2 / t2**2);
    pwald = 2 * probnorm(-abs(zwald));
    /* 95% CI for the rate ratio (log scale) */
    selog = sqrt(1/x1 + 1/x2);
    rrlo = exp(log(rr) - 1.959964 * selog);
    rrhi = exp(log(rr) + 1.959964 * selog);
    output;
  end;
  drop group events time;
run;

proc print data=rateres noobs; run;
