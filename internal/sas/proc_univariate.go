package sas

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

func init() { RegisterProc("UNIVARIATE", uvRun) }

// uvSample is the non-missing data of one analysis variable in one group,
// sorted ascending, with weights and observation numbers aligned.
type uvSample struct {
	X     []float64
	W     []float64 // nil when unweighted
	Obs   []int     // 1-based observation numbers
	NMiss int
}

// uvMoments holds the descriptive statistics of a sample.
type uvMoments struct {
	N, SumW, Mean, Sum, Std, Var, Skew, Kurt, USS, CSS, CV, StdMean float64
}

func uvComputeMoments(s *uvSample) uvMoments {
	n := float64(len(s.X))
	m := uvMoments{N: n}
	if s.W == nil {
		m.SumW = n
		m.Sum = Sum(s.X)
		m.Mean = Mean(s.X)
		m.CSS = CSS(s.X)
		for _, v := range s.X {
			m.USS += v * v
		}
		m.Skew = Skewness(s.X)
		m.Kurt = Kurtosis(s.X)
	} else {
		for i, v := range s.X {
			m.SumW += s.W[i]
			m.Sum += s.W[i] * v
			m.USS += s.W[i] * v * v
		}
		m.Mean = m.Sum / m.SumW
		for i, v := range s.X {
			m.CSS += s.W[i] * (v - m.Mean) * (v - m.Mean)
		}
		m.Skew, m.Kurt = math.NaN(), math.NaN()
	}
	if n == 0 {
		m.Mean = math.NaN()
	}
	m.Var = math.NaN()
	if n > 1 {
		m.Var = m.CSS / (n - 1)
	}
	m.Std = math.Sqrt(m.Var)
	m.CV = 100 * m.Std / m.Mean
	m.StdMean = m.Std / math.Sqrt(m.SumW)
	return m
}

// uvPctl computes the p-th percentile (0..100) of sorted data using SAS
// PCTLDEF=1..5 (or the weighted definition when w is non-nil).
func uvPctl(x, w []float64, p float64, def int) float64 {
	n := len(x)
	if n == 0 {
		return math.NaN()
	}
	if p <= 0 {
		return x[0]
	}
	if p >= 100 {
		return x[n-1]
	}
	if w != nil {
		tot := 0.0
		for _, v := range w {
			tot += v
		}
		target := tot * p / 100
		cum := 0.0
		for i := range n {
			cum += w[i]
			if math.Abs(cum-target) <= 1e-12*tot && i+1 < n {
				return (x[i] + x[i+1]) / 2
			}
			if cum > target {
				return x[i]
			}
		}
		return x[n-1]
	}
	at := func(i int) float64 { // 1-based with clamping
		if i < 1 {
			return x[0]
		}
		if i > n {
			return x[n-1]
		}
		return x[i-1]
	}
	split := func(np float64) (int, float64) {
		j := math.Floor(np + 1e-9*math.Max(1, np))
		g := np - j
		if math.Abs(g) < 1e-9 {
			g = 0
		}
		return int(j), g
	}
	switch def {
	case 1:
		j, g := split(float64(n) * p / 100)
		return (1-g)*at(j) + g*at(j+1)
	case 2:
		j, g := split(float64(n) * p / 100)
		if math.Abs(g-0.5) < 1e-9 {
			if j%2 == 0 {
				return at(j)
			}
			return at(j + 1)
		}
		return at(int(math.Floor(float64(n)*p/100 + 0.5)))
	case 3:
		j, g := split(float64(n) * p / 100)
		if g == 0 {
			return at(j)
		}
		return at(j + 1)
	case 4:
		j, g := split(float64(n+1) * p / 100)
		return (1-g)*at(j) + g*at(j+1)
	}
	return Percentile(x, p)
}

// uvSignTest returns M and Pr >= |M| for values centered at mu0.
func uvSignTest(x []float64, mu0 float64) (float64, float64) {
	np, nm := 0, 0
	for _, v := range x {
		if v > mu0 {
			np++
		} else if v < mu0 {
			nm++
		}
	}
	nt := np + nm
	if nt == 0 {
		return 0, 1
	}
	m := float64(np-nm) / 2
	p := math.Min(1, 2*BinomCDF(min(np, nm), nt, 0.5))
	return m, p
}

// uvSignedRank returns the Wilcoxon signed rank statistic S and Pr >= |S|
// (exact distribution for n <= 20, t approximation otherwise; SAS rules).
func uvSignedRank(x []float64, mu0 float64) (float64, float64) {
	var d []float64
	for _, v := range x {
		if v != mu0 {
			d = append(d, v-mu0)
		}
	}
	n := len(d)
	if n == 0 {
		return 0, 1
	}
	abs := make([]float64, n)
	for i, v := range d {
		abs[i] = math.Abs(v)
	}
	r := Ranks(abs)
	tplus := 0.0
	for i, v := range d {
		if v > 0 {
			tplus += r[i]
		}
	}
	fn := float64(n)
	s := tplus - fn*(fn+1)/4
	if n <= 20 {
		// Exact permutation distribution of T+ over the (doubled, integer) ranks.
		tot := 0
		ri := make([]int, n)
		for i, v := range r {
			ri[i] = int(math.Round(2 * v))
			tot += ri[i]
		}
		cnt := make([]float64, tot+1)
		cnt[0] = 1
		for _, v := range ri {
			for k := tot; k >= v; k-- {
				cnt[k] += cnt[k-v]
			}
		}
		center := float64(tot) / 2
		obs := math.Abs(2*tplus - center)
		num := 0.0
		for k, c := range cnt {
			if math.Abs(float64(k)-center) >= obs-1e-7 {
				num += c
			}
		}
		return s, math.Min(1, num/math.Pow(2, fn))
	}
	v := fn * (fn + 1) * (2*fn + 1) / 24
	sorted := append([]float64(nil), abs...)
	sort.Float64s(sorted)
	for i := 0; i < n; {
		j := i
		for j < n && sorted[j] == sorted[i] {
			j++
		}
		t := float64(j - i)
		v -= t * (t*t - 1) / 48
		i = j
	}
	den := fn*v - s*s
	if den <= 0 {
		return s, 0
	}
	t := s * math.Sqrt((fn-1)/den)
	return s, TTwoSided(t, fn-1)
}

