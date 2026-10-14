


/* import data */
DATA auto;
	INFILE '/home/u64596099/project1/auto-1.csv' DLM=',' FIRSTOBS=1;
	INPUT mpg cylinders displacement horsepower weight acceleration modelyear origin;
RUN;

PROC PRINT DATA=AUTO(OBS=5); 
RUN;

/* compute descriptive statistics */
PROC MEANS data=auto;
	VAR mpg;
	OUTPUT OUT=STATS MEAN=MEAN STD=STDDEV MIN=MINIMUM
		Q1=Q1 MEDIAN=MEDIAN Q3=Q3 MAX=MAXIMUM;
RUN;

PROC PRINT DATA=STATS;
RUN;

/* 1a. test for mean != 25 */

PROC TTEST DATA=auto
	H0=25
	ALPHA=0.05;
	VAR mpg;
RUN;

PROC SGPLOT Data=auto;
	Title 'MPG Distribution';
	HISTOGRAM mpg;
run;

/* 1b. */

/* combine into one variable to display side by side */
DATA plots;
    SET auto;
    	plot = 1;
    	x = displacement;
    	y = mpg;
    output;
    	plot = 2;
    	x = horsepower;
    	y = displacement;
    output;
RUN;

/* display plots  */
PROC SGPANEL DATA=plots;
    PANELBY plot / COLUMNS=2;
    SCATTER X=x Y=y / GROUP=origin;
RUN;


/* 1c. */
PROC SGPLOT DATA=auto;
	TITLE 'Box Plot of Displacement by Origin';
	VBOX displacement/ CATEGORY=origin;
RUN;

/* 1d. */
DATA MERGED;
	Set auto;
	if origin = 1 then group=1;
	else group=2;
RUN;

/* t test on origin1 vs origin2,3 */
PROC TTEST DATA=merged alpha=0.05;
	CLASS group;
	var displacement;
RUN;

/* 1e. */
PROC NPAR1WAY DATA=merged WILCOXON;
	CLASS group;
	VAR displacement;
RUN;

/* 1f. */
PROC FREQ DATA=auto;
	tables origin*cylinders/CHisq expected;
run;


/* 2. */


DATA sample;
seed1=123;
seed2=321;
do p=0.1,0.5,0.7;
do n=10,30,50;

do i=1 to 1000;

call ranbin(seed1, n, p, x);
call ranbin(seed2, n, p, y);

px = x/n;
py = y/n;
pp = px-py;
sp = sqrt(px*(1-px)/n + py*(1-py)/n);

zscore = quantile('normal', 0.975,0,1);

upper = pp+ zscore*sp;
lower = pp- zscore*sp;

if lower<=0 and 0<=upper then o=1;
else o=0;

output;
end;
end;
end;
run;


/* i used sql because i dont know how to do in SAS */
PROC Sql;
	select p,n,mean(o) as coverage from sample 
	GROUP BY p,n;
quit;



DATA sample2;
seed1=123;
seed2=321;
p1=0.1;
p2=0.2;
do n=10,30,50;

do i=1 to 1000;

call ranbin(seed1, n, p1, x);
call ranbin(seed2, n, p2, y);

px = x/n;
py = y/n;
pp = px-py;
sp = sqrt(px*(1-px)/n + py*(1-py)/n);

zscore = quantile('normal', 0.975,0,1);

upper = pp + zscore*sp;
lower = pp - zscore*sp;

if lower<=(p1-p2) and (p1-p2)<=upper then o=1;
else o=0;

output;
end;
end;
run;


/* i used sql because i dont know how to do in SAS */
PROC SQL;
select p1, p2, n, mean(o) as coverage from sample2
	GROUP BY p1, p2, n;
quit;




/* 3. */
DATA mcsim;

call streaminit(1232);
do b=1 to 10000;
	
	do n = 1 to 150;
		
		r = rand('Table', 0.2, 0.5, 0.3);
		c = rand('Table', 0.1, 0.4, 0.3, 0.2);
	output;
	end;
end;
run;

proc print data=mcsim(obs=3); run;

ods select none;
ods output chisq=chis;

PROC FREQ data=mcsim;
	by b;
	
	table r*c / chisq;
run;

ods select all;

PROC PRINT data=chis(obs=5); run;


DATA res;
	set chis;
	where Statistic='Chi-Square';
		
		c6 = quantile('CHISQ', 0.95, 6);
		c11 = quantile('CHISQ', 0.95, 11);
run;
	
	/* i used sql because i dont know how to do data operations in sas */
PROC SQL;
	select COUNT(1)/10000 as fisher from res WHERE Value > c6;
QUIT;


PROC SQL;
	SELECT	COUNT(1)/10000 as pearson from res WHERE Value > c11;
QUIT;
