package sas

import (
	"fmt"
	"html"
	"math"
	"sort"
	"strconv"
	"strings"
)

// ---------------------------------------------------------------- colors

// sgSASColors maps SAS/GRAPH color abbreviations that are not CSS names.
var sgSASColors = map[string]string{
	"LIGB": "#7EA4D8", "VLIGB": "#BBD0EE", "BIB": "#3366CC", "DABL": "#000080", "VDAB": "#00004D",
	"LIGR": "#C0C0C0", "VLIGR": "#E0E0E0", "DAGR": "#404040", "MEGR": "#808080", "GRAY": "#808080",
	"GREY": "#808080", "LIG": "#80FF80", "DAG": "#006400", "LIR": "#FF8080", "DAR": "#8B0000",
	"VIBG": "#00A0A0", "STEEL": "#4682B4", "BGR": "#00B0B0",
}

// sgColor converts a SAS color specification (name, CXrrggbb, abbreviation) to CSS.
func sgColor(s string) string {
	s = strings.Trim(strings.TrimSpace(s), `'"`)
	if s == "" {
		return ""
	}
	u := strings.ToUpper(s)
	if strings.HasPrefix(u, "CX") && len(u) == 8 {
		return "#" + u[2:]
	}
	if strings.HasPrefix(u, "#") {
		return s
	}
	if c, ok := sgSASColors[u]; ok {
		return c
	}
	return strings.ToLower(s)
}

// sgFillDefault is the HTMLBlue GraphDataDefault fill used by histograms, bars and boxes.
const sgFillDefault = "#CAD5E5"

// sgDashed reports whether a SAS line pattern is not solid.
func sgDashed(p string) bool {
	p = strings.ToUpper(strings.Trim(p, `'"`))
	return p != "" && p != "SOLID" && p != "1"
}

// ---------------------------------------------------------------- statement parsing

// sgStmt is a parsed plot statement: positional tokens, key=value options
// before the slash (Pre) and options after it (Opt).
type sgStmt struct {
	Kind string
	Line int
	Pos  []Token
	Pre  *Opts
	Opt  *Opts
}

func sgParseStmt(st *Stmt) *sgStmt {
	before, after := SplitSlash(st.Args())
	i := 0
	for i < len(before) && !(before[i].Kind == TIdent && i+1 < len(before) && before[i+1].IsOp("=")) {
		i++
	}
	kw := st.Keyword()
	if kw == "XAXIS" || kw == "YAXIS" || kw == "X2AXIS" || kw == "Y2AXIS" || kw == "KEYLEGEND" && i > 0 && before[0].Kind == TIdent {
		i = 0
	}
	return &sgStmt{Kind: kw, Line: st.Line, Pos: before[:i], Pre: ParseOpts(before[i:]), Opt: ParseOpts(after)}
}

// sgSynth builds a statement programmatically (used by SGSCATTER and PLOT).
func sgSynth(kind string, pos []string, kv ...string) *sgStmt {
	p := &sgStmt{Kind: kind, Pre: &Opts{}, Opt: &Opts{}}
	for _, n := range pos {
		p.Pos = append(p.Pos, Token{Kind: TIdent, Text: n})
	}
	for i := 0; i+1 < len(kv); i += 2 {
		if kv[i+1] == "" {
			p.Opt.List = append(p.Opt.List, Opt{Key: strings.ToUpper(kv[i])})
			continue
		}
		p.Opt.List = append(p.Opt.List, Opt{Key: strings.ToUpper(kv[i]), Eq: true, Val: []Token{{Kind: TIdent, Text: kv[i+1]}}})
	}
	return p
}

func (p *sgStmt) get(keys ...string) (Opt, bool) {
	if op, ok := p.Opt.Get(keys...); ok {
		return op, true
	}
	return p.Pre.Get(keys...)
}

func (p *sgStmt) has(keys ...string) bool { _, ok := p.get(keys...); return ok }

func (p *sgStmt) str(keys ...string) string {
	if op, ok := p.get(keys...); ok && op.Eq {
		return op.Str()
	}
	return ""
}

func (p *sgStmt) num(key string) float64 {
	if v := p.Opt.Num(key, math.NaN()); !math.IsNaN(v) {
		return v
	}
	return p.Pre.Num(key, math.NaN())
}

// sub parses a parenthesized attribute option such as MARKERATTRS=(SYMBOL=x COLOR=y).
func (p *sgStmt) sub(keys ...string) *Opts {
	op, ok := p.get(keys...)
	if !ok || !op.Eq {
		return &Opts{}
	}
	return ParseOpts(op.Inner())
}

func sgOptNum(o *Opts, key string) float64 { return o.Num(key, math.NaN()) }

// sgVarRef resolves a variable name.
func sgVarRef(d *Dataset, name string) (int, *Var, error) {
	i := d.Index(name)
	if i < 0 {
		return -1, nil, fmt.Errorf("Variable %s not found.", strings.ToUpper(name))
	}
	return i, d.Vars[i], nil
}

// sgValueList parses VALUES=(a TO b BY c, list, 'strings').
func sgValueList(toks []Token) ([]float64, []string) {
	var nums []float64
	var strs []string
	c := NewCursor(toks)
	readNum := func() (float64, bool) {
		neg := false
		if c.Peek().IsOp("-") {
			c.Next()
			neg = true
		} else if c.Peek().IsOp("+") {
			c.Next()
		}
		t := c.Peek()
		if t.Kind != TNumber {
			return 0, false
		}
		c.Next()
		if neg {
			return -t.Num, true
		}
		return t.Num, true
	}
	for !c.AtEnd() {
		t := c.Peek()
		if t.Kind == TString {
			c.Next()
			strs = append(strs, t.Text)
			continue
		}
		v, ok := readNum()
		if !ok {
			c.Next()
			continue
		}
		if c.Peek().Is("TO") {
			c.Next()
			hi, _ := readNum()
			by := 1.0
			if c.Peek().Is("BY") {
				c.Next()
				by, _ = readNum()
			}
			if by == 0 || (hi-v)/by < 0 || (hi-v)/by > 1e5 {
				nums = append(nums, v)
				continue
			}
			for k := 0; ; k++ {
				x := v + float64(k)*by
				if (by > 0 && x > hi+math.Abs(by)*1e-9) || (by < 0 && x < hi-math.Abs(by)*1e-9) {
					break
				}
				nums = append(nums, cleanFloat(x, math.Abs(by)/1000))
			}
			continue
		}
		nums = append(nums, v)
	}
	return nums, strs
}

// ---------------------------------------------------------------- axes

type sgAxis struct {
	Label     string
	HasLabel  bool
	Min, Max  float64
	Values    []float64
	ValueStrs []string
	Grid      bool
	Log       bool
	Discrete  bool
	NoLabel   bool
}

func sgParseAxis(p *sgStmt) *sgAxis {
	a := &sgAxis{Min: math.NaN(), Max: math.NaN()}
	if p == nil {
		return a
	}
	if op, ok := p.get("LABEL"); ok && op.Eq {
		a.Label, a.HasLabel = op.Str(), true
	}
	a.Min, a.Max = p.num("MIN"), p.num("MAX")
	if op, ok := p.get("VALUES"); ok && op.Eq {
		a.Values, a.ValueStrs = sgValueList(op.Inner())
	}
	a.Grid = p.has("GRID")
	switch strings.ToUpper(p.str("TYPE")) {
	case "LOG":
		a.Log = true
	case "DISCRETE":
		a.Discrete = true
	}
	if strings.EqualFold(p.str("DISPLAY"), "NONE") {
		a.NoLabel = true
	}
	if op, ok := p.get("DISPLAY"); ok {
		for _, t := range op.Inner() {
			if t.Is("NOLABEL") {
				a.NoLabel = true
			}
		}
	}
	return a
}

// ---------------------------------------------------------------- histogram binning

// sgBinning describes equal-width histogram bins: bin k is [Start+k*W, Start+(k+1)*W).
type sgBinning struct {
	Start, W float64
	N        int
}

// sgNiceFloor rounds x down to the nearest "nice" bin width {1, 2, 2.5, 4, 5} x 10^k.
func sgNiceFloor(x float64) float64 {
	if !(x > 0) || math.IsInf(x, 0) {
		return 1
	}
	e := math.Floor(math.Log10(x))
	p := math.Pow(10, e)
	f := x / p
	nice := 1.0
	for _, c := range []float64{1, 2, 2.5, 4, 5, 10} {
		if c <= f*(1+1e-9) {
			nice = c
		}
	}
	return nice * p
}

// sgBinCount returns the number of bins of width w starting at left needed so
// that max falls inside a bin (BOUNDARY=UPPER: a value on an edge goes up).
func sgBinCount(left, w, max float64) int {
	return int(math.Floor((max-left)/w+1e-9)) + 1
}

// sgDefaultBins implements the default SGPLOT/GTL histogram binning:
// the Terrell–Scott (1985) oversmoothed number of bins ceil((2n)^(1/3)) sets a
// raw width range/k, which is rounded down to a nice width; bins sit on the
// w/2 lattice, choosing between edge-aligned and midpoint-aligned placement
// the one with fewer bins, then the one most centered on the data range.
func sgDefaultBins(sorted []float64, width float64) sgBinning {
	n := len(sorted)
	if n == 0 {
		return sgBinning{Start: 0, W: 1, N: 1}
	}
	lo, hi := sorted[0], sorted[n-1]
	w := width
	if !(w > 0) {
		if hi == lo {
			w = sgNiceFloor(math.Max(math.Abs(lo), 1) * 0.1)
		} else {
			k := math.Ceil(math.Cbrt(2 * float64(n)))
			w = sgNiceFloor((hi - lo) / k)
		}
	}
	edge := math.Floor(lo/w+1e-9) * w
	mid := (math.Floor(lo/w-0.5+1e-9) + 0.5) * w
	best := sgBinning{}
	bestImb := 0.0
	for i, left := range []float64{edge, mid} {
		nb := sgBinCount(left, w, hi)
		imb := math.Abs((lo - left) - (left + float64(nb)*w - hi))
		if i == 0 || nb < best.N || nb == best.N && imb < bestImb-1e-9*w {
			best, bestImb = sgBinning{Start: left, W: w, N: nb}, imb
		}
	}
	return best
}

