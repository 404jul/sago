package sas

import (
	"fmt"
	"strings"
)

func init() { RegisterProc("SORT", bpSort) }

func bpSort(s *Session, p *ProcStep) error {
	d, err := s.ProcData(p)
	if err != nil {
		return err
	}
	inName := d.Name
	st := p.First("BY")
	if st == nil {
		return fmt.Errorf("No BY statement was specified.")
	}
	by, err := ParseBy(d, st.Args())
	if err != nil {
		return err
	}
	if len(by.Idx) == 0 {
		return fmt.Errorf("No BY variables were specified.")
	}
	rows := make([][]Value, len(d.Rows))
	copy(rows, d.Rows)
	SortRows(rows, by.Idx, by.Desc)
	bpObsRead(s, p.Line, d)

	var dups [][]Value
	if p.Opts.Has("NODUPKEY") {
		kept := rows[:0:0]
		for i, r := range rows {
			if i > 0 && cmpRows(rows[i-1], r, by.Idx, nil) == 0 {
				dups = append(dups, r)
				continue
			}
			kept = append(kept, r)
		}
		rows = kept
		s.Log.Notef(p.Line, "%d observations with duplicate key values were deleted.", len(dups))
	} else if p.Opts.Has("NODUPRECS", "NODUP", "NODUPREC", "NODUPS") {
		all := make([]int, len(d.Vars))
		for i := range all {
			all[i] = i
		}
		kept := rows[:0:0]
		for _, r := range rows {
			if len(kept) > 0 && cmpRows(kept[len(kept)-1], r, all, nil) == 0 {
				dups = append(dups, r)
				continue
			}
			kept = append(kept, r)
		}
		rows = kept
		s.Log.Notef(p.Line, "%d duplicate observations were deleted.", len(dups))
	}

	out := &Dataset{Name: d.Name, Label: d.Label, Vars: d.Vars, Rows: rows}
	allAsc := true
	for _, x := range by.Desc {
		allAsc = allAsc && !x
	}
	if allAsc {
		for _, n := range by.Names {
			out.Sorted = append(out.Sorted, strings.ToUpper(n))
		}
	}
	if op, ok := p.Opts.Get("DUPOUT"); ok {
		ref, err := DSRefFromOpt(op)
		if err != nil {
			return err
		}
		dd := &Dataset{Label: d.Label, Vars: d.Vars, Rows: dups}
		if err := s.WriteDS(ref, dd); err != nil {
			return err
		}
	}
	var ref *DSRef
	if op, ok := p.Opts.Get("OUT"); ok {
		if ref, err = DSRefFromOpt(op); err != nil {
			return err
		}
	} else {
		ref = &DSRef{Name: inName, Line: p.Line}
		if dop, ok := p.Opts.Get("DATA"); ok {
			if r, err := DSRefFromOpt(dop); err == nil {
				ref.Name = r.Name
			}
		}
		if strings.HasPrefix(strings.ToUpper(ref.Name), "SASHELP.") {
			return fmt.Errorf("Write access to member %s.DATA is denied.", strings.ToUpper(ref.Name))
		}
	}
	if ref.Line == 0 {
		ref.Line = p.Line
	}
	return s.WriteDS(ref, out)
}
