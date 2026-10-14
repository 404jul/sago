package sas

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

// PROC FREQ (fq prefix): one-way and n-way frequency tables, chi-square,
// Fisher's exact, TESTP/TESTF goodness-of-fit, OUT=, ODS tables
// OneWayFreqs, OneWayChiSq, CrossTabFreqs, ChiSq, FishersExact.

func init() { RegisterProc("FREQ", fqFreq) }

type fqDim struct {
	Name    string
	Char    bool
	Numeric bool // numeric variable -> table scores are the values
	Levels  []Value
	Count   []float64
}

func (dm *fqDim) indexOf(v Value) int {
	k := FormatValue(v, nil)
	for i, l := range dm.Levels {
		if FormatValue(l, nil) == k {
			return i
		}
	}
	return -1
}

type fqTableSpec struct {
	Vars []string
	Opts *Opts
}

func fqFreq(s *Session, p *ProcStep) error {
	d, err := s.ProcData(p)
	if err != nil {
		return err
	}
	by, groups, err := s.ProcBy(p, d)
	if err != nil {
		return err
	}
	stmts := p.Stmts("TABLES", "TABLE")
	if len(stmts) == 0 {
		return fmt.Errorf("no TABLES statement.")
	}
	reqs := make([]fqTableSpec, 0, 8)
	for _, st := range stmts {
		r, err := fqParseReqs(d, st)
		if err != nil {
			return err
		}
		reqs = append(reqs, r...)
	}
	var weightCi = -1
	if st := p.First("WEIGHT"); st != nil && len(st.Args()) > 0 {
		weightCi = d.Index(st.Args()[0].Text)
		if weightCi < 0 {
			return fmt.Errorf("Variable %s not found.", strings.ToUpper(st.Args()[0].Text))
		}
	}
	var outAll *Dataset
	for _, g := range groups {
		sub := d.Subset(g.Rows)
		if by != nil {
			s.Out.SetBy(by.Vars, g.Keys)
		}
		for _, req := range reqs {
			opts := req.Opts
			dims, counts, nmiss := fqTabulate(sub, req.Vars, weightCi, opts.Has("MISSING"))
			if opts.Has("MISSPRINT") && nmiss > 0 {
				// shown as a row but not part of percents
				_ = nmiss
			}
			var ds *Dataset
			if len(req.Vars) == 1 {
				ds, err = fqOneWay(s, req.Vars[0], dims[0], counts, nmiss, opts, !opts.Has("NOPRINT"))
			} else {
				ds, err = fqCrossTab(s, req.Vars, dims, counts, nmiss, opts, !opts.Has("NOPRINT"))
			}
			if err != nil {
				return err
			}
			if op, ok := opts.Get("OUT"); ok && op.Eq {
				if err := s.WriteDS(DSRefFromOptToks(op), ds.Clone()); err != nil {
					return err
				}
			}
			if outAll == nil {
				outAll = ds
			} else {
				appendDataset(outAll, ds)
			}
		}
	}
	return nil
}

// fqParseReqs parses one TABLES statement's arguments.
func fqParseReqs(d *Dataset, st *Stmt) ([]fqTableSpec, error) {
	main, after := SplitSlash(st.Args())
	opts := ParseOpts(after)
	var specs []fqTableSpec
	c := NewCursor(main)
	for !c.AtEnd() {
		var items [][]string
		for {
			item, err := fqReadItem(d, c)
			if err != nil {
				return nil, err
			}
			items = append(items, item)
			if !c.AcceptOp("*") {
				break
			}
		}
		// positional pairing of items
		k := 0
		for _, it := range items {
			if len(it) > k {
				k = len(it)
			}
		}
		for pos := 0; pos < k; pos++ {
			var vars []string
			for _, it := range items {
				n := it[0]
				if pos < len(it) {
					n = it[pos]
				}
				vars = append(vars, n)
			}
			specs = append(specs, fqTableSpec{Vars: vars, Opts: opts})
		}
	}
	return specs, nil
}

