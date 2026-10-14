package sas

import (
	"fmt"
	"strings"
)

// PROC SGPANEL, SGSCATTER, PLOT and GPLOT built on the sg/chart machinery.

func init() {
	RegisterProc("SGPANEL", sgRunPanel)
	RegisterProc("SGSCATTER", sgRunScatter)
	RegisterProc("PLOT", sgRunPlotProc)
	RegisterProc("GPLOT", sgRunPlotProc)
}

// sgRunPanel implements PROC SGPANEL: a grid of charts by PANELBY values.
func sgRunPanel(s *Session, p *ProcStep) error {
	d, err := s.ProcData(p)
	if err != nil {
		return err
	}
	pb := p.First("PANELBY")
	if pb == nil {
		return fmt.Errorf("The PANELBY statement is required in PROC SGPANEL.")
	}
	pbMain, pbAfter := SplitSlash(pb.Args())
	names, err := ExpandVarList(d, pbMain)
	if err != nil {
		return err
	}
	idx, err := d.Indices(names)
	if err != nil {
		return err
	}
	o := ParseOpts(pbAfter)
	columns := int(o.Num("COLUMNS", 0))
	rows := int(o.Num("ROWS", 0))
	novarname := o.Has("NOVARNAME")

	var charts []*Chart
	var titles []string
	for _, g := range d.ByGroups(idx) {
		sub := d.Subset(g.Rows)
		var body []*Stmt
		for _, st := range p.Body {
			if st.Keyword() == "PANELBY" {
				continue
			}
			body = append(body, st)
		}
		env, err := sgNewEnv(s, sub, body, p.Opts)
		if err != nil {
			return err
		}
		if len(env.plots) == 0 {
			continue
		}
		env.binRows = d.Rows // UNISCALE default: shared binning
		c, err := env.build(g.Rows)
		if err != nil {
			return err
		}
		// cell title: PanelbyVar=Value
		var parts []string
		for i, n := range names {
			if novarname {
				parts = append(parts, FormatByName(g.Keys[i], d.Var(n).Format))
			} else {
				parts = append(parts, n+"="+FormatByName(g.Keys[i], d.Var(n).Format))
			}
		}
		c.Title = strings.Join(parts, " ")
		charts = append(charts, c)
		titles = append(titles, c.Title)
	}
	if len(charts) == 0 {
		return nil
	}
	nc := columns
	if nc <= 0 {
		if rows > 0 {
			nc = (len(charts) + rows - 1) / rows
		} else {
			nc = 3
			if len(charts) < 3 {
				nc = len(charts)
			}
		}
	}
	s.Out.AddGraph(&Graph{ODSName: "SGPanel", SVG: lmPanel(charts, nc, 500, 375)})
	return nil
}

func orSplit(toks []Token) []Token {
	before, after := SplitSlash(toks)
	_ = after
	return before
}

// sgRunScatter implements PROC SGSCATTER: PLOT (ys)*(xs) and MATRIX vars.
func sgRunScatter(s *Session, p *ProcStep) error {
	d, err := s.ProcData(p)
	if err != nil {
		return err
	}
	st := p.First("PLOT", "COMPARE", "MATRIX")
	if st == nil {
		return fmt.Errorf("PROC SGSCATTER requires a PLOT, COMPARE or MATRIX statement.")
	}
	kw := st.Keyword()
	before, after := SplitSlash(st.Args())
	_ = before
	o := ParseOpts(after)
	marker := sgMarkerName(o)

	var charts []*Chart
	switch kw {
	case "MATRIX":
		names, err := ExpandVarList(d, st.Args())
		if err != nil {
			return err
		}
		if len(names) > 5 {
			names = names[:5]
		}
		n := len(names)
		for r := 0; r < n; r++ {
			for c := 0; c < n; c++ {
				chart, err := sgPairChart(s, d, names[r], names[c], o, marker, true)
				if err != nil {
					return err
				}
				if r == c {
					chart.Title = names[r]
				}
				charts = append(charts, chart)
			}
		}
		s.Out.AddGraph(&Graph{ODSName: "SGScatterMatrix", SVG: lmPanel(charts, n, float64(1000/n+160), float64(1000/n+120))})
	case "COMPARE":
		// X=(...) Y=(...) overlaid in one chart per y
		yLists, xLists, err := sgCompareLists(d, st.Args())
		if err != nil {
			return err
		}
		for _, yl := range yLists {
			c := NewChart("")
			c.XLabel, c.YLabel = strings.Join(xLists, " "), yl[0]
			for xi, xn := range xLists {
				pts := &Scatter{Color: PaletteColor(xi), Marker: marker}
				for _, row := range d.Rows {
					xv, yv := row[d.Index(xn)].AsNum(), row[d.Index(yl[0])].AsNum()
					if !isNumNaN(xv) && !isNumNaN(yv) {
						pts.X = append(pts.X, xv)
						pts.Y = append(pts.Y, yv)
					}
				}
				c.Layers = append(c.Layers, pts)
				c.Legend = append(c.Legend, LegendEntry{Label: yl[0] + "*" + xn, Kind: "marker", Color: PaletteColor(xi), Marker: "circle"})
			}
			charts = append(charts, c)
		}
		s.Out.AddGraph(&Graph{ODSName: "SGScatter", SVG: lmPanel(charts, len(charts), 480, 400)})
	default: // PLOT (ys)*(xs) — one chart per pair
		ys, xs, err := sgCrossLists(d, st.Args())
		if err != nil {
			return err
		}
		for _, yp := range ys {
			for _, xp := range xs {
				chart, err := sgPairChart(s, d, yp, xp, o, marker, false)
				if err != nil {
					return err
				}
				charts = append(charts, chart)
			}
		}
		s.Out.AddGraph(&Graph{ODSName: "SGScatter", SVG: lmPanel(charts, len(xs), 480, 400)})
	}
	return nil
}

