#!/usr/bin/env python3
"""Run SAS programs in real SAS (via saspy) and record golden snapshots.

Each program is run twice, each time in a fresh SAS session:

  pass 1  ODS TRACE only, to learn the name of every output object;
  pass 2  ODS TRACE plus ODS OUTPUT (MATCH_ALL, PERSIST=PROC) for every
          table name, then the log, the ODS tables and all WORK data sets
          are downloaded.

The snapshot is written to sascheck/golden/<program path with / -> __>.json
and compared against sago by `go test ./internal/sas -run TestSASConformance`.
See sascheck/README.md.
"""

import argparse
import json
import math
import os
import re
import sys
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent
GOLDEN_DIR = REPO / "sascheck" / "golden"

BEGIN = "SAGOCHECK-BEGIN"
END = "SAGOCHECK-END"
PREFIX = "_SGO"  # reserved WORK member prefix for harness data sets

SETUP = """options nosource nosource2 nodate nonumber pagesize=max linesize=max;
ods graphics off;
ods trace on;
"""

TEARDOWN = f"""ods output close;
ods trace off;
proc sql;
  create table work.{PREFIX}META as
    select memname, name, type, length, format, label, varnum
    from dictionary.columns
    where libname = 'WORK' and memtype = 'DATA'
    order by memname, varnum;
quit;
"""

GRAPH_PROCS = {"sgplot", "sgpanel", "sgscatter", "sgrender", "gplot", "gchart", "gcontour", "g3d"}


# ---------------------------------------------------------------- SAS code


def wrap_program(src):
    """The program as submitted: sentinels around it, and a trailing step
    boundary so a final step without RUN (or an interactive proc) finishes
    inside the captured log."""
    return f"%put {BEGIN};\n{src}\n;\nrun;\ndata _null_; put '{END}'; run;\n"


def capture_statement(names):
    """ODS OUTPUT for every table name; table k goes to _SGO<k>X, _SGO<k>X1, ..."""
    if not names:
        return ""
    parts = [f"{n}(match_all persist=proc)={PREFIX}{k}X" for k, n in enumerate(names)]
    return "ods output\n  " + "\n  ".join(parts) + ";\n"


def strip_formats(member):
    return f"data work.{PREFIX}RAW; set work.{member}; format _all_; informat _all_; run;\n"


# ---------------------------------------------------------------- log parsing


def program_log(log):
    """Lines between the BEGIN and END sentinels (the program's own log)."""
    lines = log.splitlines()
    start = next((i + 1 for i, l in enumerate(lines) if l.strip() == BEGIN), None)
    if start is None:
        raise RuntimeError(f"{BEGIN} sentinel not found in the SAS log")
    end = next((i for i in range(start, len(lines)) if lines[i].strip() == END), None)
    if end is None:
        print(f"warning: {END} sentinel not found (unclosed quote or comment?); using the rest of the log", file=sys.stderr)
        end = len(lines)
    return lines[start:end]


def split_trace(lines):
    """Removes ODS TRACE blocks; returns (trace entries, remaining lines)."""
    trace, rest = [], []
    i = 0
    while i < len(lines):
        if lines[i].strip() != "Output Added:":
            rest.append(lines[i])
            i += 1
            continue
        entry = {}
        i += 1
        dashes = 0
        while i < len(lines) and dashes < 2:
            s = lines[i].strip()
            i += 1
            if s.startswith("---"):
                dashes += 1
                continue
            key, sep, value = s.partition(":")
            if sep:
                entry[key.strip().lower()] = value.strip()
        trace.append(entry)
    return trace, rest


def is_graph(entry):
    template = entry.get("template", "").lower()
    proc = entry.get("path", "").split(".")[0].lower()
    return "graphics" in template or template.startswith("sg") or proc in GRAPH_PROCS


TAG = re.compile(r"^(NOTE|WARNING|ERROR)(?: \d+-\d+)?: ?(.*)$")


def parse_log(lines):
    """NOTE/WARNING/ERROR messages (continuation lines, indented six spaces,
    joined) and PUT lines (any other non-blank line)."""
    entries = []
    cur = None
    for line in lines:
        m = TAG.match(line)
        if m:
            cur = {"level": m.group(1), "text": m.group(2).strip()}
            entries.append(cur)
        elif not line.strip():
            cur = None
        elif cur is not None and line.startswith(" " * 6):
            cur["text"] += " " + line.strip()
        else:
            cur = None
            entries.append({"level": "PUT", "text": line.rstrip()})
    return [e for e in entries if PREFIX not in e["text"].upper()]


