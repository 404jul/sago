package sas

import (
	"math"
	"strings"
)

func init() { RegisterProc("PRINT", bpPrint) }

// bpObsRead logs SAS's "observations read" note.
func bpObsRead(s *Session, line int, d *Dataset) {
	s.Log.Notef(line, "There were %d observations read from the data set %s.", len(d.Rows), DisplayName(d.Name))
}

func bpPrint(s *Session, p *ProcStep) error {
	d, err := s.ProcData(p)
	if err != nil {
		return err
	}
	noobs := p.Opts.Has("NOOBS")
	split := ""
	if op, ok := p.Opts.Get("SPLIT", "S"); ok && op.Eq {
		split = op.Str()
	}
	useLabel := p.Opts.Has("LABEL", "L") || split != ""
	// first observation number (FIRSTOBS= data set option shifts Obs numbering)
	obsStart := 1
	if op, ok := p.Opts.Get("DATA"); ok {
		if ref, err := DSRefFromOpt(op); err == nil && ref.Opts != nil && !ref.Opts.Has("WHERE") && len(p.Stmts("WHERE")) == 0 {
			obsStart = int(ref.Opts.Num("FIRSTOBS", 1))
		}
	}

	by, groups, err := s.ProcBy(p, d)
	if err != nil {
		return err
	}
	excluded := map[int]bool{}
	if by != nil {
		for _, i := range by.Idx {
			excluded[i] = true
		}
	}
	var idIdx []int
	if st := p.First("ID"); st != nil {
		names, err := ExpandVarList(d, st.Args())
		if err != nil {
			return err
		}
		if idIdx, err = d.Indices(names); err != nil {
			return err
		}
		for _, i := range idIdx {
			excluded[i] = true
		}
	}
	var varIdx []int
	if sts := p.Stmts("VAR", "VARIABLES"); len(sts) > 0 {
		for _, st := range sts {
			names, err := ExpandVarList(d, st.Args())
			if err != nil {
				return err
			}
			ix, err := d.Indices(names)
			if err != nil {
				return err
			}
			varIdx = append(varIdx, ix...)
		}
	} else {
		for i := range d.Vars {
			if !excluded[i] {
				varIdx = append(varIdx, i)
			}
		}
	}
	sumSet := map[int]bool{}
	for _, st := range p.Stmts("SUM") {
		names, err := ExpandVarList(d, st.Args())
		if err != nil {
			return err
		}
		ix, err := d.Indices(names)
		if err != nil {
			return err
		}
		for _, i := range ix {
			if !d.Vars[i].Char {
				sumSet[i] = true
				if !bpContains(varIdx, i) && !bpContains(idIdx, i) {
					varIdx = append(varIdx, i)
				}
			}
		}
	}

	header := func(v *Var) string {
		h := v.Name
		if useLabel && v.Label != "" {
			h = v.Label
		}
		if split != "" {
			h = strings.ReplaceAll(h, split, " ")
		}
		return h
	}
	cols := append(append([]int(nil), idIdx...), varIdx...)
	newTable := func() *Table {
		t := &Table{ODSName: "Print", MissDot: true}
		if !noobs && len(idIdx) == 0 {
			t.Cols = append(t.Cols, Col{Name: "Obs", Label: "Obs"})
			t.RowHeader = 1
		} else {
			t.RowHeader = len(idIdx)
		}
		for _, i := range cols {
			v := d.Vars[i]
			t.Cols = append(t.Cols, Col{Name: v.Name, Label: header(v), Format: v.Format, Char: v.Char})
		}
		return t
	}
	hasObs := !noobs && len(idIdx) == 0
	sumRow := func(sums map[int]float64, lead string) []Value {
		var row []Value
		if hasObs {
			row = append(row, StrV(lead))
		}
		for k, i := range cols {
			switch {
			case sumSet[i]:
				row = append(row, NumV(sums[i]))
			case !hasObs && k == 0:
				row = append(row, StrV(lead))
			default:
				row = append(row, StrV(""))
			}
		}
		return row
	}
	total := map[int]float64{}
	obs := obsStart
	for gi, g := range groups {
		if by != nil {
			s.Out.SetBy(by.Vars, g.Keys)
		}
		t := newTable()
		sub := map[int]float64{}
		for _, r := range g.Rows {
			var row []Value
			if hasObs {
				row = append(row, NumV(float64(obs)))
			}
			obs++
			for _, i := range cols {
				row = append(row, r[i])
				if sumSet[i] && !math.IsNaN(r[i].Num) {
					sub[i] += r[i].Num
					total[i] += r[i].Num
				}
			}
			t.Rows = append(t.Rows, row)
		}
		if len(sumSet) > 0 {
			if by != nil {
				t.Rows = append(t.Rows, sumRow(sub, by.Vars[len(by.Vars)-1].Name))
			}
			if gi == len(groups)-1 && (by == nil || len(groups) > 1) {
				t.Rows = append(t.Rows, sumRow(total, ""))
			}
		}
		s.Out.AddTable(t)
	}
	bpObsRead(s, p.Line, d)
	return nil
}

func bpContains(xs []int, x int) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}
