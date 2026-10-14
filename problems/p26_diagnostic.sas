/* Problem 26 - Diagnostic test evaluation.
   New rapid test vs gold standard on 200 specimens. Sensitivity,
   specificity, PPV, NPV, accuracy and prevalence, each with exact
   (Clopper-Pearson) 95% confidence intervals computed with BETAINV. */
data dx;
  length gold $ 7 test $ 4;
  input gold $ test $ count;
  datalines;
disease pos 38
disease neg 12
healthy pos 8
healthy neg 142
;

proc freq data=dx;
  weight count;
  tables gold*test / norow nocol nopercent chisq;
run;

data eval;
  set dx end=last;
  retain tp fn fp tn 0;
  if gold = 'disease' and test = 'pos' then tp = count;
  else if gold = 'disease' and test = 'neg' then fn = count;
  else if gold = 'healthy' and test = 'pos' then fp = count;
  else if gold = 'healthy' and test = 'neg' then tn = count;
  if last then do;
    n1 = tp + fn;          /* diseased */
    n0 = fp + tn;          /* healthy  */
    n = n1 + n0;
    sens = tp / n1;
    spec = tn / n0;
    ppv  = tp / (tp + fp);
    npv  = tn / (fn + tn);
    acc  = (tp + tn) / n;
    prev = n1 / n;
    /* Clopper-Pearson exact 95% intervals */
    sens_lo = betainv(0.025, tp, n1 - tp + 1);
    sens_hi = betainv(0.975, tp + 1, n1 - tp);
    spec_lo = betainv(0.025, tn, n0 - tn + 1);
    spec_hi = betainv(0.975, tn + 1, n0 - tn);
    ppv_lo  = betainv(0.025, tp, tp + fp - tp + 1);
    ppv_hi  = betainv(0.975, tp + 1, tp + fp - tp);
    npv_lo  = betainv(0.025, tn, fn + tn - tn + 1);
    npv_hi  = betainv(0.975, tn + 1, fn + tn - tn);
    keep sens sens_lo sens_hi spec spec_lo spec_hi ppv ppv_lo ppv_hi
         npv npv_lo npv_hi acc prev;
    output;
  end;
run;

proc print data=eval noobs; run;
