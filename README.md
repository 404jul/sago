<p align="center">
  <img src="assets/logo.png" width="140" alt="sago logo">
</p>

<h1 align="center">sago</h1>

<p align="center">
  <strong>A SAS interpreter in Go.</strong><br>
  Run <code>.sas</code> programs, get a single self-contained HTML report —
  tables, charts, and a SAS-style log. SAS algorithms and defaults, modern output.
</p>

<p align="center">
  <a href="LICENSE"><img alt="MIT License" src="https://img.shields.io/badge/license-MIT-blue.svg"></a>
  <img alt="Go 1.23+" src="https://img.shields.io/badge/Go-1.23%2B-00ADD8?logo=go&logoColor=white">
  <img alt="Verified against scipy / statsmodels / R / SAS" src="https://img.shields.io/badge/verified%20against-scipy%20%C2%B7%20statsmodels%20%C2%B7%20R%20%C2%B7%20SAS-8A2BE2">
</p>

## Quick start

```
go build -o sago ./cmd/sago
./sago program.sas                 # writes program.html next to it
./sago -o report.html program.sas  # explicit output ('-' = stdout)
./sago -q program.sas              # do not echo the log to stderr
```

A taste — drop this in `hello.sas` and run `./sago hello.sas`:

```sas
data class;
  set sashelp.class;
  bmi = 703 * weight / (height * height);
run;

proc means data=class n mean std min max;
  var height weight bmi;
run;

proc sgplot data=class;
  scatter x=height y=weight / group=sex;
run;
```

You get an HTML report with the MEANS table and an SVG scatter, plus a SAS-style
log. Bundled `SASHELP` members: `CLASS`, `CARS`, `IRIS`, `HEART`. Relative file
paths resolve against the program's directory; absolute paths that do not exist
(e.g. SAS OnDemand `/home/u.../x.csv`) fall back to the file's base name in the
program directory.

## What runs

