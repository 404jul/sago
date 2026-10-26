/*---------------------------------------------------------------------
  Linear model inference: REG options and GLM contrasts beyond the
  examples. One small well-conditioned data set; every result lands in
  ODS tables (compared by the harness) or PUT lines.

  Ground truth (exact OLS on these data):
    yield = 10 + 2.0*x1 + 1.0*x2, n = 12, no collinearity.
  ---------------------------------------------------------------------*/
data lm;
  do i = 1 to 12;
    x1 = i;
    x2 = 13 - i;
    yield = 10 + 2 * x1 + 1 * x2 + mod(i * 7, 5) - 2;
    output;
  end;
  drop i;
run;

proc reg data=lm outest=est;
  model yield = x1 x2 / clb stb tol vif dw;
  test x1 = 2, x2 = 1;
  restrict x1 = 2;
  output out=cliout predicted=phat residual=r student=stu l95=lo u95=hi
         l95m=lom u95m=him cookd=cd h=lev press=res_pre;
run;
quit;

proc print data=est; run;
proc print data=cliout; run;

/* Weighted regression: slope must be exactly 2 (variance grows with x1) */
data w;
  do x = 1 to 8;
    y = 1 + 2 * x;
    wt = 1 / x;
    output;
  end;
run;
proc reg data=w;
  model y = x / clb;
  weight wt;
run;

/* GLM: ESTIMATE and CONTRAST statements, Type I vs III SS order effect */
data g;
  do a = 1 to 3;
    do b = 1 to 2;
      do rep = 1 to 3;
        y = 5 * a + 3 * b + 0.5 * rep;
        output;
      end;
    end;
  end;
run;
proc glm data=g;
  class a b;
  model y = a b a*b / solution;
  estimate 'a1 vs a2' a 1 -1 0;
  contrast 'b pooled' b 1 -1;
  lsmeans a b / stderr pdiff;
run;
quit;

/* ANOVA proc on the balanced design (same fit as GLM) */
proc anova data=g;
  class a b;
  model y = a b a*b;
  means a b / tukey;
run;
quit;

/* Tukey LINES with a two-letter level: means 20/18/10, MSE 0.8333,
   HSD half-width 2.177 -> A on 20, A B on 18, B on 10 */
data l3;
  input g $ y @@;
  datalines;
A 18 A 19 A 20 A 21 A 22
B 16 B 17 B 18 B 19 B 20
C 8 C 9 C 10 C 11 C 12
;
proc glm data=l3;
  class g;
  model y = g;
  means g / tukey;
run;
quit;

/* SAS's studentized-range quantile over a grid: pins the algorithm
   behind GLM's Tukey critical values (QTUKEY differs slightly from the
   exact distribution; see BUGS.md) */
data _null_;
  do k = 2 to 8;
    do df = 3, 5, 8, 12, 20, 40, 100;
      q = qtukey(0.95, k, df);
      put k= df= q= best32.;
    end;
  end;
run;
