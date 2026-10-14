package sas

import (
	"fmt"
	"html"
	"math"
	"strconv"
	"strings"
)

// Palette approximates the SAS HTMLBlue style data colors.
var Palette = []string{"#445694", "#A23A2E", "#01665E", "#543005", "#9D3CDB", "#7F8E1F", "#2597FA", "#B26084", "#D1A97C", "#47A82A", "#E1A11B", "#AF8DB8"}

// PaletteColor returns a palette color (wraps around); i<0 gives the default.
func PaletteColor(i int) string {
	if i < 0 {
		i = 0
	}
	return Palette[i%len(Palette)]
}

// Markers cycle per group like SAS (circle, plus, x, triangle, square, ...).
var Markers = []string{"circle", "plus", "x", "triangle", "square", "asterisk", "diamond", "circlefilled", "trianglefilled", "squarefilled"}

// Chart is a 2-D chart rendered to SVG.
type Chart struct {
	Title          string
	Width, Height  float64 // default 640 x 480
	XLabel, YLabel string
	XCats, YCats   []string // categorical axes (positions 0..n-1)
	XMin, XMax     float64  // NaN = automatic
	YMin, YMax     float64
	XLog, YLog     bool
	Layers         []Layer
	Legend         []LegendEntry
	LegendTitle    string
	NoGrid         bool
	XTickFmt       func(float64) string
	YTickFmt       func(float64) string
	XValues        []float64 // explicit tick values
	YValues        []float64
	// Insets are text lines drawn in the top-right of the plot area (e.g. fit statistics).
	Inset []string
}

// NewChart returns a chart with automatic axis ranges.
func NewChart(title string) *Chart {
	return &Chart{Title: title, Width: 640, Height: 480, XMin: math.NaN(), XMax: math.NaN(), YMin: math.NaN(), YMax: math.NaN()}
}

// LegendEntry is a legend item. Kind: "marker", "line", "fill".
type LegendEntry struct {
	Label  string
	Color  string
	Kind   string
	Marker string
	Dash   bool
}

// Layer is a drawable element in data coordinates.
type Layer interface {
	bounds() (xmin, xmax, ymin, ymax float64)
	draw(cv *canvas)
}

// Scatter draws markers.
type Scatter struct {
	X, Y   []float64
	Color  string
	Marker string // circle, circlefilled, square, squarefilled, triangle, trianglefilled, diamond, x, plus, asterisk
	Size   float64
	Labels []string // optional data labels
}

// Line draws a polyline (NaN breaks the line).
type Line struct {
	X, Y  []float64
	Color string
	Width float64
	Dash  bool
}

// Bars draws rectangles [X0,X1] x [Y0,Y1].
type Bars struct {
	X0, X1, Y0, Y1 []float64
	Color          string
	Opacity        float64
	Outline        string
	Labels         []string // optional labels drawn above bars
}

// Band draws a filled area between Lo and Hi along X.
type Band struct {
	X, Lo, Hi []float64
	Color     string
	Opacity   float64
}

// BoxStat holds box plot statistics at a position.
type BoxStat struct {
	Pos                  float64
	Q1, Median, Q3, Mean float64
	WhiskLo, WhiskHi     float64
	Outliers             []float64
	Width                float64 // in data units (default 0.5)
	Horizontal           bool
}

// Boxes draws box plots.
type Boxes struct {
	Boxes []BoxStat
	Color string
}

// RefLine draws a reference line at Value on axis 'x' or 'y'.
type RefLine struct {
	Axis  byte
	Value float64
	Label string
	Color string
	Dash  bool
}

// TextLabels draws text at data coordinates.
type TextLabels struct {
	X, Y  []float64
	Text  []string
	Color string
}

func minmax(xs ...[]float64) (float64, float64) {
	lo, hi := math.Inf(1), math.Inf(-1)
	for _, x := range xs {
		for _, v := range x {
			if math.IsNaN(v) || math.IsInf(v, 0) {
				continue
			}
			lo, hi = math.Min(lo, v), math.Max(hi, v)
		}
	}
	return lo, hi
}

