package sas

import (
	"fmt"
	"html"
	"math"
	"math/bits"
	"sort"
	"strconv"
	"strings"
)

func init() {
	RegisterProc("MEANS", func(s *Session, p *ProcStep) error { return bpMeans(s, p, false) })
	RegisterProc("SUMMARY", func(s *Session, p *ProcStep) error { return bpMeans(s, p, true) })
}

// bpStatDef describes a PROC MEANS statistic keyword.
type bpStatDef struct {
	Key   string // canonical keyword
	Auto  string // AUTONAME / ODS suffix
	Label string // printed column header
}

var bpStatAlias = map[string]string{
	"STDDEV": "STD", "SKEW": "SKEWNESS", "KURT": "KURTOSIS", "PRT": "PROBT", "VARIANCE": "VAR",
}

func bpPctlLabel(n int) string {
	suf := "th"
	switch n {
	case 1:
		suf = "st"
	}
	return strconv.Itoa(n) + suf + " Pctl"
}

var bpPctls = []int{1, 5, 10, 20, 25, 30, 40, 50, 60, 70, 75, 80, 90, 95, 99}

// bpLookupStat resolves a statistic keyword (nil if not a statistic).
func bpLookupStat(kw string, alpha float64) *bpStatDef {
	k := strings.ToUpper(kw)
	if a, ok := bpStatAlias[k]; ok {
		k = a
	}
	cl := strconv.FormatFloat(100*(1-alpha), 'f', -1, 64)
	m := map[string][2]string{
		"N": {"N", "N"}, "NMISS": {"NMiss", "N Miss"}, "MEAN": {"Mean", "Mean"},
		"STD": {"StdDev", "Std Dev"}, "VAR": {"Var", "Variance"}, "MIN": {"Min", "Minimum"},
		"MAX": {"Max", "Maximum"}, "RANGE": {"Range", "Range"}, "SUM": {"Sum", "Sum"},
		"SUMWGT": {"SumWgt", "Sum Wgts"}, "USS": {"USS", "Uncorrected SS"}, "CSS": {"CSS", "Corrected SS"},
		"STDERR": {"StdErr", "Std Error"}, "CV": {"CV", "Coeff of Variation"},
		"SKEWNESS": {"Skew", "Skewness"}, "KURTOSIS": {"Kurt", "Kurtosis"},
		"MEDIAN": {"Median", "Median"}, "Q1": {"Q1", "Lower Quartile"}, "Q3": {"Q3", "Upper Quartile"},
		"QRANGE": {"QRange", "Quartile Range"}, "MODE": {"Mode", "Mode"},
		"LCLM": {"LCLM", "Lower " + cl + "% CL for Mean"}, "UCLM": {"UCLM", "Upper " + cl + "% CL for Mean"},
		"T": {"t", "t Value"}, "PROBT": {"Probt", "Pr > |t|"},
	}
	for _, n := range bpPctls {
		m["P"+strconv.Itoa(n)] = [2]string{"P" + strconv.Itoa(n), bpPctlLabel(n)}
	}
	if v, ok := m[k]; ok {
		return &bpStatDef{Key: k, Auto: v[0], Label: v[1]}
	}
	return nil
}

// bpObs is one analysis observation: value, frequency and weight.
type bpObs struct{ x, f, w float64 }

// bpCalc holds computed statistics for one variable in one cell.
type bpCalc struct {
	vals          map[string]float64
	sorted        []bpObs // non-missing sorted by x (for quantiles)
	weighted      bool
	quantilesDone bool
}

func (c *bpCalc) get(key string) float64 {
	if v, ok := c.vals[key]; ok {
		return v
	}
	if !c.quantilesDone {
		c.quantiles()
	}
	if v, ok := c.vals[key]; ok {
		return v
	}
	return math.NaN()
}