// ---------------------------------------------------------------- options

type uvOutSpec struct {
	Ref     *DSRef
	Stats   []uvOutVar
	PctlPts []float64
	Pre     []string
	Names   []string
}

type uvOutVar struct {
	Name string
	Var  int // index into analysis variables
	Kw   string
}

var uvOutKeywords = map[string]bool{
	"N": true, "NMISS": true, "NOBS": true, "MEAN": true, "STD": true, "STDDEV": true, "VAR": true, "SUM": true,
	"SUMWGT": true, "CV": true, "SKEWNESS": true, "KURTOSIS": true, "USS": true, "CSS": true, "STDMEAN": true,
	"RANGE": true, "QRANGE": true, "MIN": true, "MAX": true, "MEDIAN": true, "Q1": true, "Q3": true, "VARIANCE": true, "P1": true,
	"P5": true, "P10": true, "P90": true, "P95": true, "P99": true, "MODE": true, "T": true, "PROBT": true,
	"MSIGN": true, "PROBM": true, "SIGNRANK": true, "PROBS": true, "NORMAL": true, "PROBN": true,
}

// uvParseNumList parses "a b c", "a, b" and "a to b by c" lists.
func uvParseNumList(toks []Token) []float64 {
	var out []float64
	for i := 0; i < len(toks); i++ {
		t := toks[i]
		if t.Kind != TNumber && !(t.IsOp("-") && i+1 < len(toks) && toks[i+1].Kind == TNumber) {
			continue
		}
		v := t.Num
		if t.IsOp("-") {
			i++
			v = -toks[i].Num
		}
		if i+2 < len(toks) && toks[i+1].Is("TO") && toks[i+2].Kind == TNumber {
			hi := toks[i+2].Num
			step := 1.0
			i += 2
			if i+2 < len(toks) && toks[i+1].Is("BY") && toks[i+2].Kind == TNumber {
				step = toks[i+2].Num
				i += 2
			}
			for k := 0; ; k++ {
				x := v + float64(k)*step
				if (step > 0 && x > hi+1e-9) || (step < 0 && x < hi-1e-9) || step == 0 {
					break
				}
				out = append(out, math.Round(x*1e9)/1e9)
			}
			continue
		}
		out = append(out, v)
	}
	return out
}

// uvPctlSuffix names a percentile per SAS: integers as is, otherwise
// truncated to two decimals with '.' replaced by '_'.
func uvPctlSuffix(p float64) string {
	if p == math.Trunc(p) {
		return strconv.Itoa(int(p))
	}
	t := math.Trunc(p*100+1e-7) / 100
	s := strconv.FormatFloat(t, 'f', -1, 64)
	return strings.ReplaceAll(s, ".", "_")
}

func uvParseOutput(st *Stmt, nvars int) (*uvOutSpec, error) {
	o := &uvOutSpec{}
	toks := st.Args()
	isKey := func(i int) bool {
		return i+1 < len(toks) && toks[i].Kind == TIdent && toks[i+1].IsOp("=")
	}
	for i := 0; i < len(toks); {
		if !isKey(i) {
			return nil, fmt.Errorf("Syntax error in OUTPUT statement near %s.", toks[i].Text)
		}
		kw := toks[i].Upper()
		j := i + 2
		start := j
		if kw == "OUT" {
			c := NewCursor(toks[j:])
			ref, err := ParseDSRef(c)
			if err != nil {
				return nil, err
			}
			o.Ref = ref
			i = j + c.P
			continue
		}
		for j < len(toks) && !isKey(j) {
			j++
		}
		val := toks[start:j]
		i = j
		switch kw {
		case "PCTLPTS":
			o.PctlPts = uvParseNumList(val)
		case "PCTLPRE":
			o.Pre = uvNames(val)
		case "PCTLNAME", "PCTLNDEC":
			if kw == "PCTLNAME" {
				o.Names = uvNames(val)
			}
		default:
			if !uvOutKeywords[kw] {
				return nil, fmt.Errorf("Statistic keyword %s is not valid in the OUTPUT statement.", kw)
			}
			for k, nm := range uvNames(val) {
				if k < nvars {
					o.Stats = append(o.Stats, uvOutVar{Name: nm, Var: k, Kw: kw})
				}
			}
		}
	}
	if len(o.PctlPts) > 0 && len(o.Pre) == 0 {
		return nil, fmt.Errorf("The PCTLPRE= option must be specified with PCTLPTS=.")
	}
	return o, nil
}

func uvNames(toks []Token) []string {
	var out []string
	for _, t := range toks {
		if t.Kind == TIdent {
			out = append(out, t.Text)
		}
	}
	return out
}

