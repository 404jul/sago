/* Examples from "SAS Tutorial for STAT6337" (T. Wang, 2024), runnable with sago */

/*** Read files: read.csv use infile ***/
DATA iris;
INFILE 'iris.csv' DLM=',' FIRSTOBS=1;
LENGTH SepalLength SepalWidth PetalLength PetalWidth 8. Species $15.;
INPUT SepalLength SepalWidth PetalLength PetalWidth Species $;
RUN;
/* Print the first 5 rows of the iris data */
PROC PRINT DATA = iris (OBS=5);
RUN;

/*** Data manipulation: create datasets ***/
DATA sample_data1;
LENGTH Name $20 Gender $20;
INPUT ID Name $ Gender $;
DATALINES;
1 John Male
2 Sara Female
3 Steven Male
;
RUN;
DATA sample_data2;
INPUT ID Height;
DATALINES;
1 180
2 170
3 170
;
RUN;
PROC PRINT DATA = sample_data1;
RUN;
PROC PRINT DATA = sample_data2;
RUN;

/* Merge datasets */
DATA merged_sample_data;
MERGE sample_data1 sample_data2;
BY ID;
RUN;
PROC PRINT DATA = merged_sample_data;
RUN;

/* Add a column */
data iris_with_ratio;
set iris;
retain SepalRatio;
if SepalWidth ne 0 then /* make sure the denominator is not zero */
SepalRatio = SepalLength / SepalWidth;
run;
proc print data = iris_with_ratio (obs=5);
run;

/* Remove a column */
data iris_without_width;
set iris (drop=PetalWidth);
run;
proc print data = iris_without_width (obs=5);
run;

/* Modify a column */
data iris_modified;
set iris;
SepalLengthLog = log(SepalLength);
run;
proc print data = iris_modified (obs=5);
run;

data iris_modified;
set iris;
Species = upcase(Species);
run;
proc print data = iris_modified (obs=5);
run;

/*** Descriptive statistics ***/
PROC MEANS DATA = iris;
VAR SepalLength;
OUTPUT OUT=STATS MEAN=MEAN STD=STDDEV MIN=MINIMUM Q1 = Q1 MEDIAN=MEDIAN Q3=Q3 MAX=MAXIMUM;
RUN;
PROC PRINT DATA=STATS;
RUN;

PROC MEANS DATA = iris;
BY Species;
VAR SepalLength;
OUTPUT OUT=STATS MEAN=MEAN STD=STDDEV MIN=MINIMUM Q1 = Q1 MEDIAN=MEDIAN Q3=Q3 MAX=MAXIMUM;
RUN;
PROC PRINT DATA=STATS;
RUN;

/*** Plots ***/
PROC SGPLOT DATA = iris;
TITLE "Histogram of Sepal Length";
HISTOGRAM SepalLength / FILLATTRS=(COLOR=lightblue);
RUN;

PROC SGPLOT DATA = iris;
TITLE 'Box Plot of Sepal Length by Species';
VBOX SepalLength / CATEGORY=Species;
RUN;

PROC PLOT DATA=iris;
PLOT SepalWidth*SepalLength;
TITLE 'Scatter Plot of Sepal Length vs. Sepal Width';
RUN;

PROC SGPLOT DATA = iris;
TITLE 'Scatter Plot of Sepal Length vs. Sepal Width';
SCATTER x=SepalLength y=SepalWidth / MARKERATTRS=(Symbol=CircleFilled);
RUN;

TITLE 'QQ plot for SepalLength';
PROC UNIVARIATE DATA = IRIS NOPRINT;
QQPLOT SepalLength / NORMAL(MU=EST SIGMA=EST);
RUN;

/*** Statistical tests ***/
TITLE 'One-sample t-test';
PROC TTEST DATA = iris H0 = 6 ALPHA = 0.05;
VAR SepalLength;
RUN;

DATA iris_subset;
SET iris;
IF Species = 'Iris-setosa' or Species = 'Iris-virginica';
RUN;

TITLE 'Two-sample t-test';
PROC TTEST DATA = iris_subset;
CLASS Species;
VAR SepalLength;
RUN;

TITLE 'Wilcoxon-Mann-Whitney rank sum test';
PROC NPAR1WAY DATA=iris_subset WILCOXON;
CLASS Species;
VAR SepalLength;
RUN;

DATA CARS;
SET SASHELP.CARS;
RUN;

TITLE 'Chi-square test of independence';
PROC FREQ DATA=CARS;
TABLES Type * DriveTrain / CHISQ;
RUN;