func (l *Scatter) bounds() (float64, float64, float64, float64) {
	x0, x1 := minmax(l.X)
	y0, y1 := minmax(l.Y)
	return x0, x1, y0, y1
}
func (l *Line) bounds() (float64, float64, float64, float64) {
	x0, x1 := minmax(l.X)
	y0, y1 := minmax(l.Y)
	return x0, x1, y0, y1
}
func (l *Bars) bounds() (float64, float64, float64, float64) {
	x0, x1 := minmax(l.X0, l.X1)
	y0, y1 := minmax(l.Y0, l.Y1)
	return x0, x1, y0, y1
}
func (l *Band) bounds() (float64, float64, float64, float64) {
	x0, x1 := minmax(l.X)
	y0, y1 := minmax(l.Lo, l.Hi)
	return x0, x1, y0, y1
}
func (l *Boxes) bounds() (float64, float64, float64, float64) {
	var pos, vals []float64
	for _, b := range l.Boxes {
		w := b.Width
		if w == 0 {
			w = 0.5
		}
		pos = append(pos, b.Pos-w/2, b.Pos+w/2)
		vals = append(vals, b.WhiskLo, b.WhiskHi, b.Q1, b.Q3, b.Mean)
		vals = append(vals, b.Outliers...)
	}
	p0, p1 := minmax(pos)
	v0, v1 := minmax(vals)
	if len(l.Boxes) > 0 && l.Boxes[0].Horizontal {
		return v0, v1, p0, p1
	}
	return p0, p1, v0, v1
}
func (l *RefLine) bounds() (float64, float64, float64, float64) {
	n := math.NaN()
	if l.Axis == 'x' {
		return l.Value, l.Value, n, n
	}
	return n, n, l.Value, l.Value
}
func (l *TextLabels) bounds() (float64, float64, float64, float64) {
	x0, x1 := minmax(l.X)
	y0, y1 := minmax(l.Y)
	return x0, x1, y0, y1
}

// ---------------------------------------------------------------- canvas

type canvas struct {
	b                  strings.Builder
	px0, px1, py0, py1 float64 // plot area pixels (py0 top)
	x0, x1, y0, y1     float64 // data range
	xlog, ylog         bool
}

func (cv *canvas) sx(x float64) float64 {
	if cv.xlog {
		x, cv.x0, cv.x1 = math.Log10(x), math.Log10(cv.x0), math.Log10(cv.x1)
		defer func() { cv.x0, cv.x1 = math.Pow(10, cv.x0), math.Pow(10, cv.x1) }()
	}
	return cv.px0 + (x-cv.x0)/(cv.x1-cv.x0)*(cv.px1-cv.px0)
}
func (cv *canvas) sy(y float64) float64 {
	if cv.ylog {
		y, cv.y0, cv.y1 = math.Log10(y), math.Log10(cv.y0), math.Log10(cv.y1)
		defer func() { cv.y0, cv.y1 = math.Pow(10, cv.y0), math.Pow(10, cv.y1) }()
	}
	return cv.py1 - (y-cv.y0)/(cv.y1-cv.y0)*(cv.py1-cv.py0)
}

func (cv *canvas) printf(f string, a ...any) { fmt.Fprintf(&cv.b, f, a...) }

func f2(x float64) string { return strconv.FormatFloat(x, 'f', 2, 64) }

