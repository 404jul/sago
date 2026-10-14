/*---------------------------------------------------------------------
  Problem 9 - distribution functions: p-values and critical values

  Study-design work constantly needs tail probabilities and critical
  values that tables only give to two decimals. This program builds a
  reference table entirely from DATA step distribution functions:
  PROBNORM, PROBT, PROBCHI, PROBF, PROBBNML, POISSON and the inverse
  quantile functions TINV, CINV, FINV, PROBIT, QUANTILE.

  The row of each observation is identified by DIST plus the parameter
  columns A1 (and A2 for the F distribution).

  Expected results (verified against scipy.stats):
    Normal:     two-sided p = 2*sf(z),  e.g. z=1.96 -> 0.0499958
    t:          upper-tail sf,          e.g. t=2.5, df=1 -> 0.239823
    tcrit:      t.ppf(0.975, df),       e.g. df=1 -> 12.7062
    chisq:      sf on df=1,             e.g. 3.84 -> 0.050044
    chicrit:    chi2.ppf(0.95, df),     e.g. df=1 -> 3.8415
    F:          sf(3.0),                e.g. F(3,20) -> 0.053972
    Fcrit:      f.ppf(0.95),            e.g. F(3,20) -> 3.0984
    Binom:      binom.cdf(k, 10, 0.3),  e.g. k=2 -> 0.382781
    Poisson:    poisson.cdf(k, 2),      e.g. k=2 -> 0.676676
    qNorm:      norm.ppf(0.975) = 1.959964
---------------------------------------------------------------------*/

title 'Problem 9: distribution functions';

data crit;
  length dist $8;
  do z = 0.5, 1.0, 1.645, 1.96, 2.326, 2.576, 3.0;
    dist='Normal'; a1=z; a2=.; p=2*(1-probnorm(z)); crit=.; output;
  end;
  do t = 1.5, 2.0, 2.5;
    do df = 1, 2, 5, 10, 30, 100;
      dist='t'; a1=t; a2=df; p=probt(t, df); crit=.; output;
    end;
  end;
  do df = 1, 2, 5, 10, 30, 100;
    dist='tcrit'; a1=df; a2=.; crit=tinv(0.975, df); p=.; output;
  end;
  do x = 3.84, 6.63, 11.07;
    dist='chisq'; a1=x; a2=.; p=probchi(x, 1); crit=.; output;
  end;
  do df = 1, 2, 5, 10, 20;
    dist='chicrit'; a1=df; a2=.; crit=cinv(0.95, df); p=.; output;
  end;
  do ndf = 1, 3, 5;
    do ddf = 10, 20, 60;
      dist='F'; a1=ndf; a2=ddf; p=probf(3.0, ndf, ddf); crit=.; output;
      dist='Fcrit'; a1=ndf; a2=ddf; crit=finv(0.95, ndf, ddf); p=.; output;
    end;
  end;
  do df = 4, 12, 40;
    dist='qT'; a1=df; a2=.; crit=quantile('T', 0.975, df); p=.; output;
  end;
  do pr = 0.95, 0.975, 0.995;
    dist='qNorm'; a1=pr; a2=.; crit=quantile('NORM', pr); p=.; output;
  end;
  do k = 0, 1, 2, 3;
    dist='Binom'; a1=k; a2=.; p=probbnml(0.3, 10, k); crit=.; output;
  end;
  do k = 0, 1, 2, 3;
    dist='Poisson'; a1=k; a2=.; p=poisson(2, k); crit=.; output;
  end;
  drop z t x df ndf ddf k pr;
run;

proc print data=crit noobs;
run;

title;
