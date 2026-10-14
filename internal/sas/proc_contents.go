package sas

import (
	"sort"
	"strconv"
	"strings"
)

func init() { RegisterProc("CONTENTS", bpContents) }

func bpContents(s *Session, p *ProcStep) error {
	d, err := s.ProcData(p)
	if err != nil {
		return err
	}
	name := DisplayName(d.Name)
	if p.Opts.Has("SHORT") {
		names := make([]string, len(d.Vars))
		for i, v := range d.Vars {
			names[i] = v.Name
		}
		if !p.Opts.Has("VARNUM") {
			sort.SliceStable(names, func(a, b int) bool { return strings.ToUpper(names[a]) < strings.ToUpper(names[b]) })
		}
		s.Out.AddText(&Text{Text: "Alphabetic List of Variables for " + name + "\n" + strings.Join(names, " "), Pre: true})
		return nil
	}
	attr := &Table{ODSName: "Attributes", Caption: "", RowHeader: 1, Cols: []Col{
		{Name: "Label1", Label: ""}, {Name: "cValue1", Label: "", Char: true},
	}}
	attr.AddRow("Data Set Name", name)
	attr.AddRow("Member Type", "DATA")
	attr.AddRow("Observations", strconv.Itoa(len(d.Rows)))
	attr.AddRow("Variables", strconv.Itoa(len(d.Vars)))
	if d.Label != "" {
		attr.AddRow("Label", d.Label)
	}
	s.Out.AddTable(attr)

	idx := make([]int, len(d.Vars))
	for i := range idx {
		idx[i] = i
	}
	ods, capt := "Variables", "Alphabetic List of Variables and Attributes"
	if p.Opts.Has("VARNUM", "POSITION") {
		ods, capt = "Position", "Variables in Creation Order"
	} else {
		sort.SliceStable(idx, func(a, b int) bool {
			return strings.ToUpper(d.Vars[idx[a]].Name) < strings.ToUpper(d.Vars[idx[b]].Name)
		})
	}
	hasFmt, hasInf, hasLbl := false, false, false
	for _, v := range d.Vars {
		hasFmt = hasFmt || v.Format != ""
		hasInf = hasInf || v.Informat != ""
		hasLbl = hasLbl || v.Label != ""
	}
	t := &Table{ODSName: ods, Caption: capt, Cols: []Col{
		{Name: "Num", Label: "#"}, {Name: "Variable", Label: "Variable", Char: true},
		{Name: "Type", Label: "Type", Char: true}, {Name: "Len", Label: "Len"},
		{Name: "Format", Label: "Format", Char: true, Hidden: !hasFmt},
		{Name: "Informat", Label: "Informat", Char: true, Hidden: !hasInf},
		{Name: "Label", Label: "Label", Char: true, Hidden: !hasLbl},
	}}
	for _, i := range idx {
		v := d.Vars[i]
		typ, ln := "Num", v.Length
		if v.Char {
			typ = "Char"
		}
		if ln == 0 {
			ln = 8
		}
		t.AddRow(i+1, v.Name, typ, ln, strings.ToUpper(v.Format), strings.ToUpper(v.Informat), v.Label)
	}
	s.Out.AddTable(t)
	if len(d.Sorted) > 0 {
		st := &Table{ODSName: "Sortedby", Caption: "Sort Information", RowHeader: 1, Cols: []Col{
			{Name: "Label1", Label: ""}, {Name: "cValue1", Label: "", Char: true},
		}}
		st.AddRow("Sortedby", strings.Join(d.Sorted, " "))
		st.AddRow("Validated", "YES")
		s.Out.AddTable(st)
	}
	return nil
}
