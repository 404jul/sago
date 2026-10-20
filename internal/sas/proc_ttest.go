package sas

import (
	"fmt"
	"math"
	"strings"
)

// PROC TTEST (tt prefix): one-sample, two-sample (pooled + Satterthwaite)
// and paired t tests. ODS tables: Statistics, ConfLimits, TTests, Equality.
// Default ODS graphics (summary panel + Q-Q plot) as SVG unless disabled.

func init() { RegisterProc("TTEST", ttTtest) }

func ttTtest(s *Session, p *ProcStep) error {
	d, err := s.ProcData(p)
	if err != nil {
		return err
	}
	h0 := p.Opts.Num("H0", p.Opts.Num("MU0", 0))
	alpha := p.Opts.Num("ALPHA", 0.05)
	sides := strings.ToUpper(p.Opts.Str("SIDES", "2"))
	tostLo, tostHi := math.NaN(), math.NaN()
	if op, ok := p.Opts.Get("TOST"); ok {
		nums := []float64{}
		sign := 1.0
		for _, t := range op.Inner() {
			switch {
			case t.IsOp("-"):
				sign = -1
			case t.IsOp("+"):
				sign = 1
			case t.Kind == TNumber:
				nums = append(nums, sign*t.Num)
				sign = 1
			}
		}
		switch len(nums) {
		case 1:
			tostLo, tostHi = -nums[0], nums[0]
		case 2:
			tostLo, tostHi = nums[0], nums[1]
		default:
			return fmt.Errorf("TOST= requires one or two margin values.")
		}
	}
	by, groups, err := s.ProcBy(p, d)
	if err != nil {
		return err
	}
	classStmt := p.First("CLASS")
	pairedStmt := p.First("PAIRED")
	varStmt := p.First("VAR")
	if varStmt == nil && pairedStmt == nil {
		return fmt.Errorf("no VAR or PAIRED statement.")
	}
	var vars []string
	if varStmt != nil {
		vars, err = ExpandVarList(d, varStmt.Args())
		if err != nil {
			return err
		}
	}
	showPlots := ttPlotsEnabled(s, p)
	for _, g := range groups {
		sub := d.Subset(g.Rows)
		if by != nil {
			s.Out.SetBy(by.Vars, g.Keys)
		}
		if pairedStmt != nil {
			if !math.IsNaN(tostLo) {
				s.Log.Warn(p.Line, "TOST equivalence testing is supported for the two-sample design only; the option is ignored.")
				tostLo, tostHi = math.NaN(), math.NaN()
			}
			pairs, err2 := ttParsePairs(d, pairedStmt.Args())
			if err2 != nil {
				return err2
			}
			for _, pr := range pairs {
				ttPaired(s, sub, d, pr, h0, alpha, sides)
			}
			continue
		}
		if classStmt != nil {
			ci := d.Index(classStmt.Args()[0].Text)
			if ci < 0 {
				return fmt.Errorf("Variable %s not found.", strings.ToUpper(classStmt.Args()[0].Text))
			}
			for _, name := range vars {
				if err2 := ttTwoSample(s, sub, d.Var(name), ci, d.Vars[ci], h0, alpha, sides, showPlots, tostLo, tostHi); err2 != nil {
					return err2
				}
			}
		} else {
			if !math.IsNaN(tostLo) {
				s.Log.Warn(p.Line, "TOST equivalence testing is supported for the two-sample design only; the option is ignored.")
				tostLo, tostHi = math.NaN(), math.NaN()
			}
			for _, name := range vars {
				ttOneSample(s, sub, d.Var(name), h0, alpha, sides, showPlots)
			}
		}
	}
	return nil
}

func ttPlotsEnabled(s *Session, p *ProcStep) bool {
	if v, ok := s.Ext["odsgraphics"].(bool); ok && !v {
		return false
	}
	if p.Opts.Has("PLOTS") {
		op, _ := p.Opts.Get("PLOTS")
		u := strings.ToUpper(op.Str())
		if u == "NONE" {
			return false
		}
	}
	return true
}

