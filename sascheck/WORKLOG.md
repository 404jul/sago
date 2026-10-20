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
  `b5793c53-72ba-4d8f-9bf2-a3753d1f96e9`, CSRF `f9530166-d262-4168-ac3f-e400efe0a1a9`.
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

## Queue

- [ ] fix compile-time char lengths (datastep compiler) + conversion NOTE + spurious uninit NOTE
- [ ] capture+fix remaining probes: ds_functions, ds_log_notes, ds_merge_set, ds_put_formats, logistic_class_default
- [ ] then examples/*.sas, problems/*.sas (need file materialization path: CSV embedded via data step writing ~/__sgoN_name files)
- [ ] new probes for gaps found along the way