// uvStatKw evaluates an OUTPUT statistic keyword.
func uvStatKw(kw string, s *uvSample, m uvMoments, mu0 float64, def int) float64 {
	q := func(p float64) float64 { return uvPctl(s.X, s.W, p, def) }
	switch kw {
	case "N":
		return m.N
	case "NMISS":
		return float64(s.NMiss)
	case "NOBS":
		return m.N + float64(s.NMiss)
	case "MEAN":
		return m.Mean
	case "STD", "STDDEV":
		return m.Std
	case "VAR", "VARIANCE":
		return m.Var
	case "SUM":
		return m.Sum
	case "SUMWGT":
		return m.SumW
	case "CV":
		return m.CV
	case "SKEWNESS":
		return m.Skew
	case "KURTOSIS":
		return m.Kurt
	case "USS":
		return m.USS
	case "CSS":
		return m.CSS
	case "STDMEAN":
		return m.StdMean
	case "MIN":
		return q(0)
	case "MAX":
		return q(100)
	case "RANGE":
		return q(100) - q(0)
	case "QRANGE":
		return q(75) - q(25)
	case "MEDIAN":
		return q(50)
	case "Q1":
		return q(25)
	case "Q3":
		return q(75)
	case "P1", "P5", "P10", "P90", "P95", "P99":
		p, _ := strconv.Atoi(kw[1:])
		return q(float64(p))
	case "MODE":
		if len(s.X) == 0 {
			return math.NaN()
		}
		return uvMode(s.X)
	case "T", "PROBT":
		t := (m.Mean - mu0) / m.StdMean
		if kw == "T" {
			return t
		}
		return TTwoSided(t, m.N-1)
	case "MSIGN", "PROBM":
		mm, p := uvSignTest(s.X, mu0)
		if kw == "MSIGN" {
			return mm
		}
		return p
	case "SIGNRANK", "PROBS":
		ss, p := uvSignedRank(s.X, mu0)
		if kw == "SIGNRANK" {
			return ss
		}
		return p
	case "NORMAL", "PROBN":
		tests := uvNormalityTests(s.X)
		if len(tests) == 0 {
			return math.NaN()
		}
		if kw == "NORMAL" {
			return tests[0].Stat
		}
		return tests[0].P
	}
	return math.NaN()
}

// uvMode returns the smallest most frequent value; with all values unique
// (n > 1) SAS reports a missing mode.
func uvMode(sorted []float64) float64 { return Mode(sorted) }

// ---------------------------------------------------------------- procedure

type uvProc struct {
	s        *Session
	p        *ProcStep
	d        *Dataset
	vars     []int
	mu0      []float64
	alpha    float64
	def      int
	normal   bool
	noprint  bool
	cibasic  bool
	plot     bool
	graphics bool
	wIdx     int
	fIdx     int
	obsNo    map[*Value]int
	hists    []*uvPlotReq
	qqs      []*uvPlotReq
	pps      []*uvPlotReq
}

// uvPlotReq is a HISTOGRAM / QQPLOT / PROBPLOT request.
type uvPlotReq struct {
	Vars   []int
	Normal bool
	Kernel bool
	Mu     float64 // NaN = EST
	Sigma  float64
	Color  string
	Dash   bool
}

func uvRun(s *Session, p *ProcStep) error {
	d, err := s.ProcData(p)
	if err != nil {
		return err
	}
	u := &uvProc{s: s, p: p, d: d, wIdx: -1, fIdx: -1}
	o := p.Opts
	u.alpha = o.Num("ALPHA", 0.05)
	u.def = int(o.Num("PCTLDEF", 5))
	if u.def < 1 || u.def > 5 {
		return fmt.Errorf("PCTLDEF= must be 1, 2, 3, 4, or 5.")
	}
	u.normal = o.Has("NORMAL", "NORMALTEST")
	u.noprint = o.Has("NOPRINT")
	u.cibasic = o.Has("CIBASIC")
	u.plot = o.Has("PLOT", "PLOTS")
	u.graphics = true
	if v, ok := s.Ext["odsgraphics"].(bool); ok && !v {
		u.graphics = false
	}
	used := map[int]bool{}
	byRef := []int{}
	if st := p.First("BY"); st != nil {
		b, err := ParseBy(d, st.Args())
		if err != nil {
			return err
		}
		byRef = b.Idx
	}
	var classIdx []int
	if st := p.First("CLASS"); st != nil {
		names, err := ExpandVarList(d, st.Args())
		if err != nil {
			return err
		}
		if classIdx, err = d.Indices(names); err != nil {
			return err
		}
		if len(classIdx) > 2 {
			return fmt.Errorf("No more than two CLASS variables may be specified.")
		}
	}
	single := func(kw string) (int, error) {
		st := p.First(kw)
		if st == nil {
			return -1, nil
		}
		names, err := ExpandVarList(d, st.Args())
		if err != nil || len(names) == 0 {
			return -1, err
		}
		i := d.Index(names[0])
		if d.Vars[i].Char {
			return -1, fmt.Errorf("Variable %s in list does not match type prescribed for this list.", d.Vars[i].Name)
		}
		return i, nil
	}
	if u.wIdx, err = single("WEIGHT"); err != nil {
		return err
	}
	if u.fIdx, err = single("FREQ"); err != nil {
		return err
	}
	for _, i := range append(append(append([]int{}, byRef...), classIdx...), u.wIdx, u.fIdx) {
		used[i] = true
	}
	if st := p.First("VAR"); st != nil {
		names, err := ExpandVarList(d, st.Args())
		if err != nil {
			return err
		}
		idx, err := d.Indices(names)
		if err != nil {
			return err
		}
		for _, i := range idx {
			if d.Vars[i].Char {
				return fmt.Errorf("Variable %s in list does not match type prescribed for this list.", d.Vars[i].Name)
			}
		}
		u.vars = idx
	} else {
		for i, v := range d.Vars {
			if !v.Char && !used[i] {
				u.vars = append(u.vars, i)
			}
		}
	}
	if len(u.vars) == 0 {
		return fmt.Errorf("No numeric variables to analyze.")
	}
	if op, ok := o.Get("MU0", "LOCATION"); ok {
		u.mu0 = uvParseNumList(op.Val)
	}
	for k, kw := range []string{"HISTOGRAM", "QQPLOT", "PROBPLOT"} {
		for _, st := range p.Stmts(kw) {
			req, err := u.parsePlot(st)
			if err != nil {
				return err
			}
			switch k {
			case 0:
				u.hists = append(u.hists, req)
			case 1:
				u.qqs = append(u.qqs, req)
			default:
				u.pps = append(u.pps, req)
			}
		}
	}
	var outs []*uvOutSpec
	for _, st := range p.Stmts("OUTPUT") {
		spec, err := uvParseOutput(st, len(u.vars))
		if err != nil {
			return err
		}
		outs = append(outs, spec)
	}
	by, groups, err := s.ProcBy(p, d)
	if err != nil {
		return err
	}
	u.obsNo = make(map[*Value]int, len(d.Rows))
	for i, r := range d.Rows {
		if len(r) > 0 {
			u.obsNo[&r[0]] = i + 1
		}
	}
	outDS := make([]*Dataset, len(outs))
	for k, spec := range outs {
		outDS[k] = u.newOutDS(spec, by, classIdx)
	}
	for _, g := range groups {
		if by != nil && !u.noprint {
			s.Out.SetBy(by.Vars, g.Keys)
		}
		for _, cg := range uvClassGroups(g.Rows, classIdx) {
			res := make([]*uvSample, len(u.vars))
			mom := make([]uvMoments, len(u.vars))
			for k, vi := range u.vars {
				res[k] = u.sample(cg.Rows, vi)
				mom[k] = uvComputeMoments(res[k])
				if !u.noprint {
					u.report(k, res[k], mom[k], classIdx, cg.Keys)
				}
			}
			for k, spec := range outs {
				u.outRow(outDS[k], spec, g.Keys, cg.Keys, res, mom)
			}
		}
	}
	if by != nil && !u.noprint {
		s.Out.SetBy(nil, nil)
	}
	for k, spec := range outs {
		ref := spec.Ref
		if ref == nil {
			ref = &DSRef{Name: uvDataN(s)}
		}
		outDS[k].Name = ref.Name
		if err := s.WriteDS(ref, outDS[k]); err != nil {
			return err
		}
	}
	return nil
}