// ttParsePairs parses PAIRED a*b or (a b)*(c d) var lists.
func ttParsePairs(d *Dataset, toks []Token) ([][2]string, error) {
	var out [][2]string
	c := NewCursor(toks)
	readSide := func() ([]string, error) { //nolint
		if c.Peek().IsOp("(") {
			g, _ := c.Group()
			names := VarNames(g)
			for _, n := range names {
				if d.Index(n) < 0 {
					return nil, fmt.Errorf("Variable %s not found.", strings.ToUpper(n))
				}
			}
			return names, nil
		}
		t := c.Next()
		if t.Kind != TIdent || d.Index(t.Text) < 0 {
			return nil, fmt.Errorf("Variable %s not found.", strings.ToUpper(t.Text))
		}
		return []string{t.Text}, nil
	}
	for !c.AtEnd() {
		l, err := readSide()
		if err != nil {
			return nil, err
		}
		if !c.AcceptOp("*") {
			return nil, fmt.Errorf("PAIRED statement: expected '*'.")
		}
		r, err := readSide()
		if err != nil {
			return nil, err
		}
		for i := range l {
			li, ri := i, i
			if ri >= len(r) {
				ri = len(r) - 1
			}
			out = append(out, [2]string{l[li], r[ri]})
		}
		if !c.AcceptOp(",") {
			break
		}
	}
	return out, nil
}

type ttSummary struct {
	Label string
	Char  bool
	N     float64
	Mean  float64
	Std   float64
	StdEr float64
	Min   float64
	Max   float64
}

func ttStats(label string, x []float64) ttSummary {
	st := ttSummary{Label: label}
	st.N = float64(len(x))
	if st.N == 0 {
		return st
	}
	st.Mean = Mean(x)
	st.Std = StdDev(x)
	st.StdEr = st.Std / math.Sqrt(st.N)
	sorted := Sorted(x)
	st.Min, st.Max = sorted[0], sorted[len(sorted)-1]
	return st
}

// ttAddStatRows appends Statistics/ConfLimits rows for a single sample.
func ttAddStatRows(statTbl, confTbl *Table, st ttSummary, df float64, alpha float64) {
	statTbl.AddRow(st.Label, st.N, st.Mean, st.Std, st.StdEr, st.Min, st.Max)
	tc := TInv(1-alpha/2, df)
	lcl := st.Mean - tc*st.StdEr
	ucl := st.Mean + tc*st.StdEr
	chiL := ChiSqInv(1-alpha/2, df)
	chiU := ChiSqInv(alpha/2, df)
	sdl := math.Sqrt((df * st.Std * st.Std) / chiL)
	sdu := math.Sqrt((df * st.Std * st.Std) / chiU)
	confTbl.AddRow(st.Label, st.Mean, lcl, ucl, st.Std, sdl, sdu)
}

func ttTableHeaders() (*Table, *Table, *Table, *Table) {
	st := &Table{ODSName: "Statistics", MissDot: true}
	st.Cols = []Col{
		{Name: "Variable", Label: "Variable", Char: true},
		{Name: "N", Label: "N", Format: "8."},
		{Name: "Mean", Label: "Mean", Format: "10.6"},
		{Name: "StdDev", Label: "Std Dev", Format: "10.6"},
		{Name: "StdErr", Label: "Std Err", Format: "10.6"},
		{Name: "Minimum", Label: "Minimum", Format: "10.6"},
		{Name: "Maximum", Label: "Maximum", Format: "10.6"},
	}
	cl := &Table{ODSName: "ConfLimits", MissDot: true}
	cl.Cols = []Col{
		{Name: "Variable", Label: "Variable", Char: true},
		{Name: "Mean", Label: "Mean", Format: "10.6"},
		{Name: "LowerCLMean", Label: "95% CL\nMean", Format: "10.6"},
		{Name: "UpperCLMean", Label: "95% CL\nMean", Format: "10.6"},
		{Name: "StdDev", Label: "Std Dev", Format: "10.6"},
		{Name: "LowerCLStdDev", Label: "95% CL\nStd Dev", Format: "10.6"},
		{Name: "UpperCLStdDev", Label: "95% CL\nStd Dev", Format: "10.6"},
	}
	tt := &Table{ODSName: "TTests", MissDot: true}
	tt.Cols = []Col{
		{Name: "Variable", Label: "Variable", Char: true},
		{Name: "Method", Label: "Method", Char: true},
		{Name: "Variances", Label: "Variances", Char: true},
		{Name: "DF", Label: "DF", Format: "12.4"},
		{Name: "tValue", Label: "t Value", Format: "6.2"},
		{Name: "Probt", Label: "Pr > |t|", Format: "PVALUE6.4"},
	}
	eq := &Table{ODSName: "Equality", MissDot: true}
	eq.Cols = []Col{
		{Name: "Variable", Label: "Variable", Char: true},
		{Name: "Method", Label: "Method", Char: true},
		{Name: "NumDF", Label: "Num DF", Format: "3."},
		{Name: "DenDF", Label: "Den DF", Format: "3."},
		{Name: "FValue", Label: "F Value", Format: "6.2"},
		{Name: "ProbF", Label: "Pr > F", Format: "PVALUE6.4"},
	}
	return st, cl, tt, eq
}

