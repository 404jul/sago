package sas

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// SAS conformance: every sascheck/golden/*.json is a snapshot of a program
// run in real SAS (sascheck/capture.py). The program is re-run in sago and
// the log facts, ODS tables and WORK data sets are compared. See
// sascheck/README.md.

const goldenDir = "../../sascheck/golden"

// Numeric tolerance: |a-b| <= absTol + relTol*max(|a|,|b|). Loose enough
// for iterative fits with different convergence paths, tight enough to
// catch any formula or convention difference.
const (
	relTol = 1e-6
	absTol = 1e-10
)

// maxValueDiffs caps reported cell mismatches per table / data set.
const maxValueDiffs = 5

type snapCol struct {
	Name   string `json:"name"`
	Type   string `json:"type"` // "num" | "char"
	Length int    `json:"length,omitempty"`
	Format string `json:"format,omitempty"`
	Label  string `json:"label,omitempty"`
}

// snapTable is one ODS output object. Kind "graph" objects (SAS side) are
// not compared; Captured=false means SAS listed the object in its ODS
// trace but no data set could be made from it (existence-only check).
type snapTable struct {
	Name     string    `json:"name"`
	Proc     string    `json:"proc"`
	Path     string    `json:"path,omitempty"`
	Template string    `json:"template,omitempty"`
	Kind     string    `json:"kind,omitempty"`
	Captured bool      `json:"captured"`
	Columns  []snapCol `json:"columns,omitempty"`
	Rows     [][]any   `json:"rows,omitempty"`
}

type snapDataset struct {
	Name    string    `json:"name"`
	Columns []snapCol `json:"columns"`
	Rows    [][]any   `json:"rows"`
}

// snapLog is a NOTE/WARNING/ERROR message or a PUT line (level "PUT").
type snapLog struct {
	Level string `json:"level"`
	Text  string `json:"text"`
}

// snapshot is the engine-neutral result of running one program. Cell
// values are float64 (numeric), string (character, trailing blanks
// trimmed) or nil (numeric missing).
type snapshot struct {
	Engine   string        `json:"engine"`
	Program  string        `json:"program"` // relative to the repository root
	Version  string        `json:"version,omitempty"`
	Log      []snapLog     `json:"log"`
	Tables   []snapTable   `json:"tables"`
	Datasets []snapDataset `json:"datasets"`
}

