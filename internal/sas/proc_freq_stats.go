package sas

import (
	"fmt"
	"html"
	"math"
	"strconv"
	"strings"
)

// FREQ statistics and table rendering (fq prefix).

var fqPctFmt = func(v float64) string {
	return FormatValue(NumV(v), &FormatSpec{Name: "", W: 9, D: 4, HasW: true, HasD: true})
}
var fqExpFmt = func(v float64) string {
	return FormatValue(NumV(v), &FormatSpec{Name: "", W: 9, D: 4, HasW: true, HasD: true})
}

// fqOneWay renders and tests a one-way table.
func fqOneWay(s *Session, name string, dm *fqDim, counts map[string]float64, nmiss float64, opts *Opts, show bool) (*Dataset, error) {
	k := len(dm.Levels)
	freq := make([]float64, k)
	total := 0.0
	for i := range freq {
		freq[i] = counts[fqKey(i)]
		total += freq[i]
	}
	chisqOpt := opts.Has("CHISQ")
	// TESTP=/TESTF= null values (order must match the sorted level order)
	var testp []float64
	if op, ok := opts.Get("TESTP"); ok && op.Eq {
		for _, tk := range op.Inner() {
			if tk.Kind == TNumber {
				testp = append(testp, tk.Num)
			}
		}
	}
	var testf []float64
	if op, ok := opts.Get("TESTF"); ok && op.Eq {
		for _, tk := range op.Inner() {
			if tk.Kind == TNumber {
				testf = append(testf, tk.Num)
			}
		}
	}
	// ODS OneWayFreqs + display
	build := func(withMissing bool) *Table {
		rows := k
		if withMissing && nmiss > 0 {
			rows++
		}
		t := &Table{ODSName: "OneWayFreqs", Caption: "The FREQ Procedure\n\nFrequency Table for " + name, MissDot: true}
		showPct := !opts.Has("NOPERCENT")
		showCum := !opts.Has("NOCUM")
		cols := []Col{
			{Name: "F_" + name, Label: "", Char: true, Hidden: true},
			{Name: name, Label: name, Char: dm.Char},
			{Name: "Frequency", Label: "Frequency", Format: "9."},
		}
		if showPct {
			cols = append(cols, Col{Name: "Percent", Label: "Percent", Format: "7.4"})
		}
		if len(testp) == k && chisqOpt {
			cols = append(cols, Col{Name: "TestPercent", Label: "Test\nPercent", Format: "7.2"})
		}
		if len(testf) == k && chisqOpt {
			cols = append(cols, Col{Name: "TestFrequency", Label: "Test\nFrequency", Format: "9."})
		}
		if showCum {
			cols = append(cols, Col{Name: "CumFrequency", Label: "Cumulative\nFrequency", Format: "11."},
				Col{Name: "CumPercent", Label: "Cumulative\nPercent", Format: "9.4"})
		}
		t.Cols = cols
		cum := 0.0
		for i := range rows {
			v := Missing
			f := 0.0
			isMiss := withMissing && i == k
			if !isMiss {
				v = dm.Levels[i]
				f = freq[i]
			}
			cum += f
			p := 0.0
			if total > 0 {
				p = 100 * f / total
			}
			cp := 0.0
			if total > 0 {
				cp = 100 * cum / total
			}
			row := []Value{StrV(name), v, NumV(f)}
			if showPct {
				row = append(row, NumV(p))
			}
			if len(testp) == k && chisqOpt {
				row = append(row, NumV(testp[i]))
			}
			if len(testf) == k && chisqOpt {
				row = append(row, NumV(testf[i]))
			}
			if showCum {
				row = append(row, NumV(cum), NumV(cp))
			}
			t.Rows = append(t.Rows, row)
		}
		if nmiss > 0 && !withMissing {
			t.Notes = append(t.Notes, fmt.Sprintf("Frequency Missing %.0f", nmiss))
		}
		return t
	}
	if show {
		t := build(false)
		t.ODSName = "OneWayFreqs"
		s.Out.AddTable(t)
		if nmiss > 0 && opts.Has("MISSPRINT") {
			t2 := build(true)
			t2.ODSName = "" // display-only extra rows
			s.Out.AddTable(t2)
		}
	} else {
		s.Out.capture(build(false))
	}

	// goodness-of-fit chi-square
	if chisqOpt {
		lrchi := false
		if op, ok := opts.Get("CHISQ"); ok {
			for _, tk := range op.Inner() {
				if tk.Upper() == "LRCHI" {
					lrchi = true
				}
			}
		}
		var caption string
		var expected []float64
		switch {
		case len(testp) > 0:
			caption = "Chi-Square Test for Specified Proportions"
			sum := Sum(testp)
			for _, p := range testp {
				expected = append(expected, total*p/sum)
			}
		case len(testf) > 0:
			// SAS: the expected frequencies are the TESTF= values themselves
			caption = "Chi-Square Test for Specified Frequencies"
			expected = append(expected, testf...)
		default:
			caption = "Chi-Square Test for Equal Proportions"
			for range freq {
				expected = append(expected, total/float64(k))
			}
		}
		x2, g2 := 0.0, 0.0
		valid := true
		for i := range freq {
			if expected[i] <= 0 {
				valid = false
				continue
			}
			x2 += (freq[i] - expected[i]) * (freq[i] - expected[i]) / expected[i]
			if freq[i] > 0 {
				g2 += 2 * freq[i] * math.Log(freq[i]/expected[i])
			}
		}
		df := float64(k - 1)
		t := &Table{ODSName: "OneWayChiSq", Caption: caption, MissDot: true}
		t.Cols = []Col{{Name: "Test", Label: caption, Char: true}, {Name: "nValue", Label: "", Format: "BEST12."}}
		t.AddRow("Chi-Square", x2)
		if lrchi {
			t.AddRow("Likelihood Ratio Chi-Square", g2)
		}
		t.AddRow("DF", df)
		t.AddRow("Pr > ChiSq", ChiSqSF(x2, df))
		if show {
			s.Out.AddTable(t)
		} else {
			s.Out.capture(t)
		}
		if !valid {
			s.Log.Warn(0, "The expected values under the null hypothesis are not all positive; the chi-square test is not valid.")
		}
	}

	// OUT= dataset
	ds := NewDataset(name)
	v := &Var{Name: name, Char: dm.Char, Length: 8}
	if dm.Char {
		for _, l := range dm.Levels {
			if n := len(l.Str); n > v.Length {
				v.Length = n
			}
		}
	}
	ds.Vars = []*Var{v, {Name: "COUNT", Length: 8}, {Name: "PERCENT", Length: 8}}
	if opts.Has("OUTEXPECT") {
		ds.Vars = append(ds.Vars, &Var{Name: "EXPECTED", Length: 8})
	}
	if opts.Has("OUTPCT") {
		ds.Vars = append(ds.Vars, &Var{Name: "PCT_ROW", Length: 8}, &Var{Name: "PCT_COL", Length: 8}, &Var{Name: "PCT_TABL", Length: 8})
	}
	for i := range dm.Levels {
		p := 0.0
		if total > 0 {
			p = 100 * freq[i] / total
		}
		row := []Value{dm.Levels[i], NumV(freq[i]), NumV(p)}
		if opts.Has("OUTEXPECT") {
			row = append(row, NumV(total/float64(k)))
		}
		if opts.Has("OUTPCT") {
			row = append(row, Missing, Missing, Missing)
		}
		ds.Rows = append(ds.Rows, row)
	}
	return ds, nil
}