func uvDataN(s *Session) string {
	for i := 1; ; i++ {
		n := fmt.Sprintf("DATA%d", i)
		if s.Dataset(n) == nil {
			return n
		}
	}
}

func (u *uvProc) parsePlot(st *Stmt) (*uvPlotReq, error) {
	before, after := SplitSlash(st.Args())
	req := &uvPlotReq{Mu: math.NaN(), Sigma: math.NaN()}
	if len(before) > 0 {
		names, err := ExpandVarList(u.d, before)
		if err != nil {
			return nil, err
		}
		for _, n := range names {
			i := u.d.Index(n)
			pos := -1
			for k, vi := range u.vars {
				if vi == i {
					pos = k
				}
			}
			if pos < 0 {
				return nil, fmt.Errorf("Variable %s in %s statement is not a VAR variable.", n, st.Keyword())
			}
			req.Vars = append(req.Vars, pos)
		}
	} else {
		for k := range u.vars {
			req.Vars = append(req.Vars, k)
		}
	}
	opts := ParseOpts(after)
	for _, op := range opts.List {
		switch strings.ToUpper(op.Key) {
		case "NORMAL":
			req.Normal = true
			sub := ParseOpts(op.Inner())
			if v := sub.Str("MU", "EST"); !strings.EqualFold(v, "EST") {
				req.Mu = sub.Num("MU", math.NaN())
			}
			if v := sub.Str("SIGMA", "EST"); !strings.EqualFold(v, "EST") {
				req.Sigma = sub.Num("SIGMA", math.NaN())
			}
			req.Color = sub.Str("COLOR", "")
			if l := sub.Num("L", 1); l > 1 {
				req.Dash = true
			}
		case "KERNEL":
			req.Kernel = true
		}
	}
	return req, nil
}

type uvClassGroup struct {
	Keys []Value
	Rows [][]Value
}

// uvClassGroups splits rows by CLASS variables (sorted levels).
func uvClassGroups(rows [][]Value, idx []int) []uvClassGroup {
	if len(idx) == 0 {
		return []uvClassGroup{{Rows: rows}}
	}
	sorted := append([][]Value(nil), rows...)
	SortRows(sorted, idx, nil)
	var out []uvClassGroup
	for i := 0; i < len(sorted); {
		j := i + 1
		for j < len(sorted) && cmpRows(sorted[i], sorted[j], idx, nil) == 0 {
			j++
		}
		keys := make([]Value, len(idx))
		for k, c := range idx {
			keys[k] = sorted[i][c]
		}
		out = append(out, uvClassGroup{Keys: keys, Rows: sorted[i:j]})
		i = j
	}
	return out
}

func (u *uvProc) sample(rows [][]Value, vi int) *uvSample {
	s := &uvSample{}
	type ent struct {
		x, w float64
		obs  int
	}
	es := make([]ent, 0, len(rows))
	for _, r := range rows {
		x := r[vi].Num
		w, f := 1.0, 1
		if u.fIdx >= 0 {
			fv := r[u.fIdx].Num
			if math.IsNaN(fv) || fv < 1 {
				continue
			}
			f = int(fv)
		}
		if u.wIdx >= 0 {
			w = r[u.wIdx].Num
			if math.IsNaN(w) || w <= 0 {
				continue
			}
		}
		if math.IsNaN(x) {
			s.NMiss += f
			continue
		}
		for range f {
			es = append(es, ent{x, w, u.obsNo[&r[0]]})
		}
	}
	sort.SliceStable(es, func(a, b int) bool {
		if es[a].x != es[b].x {
			return es[a].x < es[b].x
		}
		return es[a].obs < es[b].obs
	})
	s.X = make([]float64, len(es))
	s.Obs = make([]int, len(es))
	if u.wIdx >= 0 {
		s.W = make([]float64, len(es))
	}
	for i, e := range es {
		s.X[i], s.Obs[i] = e.x, e.obs
		if s.W != nil {
			s.W[i] = e.w
		}
	}
	return s
}

func (u *uvProc) mu0For(k int) float64 {
	if len(u.mu0) == 0 {
		return 0
	}
	if k < len(u.mu0) {
		return u.mu0[k]
	}
	return u.mu0[len(u.mu0)-1]
}

func uvCV(name string, v float64) Value {
	if math.IsNaN(v) {
		return StrV("")
	}
	return StrV(strings.TrimSpace(FormatByName(NumV(v), "BEST10.")))
}

