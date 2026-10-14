/* Problem 16 - Interaction and simple effects.
   Drug (A/B/C) x Sex (F/M) factorial, n=8 per cell. The drug effect
   differs by sex (interaction), so the overall drug F test is
   qualified: test the drug simple effect separately within each sex
   via BY-group one-way ANOVAs. */
data trial;
  length drug $ 1 sex $ 1;
  input drug $ sex $ resp @@;
  datalines;
A F 21.4 A F 19.8 A F 22.6 A F 20.9 A F 21.8 A F 20.5 A F 22.1 A F 21.0
B F 24.6 B F 23.1 B F 25.2 B F 24.0 B F 23.8 B F 25.5 B F 24.4 B F 23.5
C F 26.1 C F 24.9 C F 26.8 C F 25.6 C F 26.4 C F 25.2 C F 27.0 C F 25.9
A M 20.2 A M 21.5 A M 19.6 A M 20.8 A M 22.0 A M 19.9 A M 21.2 A M 20.5
B M 21.9 B M 20.6 B M 22.5 B M 21.1 B M 22.8 B M 20.3 B M 21.6 B M 22.2
C M 21.4 C M 22.7 C M 20.8 C M 22.1 C M 20.5 C M 23.0 C M 21.7 C M 20.9
;

proc glm data=trial;
  class drug sex;
  model resp = drug|sex;
run;

proc sort data=trial; by sex; run;

proc glm data=trial;
  by sex;
  class drug;
  model resp = drug;
run;