func ttOneSample(s *Session, d *Dataset, v *Var, h0, alpha float64, sides string, showPlots bool) {
	ci := d.Index(v.Name)
	var x []float64
	for _, row := range d.Rows {
		f := row[ci].AsNum()
		if !math.IsNaN(f) {
			x = append(x, f)
		}
	}
	st := ttStats(v.Name, x)
	statTbl, confTbl, ttTbl, _ := ttTableHeaders()
	statTbl.Caption = "The TTEST Procedure\nVariable: " + v.Name
	ttAddStatRows(statTbl, confTbl, st, st.N-1, alpha)
	t := (st.Mean - h0) / st.StdEr
	df := st.N - 1
	pr := ttPValue(t, df, sides)
	ttTbl.Cols[3].Label = ttSidesLabel(sides)
	ttTbl.AddRow(v.Name, "t Test", "Mean", df, t, pr)
	s.Out.AddTable(statTbl)
	s.Out.AddTable(confTbl)
	s.Out.AddTable(ttTbl)
	if showPlots && len(x) > 1 {
		ttSummaryPanel(s, v.Name, x, h0)
		s.Out.AddGraph(ttQQ(v.Name, x, Mean(x), StdDev(x), PaletteColor(0)))
	}
}

func ttSidesLabel(sides string) string {
	switch sides {
	case "U":
		return "Pr > t"
	case "L":
		return "Pr < t"
	}
	return "Pr > |t|"
}

func ttPValue(t, df float64, sides string) float64 {
	switch sides {
	case "U":
		return TSF(t, df)
	case "L":
		return TCDF(t, df)
	}
	return TTwoSided(t, df)
}

