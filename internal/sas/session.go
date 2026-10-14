package sas

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var nowFunc = time.Now

// Session holds all state for running a SAS program.
type Session struct {
	datasets    map[string]*Dataset // key: upper-case name, WORK. stripped
	Last        string              // _LAST_ data set name
	Titles      [11]string          // 1..10
	Footnotes   [11]string
	Out         *Output
	Log         *Log
	Macros      map[string]string
	Dir         string            // directory of the program (relative paths)
	Options     map[string]string // OPTIONS statement values (upper keys)
	Librefs     map[string]string
	Filerefs    map[string]string
	NoProcTitle bool
	// Ext holds component-private state (e.g. random number streams).
	Ext map[string]any
}

func NewSession(dir string) *Session {
	s := &Session{
		datasets: map[string]*Dataset{},
		Log:      &Log{},
		Macros:   map[string]string{},
		Dir:      dir,
		Options:  map[string]string{},
		Librefs:  map[string]string{},
		Filerefs: map[string]string{},
		Ext:      map[string]any{},
	}
	s.Out = newOutput(s)
	return s
}

// ---------------------------------------------------------------- datasets

// NormName normalizes a data set name: upper case, WORK. prefix removed.
func NormName(name string) string {
	n := strings.ToUpper(strings.TrimSpace(name))
	n = strings.TrimPrefix(n, "WORK.")
	return n
}

// DisplayName returns the libref-qualified name for log messages.
func DisplayName(name string) string {
	n := NormName(name)
	if !strings.Contains(n, ".") {
		return "WORK." + n
	}
	return n
}

// Dataset returns a stored data set (nil if absent). Treat rows as read-only.
func (s *Session) Dataset(name string) *Dataset {
	n := NormName(name)
	if n == "_LAST_" {
		n = s.Last
	}
	if d, ok := s.datasets[n]; ok {
		return d
	}
	if strings.HasPrefix(n, "SASHELP.") {
		if d := loadSashelp(strings.TrimPrefix(n, "SASHELP.")); d != nil {
			s.datasets[n] = d
			return d
		}
	}
	return nil
}

// SetDataset stores a data set and makes it _LAST_.
func (s *Session) SetDataset(name string, d *Dataset) {
	n := NormName(name)
	if n == "_NULL_" {
		return
	}
	d.Name = n
	s.datasets[n] = d
	s.Last = n
}

// DatasetNames lists stored data sets.
func (s *Session) DatasetNames() []string {
	var out []string
	for k := range s.datasets {
		out = append(out, k)
	}
	return out
}

// ReadDS resolves a data set reference and applies input data set options
// (KEEP= DROP= RENAME= WHERE= FIRSTOBS= OBS=). The returned data set has
// cloned variables; rows may be shared with the stored data set — do not
// mutate row slices in place (copy first).
func (s *Session) ReadDS(ref *DSRef) (*Dataset, error) {
	name := "_LAST_"
	if ref != nil {
		name = ref.Name
	}
	src := s.Dataset(name)
	if src == nil {
		if NormName(name) == "_LAST_" {
			return nil, fmt.Errorf("There is no default input data set (_LAST_ is _NULL_).")
		}
		return nil, fmt.Errorf("File %s.DATA does not exist.", DisplayName(name))
	}
	d := &Dataset{Name: src.Name, Label: src.Label, Rows: src.Rows, Sorted: src.Sorted}
	for _, v := range src.Vars {
		d.Vars = append(d.Vars, v.Clone())
	}
	if ref == nil || ref.Opts == nil {
		return d, nil
	}
	return s.applyDSOpts(d, ref.Opts, true)
}

