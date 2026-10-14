/*---------------------------------------------------------------------
  Problem 6 - two-way ANOVA with interaction (unbalanced)

  Crop yield by fertilizer (N/P/K) and irrigation level (Low/High),
  30 plots in an unbalanced arrangement (cell sizes 6/4, 4/6, 5/5).
  The data are constructed so fertilizer ranks flip between irrigation
  levels: under Low irrigation P > N > K, under High irrigation
  P > K > N - a genuine fertilizer x irrigation interaction. Because
  the design is unbalanced, Type I and Type III sums of squares differ
  for the main effects (they coincide for the interaction term).

  Q1. Overall ANOVA and the interaction F test.
  Q2. Type I vs Type III SS for the main effects.
  Q3. LSMEANS of fertilizer (equally weighted over irrigation levels),
      contrasted with the raw-class MEANS that weight by cell sizes.

  Expected results (verified against statsmodels anova_lm):
    interaction F(2,24) = 47.47, p < .0001; fert Type III F(2,24) =
    133.33, irrigation Type III F(1,24) = 448.1. fert Type I SS
    (entered first) differs from its Type III SS.
---------------------------------------------------------------------*/

title 'Problem 6: two-way ANOVA with interaction';

data crop;
  input fert $ irrig $ yield @@;
  datalines;
N Low 51.8 N Low 52.4 N Low 51.9 N Low 52.8 N Low 52.1 N Low 52.3
N High 54.9 N High 55.4 N High 54.7 N High 55.2
P Low 53.6 P Low 54.1 P Low 53.9 P Low 54.5
P High 59.8 P High 60.3 P High 59.6 P High 60.8 P High 60.1 P High 59.9
K Low 49.8 K Low 50.3 K Low 49.5 K Low 50.6 K Low 50.1
K High 58.7 K High 59.2 K High 58.9 K High 59.6 K High 59.1
;

proc glm data=crop;
  class fert irrig;
  model yield = fert irrig fert*irrig;
  means fert;
  lsmeans fert;
run;

title;