// bpWeightedPctl is SAS's weighted percentile definition (which reduces to
// PCTLDEF=5 for unit weights / integer frequencies).
func bpWeightedPctl(s []bpObs, p float64) float64 {
	if len(s) == 0 {
		return math.NaN()
	}
	total := 0.0
	for _, o := range s {
		total += o.f * o.w
	}
	target := p / 100 * total
	eps := 1e-12 * math.Max(1, total)
	cum := 0.0
	for i, o := range s {
		cum += o.f * o.w
		if math.Abs(cum-target) <= eps {
			if i+1 < len(s) {
				return (o.x + s[i+1].x) / 2
			}
			return o.x
		}
		if cum > target {
			return o.x
		}
	}
	return s[len(s)-1].x
}

func (c *bpCalc) quantiles() {
	c.quantilesDone = true
	q := func(p float64) float64 {
		if !c.weighted {
			allOne := true
			for _, o := range c.sorted {
				if o.f != 1 {
					allOne = false
					break
				}
			}
			if allOne {
				xs := make([]float64, len(c.sorted))
				for i, o := range c.sorted {
					xs[i] = o.x
				}
				return Percentile(xs, p)
			}
		}
		pos := c.sorted[:0:0]
		for _, o := range c.sorted {
			if o.f*o.w > 0 {
				pos = append(pos, o)
			}
		}
		return bpWeightedPctl(pos, p)
	}
	for _, n := range bpPctls {
		c.vals["P"+strconv.Itoa(n)] = q(float64(n))
	}
	c.vals["MEDIAN"] = c.vals["P50"]
	c.vals["Q1"] = c.vals["P25"]
	c.vals["Q3"] = c.vals["P75"]
	c.vals["QRANGE"] = c.vals["Q3"] - c.vals["Q1"]
	// mode: most frequent value (by frequency), smallest on ties
	mode, bestN, distinct := math.NaN(), 0.0, 0
	for i := 0; i < len(c.sorted); {
		j, cnt := i, 0.0
		for j < len(c.sorted) && c.sorted[j].x == c.sorted[i].x {
			cnt += c.sorted[j].f
			j++
		}
		distinct++
		if cnt > bestN {
			mode, bestN = c.sorted[i].x, cnt
		}
		i = j
	}
	if bestN <= 1 && distinct > 1 {
		mode = math.NaN()
	}
	c.vals["MODE"] = mode
}

// bpCompute computes moments for obs (missing x already counted in nmiss).
func bpCompute(obs []bpObs, nmiss float64, weighted bool, vardef string, alpha float64) *bpCalc {
	c := &bpCalc{vals: map[string]float64{}, weighted: weighted}
	n, sumw, sum := 0.0, 0.0, 0.0
	mn, mx := math.Inf(1), math.Inf(-1)
	for _, o := range obs {
		n += o.f
		sumw += o.f * o.w
		sum += o.f * o.w * o.x
		mn = math.Min(mn, o.x)
		mx = math.Max(mx, o.x)
	}
	nan := math.NaN()
	c.vals["N"] = n
	c.vals["NMISS"] = nmiss
	if n == 0 {
		for _, k := range []string{"MEAN", "STD", "VAR", "MIN", "MAX", "RANGE", "SUM", "SUMWGT", "USS", "CSS", "STDERR", "CV", "SKEWNESS", "KURTOSIS", "LCLM", "UCLM", "T", "PROBT"} {
			c.vals[k] = nan
		}
		c.quantilesDone = true
		return c
	}
	mean := nan
	if sumw > 0 {
		mean = sum / sumw
	}
	css, uss, comp := 0.0, 0.0, 0.0
	for _, o := range obs {
		d := o.x - mean
		css += o.f * o.w * d * d
		comp += o.f * o.w * d
		uss += o.f * o.w * o.x * o.x
	}
	if sumw > 0 {
		css -= comp * comp / sumw
	}
	div := nan
	switch vardef {
	case "N":
		div = n
	case "WDF":
		div = sumw - 1
	case "WEIGHT", "WGT":
		div = sumw
	default:
		div = n - 1
	}
	variance := nan
	if div > 0 {
		variance = css / div
	}
	std := math.Sqrt(variance)
	stderr, skew, kurt := nan, nan, nan
	if vardef == "DF" && n > 1 && sumw > 0 {
		stderr = std / math.Sqrt(sumw)
	}
	sDF := math.Sqrt(css / (n - 1))
	if vardef == "DF" && sDF > 0 {
		s3, s4 := 0.0, 0.0
		for _, o := range obs {
			z := (o.x - mean) / sDF
			s3 += o.f * math.Pow(o.w, 1.5) * z * z * z
			s4 += o.f * o.w * o.w * z * z * z * z
		}
		if n > 2 {
			skew = n / ((n - 1) * (n - 2)) * s3
		}
		if n > 3 {
			kurt = n*(n+1)/((n-1)*(n-2)*(n-3))*s4 - 3*(n-1)*(n-1)/((n-2)*(n-3))
		}
	}
	c.vals["MEAN"] = mean
	c.vals["VAR"] = variance
	c.vals["STD"] = std
	c.vals["MIN"] = mn
	c.vals["MAX"] = mx
	c.vals["RANGE"] = mx - mn
	c.vals["SUM"] = sum
	c.vals["SUMWGT"] = sumw
	c.vals["USS"] = uss
	c.vals["CSS"] = css
	c.vals["STDERR"] = stderr
	c.vals["CV"] = 100 * std / mean
	if mean == 0 {
		c.vals["CV"] = nan
	}
	c.vals["SKEWNESS"] = skew
	c.vals["KURTOSIS"] = kurt
	t := mean / stderr
	tq := TInv(1-alpha/2, n-1)
	c.vals["T"] = t
	c.vals["PROBT"] = TTwoSided(t, n-1)
	if math.IsNaN(stderr) || n < 2 {
		c.vals["T"], c.vals["PROBT"] = nan, nan
	}
	c.vals["LCLM"] = mean - tq*stderr
	c.vals["UCLM"] = mean + tq*stderr
	c.sorted = append([]bpObs(nil), obs...)
	sort.SliceStable(c.sorted, func(a, b int) bool { return c.sorted[a].x < c.sorted[b].x })
	return c
}