func drawMarker(cv *canvas, x, y float64, marker, color string, size float64) {
	r := size / 2
	if r == 0 {
		r = 3.5
	}
	fill := "none"
	m := strings.ToLower(marker)
	if strings.HasSuffix(m, "filled") {
		fill = color
		m = strings.TrimSuffix(m, "filled")
	}
	switch m {
	case "square":
		cv.printf(`<rect x="%s" y="%s" width="%s" height="%s" fill="%s" stroke="%s"/>`, f2(x-r), f2(y-r), f2(2*r), f2(2*r), fill, color)
	case "triangle":
		cv.printf(`<path d="M%s %sL%s %sL%s %sZ" fill="%s" stroke="%s"/>`, f2(x), f2(y-r), f2(x+r), f2(y+r), f2(x-r), f2(y+r), fill, color)
	case "diamond":
		cv.printf(`<path d="M%s %sL%s %sL%s %sL%s %sZ" fill="%s" stroke="%s"/>`, f2(x), f2(y-r), f2(x+r), f2(y), f2(x), f2(y+r), f2(x-r), f2(y), fill, color)
	case "x":
		cv.printf(`<path d="M%s %sL%s %sM%s %sL%s %s" stroke="%s"/>`, f2(x-r), f2(y-r), f2(x+r), f2(y+r), f2(x-r), f2(y+r), f2(x+r), f2(y-r), color)
	case "plus":
		cv.printf(`<path d="M%s %sL%s %sM%s %sL%s %s" stroke="%s"/>`, f2(x-r), f2(y), f2(x+r), f2(y), f2(x), f2(y-r), f2(x), f2(y+r), color)
	case "asterisk", "star":
		cv.printf(`<path d="M%s %sL%s %sM%s %sL%s %sM%s %sL%s %s" stroke="%s"/>`, f2(x-r), f2(y), f2(x+r), f2(y), f2(x-r*0.6), f2(y-r*0.8), f2(x+r*0.6), f2(y+r*0.8), f2(x-r*0.6), f2(y+r*0.8), f2(x+r*0.6), f2(y-r*0.8), color)
	default:
		cv.printf(`<circle cx="%s" cy="%s" r="%s" fill="%s" stroke="%s"/>`, f2(x), f2(y), f2(r), fill, color)
	}
}

func (l *Scatter) draw(cv *canvas) {
	color := l.Color
	if color == "" {
		color = Palette[0]
	}
	for i := range l.X {
		if i >= len(l.Y) || math.IsNaN(l.X[i]) || math.IsNaN(l.Y[i]) {
			continue
		}
		x, y := cv.sx(l.X[i]), cv.sy(l.Y[i])
		drawMarker(cv, x, y, l.Marker, color, l.Size)
		if i < len(l.Labels) && l.Labels[i] != "" {
			cv.printf(`<text x="%s" y="%s" font-size="10" fill="#333">%s</text>`, f2(x+5), f2(y-4), html.EscapeString(l.Labels[i]))
		}
	}
}

func (l *Line) draw(cv *canvas) {
	color := l.Color
	if color == "" {
		color = Palette[0]
	}
	w := l.Width
	if w == 0 {
		w = 1.6
	}
	dash := ""
	if l.Dash {
		dash = ` stroke-dasharray="6,4"`
	}
	var d strings.Builder
	pen := false
	for i := range l.X {
		if i >= len(l.Y) || math.IsNaN(l.X[i]) || math.IsNaN(l.Y[i]) {
			pen = false
			continue
		}
		if pen {
			d.WriteString("L")
		} else {
			d.WriteString("M")
			pen = true
		}
		d.WriteString(f2(cv.sx(l.X[i])) + " " + f2(cv.sy(l.Y[i])))
	}
	cv.printf(`<path d="%s" fill="none" stroke="%s" stroke-width="%s"%s/>`, d.String(), color, f2(w), dash)
}

func (l *Bars) draw(cv *canvas) {
	color := l.Color
	if color == "" {
		color = Palette[0]
	}
	op := l.Opacity
	if op == 0 {
		op = 1
	}
	outline := l.Outline
	if outline == "" {
		outline = "#333333"
	}
	for i := range l.X0 {
		xa, xb := cv.sx(l.X0[i]), cv.sx(l.X1[i])
		ya, yb := cv.sy(l.Y0[i]), cv.sy(l.Y1[i])
		if math.IsNaN(xa + xb + ya + yb) {
			continue
		}
		x, w := math.Min(xa, xb), math.Abs(xb-xa)
		y, h := math.Min(ya, yb), math.Abs(yb-ya)
		cv.printf(`<rect x="%s" y="%s" width="%s" height="%s" fill="%s" fill-opacity="%s" stroke="%s" stroke-width="0.8"/>`, f2(x), f2(y), f2(w), f2(h), color, f2(op), outline)
		if i < len(l.Labels) && l.Labels[i] != "" {
			cv.printf(`<text x="%s" y="%s" font-size="10" text-anchor="middle" fill="#333">%s</text>`, f2(x+w/2), f2(y-3), html.EscapeString(l.Labels[i]))
		}
	}
}

