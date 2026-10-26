# sascheck WORKLOG — browser-capture conformance marathon (2026-10-20)

Goal: robust conformance testing of sago against real SAS (SAS Studio on ODA,
browser session), fixing every diff. Probe+golden+fix committed together.

## Infrastructure (working)

- `sascheck/browsecap.py` — generates the two-pass submissions (same design as
  capture.py: pass1 = ODS trace to learn table names, pass2 = ods output for
  every table + a log-side dump harness that emits @SGOCOL/@SGODS/@SGOC lines
  for all WORK datasets and ODS tables), parses the raw log, writes goldens in
  capture.py's exact JSON schema.
  - pass1: `python3 sascheck/browsecap.py pass1 <prog.sas> > /tmp/x.p1.sas`
  - pass2: `... pass2 <prog.sas> --p1log /tmp/x.p1.log > /tmp/x.p2.sas`
  - golden: `... golden <prog.sas> --p2log /tmp/x.p2.log`
- `/tmp/sgorun_submit.mjs` — node module: submits code to SAS Studio's XHR API
  (POST sasexec/sessions/{sid}/asyncSubmissions), polls sasexec/submissions/{id}
  until COMPLETED, fetches the full log via GET sasexec/submissions/{id}/log,
  converts div-per-line HTML to text (strips ONE artificial leading space per
  line), writes the log file. Used from the node REPL with `agent` passed in.
- SAS Studio session: server odamid-usw2-2, session id (SID)
  `b5793c53-72ba-4d8f-9bf2-a3753d1f96e9` (died), now `17c96f71-c0c9-4a1b-a402-e2bac31aea81`, CSRF `4fb0e108-bd4c-48a7-9060-5b631209df04`.
  If a run errors with HTTP 401/404: the SAS session died — relaunch via the
  welcome.oda.sas.com tab (Launch SAS Studio button), claim the new tab, hook
  XHR (or read any asyncSubmissions URL) to get the new SID + CSRF, update here.
- Logs land in /tmp/sgorun_<name>.p1.log / .p2.log.
- Test: `go test ./internal/sas -run 'TestSASConformance/<name-substring>' -v`

## Findings & fixes

### probes/ds_char_length.sas (captured, golden committed with fix)
SAS compile-time char lengths: `p='abc  '`→$5 (literal with trailing blanks),
`q=p||'x'`→$6, `n='V:'||3`→$2+$12=$14, `cats()`→$200 default, FIRST assignment
in program order wins (`if/then s='a' else s='abcdef'` → $1, values truncated).
sago: used $8 default everywhere, no truncation. Also: SAS emits "NOTE: Numeric
values have been converted to character values" for `||` with numeric operand;
sago doesn't. sago wrongly emits "Variable I is uninitialized" for DO loop vars.

### Commit c9a6574 — 5 probes green
- ds_functions: SCAN compile length = first argument; INTNX alignment
  (BEGINNING/MIDDLE/END/SAMEDAY) implemented.
- ds_log_notes: invalid-data block (NOTE + RULE + record + PDV dump),
  INPUT() invalid-argument note, compile-time div-by-zero fold, PDV order
  (assignment targets first), merge repeats note.
- ds_merge_set: group-wise MERGE (retention within BY group, IN= per group),
  IN=/END=/NOBS= hidden from output, interleaved SET resets other sources.
- ds_put_formats: PUT explicit formats (attach + width, $QUOTE.), BEST12.
  rewrite (leading zero, 1e-4 scientific threshold, shortest round-trip).