// sgBins honors BINWIDTH=/NBINS=/BINSTART= (BINSTART is the midpoint of the first bin).
func sgBins(sorted []float64, nbins, width, start float64) sgBinning {
	n := len(sorted)
	if n == 0 {
		return sgBinning{Start: 0, W: 1, N: 1}
	}
	lo, hi := sorted[0], sorted[n-1]
	if nbins >= 1 {
		k := math.Floor(nbins)
		w := (hi - lo) / k
		if w <= 0 {
			w = 1
		}
		left := lo
		if !math.IsNaN(start) && start-w/2 <= lo {
			left = start - w/2
		}
		return sgBinning{Start: left, W: w, N: max(int(k), int(math.Ceil((hi-left)/w-1e-9)))}
	}
	if !math.IsNaN(start) && width > 0 && start-width/2 <= lo+1e-12 {
		left := start - width/2
		return sgBinning{Start: left, W: width, N: sgBinCount(left, width, hi)}
	}
	return sgDefaultBins(sorted, width)
}

// Counts returns bin counts (values outside the bins are clamped to the end bins).
func (b sgBinning) Counts(x []float64, wt []float64) []float64 {
	c := make([]float64, b.N)
	for i, v := range x {
		if math.IsNaN(v) {
			continue
		}
		k := int(math.Floor((v-b.Start)/b.W + 1e-9))
		k = max(0, min(b.N-1, k))
		f := 1.0
		if wt != nil {
			f = wt[i]
		}
		c[k] += f
	}
	return c
}

// ---------------------------------------------------------------- densities

// sgKernelC is the AMISE-optimal standardized bandwidth for a normal kernel
// under a normal reference with sigma = IQR/1.34898: (4/3)^(1/5)/(2*z(.75)) = 0.7852.
var sgKernelC = math.Pow(4.0/3.0, 0.2) / (2 * NormInv(0.75))

// sgKernelBandwidth returns lambda = c * Q * n^(-1/5) (Q = interquartile range,
// PCTLDEF=5), as used by PROC UNIVARIATE and the SGPLOT DENSITY statement.
func sgKernelBandwidth(sorted []float64, c float64) float64 {
	if !(c > 0) {
		c = sgKernelC
	}
	q := Percentile(sorted, 75) - Percentile(sorted, 25)
	if q <= 0 {
		q = StdDev(sorted) * 2 * NormInv(0.75)
	}
	if q <= 0 {
		q = 1
	}
	return c * q * math.Pow(float64(len(sorted)), -0.2)
}

// sgKernelDensity evaluates a kernel density estimate at x.
func sgKernelDensity(sorted []float64, h float64, weight string, x float64) float64 {
	s := 0.0
	n := float64(len(sorted))
	switch weight {
	case "QUADRATIC":
		for _, v := range sorted {
			t := (x - v) / h
			if math.Abs(t) <= 1 {
				s += 0.75 * (1 - t*t)
			}
		}
	case "TRIANGULAR":
		for _, v := range sorted {
			t := math.Abs(x-v) / h
			if t <= 1 {
				s += 1 - t
			}
		}
	default:
		for _, v := range sorted {
			t := (x - v) / h
			if math.Abs(t) < 40 {
				s += NormPDF(t)
			}
		}
	}
	return s / (n * h)
}

// ---------------------------------------------------------------- box statistics

// sgBoxStats computes SGPLOT box statistics: quartiles with PCTLDEF=5,
// whiskers to the most extreme observations within 1.5 IQR of the box,
// observations beyond the fences are outliers.
func sgBoxStats(vals []float64) BoxStat {
	s := Sorted(vals)
	b := BoxStat{Q1: math.NaN(), Median: math.NaN(), Q3: math.NaN(), Mean: math.NaN(), WhiskLo: math.NaN(), WhiskHi: math.NaN()}
	if len(s) == 0 {
		return b
	}
	b.Q1, b.Median, b.Q3 = Percentile(s, 25), Percentile(s, 50), Percentile(s, 75)
	b.Mean = Mean(s)
	iqr := b.Q3 - b.Q1
	loF, hiF := b.Q1-1.5*iqr, b.Q3+1.5*iqr
	b.WhiskLo, b.WhiskHi = b.Q1, b.Q3
	for _, v := range s {
		if v >= loF {
			b.WhiskLo = math.Min(v, b.Q1)
			break
		}
	}
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] <= hiF {
			b.WhiskHi = math.Max(s[i], b.Q3)
			break
		}
	}
	for _, v := range s {
		if v < loF || v > hiF {
			b.Outliers = append(b.Outliers, v)
		}
	}
	return b
}

// ---------------------------------------------------------------- extra layers

// sgLineParm draws y = Y0 + Slope*(x - X0) across the whole plot area.
type sgLineParm struct {
	X0, Y0, Slope float64
	Color         string
	Dash          bool
}

func (l *sgLineParm) bounds() (float64, float64, float64, float64) {
	n := math.NaN()
	return n, n, n, n
}

func (l *sgLineParm) draw(cv *canvas) {
	color := l.Color
	if color == "" {
		color = Palette[0]
	}
	dash := ""
	if l.Dash {
		dash = ` stroke-dasharray="6,4"`
	}
	if math.IsInf(l.Slope, 0) {
		x := cv.sx(l.X0)
		cv.printf(`<line x1="%s" y1="%s" x2="%s" y2="%s" stroke="%s" stroke-width="1.6"%s/>`, f2(x), f2(cv.py0), f2(x), f2(cv.py1), color, dash)
		return
	}
	xa, xb := cv.x0, cv.x1
	ya, yb := l.Y0+l.Slope*(xa-l.X0), l.Y0+l.Slope*(xb-l.X0)
	cv.printf(`<line x1="%s" y1="%s" x2="%s" y2="%s" stroke="%s" stroke-width="1.6"%s/>`, f2(cv.sx(xa)), f2(cv.sy(ya)), f2(cv.sx(xb)), f2(cv.sy(yb)), color, dash)
}

// sgTextMarks draws a text symbol (PROC PLOT plotting characters) at points.
type sgTextMarks struct {
	X, Y  []float64
	Text  []string
	Color string
}

func (l *sgTextMarks) bounds() (float64, float64, float64, float64) {
	x0, x1 := minmax(l.X)
	y0, y1 := minmax(l.Y)
	return x0, x1, y0, y1
}

func (l *sgTextMarks) draw(cv *canvas) {
	color := l.Color
	if color == "" {
		color = "#000000"
	}
	for i := range l.X {
		if math.IsNaN(l.X[i]) || math.IsNaN(l.Y[i]) {
			continue
		}
		cv.printf(`<text x="%s" y="%s" font-family="Courier New, monospace" font-size="12" text-anchor="middle" fill="%s">%s</text>`,
			f2(cv.sx(l.X[i])), f2(cv.sy(l.Y[i])+4), color, html.EscapeString(l.Text[i]))
	}
}

// ---------------------------------------------------------------- chart environment

// sgEnv holds a procedure's plot statements and data-wide state (category and
// group orders) shared by all charts drawn from it (BY groups, panel cells).
type sgEnv struct {
	s            *Session
	d            *Dataset
	plots        []*sgStmt
	xaxis, yaxis *sgAxis
	noAutoLegend bool
	keyLegend    *sgStmt
	insets       []*sgStmt
	aspect       float64
	// binRows, when set, are the rows used to determine histogram bins
	// (panels with a uniform X axis bin all cells alike).
	binRows [][]Value

	xDiscrete, yDiscrete bool
	xCats, yCats         []string
	xCatIdx, yCatIdx     map[string]int
	groupCache           map[int][]string
	xFmtVar, yFmtVar     *Var
}

// sgPlotKinds lists the plot statements understood by SGPLOT/SGPANEL.
var sgPlotKinds = map[string]bool{
	"SCATTER": true, "SERIES": true, "REG": true, "LOESS": true, "PBSPLINE": true, "NEEDLE": true, "STEP": true,
	"HISTOGRAM": true, "DENSITY": true, "VBOX": true, "HBOX": true, "VBAR": true, "HBAR": true,
	"VLINE": true, "HLINE": true, "DOT": true, "REFLINE": true, "LINEPARM": true, "BAND": true,
}

// sgNewEnv collects plot/axis/legend statements from a procedure body.
func sgNewEnv(s *Session, d *Dataset, body []*Stmt, opts *Opts) (*sgEnv, error) {
	e := &sgEnv{s: s, d: d, groupCache: map[int][]string{}}
	var xa, ya *sgStmt
	for _, st := range body {
		kw := st.Keyword()
		switch {
		case sgPlotKinds[kw]:
			e.plots = append(e.plots, sgParseStmt(st))
		case kw == "XAXIS":
			xa = sgParseStmt(st)
		case kw == "YAXIS":
			ya = sgParseStmt(st)
		case kw == "KEYLEGEND":
			e.keyLegend = sgParseStmt(st)
		case kw == "INSET":
			e.insets = append(e.insets, sgParseStmt(st))
		}
	}
	e.xaxis, e.yaxis = sgParseAxis(xa), sgParseAxis(ya)
	if opts != nil {
		e.noAutoLegend = opts.Has("NOAUTOLEGEND")
		e.aspect = opts.Num("ASPECT", 0)
	}
	return e, e.prepare()
}

