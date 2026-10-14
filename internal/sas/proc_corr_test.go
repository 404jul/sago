package sas

import (
	"math"
	"testing"
)

// corrSuiteData is the deterministic pharmacology-style data set from
// examples/corr_test.sas (24 obs; saturating dose-response with a
// drifting confounder).
func corrSuiteData() (dose, effect, temp []float64) {
	dose = []float64{0.5, 0.8, 1.0, 1.2, 1.5, 1.8, 2.0, 2.4, 2.8, 3.2, 3.8, 4.5,
		5.2, 6.0, 7.0, 8.5, 10.0, 12.0, 15.0, 18.0, 22.0, 27.0, 33.0, 40.0}
	effect = []float64{6.1, 7.9, 8.9, 11.1, 12.6, 14.4, 15.7, 18.0, 20.3, 22.1, 24.9, 27.2,
		31.7, 31.6, 34.7, 38.0, 41.6, 44.9, 49.1, 52.6, 56.7, 60.7, 64.8, 68.9}
	temp = []float64{18.4, 17.0, 19.6, 18.3, 20.1, 18.9, 20.9, 19.5, 21.6, 20.3, 22.9, 21.4,
		24.0, 22.6, 25.0, 23.8, 26.1, 24.7, 27.2, 25.9, 28.2, 26.9, 29.3, 28.1}
	return
}

// TestCrCoefficients pins Pearson, Spearman and Kendall tau-b against
// scipy.stats (pearsonr, spearmanr, kendalltau method='asymptotic').
func TestCrCoefficients(t *testing.T) {
	dose, effect, temp := corrSuiteData()
	for _, c := range []struct {
		name string
		x, y []float64
		want float64
	}{
		{"pearson dose-effect", dose, effect, 0.93202778},
		{"pearson dose-temp", dose, temp, 0.85454903},
		{"pearson effect-temp", effect, temp, 0.96079520},
		{"spearman dose-effect", Ranks(dose), Ranks(effect), 0.99913043},
		{"spearman dose-temp", Ranks(dose), Ranks(temp), 0.95652174},
		{"spearman effect-temp", Ranks(effect), Ranks(temp), 0.95913043},
	} {
		got := crPearson(c.x, c.y)
		if math.Abs(got-c.want) > 1e-7 {
			t.Errorf("%s = %.8f, want %.8f", c.name, got, c.want)
		}
	}
	tau, _ := crKendall(dose, effect)
	if math.Abs(tau-0.99275362) > 1e-7 {
		t.Errorf("kendall dose-effect = %.8f, want 0.99275362", tau)
	}
	tau, _ = crKendall(dose, temp)
	if math.Abs(tau-0.82608696) > 1e-7 {
		t.Errorf("kendall dose-temp = %.8f, want 0.82608696", tau)
	}
}

// TestCrPartial pins the partial Pearson correlation (sweep of the
// correlation matrix over the controlling variable) against the
// inverse-matrix formula: -P12/sqrt(P11*P22) with P = inv(R).
func TestCrPartial(t *testing.T) {
	dose, effect, temp := corrSuiteData()
	cols := [][]float64{dose, effect, temp}
	pcss := crPartialCSS(cols, 2, 1) // partial dose,effect | temp
	r := pcss[0][1] / math.Sqrt(pcss[0][0]*pcss[1][1])
	if math.Abs(r-0.77070131) > 1e-7 {
		t.Errorf("partial pearson dose,effect|temp = %.8f, want 0.77070131", r)
	}
	// Spearman partial: same sweep applied to ranked columns
	pss := crPartialCSS([][]float64{Ranks(dose), Ranks(effect), Ranks(temp)}, 2, 1)
	rs := pss[0][1] / math.Sqrt(pss[0][0]*pss[1][1])
	if math.Abs(rs-0.98996305) > 1e-7 {
		t.Errorf("partial spearman dose,effect|temp = %.8f, want 0.98996305", rs)
	}
	// partial must differ from the raw correlation (temperature is a
	// genuine confounder here)
	if math.Abs(r-crPearson(dose, effect)) < 1e-3 {
		t.Error("partial pearson too close to raw pearson")
	}
}