func fqReadItem(d *Dataset, c *Cursor) ([]string, error) {
	if c.AcceptOp("(") {
		var names []string
		for !c.AcceptOp(")") {
			if c.AtEnd() {
				return nil, fmt.Errorf("unbalanced parentheses in TABLES statement.")
			}
			t := c.Next()
			if t.Kind == TIdent {
				if d.Index(t.Text) < 0 {
					return nil, fmt.Errorf("Variable %s not found.", strings.ToUpper(t.Text))
				}
				names = append(names, d.Vars[d.Index(t.Text)].Name)
			}
		}
		return names, nil
	}
	t := c.Next()
	if t.Kind != TIdent {
		return nil, fmt.Errorf("unexpected '%s' in TABLES statement.", t.Text)
	}
	if d.Index(t.Text) < 0 {
		return nil, fmt.Errorf("Variable %s not found.", strings.ToUpper(t.Text))
	}
	return []string{d.Vars[d.Index(t.Text)].Name}, nil
}

// fqTabulate computes per-dimension level lists (sorted, SAS order) and the
// multi-way count map keyed by level indices.
func fqTabulate(d *Dataset, vars []string, weightCi int, missing bool) ([]*fqDim, map[string]float64, float64) {
	cis := make([]int, len(vars))
	dims := make([]*fqDim, len(vars))
	// pass 1: distinct values per dimension
	for i, v := range vars {
		cis[i] = d.Index(v)
		dims[i] = &fqDim{Name: d.Vars[cis[i]].Name, Char: d.Vars[cis[i]].Char, Numeric: !d.Vars[cis[i]].Char}
	}
	var nmiss float64
	for _, row := range d.Rows {
		anyMiss := false
		for _, ci := range cis {
			if row[ci].IsMissing() {
				anyMiss = true
				break
			}
		}
		if anyMiss && !missing {
			nmiss++
			continue
		}
		for i, ci := range cis {
			v := row[ci]
			if v.IsMissing() {
				v = Missing
			}
			dims[i].addLevel(v)
		}
	}
	for _, dm := range dims {
		dm.sortLevels()
	}
	// pass 2: counts
	counts := map[string]float64{}
	buf := make([]string, len(cis))
	for _, row := range d.Rows {
		w := 1.0
		if weightCi >= 0 {
			w = row[weightCi].AsNum()
			if math.IsNaN(w) || w <= 0 {
				continue
			}
		}
		anyMiss := false
		for _, ci := range cis {
			if row[ci].IsMissing() {
				anyMiss = true
				break
			}
		}
		if anyMiss && !missing {
			continue
		}
		ok := true
		for i, ci := range cis {
			v := row[ci]
			if v.IsMissing() {
				v = Missing
			}
			li := dims[i].indexOf(v)
			if li < 0 {
				ok = false
				break
			}
			buf[i] = fqKey(li)
		}
		if ok {
			counts[strings.Join(buf, ",")] += w
		}
	}
	return dims, counts, nmiss
}

func fqKey(i int) string { return strconv.Itoa(i) }

func (dm *fqDim) addLevel(v Value) {
	k := FormatValue(v, nil)
	for _, l := range dm.Levels {
		if FormatValue(l, nil) == k {
			return
		}
	}
	dm.Levels = append(dm.Levels, v)
}

func (dm *fqDim) sortLevels() {
	sort.SliceStable(dm.Levels, func(a, b int) bool {
		x, y := dm.Levels[a], dm.Levels[b]
		xm, ym := x.IsMissing(), y.IsMissing()
		if xm != ym {
			return xm
		}
		return Compare(x, y) < 0
	})
}

func cellCount(counts map[string]float64, idx []int) float64 {
	parts := make([]string, len(idx))
	for i, v := range idx {
		parts[i] = fmt.Sprint(v)
	}
	return counts[strings.Join(parts, ",")]
}

// fqScores returns table scores for a dimension.
func fqScores(dm *fqDim) []float64 {
	out := make([]float64, len(dm.Levels))
	for i, v := range dm.Levels {
		if dm.Numeric && !v.IsMissing() {
			out[i] = v.AsNum()
		} else {
			out[i] = float64(i + 1)
		}
	}
	return out
}

// DSRefFromOptToks converts an OUT= option value into a DSRef.
func DSRefFromOptToks(op Opt) *DSRef {
	r, _ := ParseDSRef(NewCursor(op.Val))
	return r
}