// bpMeansFmt mimics PROC MEANS default display (D12.-like): 7 decimals when
// the value fits in 11 characters, else 2 decimals, else BEST12.
func bpMeansFmt(x float64, maxdec int, key string) string {
	if math.IsNaN(x) || math.IsInf(x, 0) {
		return "."
	}
	switch key {
	case "N", "NMISS":
		return strconv.FormatFloat(x, 'f', -1, 64)
	case "PROBT":
		return FormatByName(NumV(x), "PVALUE6.4")
	}
	if maxdec >= 0 {
		return formatFixed(x, 0, maxdec)
	}
	ax := math.Abs(x)
	if s := strconv.FormatFloat(ax, 'f', 7, 64); len(s) <= 11 {
		return formatFixed(x, 0, 7)
	}
	if s := strconv.FormatFloat(ax, 'f', 2, 64); len(s) <= 12 {
		return formatFixed(x, 0, 2)
	}
	return formatBest(x, 12)
}

// bpOutSpec is one "stat(vars)=names" item of an OUTPUT statement.
type bpOutSpec struct {
	stat  *bpStatDef
	vars  []string
	names []string
}

type bpOutput struct {
	ref                            *DSRef
	specs                          []bpOutSpec
	autoname, autolabel, noinherit bool
	line                           int
}

