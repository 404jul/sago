package sas

import (
	"math"
)

// uvNormTest is one row of the TestsForNormality table.
type uvNormTest struct {
	Test, Lab, PType string
	Stat, P          float64
	Sign             string // "", "<" or ">"
}

// uvShapiroWilk computes the Shapiro-Wilk W statistic and Pr < W for sorted
// data (Royston 1992/1995, algorithm AS R94; the method PROC UNIVARIATE uses).
func uvShapiroWilk(y []float64) (float64, float64) {
	n := len(y)
	if n < 3 {
		return math.NaN(), math.NaN()
	}
	mean := Mean(y)
	den := 0.0
	for _, v := range y {
		den += (v - mean) * (v - mean)
	}
	if den == 0 {
		return math.NaN(), math.NaN()
	}
	wstat := func(a []float64) float64 {
		num := 0.0
		for i, v := range y {
			num += a[i] * v
		}
		return num * num / den
	}
	if n == 3 {
		c := math.Sqrt2 / 2
		w := math.Min(math.Max(wstat([]float64{-c, 0, c}), 0.75), 1)
		p := 1 - 6/math.Pi*math.Acos(math.Sqrt(w))
		return w, math.Min(math.Max(p, 0), 1)
	}
	fn := float64(n)
	m := make([]float64, n)
	mTm := 0.0
	for i := range m {
		m[i] = NormInv((float64(i+1) - 0.375) / (fn + 0.25))
		mTm += m[i] * m[i]
	}
	u := 1 / math.Sqrt(fn)
	cn := m[n-1] / math.Sqrt(mTm)
	cnm1 := m[n-2] / math.Sqrt(mTm)
	an := cn + 0.221157*u - 0.147981*u*u - 2.071190*u*u*u + 4.434685*math.Pow(u, 4) - 2.706056*math.Pow(u, 5)
	anm1 := cnm1 + 0.042981*u - 0.293762*u*u - 1.752461*u*u*u + 5.682633*math.Pow(u, 4) - 3.582633*math.Pow(u, 5)
	var phi float64
	if n <= 5 {
		phi = (mTm - 2*m[n-1]*m[n-1]) / (1 - 2*an*an)
	} else {
		phi = (mTm - 2*m[n-1]*m[n-1] - 2*m[n-2]*m[n-2]) / (1 - 2*an*an - 2*anm1*anm1)
	}
	a := make([]float64, n)
	for i := range a {
		a[i] = m[i] / math.Sqrt(phi)
	}
	if n > 5 {
		a[n-2], a[1] = anm1, -anm1
	}
	a[n-1], a[0] = an, -an
	w := wstat(a)
	var mu, sigma, g float64
	if n <= 11 {
		gamma := -2.273 + 0.459*fn
		mu = 0.5440 - 0.39978*fn + 0.025054*fn*fn - 0.0006714*fn*fn*fn
		sigma = math.Exp(1.3822 - 0.77857*fn + 0.062767*fn*fn - 0.0020322*fn*fn*fn)
		g = -math.Log(gamma - math.Log(1-w))
	} else {
		l := math.Log(fn)
		mu = -1.5861 - 0.31082*l - 0.083751*l*l + 0.0038915*l*l*l
		sigma = math.Exp(-0.4803 - 0.082676*l + 0.0030302*l*l)
		g = math.Log(1 - w)
	}
	return w, NormSF((g - mu) / sigma)
}

// uvEDF computes the Kolmogorov-Smirnov D, Cramer-von Mises W-Sq and
// Anderson-Darling A-Sq statistics of sorted data against a normal
// distribution with the given mean and standard deviation.
func uvEDF(y []float64, mu, sigma float64) (d, w2, a2 float64) {
	n := len(y)
	fn := float64(n)
	dp, dm := 0.0, 0.0
	for i, v := range y {
		u := NormCDF((v - mu) / sigma)
		fi := float64(i + 1)
		dp = math.Max(dp, fi/fn-u)
		dm = math.Max(dm, u-(fi-1)/fn)
		c := u - (2*fi-1)/(2*fn)
		w2 += c * c
		uc := NormCDF(-(y[n-1-i] - mu) / sigma) // 1 - u_(n+1-i)
		a2 += (2*fi - 1) * (math.Log(u) + math.Log(uc))
	}
	w2 += 1 / (12 * fn)
	a2 = -fn - a2/fn
	return math.Max(dp, dm), w2, a2
}

// uvTableP interpolates linearly in D'Agostino & Stephens (1986) Table 4.7
// (normal, mean and variance estimated) upper-tail percentage points.
// Returns the p-value and a bound sign when outside the table.
func uvTableP(stat float64, crit, alpha []float64) (float64, string) {
	if stat < crit[0] {
		return alpha[0], ">"
	}
	last := len(crit) - 1
	if stat > crit[last] {
		return alpha[last], "<"
	}
	for i := range last {
		if stat <= crit[i+1] {
			t := (stat - crit[i]) / (crit[i+1] - crit[i])
			return alpha[i] + t*(alpha[i+1]-alpha[i]), ""
		}
	}
	return alpha[last], ""
}

var (
	uvKSCrit   = []float64{0.775, 0.819, 0.895, 0.955, 1.035}
	uvKSAlpha  = []float64{0.15, 0.10, 0.05, 0.025, 0.01}
	uvCvMCrit  = []float64{0.074, 0.091, 0.104, 0.126, 0.148, 0.179, 0.201}
	uvADCrit   = []float64{0.470, 0.561, 0.631, 0.752, 0.873, 1.035, 1.159}
	uvQuadAlph = []float64{0.25, 0.15, 0.10, 0.05, 0.025, 0.01, 0.005}
)

// uvGoodnessOfFit returns the EDF tests (KS, CvM, AD) for a normal fit with
// estimated mean/sd, with SAS's table-interpolated p-values.
func uvGoodnessOfFit(y []float64, mu, sigma float64) []uvNormTest {
	n := float64(len(y))
	d, w2, a2 := uvEDF(y, mu, sigma)
	sn := math.Sqrt(n)
	pd, sd := uvTableP(d*(sn-0.01+0.85/sn), uvKSCrit, uvKSAlpha)
	pw, sw := uvTableP(w2*(1+0.5/n), uvCvMCrit, uvQuadAlph)
	pa, sa := uvTableP(a2*(1+0.75/n+2.25/(n*n)), uvADCrit, uvQuadAlph)
	return []uvNormTest{
		{"Kolmogorov-Smirnov", "D", "Pr > D", d, pd, sd},
		{"Cramer-von Mises", "W-Sq", "Pr > W-Sq", w2, pw, sw},
		{"Anderson-Darling", "A-Sq", "Pr > A-Sq", a2, pa, sa},
	}
}

// uvNormalityTests returns the TestsForNormality rows for sorted data.
func uvNormalityTests(y []float64) []uvNormTest {
	var out []uvNormTest
	if len(y) <= 2000 {
		w, p := uvShapiroWilk(y)
		out = append(out, uvNormTest{"Shapiro-Wilk", "W", "Pr < W", w, p, ""})
	}
	if len(y) < 2 {
		return out
	}
	sd := StdDev(y)
	if sd == 0 || math.IsNaN(sd) {
		return out
	}
	return append(out, uvGoodnessOfFit(y, Mean(y), sd)...)
}