func ttTwoSample(s *Session, d *Dataset, v *Var, ci int, cv *Var, h0, alpha float64, sides string, showPlots bool, tostLo, tostHi float64) error {
	levels := UniqueSorted(d.Col(ci))
	if len(levels) != 2 {
		return fmt.Errorf("CLASS variable %s must have exactly two levels.", cv.Name)
	}
	var xa, xb []float64
	for _, row := range d.Rows {
		y := row[d.Index(v.Name)].AsNum()
		if math.IsNaN(y) {
			continue
		}
		if Equal(row[ci], levels[0]) {
			xa = append(xa, y)
		} else {
			xb = append(xb, y)
		}
	}
	sa, sb := ttStats(FormatValue(levels[0], nil), xa), ttStats(FormatValue(levels[1], nil), xb)
	diff := sa.Mean - sb.Mean
	vp := ((sa.N-1)*sa.Std*sa.Std + (sb.N-1)*sb.Std*sb.Std) / (sa.N + sb.N - 2)
	sp := math.Sqrt(vp)
	sePool := sp * math.Sqrt(1/sa.N+1/sb.N)
	v1 := sa.Std * sa.Std / sa.N
	v2 := sb.Std * sb.Std / sb.N
	seSat := math.Sqrt(v1 + v2)
	dfSat := (v1 + v2) * (v1 + v2) / (v1*v1/(sa.N-1) + v2*v2/(sb.N-1))

	statTbl, confTbl, ttTbl, eqTbl := ttTableHeaders()
	statTbl.Caption = fmt.Sprintf("The TTEST Procedure\nVariable: %s (%s by %s)", v.Name, v.Name, cv.Name)
	ttAddStatRows(statTbl, confTbl, sa, sa.N-1, alpha)
	ttAddStatRows(statTbl, confTbl, sb, sb.N-1, alpha)
	// diff rows
	statTbl.AddRow("Diff (1-2)", sa.N+sb.N, diff, sePool, Missing, Missing, Missing)
	confTbl.AddRow("Diff (1-2) Pooled", diff, diff-TInv(1-alpha/2, sa.N+sb.N-2)*sePool, diff+TInv(1-alpha/2, sa.N+sb.N-2)*sePool, sp, math.NaN(), math.NaN())
	confTbl.AddRow("Diff (1-2) Satterthwaite", diff, diff-TInv(1-alpha/2, dfSat)*seSat, diff+TInv(1-alpha/2, dfSat)*seSat, math.NaN(), math.NaN(), math.NaN())

	tPool := (diff - h0) / sePool
	tSat := (diff - h0) / seSat
	ttTbl.AddRow(v.Name, "Pooled", "Equal", sa.N+sb.N-2, tPool, ttPValue(tPool, sa.N+sb.N-2, sides))
	ttTbl.AddRow(v.Name, "Satterthwaite", "Unequal", dfSat, tSat, ttPValue(tSat, dfSat, sides))

	f := math.Max(sa.Std*sa.Std, sb.Std*sb.Std) / math.Min(sa.Std*sa.Std, sb.Std*sb.Std)
	numDF, denDF := sa.N-1, sb.N-1
	if sa.Std*sa.Std < sb.Std*sb.Std {
		numDF, denDF = sb.N-1, sa.N-1
	}
	eqTbl.Caption = "Equality of Variances"
	eqTbl.AddRow(v.Name, "Folded F", numDF, denDF, f, math.Min(1, 2*FSF(f, float64(numDF), float64(denDF))))

	s.Out.AddTable(statTbl)
	s.Out.AddTable(confTbl)
	s.Out.AddTable(ttTbl)
	s.Out.AddTable(eqTbl)

	// TOST equivalence: two one-sided tests at the margins, plus the
	// (1-2 alpha) confidence interval that TOST inverts
	if !math.IsNaN(tostLo) {
		dfP := sa.N + sb.N - 2
		rows := []struct {
			name   string
			se, df float64
		}{{"Pooled", sePool, dfP}, {"Satterthwaite", seSat, dfSat}}
		tst := &Table{ODSName: "TOSTTests", Caption: "Equivalence Analysis for Difference: " + v.Name + " (1-2)", MissDot: true}
		tst.Cols = []Col{{Name: "Method", Label: "Method", Char: true},
			{Name: "TLow", Label: "Lower Bound\nt Value", Format: "8.4"}, {Name: "PLow", Label: "Lower Bound\nPr > t", Format: "PVALUE6.4"},
			{Name: "TUp", Label: "Upper Bound\nt Value", Format: "8.4"}, {Name: "PUpp", Label: "Upper Bound\nPr < t", Format: "PVALUE6.4"},
			{Name: "OverallP", Label: "Overall\np Value", Format: "PVALUE6.4"},
			{Name: "LCL", Label: "Lower CL", Format: "8.4"}, {Name: "UCL", Label: "Upper CL", Format: "8.4"}}
		tst.Spans = []Span{{Label: fmt.Sprintf("%g%% Conf Limits for Equivalence", 100*(1-2*alpha)), From: 6, To: 7}}
		for _, rw := range rows {
			tLow := (diff - tostLo) / rw.se
			tUp := (diff - tostHi) / rw.se
			pLow := TSF(tLow, rw.df)
			pUp := TCDF(tUp, rw.df)
			tq := TInv(1-alpha, rw.df)
			tst.AddRow(rw.name, tLow, pLow, tUp, pUp, math.Max(pLow, pUp), diff-tq*rw.se, diff+tq*rw.se)
		}
		s.Out.AddTable(tst)
	}
	if showPlots {
		by := make([]string, len(xa)+len(xb))
		for i := range xa {
			by[i] = sa.Label
		}
		for i := range xb {
			by[len(xa)+i] = sb.Label
		}
		all := append(append([]float64{}, xa...), xb...)
		panel := ttSummaryPanelGrouped(s, v.Name, all, by, sa.Label, sb.Label)
		s.Out.AddGraph(panel)
		s.Out.AddGraph(ttQQ(v.Name, all, Mean(all), StdDev(all), PaletteColor(0)))
	}
	return nil
}

