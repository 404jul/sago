package sas

import (
	"fmt"
	"math"
	"strings"
)

// PROC STANDARD: standardize variables to a target mean and/or standard
// deviation (MEAN=/STD=); writes the result to OUT=.

func init() { RegisterProc("STANDARD", stStandard) }

func stStandard(s *Session, p *ProcStep) error {
	d, err := s.ProcData(p)
	if err != nil {
		return err
	}
	meanT := p.Opts.Num("MEAN", math.NaN())
	stdT := p.Opts.Num("STD", math.NaN())
	var outRef *DSRef // SAS requires OUT=; default suffix
	if op, ok := p.Opts.Get("OUT"); ok && op.Eq {
		outRef = DSRefFromOptToks(op)
	} else {
		return fmt.Errorf("proc STANDARD requires an OUT= data set.")
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
	by, groups, err := s.ProcBy(p, d)
	if err != nil {
		return err
	}
	out := d.Clone()
	type stStats struct {
		m, scale float64
	}
	statsByKey := map[string]*stStats{}
	byCis := []int{}
	if by != nil {
		for _, v := range by.Vars {
			byCis = append(byCis, d.Index(v.Name))
		}
	}
	rowKey := func(r []Value) string {
		if by == nil {
			return ""
		}
		parts := []string{}
		for _, ci := range byCis {
			parts = append(parts, FormatValue(r[ci], nil))
		}
		return strings.Join(parts, "\x00")
	}
	for _, g := range groups {
		sub := d.Subset(g.Rows)
		for _, name := range vars {
			ci := sub.Index(name)
			var vals []float64
			for _, r := range sub.Rows {
				if x := r[ci].Num; x == x {
					vals = append(vals, x)
				}
			}
			if len(vals) == 0 {
				continue
			}
			m := Mean(vals)
			sd := Variance(vals)
			if sd > 0 {
				sd = math.Sqrt(sd)
			} else {
				sd = math.NaN()
			}
			scale := 1.0
			if !math.IsNaN(stdT) && sd == sd {
				scale = stdT / sd
			}
			target := m
			if !math.IsNaN(meanT) {
				target = meanT
			}
			key := rowKey(sub.Rows[0])
			if statsByKey[key] == nil {
				statsByKey[key] = &stStats{}
			}
			statsByKey[key+"\x00"+name] = &stStats{m: m, scale: scale}
			_ = target
		}
	}
	for _, r := range out.Rows {
		key := rowKey(r)
		for _, name := range vars {
			ci := out.Index(name)
			x := r[ci].Num
			if x != x {
				continue
			}
			st := statsByKey[key+"\x00"+name]
			if st == nil {
				continue
			}
			target := st.m
			if !math.IsNaN(meanT) {
				target = meanT
			}
			r[ci] = NumV(target + (x-st.m)*st.scale)
		}
	}
	return s.WriteDS(outRef, out)
}