# ---------------------------------------------------------------- data files


def data_files(src, prog_dir):
    """Quoted literals naming an existing file, resolved the way sago does:
    relative to the program, or an absent absolute path by base name."""
    found = {}
    for m in re.finditer(r"'([^'\n]*)'|\"([^\"\n]*)\"", src):
        lit = m.group(1) if m.group(1) is not None else m.group(2)
        if not lit or len(lit) > 400:
            continue
        p = Path(lit)
        cands = [p] if p.is_absolute() else [prog_dir / p]
        if p.is_absolute() and not p.exists():
            cands.append(prog_dir / p.name)
        for c in cands:
            if c.is_file():
                found[lit] = c
                break
    return found


def rewrite_paths(src, mapping):
    for lit, remote in mapping.items():
        for q in ("'", '"'):
            src = src.replace(q + lit + q, q + remote + q)
    return src


# ---------------------------------------------------------------- saspy


class Session:
    def __init__(self, cfgname):
        import saspy  # imported lazily so --print-sas works without it

        kw = {"results": "TEXT"}
        if cfgname:
            kw["cfgname"] = cfgname
        self.sas = saspy.SASsession(**kw)

    def submit(self, code):
        return self.sas.submit(code, results="TEXT")["LOG"]

    def upload(self, files):
        """Uploads data files into the session's WORK directory; returns
        literal -> remote path."""
        work = self.sas.workpath.rstrip("/\\")
        sep = "\\" if "\\" in work else "/"
        mapping = {}
        for lit, local in files.items():
            remote = f"{work}{sep}{local.name}"
            res = self.sas.upload(str(local), remote, overwrite=True)
            if not res.get("Success"):
                raise RuntimeError(f"upload of {local} failed:\n{res.get('LOG', '')}")
            mapping[lit] = remote
        return mapping

    def fetch(self, member):
        """Rows of WORK.member, unformatted (dates stay numbers)."""
        self.submit(strip_formats(member))
        df = self.sas.sd2df(f"{PREFIX}RAW", "WORK")
        return df

    def close(self):
        self.sas.endsas()


def run_program(session, src, files, names):
    mapping = session.upload(files) if files else {}
    session.submit(SETUP + capture_statement(names))
    return session.submit(wrap_program(rewrite_paths(src, mapping)))


def to_cell(value, char):
    missing = value is None or (isinstance(value, float) and math.isnan(value))
    if char:
        return "" if missing else str(value).rstrip(" ")
    if missing:
        return None
    try:
        return float(value)
    except (TypeError, ValueError):
        return None  # pandas NaT / special values


def frame_rows(df, cols):
    if df is None or len(df) == 0:
        return []
    by_lower = {str(c).lower(): c for c in df.columns}
    series = [df[by_lower[c["name"].lower()]].tolist() for c in cols]
    return [[to_cell(s[r], c["type"] == "char") for s, c in zip(series, cols)] for r in range(len(df))]


def read_meta(session):
    df = session.sas.sd2df(f"{PREFIX}META", "WORK")
    meta = {}
    if df is None:
        return meta
    cols = {str(c).lower(): c for c in df.columns}
    for rec in zip(*(df[cols[k]].tolist() for k in ("memname", "name", "type", "length", "format", "label"))):
        mem, name, typ, length, fmt, label = rec
        meta.setdefault(str(mem).upper(), []).append(
            {
                "name": str(name).strip(),
                "type": "char" if str(typ).strip().lower() == "char" else "num",
                "length": int(length),
                "format": "" if fmt is None or fmt != fmt else str(fmt).strip(),
                "label": "" if label is None or label != label else str(label).strip(),
            }
        )
    return meta


