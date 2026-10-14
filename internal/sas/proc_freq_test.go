package sas

import (
	"math"
	"testing"
)

// FREQ statistics pinned against scipy.stats reference values
// (chi2_contingency, Mann-Whitney/Kruskal in proc_npar1way_test.go).

func fqTestCarsTypeDriveTrain(t *testing.T) ([][]float64, []*fqDim) {
	t.Helper()
	d := loadSashelp("CARS")
	dims, counts, _ := fqTabulate(d, []string{"Type", "DriveTrain"}, -1, false)
	tbl := make([][]float64, len(dims[0].Levels))
	for i := range tbl {
		tbl[i] = make([]float64, len(dims[1].Levels))
		for j := range tbl[i] {
			tbl[i][j] = counts[fqKey(i)+","+fqKey(j)]
		}
	}
	return tbl, dims
}

func TestFqCarsTypeDriveTrain(t *testing.T) {
	tbl, dims := fqTestCarsTypeDriveTrain(t)
	stats, fisher := fqChiSq(tbl, fqScores(dims[0]), fqScores(dims[1]))
	// scipy: chi2_contingency
	want := map[string][2]float64{
		"Chi-Square":                  {185.6709835236498, ChiSqSF(185.6709835236498, 10)},
		"Likelihood Ratio Chi-Square": {187.55952746813148, ChiSqSF(187.55952746813148, 10)},
		"Mantel-Haenszel Chi-Square":  {15.715697693753226, ChiSqSF(15.715697693753226, 1)},
		"Phi Coefficient":             {math.Sqrt(185.6709835236498 / 428), math.NaN()},
		"Contingency Coefficient":     {math.Sqrt(185.6709835236498 / (185.6709835236498 + 428)), math.NaN()},
		"Cramer's V":                  {math.Sqrt(185.6709835236498 / (428 * 2)), math.NaN()},
	}
	seen := map[string]bool{}
	for _, st := range stats {
		w, ok := want[st.name]
		if !ok {
			t.Fatalf("unexpected statistic %q", st.name)
		}
		seen[st.name] = true
		if math.Abs(st.value-w[0]) > 1e-9*max(1, math.Abs(w[0])) {
			t.Errorf("%s value = %.12g, want %.12g", st.name, st.value, w[0])
		}
		if !math.IsNaN(w[1]) && math.Abs(st.prob-w[1]) > 1e-12 {
			t.Errorf("%s prob = %.12g, want %.12g", st.name, st.prob, w[1])
		}
	}
	for name := range want {
		if !seen[name] {
			t.Errorf("statistic %q missing", name)
		}
	}
	if fisher != nil {
		t.Errorf("Fisher's exact should only be reported for 2x2 (got rows)")
	}
	// scipy fisher via simulation is not exact for RxC; check 2x2 below
}

func TestFqFisher22(t *testing.T) {
	// SAS documentation example: 2x2 table [[23,15],[19,3]]
	tbl := [][]float64{{23, 15}, {19, 3}}
	f := fqFisher22(tbl)
	if f == nil {
		t.Fatal("nil fisher")
	}
	// scipy.stats.fisher_exact([[23,15],[19,3]]) and hypergeom pmf sums
	for name, want := range map[string]float64{
		"prob":  0.025756778145045494,
		"left":  0.03203138542600327,
		"right": 0.9937253927190424,
		"two":   0.0438825609395726,
	} {
		got := map[string]float64{"prob": f.prob, "left": f.left, "right": f.right, "two": f.two}[name]
		if math.Abs(got-want) > 1e-12 {
			t.Errorf("fisher %s = %.15g, want %.15g", name, got, want)
		}
	}
}

