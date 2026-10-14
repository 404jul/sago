package sas

import (
	"math"
	"testing"
)

// TestRegOutputL95Aliases covers the SAS OUTPUT statement synonyms
// L95=/U95= (prediction limits) and L95M=/U95M= (confidence limits),
// which are aliases of LCL=/UCL=/LCLM=/UCLM=.
func TestRegOutputL95Aliases(t *testing.T) {
	s := NewSession(".")
	s.RunProgram(`
data t;
  input x y;
  datalines;
1 2
2 3
3 5
;
proc reg data=t;
  model y = x;
  output out=o p=ph l95=lo u95=hi l95m=lom u95m=him;
run;
`)
	o := s.Dataset("O")
	if o == nil {
		t.Fatal("WORK.O not created")
	}
	for _, want := range []string{"ph", "lo", "hi", "lom", "him"} {
		if o.Index(want) < 0 {
			t.Fatalf("variable %s missing from WORK.O: %v", want, o.Vars)
		}
	}
	// x=2 has an exact fit through (2,3): predicted 3; CLM width from
	// t(0.975,1) * stderr of the mean prediction.
	ph := colVals(o, "ph")
	lo := colVals(o, "lo")
	hi := colVals(o, "hi")
	lom := colVals(o, "lom")
	him := colVals(o, "him")
	if ph == nil || lo == nil || hi == nil || lom == nil || him == nil {
		t.Fatal("missing columns")
	}
	for i := 0; i < 3; i++ {
		if lo[i].Num >= ph[i].Num || ph[i].Num >= hi[i].Num {
			t.Errorf("row %d: prediction limits do not bracket the fit: %v %v %v", i, lo[i].Num, ph[i].Num, hi[i].Num)
		}
		if lom[i].Num < lo[i].Num || him[i].Num > hi[i].Num {
			t.Errorf("row %d: CL mean limits must lie inside prediction limits", i)
		}
	}
	// exact values at x=2 (the mean of x): yhat = 10/3, h = 1/3, mse = 1/6
	// CLM: yhat +/- t(0.975,1)*sqrt(mse*h); CLI: yhat +/- t*sqrt(mse*(1+h))
	tcrit := TInv(0.975, 1)
	yhat := 10.0 / 3.0
	stdp := math.Sqrt((1.0 / 6.0) * (1.0 / 3.0))
	if math.Abs(ph[1].Num-yhat) > 1e-9 {
		t.Errorf("fitted value at x=2 = %v, want %v", ph[1].Num, yhat)
	}
	if math.Abs(lom[1].Num-(yhat-tcrit*stdp)) > 1e-9 || math.Abs(him[1].Num-(yhat+tcrit*stdp)) > 1e-9 {
		t.Errorf("CLM at x=2 = [%v,%v], want [%.9f,%.9f]", lom[1].Num, him[1].Num, yhat-tcrit*stdp, yhat+tcrit*stdp)
	}
	stdi := math.Sqrt((1.0 / 6.0) * (1 + 1.0/3.0))
	if math.Abs(lo[1].Num-(yhat-tcrit*stdi)) > 1e-9 || math.Abs(hi[1].Num-(yhat+tcrit*stdi)) > 1e-9 {
		t.Errorf("CLI at x=2 = [%v,%v], want [%.9f,%.9f]", lo[1].Num, hi[1].Num, yhat-tcrit*stdi, yhat+tcrit*stdi)
	}
}

func colVals(d *Dataset, name string) []Value {
	i := d.Index(name)
	if i < 0 {
		return nil
	}
	out := make([]Value, len(d.Rows))
	for r := range d.Rows {
		out[r] = d.Rows[r][i]
	}
	return out
}
