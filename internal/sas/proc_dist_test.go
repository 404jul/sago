package sas

import (
	"math"
	"testing"
)

// TestDistFuncTails pins the SAS upper-tail convention of PROBT, PROBCHI
// and PROBF (P(X > x), NOT the CDF) plus the CDF convention of PROBNORM,
// PROBBNML and POISSON, and the inverse/quantile functions. Reference
// values are scipy.stats (t.sf, chi2.sf, f.sf, norm.cdf, ...ppf).
func TestDistFuncTails(t *testing.T) {
	s := NewSession(".")
	s.RunProgram(`
data crit;
  p1 = probt(2.5, 1);
  p2 = probchi(3.84, 1);
  p3 = probf(3.0, 3, 20);
  p4 = probnorm(1.96);
  p5 = probbnml(0.3, 10, 2);
  p6 = poisson(2, 2);
  c1 = tinv(0.975, 1);
  c2 = cinv(0.95, 1);
  c3 = finv(0.95, 1, 10);
  q1 = quantile('NORM', 0.975);
  q2 = quantile('NORMAL', 0.95);
run;
`)
	d := s.Dataset("CRIT")
	if d == nil {
		t.Fatal("WORK.CRIT not created")
	}
	for _, c := range []struct {
		name string
		want float64
	}{
		{"p1", 0.12111894159084335},  // t.sf(2.5, 1)
		{"p2", 0.050043521248705085}, // chi2.sf(3.84, 1)
		{"p3", 0.05485861866829514},  // f.sf(3.0, 3, 20)
		{"p4", 0.9750021048517795},   // norm.cdf(1.96)
		{"p5", 0.38278278639999974},  // binom.cdf(2, 10, 0.3)
		{"p6", 0.6766764161830636},   // poisson.cdf(2, 2)
		{"c1", 12.706204736432095},   // t.ppf(0.975, 1)
		{"c2", 3.8414588206941205},   // chi2.ppf(0.95, 1)
		{"c3", 4.9646027437307145},   // f.ppf(0.95, 1, 10)
		{"q1", 1.959963984540054},    // norm.ppf(0.975)
		{"q2", 1.644853626951472},    // norm.ppf(0.95)
	} {
		i := d.Index(c.name)
		if i < 0 {
			t.Fatalf("variable %s missing", c.name)
		}
		got := d.Rows[0][i].Num
		if math.Abs(got-c.want) > 1e-9 {
			t.Errorf("%s = %.12f, want %.12f", c.name, got, c.want)
		}
	}
}