func (u *uvProc) report(k int, smp *uvSample, m uvMoments, classIdx []int, ckeys []Value) {
	out := u.s.Out
	v := u.d.Vars[u.vars[k]]
	head := "Variable: " + v.Name
	if v.Label != "" {
		head += " (" + v.Label + ")"
	}
	out.AddText(&Text{Text: head, Class: "byline"})
	for c, ci := range classIdx {
		cv := u.d.Vars[ci]
		out.AddText(&Text{Text: cv.Name + " = " + strings.TrimSpace(FormatByName(ckeys[c], cv.Format)), Class: "byline"})
	}
	name := v.Name
	weighted := smp.W != nil

	mt := &Table{ODSName: "Moments", Caption: "Moments", RowHeader: 0,
		Cols: []Col{{Name: "VarName", Hidden: true, Char: true}, {Name: "Label1", Label: " ", Char: true}, {Name: "cValue1", Label: " ", Char: true},
			{Name: "nValue1", Hidden: true}, {Name: "Label2", Label: " ", Char: true}, {Name: "cValue2", Label: " ", Char: true}, {Name: "nValue2", Hidden: true}}}
	addM := func(l1 string, v1 float64, l2 string, v2 float64) {
		mt.AddRow(name, l1, uvCV(name, v1), v1, l2, uvCV(name, v2), v2)
	}
	addM("N", m.N, "Sum Weights", m.SumW)
	addM("Mean", m.Mean, "Sum Observations", m.Sum)
	addM("Std Deviation", m.Std, "Variance", m.Var)
	addM("Skewness", m.Skew, "Kurtosis", m.Kurt)
	addM("Uncorrected SS", m.USS, "Corrected SS", m.CSS)
	addM("Coeff Variation", m.CV, "Std Error Mean", m.StdMean)
	if weighted {
		mt.Caption = "Weighted Moments"
	}
	out.AddTable(mt)

	q := func(p float64) float64 { return uvPctl(smp.X, smp.W, p, u.def) }
	mode := math.NaN()
	nModes, modeCount := 0, 0
	if len(smp.X) > 0 {
		mode = uvMode(smp.X)
		for i := 0; i < len(smp.X); {
			j := i
			for j < len(smp.X) && smp.X[j] == smp.X[i] {
				j++
			}
			switch c := j - i; {
			case c > modeCount:
				modeCount, nModes = c, 1
			case c == modeCount:
				nModes++
			}
			i = j
		}
	}
	bm := &Table{ODSName: "BasicMeasures", Caption: "Basic Statistical Measures",
		Cols: []Col{{Name: "VarName", Hidden: true, Char: true}, {Name: "LocMeasure", Label: "Location", Char: true}, {Name: "LocValue", Label: " ", Format: "BEST9."},
			{Name: "VarMeasure", Label: "Variability", Char: true}, {Name: "VarValue", Label: " ", Format: "BEST9."}},
		Spans: []Span{{"Location", 0, 1}, {"Variability", 2, 3}}}
	if weighted {
		bm.Caption = "Weighted Basic Statistical Measures"
	}
	bm.AddRow(name, "Mean", m.Mean, "Std Deviation", m.Std)
	bm.AddRow(name, "Median", q(50), "Variance", m.Var)
	bm.AddRow(name, "Mode", mode, "Range", q(100)-q(0))
	bm.AddRow(name, "", math.NaN(), "Interquartile Range", q(75)-q(25))
	if nModes > 1 && modeCount > 1 {
		bm.Notes = append(bm.Notes, fmt.Sprintf("Note: The mode displayed is the smallest of %d modes with a count of %d.", nModes, modeCount))
	}
	out.AddTable(bm)

	if u.cibasic && len(smp.X) > 1 {
		n := m.N
		tc := TInv(1-u.alpha/2, n-1)
		lo := ChiSqInv(1-u.alpha/2, n-1)
		hi := ChiSqInv(u.alpha/2, n-1)
		ci := &Table{ODSName: "BasicIntervals", Caption: fmt.Sprintf("Basic Confidence Limits Assuming Normality"),
			Cols: []Col{{Name: "VarName", Hidden: true, Char: true}, {Name: "Parameter", Label: "Parameter", Char: true}, {Name: "Estimate", Label: "Estimate", Format: "BEST9."},
				{Name: "LowerCL", Label: fmt.Sprintf("%g%% Confidence Limits", 100*(1-u.alpha)), Format: "BEST9."}, {Name: "UpperCL", Label: " ", Format: "BEST9."}}}
		ci.AddRow(name, "Mean", m.Mean, m.Mean-tc*m.StdMean, m.Mean+tc*m.StdMean)
		ci.AddRow(name, "Std Deviation", m.Std, math.Sqrt(m.CSS/lo), math.Sqrt(m.CSS/hi))
		ci.AddRow(name, "Variance", m.Var, m.CSS/lo, m.CSS/hi)
		out.AddTable(ci)
	}

	mu0 := u.mu0For(k)
	loc := &Table{ODSName: "TestsForLocation", Caption: "Tests for Location: Mu0=" + FormatTick(mu0),
		Cols: []Col{{Name: "VarName", Hidden: true, Char: true}, {Name: "Test", Label: "Test", Char: true}, {Name: "TestLab", Label: "Statistic", Char: true},
			{Name: "Stat", Label: " ", Format: "BEST8."}, {Name: "pType", Label: "p Value", Char: true}, {Name: "pSign", Label: " ", Char: true}, {Name: "pValue", Label: " ", Format: "PVALUE6.4"}},
		Spans: []Span{{"Statistic", 1, 2}, {"p Value", 3, 5}}}
	t := (m.Mean - mu0) / m.StdMean
	loc.AddRow(name, "Student's t", "t", t, "Pr > |t|", "", TTwoSided(t, m.N-1))
	if !weighted {
		ms, mp := uvSignTest(smp.X, mu0)
		loc.AddRow(name, "Sign", "M", ms, "Pr >= |M|", "", mp)
		ss, sp := uvSignedRank(smp.X, mu0)
		loc.AddRow(name, "Signed Rank", "S", ss, "Pr >= |S|", "", sp)
	}
	out.AddTable(loc)

	if u.normal && len(smp.X) > 0 {
		out.AddTable(uvNormTable("TestsForNormality", "Tests for Normality", name, uvNormalityTests(smp.X)))
	}

	qt := &Table{ODSName: "Quantiles", Caption: fmt.Sprintf("Quantiles (Definition %d)", u.def),
		Cols: []Col{{Name: "VarName", Hidden: true, Char: true}, {Name: "Quantile", Label: "Level", Char: true}, {Name: "Estimate", Label: "Quantile", Format: "BEST9."}}}
	if weighted {
		qt.Caption = "Weighted Quantiles"
	}
	for _, lv := range []struct {
		l string
		p float64
	}{{"100% Max", 100}, {"99%", 99}, {"95%", 95}, {"90%", 90}, {"75% Q3", 75}, {"50% Median", 50}, {"25% Q1", 25}, {"10%", 10}, {"5%", 5}, {"1%", 1}, {"0% Min", 0}} {
		qt.AddRow(name, lv.l, q(lv.p))
	}
	out.AddTable(qt)

	ex := &Table{ODSName: "ExtremeObs", Caption: "Extreme Observations",
		Cols: []Col{{Name: "VarName", Hidden: true, Char: true}, {Name: "Low", Label: "Value", Format: "BEST9."}, {Name: "LowObs", Label: "Obs"},
			{Name: "High", Label: "Value", Format: "BEST9."}, {Name: "HighObs", Label: "Obs"}},
		Spans: []Span{{"Lowest", 0, 1}, {"Highest", 2, 3}}}
	n := len(smp.X)
	for i := range min(5, n) {
		h := n - min(5, n) + i
		ex.AddRow(name, smp.X[i], smp.Obs[i], smp.X[h], smp.Obs[h])
	}
	out.AddTable(ex)

	if smp.NMiss > 0 {
		tot := float64(smp.NMiss + n)
		mv := &Table{ODSName: "MissingValues", Caption: "Missing Values",
			Cols: []Col{{Name: "VarName", Hidden: true, Char: true}, {Name: "MissValue", Label: "Missing Value", Char: true}, {Name: "Count", Label: "Count"},
				{Name: "PctAll", Label: "Percent Of All Obs", Format: "8.2"}, {Name: "PctMiss", Label: "Percent Of Missing Obs", Format: "8.2"}}}
		mv.AddRow(name, ".", smp.NMiss, 100*float64(smp.NMiss)/tot, 100.0)
		out.AddTable(mv)
	}

	u.plots(k, smp, m)
}

