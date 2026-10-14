package sas

import (
	"encoding/csv"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func init() {
	RegisterProc("IMPORT", bpImport)
	RegisterProc("EXPORT", bpExport)
}

// bpBodyOpts merges PROC statement options with body statements such as
// "GETNAMES=YES;" or "DELIMITER=',';".
func bpBodyOpts(p *ProcStep) *Opts {
	o := &Opts{List: append([]Opt(nil), p.Opts.List...)}
	for _, st := range p.Body {
		o.List = append(o.List, ParseOpts(st.Toks).List...)
	}
	return o
}

func bpDelimiter(o *Opts, dbms string) (rune, error) {
	switch dbms {
	case "TAB":
		return '\t', nil
	case "CSV":
		if !o.Has("DELIMITER", "DLM") {
			return ',', nil
		}
	}
	op, ok := o.Get("DELIMITER", "DLM")
	if !ok || len(op.Val) == 0 {
		if dbms == "DLM" {
			return ' ', nil
		}
		return ',', nil
	}
	t := op.Val[0]
	txt := t.Text
	// '09'x lexes as string "09" followed by a separate X flag
	if t.Kind == TString && len(txt) == 2 && o.Has("X") {
		if v, err := strconv.ParseUint(txt, 16, 8); err == nil {
			return rune(v), nil
		}
	}
	if txt == `\t` {
		return '\t', nil
	}
	if txt == "" {
		return 0, fmt.Errorf("Invalid DELIMITER= value.")
	}
	return []rune(txt)[0], nil
}

// bpValidName converts a header to a valid SAS name (VALIDVARNAME=V7).
func bpValidName(h string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(h) {
		if r < 128 && (r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	n := b.String()
	if n == "" {
		return ""
	}
	if n[0] >= '0' && n[0] <= '9' {
		n = "_" + n
	}
	if len(n) > 32 {
		n = n[:32]
	}
	return n
}

func bpImport(s *Session, p *ProcStep) error {
	o := bpBodyOpts(p)
	file := o.Str("DATAFILE", o.Str("FILE", ""))
	if file == "" {
		return fmt.Errorf("DATAFILE= is required.")
	}
	path, err := s.ResolvePath(file)
	if err != nil {
		return err
	}
	outOp, ok := p.Opts.Get("OUT")
	if !ok {
		return fmt.Errorf("OUT= is required.")
	}
	ref, err := DSRefFromOpt(outOp)
	if err != nil {
		return err
	}
	ref.Line = p.Line
	if s.Dataset(ref.Name) != nil && !p.Opts.Has("REPLACE") {
		s.Log.Notef(p.Line, "Import cancelled.  Output dataset %s already exists. Specify REPLACE option to overwrite it.", DisplayName(ref.Name))
		return nil
	}
	dbms := strings.ToUpper(o.Str("DBMS", ""))
	if dbms == "" {
		switch strings.ToLower(filepath.Ext(path)) {
		case ".csv":
			dbms = "CSV"
		case ".txt", ".tab", ".tsv":
			dbms = "TAB"
		default:
			dbms = "DLM"
		}
	}
	if dbms != "CSV" && dbms != "TAB" && dbms != "DLM" {
		return fmt.Errorf("DBMS=%s is not supported (CSV, TAB, DLM).", dbms)
	}
	delim, err := bpDelimiter(o, dbms)
	if err != nil {
		return err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("Physical file does not exist, %s.", path)
	}
	text := strings.TrimPrefix(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\ufeff")
	r := csv.NewReader(strings.NewReader(text))
	r.Comma = delim
	r.FieldsPerRecord = -1
	r.LazyQuotes = true
	if delim == ' ' {
		r.TrimLeadingSpace = true
	}
	recs, err := r.ReadAll()
	if err != nil {
		return fmt.Errorf("Error reading %s: %v", path, err)
	}
	getnames := !strings.EqualFold(o.Str("GETNAMES", "YES"), "NO") && !strings.EqualFold(o.Str("GETNAMES", "YES"), "N")
	datarow := 1
	if getnames {
		datarow = 2
	}
	datarow = int(o.Num("DATAROW", float64(datarow)))
	guess := int(o.Num("GUESSINGROWS", math.MaxInt))
	if op, ok := o.Get("GUESSINGROWS"); ok && strings.EqualFold(op.Str(), "MAX") {
		guess = math.MaxInt
	}
	var header []string
	if getnames && len(recs) > 0 {
		header = recs[0]
	}
	var data [][]string
	if datarow-1 < len(recs) {
		data = recs[datarow-1:]
	}
	// drop fully blank lines
	kept := data[:0:0]
	for _, rec := range data {
		blank := true
		for _, f := range rec {
			if strings.TrimSpace(f) != "" {
				blank = false
				break
			}
		}
		if !blank {
			kept = append(kept, rec)
		}
	}
	data = kept
	ncol := len(header)
	for _, rec := range data {
		ncol = max(ncol, len(rec))
	}
	d := &Dataset{}
	used := map[string]bool{}
	for j := range ncol {
		name := ""
		if j < len(header) {
			name = bpValidName(header[j])
		}
		if name == "" {
			name = "VAR" + strconv.Itoa(j+1)
		}
		base, n := name, 1
		for used[strings.ToUpper(name)] {
			n++
			suf := strconv.Itoa(n)
			if len(base)+len(suf) > 32 {
				name = base[:32-len(suf)] + suf
			} else {
				name = base + suf
			}
		}
		used[strings.ToUpper(name)] = true
		// guess type over the first GUESSINGROWS rows
		numeric, length, nonEmpty := true, 1, false
		for i, rec := range data {
			if i >= guess {
				break
			}
			f := ""
			if j < len(rec) {
				f = strings.TrimSpace(rec[j])
			}
			if f == "" {
				continue
			}
			nonEmpty = true
			length = max(length, len(f))
			if _, ok := bpParseImportNum(f); !ok {
				numeric = false
			}
		}
		if !nonEmpty {
			numeric = false
		}
		v := &Var{Name: name, Length: 8}
		if numeric {
			v.Format, v.Informat = "BEST12.", "BEST32."
		} else {
			v.Char, v.Length = true, length
			v.Format, v.Informat = fmt.Sprintf("$%d.", length), fmt.Sprintf("$%d.", length)
		}
		d.Vars = append(d.Vars, v)
	}
	for _, rec := range data {
		row := make([]Value, ncol)
		for j, v := range d.Vars {
			f := ""
			if j < len(rec) {
				f = strings.TrimSpace(rec[j])
			}
			if v.Char {
				if len(f) > v.Length {
					f = f[:v.Length]
				}
				row[j] = StrV(f)
			} else {
				x, ok := bpParseImportNum(f)
				if !ok {
					x = math.NaN()
				}
				row[j] = NumV(x)
			}
		}
		d.Rows = append(d.Rows, row)
	}
	s.Log.Notef(p.Line, "%d records were read from the infile '%s'.", len(data), path)
	if err := s.WriteDS(ref, d); err != nil {
		return err
	}
	s.Log.Notef(p.Line, "%s data set was successfully created.", DisplayName(ref.Name))
	return nil
}

func bpExport(s *Session, p *ProcStep) error {
	o := bpBodyOpts(p)
	d, err := s.ProcData(p)
	if err != nil {
		return err
	}
	file := o.Str("OUTFILE", o.Str("FILE", ""))
	if file == "" {
		return fmt.Errorf("OUTFILE= is required.")
	}
	if ref, ok := s.Filerefs[strings.ToUpper(file)]; ok {
		file = ref
	}
	if strings.HasPrefix(file, "~/") {
		if h, err := os.UserHomeDir(); err == nil {
			file = filepath.Join(h, file[2:])
		}
	}
	if !filepath.IsAbs(file) {
		file = filepath.Join(s.Dir, file)
	} else if _, err := os.Stat(filepath.Dir(file)); err != nil {
		file = filepath.Join(s.Dir, filepath.Base(file))
	}
	if _, err := os.Stat(file); err == nil && !p.Opts.Has("REPLACE") {
		return fmt.Errorf("File %s already exists. Specify REPLACE option to overwrite it.", file)
	}
	dbms := strings.ToUpper(o.Str("DBMS", "CSV"))
	delim, err := bpDelimiter(o, dbms)
	if err != nil {
		return err
	}
	putnames := !strings.EqualFold(o.Str("PUTNAMES", "YES"), "NO")
	var b strings.Builder
	w := csv.NewWriter(&b)
	w.Comma = delim
	if putnames {
		names := make([]string, len(d.Vars))
		for i, v := range d.Vars {
			names[i] = v.Name
		}
		w.Write(names)
	}
	for _, r := range d.Rows {
		rec := make([]string, len(d.Vars))
		for i, v := range d.Vars {
			if !r[i].Char && math.IsNaN(r[i].Num) {
				continue
			}
			rec[i] = strings.TrimSpace(FormatByName(r[i], v.Format))
		}
		w.Write(rec)
	}
	w.Flush()
	out := strings.ReplaceAll(b.String(), "\n", "\r\n")
	if err := os.WriteFile(file, []byte(out), 0o644); err != nil {
		return err
	}
	n := len(d.Rows)
	if putnames {
		n++
	}
	s.Log.Notef(p.Line, "%d records were written to the file '%s'.", n, file)
	s.Log.Notef(p.Line, "There were %d observations read from the data set %s.", len(d.Rows), DisplayName(d.Name))
	return nil
}

// bpParseImportNum reads a numeric field: plain numbers, "." and blanks
// (missing), and COMMA/DOLLAR/PERCENT styled values such as "$1,234" or "(5%)".
func bpParseImportNum(f string) (float64, bool) {
	if x, ok := parseNumber(f); ok {
		return x, true
	}
	t := strings.TrimSpace(f)
	neg := false
	if strings.HasPrefix(t, "(") && strings.HasSuffix(t, ")") {
		neg, t = true, t[1:len(t)-1]
	}
	if strings.HasPrefix(t, "-") {
		neg, t = !neg, t[1:]
	}
	pct := strings.HasSuffix(t, "%")
	t = strings.TrimSuffix(t, "%")
	t = strings.TrimPrefix(t, "$")
	if t == "" || strings.ContainsAny(t, "eE") {
		return math.NaN(), false
	}
	t = strings.ReplaceAll(t, ",", "")
	x, err := strconv.ParseFloat(t, 64)
	if err != nil {
		return math.NaN(), false
	}
	if pct {
		x /= 100
	}
	if neg {
		x = -x
	}
	return x, true
}
