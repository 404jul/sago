package sas

import (
	"regexp"
	"strconv"
	"testing"
)

// TestSGScatterPlotRequests covers PLOT statements with several requests
// (y*x a*b ..., mixed with (ys)*(xs) crosses): one cell per pair, laid out
// roughly square unless COLUMNS= / ROWS= says otherwise.
func TestSGScatterPlotRequests(t *testing.T) {
	const data = `data bc;
  do i = 1 to 20;
    x = i; y = 2*i + mod(i*7, 5);
    logx = log(x); logY = log(y);
    output;
  end;
run;
`
	viewBox := regexp.MustCompile(`<svg[^>]*viewBox="0 0 ([0-9.]+) ([0-9.]+)"`)
	cases := []struct {
		name, plot        string
		cells, cols, rows int
	}{
		{"pairs", "plot y*x logY*logx logY*x y*logx;", 4, 2, 2},
		{"columns", "plot y*x logY*logx logY*x y*logx / columns=1;", 4, 1, 4},
		{"rows", "plot y*x logY*logx logY*x y*logx / rows=1;", 4, 4, 1},
		{"mixed", "plot (y logY)*x (y logY)*(logx) y*(x logx);", 6, 3, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := bpRun(t, data+"proc sgscatter data=bc;\n"+tc.plot+"\nrun;\n")
			var svg string
			for _, sec := range s.Out.Sections {
				for _, it := range sec.Items {
					if g, ok := it.(*Graph); ok && g.ODSName == "SGScatter" {
						svg = g.SVG
					}
				}
			}
			if svg == "" {
				t.Fatal("no SGScatter graph")
			}
			boxes := viewBox.FindAllStringSubmatch(svg, -1)
			if got := len(boxes) - 1; got != tc.cells {
				t.Errorf("cells = %d, want %d", got, tc.cells)
			}
			num := func(s string) float64 { f, _ := strconv.ParseFloat(s, 64); return f }
			if got := num(boxes[0][1]) / num(boxes[1][1]); got != float64(tc.cols) {
				t.Errorf("columns = %g, want %d", got, tc.cols)
			}
			if got := num(boxes[0][2]) / num(boxes[1][2]); got != float64(tc.rows) {
				t.Errorf("rows = %g, want %d", got, tc.rows)
			}
		})
	}
}
