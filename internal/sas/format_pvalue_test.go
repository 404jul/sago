package sas

import "testing"

// TestPVALUEFormat pins the SAS PVALUEw.d rounding rule: values that
// round to the smallest printed digit (p >= 0.5*10^-d) print as the
// rounded value; only p < 0.5*10^-d prints as <.00..1.
func TestPVALUEFormat(t *testing.T) {
	f := FormatSpec{Name: "PVALUE", D: 4, HasD: true}
	cases := []struct {
		p    float64
		want string
	}{
		{0.000054, ".0001"}, // rounds up at the 4th decimal
		{0.000049, "<.0001"},
		{0.000031, "<.0001"},
		{0.000512, ".0005"},
		{0.0372, ".0372"},
		{0.5, ".5000"},
	}
	for _, c := range cases {
		got := FormatValue(NumV(c.p), &f)
		if got != c.want {
			t.Errorf("PVALUE(%v) = %q, want %q", c.p, got, c.want)
		}
	}
}