TITLE 'Chi-square goodness of fit test';
PROC FREQ DATA = iris;
TABLES SPECIES / CHISQ;
RUN;
PROC FREQ DATA = iris;
TABLES SPECIES / CHISQ TESTP = (20, 50, 30);
RUN;
TITLE;

/*** Data simulation ***/
data uniform_sample;
do i = 1 to 100;
x = 5 + 10 * ranuni(156);
output;
end;
run;
proc print data = uniform_sample (OBS=5);
run;

data normal_sample;
do i = 1 to 100;
x = 50 + 10 * rannor(156);
output;
end;
run;
proc print data = normal_sample (OBS=5);
run;

data binomial_sample;
do i = 1 to 100;
x = ranbin(156, 50, 0.8);
output;
end;
run;
proc print data = binomial_sample (obs=5);
run;

Data sample_data;
call streaminit(12345);
do i = 1 to 100;
x1 = rand("Normal", 50, 3.2);
x2 = rand("Binomial", 0.8, 63);
x3 = rand("Bernoulli", 0.2);
x4 = rand("Uniform");
x5 = rand("Table", .2, .3, .3, .2);
x6 = rand("Table", 1/6, 1/6, 1/6, 1/6, 1/6, 1/6);
output;
end;
run;
proc print data = sample_data (obs=5);
run;

/* Monte Carlo simulation to estimate the p-value of a test */
PROC UNIVARIATE DATA = iris MU0=6;
VAR SepalLength;
OUTPUT OUT = SepalLength T = t_obs N = N STDDEV = Sd PROBT = pValue_obs;
RUN;
DATA SepalLength;
SET SepalLength;
Nruns = 10000; Mu0 = 6;
RUN;
DATA MC_M;
SET SepalLength;
call streaminit(54321);
DO MCrun=1 TO Nruns;
  DO j=1 TO N;
    Y = RAND("NORMAL", Mu0, Sd);
    OUTPUT;
  END;
END;
RUN;
DATA MC_M_2Sample;
SET MC_M;
WHERE MCrun <= 2;
RUN;
PROC MEANS DATA = MC_M_2Sample MEAN STD MIN MAX;
BY MCrun;
VAR Y;
RUN;
PROC UNIVARIATE DATA=MC_M MU0=6 NOPRINT;
VAR Y;
CLASS MCrun;
OUTPUT OUT=SampleResult T=T_MC;
RUN;
DATA SepalLength;
SET SepalLength;
DO i=1 TO Nruns; OUTPUT; END;
RUN;
DATA Compare;
MERGE SepalLength SampleResult;
indicator = 2*( T_MC >= abs(T_obs));
RUN;
PROC MEANS DATA=Compare NOPRINT;
VAR indicator;
OUTPUT OUT=P MEAN=Pvalue;
RUN;
PROC PRINT DATA=P; TITLE 'Estimated p-value';
VAR Pvalue;
RUN;
TITLE;

/*** Regression ***/
PROC GPLOT Data = CARS;
PLOT MPG_City*(Weight EngineSize HorsePower);
RUN;
PROC SGSCATTER DATA=CARS;
PLOT (MPG_CITY)*(WEIGHT ENGINESIZE HORSEPOWER);
RUN;
PROC CORR DATA=CARS;
VAR MPG_CITY WEIGHT ENGINESIZE HORSEPOWER;
RUN;

PROC REG DATA = CARS;
MODEL MPG_City = Weight Horsepower;
RUN;
PROC REG DATA = CARS;
MODEL MPG_City = Weight Horsepower / noint;
RUN;
DATA CARS;
SET CARS;
WH_Inter = Weight*Horsepower;
RUN;
PROC REG data=cars;
model MPG_City = Weight Horsepower WH_Inter;
RUN;

PROC REG DATA = CARS;
MODEL MPG_City = Weight Horsepower EngineSize / lackfit;
OUTPUT OUT=D RSTUDENT=R PREDICTED=P;
RUN;
PROC PLOT Data=D;
plot R*(P Weight Horsepower EngineSize);
RUN;
DATA D;
SET D;
absR = abs(R);
RUN;
PROC GPLOT DATA = D;
PLOT absR*P;
RUN;
PROC UNIVARIATE DATA=D NORMAL PLOT;
VAR R;
RUN;

PROC MODEL DATA=D;
PARMS b0 b1 b2 b3;
MPG_City = b0 + b1*Weight + b2*Horsepower + b3*EngineSize;
fit MPG_City / WHITE BREUSCH=(Weight Horsepower EngineSize);
fit MPG_City / BREUSCH=(Weight);
fit MPG_City / BREUSCH=(Horsepower);
fit MPG_City / BREUSCH=(EngineSize);
RUN;