func (e *sgEnv) key(v Value, vr *Var) string {
	return strings.TrimSpace(FormatByName(v, vr.Format))
}

// groups returns the group keys of variable idx in data order.
func (e *sgEnv) groups(idx int) []string {
	if g, ok := e.groupCache[idx]; ok {
		return g
	}
	seen := map[string]bool{}
	var out []string
	for _, r := range e.d.Rows {
		k := e.key(r[idx], e.d.Vars[idx])
		if !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	e.groupCache[idx] = out
	return out
}

// cats returns the sorted distinct non-missing formatted values of the given columns.
func (e *sgEnv) cats(cols []int) []string {
	type kv struct {
		v Value
		k string
	}
	var all []kv
	seen := map[string]bool{}
	for _, c := range cols {
		for _, r := range e.d.Rows {
			if r[c].IsMissing() {
				continue
			}
			k := e.key(r[c], e.d.Vars[c])
			if !seen[k] {
				seen[k] = true
				all = append(all, kv{r[c], k})
			}
		}
	}
	sort.SliceStable(all, func(i, j int) bool { return Compare(all[i].v, all[j].v) < 0 })
	out := make([]string, len(all))
	for i, a := range all {
		out[i] = a.k
	}
	return out
}

// prepare validates variables and determines discrete axes and categories.
func (e *sgEnv) prepare() error {
	var xcols, ycols []int
	xd, yd := e.xaxis.Discrete, e.yaxis.Discrete
	addVar := func(name string, dst *[]int) error {
		i, _, err := sgVarRef(e.d, name)
		if err != nil {
			return err
		}
		*dst = append(*dst, i)
		return nil
	}
	for _, p := range e.plots {
		switch p.Kind {
		case "VBAR", "VLINE", "HBAR", "HLINE", "DOT":
			if len(p.Pos) == 0 {
				return fmt.Errorf("%s statement requires a category variable.", p.Kind)
			}
			if p.Kind[0] == 'V' {
				xd = true
				if err := addVar(p.Pos[0].Text, &xcols); err != nil {
					return err
				}
			} else {
				yd = true
				if err := addVar(p.Pos[0].Text, &ycols); err != nil {
					return err
				}
			}
		case "VBOX", "HBOX":
			if p.Kind == "VBOX" {
				xd = true
			} else {
				yd = true
			}
			if c := p.str("CATEGORY"); c != "" {
				dst := &xcols
				if p.Kind == "HBOX" {
					dst = &ycols
				}
				if err := addVar(c, dst); err != nil {
					return err
				}
			}
		}
	}
	// character X/Y on point plots make the axis discrete
	for _, p := range e.plots {
		switch p.Kind {
		case "SCATTER", "SERIES", "NEEDLE", "STEP":
			if x := p.str("X"); x != "" {
				i, v, err := sgVarRef(e.d, x)
				if err != nil {
					return err
				}
				if v.Char || xd {
					xd = true
					xcols = append(xcols, i)
				}
				if e.xFmtVar == nil {
					e.xFmtVar = v
				}
			}
			if y := p.str("Y"); y != "" {
				i, v, err := sgVarRef(e.d, y)
				if err != nil {
					return err
				}
				if v.Char || yd {
					yd = true
					ycols = append(ycols, i)
				}
				if e.yFmtVar == nil {
					e.yFmtVar = v
				}
			}
		}
	}
	e.xDiscrete, e.yDiscrete = xd, yd
	if xd {
		e.xCats = e.cats(xcols)
		if len(e.xCats) == 0 {
			e.xCats = []string{" "}
		}
		if len(e.xaxis.ValueStrs) > 0 {
			e.xCats = e.xaxis.ValueStrs
		}
		e.xCatIdx = sgIndex(e.xCats)
	}
	if yd {
		e.yCats = e.cats(ycols)
		if len(e.yCats) == 0 {
			e.yCats = []string{" "}
		}
		if len(e.yaxis.ValueStrs) > 0 {
			e.yCats = e.yaxis.ValueStrs
		}
		e.yCatIdx = sgIndex(e.yCats)
	}
	return nil
}

func sgIndex(cats []string) map[string]int {
	m := make(map[string]int, len(cats))
	for i, c := range cats {
		m[c] = i
	}
	return m
}

// ---------------------------------------------------------------- chart builder

type sgBuilder struct {
	e        *sgEnv
	rows     [][]Value
	c        *Chart
	legend   []LegendEntry
	legTitle string
	xlabels  []string
	ylabels  []string
	ncycle   int
	cyc      int
	nplots   int
	hist     *sgBinning
	histN    float64
	histScl  string
	grouped  bool
	warned   map[string]bool
}

func (b *sgBuilder) addLabel(dst *[]string, s string) {
	for _, x := range *dst {
		if x == s {
			return
		}
	}
	*dst = append(*dst, s)
}

// nextColor returns the color for a non-grouped plot: plots cycle through the
// GraphData colors when several cycling plots are overlaid.
func (b *sgBuilder) nextColor() string {
	if b.ncycle <= 1 {
		return Palette[0]
	}
	c := PaletteColor(b.cyc)
	b.cyc++
	return c
}

// coord maps a value to an axis coordinate (category index on discrete axes).
func (b *sgBuilder) coord(v Value, vr *Var, discrete bool, idx map[string]int) float64 {
	if discrete {
		if v.IsMissing() {
			return math.NaN()
		}
		if i, ok := idx[b.e.key(v, vr)]; ok {
			return float64(i)
		}
		return math.NaN()
	}
	if v.Char {
		return math.NaN()
	}
	return v.Num
}

func (b *sgBuilder) xcoord(v Value, vr *Var) float64 {
	return b.coord(v, vr, b.e.xDiscrete, b.e.xCatIdx)
}
func (b *sgBuilder) ycoord(v Value, vr *Var) float64 {
	return b.coord(v, vr, b.e.yDiscrete, b.e.yCatIdx)
}

// groupSplit splits rows by the GROUP= variable (nil groupVar: one group).
type sgGroup struct {
	Key   string
	Index int
	Rows  [][]Value
}

func (b *sgBuilder) groupSplit(p *sgStmt) ([]sgGroup, *Var, error) {
	g := p.str("GROUP")
	if g == "" {
		return []sgGroup{{Index: -1, Rows: b.rows}}, nil, nil
	}
	gi, gv, err := sgVarRef(b.e.d, g)
	if err != nil {
		return nil, nil, err
	}
	order := b.e.groups(gi)
	byKey := map[string][][]Value{}
	for _, r := range b.rows {
		k := b.e.key(r[gi], gv)
		byKey[k] = append(byKey[k], r)
	}
	var out []sgGroup
	for i, k := range order {
		if rs, ok := byKey[k]; ok {
			out = append(out, sgGroup{Key: k, Index: i, Rows: rs})
		}
	}
	b.grouped = true
	if b.legTitle == "" {
		b.legTitle = gv.DisplayLabel()
	}
	return out, gv, nil
}

// addGroupLegend adds a legend entry for a group once.
func (b *sgBuilder) addGroupLegend(g sgGroup, kind, marker string, dash bool) {
	for _, l := range b.legend {
		if l.Label == g.Key && l.Kind == kind {
			return
		}
	}
	b.legend = append(b.legend, LegendEntry{Label: g.Key, Color: PaletteColor(g.Index), Kind: kind, Marker: marker, Dash: dash})
}

func (b *sgBuilder) plotLegend(p *sgStmt, def string, color, kind, marker string, dash bool) {
	label := p.str("LEGENDLABEL")
	if label == "" {
		label = def
	}
	b.legend = append(b.legend, LegendEntry{Label: label, Color: color, Kind: kind, Marker: marker, Dash: dash})
}

// build renders the plot statements over rows into a chart.
func (e *sgEnv) build(rows [][]Value) (*Chart, error) {
	b := &sgBuilder{e: e, rows: rows, c: NewChart(""), warned: map[string]bool{}}
	for _, p := range e.plots {
		switch p.Kind {
		case "REFLINE", "LINEPARM", "BAND":
			continue
		case "SCATTER", "SERIES", "DENSITY", "REG", "LOESS", "PBSPLINE", "NEEDLE", "STEP", "VLINE", "HLINE", "DOT":
			if p.str("GROUP") == "" {
				b.ncycle++
			}
		}
		b.nplots++
	}
	// histograms first (densities scale to them)
	for _, p := range e.plots {
		if p.Kind == "HISTOGRAM" && b.hist == nil {
			if err := b.prepHist(p); err != nil {
				return nil, err
			}
		}
	}
	for _, p := range e.plots {
		var err error
		switch p.Kind {
		case "SCATTER":
			err = b.scatter(p)
		case "SERIES", "STEP", "NEEDLE":
			err = b.series(p)
		case "REG", "LOESS", "PBSPLINE":
			err = b.fit(p)
		case "HISTOGRAM":
			err = b.histogram(p)
		case "DENSITY":
			err = b.density(p)
		case "VBOX", "HBOX":
			err = b.box(p)
		case "VBAR", "HBAR", "VLINE", "HLINE", "DOT":
			err = b.bar(p)
		case "REFLINE":
			err = b.refline(p)
		case "LINEPARM":
			err = b.lineparm(p)
		case "BAND":
			err = b.band(p)
		}
		if err != nil {
			return nil, err
		}
	}
	b.finish()
	return b.c, nil
}

func (b *sgBuilder) finish() {
	c, e := b.c, b.e
	if e.xDiscrete {
		c.XCats = e.xCats
	}
	if e.yDiscrete {
		c.YCats = e.yCats
	}
	c.XLabel = strings.Join(b.xlabels, ", ")
	c.YLabel = strings.Join(b.ylabels, ", ")
	if e.xaxis.HasLabel {
		c.XLabel = e.xaxis.Label
	}
	if e.yaxis.HasLabel {
		c.YLabel = e.yaxis.Label
	}
	if e.xaxis.NoLabel {
		c.XLabel = ""
	}
	if e.yaxis.NoLabel {
		c.YLabel = ""
	}
	c.XMin, c.XMax, c.YMin, c.YMax = e.xaxis.Min, e.xaxis.Max, e.yaxis.Min, e.yaxis.Max
	c.XValues, c.YValues = e.xaxis.Values, e.yaxis.Values
	c.XLog, c.YLog = e.xaxis.Log, e.yaxis.Log
	c.NoGrid = !e.xaxis.Grid && !e.yaxis.Grid
	if v := e.xFmtVar; v != nil && v.Format != "" && !e.xDiscrete {
		f := v.Format
		c.XTickFmt = func(x float64) string { return strings.TrimSpace(FormatByName(NumV(x), f)) }
	}
	if v := e.yFmtVar; v != nil && v.Format != "" && !e.yDiscrete {
		f := v.Format
		c.YTickFmt = func(x float64) string { return strings.TrimSpace(FormatByName(NumV(x), f)) }
	}
	if e.aspect > 0 {
		c.Height = math.Max(200, c.Width*e.aspect+80)
	}
	for _, in := range e.insets {
		for _, t := range in.Pos {
			if t.Kind == TString {
				c.Inset = append(c.Inset, t.Text)
			}
		}
	}
	show := (b.grouped || b.nplots > 1) && !e.noAutoLegend || e.keyLegend != nil
	if show && len(b.legend) > 0 {
		c.Legend = b.legend
		if b.grouped {
			c.LegendTitle = b.legTitle
		}
		if e.keyLegend != nil {
			if t := e.keyLegend.str("TITLE"); t != "" {
				c.LegendTitle = t
			}
		}
	}
}

func (b *sgBuilder) xyVars(p *sgStmt) (xi int, xv *Var, yi int, yv *Var, err error) {
	xn, yn := p.str("X"), p.str("Y")
	if xn == "" || yn == "" {
		return 0, nil, 0, nil, fmt.Errorf("%s statement requires X= and Y= variables.", p.Kind)
	}
	if xi, xv, err = sgVarRef(b.e.d, xn); err != nil {
		return
	}
	yi, yv, err = sgVarRef(b.e.d, yn)
	return
}

// markerAttrs returns symbol, explicit color and size from MARKERATTRS=.
func (b *sgBuilder) markerAttrs(p *sgStmt) (string, string, float64) {
	m := p.sub("MARKERATTRS", "MARKERSATTRS")
	sym := strings.ToLower(m.Str("SYMBOL", "circle"))
	return sym, sgColor(m.Str("COLOR", "")), m.Num("SIZE", 0)
}

func (b *sgBuilder) lineAttrs(p *sgStmt) (string, bool, float64) {
	l := p.sub("LINEATTRS")
	return sgColor(l.Str("COLOR", "")), sgDashed(l.Str("PATTERN", "")), l.Num("THICKNESS", 0)
}

func (b *sgBuilder) dataLabels(p *sgStmt, rows [][]Value, yi int) []string {
	op, ok := p.get("DATALABEL")
	if !ok {
		return nil
	}
	li := yi
	if op.Eq {
		if i := b.e.d.Index(op.Str()); i >= 0 {
			li = i
		}
	}
	out := make([]string, len(rows))
	for k, r := range rows {
		out[k] = b.e.key(r[li], b.e.d.Vars[li])
	}
	return out
}

func (b *sgBuilder) scatter(p *sgStmt) error {
	xi, xv, yi, yv, err := b.xyVars(p)
	if err != nil {
		return err
	}
	b.addLabel(&b.xlabels, xv.DisplayLabel())
	b.addLabel(&b.ylabels, yv.DisplayLabel())
	sym, col, size := b.markerAttrs(p)
	groups, _, err := b.groupSplit(p)
	if err != nil {
		return err
	}
	var plain string
	if groups[0].Index < 0 {
		plain = b.nextColor()
		if col != "" {
			plain = col
		}
	}
	for _, g := range groups {
		sc := &Scatter{Marker: sym, Size: size, Labels: b.dataLabels(p, g.Rows, yi)}
		for _, r := range g.Rows {
			sc.X = append(sc.X, b.xcoord(r[xi], xv))
			sc.Y = append(sc.Y, b.ycoord(r[yi], yv))
		}
		if g.Index >= 0 {
			sc.Color = PaletteColor(g.Index)
			b.addGroupLegend(g, "marker", sym, false)
		} else {
			sc.Color = plain
			b.plotLegend(p, yv.DisplayLabel(), plain, "marker", sym, false)
		}
		b.c.Layers = append(b.c.Layers, sc)
	}
	return nil
}

func (b *sgBuilder) series(p *sgStmt) error {
	xi, xv, yi, yv, err := b.xyVars(p)
	if err != nil {
		return err
	}
	b.addLabel(&b.xlabels, xv.DisplayLabel())
	b.addLabel(&b.ylabels, yv.DisplayLabel())
	lcol, dash, thick := b.lineAttrs(p)
	sym, mcol, size := b.markerAttrs(p)
	groups, _, err := b.groupSplit(p)
	if err != nil {
		return err
	}
	plain := ""
	if groups[0].Index < 0 {
		plain = b.nextColor()
	}
	base := p.num("BASELINE")
	if math.IsNaN(base) {
		base = 0
	}
	for _, g := range groups {
		color := plain
		if g.Index >= 0 {
			color = PaletteColor(g.Index)
		} else if lcol != "" {
			color = lcol
		}
		var xs, ys []float64
		for _, r := range g.Rows {
			xs = append(xs, b.xcoord(r[xi], xv))
			ys = append(ys, b.ycoord(r[yi], yv))
		}
		ln := &Line{Color: color, Width: thick, Dash: dash}
		switch p.Kind {
		case "SERIES":
			ln.X, ln.Y = xs, ys
		case "STEP":
			for k := range xs {
				if k > 0 && !math.IsNaN(ys[k-1]) {
					ln.X = append(ln.X, xs[k])
					ln.Y = append(ln.Y, ys[k-1])
				}
				ln.X = append(ln.X, xs[k])
				ln.Y = append(ln.Y, ys[k])
			}
		case "NEEDLE":
			for k := range xs {
				ln.X = append(ln.X, xs[k], xs[k], math.NaN())
				ln.Y = append(ln.Y, base, ys[k], math.NaN())
			}
		}
		b.c.Layers = append(b.c.Layers, ln)
		if p.has("MARKERS") || p.Kind == "NEEDLE" && !p.has("NOMARKERS") {
			mc := color
			if mcol != "" && g.Index < 0 {
				mc = mcol
			}
			b.c.Layers = append(b.c.Layers, &Scatter{X: xs, Y: ys, Color: mc, Marker: sym, Size: size, Labels: b.dataLabels(p, g.Rows, yi)})
		}
		if g.Index >= 0 {
			b.addGroupLegend(g, "line", "", dash)
		} else {
			b.plotLegend(p, yv.DisplayLabel(), color, "line", "", dash)
		}
	}
	return nil
}

// ---------------------------------------------------------------- fits

// sgPolyFit fits y = b0 + b1 x + ... + bd x^d and evaluates prediction and limits.
type sgPolyFit struct {
	fit *OLSFit
	deg int
	mse float64
}

func sgFitPoly(xs, ys []float64, deg int) *sgPolyFit {
	X := make([][]float64, len(xs))
	for i, x := range xs {
		row := make([]float64, deg+1)
		v := 1.0
		for j := range row {
			row[j] = v
			v *= x
		}
		X[i] = row
	}
	f := FitOLS(X, ys, nil)
	mse := math.NaN()
	if f.DFE > 0 {
		mse = f.SSE / float64(f.DFE)
	}
	return &sgPolyFit{fit: f, deg: deg, mse: mse}
}

// at returns the prediction, its standard error and the prediction-interval SE.
func (f *sgPolyFit) at(x float64) (yhat, seMean, sePred float64) {
	row := make([]float64, f.deg+1)
	v := 1.0
	for j := range row {
		row[j] = v
		v *= x
	}
	yhat = f.fit.Predict(row)
	q := 0.0
	for i := range row {
		for j := range row {
			q += row[i] * f.fit.XtXInv[i][j] * row[j]
		}
	}
	return yhat, math.Sqrt(f.mse * q), math.Sqrt(f.mse * (1 + q))
}

// sgLoess fits a local regression (tricube weights, degree 1 or 2) with
// smoothing parameter s (fraction of points in each neighborhood). It returns
// fitted values at the sorted data points and the trace of the smoother matrix.
func sgLoessAt(xs, ys []float64, s float64, deg int, x0 float64, wantHat int) (float64, float64) {
	n := len(xs)
	q := int(math.Floor(s * float64(n)))
	q = max(q, deg+1)
	q = min(q, n)
	// q nearest neighbours of x0 in sorted xs
	pos := sort.SearchFloat64s(xs, x0)
	l, r := pos-1, pos // window (l, r)
	for range q {
		if l < 0 {
			r++
		} else if r >= n {
			l--
		} else if x0-xs[l] <= xs[r]-x0 {
			l--
		} else {
			r++
		}
	}
	lo, hi := l+1, r-1
	h := math.Max(x0-xs[lo], xs[hi]-x0)
	if s > 1 {
		h *= math.Pow(s, 1.0/1.0)
	}
	if h <= 0 {
		h = 1e-12
	}
	// include ties at distance h
	for lo > 0 && x0-xs[lo-1] <= h {
		lo--
	}
	for hi < n-1 && xs[hi+1]-x0 <= h {
		hi++
	}
	// weighted polynomial fit centred at x0
	m := deg + 1
	var A [3][3]float64
	var bv [3]float64
	w := make([]float64, hi-lo+1)
	for i := lo; i <= hi; i++ {
		d := math.Abs(xs[i]-x0) / h
		wt := 0.0
		if d < 1 {
			t := 1 - d*d*d
			wt = t * t * t
		}
		w[i-lo] = wt
		pw := [3]float64{1, xs[i] - x0, (xs[i] - x0) * (xs[i] - x0)}
		for a := range m {
			for c := range m {
				A[a][c] += wt * pw[a] * pw[c]
			}
			bv[a] += wt * pw[a] * ys[i]
		}
	}
	mat := make([][]float64, m)
	for a := range mat {
		mat[a] = make([]float64, m)
		for c := range mat[a] {
			mat[a][c] = A[a][c]
		}
	}
	inv := Invert(mat)
	if inv == nil {
		// degenerate neighbourhood: weighted mean
		sw, sy := 0.0, 0.0
		for i := lo; i <= hi; i++ {
			sw += w[i-lo]
			sy += w[i-lo] * ys[i]
		}
		hat := 0.0
		if wantHat >= lo && wantHat <= hi && sw > 0 {
			hat = w[wantHat-lo] / sw
		}
		return sy / sw, hat
	}
	yhat := 0.0
	for a := range m {
		yhat += inv[0][a] * bv[a]
	}
	hat := 0.0
	if wantHat >= lo && wantHat <= hi {
		pw := [3]float64{1, xs[wantHat] - x0, (xs[wantHat] - x0) * (xs[wantHat] - x0)}
		for a := range m {
			hat += inv[0][a] * pw[a]
		}
		hat *= w[wantHat-lo]
	}
	return yhat, hat
}

// sgLoessAICC returns the AICC criterion (Hurvich, Simonoff & Tsai 1998) for smoothing s.
func sgLoessAICC(xs, ys []float64, s float64, deg int) float64 {
	n := float64(len(xs))
	rss, tr := 0.0, 0.0
	for i := range xs {
		yh, h := sgLoessAt(xs, ys, s, deg, xs[i], i)
		rss += (ys[i] - yh) * (ys[i] - yh)
		tr += h
	}
	if n-tr-2 <= 0 {
		return math.Inf(1)
	}
	return math.Log(rss/n) + 1 + 2*(tr+1)/(n-tr-2)
}

// sgLoessSmooth selects the smoothing parameter minimizing AICC by golden-section search.
func sgLoessSmooth(xs, ys []float64, deg int) float64 {
	n := float64(len(xs))
	a, c := math.Min(1, float64(deg+2)/n+1e-9), 1.0
	if a >= c {
		return 1
	}
	g := (math.Sqrt(5) - 1) / 2
	x1, x2 := c-g*(c-a), a+g*(c-a)
	f1, f2v := sgLoessAICC(xs, ys, x1, deg), sgLoessAICC(xs, ys, x2, deg)
	for k := 0; k < 40 && c-a > 1e-4; k++ {
		if f1 <= f2v {
			c, x2, f2v = x2, x1, f1
			x1 = c - g*(c-a)
			f1 = sgLoessAICC(xs, ys, x1, deg)
		} else {
			a, x1, f1 = x1, x2, f2v
			x2 = a + g*(c-a)
			f2v = sgLoessAICC(xs, ys, x2, deg)
		}
	}
	if f1 <= f2v {
		return x1
	}
	return x2
}

func (b *sgBuilder) fit(p *sgStmt) error {
	if p.Kind == "PBSPLINE" {
		if !b.warned["PBSPLINE"] {
			b.warned["PBSPLINE"] = true
			b.e.s.Log.Warn(p.Line, "The PBSPLINE statement is not supported by sago; the plot is drawn without the spline fit.")
		}
	}
	xi, xv, yi, yv, err := b.xyVars(p)
	if err != nil {
		return err
	}
	b.addLabel(&b.xlabels, xv.DisplayLabel())
	b.addLabel(&b.ylabels, yv.DisplayLabel())
	sym, mcol, size := b.markerAttrs(p)
	lcol, dash, thick := b.lineAttrs(p)
	groups, _, err := b.groupSplit(p)
	if err != nil {
		return err
	}
	plain := ""
	if groups[0].Index < 0 {
		plain = b.nextColor()
	}
	alpha := p.num("ALPHA")
	if math.IsNaN(alpha) {
		alpha = 0.05
	}
	deg := int(p.num("DEGREE"))
	if deg < 1 {
		deg = 1
	}
	if p.Kind == "LOESS" && deg > 2 {
		deg = 2
	}
	pct := strconv.FormatFloat(100*(1-alpha), 'f', -1, 64)
	for _, g := range groups {
		color := plain
		if g.Index >= 0 {
			color = PaletteColor(g.Index)
		}
		var xs, ys []float64
		for _, r := range g.Rows {
			x, y := b.xcoord(r[xi], xv), b.ycoord(r[yi], yv)
			if math.IsNaN(x) || math.IsNaN(y) {
				continue
			}
			xs = append(xs, x)
			ys = append(ys, y)
		}
		if !p.has("NOMARKERS") {
			mc := color
			if mcol != "" && g.Index < 0 {
				mc = mcol
			}
			b.c.Layers = append(b.c.Layers, &Scatter{X: xs, Y: ys, Color: mc, Marker: sym, Size: size})
		}
		if len(xs) < 2 || p.Kind == "PBSPLINE" {
			continue
		}
		fc := color
		if lcol != "" {
			fc = lcol
		}
		lo, hi := minmax(xs)
		const ng = 201
		gx := make([]float64, ng)
		for k := range gx {
			gx[k] = lo + (hi-lo)*float64(k)/float64(ng-1)
		}
		fitLine := &Line{Color: fc, Width: math.Max(thick, 2), Dash: dash}
		name := "Regression"
		if p.Kind == "REG" {
			pf := sgFitPoly(xs, ys, deg)
			t := TInv(1-alpha/2, float64(pf.fit.DFE))
			band := &Band{Color: fc, Opacity: 0.2}
			var pl, pu []float64
			for _, x := range gx {
				yh, sm, sp := pf.at(x)
				fitLine.X = append(fitLine.X, x)
				fitLine.Y = append(fitLine.Y, yh)
				band.X = append(band.X, x)
				band.Lo = append(band.Lo, yh-t*sm)
				band.Hi = append(band.Hi, yh+t*sm)
				pl = append(pl, yh-t*sp)
				pu = append(pu, yh+t*sp)
			}
			if p.has("CLM") && pf.fit.DFE > 0 {
				b.c.Layers = append([]Layer{band}, b.c.Layers...)
				b.legend = append(b.legend, LegendEntry{Label: pct + "% Confidence Limits", Color: fc, Kind: "fill"})
			}
			if p.has("CLI") && pf.fit.DFE > 0 {
				b.c.Layers = append(b.c.Layers, &Line{X: gx, Y: pl, Color: fc, Dash: true, Width: 1.2}, &Line{X: gx, Y: pu, Color: fc, Dash: true, Width: 1.2})
				b.legend = append(b.legend, LegendEntry{Label: pct + "% Prediction Limits", Color: fc, Kind: "line", Dash: true})
			}
		} else {
			name = "Loess"
			idx := make([]int, len(xs))
			for k := range idx {
				idx[k] = k
			}
			sort.SliceStable(idx, func(a, c int) bool { return xs[idx[a]] < xs[idx[c]] })
			sx, sy := make([]float64, len(xs)), make([]float64, len(xs))
			for k, j := range idx {
				sx[k], sy[k] = xs[j], ys[j]
			}
			sm := p.num("SMOOTH")
			if !(sm > 0) {
				sm = sgLoessSmooth(sx, sy, deg)
			}
			for _, x := range gx {
				yh, _ := sgLoessAt(sx, sy, sm, deg, x, -1)
				fitLine.X = append(fitLine.X, x)
				fitLine.Y = append(fitLine.Y, yh)
			}
		}
		b.c.Layers = append(b.c.Layers, fitLine)
		if g.Index >= 0 {
			b.addGroupLegend(g, "line", "", dash)
		} else {
			b.plotLegend(p, name, fc, "line", "", dash)
		}
	}
	return nil
}

// ---------------------------------------------------------------- histogram / density

func sgNumVals(rows [][]Value, i int) []float64 {
	out := make([]float64, 0, len(rows))
	for _, r := range rows {
		if !r[i].Char && !math.IsNaN(r[i].Num) {
			out = append(out, r[i].Num)
		}
	}
	return out
}

func sgScaleLabel(scale string) string {
	switch scale {
	case "COUNT":
		return "Count"
	case "PROPORTION":
		return "Proportion"
	case "DENSITY":
		return "Density"
	}
	return "Percent"
}

// sgHistBinning determines the bins for a HISTOGRAM statement on the given rows.
func sgHistBinning(p *sgStmt, rows [][]Value, vi int) sgBinning {
	return sgBins(Sorted(sgNumVals(rows, vi)), p.num("NBINS"), p.num("BINWIDTH"), p.num("BINSTART"))
}

func (b *sgBuilder) prepHist(p *sgStmt) error {
	if len(p.Pos) == 0 {
		return fmt.Errorf("HISTOGRAM statement requires a variable.")
	}
	vi, _, err := sgVarRef(b.e.d, p.Pos[0].Text)
	if err != nil {
		return err
	}
	brows := b.rows
	if b.e.binRows != nil {
		brows = b.e.binRows
	}
	bins := sgHistBinning(p, brows, vi)
	b.hist = &bins
	b.histScl = strings.ToUpper(p.str("SCALE"))
	if b.histScl == "" {
		b.histScl = "PERCENT"
	}
	b.histN = float64(len(sgNumVals(b.rows, vi)))
	return nil
}

func (b *sgBuilder) histogram(p *sgStmt) error {
	vi, vv, err := sgVarRef(b.e.d, p.Pos[0].Text)
	if err != nil {
		return err
	}
	b.addLabel(&b.xlabels, vv.DisplayLabel())
	if b.e.xFmtVar == nil {
		b.e.xFmtVar = vv
	}
	scale := strings.ToUpper(p.str("SCALE"))
	if scale == "" {
		scale = "PERCENT"
	}
	b.addLabel(&b.ylabels, sgScaleLabel(scale))
	bins := *b.hist
	if brows := b.e.binRows; brows == nil && p.str("GROUP") == "" {
		bins = sgHistBinning(p, b.rows, vi)
	}
	fill := sgColor(p.sub("FILLATTRS").Str("COLOR", ""))
	tr := p.num("TRANSPARENCY")
	groups, _, err := b.groupSplit(p)
	if err != nil {
		return err
	}
	for _, g := range groups {
		vals := sgNumVals(g.Rows, vi)
		cnt := bins.Counts(vals, nil)
		n := float64(len(vals))
		bars := &Bars{Outline: "#333333"}
		switch {
		case g.Index >= 0:
			bars.Color = PaletteColor(g.Index)
			bars.Opacity = 0.5
			b.addGroupLegend(g, "fill", "", false)
		case fill != "":
			bars.Color = fill
		default:
			bars.Color = sgFillDefault
		}
		if !math.IsNaN(tr) {
			bars.Opacity = math.Max(0.02, 1-tr)
		}
		if p.has("NOFILL") {
			bars.Opacity = 0.01
		}
		for k, c := range cnt {
			v := c
			switch scale {
			case "PERCENT":
				v = 100 * c / n
			case "PROPORTION":
				v = c / n
			case "DENSITY":
				v = c / (n * bins.W)
			}
			x0 := bins.Start + float64(k)*bins.W
			bars.X0 = append(bars.X0, x0)
			bars.X1 = append(bars.X1, x0+bins.W)
			bars.Y0 = append(bars.Y0, 0)
			bars.Y1 = append(bars.Y1, v)
		}
		b.c.Layers = append(b.c.Layers, bars)
		if g.Index < 0 && p.str("LEGENDLABEL") != "" {
			b.plotLegend(p, "", bars.Color, "fill", "", false)
		}
	}
	if p.has("SHOWBINS") && len(b.e.xaxis.Values) == 0 {
		var mids []float64
		for k := range bins.N {
			mids = append(mids, cleanFloat(bins.Start+(float64(k)+0.5)*bins.W, bins.W/100))
		}
		b.c.XValues = mids
		b.e.xaxis.Values = mids
	}
	return nil
}

func (b *sgBuilder) density(p *sgStmt) error {
	if len(p.Pos) == 0 {
		return fmt.Errorf("DENSITY statement requires a variable.")
	}
	vi, vv, err := sgVarRef(b.e.d, p.Pos[0].Text)
	if err != nil {
		return err
	}
	b.addLabel(&b.xlabels, vv.DisplayLabel())
	lcol, dash, thick := b.lineAttrs(p)
	typ := "NORMAL"
	var tOpts *Opts
	if op, ok := p.get("TYPE"); ok && op.Eq && len(op.Val) > 0 {
		typ = op.Val[0].Upper()
		if len(op.Val) > 1 {
			tOpts = ParseOpts(op.Val[2 : len(op.Val)-1])
		}
	}
	if tOpts == nil {
		tOpts = &Opts{}
	}
	scale := strings.ToUpper(p.str("SCALE"))
	if b.hist != nil {
		scale = b.histScl
	} else if scale == "" {
		scale = "PERCENT"
	}
	if b.hist == nil {
		b.addLabel(&b.ylabels, sgScaleLabel(scale))
	}
	groups, _, err := b.groupSplit(p)
	if err != nil {
		return err
	}
	plain := ""
	if groups[0].Index < 0 {
		plain = b.nextColor()
		if lcol != "" {
			plain = lcol
		}
	}
	for _, g := range groups {
		s := Sorted(sgNumVals(g.Rows, vi))
		if len(s) == 0 {
			continue
		}
		n := float64(len(s))
		w := 1.0
		var lo, hi float64
		if b.hist != nil {
			w = b.hist.W
			lo, hi = b.hist.Start, b.hist.Start+float64(b.hist.N)*b.hist.W
		} else {
			bins := sgDefaultBins(s, 0)
			w = bins.W
			r := s[len(s)-1] - s[0]
			lo, hi = s[0]-0.05*r, s[len(s)-1]+0.05*r
		}
		factor := 1.0
		switch scale {
		case "PERCENT":
			factor = 100 * w
		case "COUNT":
			factor = n * w
		case "PROPORTION":
			factor = w
		}
		var f func(float64) float64
		label := "Normal"
		if typ == "KERNEL" {
			label = "Kernel"
			h := sgKernelBandwidth(s, sgOptNum(tOpts, "C"))
			wt := strings.ToUpper(tOpts.Str("WEIGHT", "NORMAL"))
			f = func(x float64) float64 { return sgKernelDensity(s, h, wt, x) }
		} else {
			mu, sd := sgOptNum(tOpts, "MU"), sgOptNum(tOpts, "SIGMA")
			if math.IsNaN(mu) {
				mu = Mean(s)
			}
			if math.IsNaN(sd) {
				sd = StdDev(s)
			}
			f = func(x float64) float64 { return NormPDF((x-mu)/sd) / sd }
		}
		color := plain
		if g.Index >= 0 {
			color = PaletteColor(g.Index)
		}
		ln := &Line{Color: color, Width: math.Max(thick, 2), Dash: dash}
		const ng = 241
		for k := range ng {
			x := lo + (hi-lo)*float64(k)/float64(ng-1)
			ln.X = append(ln.X, x)
			ln.Y = append(ln.Y, factor*f(x))
		}
		b.c.Layers = append(b.c.Layers, ln)
		if g.Index >= 0 {
			b.addGroupLegend(g, "line", "", dash)
		} else {
			b.plotLegend(p, label, color, "line", "", dash)
		}
	}
	return nil
}

// ---------------------------------------------------------------- boxes

func (b *sgBuilder) box(p *sgStmt) error {
	if len(p.Pos) == 0 {
		return fmt.Errorf("%s statement requires an analysis variable.", p.Kind)
	}
	vi, vv, err := sgVarRef(b.e.d, p.Pos[0].Text)
	if err != nil {
		return err
	}
	horiz := p.Kind == "HBOX"
	ci, cv := -1, (*Var)(nil)
	if c := p.str("CATEGORY"); c != "" {
		ci, cv, _ = sgVarRef(b.e.d, c)
	}
	if horiz {
		b.addLabel(&b.xlabels, vv.DisplayLabel())
		if cv != nil {
			b.addLabel(&b.ylabels, cv.DisplayLabel())
		}
	} else {
		b.addLabel(&b.ylabels, vv.DisplayLabel())
		if cv != nil {
			b.addLabel(&b.xlabels, cv.DisplayLabel())
		}
	}
	cats, catIdx := b.e.xCats, b.e.xCatIdx
	if horiz {
		cats, catIdx = b.e.yCats, b.e.yCatIdx
	}
	groups, _, err := b.groupSplit(p)
	if err != nil {
		return err
	}
	ng := len(groups)
	fill := sgColor(p.sub("FILLATTRS").Str("COLOR", ""))
	for gi, g := range groups {
		vals := make([][]float64, len(cats))
		for _, r := range g.Rows {
			k := 0
			if ci >= 0 {
				if r[ci].IsMissing() {
					continue
				}
				kk, ok := catIdx[b.e.key(r[ci], cv)]
				if !ok {
					continue
				}
				k = kk
			}
			if !r[vi].Char && !math.IsNaN(r[vi].Num) {
				vals[k] = append(vals[k], r[vi].Num)
			}
		}
		color := Palette[0]
		if g.Index >= 0 {
			color = PaletteColor(g.Index)
			b.addGroupLegend(g, "fill", "", false)
		} else if fill != "" {
			color = fill
		}
		layer := &Boxes{Color: color}
		width := 0.5 / float64(ng)
		for k, vs := range vals {
			if len(vs) == 0 {
				continue
			}
			bs := sgBoxStats(vs)
			bs.Pos = float64(k)
			if ng > 1 {
				bs.Pos += (float64(gi) - float64(ng-1)/2) * (0.8 / float64(ng))
			}
			bs.Width = width
			bs.Horizontal = horiz
			if p.has("NOMEAN") {
				bs.Mean = math.NaN()
			}
			if p.has("NOOUTLIERS") {
				bs.Outliers = nil
			}
			layer.Boxes = append(layer.Boxes, bs)
		}
		b.c.Layers = append(b.c.Layers, layer)
	}
	return nil
}

// ---------------------------------------------------------------- bars / lines / dots

func sgStatLabel(stat string, resp *Var) string {
	switch stat {
	case "FREQ":
		return "Frequency"
	case "PERCENT":
		return "Percent"
	}
	name := map[string]string{"SUM": "Sum", "MEAN": "Mean", "MEDIAN": "Median"}[stat]
	return resp.DisplayLabel() + " (" + name + ")"
}

func (b *sgBuilder) bar(p *sgStmt) error {
	ci, cv, err := sgVarRef(b.e.d, p.Pos[0].Text)
	if err != nil {
		return err
	}
	ri, rv := -1, (*Var)(nil)
	if r := p.str("RESPONSE"); r != "" {
		if ri, rv, err = sgVarRef(b.e.d, r); err != nil {
			return err
		}
	}
	stat := strings.ToUpper(p.str("STAT"))
	if stat == "" {
		stat = "FREQ"
		if ri >= 0 {
			stat = "SUM"
		}
	}
	if ri < 0 && stat != "PERCENT" {
		stat = "FREQ"
	}
	vertical := p.Kind == "VBAR" || p.Kind == "VLINE"
	cats, catIdx := b.e.xCats, b.e.xCatIdx
	if !vertical {
		cats, catIdx = b.e.yCats, b.e.yCatIdx
	}
	if vertical {
		b.addLabel(&b.xlabels, cv.DisplayLabel())
		b.addLabel(&b.ylabels, sgStatLabel(stat, rv))
	} else {
		b.addLabel(&b.ylabels, cv.DisplayLabel())
		b.addLabel(&b.xlabels, sgStatLabel(stat, rv))
	}
	groups, _, err := b.groupSplit(p)
	if err != nil {
		return err
	}
	// statistic per group per category
	total := 0.0
	vals := make([][]float64, len(groups))
	for gi, g := range groups {
		acc := make([][]float64, len(cats))
		cnt := make([]float64, len(cats))
		for _, r := range g.Rows {
			if r[ci].IsMissing() {
				continue
			}
			k, ok := catIdx[b.e.key(r[ci], cv)]
			if !ok {
				continue
			}
			cnt[k]++
			if ri >= 0 && !r[ri].Char && !math.IsNaN(r[ri].Num) {
				acc[k] = append(acc[k], r[ri].Num)
			}
		}
		vals[gi] = make([]float64, len(cats))
		for k := range cats {
			var v float64
			switch stat {
			case "FREQ":
				v = cnt[k]
			case "PERCENT":
				if ri >= 0 {
					v = Sum(acc[k])
				} else {
					v = cnt[k]
				}
			case "SUM":
				v = Sum(acc[k])
				if len(acc[k]) == 0 {
					v = math.NaN()
				}
			case "MEAN":
				v = Mean(acc[k])
			case "MEDIAN":
				v = Percentile(Sorted(acc[k]), 50)
			}
			vals[gi][k] = v
			if stat == "PERCENT" {
				total += v
			}
		}
	}
	if stat == "PERCENT" && total > 0 {
		for gi := range vals {
			for k := range vals[gi] {
				vals[gi][k] = 100 * vals[gi][k] / total
			}
		}
	}
	fill := sgColor(p.sub("FILLATTRS").Str("COLOR", ""))
	ng := len(groups)
	cluster := strings.EqualFold(p.str("GROUPDISPLAY"), "CLUSTER")
	label := p.has("DATALABEL")
	if p.Kind == "VBAR" || p.Kind == "HBAR" {
		bw := p.num("BARWIDTH")
		if math.IsNaN(bw) {
			bw = 0.8
		}
		stack := make([]float64, len(cats))
		for gi, g := range groups {
			bars := &Bars{Outline: "#333333"}
			switch {
			case g.Index >= 0:
				bars.Color = PaletteColor(g.Index)
				b.addGroupLegend(g, "fill", "", false)
			case fill != "":
				bars.Color = fill
			default:
				bars.Color = sgFillDefault
			}
			if tr := p.num("TRANSPARENCY"); !math.IsNaN(tr) {
				bars.Opacity = math.Max(0.02, 1-tr)
			}
			for k, v := range vals[gi] {
				if math.IsNaN(v) {
					continue
				}
				c0, c1 := float64(k)-bw/2, float64(k)+bw/2
				v0, v1 := 0.0, v
				if ng > 1 && cluster {
					w := bw / float64(ng)
					c0 = float64(k) - bw/2 + float64(gi)*w
					c1 = c0 + w
				} else if ng > 1 {
					v0, v1 = stack[k], stack[k]+v
					stack[k] += v
				}
				if vertical {
					bars.X0, bars.X1, bars.Y0, bars.Y1 = append(bars.X0, c0), append(bars.X1, c1), append(bars.Y0, v0), append(bars.Y1, v1)
				} else {
					bars.X0, bars.X1, bars.Y0, bars.Y1 = append(bars.X0, v0), append(bars.X1, v1), append(bars.Y0, c0), append(bars.Y1, c1)
				}
				if label && vertical {
					bars.Labels = append(bars.Labels, FormatTick(cleanFloat(v, math.Max(math.Abs(v)/1e4, 1e-4))))
				} else {
					bars.Labels = append(bars.Labels, "")
				}
			}
			b.c.Layers = append(b.c.Layers, bars)
		}
		return nil
	}
	// VLINE / HLINE / DOT
	plain := ""
	if groups[0].Index < 0 {
		plain = b.nextColor()
	}
	lcol, dash, thick := b.lineAttrs(p)
	sym, mcol, size := b.markerAttrs(p)
	for gi, g := range groups {
		color := plain
		if g.Index >= 0 {
			color = PaletteColor(g.Index)
		} else if lcol != "" {
			color = lcol
		}
		pos := make([]float64, len(cats))
		for k := range cats {
			pos[k] = float64(k)
		}
		xs, ys := pos, vals[gi]
		if !vertical {
			xs, ys = vals[gi], pos
		}
		if p.Kind != "DOT" {
			b.c.Layers = append(b.c.Layers, &Line{X: xs, Y: ys, Color: color, Dash: dash, Width: thick})
		}
		if p.Kind == "DOT" || p.has("MARKERS") {
			mc := color
			if mcol != "" && g.Index < 0 {
				mc = mcol
			}
			if p.Kind == "DOT" && !p.has("MARKERATTRS") {
				sym = "circlefilled"
			}
			b.c.Layers = append(b.c.Layers, &Scatter{X: xs, Y: ys, Color: mc, Marker: sym, Size: size})
		}
		if g.Index >= 0 {
			b.addGroupLegend(g, "line", "", dash)
		} else {
			b.plotLegend(p, sgStatLabel(stat, rv), color, "line", "", dash)
		}
	}
	return nil
}

// ---------------------------------------------------------------- reference lines, bands

func (b *sgBuilder) refline(p *sgStmt) error {
	axis := byte('y')
	if strings.EqualFold(p.str("AXIS"), "X") || strings.EqualFold(p.str("AXIS"), "X2") {
		axis = 'x'
	}
	discrete, idx := b.e.yDiscrete, b.e.yCatIdx
	if axis == 'x' {
		discrete, idx = b.e.xDiscrete, b.e.xCatIdx
	}
	var labels []string
	labelAll := false
	if op, ok := p.get("LABEL"); ok {
		if op.Eq {
			_, labels = sgValueList(op.Inner())
			if len(labels) == 0 {
				labels = []string{op.Str()}
			}
		} else {
			labelAll = true
		}
	}
	col, dash, _ := b.lineAttrs(p)
	var vals []float64
	var texts []string
	for _, t := range p.Pos {
		switch t.Kind {
		case TNumber:
			vals = append(vals, t.Num)
			texts = append(texts, t.Text)
		case TString:
			if discrete {
				if k, ok := idx[t.Text]; ok {
					vals = append(vals, float64(k))
					texts = append(texts, t.Text)
				}
			}
		case TIdent:
			vi, vv, err := sgVarRef(b.e.d, t.Text)
			if err != nil {
				return err
			}
			seen := map[string]bool{}
			for _, r := range b.rows {
				if r[vi].IsMissing() {
					continue
				}
				k := b.e.key(r[vi], vv)
				if seen[k] {
					continue
				}
				seen[k] = true
				v := b.coord(r[vi], vv, discrete, idx)
				if !math.IsNaN(v) {
					vals = append(vals, v)
					texts = append(texts, k)
				}
			}
		}
	}
	// "-5" lexes as op + number
	for i := 0; i+1 < len(p.Pos); i++ {
		if p.Pos[i].IsOp("-") && p.Pos[i+1].Kind == TNumber {
			for k, v := range vals {
				if v == p.Pos[i+1].Num && texts[k] == p.Pos[i+1].Text {
					vals[k] = -v
					texts[k] = "-" + texts[k]
					break
				}
			}
		}
	}
	if col == "" {
		col = "#555555"
	}
	for k, v := range vals {
		rl := &RefLine{Axis: axis, Value: v, Color: col, Dash: dash}
		switch {
		case k < len(labels):
			rl.Label = labels[k]
		case labelAll:
			rl.Label = texts[k]
		}
		b.c.Layers = append(b.c.Layers, rl)
	}
	return nil
}

// sgNumOrVar returns a numeric option value: a literal, or the first non-missing value of a variable.
func (b *sgBuilder) numOrVar(p *sgStmt, key string) ([]float64, error) {
	op, ok := p.get(key)
	if !ok || !op.Eq || len(op.Val) == 0 {
		return nil, fmt.Errorf("%s statement requires %s=.", p.Kind, key)
	}
	t := op.Val[0]
	if t.Kind == TNumber {
		return []float64{t.Num}, nil
	}
	vi, _, err := sgVarRef(b.e.d, t.Text)
	if err != nil {
		return nil, err
	}
	out := make([]float64, len(b.rows))
	for k, r := range b.rows {
		out[k] = r[vi].Num
	}
	return out, nil
}

func (b *sgBuilder) lineparm(p *sgStmt) error {
	xs, err := b.numOrVar(p, "X")
	if err != nil {
		return err
	}
	ys, err := b.numOrVar(p, "Y")
	if err != nil {
		return err
	}
	ss, err := b.numOrVar(p, "SLOPE")
	if err != nil {
		return err
	}
	col, dash, _ := b.lineAttrs(p)
	if col == "" {
		col = b.nextColor()
	}
	n := max(len(xs), len(ys), len(ss))
	at := func(v []float64, k int) float64 {
		if len(v) == 1 {
			return v[0]
		}
		return v[k]
	}
	for k := range n {
		x, y, s := at(xs, k), at(ys, k), at(ss, k)
		if math.IsNaN(x) || math.IsNaN(y) {
			continue
		}
		if math.IsNaN(s) {
			s = math.Inf(1)
		}
		b.c.Layers = append(b.c.Layers, &sgLineParm{X0: x, Y0: y, Slope: s, Color: col, Dash: dash})
		if len(xs) == 1 && len(ys) == 1 && len(ss) == 1 {
			break
		}
	}
	return nil
}

func (b *sgBuilder) band(p *sgStmt) error {
	xn := p.str("X")
	if xn == "" {
		return fmt.Errorf("BAND statement requires X= (Y= bands are not supported).")
	}
	xi, xv, err := sgVarRef(b.e.d, xn)
	if err != nil {
		return err
	}
	lo, err := b.numOrVar(p, "LOWER")
	if err != nil {
		return err
	}
	hi, err := b.numOrVar(p, "UPPER")
	if err != nil {
		return err
	}
	type pt struct{ x, l, h float64 }
	var pts []pt
	for k, r := range b.rows {
		x := b.xcoord(r[xi], xv)
		l, h := lo[0], hi[0]
		if len(lo) > 1 {
			l = lo[k]
		}
		if len(hi) > 1 {
			h = hi[k]
		}
		if !math.IsNaN(x) {
			pts = append(pts, pt{x, l, h})
		}
	}
	sort.SliceStable(pts, func(a, c int) bool { return pts[a].x < pts[c].x })
	col := sgColor(p.sub("FILLATTRS").Str("COLOR", ""))
	if col == "" {
		col = b.nextColor()
	}
	op := 0.35
	if tr := p.num("TRANSPARENCY"); !math.IsNaN(tr) {
		op = math.Max(0.02, 1-tr)
	}
	band := &Band{Color: col, Opacity: op}
	for _, q := range pts {
		band.X = append(band.X, q.x)
		band.Lo = append(band.Lo, q.l)
		band.Hi = append(band.Hi, q.h)
	}
	b.c.Layers = append([]Layer{band}, b.c.Layers...)
	if p.str("LEGENDLABEL") != "" {
		b.plotLegend(p, "", col, "fill", "", false)
	}
	return nil
}

// ---------------------------------------------------------------- multi-cell figures

// sgCell is one cell of a panel/matrix figure.
type sgCell struct {
	Header string
	Chart  *Chart // nil = empty cell
	Text   string // centered text for an empty cell (scatter-matrix diagonal)
}

// sgChartBounds returns the data bounds of a chart's layers.
func sgChartBounds(c *Chart) (x0, x1, y0, y1 float64, bars bool) {
	x0, x1, y0, y1 = math.Inf(1), math.Inf(-1), math.Inf(1), math.Inf(-1)
	for _, l := range c.Layers {
		a, bb, cc, d := l.bounds()
		if !math.IsNaN(a) {
			x0, x1 = math.Min(x0, a), math.Max(x1, bb)
		}
		if !math.IsNaN(cc) {
			y0, y1 = math.Min(y0, cc), math.Max(y1, d)
		}
		if _, ok := l.(*Bars); ok {
			bars = true
		}
	}
	return
}

// sgUnify gives charts a common axis range (axis 'x' or 'y') unless fixed by the user.
func sgUnify(charts []*Chart, axis byte) {
	lo, hi := math.Inf(1), math.Inf(-1)
	bars := false
	var cs []*Chart
	for _, c := range charts {
		if c == nil {
			continue
		}
		if axis == 'x' && (len(c.XCats) > 0 || !math.IsNaN(c.XMin) || len(c.XValues) > 0) ||
			axis == 'y' && (len(c.YCats) > 0 || !math.IsNaN(c.YMin) || len(c.YValues) > 0) {
			return
		}
		x0, x1, y0, y1, bb := sgChartBounds(c)
		if axis == 'x' {
			lo, hi = math.Min(lo, x0), math.Max(hi, x1)
		} else {
			lo, hi = math.Min(lo, y0), math.Max(hi, y1)
			bars = bars || bb
		}
		cs = append(cs, c)
	}
	if len(cs) == 0 || math.IsInf(lo, 0) {
		return
	}
	a, b, ticks := axisRange(lo, hi, math.NaN(), math.NaN(), bars, nil)
	for _, c := range cs {
		if axis == 'x' {
			c.XMin, c.XMax, c.XValues = a, b, ticks
		} else {
			c.YMin, c.YMax, c.YValues = a, b, ticks
		}
	}
}

// sgGridSVG lays out cells in a grid with headers and a shared legend.
func sgGridSVG(cells []sgCell, cols int, cellW, cellH float64, legend []LegendEntry, legTitle string) string {
	if cols < 1 {
		cols = 1
	}
	rows := (len(cells) + cols - 1) / cols
	hdr := 0.0
	for _, c := range cells {
		if c.Header != "" {
			hdr = 20
		}
	}
	legH := 0.0
	if len(legend) > 0 {
		legH = 30
	}
	W := float64(cols) * cellW
	H := float64(rows)*(cellH+hdr) + legH
	cv := &canvas{}
	cv.printf(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %s %s" width="%s" height="%s" font-family="Arial, Helvetica, sans-serif" font-size="12">`, f2(W), f2(H), f2(W), f2(H))
	cv.printf(`<rect x="0" y="0" width="%s" height="%s" fill="#ffffff"/>`, f2(W), f2(H))
	for i, cell := range cells {
		x := float64(i%cols) * cellW
		y := float64(i/cols) * (cellH + hdr)
		if cell.Header != "" {
			cv.printf(`<rect x="%s" y="%s" width="%s" height="18" fill="#E6E9EE" stroke="#b0b7bb"/>`, f2(x+4), f2(y+1), f2(cellW-8))
			cv.printf(`<text x="%s" y="%s" text-anchor="middle" fill="#000">%s</text>`, f2(x+cellW/2), f2(y+14), html.EscapeString(cell.Header))
		}
		if cell.Chart != nil {
			cell.Chart.Width, cell.Chart.Height = cellW, cellH
			svg := cell.Chart.SVG()
			svg = strings.Replace(svg, "<svg ", fmt.Sprintf(`<svg x="%s" y="%s" `, f2(x), f2(y+hdr)), 1)
			cv.b.WriteString(svg)
		} else if cell.Text != "" {
			cv.printf(`<rect x="%s" y="%s" width="%s" height="%s" fill="#fff" stroke="#b0b7bb"/>`, f2(x+10), f2(y+hdr+10), f2(cellW-20), f2(cellH-20))
			cv.printf(`<text x="%s" y="%s" text-anchor="middle" font-size="13" fill="#000">%s</text>`, f2(x+cellW/2), f2(y+hdr+cellH/2), html.EscapeString(cell.Text))
		}
	}
	if len(legend) > 0 {
		ly := H - 14
		lx := 20.0
		if legTitle != "" {
			cv.printf(`<text x="%s" y="%s" fill="#000">%s</text>`, f2(lx), f2(ly+4), html.EscapeString(legTitle))
			lx += float64(len(legTitle))*7 + 10
		}
		for _, e := range legend {
			switch e.Kind {
			case "line":
				dash := ""
				if e.Dash {
					dash = ` stroke-dasharray="6,4"`
				}
				cv.printf(`<line x1="%s" y1="%s" x2="%s" y2="%s" stroke="%s" stroke-width="2"%s/>`, f2(lx), f2(ly), f2(lx+22), f2(ly), e.Color, dash)
				lx += 26
			case "fill":
				cv.printf(`<rect x="%s" y="%s" width="12" height="12" fill="%s" stroke="#333"/>`, f2(lx), f2(ly-6), e.Color)
				lx += 16
			default:
				drawMarker(cv, lx+5, ly, e.Marker, e.Color, 8)
				lx += 14
			}
			cv.printf(`<text x="%s" y="%s" fill="#333">%s</text>`, f2(lx), f2(ly+4), html.EscapeString(e.Label))
			lx += float64(len(e.Label))*7 + 16
		}
	}
	cv.printf(`</svg>`)
	return cv.b.String()
}

// sgGridDims picks a grid for n cells given optional COLUMNS=/ROWS=.
func sgGridDims(n, cols, rows int) (int, int) {
	switch {
	case cols > 0 && rows > 0:
		return cols, rows
	case cols > 0:
		return cols, max(1, (n+cols-1)/cols)
	case rows > 0:
		return max(1, (n+rows-1)/rows), rows
	}
	c := int(math.Ceil(math.Sqrt(float64(n))))
	c = max(1, c)
	return c, max(1, (n+c-1)/c)
}

// sgCellSize returns cell dimensions for a grid with the given number of columns.
func sgCellSize(cols int) (float64, float64) {
	w := math.Max(240, math.Min(480, 880/float64(cols)))
	return w, w * 0.78
}

// sgByLoop runs fn for each BY group of the procedure's data (BY line set).
func sgByLoop(s *Session, p *ProcStep, d *Dataset, fn func(rows [][]Value) error) error {
	by, groups, err := s.ProcBy(p, d)
	if err != nil {
		return err
	}
	for _, g := range groups {
		if by != nil {
			s.Out.SetBy(by.Vars, g.Keys)
		}
		if err := fn(g.Rows); err != nil {
			return err
		}
	}
	return nil
}