// uvNormTable builds a normality/goodness-of-fit table.
func uvNormTable(ods, caption, name string, tests []uvNormTest) *Table {
	t := &Table{ODSName: ods, Caption: caption,
		Cols: []Col{{Name: "VarName", Hidden: true, Char: true}, {Name: "Test", Label: "Test", Char: true}, {Name: "TestLab", Label: "Statistic", Char: true},
			{Name: "Stat", Label: " ", Format: "BEST9."}, {Name: "pType", Label: "p Value", Char: true}, {Name: "pSign", Label: " ", Char: true}, {Name: "pValue", Label: " ", Format: "PVALUE6.4"}},
		Spans: []Span{{"Statistic", 1, 2}, {"p Value", 3, 5}}}
	for _, r := range tests {
		t.AddRow(name, r.Test, r.Lab, r.Stat, r.PType, r.Sign, r.P)
	}
	return t
}

func (u *uvProc) plots(k int, smp *uvSample, m uvMoments) {
	v := u.d.Vars[u.vars[k]]
	label := v.DisplayLabel()
	x := smp.X
	if len(x) == 0 {
		return
	}
	if u.plot && u.graphics {
		h := uvHistChart(x, "Distribution of "+label, label, false, 0, 0, false)
		h.Height = 300
		b := uvBoxChart(x, "", label)
		qq := uvQQChart(x, "Normal Probability Plot for "+label, label, m.Mean, m.Std, "", false)
		u.s.Out.AddGraph(&Graph{ODSName: "Plots", Caption: "Plots for " + label, SVG: h.SVG() + b.SVG() + qq.SVG()})
	}
	for _, r := range u.hists {
		if !uvHas(r.Vars, k) {
			continue
		}
		mu, sg := r.Mu, r.Sigma
		if math.IsNaN(mu) {
			mu = m.Mean
		}
		if math.IsNaN(sg) {
			sg = m.Std
		}
		if u.graphics {
			c := uvHistChart(x, "Distribution of "+label, label, r.Normal, mu, sg, r.Kernel)
			u.s.Out.AddGraph(&Graph{ODSName: "Histogram", Caption: "Histogram for " + label, SVG: c.SVG()})
		}
		if r.Normal && len(x) > 1 && sg > 0 {
			u.fitTables(v.Name, smp, mu, sg, math.IsNaN(r.Mu) && math.IsNaN(r.Sigma), r)
		}
	}
	for _, r := range u.qqs {
		if !uvHas(r.Vars, k) || !u.graphics {
			continue
		}
		mu, sg := r.Mu, r.Sigma
		if math.IsNaN(mu) {
			mu = m.Mean
		}
		if math.IsNaN(sg) {
			sg = m.Std
		}
		c := uvQQChart(x, "Q-Q Plot for "+label, label, mu, sg, r.Color, r.Dash)
		if !r.Normal {
			c.Layers = c.Layers[:1]
		}
		u.s.Out.AddGraph(&Graph{ODSName: "QQPlot", Caption: "Q-Q Plot for " + label, SVG: c.SVG()})
	}
	for _, r := range u.pps {
		if !uvHas(r.Vars, k) || !u.graphics {
			continue
		}
		mu, sg := r.Mu, r.Sigma
		if math.IsNaN(mu) {
			mu = m.Mean
		}
		if math.IsNaN(sg) {
			sg = m.Std
		}
		c := uvQQChart(x, "Probability Plot for "+label, label, mu, sg, r.Color, r.Dash)
		c.XLabel = "Normal Percentiles"
		c.XTickFmt = func(z float64) string { return FormatTick(math.Round(NormCDF(z)*1000) / 10) }
		if !r.Normal {
			c.Layers = c.Layers[:1]
		}
		u.s.Out.AddGraph(&Graph{ODSName: "ProbPlot", Caption: "Probability Plot for " + label, SVG: c.SVG()})
	}
}