PROC UNIVARIATE DATA = D NOPRINT;
VAR Weight Horsepower EngineSize;
OUTPUT OUT = Medians Median = MedW MedH MedE N=N;
RUN;
DATA Medians;
SET medians;
DO i = 1 TO N; OUTPUT; END;
RUN;
DATA WeightBF;
MERGE D Medians;
Group = (Weight > MedW);
RUN;
PROC GLM Data = WeightBF;
class Group;
model R = Group;
means Group / hovtest = BF;
run;

PROC GLM DATA = CARS;
MODEL MPG_City = Weight Horsepower EngineSize;
RUN;

PROC REG DATA = CARS;
MODEL MPG_City = Weight Horsepower EngineSize;
x2x3: test Horsepower = EngineSize = 0;
RUN;

PROC CORR DATA=CARS;
VAR MPG_CITY WEIGHT;
PARTIAL HORSEPOWER ENGINESIZE;
RUN;

/*** Bootstrap ***/
data Horsepower(keep=Horsepower);
set CARS;
run;
proc means data=Horsepower noprint;
var Horsepower;
output out=OrigStats(keep = N orig_mean orig_std) N = N mean = orig_mean std = orig_std;
run;
proc print data = OrigStats;
run;

%let NumSamples = 1000;
proc surveyselect data=Horsepower NOPRINT seed=1
  out=BootSamples
  method=urs
  samprate=1
  OUTHITS
  reps=&NumSamples;
run;
proc means data=BootSamples noprint;
by Replicate;
var Horsepower;
output out=BootMeans mean = boot_means std = boot_stds;
run;
proc print data=BootMeans(obs=5);
run;

proc sgplot data=BootMeans;
label boot_means = "Bootstrap means";
histogram boot_means / scale=count;
run;
proc univariate data=BootMeans;
var boot_means;
qqplot / normal(mu=est sigma=est color=steelblue l=2);
run;

proc means data=BootMeans noprint;
var boot_means;
output out = BootStats mean = boot_mean std = boot_se;
run;
data boot_bias_and_se(keep = orig_mean orig_std boot_bias boot_se);
merge BootStats OrigStats;
boot_bias = boot_mean - orig_mean;
run;
proc print data = boot_bias_and_se;
run;

data normal_ci (keep = lower_ci_norm upper_ci_norm);
set boot_bias_and_se;
z_value = quantile('normal', 0.975, 0, 1);
lower_ci_norm = (orig_mean - boot_bias) - z_value * boot_se;
upper_ci_norm = (orig_mean - boot_bias) + z_value * boot_se;
run;

proc univariate data=BootMeans noprint;
var boot_means;
output out=boot_pctiles pctlpre=Pct95_ pctlpts=2.5 97.5 pctlname=Lower Upper;
run;
data percentile_ci(keep = lower_ci_pct upper_ci_pct);
set boot_pctiles;
lower_ci_pct = Pct95_Lower;
upper_ci_pct = Pct95_Upper;
run;

data basic_ci(keep = lower_ci_basic upper_ci_basic);
merge boot_pctiles OrigStats;
lower_ci_basic = 2 * orig_mean - Pct95_Upper;
upper_ci_basic = 2 * orig_mean - Pct95_Lower;
run;

data TValues;
if _N_ = 1 then do;
  set OrigStats;
  set BootMeans;
end;
set BootMeans;
tvalue = sqrt(N) * (boot_means - orig_mean) / boot_stds;
run;
proc univariate data=TValues noprint;
var tvalue;
output out=t_pctiles pctlpts=2.5 97.5 pctlpre=t_pctile_;
run;
data studentized_ci(keep = lower_ci_stu upper_ci_stu);
merge OrigStats t_pctiles;
lower_ci_stu = orig_mean - t_pctile_97_5 * orig_std / sqrt(N);
upper_ci_stu = orig_mean - t_pctile_2_5 * orig_std / sqrt(N);
run;

data combined_ci;
merge normal_ci studentized_ci basic_ci percentile_ci;
run;
proc print data=combined_ci noobs label;
var lower_ci_norm upper_ci_norm lower_ci_stu upper_ci_stu
    lower_ci_basic upper_ci_basic lower_ci_pct upper_ci_pct;
label lower_ci_norm="Normal Approximation Lower"
      upper_ci_norm="Normal Approximation Upper"
      lower_ci_stu="Studentized Lower"
      upper_ci_stu="Studentized Upper"
      lower_ci_basic="Basic Bootstrap Lower"
      upper_ci_basic="Basic Bootstrap Upper"
      lower_ci_pct="Percentile Bootstrap Lower"
      upper_ci_pct="Percentile Bootstrap Upper";
run;