func ttPaired(s *Session, d *Dataset, full *Dataset, pr [2]string, h0, alpha float64, sides string) {
	i1, i2 := full.Index(pr[0]), full.Index(pr[1])
	var diffs []float64
	for _, row := range d.Rows {
		a, b := row[i1].AsNum(), row[i2].AsNum()
		if !math.IsNaN(a) && !math.IsNaN(b) {
			diffs = append(diffs, a-b)
		}
	}
	label := pr[0] + " - " + pr[1]
	st := ttStats(label, diffs)
	statTbl, confTbl, ttTbl, _ := ttTableHeaders()
	statTbl.Caption = "The TTEST Procedure\nVariable: " + label
	ttAddStatRows(statTbl, confTbl, st, st.N-1, alpha)
	t := (st.Mean - h0) / st.StdEr
	df := st.N - 1
	ttTbl.AddRow(label, "Paired", "Difference", df, t, ttPValue(t, df, sides))
	s.Out.AddTable(statTbl)
	s.Out.AddTable(confTbl)
	s.Out.AddTable(ttTbl)
}

// ttSummaryPanel renders SAS's default summary panel (histogram + densities
// + boxplot) for one sample.
func ttSummaryPanel(s *Session, name string, x []float64, h0 float64) {
	s.Out.AddGraph(ttSummaryPanelGrouped(s, name, x, nil, "", ""))
}

func ttSummaryPanelGrouped(s *Session, name string, x []float64, by []string, l1, l2 string) *Graph {
	mean, sd := Mean(x), StdDev(x)
	c := NewChart("Distribution of " + name)
	c.Width, c.Height = 640, 320
	bins := ttBins(x)
	hist := &Bars{Color: PaletteColor(0), Opacity: 0.85}
	total := 0.0
	for _, b := range bins {
		total += b.Count
	}
	for _, b := range bins {
		hist.X0 = append(hist.X0, b.Lo)
		hist.X1 = append(hist.X1, b.Hi)
		hist.Y0 = append(hist.Y0, 0)
		if total > 0 {
			hist.Y1 = append(hist.Y1, b.Count/total)
		} else {
			hist.Y1 = append(hist.Y1, 0)
		}
	}
	c.Layers = append(c.Layers, hist)
	// normal density overlay scaled to proportion
	nd := &Line{Color: "#000000", Width: 1.4}
	lo, hi := minmax(x)
	span := hi - lo
	for i := 0; i <= 100; i++ {
		vx := lo - 0.1*span + float64(i)/100*1.2*span
		nd.X = append(nd.X, vx)
		nd.Y = append(nd.Y, NormPDF((vx-mean)/sd)/sd)
	}
	c.Layers = append(c.Layers, nd)
	if len(by) > 0 && l1 != "" && l2 != "" {
		// box plot at top for each group
		var b1, b2 []float64
		for i, v := range x {
			if by[i] == l1 {
				b1 = append(b1, v)
			} else {
				b2 = append(b2, v)
			}
		}
		bl := ttBoxLayer(b1, 0.75, l1, PaletteColor(0))
		br := ttBoxLayer(b2, 1.25, l2, PaletteColor(1))
		c.Layers = append(c.Layers, bl, br)
		c.Legend = []LegendEntry{
			{Label: l1, Kind: "fill", Color: PaletteColor(0)},
			{Label: l2, Kind: "fill", Color: PaletteColor(1)},
		}
	} else {
		c.Layers = append(c.Layers, ttBoxLayer(x, 1.0, name, PaletteColor(0)))
	}
	c.Legend = append(c.Legend, LegendEntry{Label: "Normal", Kind: "line", Color: "#000000"})
	c.XLabel = name
	c.YMin, c.YMax = 0, math.NaN()
	return &Graph{ODSName: "SummaryPanel", SVG: c.SVG()}
}

