package sas

import (
	"fmt"
	"strings"
)

func init() { RegisterProc("DATASETS", bpDatasets) }

// bpDatasets supports DELETE, KILL and NOLIST for a library (WORK by default).
func bpDatasets(s *Session, p *ProcStep) error {
	lib := strings.ToUpper(p.Opts.Str("LIBRARY", p.Opts.Str("LIB", p.Opts.Str("DDNAME", "WORK"))))
	qual := func(n string) string {
		if lib == "WORK" {
			return NormName(n)
		}
		return lib + "." + strings.ToUpper(n)
	}
	inLib := func(n string) bool {
		if lib == "WORK" {
			return !strings.Contains(n, ".")
		}
		return strings.HasPrefix(n, lib+".")
	}
	if lib == "SASHELP" && (p.Opts.Has("KILL") || len(p.Stmts("DELETE")) > 0) {
		return fmt.Errorf("Write access to library SASHELP is denied.")
	}
	if p.Opts.Has("KILL") {
		for _, n := range s.DatasetNames() {
			if inLib(n) {
				delete(s.datasets, n)
				s.Log.Notef(p.Line, "Deleting %s (memtype=DATA).", DisplayName(n))
				if s.Last == n {
					s.Last = ""
				}
			}
		}
	}
	for _, st := range p.Stmts("DELETE") {
		args, _ := SplitSlash(st.Args())
		for _, n := range VarNames(args) {
			key := qual(n)
			if _, ok := s.datasets[key]; !ok {
				s.Log.Warn(st.Line, fmt.Sprintf("The file %s.%s (memtype=DATA) was not found, but appears on a DELETE statement.", lib, strings.ToUpper(n)))
				continue
			}
			delete(s.datasets, key)
			if s.Last == key {
				s.Last = ""
			}
			s.Log.Notef(st.Line, "Deleting %s (memtype=DATA).", DisplayName(key))
		}
	}
	return nil
}