func bpParseOutput(st *Stmt, d *Dataset, alpha float64) (*bpOutput, error) {
	before, after := SplitSlash(st.Args())
	o := &bpOutput{line: st.Line}
	for _, t := range after {
		switch {
		case t.Is("AUTONAME"):
			o.autoname = true
		case t.Is("AUTOLABEL"):
			o.autolabel = true
		case t.Is("NOINHERIT"):
			o.noinherit = true
		}
	}
	c := NewCursor(before)
	for !c.AtEnd() {
		t := c.Next()
		if t.Is("OUT") && c.Peek().IsOp("=") {
			c.Next()
			ref, err := ParseDSRef(c)
			if err != nil {
				return nil, err
			}
			ref.Line = st.Line
			o.ref = ref
			continue
		}
		if t.Kind != TIdent {
			return nil, fmt.Errorf("Syntax error in OUTPUT statement near '%s'.", t.Text)
		}
		kw := t.Upper()
		var vars []string
		if g, ok := c.Group(); ok {
			names, err := ExpandVarList(d, g)
			if err != nil {
				return nil, err
			}
			vars = names
		}
		defs := []*bpStatDef{bpLookupStat(kw, alpha)}
		if kw == "CLM" {
			defs = []*bpStatDef{bpLookupStat("LCLM", alpha), bpLookupStat("UCLM", alpha)}
		}
		if defs[0] == nil {
			if kw == "IDGROUP" || kw == "MAXID" || kw == "MINID" {
				continue
			}
			return nil, fmt.Errorf("Statistic %s is not valid in the OUTPUT statement.", kw)
		}
		var names []string
		if c.AcceptOp("=") {
			for !c.AtEnd() {
				n := c.Peek()
				if n.Kind != TIdent || c.PeekN(1).IsOp("=", "(") {
					break
				}
				names = append(names, c.Next().Text)
			}
		}
		for _, def := range defs {
			nm := names
			if len(defs) > 1 {
				nm = nil // CLM= takes generated names
			}
			o.specs = append(o.specs, bpOutSpec{stat: def, vars: vars, names: nm})
		}
	}
	if o.ref == nil {
		o.ref = &DSRef{Name: bpNextDataName(), Line: st.Line, Opts: &Opts{}}
	}
	return o, nil
}

// bpNextDataName mimics SAS's DATAn naming for OUTPUT without OUT=.
var bpDataN = 0

func bpNextDataName() string {
	bpDataN++
	return "DATA" + strconv.Itoa(bpDataN)
}

// cell key for class combinations
type bpCell struct {
	typ  int
	keys []Value // values for all class vars (missing for vars not in type)
	rows [][]Value
}