// fqCrossTab renders and tests an n-way (2-way) table.
func fqCrossTab(s *Session, vars []string, dims []*fqDim, counts map[string]float64, nmiss float64, opts *Opts, show bool) (*Dataset, error) {
	r, c := len(dims[0].Levels), len(dims[1].Levels)
	tbl := make([][]float64, r)
	for i := range tbl {
		tbl[i] = make([]float64, c)
		for j := range tbl[i] {
			tbl[i][j] = counts[fqKey(i)+","+fqKey(j)]
		}
	}
	n := Sum2(tbl)
	ri := RowSums(tbl)
	cj := ColSums(tbl)

	if show {
		s.Out.AddHTML(fqCrossTabHTML(vars, dims, tbl, n, ri, cj, opts, nmiss))
	}
	// capture CrossTabFreqs
	ct := &Table{ODSName: "CrossTabFreqs", MissDot: true}
	ct.Cols = []Col{
		{Name: "Table", Label: "Table", Char: true, Hidden: true},
		{Name: vars[0], Label: vars[0], Char: dims[0].Char, Hidden: true},
		{Name: vars[1], Label: vars[1], Char: dims[1].Char, Hidden: true},
		{Name: "Frequency", Label: "Frequency", Format: "10."},
		{Name: "Expected", Label: "Expected", Format: "9.4", Hidden: !opts.Has("EXPECTED")},
		{Name: "Percent", Label: "Percent", Format: "7.4", Hidden: opts.Has("NOPERCENT")},
		{Name: "RowPercent", Label: "Row Pct", Format: "7.4", Hidden: opts.Has("NOROW")},
		{Name: "ColPercent", Label: "Col Pct", Format: "7.4", Hidden: opts.Has("NOCOL")},
	}
	odsName := fqOdsTableName(vars)
	for i := range r {
		for j := range c {
			row := []Value{StrV(odsName), dims[0].Levels[i], dims[1].Levels[j], NumV(tbl[i][j])}
			exp := 0.0
			if n > 0 {
				exp = ri[i] * cj[j] / n
			}
			row = append(row, NumV(exp))
			p := 0.0
			if n > 0 {
				p = 100 * tbl[i][j] / n
			}
			row = append(row, NumV(p))
			rp, cp := Missing, Missing
			if ri[i] > 0 {
				rp = NumV(100 * tbl[i][j] / ri[i])
			}
			if cj[j] > 0 {
				cp = NumV(100 * tbl[i][j] / cj[j])
			}
			row = append(row, rp, cp)
			ct.Rows = append(ct.Rows, row)
		}
	}
	// margins
	for i := range r {
		p := 0.0
		if n > 0 {
			p = 100 * ri[i] / n
		}
		ct.Rows = append(ct.Rows, []Value{StrV(odsName), dims[0].Levels[i], StrV("Total"), NumV(ri[i]), NumV(ri[i]), NumV(p), NumV(100), Missing})
	}
	for j := range c {
		p := 0.0
		if n > 0 {
			p = 100 * cj[j] / n
		}
		ct.Rows = append(ct.Rows, []Value{StrV(odsName), StrV("Total"), dims[1].Levels[j], NumV(cj[j]), NumV(cj[j]), NumV(p), Missing, NumV(100)})
	}
	ct.Rows = append(ct.Rows, []Value{StrV(odsName), StrV("Total"), StrV("Total"), NumV(n), NumV(n), NumV(100), Missing, Missing})
	s.Out.capture(ct)

	// chi-square statistics
	stats, fisher := fqChiSq(tbl, fqScores(dims[0]), fqScores(dims[1]))
	lowExpected := 0
	cells := r * c
	if n > 0 {
		for i := range r {
			for j := range c {
				if ri[i]*cj[j]/n < 5 {
					lowExpected++
				}
			}
		}
	}
	if show {
		t := &Table{ODSName: "ChiSq", Caption: "Statistics for Table of " + strings.Join(vars, " by "), MissDot: true}
		t.Cols = []Col{
			{Name: "Table", Label: "", Char: true, Hidden: true},
			{Name: "Statistic", Label: "Statistic", Char: true},
			{Name: "DF", Label: "DF", Format: "3."},
			{Name: "Value", Label: "Value", Format: "8.4"},
			{Name: "Prob", Label: "Pr > ChiSq", Format: "PVALUE6.4"},
		}
		t.RowHeader = 1
		for _, row := range stats {
			t.AddRow(odsName, row.name, row.df, row.value, row.prob)
		}
		s.Out.AddTable(t)
		if lowExpected > 0 && 100.0*float64(lowExpected)/float64(cells) > 20 {
			t.Notes = append(t.Notes, fmt.Sprintf("%.0f%% of the cells have expected counts less than 5. Chi-Square may not be a valid test.", math.Round(100*float64(lowExpected)/float64(cells))))
			s.Out.AddText(&Text{Text: t.Notes[0]})
		}
		if fisher != nil {
			ft := &Table{ODSName: "FishersExact", Caption: "Fisher's Exact Test for Table of " + strings.Join(vars, " by "), MissDot: true}
			ft.Cols = []Col{{Name: "Table", Label: "", Char: true, Hidden: true}, {Name: "Statistic", Label: "Cell (1,1) Frequency (F)", Char: true}, {Name: "Value", Label: "Value", Format: "8.4"}}
			ft.RowHeader = 1
			ft.AddRow(odsName, "Cell (1,1) Frequency (F)", tbl[0][0])
			ft.AddRow(odsName, "Left-sided Pr <= F", fisher.left)
			ft.AddRow(odsName, "Right-sided Pr >= F", fisher.right)
			ft.AddRow(odsName, "Table Probability (P)", fisher.prob)
			ft.AddRow(odsName, "Two-sided Pr <= P", fisher.two)
			s.Out.AddTable(ft)
		}
	} else {
		t := &Table{ODSName: "ChiSq", MissDot: true}
		t.Cols = []Col{
			{Name: "Table", Label: "Table", Char: true, Hidden: true},
			{Name: "Statistic", Label: "Statistic", Char: true},
			{Name: "DF", Label: "DF", Format: "3."},
			{Name: "Value", Label: "Value", Format: "8.4"},
			{Name: "Prob", Label: "Pr > ChiSq", Format: "PVALUE6.4"},
		}
		for _, row := range stats {
			t.AddRow(odsName, row.name, row.df, row.value, row.prob)
		}
		s.Out.capture(t)
		if fisher != nil {
			ft := &Table{ODSName: "FishersExact", MissDot: true}
			ft.Cols = []Col{{Name: "Table", Label: "Table", Char: true, Hidden: true}, {Name: "Statistic", Label: "Statistic", Char: true}, {Name: "nValue", Label: "nValue", Format: "BEST12."}}
			ft.AddRow(odsName, "Cell (1,1) Frequency (F)", tbl[0][0])
			ft.AddRow(odsName, "Left-sided Pr <= F", fisher.left)
			ft.AddRow(odsName, "Right-sided Pr >= F", fisher.right)
			ft.AddRow(odsName, "Table Probability (P)", fisher.prob)
			ft.AddRow(odsName, "Two-sided Pr <= P", fisher.two)
			s.Out.capture(ft)
		}
	}

	// OUT=
	ds := NewDataset(vars[0])
	ds.Vars = []*Var{
		{Name: vars[0], Char: dims[0].Char, Length: 8},
		{Name: vars[1], Char: dims[1].Char, Length: 8},
		{Name: "COUNT", Length: 8}, {Name: "PERCENT", Length: 8},
	}
	if opts.Has("OUTEXPECT") {
		ds.Vars = append(ds.Vars, &Var{Name: "EXPECTED", Length: 8})
	}
	if opts.Has("OUTPCT") {
		ds.Vars = append(ds.Vars, &Var{Name: "PCT_ROW", Length: 8}, &Var{Name: "PCT_COL", Length: 8}, &Var{Name: "PCT_TABL", Length: 8})
	}
	for i := range r {
		for j := range c {
			row := []Value{dims[0].Levels[i], dims[1].Levels[j], NumV(tbl[i][j])}
			p := 0.0
			if n > 0 {
				p = 100 * tbl[i][j] / n
			}
			row = append(row, NumV(p))
			if opts.Has("OUTEXPECT") {
				exp := 0.0
				if n > 0 {
					exp = ri[i] * cj[j] / n
				}
				row = append(row, NumV(exp))
			}
			if opts.Has("OUTPCT") {
				rp, cp := Missing, Missing
				if ri[i] > 0 {
					rp = NumV(100 * tbl[i][j] / ri[i])
				}
				if cj[j] > 0 {
					cp = NumV(100 * tbl[i][j] / cj[j])
				}
				row = append(row, rp, cp, NumV(p))
			}
			ds.Rows = append(ds.Rows, row)
		}
	}
	return ds, nil
}

