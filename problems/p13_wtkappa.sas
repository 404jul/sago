/* Problem 13 - Weighted kappa with two weighting schemes.
   Same 4x4 skin-condition ratings as before: weighted kappa credits
   near-miss disagreements. Default weights are Cicchetti-Allison
   (linear); WT=FC selects Fleiss-Cohen (quadratic). */
data SkinCondition;
   length derm1 $ 8 derm2 $ 8;
   input derm1 $ derm2 $ count;
   datalines;
terrible terrible 10
terrible     poor 4
terrible marginal 1
terrible    clear 0
poor     terrible 5
poor         poor 10
poor     marginal 12
poor        clear 2
marginal terrible 2
marginal     poor 4
marginal marginal 12
marginal    clear 5
clear    terrible 0
clear        poor 2
clear    marginal 6
clear       clear 13
;
ods output KappaStatistics=_ks_ WtkapTest=_wt_;
proc freq data=SkinCondition order=data;
   weight count;
   tables derm1*derm2 / agree;
run;
ods output KappaStatistics=_ksf_ WtkapTest=_wtf_;
proc freq data=SkinCondition order=data;
   weight count;
   tables derm1*derm2 / agree(wt=fc);
run;