func bpMeans(s *Session, p *ProcStep, summary bool) error {
	d, err := s.ProcData(p)
	if err != nil {
		return err
	}
	o := p.Opts
	alpha := o.Num("ALPHA", 0.05)
	vardef := strings.ToUpper(o.Str("VARDEF", "DF"))
	maxdec := int(o.Num("MAXDEC", -1))
	noprint := o.Has("NOPRINT") || (summary && !o.Has("PRINT"))
	missingClass := o.Has("MISSING")
	nway := o.Has("NWAY")

	var stats []*bpStatDef
	for _, op := range o.List {
		if op.Eq {
			continue
		}
		if op.Key == "CLM" {
			stats = append(stats, bpLookupStat("LCLM", alpha), bpLookupStat("UCLM", alpha))
			continue
		}
		if def := bpLookupStat(op.Key, alpha); def != nil {
			stats = append(stats, def)
		}
	}
	if len(stats) == 0 {
		for _, k := range []string{"N", "MEAN", "STD", "MIN", "MAX"} {
			stats = append(stats, bpLookupStat(k, alpha))
		}
	}

	by, groups, err := s.ProcBy(p, d)
	if err != nil {
		return err
	}
	used := map[int]bool{}
	if by != nil {
		for _, i := range by.Idx {
			used[i] = true
		}
	}
	var classIdx []int
	for _, st := range p.Stmts("CLASS", "CLASSES") {
		args, _ := SplitSlash(st.Args())
		names, err := ExpandVarList(d, args)
		if err != nil {
			return err
		}
		ix, err := d.Indices(names)
		if err != nil {
			return err
		}
		classIdx = append(classIdx, ix...)
		if _, after := SplitSlash(st.Args()); after != nil {
			for _, t := range after {
				if t.Is("MISSING") {
					missingClass = true
				}
			}
		}
	}
	for _, i := range classIdx {
		used[i] = true
	}
	single := func(kw string) (int, error) {
		st := p.First(kw)
		if st == nil || len(st.Args()) == 0 {
			return -1, nil
		}
		i := d.Index(st.Args()[0].Text)
		if i < 0 {
			return -1, fmt.Errorf("Variable %s not found.", st.Args()[0].Upper())
		}
		used[i] = true
		return i, nil
	}
	wIdx, err := single("WEIGHT")
	if err != nil {
		return err
	}
	fIdx, err := single("FREQ")
	if err != nil {
		return err
	}
	var idIdx []int
	if st := p.First("ID"); st != nil {
		names, err := ExpandVarList(d, st.Args())
		if err != nil {
			return err
		}
		idIdx, _ = d.Indices(names)
		for _, i := range idIdx {
			used[i] = true
		}
	}
	var varIdx []int
	if sts := p.Stmts("VAR", "VARIABLES"); len(sts) > 0 {
		for _, st := range sts {
			args, _ := SplitSlash(st.Args())
			names, err := ExpandVarList(d, args)
			if err != nil {
				return err
			}
			ix, err := d.Indices(names)
			if err != nil {
				return err
			}
			// _ALL_ is restricted to numeric variables; explicitly named
			// character variables are an error.
			allTok := false
			for _, t := range args {
				if t.Kind == TIdent && strings.EqualFold(t.Text, "_ALL_") {
					allTok = true
					break
				}
			}
			for _, i := range ix {
				if d.Vars[i].Char {
					if allTok {
						continue
					}
					return fmt.Errorf("Variable %s in list does not match type prescribed for this list.", d.Vars[i].Name)
				}
				varIdx = append(varIdx, i)
			}
		}
	} else {
		for i, v := range d.Vars {
			if !v.Char && !used[i] {
				varIdx = append(varIdx, i)
			}
		}
	}
	if len(varIdx) == 0 && len(p.Stmts("OUTPUT")) == 0 {
		return fmt.Errorf("There are no valid analysis variables.")
	}

	k := len(classIdx)
	full := (1 << k) - 1
	// requested types (TYPES / WAYS)
	var reqTypes []int
	if st := p.First("TYPES"); st != nil {
		seen := map[int]bool{}
		c := NewCursor(st.Args())
		for !c.AtEnd() {
			if g, ok := c.Group(); ok && len(g) == 0 {
				seen[0] = true
				continue
			}
			typ := 0
			for {
				t := c.Next()
				for j, ci := range classIdx {
					if strings.EqualFold(d.Vars[ci].Name, t.Text) {
						typ |= 1 << (k - 1 - j)
					}
				}
				if !c.AcceptOp("*") {
					break
				}
			}
			seen[typ] = true
		}
		for t := range seen {
			reqTypes = append(reqTypes, t)
		}
	}
	if st := p.First("WAYS"); st != nil {
		ways := map[int]bool{}
		for _, t := range st.Args() {
			if t.Kind == TNumber {
				ways[int(t.Num)] = true
			}
		}
		for t := range full + 1 {
			if ways[bits.OnesCount(uint(t))] {
				reqTypes = append(reqTypes, t)
			}
		}
	}
	sort.Ints(reqTypes)
	outTypes := reqTypes
	if reqTypes == nil {
		for t := range full + 1 {
			outTypes = append(outTypes, t)
		}
	}
	if nway {
		outTypes = []int{full}
	}
	printTypes := reqTypes
	if printTypes == nil || nway {
		printTypes = []int{full}
	}

	var outputs []*bpOutput
	for _, st := range p.Stmts("OUTPUT") {
		out, err := bpParseOutput(st, d, alpha)
		if err != nil {
			return err
		}
		outputs = append(outputs, out)
	}

	// cells per BY group
	allTypes := map[int]bool{}
	for _, t := range outTypes {
		if len(outputs) > 0 {
			allTypes[t] = true
		}
	}
	for _, t := range printTypes {
		if !noprint {
			allTypes[t] = true
		}
	}
	var typeList []int
	for t := range allTypes {
		typeList = append(typeList, t)
	}
	sort.Ints(typeList)

	type grpResult struct {
		keys  []Value
		cells []*bpCell
		calc  map[*bpCell][]*bpCalc
	}
	var results []grpResult
	weighted := wIdx >= 0
	for _, g := range groups {
		rows := g.Rows
		if k > 0 && !missingClass {
			kept := make([][]Value, 0, len(rows))
			for _, r := range rows {
				ok := true
				for _, ci := range classIdx {
					if r[ci].IsMissing() {
						ok = false
						break
					}
				}
				if ok {
					kept = append(kept, r)
				}
			}
			rows = kept
		}
		if fIdx >= 0 {
			kept := make([][]Value, 0, len(rows))
			for _, r := range rows {
				if f := r[fIdx].Num; !math.IsNaN(f) && f >= 1 {
					kept = append(kept, r)
				}
			}
			rows = kept
		}
		gr := grpResult{keys: g.Keys, calc: map[*bpCell][]*bpCalc{}}
		for _, typ := range typeList {
			gr.cells = append(gr.cells, bpSplitCells(rows, classIdx, typ, k)...)
		}
		for _, cell := range gr.cells {
			cs := make([]*bpCalc, len(varIdx))
			for vi, ix := range varIdx {
				obs := make([]bpObs, 0, len(cell.rows))
				nmiss := 0.0
				for _, r := range cell.rows {
					f := 1.0
					if fIdx >= 0 {
						f = math.Trunc(r[fIdx].Num)
					}
					w := 1.0
					if weighted {
						w = r[wIdx].Num
						if math.IsNaN(w) {
							continue
						}
						if w < 0 {
							w = 0
						}
					}
					x := r[ix].Num
					if math.IsNaN(x) {
						nmiss += f
						continue
					}
					obs = append(obs, bpObs{x, f, w})
				}
				cs[vi] = bpCompute(obs, nmiss, weighted, vardef, alpha)
			}
			gr.calc[cell] = cs
		}
		results = append(results, gr)
	}

	// printed output
	if !noprint && len(varIdx) > 0 {
		pt := map[int]bool{}
		for _, t := range printTypes {
			pt[t] = true
		}
		for _, gr := range results {
			if by != nil {
				s.Out.SetBy(by.Vars, gr.keys)
			}
			var cells []*bpCell
			for _, c := range gr.cells {
				if pt[c.typ] {
					cells = append(cells, c)
				}
			}
			bpPrintSummary(s, d, classIdx, varIdx, stats, cells, gr.calc, maxdec, fIdx)
		}
		if by != nil {
			s.Out.SetBy(nil, nil)
		}
	}
	bpObsRead(s, p.Line, d)

	// output data sets
	for _, out := range outputs {
		od := &Dataset{}
		var byVars []*Var
		if by != nil {
			for _, v := range by.Vars {
				byVars = append(byVars, v.Clone())
			}
		}
		od.Vars = append(od.Vars, byVars...)
		for _, ci := range classIdx {
			od.Vars = append(od.Vars, d.Vars[ci].Clone())
		}
		od.Vars = append(od.Vars, &Var{Name: "_TYPE_", Length: 8}, &Var{Name: "_FREQ_", Length: 8})
		for _, ii := range idIdx {
			od.Vars = append(od.Vars, d.Vars[ii].Clone())
		}
		type col struct {
			stat string
			vi   int
		}
		var cols []col
		defaultStat := len(out.specs) == 0
		if defaultStat {
			od.Vars = append(od.Vars, &Var{Name: "_STAT_", Char: true, Length: 8})
			for vi, ix := range varIdx {
				v := d.Vars[ix].Clone()
				od.Vars = append(od.Vars, v)
				cols = append(cols, col{"", vi})
			}
		} else {
			for _, sp := range out.specs {
				vlist := varIdx
				if sp.vars != nil {
					vlist = nil
					for _, n := range sp.vars {
						vlist = append(vlist, d.Index(n))
					}
				}
				count := len(vlist)
				if len(sp.names) > 0 && len(sp.names) < count {
					count = len(sp.names)
				}
				for j := 0; j < count; j++ {
					src := d.Vars[vlist[j]]
					vi := -1
					for q, ix := range varIdx {
						if ix == vlist[j] {
							vi = q
						}
					}
					if vi < 0 {
						return fmt.Errorf("Variable %s in OUTPUT statement is not an analysis variable.", src.Name)
					}
					var name string
					switch {
					case j < len(sp.names):
						name = sp.names[j]
					case out.autoname:
						name = src.Name + "_" + sp.stat.Auto
					default:
						name = src.Name
					}
					if od.Index(name) >= 0 {
						if out.autoname && j >= len(sp.names) {
							continue
						}
						s.Log.Warn(out.line, fmt.Sprintf("Variable %s already exists on file %s.", name, DisplayName(out.ref.Name)))
						continue
					}
					v := &Var{Name: name, Length: 8}
					if !out.noinherit {
						if sp.stat.Key != "N" && sp.stat.Key != "NMISS" {
							v.Format = src.Format
						}
						v.Label = src.Label
					}
					if out.autolabel {
						v.Label = src.DisplayLabel() + "_" + sp.stat.Auto
					}
					od.Vars = append(od.Vars, v)
					cols = append(cols, col{sp.stat.Key, vi})
				}
			}
		}
		ot := map[int]bool{}
		for _, t := range outTypes {
			ot[t] = true
		}
		for _, gr := range results {
			for _, cell := range gr.cells {
				if !ot[cell.typ] {
					continue
				}
				base := append([]Value(nil), gr.keys...)
				base = append(base, cell.keys...)
				freq := 0.0
				for _, r := range cell.rows {
					if fIdx >= 0 {
						freq += math.Trunc(r[fIdx].Num)
					} else {
						freq++
					}
				}
				base = append(base, NumV(float64(cell.typ)), NumV(freq))
				for _, ii := range idIdx {
					var best Value = MissingOf(d.Vars[ii].Char)
					for ri, r := range cell.rows {
						if ri == 0 || Compare(r[ii], best) > 0 {
							best = r[ii]
						}
					}
					base = append(base, best)
				}
				cs := gr.calc[cell]
				if defaultStat {
					for _, st := range []string{"N", "MIN", "MAX", "MEAN", "STD"} {
						row := append(append([]Value(nil), base...), StrV(st))
						for _, c := range cols {
							row = append(row, NumV(cs[c.vi].get(st)))
						}
						od.Rows = append(od.Rows, row)
					}
					continue
				}
				row := base
				for _, c := range cols {
					row = append(row, NumV(cs[c.vi].get(c.stat)))
				}
				od.Rows = append(od.Rows, row)
			}
		}
		if err := s.WriteDS(out.ref, od); err != nil {
			return err
		}
	}
	return nil
}