func (l *Band) draw(cv *canvas) {
	color := l.Color
	if color == "" {
		color = Palette[0]
	}
	op := l.Opacity
	if op == 0 {
		op = 0.25
	}
	var d strings.Builder
	first := true
	for i := range l.X {
		if math.IsNaN(l.Hi[i]) {
			continue
		}
		if first {
			d.WriteString("M")
			first = false
		} else {
			d.WriteString("L")
		}
		d.WriteString(f2(cv.sx(l.X[i])) + " " + f2(cv.sy(l.Hi[i])))
	}
	for i := len(l.X) - 1; i >= 0; i-- {
		if math.IsNaN(l.Lo[i]) {
			continue
		}
		d.WriteString("L" + f2(cv.sx(l.X[i])) + " " + f2(cv.sy(l.Lo[i])))
	}
	d.WriteString("Z")
	cv.printf(`<path d="%s" fill="%s" fill-opacity="%s" stroke="none"/>`, d.String(), color, f2(op))
}

func (l *Boxes) draw(cv *canvas) {
	color := l.Color
	if color == "" {
		color = Palette[0]
	}
	for _, b := range l.Boxes {
		w := b.Width
		if w == 0 {
			w = 0.5
		}
		if b.Horizontal {
			ya, yb := cv.sy(b.Pos-w/2), cv.sy(b.Pos+w/2)
			yc := cv.sy(b.Pos)
			x1, x3 := cv.sx(b.Q1), cv.sx(b.Q3)
			top, h := math.Min(ya, yb), math.Abs(yb-ya)
			cv.printf(`<rect x="%s" y="%s" width="%s" height="%s" fill="%s" fill-opacity="0.25" stroke="%s"/>`, f2(math.Min(x1, x3)), f2(top), f2(math.Abs(x3-x1)), f2(h), color, color)
			cv.printf(`<line x1="%s" y1="%s" x2="%s" y2="%s" stroke="%s" stroke-width="2"/>`, f2(cv.sx(b.Median)), f2(top), f2(cv.sx(b.Median)), f2(top+h), color)
			cv.printf(`<path d="M%s %sL%s %sM%s %sL%s %sM%s %sL%s %sM%s %sL%s %s" stroke="%s"/>`,
				f2(cv.sx(b.WhiskLo)), f2(yc), f2(math.Min(x1, x3)), f2(yc), f2(math.Max(x1, x3)), f2(yc), f2(cv.sx(b.WhiskHi)), f2(yc),
				f2(cv.sx(b.WhiskLo)), f2(top+h*0.25), f2(cv.sx(b.WhiskLo)), f2(top+h*0.75), f2(cv.sx(b.WhiskHi)), f2(top+h*0.25), f2(cv.sx(b.WhiskHi)), f2(top+h*0.75), color)
			if !math.IsNaN(b.Mean) {
				drawMarker(cv, cv.sx(b.Mean), yc, "diamond", color, 8)
			}
			for _, o := range b.Outliers {
				drawMarker(cv, cv.sx(o), yc, "circle", color, 6)
			}
			continue
		}
		xa, xb := cv.sx(b.Pos-w/2), cv.sx(b.Pos+w/2)
		xc := cv.sx(b.Pos)
		y1, y3 := cv.sy(b.Q1), cv.sy(b.Q3)
		left, wd := math.Min(xa, xb), math.Abs(xb-xa)
		cv.printf(`<rect x="%s" y="%s" width="%s" height="%s" fill="%s" fill-opacity="0.25" stroke="%s"/>`, f2(left), f2(math.Min(y1, y3)), f2(wd), f2(math.Abs(y1-y3)), color, color)
		cv.printf(`<line x1="%s" y1="%s" x2="%s" y2="%s" stroke="%s" stroke-width="2"/>`, f2(left), f2(cv.sy(b.Median)), f2(left+wd), f2(cv.sy(b.Median)), color)
		cv.printf(`<path d="M%s %sL%s %sM%s %sL%s %sM%s %sL%s %sM%s %sL%s %s" stroke="%s"/>`,
			f2(xc), f2(cv.sy(b.WhiskLo)), f2(xc), f2(math.Max(y1, y3)), f2(xc), f2(math.Min(y1, y3)), f2(xc), f2(cv.sy(b.WhiskHi)),
			f2(left+wd*0.25), f2(cv.sy(b.WhiskLo)), f2(left+wd*0.75), f2(cv.sy(b.WhiskLo)), f2(left+wd*0.25), f2(cv.sy(b.WhiskHi)), f2(left+wd*0.75), f2(cv.sy(b.WhiskHi)), color)
		if !math.IsNaN(b.Mean) {
			drawMarker(cv, xc, cv.sy(b.Mean), "diamond", color, 8)
		}
		for _, o := range b.Outliers {
			drawMarker(cv, xc, cv.sy(o), "circle", color, 6)
		}
	}
}

