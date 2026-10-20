/* Compile-time length of character variables. */
data first_assign;
  do i = 1 to 3;
    if i = 1 then s = 'a';
    else s = 'abcdef';
    output;
  end;
run;

data concat;
  p = 'abc  ';
  q = p || 'x';
  r = trim(p) || 'x';
  n = 'V:' || 3;
  c = cats('V:', 3);
  len_q = length(q);
  len_n = lengthc(n);
run;

data explicit;
  length t $3;
  t = 'abcdef';
  input name $;
  datalines;
abcdefghijkl
;
