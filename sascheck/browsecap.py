#!/usr/bin/env python3
"""Capture sascheck goldens through the browser SAS Studio session.

Same output as capture.py, but the submission is generated here, pasted
into SAS Studio by hand (or by an agent driving the browser), run there,
and only the raw log text comes back. Everything the golden needs — the
program log, the ODS trace, every ODS table and every WORK data set — is
recovered from that log: after the SAGOCHECK-END sentinel a dump harness
writes one line per column attribute (@SGOCOL), one header per data set
(@SGODS) and one line per cell (@SGOC) in an unambiguous pipe format.
Numeric cells are BEST32., character cells are $hex. so no value can
corrupt the framing.

  python3 browsecap.py pass1 probes/ds_char_length.sas
      print the first-pass code (ODS trace; learn the table names).
  python3 browsecap.py pass2 probes/ds_char_length.sas --p1log p1.log
      print the second-pass code (ods output for every traced table and
      the dump harness).
  python3 browsecap.py golden probes/ds_char_length.sas --p2log p2.log
      parse the raw second-pass log and write golden/<name>.json.
"""

import argparse
import json
import re
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
import capture  # noqa: E402  (dumps, golden_path, split_trace, is_graph, ...)

REPO = capture.REPO
BEGIN, END, PREFIX = capture.BEGIN, capture.END, capture.PREFIX

# ---------------------------------------------------------------- code generation


def file_prologue(files):
    """Materializes local data files inside the SAS session (home dir) and
    returns (code, literal -> remote path)."""
    code, mapping = [], {}
    for i, (lit, local) in enumerate(sorted(files.items())):
        remote = f"~/__sgo{i}_{re.sub(r'[^A-Za-z0-9._-]', '_', local.name)}"
        lines = local.read_text(errors="replace").splitlines()
        body = "\n".join(f"  put '{l.replace(chr(39), chr(39) * 2)}';" for l in lines)
        code.append(
            f"filename _sgof{i} '{remote}' lrecl=32767;\n"
            f"data _null_;\n  file _sgof{i} lrecl=32767;\n{body}\nrun;\n"
        )
        mapping[lit] = remote
    return "\n".join(code), mapping


RESET = """options nosource nosource2 nodate nonumber pagesize=max linesize=max;
ods graphics off;
ods output close;
ods trace on;
proc datasets lib=work kill nolist; quit;
"""


def submission(src, files, names, run_id, pass2=False):
    """The full editor content for one run. pass2 => dump harness appended."""
    prologue, mapping = file_prologue(files)
    src = capture.rewrite_paths(src, mapping)
    parts = [RESET]
    if prologue:
        parts.append(prologue)
    if names:
        parts.append(capture.capture_statement(names))
    parts.append(capture.wrap_program(src))
    parts.append(f'data _null_; put "@SGORUN {run_id}"; run;\n')
    if pass2:
        parts.append(DUMP_HARNESS)
        parts.append('data _null_; put "@SGOVER &sysvlong"; run;\n')
    return "\n".join(parts)


DUMP_HARNESS = """\
ods trace off;
proc sql;
  create table work._SGOMETA as
    select memname, name, type, length, format, label, varnum
    from dictionary.columns
    where libname = 'WORK' and memtype = 'DATA'
    order by memname, varnum;
quit;
data _null_;
  set work._SGOMETA;
  put "@SGOCOL|" memname "|" name "|" type "|" length "|" format "|" label "|" varnum "|";
run;
data _null_;
  set work._SGOMETA;
  by memname;
  if first.memname then do;
    call execute('data _null_; put "@SGODS|' || trim(memname) || '|"; run;');
    call execute('data _null_; length _c $32767; set work.' || trim(memname) || ';');
  end;
  if type = 'char' then do;
    call execute('_c = putc(trimn(' || trim(name) || '), cats("$hex", 2 * max(lengthn(' || trim(name) || '), 1)));');
    call execute('put "@SGOC|" _n_ "|' || trim(name) || '|C|" _c;');
  end;
  else
    call execute('put "@SGOC|" _n_ "|' || trim(name) || '|N|" ' || trim(name) || ' best16.;');
  if last.memname then call execute('run;');
run;
"""


# ---------------------------------------------------------------- log parsing

SGOCOL = re.compile(r"^@SGOCOL\|(.*)$")
SGODS = re.compile(r"^@SGODS\|([^|]+)\|?\s*$")
SGOC = re.compile(r"^@SGOC\|([^|]*)\|([^|]*)\|([CN])\|(.*)$")
SGOVER = re.compile(r"^@SGOVER (.*)$")