func (s *Session) applyDSOpts(d *Dataset, o *Opts, input bool) (*Dataset, error) {
	// KEEP / DROP
	if op, ok := o.Get("KEEP"); ok {
		names, err := ExpandVarList(d, op.Inner())
		if err != nil {
			return nil, fmt.Errorf("KEEP= option: %v", err)
		}
		idx, _ := d.Indices(names)
		d = d.Select(sortedUnique(idx))
	}
	if op, ok := o.Get("DROP"); ok {
		names, err := ExpandVarList(d, op.Inner())
		if err != nil {
			return nil, fmt.Errorf("DROP= option: %v", err)
		}
		drop := map[int]bool{}
		for _, n := range names {
			drop[d.Index(n)] = true
		}
		var idx []int
		for i := range d.Vars {
			if !drop[i] {
				idx = append(idx, i)
			}
		}
		d = d.Select(idx)
	}
	if op, ok := o.Get("RENAME"); ok {
		if err := applyRename(d, op.Inner()); err != nil {
			return nil, err
		}
	}
	if op, ok := o.Get("WHERE"); ok && input {
		e, err := ParseWhere(op.Inner())
		if err != nil {
			return nil, fmt.Errorf("WHERE= option: %v", err)
		}
		d = s.FilterWhere(d, e)
	}
	first := int(o.Num("FIRSTOBS", 1))
	obs := int(o.Num("OBS", -1))
	if input && (first > 1 || obs >= 0) {
		lo := first - 1
		hi := len(d.Rows)
		if obs >= 0 && obs < hi {
			hi = obs
		}
		if lo > hi {
			lo = hi
		}
		if lo > len(d.Rows) {
			lo = len(d.Rows)
		}
		d.Rows = d.Rows[lo:hi]
	}
	if lbl, ok := o.Get("LABEL"); ok {
		d.Label = lbl.Str()
	}
	return d, nil
}