func isNumNaN(f float64) bool { return f != f }

func sgMarkerName(o *Opts) string {
	if sub, ok := o.Get("MARKERATTRS"); ok {
		inner := ParseOpts(sub.Inner())
		if sym, ok := inner.Get("SYMBOL"); ok {
			return strings.ToLower(sym.Str())
		}
	}
	return "circle"
}

// sgCrossLists parses "(y1 y2)*(x1 x2)" into paired variable lists.
func sgCrossLists(d *Dataset, toks []Token) ([]string, []string, error) {
	star := -1
	depth := 0
	for i, t := range toks {
		switch {
		case t.IsOp("("):
			depth++
		case t.IsOp(")"):
			depth--
		case t.IsOp("*") && depth == 0:
			star = i
		}
	}
	if star < 0 {
		// y*x simple
		names := VarNames(toks)
		if len(names) == 2 {
			return []string{names[0]}, []string{names[1]}, nil
		}
		return nil, nil, fmt.Errorf("Syntax error in plot request.")
	}
	ys, err := ExpandVarList(d, groupNames(d, toks[:star]))
	if err != nil {
		return nil, nil, err
	}
	xs, err := ExpandVarList(d, groupNames(d, toks[star+1:]))
	if err != nil {
		return nil, nil, err
	}
	return ys, xs, nil
}

// groupNames strips outer parens from an item.
func groupNames(d *Dataset, toks []Token) []Token {
	c := NewCursor(toks)
	if g, ok := c.Group(); ok && c.AtEnd() {
		return g
	}
	return toks
}

func sgCompareLists(d *Dataset, toks []Token) ([][]string, []string, error) {
	var ys, xs []string
	c := NewCursor(toks)
	for !c.AtEnd() {
		t := c.Next()
		if !c.AcceptOp("=") {
			continue
		}
		if c.Peek().IsOp("(") {
			g, _ := c.Group()
			names, err := ExpandVarList(d, g)
			if err != nil {
				return nil, nil, err
			}
			if strings.EqualFold(t.Text, "Y") {
				ys = names
			} else {
				xs = names
			}
		} else {
			n := c.Peek().Text
			c.Next()
			if strings.EqualFold(t.Text, "Y") {
				ys = []string{n}
			} else {
				xs = []string{n}
			}
		}
	}
	if len(ys) == 0 || len(xs) == 0 {
		return nil, nil, fmt.Errorf("COMPARE requires X= and Y= lists.")
	}
	// one chart per y with all x overlaid
	out := make([][]string, len(ys))
	for i := range ys {
		out[i] = []string{ys[i]}
	}
	return out, xs, nil
}

