package sas

import (
	"math"
	"testing"
)

// EXPORT of SASHELP.CARS (formatted values such as $36,945) followed by
// IMPORT must reproduce every value and variable type.
func TestBpExportImportRoundTrip(t *testing.T) {
	s := bpRun(t, `proc export data=sashelp.cars outfile="c.csv" dbms=csv replace; run;
	proc import datafile="c.csv" out=back dbms=csv replace; getnames=yes; run;`)
	orig, back := s.Dataset("SASHELP.CARS"), s.Dataset("BACK")
	if len(back.Rows) != len(orig.Rows) || len(back.Vars) != len(orig.Vars) {
		t.Fatalf("shape %dx%d, want %dx%d", len(back.Rows), len(back.Vars), len(orig.Rows), len(orig.Vars))
	}
	for j, v := range orig.Vars {
		if back.Vars[j].Name != v.Name || back.Vars[j].Char != v.Char {
			t.Fatalf("var %d = %s/%v, want %s/%v", j, back.Vars[j].Name, back.Vars[j].Char, v.Name, v.Char)
		}
	}
	for i, r := range orig.Rows {
		for j := range orig.Vars {
			a, b := r[j], back.Rows[i][j]
			if a.Char {
				if a.Str != b.Str && " "+b.Str != a.Str {
					t.Fatalf("row %d %s: %q vs %q", i, orig.Vars[j].Name, b.Str, a.Str)
				}
			} else if !(math.IsNaN(a.Num) && math.IsNaN(b.Num)) && a.Num != b.Num {
				t.Fatalf("row %d %s: %v vs %v", i, orig.Vars[j].Name, b.Num, a.Num)
			}
		}
	}
}

func TestBpSortNodupkey(t *testing.T) {
	s := bpRun(t, `proc sort data=sashelp.class out=o nodupkey dupout=d; by sex descending age; run;`)
	o, d := s.Dataset("O"), s.Dataset("D")
	// distinct (sex, age) pairs in CLASS: F 11..15 (5), M 11..16 (6)
	if len(o.Rows) != 11 || len(d.Rows) != 8 {
		t.Fatalf("got %d kept, %d dups", len(o.Rows), len(d.Rows))
	}
	if o.Rows[0][1].Str != "F" || o.Rows[0][2].Num != 15 || o.Rows[0][0].Str != "Janet" {
		t.Fatalf("first row %v", o.Rows[0])
	}
}

func TestBpSurveySelectURS(t *testing.T) {
	s := bpRun(t, `proc surveyselect data=sashelp.cars(keep=horsepower) noprint seed=1 out=b method=urs samprate=1 outhits reps=1000; run;`)
	b := s.Dataset("B")
	if len(b.Rows) != 428000 || b.Vars[0].Name != "Replicate" || b.Vars[2].Name != "NumberHits" {
		t.Fatalf("rows=%d vars=%v", len(b.Rows), b.Vars[0].Name)
	}
}