func sortedUnique(idx []int) []int {
	seen := map[int]bool{}
	var out []int
	for _, i := range idx {
		if !seen[i] {
			seen[i] = true
			out = append(out, i)
		}
	}
	// KEEP= preserves original variable order
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

func applyRename(d *Dataset, toks []Token) error {
	c := NewCursor(toks)
	for !c.AtEnd() {
		from := c.Next()
		if !c.AcceptOp("=") {
			return fmt.Errorf("RENAME= option: expected '='")
		}
		to := c.Next()
		i := d.Index(from.Text)
		if i < 0 {
			return fmt.Errorf("RENAME= option: variable %s not found", strings.ToUpper(from.Text))
		}
		d.Vars[i].Name = to.Text
	}
	return nil
}

// FilterWhere returns a dataset with only rows satisfying e.
func (s *Session) FilterWhere(d *Dataset, e Expr) *Dataset {
	out := &Dataset{Name: d.Name, Label: d.Label, Vars: d.Vars, Sorted: d.Sorted}
	var row []Value
	ctx := &EvalCtx{Sess: s, Get: func(name string) (Value, bool) {
		if i := d.Index(name); i >= 0 {
			return row[i], true
		}
		return Missing, false
	}}
	for _, r := range d.Rows {
		row = r
		if e.Eval(ctx).Truth() {
			out.Rows = append(out.Rows, r)
		}
	}
	return out
}

// WriteDS applies output data set options (KEEP= DROP= RENAME= LABEL=) and stores.
func (s *Session) WriteDS(ref *DSRef, d *Dataset) error {
	if ref == nil {
		return fmt.Errorf("no output data set")
	}
	if ref.Opts != nil {
		var err error
		d, err = s.applyDSOpts(d, ref.Opts, false)
		if err != nil {
			return err
		}
	}
	s.SetDataset(ref.Name, d)
	if NormName(ref.Name) != "_NULL_" {
		s.Log.Notef(ref.Line, "The data set %s has %d observations and %d variables.", DisplayName(ref.Name), len(d.Rows), len(d.Vars))
	}
	return nil
}

// ResolvePath resolves a file path relative to the program directory. If an
// absolute path does not exist (e.g. a SAS OnDemand /home/uXXXX/... path),
// the file's base name is looked up next to the program.
func (s *Session) ResolvePath(p string) (string, error) {
	if ref, ok := s.Filerefs[strings.ToUpper(p)]; ok {
		p = ref
	}
	if strings.HasPrefix(p, "~/") {
		if h, err := os.UserHomeDir(); err == nil {
			p = filepath.Join(h, p[2:])
		}
	}
	cand := p
	if !filepath.IsAbs(cand) {
		cand = filepath.Join(s.Dir, p)
	}
	if _, err := os.Stat(cand); err == nil {
		return cand, nil
	}
	base := filepath.Join(s.Dir, filepath.Base(p))
	if _, err := os.Stat(base); err == nil {
		s.Log.Note(0, fmt.Sprintf("Physical file %s not found; using %s instead.", p, base))
		return base, nil
	}
	return "", fmt.Errorf("Physical file does not exist, %s.", p)
}

// ---------------------------------------------------------------- steps

// ProcStep is a PROC invocation: header statement and body statements.
type ProcStep struct {
	Name   string // upper case
	Header *Stmt
	Opts   *Opts // options from the PROC statement
	Body   []*Stmt
	Line   int
}

// Stmts returns body statements with the given keywords.
func (p *ProcStep) Stmts(kw ...string) []*Stmt {
	var out []*Stmt
	for _, st := range p.Body {
		k := st.Keyword()
		for _, w := range kw {
			if k == strings.ToUpper(w) {
				out = append(out, st)
				break
			}
		}
	}
	return out
}

// First returns the last body statement with the keyword (SAS: later wins), or nil.
func (p *ProcStep) First(kw ...string) *Stmt {
	s := p.Stmts(kw...)
	if len(s) == 0 {
		return nil
	}
	return s[len(s)-1]
}

// ProcFunc runs a procedure.
type ProcFunc func(s *Session, p *ProcStep) error

var procs = map[string]ProcFunc{}

// procQuitTerminated lists procedures that run statements until QUIT.
var procQuitTerminated = map[string]bool{"SQL": true}

// RegisterProc registers a procedure implementation.
func RegisterProc(name string, f ProcFunc) { procs[strings.ToUpper(name)] = f }

// DataStep is a DATA step: header (DATA statement) and body statements.
type DataStep struct {
	Header *Stmt
	Body   []*Stmt
	Line   int
}

// RunDataStepFunc executes a DATA step (installed by the data step component).
var RunDataStepFunc func(s *Session, d *DataStep) error

// ProcData resolves DATA= (or _LAST_) for a procedure and applies body
// WHERE, LABEL and FORMAT statements.
func (s *Session) ProcData(p *ProcStep) (*Dataset, error) {
	var ref *DSRef
	if op, ok := p.Opts.Get("DATA"); ok {
		r, err := DSRefFromOpt(op)
		if err != nil {
			return nil, err
		}
		ref = r
	}
	d, err := s.ReadDS(ref)
	if err != nil {
		return nil, err
	}
	var where Expr
	for _, st := range p.Stmts("WHERE") {
		args := st.Args()
		also := false
		if len(args) > 0 && args[0].Is("ALSO", "SAME") {
			args = args[1:]
			also = true
		}
		e, err := ParseWhere(args)
		if err != nil {
			return nil, fmt.Errorf("WHERE statement: %v", err)
		}
		if also && where != nil {
			where = &Binary{Op: "AND", L: where, R: e}
		} else {
			where = e
		}
	}
	if where != nil {
		d = s.FilterWhere(d, where)
	}
	for _, st := range p.Stmts("LABEL") {
		ApplyLabels(d, st.Args())
	}
	for _, st := range p.Stmts("FORMAT") {
		ApplyFormats(d, st.Args())
	}
	return d, nil
}

// ApplyLabels applies "var='label' ..." to variables of d.
func ApplyLabels(d *Dataset, toks []Token) {
	c := NewCursor(toks)
	for !c.AtEnd() {
		name := c.Next()
		if !c.AcceptOp("=") {
			continue
		}
		val := c.Next()
		if v := d.Var(name.Text); v != nil {
			v.Label = val.Text
		}
	}
}

// ApplyFormats applies "var1 var2 fmt. var3 fmt2." to variables of d.
func ApplyFormats(d *Dataset, toks []Token) {
	var pending []string
	c := NewCursor(toks)
	for !c.AtEnd() {
		t := c.Next()
		fmtText := ""
		if t.IsOp("$") && !c.AtEnd() {
			fmtText = "$" + c.Next().Text
		} else if t.Kind == TNumber || (t.Kind == TIdent && strings.Contains(t.Text, ".")) {
			fmtText = t.Text
		}
		if fmtText != "" {
			for _, n := range pending {
				if v := d.Var(n); v != nil {
					v.Format = fmtText
				}
			}
			pending = nil
			continue
		}
		if t.Kind == TIdent {
			pending = append(pending, t.Text)
		}
	}
	for _, n := range pending { // trailing names without a format remove it
		if v := d.Var(n); v != nil {
			v.Format = ""
		}
	}
}

// ByInfo describes BY variables.
type ByInfo struct {
	Names []string
	Idx   []int
	Desc  []bool
	Vars  []*Var
}

// ParseBy parses a BY statement's arguments against a dataset.
func ParseBy(d *Dataset, toks []Token) (*ByInfo, error) {
	b := &ByInfo{}
	desc := false
	for _, t := range toks {
		if t.Is("DESCENDING") {
			desc = true
			continue
		}
		if t.Is("NOTSORTED", "GROUPFORMAT") {
			continue
		}
		if t.Kind != TIdent {
			continue
		}
		i := d.Index(t.Text)
		if i < 0 {
			return nil, fmt.Errorf("Variable %s not found.", strings.ToUpper(t.Text))
		}
		b.Names = append(b.Names, d.Vars[i].Name)
		b.Idx = append(b.Idx, i)
		b.Desc = append(b.Desc, desc)
		b.Vars = append(b.Vars, d.Vars[i])
		desc = false
	}
	return b, nil
}

// ProcBy parses the procedure's BY statement (nil if none) and verifies
// the data is sorted.
func (s *Session) ProcBy(p *ProcStep, d *Dataset) (*ByInfo, []Group, error) {
	st := p.First("BY")
	if st == nil {
		return nil, d.ByGroups(nil), nil
	}
	b, err := ParseBy(d, st.Args())
	if err != nil {
		return nil, nil, err
	}
	notsorted := false
	for _, t := range st.Args() {
		if t.Is("NOTSORTED") {
			notsorted = true
		}
	}
	if !notsorted && !d.IsSortedBy(b.Idx, b.Desc) {
		return nil, nil, fmt.Errorf("Data set %s is not sorted in ascending sequence.", DisplayName(d.Name))
	}
	return b, d.ByGroups(b.Idx), nil
}

// Subset builds a dataset sharing vars with d containing the given rows.
func (d *Dataset) Subset(rows [][]Value) *Dataset {
	return &Dataset{Name: d.Name, Label: d.Label, Vars: d.Vars, Rows: rows}
}

// ---------------------------------------------------------------- program execution

var stepBoundary = map[string]bool{"DATA": true, "PROC": true}

// RunProgram splits and executes a program.
func (s *Session) RunProgram(src string) {
	stmts := SplitProgram(src, s.Macros, s.Log)
	s.Run(stmts)
}

// Run executes statements.
func (s *Session) Run(stmts []*Stmt) {
	i := 0
	for i < len(stmts) {
		st := stmts[i]
		kw := st.Keyword()
		switch {
		case kw == "DATA":
			body, next := s.collectStep(stmts, i+1, false)
			s.echo(st, body)
			s.runData(&DataStep{Header: st, Body: body, Line: st.Line})
			i = next
		case kw == "PROC":
			name := ""
			if len(st.Toks) > 1 {
				name = st.Toks[1].Upper()
			}
			body, next := s.collectStep(stmts, i+1, procQuitTerminated[name])
			s.echo(st, body)
			s.runProc(st, name, body)
			i = next
		default:
			s.echo(st, nil)
			if !s.globalStmt(st) {
				if kw != "" {
					s.Log.Error(st.Line, fmt.Sprintf("Statement %s is not valid or it is used out of proper order.", kw))
				}
			}
			i++
		}
	}
}

func (s *Session) echo(st *Stmt, body []*Stmt) {
	s.Log.Source(st.Line, st.Text+";")
	for _, b := range body {
		s.Log.Source(b.Line, b.Text+";")
		if len(b.Datalines) > 0 {
			s.Log.Source(b.Line+1, fmt.Sprintf("[%d data lines]", len(b.Datalines)))
		}
	}
}

// collectStep gathers body statements until RUN/QUIT or the next step.
// Global statements (TITLE, OPTIONS, ODS ...) execute immediately.
func (s *Session) collectStep(stmts []*Stmt, i int, quitOnly bool) ([]*Stmt, int) {
	var body []*Stmt
	for i < len(stmts) {
		st := stmts[i]
		kw := st.Keyword()
		if stepBoundary[kw] {
			return body, i
		}
		if kw == "QUIT" || (kw == "RUN" && !quitOnly) {
			return body, i + 1
		}
		if kw == "RUN" {
			i++
			continue
		}
		if isGlobalKw(kw) && kw != "X" && kw != "DM" {
			s.globalStmt(st)
			i++
			continue
		}
		body = append(body, st)
		i++
		if len(st.Datalines) > 0 || kw == "DATALINES" || kw == "CARDS" || kw == "LINES" || kw == "DATALINES4" || kw == "CARDS4" {
			// DATALINES ends the step; an optional RUN may follow
			if i < len(stmts) && stmts[i].Keyword() == "RUN" {
				i++
			}
			return body, i
		}
	}
	return body, i
}

func isGlobalKw(kw string) bool {
	switch {
	case strings.HasPrefix(kw, "TITLE"), strings.HasPrefix(kw, "FOOTNOTE"):
		return true
	}
	switch kw {
	case "OPTIONS", "LIBNAME", "FILENAME", "ODS", "GOPTIONS", "ENDSAS", "DM", "X", "RESETLINE", "SYMBOL", "AXIS", "LEGEND", "PATTERN":
		return true
	}
	return false
}

func (s *Session) runData(d *DataStep) {
	if RunDataStepFunc == nil {
		s.Log.Error(d.Line, "DATA step support is not available.")
		return
	}
	if err := RunDataStepFunc(s, d); err != nil {
		s.Log.Error(d.Line, err.Error())
		s.Log.Note(d.Line, "The SAS System stopped processing this step because of errors.")
	}
}

func (s *Session) runProc(header *Stmt, name string, body []*Stmt) {
	f, ok := procs[name]
	if !ok {
		s.Log.Error(header.Line, fmt.Sprintf("Procedure %s not found.", name))
		return
	}
	var optToks []Token
	if len(header.Toks) > 2 {
		optToks = header.Toks[2:]
	}
	p := &ProcStep{Name: name, Header: header, Opts: ParseOpts(optToks), Body: body, Line: header.Line}
	s.Out.BeginProc(procLabel(name))
	err := f(s, p)
	s.Out.EndProc()
	if err != nil {
		s.Log.Error(header.Line, err.Error())
		s.Log.Note(header.Line, "The SAS System stopped processing this step because of errors.")
	}
	s.Out.Select = nil
}

func procLabel(name string) string {
	switch name {
	case "PRINT", "SGPLOT", "SGPANEL", "SGSCATTER", "SQL", "SORT", "IMPORT", "EXPORT", "DATASETS":
		return ""
	}
	return "The " + name + " Procedure"
}

// globalStmt executes a global statement; returns false if not global.
func (s *Session) globalStmt(st *Stmt) bool {
	kw := st.Keyword()
	args := st.Args()
	switch {
	case kw == "RUN" || kw == "QUIT" || kw == "ENDSAS" || kw == "":
		return true
	case strings.HasPrefix(kw, "TITLE") || strings.HasPrefix(kw, "FOOTNOTE"):
		isTitle := strings.HasPrefix(kw, "TITLE")
		numStr := strings.TrimPrefix(strings.TrimPrefix(kw, "TITLE"), "FOOTNOTE")
		n := 1
		if numStr != "" {
			fmt.Sscanf(numStr, "%d", &n)
		}
		if n < 1 || n > 10 {
			return false
		}
		text := titleText(args)
		arr := &s.Titles
		if !isTitle {
			arr = &s.Footnotes
		}
		arr[n] = text
		for k := n + 1; k <= 10; k++ {
			arr[k] = ""
		}
		return true
	case kw == "OPTIONS" || kw == "GOPTIONS":
		o := ParseOpts(args)
		for _, op := range o.List {
			s.Options[op.Key] = op.Str()
		}
		return true
	case kw == "LIBNAME":
		if len(args) >= 2 {
			s.Librefs[args[0].Upper()] = args[1].Text
		}
		return true
	case kw == "FILENAME":
		if len(args) >= 2 {
			s.Filerefs[args[0].Upper()] = args[len(args)-1].Text
			for _, a := range args[1:] {
				if a.Kind == TString {
					s.Filerefs[args[0].Upper()] = a.Text
					break
				}
			}
		}
		return true
	case kw == "ODS":
		s.odsStmt(st)
		return true
	case kw == "DM" || kw == "X" || kw == "RESETLINE" || kw == "SYMBOL" || kw == "AXIS" || kw == "LEGEND" || kw == "PATTERN":
		return true
	}
	return false
}

func titleText(toks []Token) string {
	var parts []string
	c := NewCursor(toks)
	for !c.AtEnd() {
		t := c.Next()
		// skip options like j=c h=2 color=red font=x bold italic
		if t.Kind == TIdent && c.Peek().IsOp("=") {
			c.Next()
			c.Next()
			continue
		}
		if t.Is("BOLD", "ITALIC", "UNDERLIN") {
			continue
		}
		if t.Kind == TString {
			parts = append(parts, t.Text)
		} else {
			parts = append(parts, t.Text)
		}
	}
	return strings.Join(parts, " ")
}

func (s *Session) odsStmt(st *Stmt) {
	args := st.Args()
	if len(args) == 0 {
		return
	}
	switch args[0].Upper() {
	case "SELECT":
		s.Out.SelectNone = false
		s.Out.Select = nil
		if len(args) > 1 {
			switch args[1].Upper() {
			case "NONE":
				s.Out.SelectNone = true
				return
			case "ALL":
				return
			}
			s.Out.Select = map[string]bool{}
			for _, a := range args[1:] {
				s.Out.Select[odsBaseName(a.Text)] = true
			}
		}
	case "EXCLUDE":
		if len(args) > 1 {
			switch args[1].Upper() {
			case "NONE":
				s.Out.Exclude = map[string]bool{}
				return
			case "ALL":
				s.Out.SelectNone = true
				return
			}
			for _, a := range args[1:] {
				s.Out.Exclude[odsBaseName(a.Text)] = true
			}
		}
	case "OUTPUT":
		c := NewCursor(args[1:])
		for !c.AtEnd() {
			t := c.Next()
			if t.Kind != TIdent {
				continue
			}
			if c.AcceptOp("=") {
				target := c.Next()
				if g, ok := c.Group(); ok {
					_ = g // data set options on ODS OUTPUT targets are ignored
				}
				s.Out.ODSOutputs[odsBaseName(t.Text)] = target.Text
			} else if t.Is("CLOSE", "CLEAR") {
				s.Out.ODSOutputs = map[string]string{}
			}
		}
	case "NOPROCTITLE":
		s.NoProcTitle = true
	case "PROCTITLE":
		s.NoProcTitle = false
	case "PDF", "RTF", "HTML", "HTML5", "LISTING", "EXCEL", "POWERPOINT", "PS":
		if len(args) > 1 && args[1].Is("CLOSE") {
			return
		}
		if !args[0].Is("HTML", "HTML5", "LISTING") {
			s.Log.Note(st.Line, fmt.Sprintf("ODS %s destination is not supported by sago; results are written to the HTML report only.", args[0].Upper()))
		}
	}
}

// odsBaseName upper-cases an ODS path, keeping the last component (Freq.Table1.ChiSq -> CHISQ).
func odsBaseName(s string) string {
	u := strings.ToUpper(s)
	if i := strings.LastIndex(u, "."); i >= 0 && i < len(u)-1 {
		u = u[i+1:]
	}
	return u
}