func fqOdsTableName(vars []string) string {
	return "Table " + strings.Join(vars, " * ")
}

type fqStat struct {
	name  string
	df    float64
	value float64
	prob  float64
}

type fqFisher struct {
	left, right, prob, two float64
}

// Sum2, RowSums, ColSums are small matrix helpers (fq prefix in name).
func Sum2(m [][]float64) float64 {
	s := 0.0
	for _, row := range m {
		s += Sum(row)
	}
	return s
}
func RowSums(m [][]float64) []float64 {
	out := make([]float64, len(m))
	for i, row := range m {
		out[i] = Sum(row)
	}
	return out
}
func ColSums(m [][]float64) []float64 {
	out := make([]float64, len(m[0]))
	for _, row := range m {
		for j, v := range row {
			out[j] += v
		}
	}
	return out
}

// fqChiSq computes the standard statistics for an r x c table
// (rowSc/colSc are the SCORES=TABLE row/column scores for Mantel-Haenszel).
func fqChiSq(tbl [][]float64, rowSc, colSc []float64) ([]fqStat, *fqFisher) {
	r := len(tbl)
	c := len(tbl[0])
	n := Sum2(tbl)
	ri, cj := RowSums(tbl), ColSums(tbl)
	var out []fqStat
	if n == 0 {
		return out, nil
	}
	x2, g2 := 0.0, 0.0
	for i := range r {
		for j := range c {
			e := ri[i] * cj[j] / n
			o := tbl[i][j]
			if e > 0 {
				x2 += (o - e) * (o - e) / e
				if o > 0 {
					g2 += 2 * o * math.Log(o/e)
				}
			}
		}
	}
	df := float64((r - 1) * (c - 1))
	out = append(out,
		fqStat{"Chi-Square", df, x2, ChiSqSF(x2, df)},
		fqStat{"Likelihood Ratio Chi-Square", df, g2, ChiSqSF(g2, df)},
	)
	if r == 2 && c == 2 {
		x2c := 0.0
		for i := range r {
			for j := range c {
				e := ri[i] * cj[j] / n
				d := math.Abs(tbl[i][j]-e) - 0.5
				if d > 0 {
					x2c += d * d / e
				}
			}
		}
		out = append(out, fqStat{"Continuity Adj. Chi-Square", df, x2c, ChiSqSF(x2c, df)})
	}
	// Mantel-Haenszel: (n-1)*r^2 with table scores
	mh := fqMHChiSq(tbl, rowSc, colSc)
	mhp := Missing.Num
	if !math.IsNaN(mh) {
		mhp = ChiSqSF(mh, 1)
	}
	out = append(out, fqStat{"Mantel-Haenszel Chi-Square", 1, mh, mhp})
	// association measures
	phi := math.Sqrt(x2 / n)
	if r == 2 && c == 2 {
		phi = (tbl[0][0]*tbl[1][1] - tbl[0][1]*tbl[1][0]) / math.Sqrt(ri[0]*ri[1]*cj[0]*cj[1])
	}
	cc := math.Sqrt(x2 / (x2 + n))
	v := math.Sqrt(x2 / (n * math.Min(float64(r-1), float64(c-1))))
	out = append(out,
		fqStat{"Phi Coefficient", Missing.Num, phi, Missing.Num},
		fqStat{"Contingency Coefficient", Missing.Num, cc, Missing.Num},
		fqStat{"Cramer's V", Missing.Num, v, Missing.Num},
	)
	// Fisher for 2x2
	var fish *fqFisher
	if r == 2 && c == 2 {
		fish = fqFisher22(tbl)
	}
	return out, fish
}

