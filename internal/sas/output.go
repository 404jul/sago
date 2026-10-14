package sas

import (
	"fmt"
	"math"
	"strings"
)

// ---------------------------------------------------------------- Log

type LogLevel int

const (
	LogSource LogLevel = iota
	LogNote
	LogWarn
	LogError
	LogPut
)

type LogEntry struct {
	Level LogLevel
	Line  int
	Msg   string
}

type Log struct {
	Entries []LogEntry
	Errors  int
	Warns   int
	// Echo, if set, receives each entry as it is logged (CLI stderr).
	Echo func(LogEntry)
}

func (l *Log) add(e LogEntry) {
	l.Entries = append(l.Entries, e)
	if l.Echo != nil {
		l.Echo(e)
	}
}

func (l *Log) Note(line int, msg string) { l.add(LogEntry{LogNote, line, msg}) }
func (l *Log) Warn(line int, msg string) {
	l.Warns++
	l.add(LogEntry{LogWarn, line, msg})
}
func (l *Log) Error(line int, msg string) {
	l.Errors++
	l.add(LogEntry{LogError, line, msg})
}
func (l *Log) Put(msg string)                     { l.add(LogEntry{LogPut, 0, msg}) }
func (l *Log) Source(line int, text string)       { l.add(LogEntry{LogSource, line, text}) }
func (l *Log) Notef(line int, f string, a ...any) { l.Note(line, fmt.Sprintf(f, a...)) }

func (e LogEntry) String() string {
	switch e.Level {
	case LogNote:
		return "NOTE: " + e.Msg
	case LogWarn:
		return "WARNING: " + e.Msg
	case LogError:
		if e.Line > 0 {
			return fmt.Sprintf("ERROR (line %d): %s", e.Line, e.Msg)
		}
		return "ERROR: " + e.Msg
	case LogSource:
		return fmt.Sprintf("%4d  %s", e.Line, e.Msg)
	}
	return e.Msg
}

// ---------------------------------------------------------------- Output items

// Col describes a table column. Name is the variable name used when the
// table is captured with ODS OUTPUT; Label is the displayed header.
type Col struct {
	Name   string
	Label  string
	Format string // SAS format applied to numeric cells ("" = BEST12.)
	Char   bool
	Hidden bool // captured by ODS OUTPUT but not displayed
}

// Span is a spanning header above columns From..To (inclusive, display indices).
type Span struct {
	Label    string
	From, To int
}

// Table is a rendered result table (one ODS output object).
type Table struct {
	ODSName   string // e.g. "Moments", "ChiSq", "TTests"
	Caption   string // title displayed above the table
	Cols      []Col
	Rows      [][]Value
	RowHeader int // leading columns rendered as row headers
	Spans     []Span
	Notes     []string
	MissDot   bool // show numeric missing as "." (PRINT); otherwise blank
	ObsCol    bool // unused marker for PRINT-like tables
}

// AddRow appends a row of values (convenience: accepts Value, float64, int, string).
func (t *Table) AddRow(cells ...any) {
	row := make([]Value, len(cells))
	for i, c := range cells {
		row[i] = ToValue(c)
	}
	t.Rows = append(t.Rows, row)
}

// ToValue converts Go values to SAS values.
func ToValue(c any) Value {
	switch x := c.(type) {
	case Value:
		return x
	case float64:
		return NumV(x)
	case int:
		return NumV(float64(x))
	case int64:
		return NumV(float64(x))
	case string:
		return StrV(x)
	case bool:
		return BoolV(x)
	case nil:
		return Missing
	}
	return StrV(fmt.Sprint(c))
}

// Cell formats a cell for display.
func (t *Table) Cell(r, c int) string {
	v := t.Rows[r][c]
	if !v.Char && math.IsNaN(v.Num) {
		if t.MissDot {
			return "."
		}
		return ""
	}
	return FormatByName(v, t.Cols[c].Format)
}

// ToDataset converts the table to a dataset (ODS OUTPUT).
func (t *Table) ToDataset(name string) *Dataset {
	d := NewDataset(name)
	for i, c := range t.Cols {
		char := c.Char
		if !char && len(t.Rows) > 0 {
			char = t.Rows[0][i].Char
		}
		nm := c.Name
		if nm == "" {
			nm = sanitizeName(c.Label)
		}
		v := &Var{Name: nm, Char: char, Length: 8, Format: c.Format, Label: c.Label}
		if char {
			for _, r := range t.Rows {
				if l := len(r[i].Str); l > v.Length {
					v.Length = l
				}
			}
		}
		d.Vars = append(d.Vars, v)
	}
	for _, r := range t.Rows {
		d.Rows = append(d.Rows, append([]Value(nil), r...))
	}
	return d
}

func sanitizeName(s string) string {
	var b strings.Builder
	for i := range len(s) {
		c := s[i]
		if isIdentChar(c) {
			b.WriteByte(c)
		} else if b.Len() > 0 && !strings.HasSuffix(b.String(), "_") {
			b.WriteByte('_')
		}
	}
	out := strings.Trim(b.String(), "_")
	if out == "" || isDigit(out[0]) {
		out = "_" + out
	}
	if len(out) > 32 {
		out = out[:32]
	}
	return out
}

// Graph is an SVG figure.
type Graph struct {
	ODSName string
	Caption string
	SVG     string
}

// Text is a block of text (e.g. BY lines, notes, PROC PLOT output).
type Text struct {
	Text  string
	Pre   bool   // preformatted (monospace)
	Class string // "byline", "note", ...
}