| | |
|---|---|
| **DATA step** | PDV/compile-time typing · `SET` (concatenation, BY interleave, multiple independent streams, `END=`/`IN=`/`NOBS=`) · `MERGE` (BY match-merge and positional) · `INFILE`/`INPUT` (list/column/formatted, `:informat`, `@`/`@@`, `DLM=`/`DSD`/`MISSOVER`/`TRUNCOVER`/`STOPOVER`) · `DATALINES` · `IF/THEN/ELSE`, subsetting `IF`, `WHERE` · iterative `DO`, `DO WHILE/UNTIL`, `SELECT` · arrays, `RETAIN`, sum statement · `KEEP/DROP/RENAME/LENGTH/LABEL/FORMAT/ATTRIB` · `OUTPUT/DELETE/STOP/RETURN/LEAVE/CONTINUE` · `PUT` · `CALL STREAMINIT/RANUNI/RANNOR/RANBIN/.../SYMPUT` · `LAG/DIF`, `first.`/`last.` · SAS log notes (missing values generated, invalid data, observations read, ...) |
| **Statistics** | `FREQ` (n-way tables, `CHISQ`, Fisher's exact, `TESTP/TESTF`, `OUT=`, ODS) · `NPAR1WAY` (`WILCOXON`, `MEDIAN`, `ANOVA`, `VW`, `SAVAGE`, `MOOD`, `KLOTZ`, `AB`, `ST`, `CONOVER`, `HL`, `FP`, `DSCF`, `EDF`/`KS` incl. Cramer-von Mises + Kuiper) · `LIFETEST` (Kaplan–Meier, Greenwood SE, log-rank/Wilcoxon/−2Log(LR)) · `PHREG` (Cox, Breslow/Efron ties) · `GENMOD` (Poisson/log, `OFFSET=`, GOF) · `TTEST` (one-sample, pooled + Satterthwaite, `PAIRED`) · `UNIVARIATE` (Moments, quantiles, normality tests, `OUTPUT OUT=`, `QQPLOT`/`HISTOGRAM`) · `CORR` (Pearson/Spearman/Kendall, `PARTIAL`, `WITH`) · `REG` (`NOINT/CLB/LACKFIT`, `TEST`, diagnostics + plots) · `GLM`/`ANOVA` (Type I/III SS, interactions/ANCOVA, `MEANS ... /HOVTEST=`, LSMEANS) · `MODEL` (nonlinear OLS, `WHITE`, `BREUSCH=`) |
| **Base procs** | `PRINT` (BY/ID/SUM/NOOBS/OBS=) · `SORT` (`OUT=`, `NODUPKEY`) · `MEANS`/`SUMMARY` (full statistic list, CLASS/BY/WEIGHT/FREQ, `OUTPUT OUT=` incl. AUTONAME, `_TYPE_/_FREQ_`) · `CONTENTS` · `IMPORT`/`EXPORT` · `SURVEYSELECT` (SRS/URS/SYS, `REPS=`) · `DATASETS` |
| **Graphics** | `SGPLOT` (SCATTER/SERIES/REG/HISTOGRAM/DENSITY/VBOX/HBOX/VBAR/HBAR/REFLINE/LINEPARM, `GROUP=`, axis statements) · `SGPANEL` (`PANELBY` grid) · `SGSCATTER` (`PLOT`/`COMPARE`/`MATRIX`) · `PLOT`/`GPLOT` (SVG scatters) |
| **SQL** | `SELECT` (joins, GROUP BY/HAVING/ORDER BY, DISTINCT, subqueries, UNION, CASE, aggregates incl. `MEAN(expr)`, remerging) · `CREATE TABLE AS/LIKE` · `DROP TABLE` · `FORMAT=`/`LABEL=` · `CALCULATED` |
| **Global** | `%LET`/`&macro` · `TITLE`/`FOOTNOTE` · `OPTIONS` · `LIBNAME` · `FILENAME` · `ODS SELECT/EXCLUDE/OUTPUT/GRAPHICS` (tables become data sets) |

`RANUNI` is bit-exact with SAS (multiplicative generator 397204094 mod
2³¹−1); `RANNOR/RANBIN/RANPOI/...` follow the SAS constructions; `RAND`
uses MT19937 (deterministic, not bit-identical to SAS).

## Verified numbers

Output is checked against scipy/statsmodels (t-tests, chi-square, Fisher,
Mann-Whitney/Kruskal, regression, ANOVA Type I/III, Tukey HSD, Levene,
Shapiro-Wilk, correlations, White/Breusch-Pagan, Mood/Ansari-Bradley scale
tests), against published SAS/STAT output (NPAR1WAY Savage, Hodges-Lehmann,
LIFETEST Kaplan-Meier), the documented SAS formulas (Fligner-Policello), and
independent R implementations PMCMRplus (DSCF), survival (Kaplan-Meier, Cox),
and glm (Poisson) — see `*_test.go`.

Run the whole suite:

```
go test ./internal/sas
```

## Examples

Every file in [`examples/`](examples/) is a runnable program; each one
exercises a feature area and doubles as a regression test.

| Program | Demonstrates |
|---|---|
| [`tutorial.sas`](examples/tutorial.sas) | every example from the STAT 6337 SAS tutorial |
| [`project1.sas`](examples/project1.sas) + [`auto-1.csv`](examples/auto-1.csv) | end-to-end analysis project |
| [`ks_test.sas`](examples/ks_test.sas) | Kolmogorov-Smirnov: two-sample, k-sample, one-sample normality |
| [`cat_test.sas`](examples/cat_test.sas) | chi-square goodness of fit, 2×2 + Fisher's exact, 3×3 association |
| [`reg_test.sas`](examples/reg_test.sas) | REG: CLB, lack-of-fit, collinearity/VIF, TEST statement, prediction limits |
| [`anova_test.sas`](examples/anova_test.sas) | unbalanced one-way ANOVA: Tukey-Kramer/Bonferroni/Scheffe CLDiffs, Levene HOVTEST, Tukey lines |
| [`paired_test.sas`](examples/paired_test.sas) | paired t test, sign and exact Wilcoxon signed-rank with zeros and ties |
| [`twoway_test.sas`](examples/twoway_test.sas) | unbalanced two-way ANOVA: Type I vs Type III SS, LSMEANS |
| [`corr_test.sas`](examples/corr_test.sas) | Pearson/Spearman/Kendall on a saturating dose-response, PARTIAL correlations |
| [`diag_test.sas`](examples/diag_test.sas) | REG influence diagnostics: RStudent, Cook's D, leverage, DFFITS, PRESS |
| [`dist_test.sas`](examples/dist_test.sas) | DATA step distribution functions: PROBT/PROBCHI/PROBF, TINV/CINV/FINV, QUANTILE, PROBBNML, POISSON |
| [`rng_test.sas`](examples/rng_test.sas) | RANUNI/RANNOR stream semantics, RAND NORMAL, SURVEYSELECT SRS/SYS/URS |
| [`scale_test.sas`](examples/scale_test.sas) | NPAR1WAY Mood/Klotz/Ansari-Bradley two-sample scale tests |
| [`score_test.sas`](examples/score_test.sas) | NPAR1WAY Savage/Siegel-Tukey/Conover, verified against published SAS chapter figures |
| [`hl_test.sas`](examples/hl_test.sas) | NPAR1WAY Hodges-Lehmann location shift with Moses/exact confidence limits |
| [`fp_test.sas`](examples/fp_test.sas) | NPAR1WAY Fligner-Policello robust median test |
| [`dscf_test.sas`](examples/dscf_test.sas) | NPAR1WAY DSCF all-pairs multiple comparisons, cross-checked against R PMCMRplus |
| [`lifetest_test.sas`](examples/lifetest_test.sas) | LIFETEST: Kaplan-Meier, verified against published SAS figures and R survival |
| [`phreg_test.sas`](examples/phreg_test.sas) | PHREG: Cox regression, Breslow/Efron ties, verified against SAS and R coxph |
| [`genmod_test.sas`](examples/genmod_test.sas) | GENMOD: Poisson regression with exposure offset, verified against R glm |

## Layout

```
cmd/sago          CLI
internal/sas      the emulator (lexer, DATA step, procs, stats, HTML/SVG)
examples/         runnable .sas programs (see table above)
```

## License

[MIT](LICENSE)
