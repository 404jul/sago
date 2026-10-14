package sas

import (
	"math"
	"sort"
	"strings"
)

// Value is a SAS value: numeric (float64, NaN = missing ".") or character.
type Value struct {
	Char bool
	Num  float64
	Str  string
}

// Missing is the numeric missing value ".".
var Missing = Value{Num: math.NaN()}

func NumV(f float64) Value { return Value{Num: f} }
func StrV(s string) Value  { return Value{Char: true, Str: s} }
func BoolV(b bool) Value {
	if b {
		return Value{Num: 1}
	}
	return Value{Num: 0}
}

// MissingOf returns the missing value of the given type.
func MissingOf(char bool) Value {
	if char {
		return StrV("")
	}
	return Missing
}

func (v Value) IsMissing() bool {
	if v.Char {
		return strings.TrimSpace(v.Str) == ""
	}
	return math.IsNaN(v.Num)
}

// Truth implements SAS boolean semantics: missing and 0 are false.
func (v Value) Truth() bool {
	if v.Char {
		f, ok := parseNumber(strings.TrimSpace(v.Str))
		return ok && f != 0 && !math.IsNaN(f)
	}
	return !math.IsNaN(v.Num) && v.Num != 0
}

// AsNum converts to numeric (character values are parsed; invalid -> missing).
func (v Value) AsNum() float64 {
	if !v.Char {
		return v.Num
	}
	f, ok := parseNumber(strings.TrimSpace(v.Str))
	if !ok {
		return math.NaN()
	}
	return f
}

// AsStr converts to character. Numeric values use BEST12. trimmed
// (callers needing SAS's right-aligned automatic conversion use FormatValue).
func (v Value) AsStr() string {
	if v.Char {
		return v.Str
	}
	return strings.TrimSpace(FormatValue(v, nil))
}

// Compare orders values SAS-style: numeric missing sorts lowest; character
// values compare with trailing blanks ignored. Mixed types: numeric < char.
func Compare(a, b Value) int {
	if a.Char != b.Char {
		if !a.Char {
			return -1
		}
		return 1
	}
	if a.Char {
		as := strings.TrimRight(a.Str, " ")
		bs := strings.TrimRight(b.Str, " ")
		return strings.Compare(as, bs)
	}
	am, bm := math.IsNaN(a.Num), math.IsNaN(b.Num)
	switch {
	case am && bm:
		return 0
	case am:
		return -1
	case bm:
		return 1
	case a.Num < b.Num:
		return -1
	case a.Num > b.Num:
		return 1
	}
	return 0
}

func Equal(a, b Value) bool { return Compare(a, b) == 0 }

// Var describes a dataset variable.
type Var struct {
	Name     string
	Char     bool
	Length   int // bytes; numeric = 8
	Format   string
	Informat string
	Label    string
}

// DisplayLabel returns the label, or the name if there is no label.
func (v *Var) DisplayLabel() string {
	if v.Label != "" {
		return v.Label
	}
	return v.Name
}

func (v *Var) Clone() *Var { c := *v; return &c }

// Dataset is an in-memory SAS data set.
type Dataset struct {
	Name  string
	Label string
	Vars  []*Var
	Rows  [][]Value
	// Sorted holds the BY variables the data is known to be sorted by (upper case).
	Sorted []string
}

func NewDataset(name string) *Dataset { return &Dataset{Name: name} }

// Index returns the column index of a variable (case-insensitive) or -1.
func (d *Dataset) Index(name string) int {
	for i, v := range d.Vars {
		if strings.EqualFold(v.Name, name) {
			return i
		}
	}
	return -1
}

func (d *Dataset) Var(name string) *Var {
	if i := d.Index(name); i >= 0 {
		return d.Vars[i]
	}
	return nil
}

// AddVar appends a variable (existing rows get missing) and returns its index.
// If the variable exists its index is returned unchanged.
func (d *Dataset) AddVar(v *Var) int {
	if i := d.Index(v.Name); i >= 0 {
		return i
	}
	if v.Length == 0 {
		if v.Char {
			v.Length = 8
		} else {
			v.Length = 8
		}
	}
	d.Vars = append(d.Vars, v)
	for r := range d.Rows {
		d.Rows[r] = append(d.Rows[r], MissingOf(v.Char))
	}
	return len(d.Vars) - 1
}

