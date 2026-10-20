/* DATA step log notes: uninitialized, invalid data, invalid argument,
   division by zero, missing values generated, MERGE repeats. */
data loop;
  do i = 1 to 2;
    y = i * 2;
    output;
  end;
run;

data uninit;
  x = z + 1;
run;

data bad;
  input x y;
  datalines;
1 2
abc 3
;

data conv;
  n1 = input('12.5', 8.);
  n2 = input('abc', 8.);
  q = 1 / 0;
  m = . + 1;
  c2n = '42' + 1;
run;

data a;
  input id v;
  datalines;
1 1
1 2
;

data both;
  merge a a(rename=(v=w));
  by id;
run;