// bpSplitCells groups rows by the class variables included in typ, ordered
// by class values.
func bpSplitCells(rows [][]Value, classIdx []int, typ, k int) []*bpCell {
	var inc []int
	for j, ci := range classIdx {
		if typ&(1<<(k-1-j)) != 0 {
			inc = append(inc, ci)
		}
	}
	mk := func(r []Value) []Value {
		keys := make([]Value, len(classIdx))
		for j, ci := range classIdx {
			if typ&(1<<(k-1-j)) != 0 {
				keys[j] = r[ci]
			} else {
				keys[j] = MissingOf(r[ci].Char)
			}
		}
		return keys
	}
	if len(inc) == 0 {
		keys := make([]Value, len(classIdx))
		for j := range classIdx {
			keys[j] = Missing
			if len(rows) > 0 {
				keys[j] = MissingOf(rows[0][classIdx[j]].Char)
			}
		}
		return []*bpCell{{typ: typ, keys: keys, rows: rows}}
	}
	sorted := make([][]Value, len(rows))
	copy(sorted, rows)
	SortRows(sorted, inc, nil)
	var cells []*bpCell
	for i := 0; i < len(sorted); {
		j := i + 1
		for j < len(sorted) && cmpRows(sorted[i], sorted[j], inc, nil) == 0 {
			j++
		}
		cells = append(cells, &bpCell{typ: typ, keys: mk(sorted[i]), rows: sorted[i:j:j]})
		i = j
	}
	return cells
}