func (l *RefLine) draw(cv *canvas) {
	color := l.Color
	if color == "" {
		color = "#666666"
	}
	dash := ` stroke-dasharray="5,4"`
	if !l.Dash && l.Color != "" {
		dash = ""
	}
	if l.Axis == 'x' {
		x := cv.sx(l.Value)
		cv.printf(`<line x1="%s" y1="%s" x2="%s" y2="%s" stroke="%s"%s/>`, f2(x), f2(cv.py0), f2(x), f2(cv.py1), color, dash)
		if l.Label != "" {
			cv.printf(`<text x="%s" y="%s" font-size="10" fill="%s">%s</text>`, f2(x+3), f2(cv.py0+12), color, html.EscapeString(l.Label))
		}
		return
	}
	y := cv.sy(l.Value)
	cv.printf(`<line x1="%s" y1="%s" x2="%s" y2="%s" stroke="%s"%s/>`, f2(cv.px0), f2(y), f2(cv.px1), f2(y), color, dash)
	if l.Label != "" {
		cv.printf(`<text x="%s" y="%s" font-size="10" text-anchor="end" fill="%s">%s</text>`, f2(cv.px1-3), f2(y-3), color, html.EscapeString(l.Label))
	}
}

func (l *TextLabels) draw(cv *canvas) {
	color := l.Color
	if color == "" {
		color = "#333333"
	}
	for i := range l.X {
		if i < len(l.Text) {
			cv.printf(`<text x="%s" y="%s" font-size="10" text-anchor="middle" fill="%s">%s</text>`, f2(cv.sx(l.X[i])), f2(cv.sy(l.Y[i])), color, html.EscapeString(l.Text[i]))
		}
	}
}

// ---------------------------------------------------------------- ticks

// NiceTicks returns "nice" tick values covering [lo, hi] (about n ticks).
func NiceTicks(lo, hi float64, n int) []float64 {
	if math.IsNaN(lo) || math.IsNaN(hi) || math.IsInf(lo, 0) || math.IsInf(hi, 0) {
		return nil
	}
	if hi == lo {
		d := math.Max(math.Abs(lo)*0.1, 1)
		lo, hi = lo-d, hi+d
	}
	step := niceNum((hi-lo)/float64(max(n-1, 1)), true)
	start := math.Floor(lo/step+1e-9) * step
	var out []float64
	for v := start; v <= hi+step*1e-6; v += step {
		if v >= lo-step*1e-6 {
			out = append(out, cleanFloat(v, step))
		}
	}
	return out
}

