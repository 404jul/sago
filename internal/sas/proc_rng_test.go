package sas

import (
	"math"
	"testing"
)

// TestRanuniStream pins the SAS RANUNI recurrence
// (seed = mod(397204094*seed, 2**31-1), value = seed/(2**31-1)) and the
// stream semantics: a constant seed continues one stream across call
// sites and iterations of the DATA step.
func TestRanuniStream(t *testing.T) {
	s := NewSession(".")
	s.RunProgram(`
data uniforms;
  do i = 1 to 8;
    u = ranuni(12345);
    z = rannor(12345);
    output;
  end;
  drop i;
run;
`)
	d := s.Dataset("UNIFORMS")
	if d == nil {
		t.Fatal("WORK.UNIFORMS not created")
	}
	// documented SAS recurrence
	sref := 12345
	stream := make([]float64, 24)
	for i := range stream {
		sref = (sref * 397204094) % 2147483647
		stream[i] = float64(sref) / 2147483647.0
	}
	iu := d.Index("u")
	iz := d.Index("z")
	if iu < 0 || iz < 0 {
		t.Fatal("u/z variables missing")
	}
	for i := 0; i < 8; i++ {
		if got := d.Rows[i][iu].Num; math.Abs(got-stream[3*i]) > 1e-12 {
			t.Errorf("u[%d] = %.12f, want %.12f", i, got, stream[3*i])
		}
		u2, u3 := stream[3*i+1], stream[3*i+2]
		wantZ := math.Sqrt(-2*math.Log(u2)) * math.Cos(2*math.Pi*u3)
		if got := d.Rows[i][iz].Num; math.Abs(got-wantZ) > 1e-9 {
			t.Errorf("z[%d] = %.12f, want %.12f", i, got, wantZ)
		}
	}
}

// TestRandNormalStreamInit checks that RAND('NORMAL') with a fixed
// CALL STREAMINIT seed varies across observations and is approximately
// standard normal (SAS semantics: no mu/sigma arguments).
func TestRandNormalStreamInit(t *testing.T) {
	s := NewSession(".")
	s.RunProgram(`
data norm;
  call streaminit(7);
  do i = 1 to 4000;
    z = rand('NORMAL');
    output;
  end;
  drop i;
run;
proc means data=norm noprint;
  var z;
  output out=m mean=mz std=sz n=nz;
run;
`)
	d := s.Dataset("NORM")
	if d == nil {
		t.Fatal("WORK.NORM not created")
	}
	iz := d.Index("z")
	if iz < 0 {
		t.Fatal("z missing")
	}
	sum, sumsq := 0.0, 0.0
	prev := d.Rows[0][iz].Num
	varied := false
	for i, r := range d.Rows {
		v := r[iz].Num
		if i > 0 && v != prev {
			varied = true
		}
		prev = v
		sum += v
		sumsq += v * v
	}
	if !varied {
		t.Fatal("RAND('NORMAL') returned a constant stream")
	}
	n := float64(len(d.Rows))
	mean := sum / n
	std := math.Sqrt(sumsq/n - mean*mean)
	if math.Abs(mean) > 0.05 {
		t.Errorf("mean = %v, want ~0", mean)
	}
	if math.Abs(std-1) > 0.05 {
		t.Errorf("std = %v, want ~1", std)
	}
}

// TestSurveySelectSRS pins the SRS OUT= structure: exact sample size,
// distinct units, SelectionProb = n/N and SamplingWeight = N/n.
func TestSurveySelectSRS(t *testing.T) {
	s := NewSession(".")
	s.RunProgram(`
data plots;
  do plot = 1 to 20;
    yield = 48 + plot * 0.25;
    output;
  end;
run;
proc surveyselect data=plots method=srs n=6 seed=12345 outhits out=srs;
  id plot yield;
run;
`)
	d := s.Dataset("SRS")
	if d == nil {
		t.Fatal("WORK.SRS not created")
	}
	if len(d.Rows) != 6 {
		t.Fatalf("SRS rows = %d, want 6", len(d.Rows))
	}
	ip, iw := d.Index("SelectionProb"), d.Index("SamplingWeight")
	plots := map[float64]bool{}
	for _, r := range d.Rows {
		if r[ip].Num != 0.3 {
			t.Errorf("SelectionProb = %v, want 0.3", r[ip].Num)
		}
		if math.Abs(r[iw].Num-20.0/6.0) > 1e-9 {
			t.Errorf("SamplingWeight = %v, want %v", r[iw].Num, 20.0/6.0)
		}
		plots[r[0].Num] = true
	}
	if len(plots) != 6 {
		t.Errorf("SRS selected %d distinct plots, want 6", len(plots))
	}
}