type ttBin struct {
	Lo, Hi, Count float64
}

// ttBins computes SGPLOT-style default bins (Terrell-Scott 'nice' width).
func ttBins(x []float64) []ttBin {
	sorted := Sorted(x)
	if len(sorted) < 2 {
		return nil
	}
	lo, hi := sorted[0], sorted[len(sorted)-1]
	if hi == lo {
		lo -= 0.5
		hi += 0.5
	}
	n := float64(len(sorted))
	width := niceNum(3.5*StdDev(x)/math.Pow(n, 1.0/3), true)
	if width <= 0 {
		width = niceNum((hi-lo)/10, true)
	}
	start := math.Floor(lo/width) * width
	var out []ttBin
	for b := start; b < hi+width; b += width {
		lo2, hi2 := b, b+width
		cnt := 0.0
		for _, v := range sorted {
			if v >= lo2 && (v < hi2 || (hi2 == b+width && v == sorted[len(sorted)-1] && v <= hi2)) {
				cnt++
			}
		}
		if cnt > 0 {
			out = append(out, ttBin{lo2, hi2, cnt})
		}
	}
	return out
}

// ttBoxLayer returns a horizontal box plot layer at the given row position.
func ttBoxLayer(x []float64, rowPos float64, label string, color string) *Boxes {
	if len(x) == 0 {
		return &Boxes{}
	}
	sorted := Sorted(x)
	q1 := Percentile(sorted, 25)
	med := Percentile(sorted, 50)
	q3 := Percentile(sorted, 75)
	iqr := q3 - q1
	loLim, hiLim := q1-1.5*iqr, q3+1.5*iqr
	wlo, whi := sorted[0], sorted[len(sorted)-1]
	var outs []float64
	for _, v := range sorted {
		if v < loLim || v > hiLim {
			outs = append(outs, v)
		}
	}
	for _, v := range sorted {
		if v >= loLim && v <= hiLim {
			wlo, whi = v, v
		}
	}
	// whiskers: extreme obs within limits
	wlo = math.NaN()
	whi = math.NaN()
	for _, v := range sorted {
		if v >= loLim {
			wlo = v
			break
		}
	}
	for i := len(sorted) - 1; i >= 0; i-- {
		if sorted[i] <= hiLim {
			whi = sorted[i]
			break
		}
	}
	return &Boxes{Boxes: []BoxStat{{
		Pos: rowPos, Q1: q1, Median: med, Q3: q3, Mean: Mean(x),
		WhiskLo: wlo, WhiskHi: whi, Outliers: outs, Width: 0.4, Horizontal: true,
	}}, Color: color}
}

// ttQQ renders a normal Q-Q plot (SAS plotting positions).
func ttQQ(name string, x []float64, mu, sigma float64, color string) *Graph {
	sorted := Sorted(x)
	n := float64(len(sorted))
	q := &Scatter{Color: color, Marker: "circlefilled"}
	ref := &Line{Color: "#000000", Dash: true}
	for i, v := range sorted {
		p := (float64(i+1) - 0.375) / (n + 0.25)
		z := NormInv(p)
		q.X = append(q.X, z*sigma+mu)
		q.Y = append(q.Y, v)
		ref.X = append(ref.X, z*sigma+mu)
		ref.Y = append(ref.Y, z*sigma+mu)
	}
	c := NewChart("Q-Q Plot for " + name)
	c.Width, c.Height = 480, 480
	c.Layers = []Layer{ref, q}
	c.XLabel = "Normal Quantiles"
	c.YLabel = name
	return &Graph{ODSName: "QQPlot", SVG: c.SVG()}
}