func cleanFloat(v, step float64) float64 {
	dec := 0
	if step < 1 {
		dec = int(math.Ceil(-math.Log10(step))) + 1
	}
	p := math.Pow(10, float64(dec))
	return math.Round(v*p) / p
}

func niceNum(x float64, round bool) float64 {
	if x <= 0 {
		return 1
	}
	exp := math.Floor(math.Log10(x))
	f := x / math.Pow(10, exp)
	var nf float64
	if round {
		switch {
		case f < 1.5:
			nf = 1
		case f < 3:
			nf = 2
		case f < 7:
			nf = 5
		default:
			nf = 10
		}
	} else {
		switch {
		case f <= 1:
			nf = 1
		case f <= 2:
			nf = 2
		case f <= 5:
			nf = 5
		default:
			nf = 10
		}
	}
	return nf * math.Pow(10, exp)
}

// FormatTick renders a tick value compactly.
func FormatTick(v float64) string {
	if math.Abs(v) < 1e-12 {
		return "0"
	}
	s := strconv.FormatFloat(v, 'f', -1, 64)
	if len(s) > 10 {
		s = strconv.FormatFloat(v, 'g', 6, 64)
	}
	return s
}

// ---------------------------------------------------------------- render

// SVG renders the chart.
func (c *Chart) SVG() string {
	W, H := c.Width, c.Height
	if W == 0 {
		W = 640
	}
	if H == 0 {
		H = 480
	}
	// data bounds
	x0, x1, y0, y1 := math.Inf(1), math.Inf(-1), math.Inf(1), math.Inf(-1)
	hasBars := false
	for _, l := range c.Layers {
		a, b, cc, d := l.bounds()
		if !math.IsNaN(a) {
			x0, x1 = math.Min(x0, a), math.Max(x1, b)
		}
		if !math.IsNaN(cc) {
			y0, y1 = math.Min(y0, cc), math.Max(y1, d)
		}
		if _, ok := l.(*Bars); ok {
			hasBars = true
		}
	}
	if math.IsInf(x0, 0) {
		x0, x1 = 0, 1
	}
	if math.IsInf(y0, 0) {
		y0, y1 = 0, 1
	}
	var xt, yt []float64
	if len(c.XCats) > 0 {
		x0, x1 = -0.5, float64(len(c.XCats))-0.5
	} else {
		x0, x1, xt = axisRange(x0, x1, c.XMin, c.XMax, false, c.XValues)
	}
	if len(c.YCats) > 0 {
		y0, y1 = -0.5, float64(len(c.YCats))-0.5
	} else {
		y0, y1, yt = axisRange(y0, y1, c.YMin, c.YMax, hasBars && len(c.XCats) > 0 || hasBars, c.YValues)
	}

	// layout
	top := 16.0
	if c.Title != "" {
		top = 40
	}
	ytf := c.YTickFmt
	if ytf == nil {
		ytf = FormatTick
	}
	xtf := c.XTickFmt
	if xtf == nil {
		xtf = FormatTick
	}
	maxYLab := 0
	if len(c.YCats) > 0 {
		for _, s := range c.YCats {
			maxYLab = max(maxYLab, len(s))
		}
	} else {
		for _, v := range yt {
			maxYLab = max(maxYLab, len(ytf(v)))
		}
	}
	left := 30 + float64(maxYLab)*7
	if c.YLabel != "" {
		left += 18
	}
	rotateX := false
	if len(c.XCats) > 0 {
		maxL := 0
		for _, s := range c.XCats {
			maxL = max(maxL, len(s))
		}
		if float64(maxL*7) > (W-left-20)/float64(len(c.XCats)) {
			rotateX = true
		}
	}
	bottom := 46.0
	if rotateX {
		maxL := 0
		for _, s := range c.XCats {
			maxL = max(maxL, len(s))
		}
		bottom = 30 + math.Min(float64(maxL)*5.5, 120)
	}
	legendH := 0.0
	if len(c.Legend) > 0 {
		legendH = 28
	}
	cv := &canvas{px0: left, px1: W - 20, py0: top, py1: H - bottom - legendH, x0: x0, x1: x1, y0: y0, y1: y1, xlog: c.XLog, ylog: c.YLog}

	cv.printf(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %s %s" width="%s" height="%s" font-family="Arial, Helvetica, sans-serif" font-size="12">`, f2(W), f2(H), f2(W), f2(H))
	cv.printf(`<rect x="0" y="0" width="%s" height="%s" fill="#ffffff"/>`, f2(W), f2(H))
	if c.Title != "" {
		cv.printf(`<text x="%s" y="24" text-anchor="middle" font-size="15" font-weight="bold" fill="#000">%s</text>`, f2(W/2), html.EscapeString(c.Title))
	}
	cv.printf(`<rect x="%s" y="%s" width="%s" height="%s" fill="#ffffff" stroke="#b0b7bb"/>`, f2(cv.px0), f2(cv.py0), f2(cv.px1-cv.px0), f2(cv.py1-cv.py0))

	// grid + ticks
	if len(c.XCats) > 0 {
		for i, s := range c.XCats {
			x := cv.sx(float64(i))
			cv.printf(`<line x1="%s" y1="%s" x2="%s" y2="%s" stroke="#888"/>`, f2(x), f2(cv.py1), f2(x), f2(cv.py1+4))
			if rotateX {
				cv.printf(`<text transform="translate(%s,%s) rotate(-45)" text-anchor="end" fill="#333">%s</text>`, f2(x+3), f2(cv.py1+14), html.EscapeString(s))
			} else {
				cv.printf(`<text x="%s" y="%s" text-anchor="middle" fill="#333">%s</text>`, f2(x), f2(cv.py1+18), html.EscapeString(s))
			}
		}
	} else {
		for _, v := range xt {
			x := cv.sx(v)
			if !c.NoGrid {
				cv.printf(`<line x1="%s" y1="%s" x2="%s" y2="%s" stroke="#e8e8e8"/>`, f2(x), f2(cv.py0), f2(x), f2(cv.py1))
			}
			cv.printf(`<line x1="%s" y1="%s" x2="%s" y2="%s" stroke="#888"/>`, f2(x), f2(cv.py1), f2(x), f2(cv.py1+4))
			cv.printf(`<text x="%s" y="%s" text-anchor="middle" fill="#333">%s</text>`, f2(x), f2(cv.py1+18), html.EscapeString(xtf(v)))
		}
	}
	if len(c.YCats) > 0 {
		for i, s := range c.YCats {
			y := cv.sy(float64(i))
			cv.printf(`<line x1="%s" y1="%s" x2="%s" y2="%s" stroke="#888"/>`, f2(cv.px0-4), f2(y), f2(cv.px0), f2(y))
			cv.printf(`<text x="%s" y="%s" text-anchor="end" fill="#333">%s</text>`, f2(cv.px0-7), f2(y+4), html.EscapeString(s))
		}
	} else {
		for _, v := range yt {
			y := cv.sy(v)
			if !c.NoGrid {
				cv.printf(`<line x1="%s" y1="%s" x2="%s" y2="%s" stroke="#e8e8e8"/>`, f2(cv.px0), f2(y), f2(cv.px1), f2(y))
			}
			cv.printf(`<line x1="%s" y1="%s" x2="%s" y2="%s" stroke="#888"/>`, f2(cv.px0-4), f2(y), f2(cv.px0), f2(y))
			cv.printf(`<text x="%s" y="%s" text-anchor="end" fill="#333">%s</text>`, f2(cv.px0-7), f2(y+4), html.EscapeString(ytf(v)))
		}
	}
	// layers clipped to plot area
	clipID := fmt.Sprintf("clip%p", c)
	cv.printf(`<clipPath id="%s"><rect x="%s" y="%s" width="%s" height="%s"/></clipPath><g clip-path="url(#%s)">`, clipID, f2(cv.px0), f2(cv.py0-1), f2(cv.px1-cv.px0), f2(cv.py1-cv.py0+2), clipID)
	for _, l := range c.Layers {
		l.draw(cv)
	}
	cv.printf(`</g>`)
	// axis labels
	if c.XLabel != "" {
		ly := cv.py1 + 36
		if rotateX {
			ly = H - legendH - 6
		}
		cv.printf(`<text x="%s" y="%s" text-anchor="middle" fill="#000">%s</text>`, f2((cv.px0+cv.px1)/2), f2(ly), html.EscapeString(c.XLabel))
	}
	if c.YLabel != "" {
		cv.printf(`<text transform="translate(16,%s) rotate(-90)" text-anchor="middle" fill="#000">%s</text>`, f2((cv.py0+cv.py1)/2), html.EscapeString(c.YLabel))
	}
	// inset
	for i, s := range c.Inset {
		cv.printf(`<text x="%s" y="%s" text-anchor="end" font-size="11" fill="#333">%s</text>`, f2(cv.px1-6), f2(cv.py0+15+float64(i)*14), html.EscapeString(s))
	}
	// legend
	if len(c.Legend) > 0 {
		y := H - 14
		x := cv.px0
		if c.LegendTitle != "" {
			cv.printf(`<text x="%s" y="%s" fill="#000">%s</text>`, f2(x), f2(y+4), html.EscapeString(c.LegendTitle))
			x += float64(len(c.LegendTitle))*7 + 10
		}
		for _, e := range c.Legend {
			switch e.Kind {
			case "line":
				dash := ""
				if e.Dash {
					dash = ` stroke-dasharray="6,4"`
				}
				cv.printf(`<line x1="%s" y1="%s" x2="%s" y2="%s" stroke="%s" stroke-width="2"%s/>`, f2(x), f2(y), f2(x+22), f2(y), e.Color, dash)
				x += 26
			case "fill":
				cv.printf(`<rect x="%s" y="%s" width="12" height="12" fill="%s" stroke="#333"/>`, f2(x), f2(y-6), e.Color)
				x += 16
			default:
				drawMarker(cv, x+5, y, e.Marker, e.Color, 8)
				x += 14
			}
			cv.printf(`<text x="%s" y="%s" fill="#333">%s</text>`, f2(x), f2(y+4), html.EscapeString(e.Label))
			x += float64(len(e.Label))*7 + 16
		}
	}
	cv.printf(`</svg>`)
	return cv.b.String()
}

func axisRange(lo, hi, fixLo, fixHi float64, zeroBase bool, values []float64) (float64, float64, []float64) {
	if len(values) > 0 {
		a, b := minmax(values)
		lo, hi = math.Min(lo, a), math.Max(hi, b)
	}
	if zeroBase {
		lo = math.Min(lo, 0)
		hi = math.Max(hi, 0)
	}
	if !math.IsNaN(fixLo) {
		lo = fixLo
	}
	if !math.IsNaN(fixHi) {
		hi = fixHi
	}
	ticks := values
	if len(ticks) == 0 {
		ticks = NiceTicks(lo, hi, 6)
	}
	if len(ticks) == 0 {
		return lo, hi, nil
	}
	a, b := lo, hi
	if math.IsNaN(fixLo) && ticks[0] < a {
		a = ticks[0]
	}
	if math.IsNaN(fixHi) && ticks[len(ticks)-1] > b {
		b = ticks[len(ticks)-1]
	}
	// extend to cover data with a small margin so markers are not on the frame
	if b == a {
		b = a + 1
	}
	pad := (b - a) * 0.03
	if math.IsNaN(fixLo) && !(zeroBase && a == 0) {
		a -= pad
	}
	if math.IsNaN(fixHi) {
		b += pad
	}
	var in []float64
	for _, t := range ticks {
		if t >= a-1e-9 && t <= b+1e-9 {
			in = append(in, t)
		}
	}
	return a, b, in
}
