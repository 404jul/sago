# sascheck — conformance against real SAS

Runs programs in real SAS, saves the results as golden snapshots, and checks
sago against them in `go test`. You only need SAS to capture a snapshot.
Comparing runs offline.

```
sascheck/
  capture.py   runs programs in SAS via saspy and writes golden/*.json
  probes/      small targeted programs (DATA step semantics, proc defaults)
  golden/      SAS snapshots, committed (created by capture.py)
internal/sas/sascheck_test.go   TestSASConformance: runs sago, diffs against golden/
```

## One-time setup

1. Install Java and saspy: `pip install saspy`.
2. Configure a saspy connection, e.g. SAS OnDemand for Academics (the `oda`
   IOM configuration in saspy's docs). Keep credentials in `~/.authinfo`,
   not in the repository.
3. Check it: `python3 -c "import saspy; s = saspy.SASsession(cfgname='oda'); print(s.sasver)"`.

Check that the ODA terms of use allow this kind of use before relying on them.

## Procedure

```
# 1. capture SAS ground truth (any .sas files inside this repository)
python3 sascheck/capture.py --cfgname oda sascheck/probes/*.sas examples/*.sas problems/*.sas

# 2. compare sago against it
go test ./internal/sas -run TestSASConformance            # all goldens
go test ./internal/sas -run 'TestSASConformance/probes__ds_char' -v

# 3. fix sago until the program passes, then commit the golden with the fix
```

`python3 sascheck/capture.py --print-sas <program>` prints the exact code
submitted to SAS without connecting (no saspy needed).

Golden names come from the program path: `examples/reg_test.sas` →
`golden/examples__reg_test.json`. Capture again whenever a program changes.

### Agent loop

1. Pick a semantic area. Write a small probe in `probes/` that leaves its
   results in WORK data sets (compared exactly) and/or PUT lines (compared
   line by line).
2. Capture it, run the test, and fix sago until the diff is empty.
3. Commit the probe, the golden and the fix together.

Treat SAS as the reference. Fix a probe only if SAS itself rejects it
(an ERROR in the golden log that you didn't intend).

## How a capture works

Each program runs twice, each time in a fresh SAS session. Data files that
the program references as quoted paths (relative, or an absolute path that
doesn't exist locally, resolved by base name the way sago does) are uploaded
to the session's WORK directory, and the literals are rewritten to point
there.

| Pass | Submitted | Used for |
|---|---|---|
| 1 | `ods trace on` + program | names of all output objects |
| 2 | `ods trace on` + `ods output <every table name>(match_all persist=proc)=_SGO<k>X` + program | log, tables, WORK data sets |

Both passes set `options nosource nosource2 pagesize=max linesize=max;
ods graphics off;`. The program is wrapped in `SAGOCHECK-BEGIN` / `-END`
sentinels, so only the program's own log is recorded. A trailing
`run; data _null_ ...; run;` closes a final step that has no RUN.
Afterwards, `dictionary.columns` gives the variable attributes, and each
WORK member is copied with `format _all_;` before download. That way dates
and other formatted values arrive as raw numbers.

## What is compared

| Area | Rule |
|---|---|
| ODS tables | The k-th SAS occurrence of each ODS name is paired with sago's k-th occurrence (each BY group is a separate occurrence). Must match: presence on both sides, producing proc, row count, column types, cell values. Columns only in sago fail; columns only in SAS (often hidden ODS columns) are only logged. ODS Graphics objects are skipped. |
| WORK data sets | Must match: presence on both sides, variable order, type, character length, format, label, row count, cell values. |
| Log | ERROR and WARNING presence. Counts of semantic NOTEs (`logFacts` in `sascheck_test.go`: data set created / observations read, missing values generated, type conversions, uninitialized variable, invalid data/argument, division by zero, MERGE repeats, step stopped). PUT lines are compared exactly, in order. Timing and other incidental notes are ignored. |

Numbers match if `|a-b| <= 1e-10 + 1e-6·max(|a|,|b|)`. Character values are
compared without trailing blanks. Missing numeric values are `null`; saspy
doesn't preserve special missing values (`.A`–`.Z`), so they are compared as
plain missing.

## Known limits

- A program's own `ods output close/clear` or `ods select none` can stop
  SAS from creating harness data sets. Those tables are recorded as
  `captured: false` and checked for existence only.
- If a table name appears a different number of times in the ODS trace than
  as `match_all` data sets, capture.py warns and doesn't compare that table's
  values.
- Only WORK is compared. Files the program writes (`proc export`, `file`)
  are not.
- A log line that is indented six or more spaces right after a NOTE is read
  as part of that NOTE.

## Status

Open conformance failures and harness caveats are listed in
[BUGS.md](BUGS.md); session notes and recovery steps in [WORKLOG.md](WORKLOG.md).
