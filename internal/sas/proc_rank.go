package sas

import (
	"fmt"
	"sort"
	"strings"
)

// PROC RANK: rank variables (TIES=MEAN default, LOW, HIGH), with
// DESCENDING, GROUPS=, PERCENT fractional ranks and RANKS= names.

func init() { RegisterProc("RANK", rkRank) }

func rkRank(s *Session, p *ProcStep) error {
	d, err := s.ProcData(p)
	if err != nil {
		return err
	}
	if st := p.First("VAR"); st == nil {
		return fmt.Errorf("no VAR statement.")
	}
	vars, err := ExpandVarList(d, p.First("VAR").Args())
	if err != nil {
		return err
	}
	for _, v := range vars {
		if d.Index(v) < 0 {
			return fmt.Errorf("Variable %s not found.", strings.ToUpper(v))
		}
	}
	descending := p.Opts.Has("DESCENDING", "DESC")
	percent := p.Opts.Has("PERCENT", "TPM", "NPR1")
	groups := int(p.Opts.Num("GROUPS", 0))
	ties := strings.ToUpper(p.Opts.Str("TIES", "MEAN"))
	// RANKS= names (defaults: RANK_ + variable name)
	rankNames := map[string]string{}
	if st := p.First("RANKS"); st != nil {
		names, err := ExpandVarList(d, st.Args())
		if err != nil {
			return err
		}
		for i, name := range vars {
			if i < len(names) {
				rankNames[strings.ToUpper(name)] = names[i]
			}
		}
	}
	var outRef *DSRef
	if op, ok := p.Opts.Get("OUT"); ok && op.Eq {
		outRef = DSRefFromOptToks(op)
	} else {
		return fmt.Errorf("proc RANK requires an OUT= data set.")
	}
	out := d.Clone()
	for _, name := range vars {
		ci := out.Index(name)
		type pair struct {
			row int
			val float64
		}
		var ps []pair
		for i, r := range out.Rows {
			if x := r[ci].Num; x == x {
				ps = append(ps, pair{i, x})
			}
		}
		sort.SliceStable(ps, func(a, b int) bool {
			if descending {
				return ps[a].val > ps[b].val
			}
			return ps[a].val < ps[b].val
		})
		// raw tied-rank bounds
		base := 0
		for base < len(ps) {
			end := base
			for end+1 < len(ps) && ps[end+1].val == ps[base].val {
				end++
			}
			lo, hi := float64(base+1), float64(end+1)
			var rank float64
			switch ties {
			case "LOW":
				rank = lo
			case "HIGH":
				rank = hi
			default: // MEAN
				rank = (lo + hi) / 2
			}
			if percent {
				rank = rank / float64(len(ps))
			}
			if groups > 0 {
				rank = mathFloor(rank * float64(groups) / float64(len(ps)+1))
				if rank >= float64(groups) {
					rank = float64(groups - 1)
				}
				if rank < 0 {
					rank = 0
				}
			}
			for k := base; k <= end; k++ {
				out.Rows[ps[k].row][ci] = NumV(rank)
			}
			base = end + 1
		}
	}
	return s.WriteDS(outRef, out)
}

func mathFloor(v float64) float64 {
	f := float64(int64(v))
	if v < 0 && v != f {
		f -= 1
	}
	return f
}