func TestFqOneWayGOF(t *testing.T) {
	// iris species counts 50/50/50 with testp 20/50/30 -> X2 = 22.2222
	dims := []*fqDim{
		{Name: "Species", Char: true, Levels: []Value{StrV("Setosa"), StrV("Versicolor"), StrV("Virginica")}},
	}
	counts := map[string]float64{fqKey(0): 50, fqKey(1): 50, fqKey(2): 50}
	fqOneWayCapture = nil
	sess := NewSession(".")
	sess.Out.BeginProc("")
	sess.Out.SelectNone = true
	sess.Out.ODSOutputs["ONEWAYCHISQ"] = "_chisq_"
	_, err := fqOneWay(sess, "Species", dims[0], counts, 0, ParseOpts(mustToks("/ chisq testp=(20,50,30)")), false)
	if err != nil {
		t.Fatal(err)
	}
	sess.Out.EndProc()
	ds := sess.Dataset("_chisq_")
	if ds == nil {
		t.Fatal("no ODS OUTPUT OneWayChiSq captured")
	}
	// SAS vertical layout: rows Chi-Square / DF / Pr > ChiSq (label in col 0)
	if ds.Rows[0][0].Str != "Chi-Square" || ds.Rows[1][0].Str != "DF" || ds.Rows[2][0].Str != "Pr > ChiSq" {
		t.Fatalf("unexpected OneWayChiSq rows: %v", ds.Rows)
	}
	got := ds.Rows[0][1].Num
	if math.Abs(got-22.22222222222222) > 1e-9 {
		t.Errorf("GOF X2 = %.12g, want 22.22222222222222", got)
	}
}

func mustToks(s string) []Token {
	toks, _ := Tokenize(s, 1)
	return toks
}

var fqOneWayCapture *Dataset // reserved hook (unused)

// TestFqOneWayGOFHairColor pins the one-way goodness-of-fit test against
// SAS/STAT 9.2 PROC FREQ Example 35.3 (children's hair color): Region 1
// Chi-Square 7.7602 (Pr > ChiSq 0.1008), Region 2 Chi-Square 21.3824
// (Pr > ChiSq 0.0003). Levels sort alphabetically, so the TESTP list is
// the documented (30 12 30 25 3) permuted to (3 25 30 30 12).
func TestFqOneWayGOFHairColor(t *testing.T) {
	dm := &fqDim{Name: "Hair", Char: true, Levels: []Value{StrV("black"), StrV("dark"), StrV("fair"), StrV("medium"), StrV("red")}}
	for _, tc := range []struct {
		counts map[string]float64
		x2, p  float64
	}{
		{map[string]float64{"0": 3, "1": 65, "2": 76, "3": 83, "4": 19}, 7.7602, 0.1008},
		{map[string]float64{"0": 19, "1": 117, "2": 152, "3": 134, "4": 94}, 21.3824, 0.0003},
	} {
		sess := NewSession(".")
		sess.Out.BeginProc("")
		sess.Out.SelectNone = true
		sess.Out.ODSOutputs["ONEWAYCHISQ"] = "_chisq_"
		if _, err := fqOneWay(sess, "Hair", dm, tc.counts, 0, ParseOpts(mustToks("/ nocum chisq testp=(3 25 30 30 12)")), false); err != nil {
			t.Fatal(err)
		}
		sess.Out.EndProc()
		ds := sess.Dataset("_chisq_")
		if ds == nil {
			t.Fatal("no ODS OUTPUT OneWayChiSq captured")
		}
		if math.Abs(ds.Rows[0][1].Num-tc.x2) > 5e-5 {
			t.Errorf("Chi-Square = %.6f, want %.4f", ds.Rows[0][1].Num, tc.x2)
		}
		if ds.Rows[1][1].Num != 4 {
			t.Errorf("DF = %v, want 4", ds.Rows[1][1].Num)
		}
		if math.Abs(ds.Rows[2][1].Num-tc.p) > 5e-5 {
			t.Errorf("Pr > ChiSq = %.6f, want %.4f", ds.Rows[2][1].Num, tc.p)
		}
	}
}
