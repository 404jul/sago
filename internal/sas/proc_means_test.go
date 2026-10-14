package sas

import (
	"math"
	"testing"
)

func bpRun(t *testing.T, src string) *Session {
	t.Helper()
	s := NewSession(t.TempDir())
	s.RunProgram(src)
	if s.Log.Errors > 0 {
		for _, e := range s.Log.Entries {
			t.Log(e.String())
		}
		t.Fatalf("program had errors")
	}
	return s
}

func bpCheck(t *testing.T, d *Dataset, row int, name string, want float64) {
	t.Helper()
	i := d.Index(name)
	if i < 0 {
		t.Fatalf("variable %s missing", name)
	}
	got := d.Rows[row][i].Num
	if math.Abs(got-want) > 1e-9*math.Max(1, math.Abs(want)) {
		t.Errorf("%s row %d = %.15g, want %.15g", name, row, got, want)
	}
}

// References computed with numpy/scipy (ddof=1, bias=False skew/kurtosis,
// t quantile CLM) and the PCTLDEF=5 definition.
func TestBpMeansClassStats(t *testing.T) {
	s := bpRun(t, `proc means data=sashelp.class noprint nway; class sex; var height;
	output out=o mean= lclm= t= q1= median= q3= skew= kurt= / autoname; run;`)
	d := s.Dataset("O")
	want := []string{"Sex", "_TYPE_", "_FREQ_", "Height_Mean", "Height_LCLM", "Height_t", "Height_Q1", "Height_Median", "Height_Q3", "Height_Skew", "Height_Kurt"}
	for i, n := range want {
		if d.Vars[i].Name != n {
			t.Fatalf("var %d = %s, want %s", i, d.Vars[i].Name, n)
		}
	}
	bpCheck(t, d, 0, "_TYPE_", 1)
	bpCheck(t, d, 0, "_FREQ_", 9)
	bpCheck(t, d, 0, "Height_Mean", 60.58888888888888)
	bpCheck(t, d, 0, "Height_LCLM", 56.731460883574314)
	bpCheck(t, d, 0, "Height_t", 36.22056669177154)
	bpCheck(t, d, 0, "Height_Q1", 56.5)
	bpCheck(t, d, 0, "Height_Median", 62.5)
	bpCheck(t, d, 0, "Height_Q3", 64.3)
	bpCheck(t, d, 0, "Height_Skew", -0.7238642646765566)
	bpCheck(t, d, 0, "Height_Kurt", -0.3464948955384193)
	bpCheck(t, d, 1, "Height_Median", 64.15)
	bpCheck(t, d, 1, "Height_Skew", 0.0409591748425531)
	bpCheck(t, d, 1, "Height_Kurt", -0.9348759557617439)
}

func TestBpMeansCars(t *testing.T) {
	s := bpRun(t, `proc means data=sashelp.cars noprint; var msrp;
	output out=o mean=m std=sd p25=a median=b p90=c skewness=sk kurtosis=ku; run;`)
	d := s.Dataset("O")
	bpCheck(t, d, 0, "m", 32774.85514018692)
	bpCheck(t, d, 0, "sd", 19431.71667371752)
	bpCheck(t, d, 0, "a", 20329.5)
	bpCheck(t, d, 0, "b", 27635)
	bpCheck(t, d, 0, "c", 52795)
	bpCheck(t, d, 0, "sk", 2.7980992746701703)
	bpCheck(t, d, 0, "ku", 13.879205524289226)
}

func TestBpMeansWeighted(t *testing.T) {
	s := bpRun(t, `proc means data=sashelp.class noprint; var height; weight weight;
	output out=o mean=m var=v stderr=se q1=a median=b q3=c; run;`)
	d := s.Dataset("O")
	bpCheck(t, d, 0, "m", 63.30757695343331)
	bpCheck(t, d, 0, "v", 2427.776160659476)
	bpCheck(t, d, 0, "se", 1.1302392481427748)
	bpCheck(t, d, 0, "a", 59)
	bpCheck(t, d, 0, "b", 63.5)
	bpCheck(t, d, 0, "c", 66.5)
}

func TestBpMeansDisplay(t *testing.T) {
	cases := []struct {
		x    float64
		want string
	}{{13.315789473684211, "13.3157895"}, {100.02631578947368, "100.0263158"}, {3577.9532710280373, "3577.95"}, {758.98321460987, "758.9832146"}, {192465, "192465.00"}}
	for _, c := range cases {
		if got := bpMeansFmt(c.x, -1, "MEAN"); got != c.want {
			t.Errorf("bpMeansFmt(%v) = %s, want %s", c.x, got, c.want)
		}
	}
}