// sgPairChart renders one scatter (or diagonal histogram for matrices).
func sgPairChart(s *Session, d *Dataset, yname, xname string, o *Opts, marker string, matrix bool) (*Chart, error) {
	yi, yv, err := sgVarRef(d, yname)
	if err != nil {
		return nil, err
	}
	xi, xv, err := sgVarRef(d, xname)
	if err != nil {
		return nil, err
	}
	c := NewChart("")
	c.XLabel = xv.DisplayLabel()
	c.YLabel = yv.DisplayLabel()
	group := -1
	if gop, ok := o.Get("GROUP"); ok && gop.Eq {
		group = d.Index(gop.Str())
	}
	colors := map[string]int{}
	pts := &Scatter{Marker: marker}
	if matrix && yname == xname {
		vals := NonMissing(d.NumCol(yi))
		sorted := Sorted(vals)
		bins := sgBins(sorted, 0, 0, 0)
		cnt := bins.Counts(vals, nil)
		hist := &Bars{Color: PaletteColor(0), Opacity: 0.9}
		total := 0.0
		for _, v := range cnt {
			total += v
		}
		for i := range cnt {
			lo := bins.Start + float64(i)*bins.W
			hist.X0 = append(hist.X0, lo)
			hist.X1 = append(hist.X1, lo+bins.W)
			hist.Y0 = append(hist.Y0, 0)
			if total > 0 {
				hist.Y1 = append(hist.Y1, cnt[i]/total)
			} else {
				hist.Y1 = append(hist.Y1, 0)
			}
		}
		c.Layers = []Layer{hist}
		c.YTickFmt = func(f float64) string { return FormatTick(f * 100) }
		return c, nil
	}
	for _, row := range d.Rows {
		x, y := row[xi].AsNum(), row[yi].AsNum()
		if isNumNaN(x) || isNumNaN(y) {
			continue
		}
		col := PaletteColor(0)
		if group >= 0 {
			k := FormatValue(row[group], nil)
			ci, seen := colors[k]
			if !seen {
				ci = len(colors)
				colors[k] = ci
				c.Legend = append(c.Legend, LegendEntry{Label: k, Kind: "marker", Color: PaletteColor(ci), Marker: "circle"})
			}
			col = PaletteColor(ci)
		}
		pts.X = append(pts.X, x)
		pts.Y = append(pts.Y, y)
		pts.Color = col
	}
	c.Layers = []Layer{pts}
	return c, nil
}

// sgRunPlotProc implements PROC PLOT and GPLOT: scatter plots of y*x lists.
func sgRunPlotProc(s *Session, p *ProcStep) error {
	d, err := s.ProcData(p)
	if err != nil {
		return err
	}
	plots := p.Stmts("PLOT")
	if len(plots) == 0 {
		return fmt.Errorf("no PLOT statement.")
	}
	var charts []*Chart
	for _, st := range plots {
		main, after := SplitSlash(st.Args())
		o := ParseOpts(after)
		// requests separated by spaces at top level: y*x y2*x2, y*(x1 x2)
		for _, req := range sgSplitRequests(main) {
			ys, xs, err := sgCrossLists(d, req)
			if err != nil {
				return err
			}
			overlay := o.Has("OVERLAY") || (p.Name == "GPLOT" && len(ys)*len(xs) > 1 && !o.Has("NOOVERLAY"))
			if overlay {
				c := NewChart("")
				nc := 0
				for _, yp := range ys {
					for _, xp := range xs {
						yi, _, _ := sgVarRef(d, yp)
						xi, xv, _ := sgVarRef(d, xp)
						pts := &Scatter{Color: PaletteColor(nc), Marker: Markers[nc%len(Markers)]}
						for _, row := range d.Rows {
							a, b := row[xi].AsNum(), row[yi].AsNum()
							if !isNumNaN(a) && !isNumNaN(b) {
								pts.X = append(pts.X, a)
								pts.Y = append(pts.Y, b)
							}
						}
						c.Layers = append(c.Layers, pts)
						c.Legend = append(c.Legend, LegendEntry{Label: yp + "*" + xp, Kind: "marker", Color: PaletteColor(nc), Marker: Markers[nc%len(Markers)]})
						nc++
						if c.XLabel == "" {
							c.XLabel = xv.DisplayLabel()
						}
					}
					if c.YLabel == "" {
						if yi, yv, err := sgVarRef(d, yp); err == nil {
							_ = yi
							c.YLabel = yv.DisplayLabel()
						}
					}
				}
				charts = append(charts, c)
				continue
			}
			for _, yp := range ys {
				for _, xp := range xs {
					chart, err := sgPairChart(s, d, yp, xp, o, "x", false)
					if err != nil {
						return err
					}
					charts = append(charts, chart)
				}
			}
		}
	}
	for _, c := range charts {
		s.Out.AddGraph(&Graph{ODSName: "Plot", SVG: c.SVG()})
	}
	return nil
}

// sgSplitRequests splits "y*x z*w 'sym'" style requests at top level.
func sgSplitRequests(toks []Token) [][]Token {
	var out [][]Token
	depth := 0
	cur := []Token{}
	for _, t := range toks {
		switch {
		case t.IsOp("("):
			depth++
			cur = append(cur, t)
		case t.IsOp(")"):
			depth--
			cur = append(cur, t)
		case t.IsOp("=") && depth == 0:
			// symbol assignment: consume next token
			cur = append(cur, t)
		case t.Kind == TString && depth == 0 && len(cur) > 0:
			cur = append(cur, t)
		case t.Kind == TIdent && depth == 0 && len(cur) > 0 && !wasStarOrEq(cur):
			out = append(out, cur)
			cur = []Token{t}
		default:
			cur = append(cur, t)
		}
	}
	if len(cur) > 0 {
		out = append(out, cur)
	}
	return out
}

func wasStarOrEq(cur []Token) bool {
	if len(cur) == 0 {
		return false
	}
	last := cur[len(cur)-1]
	return last.IsOp("*", "=")
}
