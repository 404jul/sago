/* Problem 11 - Welch's ANOVA (unequal variances).
   Three labs with unequal sample sizes and clearly different
   precision; the ordinary ANOVA F is unreliable when variances
   differ. Welch's variance-weighted F with Satterthwaite-type
   denominator df. */
data labs;
  length lab $ 5;
  input lab $ result @@;
  datalines;
east 10.2 east 9.8 east 10.5 east 9.9 east 10.1 east 10.3
east 9.7 east 10.4
west 13.8 west 10.9 west 14.5 west 11.4 west 13.2 west 12.8
west 11.1 west 14.1 west 10.5 west 13.5
south 18.9 south 13.6 south 19.4 south 14.2 south 18.1
south 13.9 south 19.0 south 14.8
;

proc glm data=labs;
  class lab;
  model result = lab;
  means lab / welch;
run;