def capture(program, cfgname):
    prog = Path(program).resolve()
    src = prog.read_text()
    files = data_files(src, prog.parent)

    s1 = Session(cfgname)
    try:
        trace1, _ = split_trace(program_log(run_program(s1, src, files, [])))
    finally:
        s1.close()
    names = []
    for e in trace1:
        n = e.get("name", "")
        if n and not is_graph(e) and n.upper() not in (x.upper() for x in names):
            names.append(n)

    s2 = Session(cfgname)
    try:
        trace, rest = split_trace(program_log(run_program(s2, src, files, names)))
        s2.submit(TEARDOWN)
        meta = read_meta(s2)
        members = {m: s2.fetch(m) for m in meta if not m.startswith(PREFIX + "META") and m != PREFIX + "RAW"}
        version = str(getattr(s2.sas, "sasver", ""))
    finally:
        s2.close()

    # match_all data sets of table k, in creation order
    captured = {}
    for k in range(len(names)):
        pat = re.compile(rf"^{PREFIX}{k}X(\d*)$")
        hits = sorted((int(m.group(1) or -1), mem) for mem in members if (m := pat.match(mem)))
        captured[names[k].upper()] = [mem for _, mem in hits]

    counts = {}
    for e in trace:
        counts[e.get("name", "").upper()] = counts.get(e.get("name", "").upper(), 0) + 1
    for key, mems in captured.items():
        if mems and len(mems) != counts.get(key, 0):
            print(f"warning: {key}: {counts.get(key, 0)} trace entries but {len(mems)} data sets; not comparing its values", file=sys.stderr)
            captured[key] = []

    tables, used = [], {}
    for e in trace:
        name = e.get("name", "")
        key = name.upper()
        t = {"name": name, "proc": e.get("path", "").split(".")[0], "path": e.get("path", ""), "template": e.get("template", "")}
        if is_graph(e):
            t["kind"] = "graph"
        i = used.get(key, 0)
        used[key] = i + 1
        mems = captured.get(key, [])
        t["captured"] = i < len(mems)
        if t["captured"]:
            mem = mems[i]
            t["columns"] = [{"name": c["name"], "type": c["type"]} for c in meta[mem]]
            t["rows"] = frame_rows(members[mem], meta[mem])
        tables.append(t)

    datasets = [
        {"name": mem, "columns": meta[mem], "rows": frame_rows(members[mem], meta[mem])}
        for mem in sorted(members)
        if not mem.startswith(PREFIX)
    ]
    return {
        "engine": "sas",
        "program": prog.relative_to(REPO).as_posix(),
        "version": version,
        "log": parse_log(rest),
        "tables": tables,
        "datasets": datasets,
    }


# ---------------------------------------------------------------- output


def dumps(v, indent=0):
    """JSON with one line per row / column: reviewable git diffs."""
    pad = " " * indent
    if isinstance(v, dict):
        if not v:
            return "{}"
        if all(not isinstance(x, (dict, list)) for x in v.values()):
            return json.dumps(v, ensure_ascii=False, allow_nan=False)
        items = [f'{pad} {json.dumps(k)}: {dumps(x, indent + 1)}' for k, x in v.items()]
        return "{\n" + ",\n".join(items) + "\n" + pad + "}"
    if isinstance(v, list):
        if not v:
            return "[]"
        if all(not isinstance(x, (dict, list)) for x in v):
            return json.dumps(v, ensure_ascii=False, allow_nan=False)
        items = [pad + " " + dumps(x, indent + 1) for x in v]
        return "[\n" + ",\n".join(items) + "\n" + pad + "]"
    return json.dumps(v, ensure_ascii=False, allow_nan=False)


def golden_path(program, out_dir):
    rel = Path(program).resolve().relative_to(REPO).with_suffix("")
    return Path(out_dir) / ("__".join(rel.parts) + ".json")


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("programs", nargs="+", help=".sas files inside this repository")
    ap.add_argument("--cfgname", help="saspy configuration name (e.g. oda)")
    ap.add_argument("--out-dir", default=str(GOLDEN_DIR))
    ap.add_argument("--print-sas", action="store_true", help="print the code submitted for the first program and exit")
    args = ap.parse_args()

    if args.print_sas:
        prog = Path(args.programs[0]).resolve()
        src = prog.read_text()
        files = data_files(src, prog.parent)
        demo = {lit: f"<WORK>/{p.name}" for lit, p in files.items()}
        print("/* submit 1 */\n" + SETUP + capture_statement(["<Table1>", "<Table2>"]))
        print("/* submit 2 */\n" + wrap_program(rewrite_paths(src, demo)))
        print("/* submit 3 */\n" + TEARDOWN)
        print("/* per WORK member */\n" + strip_formats("<member>"))
        return 0

    os.makedirs(args.out_dir, exist_ok=True)
    failed = 0
    for program in args.programs:
        try:
            snap = capture(program, args.cfgname)
        except Exception as e:  # report and continue with the next program
            failed += 1
            print(f"FAIL {program}: {e}", file=sys.stderr)
            continue
        out = golden_path(program, args.out_dir)
        out.write_text(dumps(snap) + "\n")
        print(f"ok   {program} -> {out.relative_to(REPO) if out.is_relative_to(REPO) else out}")
    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(main())