// HTML is raw HTML (escape hatch for complex layouts such as crosstabs).
// If Table is set, it is used for ODS OUTPUT capture under its ODSName.
type HTML struct {
	HTML  string
	Table *Table
}

// Section is the output of one procedure invocation.
type Section struct {
	Proc      string
	Titles    []string
	Footnotes []string
	Items     []any
}

// Output collects results and handles ODS SELECT / ODS OUTPUT.
type Output struct {
	Sections []*Section
	cur      *Section
	sess     *Session

	SelectNone bool
	Exclude    map[string]bool // ODS EXCLUDE names (upper)
	Select     map[string]bool // ODS SELECT names (upper); nil = all
	// ODSOutputs maps ODS table name (upper) -> target dataset name.
	ODSOutputs map[string]string
	pending    map[string]*Dataset
	byVars     []*Var
	byKeys     []Value
}

func newOutput(s *Session) *Output {
	return &Output{sess: s, ODSOutputs: map[string]string{}, pending: map[string]*Dataset{}, Exclude: map[string]bool{}}
}

// BeginProc starts a new output section headed with the current titles.
func (o *Output) BeginProc(label string) {
	sec := &Section{Proc: label}
	for _, t := range o.sess.Titles {
		if t != "" {
			sec.Titles = append(sec.Titles, t)
		}
	}
	for _, t := range o.sess.Footnotes {
		if t != "" {
			sec.Footnotes = append(sec.Footnotes, t)
		}
	}
	o.cur = sec
	o.byVars, o.byKeys = nil, nil
	o.Sections = append(o.Sections, sec)
}

// EndProc stores ODS OUTPUT datasets captured during the procedure.
func (o *Output) EndProc() {
	for key, ds := range o.pending {
		o.sess.SetDataset(ds.Name, ds)
		o.sess.Log.Notef(0, "The data set WORK.%s has %d observations and %d variables.", strings.ToUpper(ds.Name), len(ds.Rows), len(ds.Vars))
		delete(o.ODSOutputs, key)
	}
	o.pending = map[string]*Dataset{}
	o.byVars, o.byKeys = nil, nil
}

func (o *Output) visible(odsName string) bool {
	if o.SelectNone {
		return false
	}
	u := strings.ToUpper(odsName)
	if o.Exclude[u] {
		return false
	}
	if o.Select != nil && odsName != "" && !o.Select[u] {
		return false
	}
	return true
}

func (o *Output) add(item any) {
	if o.cur == nil {
		o.BeginProc("")
	}
	o.cur.Items = append(o.cur.Items, item)
}

// SetBy declares the current BY group and prints a BY line. Pass nil to clear.
func (o *Output) SetBy(vars []*Var, keys []Value) {
	o.byVars, o.byKeys = vars, keys
	if len(vars) == 0 {
		return
	}
	parts := make([]string, len(vars))
	for i, v := range vars {
		parts[i] = v.Name + "=" + FormatByName(keys[i], v.Format)
	}
	if !o.SelectNone {
		o.add(&Text{Text: strings.Join(parts, " "), Class: "byline"})
	}
}

// AddTable displays a table (subject to ODS SELECT) and captures it for ODS OUTPUT.
func (o *Output) AddTable(t *Table) {
	o.capture(t)
	if o.visible(t.ODSName) {
		o.add(t)
	}
}

// AddHTML displays raw HTML; h.Table (optional) is captured for ODS OUTPUT.
func (o *Output) AddHTML(h *HTML) {
	name := ""
	if h.Table != nil {
		o.capture(h.Table)
		name = h.Table.ODSName
	}
	if o.visible(name) {
		o.add(h)
	}
}

func (o *Output) AddGraph(g *Graph) {
	if o.visible(g.ODSName) {
		o.add(g)
	}
}

func (o *Output) AddText(t *Text) {
	if o.visible("") {
		o.add(t)
	}
}

func (o *Output) capture(t *Table) {
	if t.ODSName == "" {
		return
	}
	key := strings.ToUpper(t.ODSName)
	target, ok := o.ODSOutputs[key]
	if !ok {
		return
	}
	ds := t.ToDataset(target)
	if len(o.byVars) > 0 {
		var vars []*Var
		for _, v := range o.byVars {
			vars = append(vars, v.Clone())
		}
		ds.Vars = append(vars, ds.Vars...)
		for i, r := range ds.Rows {
			ds.Rows[i] = append(append([]Value(nil), o.byKeys...), r...)
		}
	}
	if prev, ok := o.pending[key]; ok {
		appendDataset(prev, ds)
	} else {
		o.pending[key] = ds
	}
}

// appendDataset appends rows of src to dst, aligning variables by name.
func appendDataset(dst, src *Dataset) {
	idx := make([]int, len(src.Vars))
	for i, v := range src.Vars {
		j := dst.Index(v.Name)
		if j < 0 {
			j = dst.AddVar(v.Clone())
		} else if v.Char && dst.Vars[j].Char && v.Length > dst.Vars[j].Length {
			dst.Vars[j].Length = v.Length
		}
		idx[i] = j
	}
	for _, r := range src.Rows {
		row := make([]Value, len(dst.Vars))
		for j, v := range dst.Vars {
			row[j] = MissingOf(v.Char)
		}
		for i, j := range idx {
			row[j] = r[i]
		}
		dst.Rows = append(dst.Rows, row)
	}
}