def parse_members(raw):
    """(meta, rows, exists) from the dump-harness lines anywhere in the log.
    meta: member -> [column dict]; rows: member -> [row][colname -> value];
    exists: ordered member names (a header alone means an empty data set)."""
    meta, rowmap, exists = {}, {}, []
    last = None  # member for @SGOC lines, set by each @SGODS header
    for line in raw.splitlines():
        line = line.strip()
        if m := SGOCOL.match(line):
            parts = m.group(1).split("|")
            if parts and parts[-1] == "":
                parts.pop()  # the emitter terminates every line with a pipe
            if len(parts) < 7:
                continue
            mem, name, typ, length, fmt, label = parts[:6]
            varnum = parts[-1]
            try:
                length, varnum = int(length), int(varnum)
            except ValueError:
                continue
            label = "|".join(parts[5:-1])  # labels may contain pipes
            meta.setdefault(mem.strip(), []).append(
                {
                    "name": name.strip(),
                    "type": "char" if typ.strip().lower() == "char" else "num",
                    "length": length,
                    "format": fmt.strip(),
                    "label": label.strip(),
                }
            )
        elif m := SGODS.match(line):
            last = m.group(1).strip()
            if last not in rowmap:
                exists.append(last)
            rowmap.setdefault(last, {})
        elif m := SGOC.match(line):
            row, col, kind, val = m.groups()
            if last is None:
                continue
            row = int(row)
            rows = rowmap[last].setdefault(row, {})
            if kind == "C":
                try:
                    v = bytes.fromhex(val.strip()).decode("utf-8", "replace").rstrip(" ")
                except ValueError:
                    v = ""
                rows[col.strip()] = v
            else:
                val = val.strip()
                try:
                    rows[col.strip()] = float(val)
                except ValueError:
                    rows[col.strip()] = None
    return meta, rowmap, exists


def frame_rows(rows_by_mem, mem, cols):
    rows = rows_by_mem.get(mem, {})
    return [[rows.get(r, {}).get(c["name"]) for c in cols] for r in sorted(rows)]


def build_snapshot(raw, program):
    prog = Path(program).resolve()
    lines = raw.splitlines()
    region = capture.program_log(raw)
    trace, rest = capture.split_trace(region)
    meta, rowmap, exists = parse_members(raw)

    names = []
    for e in trace:
        n = e.get("name", "")
        if n and not capture.is_graph(e) and n.upper() not in (x.upper() for x in names):
            names.append(n)

    captured = {}
    for k, name in enumerate(names):
        pat = re.compile(rf"^{PREFIX}{k}X(\d*)$")
        hits = sorted(
            (int(m.group(1) or -1), mem)
            for mem in exists
            if (m := pat.match(mem))
        )
        captured[name.upper()] = [mem for _, mem in hits]

    counts = {}
    for e in trace:
        counts[e.get("name", "").upper()] = counts.get(e.get("name", "").upper(), 0) + 1
    for key, mems in captured.items():
        if mems and len(mems) != counts.get(key, 0):
            print(
                f"warning: {key}: {counts.get(key, 0)} trace entries but {len(mems)} data sets; not comparing its values",
                file=sys.stderr,
            )
            captured[key] = []

    tables, used = [], {}
    for e in trace:
        name = e.get("name", "")
        key = name.upper()
        t = {
            "name": name,
            "proc": e.get("path", "").split(".")[0],
            "path": e.get("path", ""),
            "template": e.get("template", ""),
        }
        if capture.is_graph(e):
            t["kind"] = "graph"
        i = used.get(key, 0)
        used[key] = i + 1
        mems = captured.get(key, [])
        t["captured"] = i < len(mems)
        if t["captured"]:
            mem = mems[i]
            t["columns"] = [{"name": c["name"], "type": c["type"]} for c in meta.get(mem, [])]
            t["rows"] = frame_rows(rowmap, mem, meta.get(mem, []))
        tables.append(t)

    datasets = [
        {"name": mem, "columns": meta[mem], "rows": frame_rows(rowmap, mem, meta[mem])}
        for mem in sorted(exists)
        if not mem.startswith(PREFIX)
    ]

    version = ""
    for line in lines:
        if m := SGOVER.match(line.strip()):
            version = m.group(1).strip()

    return {
        "engine": "sas",
        "program": prog.relative_to(REPO).as_posix(),
        "version": version,
        "log": capture.parse_log(rest),
        "tables": tables,
        "datasets": datasets,
    }


# ---------------------------------------------------------------- cli


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    sub = ap.add_subparsers(dest="cmd", required=True)

    p1 = sub.add_parser("pass1", help="print the first-pass submission")
    p1.add_argument("program")

    p2 = sub.add_parser("pass2", help="print the second-pass submission")
    p2.add_argument("program")
    p2.add_argument("--p1log", required=True, help="raw log text of the pass-1 run")
    p2.add_argument("--run-id", default="p2")

    g = sub.add_parser("golden", help="write golden/<name>.json from a pass-2 log")
    g.add_argument("program")
    g.add_argument("--p2log", required=True)

    args = ap.parse_args()
    prog = Path(args.program).resolve()
    src = prog.read_text()
    files = capture.data_files(src, prog.parent)

    if args.cmd == "pass1":
        print(submission(src, files, [], "p1"))
        return 0

    if args.cmd == "pass2":
        raw = Path(args.p1log).read_text()
        trace, _ = capture.split_trace(capture.program_log(raw))
        names = []
        for e in trace:
            n = e.get("name", "")
            if n and not capture.is_graph(e) and n.upper() not in (x.upper() for x in names):
                names.append(n)
        print(submission(src, files, names, args.run_id, pass2=True))
        return 0

    raw = Path(args.p2log).read_text()
    snap = build_snapshot(raw, args.program)
    out = capture.golden_path(args.program, str(capture.GOLDEN_DIR))
    out.parent.mkdir(parents=True, exist_ok=True)
    out.write_text(capture.dumps(snap) + "\n")
    print(f"ok {args.program} -> {out.relative_to(REPO)}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