// bpPrintSummary renders the "Summary" table: stacked display and a wide
// table (one row per class level, columns var_Stat) for ODS OUTPUT.
func bpPrintSummary(s *Session, d *Dataset, classIdx, varIdx []int, stats []*bpStatDef, cells []*bpCell, calc map[*bpCell][]*bpCalc, maxdec, fIdx int) {
	esc := html.EscapeString
	anyLabel := false
	for _, ix := range varIdx {
		if d.Vars[ix].Label != "" {
			anyLabel = true
		}
	}
	multi := len(varIdx) > 1
	capt := &Table{ODSName: "Summary"}
	for _, ci := range classIdx {
		v := d.Vars[ci]
		capt.Cols = append(capt.Cols, Col{Name: v.Name, Label: v.DisplayLabel(), Format: v.Format, Char: v.Char})
	}
	if len(classIdx) > 0 {
		capt.Cols = append(capt.Cols, Col{Name: "NObs", Label: "N Obs"})
	}
	for _, ix := range varIdx {
		v := d.Vars[ix]
		capt.Cols = append(capt.Cols, Col{Name: "VName_" + v.Name, Label: "Variable", Char: true})
		if v.Label != "" {
			capt.Cols = append(capt.Cols, Col{Name: "Label_" + v.Name, Label: "Label", Char: true})
		}
		for _, st := range stats {
			capt.Cols = append(capt.Cols, Col{Name: v.Name + "_" + st.Auto, Label: st.Label})
		}
	}

	var b strings.Builder
	b.WriteString("<table class=\"sas\">")
	if !multi && len(varIdx) == 1 {
		v := d.Vars[varIdx[0]]
		c := "Analysis Variable : " + v.Name
		if v.Label != "" {
			c += " " + v.Label
		}
		fmt.Fprintf(&b, "<caption>%s</caption>", esc(c))
	}
	b.WriteString("<thead><tr>")
	for _, ci := range classIdx {
		fmt.Fprintf(&b, "<th>%s</th>", esc(d.Vars[ci].DisplayLabel()))
	}
	if len(classIdx) > 0 {
		b.WriteString("<th>N Obs</th>")
	}
	if multi {
		b.WriteString("<th>Variable</th>")
		if anyLabel {
			b.WriteString("<th>Label</th>")
		}
	}
	for _, st := range stats {
		fmt.Fprintf(&b, "<th>%s</th>", esc(st.Label))
	}
	b.WriteString("</tr></thead><tbody>")
	for _, cell := range cells {
		cs := calc[cell]
		nobs := 0.0
		for _, r := range cell.rows {
			if fIdx >= 0 {
				nobs += math.Trunc(r[fIdx].Num)
			} else {
				nobs++
			}
		}
		crow := []Value{}
		crow = append(crow, cell.keys...)
		if len(classIdx) > 0 {
			crow = append(crow, NumV(nobs))
		}
		for vi, ix := range varIdx {
			v := d.Vars[ix]
			b.WriteString("<tr>")
			if vi == 0 {
				span := len(varIdx)
				for j, ci := range classIdx {
					fmt.Fprintf(&b, "<th rowspan=\"%d\">%s</th>", span, esc(FormatByName(cell.keys[j], d.Vars[ci].Format)))
				}
				if len(classIdx) > 0 {
					fmt.Fprintf(&b, "<td rowspan=\"%d\">%s</td>", span, strconv.FormatFloat(nobs, 'f', -1, 64))
				}
			}
			if multi {
				fmt.Fprintf(&b, "<th>%s</th>", esc(v.Name))
				if anyLabel {
					fmt.Fprintf(&b, "<td class=\"c\">%s</td>", esc(v.Label))
				}
			}
			crow = append(crow, StrV(v.Name))
			if v.Label != "" {
				crow = append(crow, StrV(v.Label))
			}
			for _, st := range stats {
				x := cs[vi].get(st.Key)
				crow = append(crow, NumV(x))
				fmt.Fprintf(&b, "<td>%s</td>", esc(bpMeansFmt(x, maxdec, st.Key)))
			}
			b.WriteString("</tr>")
		}
		capt.Rows = append(capt.Rows, crow)
	}
	b.WriteString("</tbody></table>")
	s.Out.AddHTML(&HTML{HTML: b.String(), Table: capt})
}