// fqFisher22 computes Fisher's exact test for a 2x2 table.
func fqFisher22(tbl [][]float64) *fqFisher {
	n := tbl[0][0] + tbl[0][1] + tbl[1][0] + tbl[1][1]
	r1 := tbl[0][0] + tbl[0][1]
	c1 := tbl[0][0] + tbl[1][0]
	lo := math.Max(0, c1-(n-r1))
	hi := math.Min(r1, c1)
	probs := make([]float64, 0, int(hi-lo)+1)
	for x := lo; x <= hi; x++ {
		probs = append(probs, HypergeomPMF(int(x), int(n), int(r1), int(c1)))
	}
	x11 := int(tbl[0][0])
	idx := x11 - int(lo)
	if idx < 0 || idx >= len(probs) {
		return nil
	}
	p0 := probs[idx]
	var left, right, two float64
	for i, p := range probs {
		if float64(int(lo)+i) <= tbl[0][0]+1e-9 {
			left += p
		}
		if float64(int(lo)+i) >= tbl[0][0]-1e-9 {
			right += p
		}
		if p <= p0*(1+1e-7) {
			two += p
		}
	}
	return &fqFisher{left: math.Min(1, left), right: math.Min(1, right), prob: p0, two: math.Min(1, two)}
}

// fqCrossTabHTML renders the SAS-style crosstab.
func fqCrossTabHTML(vars []string, dims []*fqDim, tbl [][]float64, n float64, ri, cj []float64, opts *Opts, nmiss float64) *HTML {
	var b strings.Builder
	esc := html.EscapeString
	fmt.Fprintf(&b, "<div class=\"proc\">Cross-Tabular Frequency Table for %s by %s</div>", esc(vars[0]), esc(vars[1]))
	wantExp := opts.Has("EXPECTED")
	wantCell := opts.Has("CELLCHI2")
	wantDev := opts.Has("DEVIATION")
	wantPct := !opts.Has("NOPERCENT")
	wantRow := !opts.Has("NOROW")
	wantCol := !opts.Has("NOCOL")
	r, c := len(tbl), len(tbl[0])

	head := func(extra string) {
		b.WriteString("<tr><th rowspan=\"2\">" + esc(dims[0].Name) + "</th><th colspan=\"" + strconv.Itoa(c+1) + "\">" + esc(dims[1].Name) + "</th></tr><tr>")
		for j := range c {
			b.WriteString("<th>" + esc(cellLabel(dims[1].Levels[j])) + "</th>")
		}
		b.WriteString("<th>Total</th>" + extra + "</tr>")
	}
	b.WriteString("<table class=\"sas\"><caption>Table of " + esc(vars[0]) + " by " + esc(vars[1]) + "</caption><thead>")
	head("")
	b.WriteString("</thead><tbody>")
	for i := range r {
		b.WriteString("<tr><th>" + esc(cellLabel(dims[0].Levels[i])) + "</th>")
		for j := range c {
			o := tbl[i][j]
			e := ri[i] * cj[j] / n
			fmt.Fprintf(&b, "<td class=\"c\" style=\"white-space:nowrap\">%.0f<br>", o)
			if wantExp {
				fmt.Fprintf(&b, "%s<br>", fqExpFmt(e))
			}
			if wantDev {
				fmt.Fprintf(&b, "%s<br>", fqExpFmt(o-e))
			}
			if wantCell {
				fmt.Fprintf(&b, "%s<br>", fqExpFmt((o-e)*(o-e)/e))
			}
			if wantPct {
				fmt.Fprintf(&b, "%s%%<br>", fqPctFmt(100*o/n))
			}
			if wantRow {
				fmt.Fprintf(&b, "%s%%<br>", fqPctFmt(100*o/ri[i]))
			}
			if wantCol {
				fmt.Fprintf(&b, "%s%%<br>", fqPctFmt(100*o/cj[j]))
			}
			b.WriteString("</td>")
		}
		fmt.Fprintf(&b, "<td>%.0f<br>%s%%</td>", ri[i], fqPctFmt(100*ri[i]/n))
		b.WriteString("</tr>")
	}
	b.WriteString("<tr><th>Total</th>")
	for j := range c {
		fmt.Fprintf(&b, "<td>%.0f<br>%s%%</td>", cj[j], fqPctFmt(100*cj[j]/n))
	}
	fmt.Fprintf(&b, "<td>%.0f<br>%s%%</td></tr>", n, fqPctFmt(100))
	b.WriteString("</tbody></table>")
	if nmiss > 0 {
		fmt.Fprintf(&b, "<p class=\"notes\">Frequency Missing %.0f</p>", nmiss)
	}
	return &HTML{HTML: b.String()}
}