// Col returns all values of one column.
func (d *Dataset) Col(i int) []Value {
	out := make([]Value, len(d.Rows))
	for r, row := range d.Rows {
		out[r] = row[i]
	}
	return out
}

// NumCol returns a numeric column as float64 (missing = NaN).
func (d *Dataset) NumCol(i int) []float64 {
	out := make([]float64, len(d.Rows))
	for r, row := range d.Rows {
		out[r] = row[i].AsNum()
	}
	return out
}

// Clone deep-copies vars and rows.
func (d *Dataset) Clone() *Dataset {
	c := &Dataset{Name: d.Name, Label: d.Label, Sorted: append([]string(nil), d.Sorted...)}
	for _, v := range d.Vars {
		c.Vars = append(c.Vars, v.Clone())
	}
	c.Rows = make([][]Value, len(d.Rows))
	for i, r := range d.Rows {
		c.Rows[i] = append([]Value(nil), r...)
	}
	return c
}

// Select returns a new dataset with only the given columns (by index).
func (d *Dataset) Select(idx []int) *Dataset {
	c := &Dataset{Name: d.Name, Label: d.Label}
	for _, i := range idx {
		c.Vars = append(c.Vars, d.Vars[i].Clone())
	}
	c.Rows = make([][]Value, len(d.Rows))
	for r, row := range d.Rows {
		nr := make([]Value, len(idx))
		for j, i := range idx {
			nr[j] = row[i]
		}
		c.Rows[r] = nr
	}
	return c
}

// Group is a set of consecutive rows sharing BY-variable values.
type Group struct {
	Keys []Value // values of the BY variables
	Rows [][]Value
}

// ByGroups splits rows into groups of consecutive equal values of the BY
// columns (data must already be sorted/grouped; SAS errors otherwise, we
// group consecutively like SAS with NOTSORTED). With no BY columns, a single
// group with all rows is returned.
func (d *Dataset) ByGroups(byIdx []int) []Group {
	if len(byIdx) == 0 {
		return []Group{{Rows: d.Rows}}
	}
	var groups []Group
	for _, row := range d.Rows {
		n := len(groups)
		if n > 0 && sameKeys(groups[n-1].Keys, row, byIdx) {
			groups[n-1].Rows = append(groups[n-1].Rows, row)
			continue
		}
		keys := make([]Value, len(byIdx))
		for j, i := range byIdx {
			keys[j] = row[i]
		}
		groups = append(groups, Group{Keys: keys, Rows: [][]Value{row}})
	}
	return groups
}

func sameKeys(keys []Value, row []Value, idx []int) bool {
	for j, i := range idx {
		if !Equal(keys[j], row[i]) {
			return false
		}
	}
	return true
}

// IsSortedBy reports whether rows are in ascending order of the given columns
// (desc[j] true = descending).
func (d *Dataset) IsSortedBy(idx []int, desc []bool) bool {
	for r := 1; r < len(d.Rows); r++ {
		if cmpRows(d.Rows[r-1], d.Rows[r], idx, desc) > 0 {
			return false
		}
	}
	return true
}

func cmpRows(a, b []Value, idx []int, desc []bool) int {
	for j, i := range idx {
		c := Compare(a[i], b[i])
		if desc != nil && desc[j] {
			c = -c
		}
		if c != 0 {
			return c
		}
	}
	return 0
}

// SortRows stable-sorts rows by the given columns.
func SortRows(rows [][]Value, idx []int, desc []bool) {
	sort.SliceStable(rows, func(a, b int) bool { return cmpRows(rows[a], rows[b], idx, desc) < 0 })
}

// UniqueSorted returns the distinct values in SAS sort order.
func UniqueSorted(vals []Value) []Value {
	s := append([]Value(nil), vals...)
	sort.SliceStable(s, func(a, b int) bool { return Compare(s[a], s[b]) < 0 })
	var out []Value
	for i, v := range s {
		if i == 0 || !Equal(v, s[i-1]) {
			out = append(out, v)
		}
	}
	return out
}