func uvHas(list []int, k int) bool {
	for _, v := range list {
		if v == k {
			return true
		}
	}
	return false
}

// fitTables prints the HISTOGRAM / NORMAL fitted-distribution tables.
func (u *uvProc) fitTables(name string, smp *uvSample, mu, sg float64, est bool, r *uvPlotReq) {
	out := u.s.Out
	pe := &Table{ODSName: "ParameterEstimates", Caption: "Parameters for Normal Distribution",
		Cols: []Col{{Name: "VarName", Hidden: true, Char: true}, {Name: "Parameter", Label: "Parameter", Char: true}, {Name: "Symbol", Label: "Symbol", Char: true}, {Name: "Estimate", Label: "Estimate", Format: "BEST9."}}}
	pe.AddRow(name, "Mean", "Mu", mu)
	pe.AddRow(name, "Std Dev", "Sigma", sg)
	out.AddTable(pe)
	var tests []uvNormTest
	if est {
		tests = uvGoodnessOfFit(smp.X, mu, sg)
	} else {
		tests = uvGoodnessOfFitKnown(smp.X, mu, sg)
	}
	out.AddTable(uvNormTable("GoodnessOfFit", "Goodness-of-Fit Tests for Normal Distribution", name, tests))
	fq := &Table{ODSName: "FitQuantiles", Caption: "Quantiles for Normal Distribution",
		Cols:  []Col{{Name: "VarName", Hidden: true, Char: true}, {Name: "Percent", Label: "Percent", Format: "8.1"}, {Name: "ObsQuant", Label: "Observed", Format: "BEST9."}, {Name: "EstQuant", Label: "Estimated", Format: "BEST9."}},
		Spans: []Span{{"Quantile", 1, 2}}}
	for _, pc := range []float64{1, 5, 10, 25, 50, 75, 90, 95, 99} {
		fq.AddRow(name, pc, uvPctl(smp.X, smp.W, pc, u.def), mu+sg*NormInv(pc/100))
	}
	out.AddTable(fq)
}

// uvGoodnessOfFitKnown uses Stephens' case 0 (fully specified) tables.
func uvGoodnessOfFitKnown(y []float64, mu, sigma float64) []uvNormTest {
	n := float64(len(y))
	d, w2, a2 := uvEDF(y, mu, sigma)
	sn := math.Sqrt(n)
	al := []float64{0.25, 0.15, 0.10, 0.05, 0.025, 0.01}
	pd, sd := uvTableP(d*(sn+0.12+0.11/sn), []float64{1.019, 1.138, 1.224, 1.358, 1.480, 1.628}, al)
	pw, sw := uvTableP((w2-0.4/n+0.6/(n*n))*(1+1/n), []float64{0.209, 0.284, 0.347, 0.461, 0.581, 0.743}, al)
	pa, sa := uvTableP(a2, []float64{1.248, 1.610, 1.933, 2.492, 3.070, 3.880}, al)
	return []uvNormTest{
		{"Kolmogorov-Smirnov", "D", "Pr > D", d, pd, sd},
		{"Cramer-von Mises", "W-Sq", "Pr > W-Sq", w2, pw, sw},
		{"Anderson-Darling", "A-Sq", "Pr > A-Sq", a2, pa, sa},
	}
}

// ---------------------------------------------------------------- OUTPUT data set

func (u *uvProc) newOutDS(spec *uvOutSpec, by *ByInfo, classIdx []int) *Dataset {
	ds := NewDataset("")
	if by != nil {
		for _, v := range by.Vars {
			ds.AddVar(v.Clone())
		}
	}
	for _, ci := range classIdx {
		ds.AddVar(u.d.Vars[ci].Clone())
	}
	for _, sv := range spec.Stats {
		ds.AddVar(&Var{Name: sv.Name, Length: 8})
	}
	for _, nm := range uvPctlVarNames(spec) {
		ds.AddVar(&Var{Name: nm, Length: 8})
	}
	return ds
}

func uvPctlVarNames(spec *uvOutSpec) []string {
	var out []string
	for pi, pt := range spec.PctlPts {
		suffix := uvPctlSuffix(pt)
		if pi < len(spec.Names) {
			suffix = spec.Names[pi]
		}
		for _, pre := range spec.Pre {
			nm := pre + suffix
			if len(nm) > 32 {
				nm = pre[:32-len(suffix)] + suffix
			}
			out = append(out, nm)
		}
	}
	return out
}

func (u *uvProc) outRow(ds *Dataset, spec *uvOutSpec, byKeys, classKeys []Value, smp []*uvSample, mom []uvMoments) {
	row := make([]Value, 0, len(ds.Vars))
	if len(ds.Vars) > 0 {
		row = append(row, byKeys...)
		row = append(row, classKeys...)
	}
	for _, sv := range spec.Stats {
		row = append(row, NumV(uvStatKw(sv.Kw, smp[sv.Var], mom[sv.Var], u.mu0For(sv.Var), u.def)))
	}
	for _, pt := range spec.PctlPts {
		for k := range spec.Pre {
			if k < len(smp) {
				row = append(row, NumV(uvPctl(smp[k].X, smp[k].W, pt, u.def)))
			}
		}
	}
	for len(row) < len(ds.Vars) {
		row = append(row, Missing)
	}
	ds.Rows = append(ds.Rows, row[:len(ds.Vars)])
}

// ---------------------------------------------------------------- charts

