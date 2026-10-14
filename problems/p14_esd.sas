/* Problem 14 - Outlier detection: generalized (Rosner) ESD test,
   built from DATA-step primitives (arrays, POINT=/NOBS, %LET macros,
   TINV). Tests up to R=3 outliers at alpha=0.05; each step removes
   the observation farthest from the current mean and compares its
   studentized deviation with Rosner's critical value lambda. */
data chem;
  input y @@;
  datalines;
5.29 5.31 5.33 5.35 5.32 5.34 5.30 5.28 5.36 5.33
5.31 5.34 5.29 5.32 5.35 5.30 7.95 8.12
;

%let N = 18;
%let R = 3;
%let alpha = 0.05;

proc sort data=chem; by y; run;

data esd;
  array yy(&N) _temporary_;
  array mask(&N) 8;
  if _n_ = 1 then do;
    do i = 1 to &N;
      set chem point=i nobs=nobs;
      yy(i) = y;
      mask(i) = 1;
    end;
  end;
  nleft = &N;
  do step = 1 to &R;
    sm = 0;
    do i = 1 to &N;
      if mask(i) then sm + yy(i);
    end;
    mean = sm / nleft;
    ss = 0;
    do i = 1 to &N;
      if mask(i) then ss + (yy(i)-mean)**2;
    end;
    sd = sqrt(ss / (nleft-1));
    gmax = 0;
    imax = 0;
    do i = 1 to &N;
      if mask(i) then do;
        g = abs(yy(i)-mean) / sd;
        if g > gmax then do; gmax = g; imax = i; end;
      end;
    end;
    p = 1 - &alpha / (2 * nleft);
    tcrit = tinv(p, nleft - 2);
    lam = (nleft * tcrit) / sqrt((nleft - 2 + tcrit**2) * (nleft + 1));
    outlier_val = yy(imax);
    keepflag = (gmax > lam);
    output;
    mask(imax) = 0;
    nleft + (-1);
  end;
  stop;
run;

proc print data=esd noobs; var step outlier_val gmax lam keepflag; run;