- logistic_class_default: LOGISTIC rewritten to ODS OUTPUT shapes; EFFECT
  coding default, FREQ weights, ModelANOVA joint Wald, OR contrasts,
  GCONV stopping (SAS's own convergence slop reproduced deliberately).
  Unit-test tolerances loosened to GCONV precision; global Wald pinned to
  the joint statistic (10.5841... for the chd data).
- browsecap: normalize_rule_lines rewrites the wrapper-dependent line
  numbers of invalid-data records to program-relative numbers.

## Queue

- [ ] fix compile-time char lengths (datastep compiler) + conversion NOTE + spurious uninit NOTE
- [ ] capture+fix remaining probes: ds_functions, ds_log_notes, ds_merge_set, ds_put_formats, logistic_class_default
- [ ] then examples/*.sas, problems/*.sas (need file materialization path: CSV embedded via data step writing ~/__sgoN_name files)
- [ ] new probes for gaps found along the way

## Session end state (2026-10-20, wrap-up)

Green: all unit tests; conformance for probes (6/6), examples anova partially,
diag, dist, lifetest, scale, fp/hl/dscf (npar1way), logistic_class_default.

Remaining failing conformance (with golden specs already in sascheck/golden/
as the worklist — each shows the exact SAS columns/rows to emit):
- examples__anova_test (27): GLM CLDiffs/CLDiffsInfo cValue widths
  (%8.2 alpha etc.), Significance column numeric.
- examples__cat_test (24): FREQ OneWayFreqs (F_<var> holds the level value),
  OneWayChiSq rows.
- examples__corr_test (10): CORR SimpleStats/VarInformation column sets.
- examples__genmod_test (24): GENMOD missing ModelInfo/NObs/ModelFit/
  ConvergenceStatus tables; ParameterEstimates without WaldChiSq column.
- examples__ks_test (38): NPAR1WAY EDF tables (KSTest per-class+Total rows,
  KSStats/CVMStats/KuiperStats label-value pair layouts, KS2Stats).
- examples__paired_test (9): TTEST Statistics/ConfLimits/TTests Variable
  column; UNIVARIATE Moments cValue width formatting.
- examples__phreg_test (35): PHREG ModelInfo/NObs/CensoredSummary/
  ConvergenceStatus/ParameterEstimates/GlobalTests/FitStatistics tables.
- examples__score_test (42): class sort order for numeric classes;
  SavageScores layout largely matches otherwise.
- examples__twoway_test (2): GLM Means/LSMeans columns.

Stale goldens moved to sascheck/golden/stale/ (question1, rng_test,
reg_test): captured before the PROC PRINT ODS OUTPUT exclusion, so they
carry the harness artifact error "PROC PRINT does not support the ODS
OUTPUT statement." Re-capture with the current browsecap (one command:
browsecap pass1/pass2/golden) and move back. Also never captured:
project1, tutorial (their first runs crashed the ODA workspace before the
row-capped dump harness existed; the capped harness should handle them).

Recovery notes: SAS sessions die under load/idle; relaunch via the welcome
tab, hook XHR, trigger one run to harvest SID+CSRF (documented above).

Open bugs and remaining conformance failures are tracked in
[sascheck/BUGS.md](BUGS.md) — keep it current as areas get fixed.

## Session 2026-10-26 — linear models (REG/GLM/ANOVA) in the browser

Scope: linear-model inference. SAS Studio session was dead (browser
fresh, no cookies); sign-in modal left open in the browser pane —
capture steps below assume the user has signed in and SAS Studio is
launched from welcome.oda.sas.com.

Fixed without new captures (goldens already in the repo):
- GLM: CLDiffsInfo/MCLinesInfo cValue1 fixed w.d widths, CLDiffs
  Significance numeric 0/1 (display via new SIG. format), MCLines
  rebuilt to SAS's ODS shape (Line1/Mean/N/Level + hidden Eq1..Eqk
  span indicators, continuation rows between levels, letter centered
  on line i+j for a block spanning levels i..j), plain Means table
  suppressed when mctest options present, Means columns named
  "Mean <dep>"/"SD <dep>" with values only for one-effect models,
  LSMeans column <dep>LSMean. twoway_test fully green; anova_test down
  to the Tukey-q diffs.
- REG (vs stale golden — tables still ground truth): lack of fit/pure
  error rows back inside the ANOVA table (no separate LackFitANOVA),
  CLB replaces Probt with LowerCL/UpperCL (VIF column sits between
  tValue and LowerCL), TestANOVA = Model/Test(hidden)/Source/DF/MS/
  FValue/ProbF/Control with the test label in Test, OUTPUT OUT=
  canonical order P, LCLM, UCLM, LCL, UCL, R, ... with SAS's labels
  ("Lower Bound of 95% C.I. for Mean" etc.). reg_test tables+data
  match the stale golden 100%.
- GLM general CONTRAST/ESTIMATE (commit 84bdd42) via f.testL over the
  over-parameterized columns; ESTIMATE was silently ignored before.
- QTUKEY data step function.

Ground-truth finding: SAS's studentized-range quantile is NOT the
exact distribution — 3.77288547881831 vs exact 3.772928965727 at
(0.95,3,12); sago computes the exact value and needs SAS's algorithm.
Grid queued in probes/lm_inference.sas.

Capture steps when signed in (from the repo root):
  python3 sascheck/browsecap.py pass1 examples/reg_test.sas > /tmp/reg_test.p1.sas
  python3 sascheck/browsecap.py pass1 sascheck/probes/lm_inference.sas > /tmp/lm_inference.p1.sas
  (submit each via the SAS Studio XHR bridge, save logs, then)
  python3 sascheck/browsecap.py pass2 examples/reg_test.sas --p1log /tmp/reg_test.p1.log > /tmp/reg_test.p2.sas
  ... run, then:
  python3 sascheck/browsecap.py golden examples/reg_test.sas --p2log /tmp/reg_test.p2.log
  python3 sascheck/browsecap.py golden sascheck/probes/lm_inference.sas --p2log /tmp/lm_inference.p2.log
  mv golden/stale/examples__reg_test.json aside; re-capture overwrites golden/.