// uvHistBins returns bin edges for a histogram of sorted data.
func uvHistBins(x []float64) []float64 {
	lo, hi := x[0], x[len(x)-1]
	if lo == hi {
		return []float64{lo - 0.5, lo + 0.5}
	}
	nb := int(math.Ceil(math.Log2(float64(len(x))) + 1))
	nb = max(nb, 5)
	ticks := NiceTicks(lo, hi, nb)
	if len(ticks) < 2 {
		return []float64{lo, hi}
	}
	w := ticks[1] - ticks[0]
	start := math.Floor(lo/w) * w
	var edges []float64
	for e := start; e < hi+w*1e-9 || len(edges) < 2; e += w {
		edges = append(edges, cleanFloat(e, w))
		if e > hi {
			break
		}
	}
	if edges[len(edges)-1] <= hi {
		edges = append(edges, cleanFloat(edges[len(edges)-1]+w, w))
	}
	return edges
}

// uvKernelBW is SAS's default normal-kernel bandwidth (c = 0.7852 times the
// interquartile range times n^(-1/5), falling back to the standard deviation).
func uvKernelBW(x []float64) float64 {
	q := Percentile(x, 75) - Percentile(x, 25)
	s := StdDev(x)
	if q <= 0 {
		q = 1.349 * s
	}
	return 0.7852 * q * math.Pow(float64(len(x)), -0.2)
}

// uvHistChart draws a percent histogram with optional normal/kernel curves.
func uvHistChart(x []float64, title, xlabel string, normal bool, mu, sigma float64, kernel bool) *Chart {
	c := NewChart(title)
	c.XLabel, c.YLabel = xlabel, "Percent"
	edges := uvHistBins(x)
	nb := len(edges) - 1
	cnt := make([]float64, nb)
	for _, v := range x {
		i := sort.SearchFloat64s(edges, v)
		if i > 0 && (i >= len(edges) || edges[i] != v) {
			i--
		}
		i = min(max(i, 0), nb-1)
		cnt[i]++
	}
	b := &Bars{Color: PaletteColor(0), Opacity: 0.6, Outline: "#333333"}
	n := float64(len(x))
	for i := range nb {
		b.X0 = append(b.X0, edges[i])
		b.X1 = append(b.X1, edges[i+1])
		b.Y0 = append(b.Y0, 0)
		b.Y1 = append(b.Y1, 100*cnt[i]/n)
	}
	c.Layers = append(c.Layers, b)
	c.YMin = 0
	w := edges[1] - edges[0]
	lo, hi := edges[0], edges[nb]
	grid := func(f func(float64) float64) ([]float64, []float64) {
		var gx, gy []float64
		for i := 0; i <= 200; i++ {
			t := lo + (hi-lo)*float64(i)/200
			gx = append(gx, t)
			gy = append(gy, 100*w*f(t))
		}
		return gx, gy
	}
	if normal && sigma > 0 {
		gx, gy := grid(func(t float64) float64 { return NormPDF((t-mu)/sigma) / sigma })
		c.Layers = append(c.Layers, &Line{X: gx, Y: gy, Color: PaletteColor(1), Width: 2})
		c.Legend = append(c.Legend, LegendEntry{Label: "Normal", Color: PaletteColor(1), Kind: "line"})
	}
	if kernel && len(x) > 1 {
		h := uvKernelBW(x)
		if h > 0 {
			gx, gy := grid(func(t float64) float64 {
				s := 0.0
				for _, v := range x {
					s += NormPDF((t - v) / h)
				}
				return s / (n * h)
			})
			c.Layers = append(c.Layers, &Line{X: gx, Y: gy, Color: PaletteColor(2), Width: 2, Dash: true})
			c.Legend = append(c.Legend, LegendEntry{Label: "Kernel", Color: PaletteColor(2), Kind: "line", Dash: true})
		}
	}
	return c
}

// uvBoxStat computes box plot statistics (whiskers at 1.5 IQR, SAS schematic).
func uvBoxStat(x []float64, pos float64) BoxStat {
	q1, med, q3 := Percentile(x, 25), Percentile(x, 50), Percentile(x, 75)
	iqr := q3 - q1
	b := BoxStat{Pos: pos, Q1: q1, Median: med, Q3: q3, Mean: Mean(x), WhiskLo: q1, WhiskHi: q3}
	for _, v := range x {
		if v < q1-1.5*iqr || v > q3+1.5*iqr {
			b.Outliers = append(b.Outliers, v)
			continue
		}
		b.WhiskLo = math.Min(b.WhiskLo, v)
		b.WhiskHi = math.Max(b.WhiskHi, v)
	}
	return b
}

func uvBoxChart(x []float64, title, xlabel string) *Chart {
	c := NewChart(title)
	c.Height = 160
	c.XLabel = xlabel
	b := uvBoxStat(x, 0)
	b.Horizontal = true
	c.Layers = append(c.Layers, &Boxes{Boxes: []BoxStat{b}})
	c.YCats = []string{""}
	return c
}

// uvQQChart plots ordered data against normal quantiles of (i-0.375)/(n+0.25)
// with the reference line mu + sigma*q.
func uvQQChart(x []float64, title, ylabel string, mu, sigma float64, color string, dash bool) *Chart {
	c := NewChart(title)
	c.XLabel, c.YLabel = "Normal Quantiles", ylabel
	n := float64(len(x))
	qs := make([]float64, len(x))
	for i := range x {
		qs[i] = NormInv((float64(i+1) - 0.375) / (n + 0.25))
	}
	c.Layers = append(c.Layers, &Scatter{X: qs, Y: x, Marker: "circle"})
	if !math.IsNaN(mu) && !math.IsNaN(sigma) && len(qs) > 0 {
		if color == "" {
			color = PaletteColor(1)
		}
		a, b := qs[0], qs[len(qs)-1]
		c.Layers = append(c.Layers, &Line{X: []float64{a, b}, Y: []float64{mu + sigma*a, mu + sigma*b}, Color: color, Width: 2, Dash: dash})
		c.Inset = []string{"Normal Line:", fmt.Sprintf("Mu=%s, Sigma=%s", FormatTick(math.Round(mu*1e4)/1e4), FormatTick(math.Round(sigma*1e4)/1e4))}
	}
	return c
}