func TestSASConformance(t *testing.T) {
	goldens, err := filepath.Glob(filepath.Join(goldenDir, "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(goldens) == 0 {
		t.Skip("no SAS goldens; capture some with sascheck/capture.py")
	}
	for _, g := range goldens {
		t.Run(strings.TrimSuffix(filepath.Base(g), ".json"), func(t *testing.T) {
			raw, err := os.ReadFile(g)
			if err != nil {
				t.Fatal(err)
			}
			var want snapshot
			if err := json.Unmarshal(raw, &want); err != nil {
				t.Fatalf("%s: %v", g, err)
			}
			got, err := sagoSnapshot(filepath.Join("../..", filepath.FromSlash(want.Program)))
			if err != nil {
				t.Fatal(err)
			}
			for _, d := range compareSnapshots(&want, got) {
				if d.info {
					t.Log(d.msg)
				} else {
					t.Error(d.msg)
				}
			}
		})
	}
}

// sagoSnapshot runs a program in a fresh session and records it the way
// capture.py records SAS.
func sagoSnapshot(path string) (*snapshot, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	dir, err := filepath.Abs(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	s := NewSession(dir)
	snap := &snapshot{Engine: "sago", Program: path}
	s.Out.OnTable = func(proc, odsName string, ds *Dataset) {
		cols, rows := snapData(ds)
		snap.Tables = append(snap.Tables, snapTable{Name: odsName, Proc: proc, Captured: true, Columns: cols, Rows: rows})
	}
	s.RunProgram(string(src))

	for _, e := range s.Log.Entries {
		lvl := ""
		switch e.Level {
		case LogNote:
			lvl = "NOTE"
		case LogWarn:
			lvl = "WARNING"
		case LogError:
			lvl = "ERROR"
		case LogPut:
			lvl = "PUT"
		default:
			continue
		}
		snap.Log = append(snap.Log, snapLog{lvl, e.Msg})
	}
	names := s.DatasetNames()
	sort.Strings(names)
	for _, n := range names {
		if strings.Contains(n, ".") { // SASHELP and other librefs
			continue
		}
		cols, rows := snapData(s.Dataset(n))
		snap.Datasets = append(snap.Datasets, snapDataset{Name: n, Columns: cols, Rows: rows})
	}
	return snap, nil
}

func snapData(d *Dataset) ([]snapCol, [][]any) {
	cols := make([]snapCol, len(d.Vars))
	for i, v := range d.Vars {
		typ := "num"
		if v.Char {
			typ = "char"
		}
		cols[i] = snapCol{Name: v.Name, Type: typ, Length: v.Length, Format: v.Format, Label: v.Label}
	}
	rows := make([][]any, len(d.Rows))
	for r, row := range d.Rows {
		out := make([]any, len(row))
		for c, v := range row {
			switch {
			case v.Char:
				out[c] = strings.TrimRight(v.Str, " ")
			case math.IsNaN(v.Num):
				out[c] = nil
			case math.IsInf(v.Num, 0):
				out[c] = strconv.FormatFloat(v.Num, 'g', -1, 64) // never valid SAS; surfaces as a mismatch
			default:
				out[c] = v.Num
			}
		}
		rows[r] = out
	}
	return cols, rows
}

// ---------------------------------------------------------------- comparison

type confDiff struct {
	info bool // reported, but not a failure
	msg  string
}

func compareSnapshots(sas, sago *snapshot) []confDiff {
	var out []confDiff
	out = append(out, compareLog(sas.Log, sago.Log)...)
	out = append(out, compareTables(sas.Tables, sago.Tables)...)
	out = append(out, compareDatasets(sas.Datasets, sago.Datasets)...)
	return out
}

// logFacts are the log messages with semantic content. Timing, version
// and other incidental notes are ignored. Captured groups become part of
// the fact; line/column positions never do.
var logFacts = []struct {
	name string
	re   *regexp.Regexp
}{
	{"data set created", regexp.MustCompile(`(?i)^The data set (\w+\.\w+) has (\d+) observations and (\d+) variables`)},
	{"observations read", regexp.MustCompile(`(?i)^There were (\d+) observations read from the data set (\w+\.\w+)`)},
	{"missing values generated", regexp.MustCompile(`(?i)^Missing values were generated as a result of performing an operation on missing values`)},
	{"numeric to character conversion", regexp.MustCompile(`(?i)^Numeric values have been converted to character values`)},
	{"character to numeric conversion", regexp.MustCompile(`(?i)^Character values have been converted to numeric values`)},
	{"uninitialized variable", regexp.MustCompile(`(?i)^Variable (\w+) is uninitialized`)},
	{"invalid data", regexp.MustCompile(`(?i)^Invalid data for (\w+) in line`)},
	{"invalid numeric data", regexp.MustCompile(`(?i)^Invalid numeric data, '(.*?)'`)},
	{"invalid argument", regexp.MustCompile(`(?i)^Invalid argument to function (\w+)`)},
	{"division by zero", regexp.MustCompile(`(?i)^Division by zero detected`)},
	{"math operations set to missing", regexp.MustCompile(`(?i)^Mathematical operations could not be performed`)},
	{"merge repeats of BY values", regexp.MustCompile(`(?i)^MERGE statement has more than one data set with repeats of BY values`)},
	{"step stopped by errors", regexp.MustCompile(`(?i)^The SAS System stopped processing this step because of errors`)},
}

var wsRun = regexp.MustCompile(`\s+`)

func logFactCounts(log []snapLog) map[string]int {
	m := map[string]int{}
	for _, e := range log {
		if e.Level == "PUT" {
			continue
		}
		text := wsRun.ReplaceAllString(strings.TrimSpace(e.Text), " ")
		for _, f := range logFacts {
			if g := f.re.FindStringSubmatch(text); g != nil {
				key := f.name
				if len(g) > 1 {
					key += " [" + strings.ToUpper(strings.Join(g[1:], " ")) + "]"
				}
				m[key]++
				break
			}
		}
	}
	return m
}

func logTexts(log []snapLog, level string) []string {
	var out []string
	for _, e := range log {
		if e.Level == level {
			out = append(out, strings.TrimRight(e.Text, " "))
		}
	}
	return out
}

func compareLog(sas, sago []snapLog) []confDiff {
	var out []confDiff
	for _, lvl := range []string{"ERROR", "WARNING"} {
		a, b := logTexts(sas, lvl), logTexts(sago, lvl)
		if (len(a) > 0) != (len(b) > 0) {
			out = append(out, confDiff{msg: fmt.Sprintf("log: SAS has %d %s(s) %q; sago has %d %q", len(a), lvl, a, len(b), b)})
		}
	}
	fa, fb := logFactCounts(sas), logFactCounts(sago)
	keys := map[string]bool{}
	for k := range fa {
		keys[k] = true
	}
	for k := range fb {
		keys[k] = true
	}
	sorted := make([]string, 0, len(keys))
	for k := range keys {
		sorted = append(sorted, k)
	}
	sort.Strings(sorted)
	for _, k := range sorted {
		if fa[k] != fb[k] {
			out = append(out, confDiff{msg: fmt.Sprintf("log NOTE %s: SAS %d time(s), sago %d", k, fa[k], fb[k])})
		}
	}
	pa, pb := logTexts(sas, "PUT"), logTexts(sago, "PUT")
	for i := 0; i < len(pa) || i < len(pb); i++ {
		var a, b string
		if i < len(pa) {
			a = pa[i]
		}
		if i < len(pb) {
			b = pb[i]
		}
		if a != b || i >= len(pa) || i >= len(pb) {
			out = append(out, confDiff{msg: fmt.Sprintf("log PUT line %d (of SAS %d, sago %d): SAS %q, sago %q", i+1, len(pa), len(pb), a, b)})
			break
		}
	}
	return out
}

// compareTables pairs the k-th SAS occurrence of each ODS name with the
// k-th sago occurrence (BY groups are separate occurrences on both sides).
func compareTables(sas, sago []snapTable) []confDiff {
	var out []confDiff
	sagoBy := map[string][]snapTable{}
	for _, t := range sago {
		k := strings.ToUpper(t.Name)
		sagoBy[k] = append(sagoBy[k], t)
	}
	seen := map[string]int{}
	for _, st := range sas {
		if st.Kind == "graph" {
			continue
		}
		k := strings.ToUpper(st.Name)
		i := seen[k]
		seen[k]++
		where := fmt.Sprintf("table %s #%d (PROC %s)", st.Name, i+1, strings.ToUpper(st.Proc))
		if i >= len(sagoBy[k]) {
			out = append(out, confDiff{msg: where + ": produced by SAS, missing in sago"})
			continue
		}
		gt := sagoBy[k][i]
		if !strings.EqualFold(st.Proc, gt.Proc) {
			out = append(out, confDiff{msg: fmt.Sprintf("%s: sago produced it from PROC %s (tables out of step)", where, gt.Proc)})
			continue
		}
		if !st.Captured {
			continue
		}
		out = append(out, compareData(where, st.Columns, st.Rows, gt.Columns, gt.Rows, false)...)
	}
	for _, t := range sago {
		k := strings.ToUpper(t.Name)
		if seen[k] > 0 {
			seen[k]--
			continue
		}
		out = append(out, confDiff{msg: fmt.Sprintf("table %s (PROC %s): produced by sago, not by SAS", t.Name, t.Proc)})
	}
	return out
}

func compareDatasets(sas, sago []snapDataset) []confDiff {
	var out []confDiff
	sagoBy := map[string]snapDataset{}
	for _, d := range sago {
		sagoBy[strings.ToUpper(d.Name)] = d
	}
	for _, sd := range sas {
		k := strings.ToUpper(sd.Name)
		where := "data set WORK." + k
		gd, ok := sagoBy[k]
		if !ok {
			out = append(out, confDiff{msg: where + ": created by SAS, missing in sago"})
			continue
		}
		delete(sagoBy, k)
		out = append(out, compareData(where, sd.Columns, sd.Rows, gd.Columns, gd.Rows, true)...)
	}
	extra := make([]string, 0, len(sagoBy))
	for k := range sagoBy {
		extra = append(extra, k)
	}
	sort.Strings(extra)
	for _, k := range extra {
		out = append(out, confDiff{msg: "data set WORK." + k + ": created by sago, not by SAS"})
	}
	return out
}

// compareData compares columns (by name, case-insensitive) and cells.
// Data sets also compare variable order and attributes; for ODS tables,
// SAS-only columns (often hidden ODS columns) are informational.
func compareData(where string, sc []snapCol, sr [][]any, gc []snapCol, gr [][]any, attrs bool) []confDiff {
	var out []confDiff
	gIdx := map[string]int{}
	for i, c := range gc {
		gIdx[strings.ToUpper(c.Name)] = i
	}
	sIdx := map[string]int{}
	for i, c := range sc {
		sIdx[strings.ToUpper(c.Name)] = i
	}
	type pair struct {
		name string
		s, g int
	}
	var shared []pair
	var sasOnly, sagoOnly []string
	for i, c := range sc {
		if j, ok := gIdx[strings.ToUpper(c.Name)]; ok {
			shared = append(shared, pair{c.Name, i, j})
		} else {
			sasOnly = append(sasOnly, c.Name)
		}
	}
	for _, c := range gc {
		if _, ok := sIdx[strings.ToUpper(c.Name)]; !ok {
			sagoOnly = append(sagoOnly, c.Name)
		}
	}
	if len(sasOnly) > 0 {
		out = append(out, confDiff{info: !attrs, msg: fmt.Sprintf("%s: columns only in SAS: %s", where, strings.Join(sasOnly, " "))})
	}
	if len(sagoOnly) > 0 {
		out = append(out, confDiff{msg: fmt.Sprintf("%s: columns only in sago: %s", where, strings.Join(sagoOnly, " "))})
	}
	if attrs && len(sasOnly) == 0 && len(sagoOnly) == 0 {
		for i, p := range shared {
			if p.g != i {
				out = append(out, confDiff{msg: fmt.Sprintf("%s: variable order: SAS %s, sago %s", where, colNames(sc), colNames(gc))})
				break
			}
		}
	}
	for _, p := range shared {
		a, b := sc[p.s], gc[p.g]
		if a.Type != b.Type {
			out = append(out, confDiff{msg: fmt.Sprintf("%s: %s type: SAS %s, sago %s", where, p.name, a.Type, b.Type)})
			continue
		}
		if !attrs {
			continue
		}
		if a.Type == "char" && a.Length != b.Length {
			out = append(out, confDiff{msg: fmt.Sprintf("%s: %s length: SAS $%d, sago $%d", where, p.name, a.Length, b.Length)})
		}
		if normFormat(a.Format) != normFormat(b.Format) {
			out = append(out, confDiff{msg: fmt.Sprintf("%s: %s format: SAS %q, sago %q", where, p.name, a.Format, b.Format)})
		}
		if a.Label != b.Label {
			out = append(out, confDiff{msg: fmt.Sprintf("%s: %s label: SAS %q, sago %q", where, p.name, a.Label, b.Label)})
		}
	}
	if len(sr) != len(gr) {
		out = append(out, confDiff{msg: fmt.Sprintf("%s: SAS %d rows, sago %d", where, len(sr), len(gr))})
	}
	n := 0
	for r := 0; r < len(sr) && r < len(gr); r++ {
		for _, p := range shared {
			if sc[p.s].Type != gc[p.g].Type {
				continue
			}
			a, b := sr[r][p.s], gr[r][p.g]
			if sameCell(a, b) {
				continue
			}
			if n < maxValueDiffs {
				out = append(out, confDiff{msg: fmt.Sprintf("%s: row %d %s: SAS %s, sago %s", where, r+1, p.name, cellString(a), cellString(b))})
			}
			n++
		}
	}
	if n > maxValueDiffs {
		out = append(out, confDiff{msg: fmt.Sprintf("%s: ... %d more cell mismatches", where, n-maxValueDiffs)})
	}
	return out
}

func colNames(cs []snapCol) string {
	names := make([]string, len(cs))
	for i, c := range cs {
		names[i] = c.Name
	}
	return strings.Join(names, " ")
}

// normFormat makes "date9", "DATE9." and "Date9." equal.
func normFormat(f string) string {
	f = strings.ToUpper(strings.TrimSpace(f))
	if f != "" && !strings.Contains(f, ".") {
		f += "."
	}
	return f
}

func sameCell(a, b any) bool {
	switch x := a.(type) {
	case nil:
		return b == nil
	case float64:
		y, ok := b.(float64)
		return ok && (x == y || math.Abs(x-y) <= absTol+relTol*math.Max(math.Abs(x), math.Abs(y)))
	case string:
		y, ok := b.(string)
		return ok && strings.TrimRight(x, " ") == strings.TrimRight(y, " ")
	}
	return false
}

func cellString(v any) string {
	switch x := v.(type) {
	case nil:
		return "."
	case float64:
		return strconv.FormatFloat(x, 'g', -1, 64)
	case string:
		return strconv.Quote(x)
	}
	return fmt.Sprint(v)
}
