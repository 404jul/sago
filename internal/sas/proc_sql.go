package sas

import (
	"fmt"
	"strings"
)

// PROC SQL driver: dispatches statements to the sql parser/executor.
func init() { RegisterProc("SQL", sqlRunProc) }

func sqlRunProc(s *Session, p *ProcStep) error {
	r := &sqlRunner{s: s, inobs: -1, warned: map[string]bool{}}
	if op, ok := p.Opts.Get("OUTOBS"); ok && op.Eq {
		r.inobs = int(op.Val[0].Num)
	}
	noprint := p.Opts.Has("NOPRINT")
	number := p.Opts.Has("NUMBER")
	for _, st := range p.Body {
		kw := st.Keyword()
		var err error
		switch kw {
		case "SELECT":
			err = r.sqlSelect(st, noprint, number)
		case "CREATE":
			err = r.sqlCreate(st)
		case "DROP":
			err = r.sqlDrop(st)
		case "INSERT", "DELETE", "UPDATE", "ALTER", "DESCRIBE", "VALIDATE", "EXECUTE", "CONNECT", "DISCONNECT":
			err = fmt.Errorf("The %s statement is not supported by sago PROC SQL.", kw)
		case "RESET", "FLUSH", "CHECKPOINT":
			err = nil
		default:
			err = fmt.Errorf("Syntax error, expected one of: SELECT, CREATE, DROP, INSERT, DELETE, UPDATE.")
		}
		if err != nil {
			s.Log.Error(st.Line, err.Error())
			s.Log.Note(st.Line, "The SAS System stopped processing this step because of errors.")
			return nil
		}
	}
	return nil
}

func (r *sqlRunner) runQueryText(toks []Token) (*sqlResult, error) {
	// toks must include the leading SELECT (parseQuery consumes it)
	p := &sqlParser{c: NewCursor(toks)}
	q, err := p.parseQuery()
	if err != nil {
		return nil, err
	}
	if !p.c.AtEnd() {
		return nil, sqlSyntaxErr(p.c.Peek())
	}
	plan, _, err := r.compileQuery(q, nil)
	if err != nil {
		return nil, err
	}
	return plan.run()
}

// sqlSelect executes a SELECT (display or remerging) statement.
func (r *sqlRunner) sqlSelect(st *Stmt, noprint, number bool) error {
	res, err := r.runQueryText(st.Toks)
	if err != nil {
		return err
	}
	if noprint {
		return nil
	}
	t := &Table{ODSName: "SQL_Results", MissDot: true}
	for _, c := range res.cols {
		t.Cols = append(t.Cols, Col{Name: sanitizeName(c.name), Label: c.header(), Char: c.char, Format: c.format})
	}
	if number {
		t.Cols = append([]Col{{Name: "Row", Label: "Row", Format: "8."}}, t.Cols...)
	}
	for _, row := range res.rows {
		if number {
			n := append([]Value{NumV(float64(len(t.Rows) + 1))}, row...)
			t.Rows = append(t.Rows, n)
			continue
		}
		t.Rows = append(t.Rows, row)
	}
	if len(res.rows) == 0 {
		t.Notes = append(t.Notes, "No rows were selected.")
	}
	r.s.Out.AddTable(t)
	return nil
}

// sqlCreate handles CREATE TABLE name AS query | LIKE table.
func (r *sqlRunner) sqlCreate(st *Stmt) error {
	c := NewCursor(st.Args())
	if !c.AcceptKw("TABLE") {
		return fmt.Errorf("Syntax error, expected TABLE after CREATE.")
	}
	name, err := c.ExpectIdent()
	if err != nil {
		return err
	}
	if c.AcceptKw("LIKE") {
		ref, err := ParseDSRef(c)
		if err != nil {
			return err
		}
		src, err := r.s.ReadDS(ref)
		if err != nil {
			return err
		}
		d := NewDataset(name)
		for _, v := range src.Vars {
			d.Vars = append(d.Vars, v.Clone())
		}
		return r.s.WriteDS(&DSRef{Name: name, Line: st.Line}, d)
	}
	if c.AcceptKw("VIEW") {
		return fmt.Errorf("CREATE VIEW is not supported by sago PROC SQL.")
	}
	if !c.AcceptKw("AS") {
		return fmt.Errorf("Syntax error, expected AS in CREATE TABLE statement.")
	}
	res, err := r.runQueryText(c.Rest())
	if err != nil {
		return err
	}
	d := sqlResultDataset(res, name)
	return r.s.WriteDS(&DSRef{Name: name, Line: st.Line}, d)
}

func (r *sqlRunner) sqlDrop(st *Stmt) error {
	c := NewCursor(st.Args())
	if !c.AcceptKw("TABLE") {
		return fmt.Errorf("Syntax error, expected TABLE after DROP.")
	}
	for {
		name, err := c.ExpectIdent()
		if err != nil {
			return err
		}
		if r.s.Dataset(name) == nil {
			r.s.Log.Warn(st.Line, fmt.Sprintf("Table %s does not exist.", DisplayName(name)))
		}
		delete(r.s.datasets, NormName(name))
		if !c.AcceptOp(",") {
			break
		}
	}
	return nil
}

var _ = strings.ToUpper
