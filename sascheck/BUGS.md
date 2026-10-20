# Open bugs & conformance failures

Golden-driven worklist. Every entry has a committed golden in
`sascheck/golden/` that shows SAS's exact table columns, row layout and
values — read the JSON next to the entry before fixing. Run one area with:

    go test ./internal/sas -run 'TestSASConformance/<name>' -count=1 -v

Failure lines are `sascheck_test.go:108` (106 lines are informational
"SAS-only hidden columns" notes, not failures).

Last full census: 211 failing diffs across 9 examples; all unit tests and
all 6 probe goldens green (commit 4c0f58b and later).

## 1. PROC GLM — anova_test (27), twoway_test (2)

- `CLDiffsInfo` / `MCLinesInfo` (13 diffs): SAS's rows carry formatted
  character values with fixed widths — Alpha as "%8.2f" (`"    0.05"`),
  DF as `%8.0g`-style (`"      38"`), t values `%8.5f` (`" 3.79925"`).
  sago writes unformatted numbers into the cValue columns. Widths are in
  `golden/examples__anova_test.json` → tables CLDiffsInfo/MCLinesInfo.
- `MCLines` (7): row set and Significance column (numeric in SAS, sago
  emits char).
- `CLDiffs` (3): same Significance type mismatch.
- `Means` (2) / `LSMeans` (1) in twoway_test: sago missing columns
  (`Mean`, `SD`, `LSMean`) — compare against
  `golden/examples__twoway_test.json` table Means/LSMeans.

## 2. PROC FREQ — cat_test (24)

- `CrossTabFreqs` (14): cell layout differs from SAS's ODS shape (Table /
  rows-per-cell with Expected/Deviation/Percent columns) — spec in
  `golden/examples__cat_test.json`.
- `OneWayFreqs` (4): the `F_<var>` column must hold the formatted level
  value per row; sago repeats the variable name.
- `OneWayChiSq` (2) / `OneWayLRChiSq` (1) / `ChiSq` (1) / `FishersExact`
  (2): row sets differ (SAS has one row per statistic; extra/missing rows
  on the sago side — e.g. OneWayChiSq 4 rows vs SAS 3).

## 3. PROC CORR — corr_test (10)

- `SimpleStats` (8): with WITH/parallel lists sago emits extra columns
  (`Sum`, `PartialVar`) and wrong Variable ordering (row 1 `temp` vs
  `dose`). Spec: `golden/examples__corr_test.json` SimpleStats #2.
- `VarInformation` (2): drop sago-only `Label1`/`cValue1` columns.

## 4. PROC GENMOD — genmod_test (24)

Missing tables (one per run, 3 runs): `ModelInfo`, `NObs`, `ModelFit`,
`ConvergenceStatus` — follow the LOGISTIC pattern
(`proc_logistic.go` procCol/emit); exact columns and rows are in
`golden/examples__genmod_test.json`.
`ParameterEstimates` (9): drop the `WaldChiSq` column (SAS's PE table has
Variable/DF/Estimate/StdErr/ProbChiSq layout; a Level1/Model columns check
also needed), and #3 has extra rows (SAS 3 vs sago 2 — likely a missing
class level or intercept row).

## 5. NPAR1WAY EDF family — ks_test (38)

- `KSTest` / `CVMTest` / `KuiperTest` (10): ODS layouts are
  per-class + Total rows (KSTest: Variable/Class/N/EDFatMaximum/
  DevAtMaximum/ObsAtMaximum/ValueAtMaximum — sago still emits the old
  Label/Value pair layout with 5 rows instead of 3).
- `KSStats` / `CVMStats` / `KuiperStats` (10): label-value PAIR layouts
  (Name1/Label1/cValue1/nValue1/Name2/...), values already computed.
- `KS2Stats` (3): produced by SAS, missing in sago (two-sample EDF
  summary: NMAT/D statistics rows).
- `Moments` (6, PROC UNIVARIATE in the same example): cValue width
  formatting (see paired_test below).

## 6. PROC TTEST + PROC UNIVARIATE — paired_test (9)

- `Moments` (4) / `TestsForLocation` (1) / `LocationCounts` (1): the
  label-value pair tables must format cValue/nValue cells with SAS's
  display widths (N as `%8.0f` → `"        12"`) and TestsForLocation
  needs the absolute-value test rows SAS lists.
- `Statistics` / `ConfLimits` / `TTests` (3): sago-only `Variable`,
  `Method`, `Variances` columns — SAS's paired-run ODS tables omit them.

## 7. PROC PHREG — phreg_test (35)

Missing tables entirely (4 runs): `ModelInfo`, `NObs`, `CensoredSummary`,
`ConvergenceStatus`, `FitStatistics` (runs 3-4), plus
`ParameterEstimates` (11: sago emits an older shape) and `GlobalTests`
(6: sago missing or stale shape). Specs in
`golden/examples__phreg_test.json` — note run 2 in SAS errored
(`ties=efron` was a fixed example bug; the golden still carries that
step-stopped log, so the log comparison there is expected until the
golden is re-captured).

## 8. NPAR1WAY class ordering — score_test (42)

- Score tables (`SavageScores`/`ConoverScores`/`STScores` ×2 each) put
  classes in the wrong order when the class values are numeric strings
  (`0` vs `.04`): SAS sorts numerically (0 first, N=16), sago sorts as
  text ('.04' first, N=11). Root cause is class ordering by formatted
  text in `npNpar1way`; sort by the raw Value (Compare) and keep the
  formatted value for display.
- `ConoverAnalysis` / `STAnalysis` (4): chi-square column present but
  row/DF values off after the ordering fix.
- `data set WORK.G4` / `WORK.GOSSYPOL` (2): small data-set differences
  (check row values against golden).

## 9. Stale / missing goldens (harness artifacts, not sago bugs)

- `sascheck/golden/stale/{examples__question1,examples__rng_test,
  examples__reg_test}.json`: captured before the PROC PRINT ODS OUTPUT
  exclusion, so the log carries the harness artifact ERROR "PROC PRINT
  does not support the ODS OUTPUT statement." Re-capture with the current
  browsecap and move back into `sascheck/golden/`.
- Never captured (first attempts crashed the ODA workspace before the
  row-capped dump harness existed; the capped harness should handle
  them): `examples/project1.sas`, `examples/tutorial.sas`. All
  problems/*.sas are also uncaptured.

## 10. Harness / tooling notes (not blocking)

- `browsecap.py` normalize_rule_lines rewrites invalid-data record line
  numbers to program-relative ones; question1's SAS RULE line uses a
  slightly different leading-space count ("RULE:     " with 5 spaces) —
  if a golden needs it, extend the rewriter.
- The read-note whitelist (FREQ/LOGISTIC central + PRINT/MEANS/SORT own)
  may need new entries as more procs get goldens (watch for
  "observations read" diffs in new captures).
- SAS sessions die under heavy runs or idleness; relaunch recipe in the
  section above (welcome tab → Launch → hook XHR → trigger one run to
  harvest SID+CSRF → update this file).
- sago limitations seen while testing but not yet probed/pinned:
  pointer-variable INPUT is rejected; SURVEYSELECT OUTHITS+SRS should
  error ("The OUTHITS option may not be specified with METHOD=SRS") and
  PRINT of an empty data set should note "No variables in data set X"
  (both appear in the rng golden once re-captured).