func cellLabel(v Value) string {
	if v.IsMissing() {
		return "."
	}
	return strings.TrimRight(v.AsStr(), " ")
}

// fqMHChiSq computes the Mantel-Haenszel statistic with table scores.
func fqMHChiSq(tbl [][]float64, rowSc, colSc []float64) float64 {
	n := Sum2(tbl)
	if n <= 1 {
		return math.NaN()
	}
	r, c := len(tbl), len(tbl[0])
	ri, cj := RowSums(tbl), ColSums(tbl)
	rm, cm := 0.0, 0.0
	for i := range ri {
		rm += ri[i] * rowSc[i]
	}
	for j := range cj {
		cm += cj[j] * colSc[j]
	}
	rm, cm = rm/n, cm/n
	sxx, syy, sxy := 0.0, 0.0, 0.0
	for i := range r {
		for j := range c {
			dx := rowSc[i] - rm
			dy := colSc[j] - cm
			sxx += tbl[i][j] * dx * dx
			syy += tbl[i][j] * dy * dy
			sxy += tbl[i][j] * dx * dy
		}
	}
	if sxx == 0 || syy == 0 {
		return math.NaN()
	}
	rCorr := sxy / math.Sqrt(sxx*syy)
	return (n - 1) * rCorr * rCorr
}
