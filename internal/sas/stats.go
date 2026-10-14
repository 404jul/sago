package sas

import (
	"math"
	"sort"
)

// ---------------------------------------------------------------- special functions

// LogBeta returns ln B(a,b).
func LogBeta(a, b float64) float64 {
	la, _ := math.Lgamma(a)
	lb, _ := math.Lgamma(b)
	lab, _ := math.Lgamma(a + b)
	return la + lb - lab
}

// RegIncBeta is the regularized incomplete beta I_x(a,b).
func RegIncBeta(a, b, x float64) float64 {
	if math.IsNaN(x) || a <= 0 || b <= 0 {
		return math.NaN()
	}
	if x <= 0 {
		return 0
	}
	if x >= 1 {
		return 1
	}
	lbt := a*math.Log(x) + b*math.Log1p(-x) - LogBeta(a, b)
	if x < (a+1)/(a+b+2) {
		return math.Exp(lbt) * betaCF(a, b, x) / a
	}
	return 1 - math.Exp(lbt)*betaCF(b, a, 1-x)/b
}

func betaCF(a, b, x float64) float64 {
	const eps = 1e-16
	const fpmin = 1e-300
	qab, qap, qam := a+b, a+1, a-1
	c := 1.0
	d := 1 - qab*x/qap
	if math.Abs(d) < fpmin {
		d = fpmin
	}
	d = 1 / d
	h := d
	for m := 1; m <= 100000; m++ {
		fm := float64(m)
		m2 := 2 * fm
		aa := fm * (b - fm) * x / ((qam + m2) * (a + m2))
		d = 1 + aa*d
		if math.Abs(d) < fpmin {
			d = fpmin
		}
		c = 1 + aa/c
		if math.Abs(c) < fpmin {
			c = fpmin
		}
		d = 1 / d
		h *= d * c
		aa = -(a + fm) * (qab + fm) * x / ((a + m2) * (qap + m2))
		d = 1 + aa*d
		if math.Abs(d) < fpmin {
			d = fpmin
		}
		c = 1 + aa/c
		if math.Abs(c) < fpmin {
			c = fpmin
		}
		d = 1 / d
		del := d * c
		h *= del
		if math.Abs(del-1) < eps {
			break
		}
	}
	return h
}

// RegGammaP is the regularized lower incomplete gamma P(a,x).
func RegGammaP(a, x float64) float64 {
	if x <= 0 {
		return 0
	}
	if x < a+1 {
		return gammaSeries(a, x)
	}
	return 1 - gammaCF(a, x)
}

// RegGammaQ is the regularized upper incomplete gamma Q(a,x) = 1-P(a,x).
func RegGammaQ(a, x float64) float64 {
	if x <= 0 {
		return 1
	}
	if x < a+1 {
		return 1 - gammaSeries(a, x)
	}
	return gammaCF(a, x)
}

func gammaSeries(a, x float64) float64 {
	lg, _ := math.Lgamma(a)
	ap := a
	sum := 1 / a
	del := sum
	for range 100000 {
		ap++
		del *= x / ap
		sum += del
		if math.Abs(del) < math.Abs(sum)*1e-16 {
			break
		}
	}
	return sum * math.Exp(-x+a*math.Log(x)-lg)
}

func gammaCF(a, x float64) float64 {
	lg, _ := math.Lgamma(a)
	const fpmin = 1e-300
	b := x + 1 - a
	c := 1 / fpmin
	d := 1 / b
	h := d
	for i := 1; i < 100000; i++ {
		an := -float64(i) * (float64(i) - a)
		b += 2
		d = an*d + b
		if math.Abs(d) < fpmin {
			d = fpmin
		}
		c = b + an/c
		if math.Abs(c) < fpmin {
			c = fpmin
		}
		d = 1 / d
		del := d * c
		h *= del
		if math.Abs(del-1) < 1e-16 {
			break
		}
	}
	return math.Exp(-x+a*math.Log(x)-lg) * h
}

// ---------------------------------------------------------------- distributions

func NormCDF(x float64) float64 { return 0.5 * math.Erfc(-x/math.Sqrt2) }
func NormSF(x float64) float64  { return 0.5 * math.Erfc(x/math.Sqrt2) }
func NormPDF(x float64) float64 { return math.Exp(-0.5*x*x) / math.Sqrt(2*math.Pi) }

// NormInv is the standard normal quantile (Acklam + Halley refinement).
func NormInv(p float64) float64 {
	if math.IsNaN(p) || p < 0 || p > 1 {
		return math.NaN()
	}
	if p == 0 {
		return math.Inf(-1)
	}
	if p == 1 {
		return math.Inf(1)
	}
	a := []float64{-3.969683028665376e+01, 2.209460984245205e+02, -2.759285104469687e+02, 1.383577518672690e+02, -3.066479806614716e+01, 2.506628277459239e+00}
	b := []float64{-5.447609879822406e+01, 1.615858368580409e+02, -1.556989798598866e+02, 6.680131188771972e+01, -1.328068155288572e+01}
	c := []float64{-7.784894002430293e-03, -3.223964580411365e-01, -2.400758277161838e+00, -2.549732539343734e+00, 4.374664141464968e+00, 2.938163982698783e+00}
	d := []float64{7.784695709041462e-03, 3.224671290700398e-01, 2.445134137142996e+00, 3.754408661907416e+00}
	const plow = 0.02425
	var x float64
	switch {
	case p < plow:
		q := math.Sqrt(-2 * math.Log(p))
		x = (((((c[0]*q+c[1])*q+c[2])*q+c[3])*q+c[4])*q + c[5]) / ((((d[0]*q+d[1])*q+d[2])*q+d[3])*q + 1)
	case p <= 1-plow:
		q := p - 0.5
		r := q * q
		x = (((((a[0]*r+a[1])*r+a[2])*r+a[3])*r+a[4])*r + a[5]) * q / (((((b[0]*r+b[1])*r+b[2])*r+b[3])*r+b[4])*r + 1)
	default:
		q := math.Sqrt(-2 * math.Log1p(-p))
		x = -(((((c[0]*q+c[1])*q+c[2])*q+c[3])*q+c[4])*q + c[5]) / ((((d[0]*q+d[1])*q+d[2])*q+d[3])*q + 1)
	}
	for range 2 {
		e := NormCDF(x) - p
		u := e * math.Sqrt(2*math.Pi) * math.Exp(x*x/2)
		x = x - u/(1+x*u/2)
	}
	return x
}

// TCDF is the Student t CDF.
func TCDF(t, df float64) float64 {
	if math.IsNaN(t) || math.IsNaN(df) || df <= 0 {
		return math.NaN()
	}
	if math.IsInf(df, 1) {
		return NormCDF(t)
	}
	x := df / (df + t*t)
	tail := 0.5 * RegIncBeta(df/2, 0.5, x)
	if t > 0 {
		return 1 - tail
	}
	return tail
}

// TSF is P(T > t).
func TSF(t, df float64) float64 { return TCDF(-t, df) }

// TTwoSided is P(|T| > |t|), computed without cancellation.
func TTwoSided(t, df float64) float64 {
	if math.IsNaN(t) || df <= 0 {
		return math.NaN()
	}
	if math.IsInf(df, 1) {
		return 2 * NormSF(math.Abs(t))
	}
	return RegIncBeta(df/2, 0.5, df/(df+t*t))
}

func TPDF(t, df float64) float64 {
	return math.Exp(-LogBeta(0.5, df/2) - 0.5*math.Log(df) - (df+1)/2*math.Log1p(t*t/df))
}

// TInv is the Student t quantile.
func TInv(p, df float64) float64 {
	if p <= 0 || p >= 1 || df <= 0 {
		if p == 0 {
			return math.Inf(-1)
		}
		if p == 1 {
			return math.Inf(1)
		}
		return math.NaN()
	}
	if math.IsInf(df, 1) {
		return NormInv(p)
	}
	if p == 0.5 {
		return 0
	}
	// invert the tail via incomplete beta for accuracy
	tail := p
	if p > 0.5 {
		tail = 1 - p
	}
	x := invertMonotone(func(t float64) float64 { return TSF(t, df) }, tail, 0, 10, true)
	if p < 0.5 {
		return -x
	}
	return x
}

func ChiSqCDF(x, df float64) float64 {
	if x <= 0 {
		return 0
	}
	return RegGammaP(df/2, x/2)
}
func ChiSqSF(x, df float64) float64 {
	if x <= 0 {
		return 1
	}
	return RegGammaQ(df/2, x/2)
}
func ChiSqPDF(x, df float64) float64 {
	if x < 0 {
		return 0
	}
	lg, _ := math.Lgamma(df / 2)
	return math.Exp((df/2-1)*math.Log(x) - x/2 - df/2*math.Ln2 - lg)
}

func ChiSqInv(p, df float64) float64 {
	if p <= 0 {
		return 0
	}
	if p >= 1 {
		return math.Inf(1)
	}
	if p > 0.5 {
		return invertMonotone(func(x float64) float64 { return ChiSqSF(x, df) }, 1-p, 0, df+10, true)
	}
	return invertMonotone(func(x float64) float64 { return ChiSqCDF(x, df) }, p, 0, df+10, false)
}

func FCDF(f, d1, d2 float64) float64 {
	if f <= 0 {
		return 0
	}
	return RegIncBeta(d1/2, d2/2, d1*f/(d1*f+d2))
}
func FSF(f, d1, d2 float64) float64 {
	if f <= 0 {
		return 1
	}
	return RegIncBeta(d2/2, d1/2, d2/(d2+d1*f))
}
func FPDF(f, d1, d2 float64) float64 {
	if f < 0 {
		return 0
	}
	return math.Exp(0.5*(d1*math.Log(d1*f)+d2*math.Log(d2)-(d1+d2)*math.Log(d1*f+d2)) - math.Log(f) - LogBeta(d1/2, d2/2))
}
func FInv(p, d1, d2 float64) float64 {
	if p <= 0 {
		return 0
	}
	if p >= 1 {
		return math.Inf(1)
	}
	if p > 0.5 {
		return invertMonotone(func(x float64) float64 { return FSF(x, d1, d2) }, 1-p, 0, 10, true)
	}
	return invertMonotone(func(x float64) float64 { return FCDF(x, d1, d2) }, p, 0, 10, false)
}

func BetaCDF(x, a, b float64) float64 { return RegIncBeta(a, b, x) }
func BetaInv(p, a, b float64) float64 {
	lo, hi := 0.0, 1.0
	for range 200 {
		mid := (lo + hi) / 2
		if RegIncBeta(a, b, mid) < p {
			lo = mid
		} else {
			hi = mid
		}
	}
	return (lo + hi) / 2
}

func GammaCDF(x, shape, scale float64) float64 { return RegGammaP(shape, x/scale) }

// BinomPMF returns P(X=k) for Binomial(n,p).
func BinomPMF(k, n int, p float64) float64 {
	if k < 0 || k > n {
		return 0
	}
	if p == 0 {
		return b2f(k == 0)
	}
	if p == 1 {
		return b2f(k == n)
	}
	return math.Exp(LogChoose(n, k) + float64(k)*math.Log(p) + float64(n-k)*math.Log1p(-p))
}

// BinomCDF returns P(X<=k).
func BinomCDF(k, n int, p float64) float64 {
	if k < 0 {
		return 0
	}
	if k >= n {
		return 1
	}
	return RegIncBeta(float64(n-k), float64(k+1), 1-p)
}

func PoissonPMF(k int, l float64) float64 {
	if k < 0 {
		return 0
	}
	lf, _ := math.Lgamma(float64(k + 1))
	return math.Exp(float64(k)*math.Log(l) - l - lf)
}
func PoissonCDF(k int, l float64) float64 {
	if k < 0 {
		return 0
	}
	return RegGammaQ(float64(k+1), l)
}

func LogChoose(n, k int) float64 {
	a, _ := math.Lgamma(float64(n + 1))
	b, _ := math.Lgamma(float64(k + 1))
	c, _ := math.Lgamma(float64(n - k + 1))
	return a - b - c
}

// HypergeomPMF: P(X=k) drawing n from N with K successes.
func HypergeomPMF(k, N, K, n int) float64 {
	if k < 0 || k > K || k > n || n-k > N-K {
		return 0
	}
	return math.Exp(LogChoose(K, k) + LogChoose(N-K, n-k) - LogChoose(N, n))
}

func b2f(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

// invertMonotone solves f(x)=target on [lo, ∞) by bracketing + bisection.
// decreasing tells whether f decreases in x.
func invertMonotone(f func(float64) float64, target, lo, hi float64, decreasing bool) float64 {
	below := func(x float64) bool { // true if solution is above x
		v := f(x)
		if decreasing {
			return v > target
		}
		return v < target
	}
	for i := 0; i < 2000 && below(hi); i++ {
		lo = hi
		hi *= 2
	}
	for range 300 {
		mid := (lo + hi) / 2
		if mid == lo || mid == hi {
			break
		}
		if below(mid) {
			lo = mid
		} else {
			hi = mid
		}
	}
	return (lo + hi) / 2
}

// ---------------------------------------------------------------- descriptive helpers

// NonMissing returns the non-NaN values.
func NonMissing(x []float64) []float64 {
	out := make([]float64, 0, len(x))
	for _, v := range x {
		if !math.IsNaN(v) {
			out = append(out, v)
		}
	}
	return out
}

func Sum(x []float64) float64 {
	s := 0.0
	for _, v := range x {
		s += v
	}
	return s
}

func Mean(x []float64) float64 {
	if len(x) == 0 {
		return math.NaN()
	}
	return Sum(x) / float64(len(x))
}

// CSS is the corrected sum of squares (two-pass for accuracy).
func CSS(x []float64) float64 {
	m := Mean(x)
	s, c := 0.0, 0.0
	for _, v := range x {
		d := v - m
		s += d * d
		c += d
	}
	return s - c*c/float64(len(x))
}

// Variance with n-1 denominator (VARDEF=DF).
func Variance(x []float64) float64 {
	if len(x) < 2 {
		return math.NaN()
	}
	return CSS(x) / float64(len(x)-1)
}

func StdDev(x []float64) float64 { return math.Sqrt(Variance(x)) }

// Skewness (SAS VARDEF=DF formula).
func Skewness(x []float64) float64 {
	n := float64(len(x))
	if n < 3 {
		return math.NaN()
	}
	m, s := Mean(x), StdDev(x)
	if s == 0 {
		return math.NaN()
	}
	t := 0.0
	for _, v := range x {
		z := (v - m) / s
		t += z * z * z
	}
	return n / ((n - 1) * (n - 2)) * t
}

// Kurtosis (SAS VARDEF=DF excess kurtosis).
func Kurtosis(x []float64) float64 {
	n := float64(len(x))
	if n < 4 {
		return math.NaN()
	}
	m, s := Mean(x), StdDev(x)
	if s == 0 {
		return math.NaN()
	}
	t := 0.0
	for _, v := range x {
		z := (v - m) / s
		t += z * z * z * z
	}
	return n*(n+1)/((n-1)*(n-2)*(n-3))*t - 3*(n-1)*(n-1)/((n-2)*(n-3))
}

// Sorted returns a sorted copy of non-missing values.
func Sorted(x []float64) []float64 {
	s := NonMissing(x)
	sort.Float64s(s)
	return s
}

// Percentile computes the p-th percentile (0..100) of sorted data with SAS
// PCTLDEF=5 (empirical distribution function with averaging) — the default
// for PROC MEANS and PROC UNIVARIATE.
func Percentile(sorted []float64, p float64) float64 {
	n := len(sorted)
	if n == 0 {
		return math.NaN()
	}
	np := float64(n) * p / 100
	j := math.Floor(np + 1e-9*math.Max(1, np))
	g := np - j
	if math.Abs(g) < 1e-9 {
		g = 0
	}
	ji := int(j)
	if g > 0 {
		if ji >= n {
			return sorted[n-1]
		}
		return sorted[ji] // x(j+1), 0-based
	}
	if ji <= 0 {
		return sorted[0]
	}
	if ji >= n {
		return sorted[n-1]
	}
	return (sorted[ji-1] + sorted[ji]) / 2
}

// Ranks returns mid-ranks (1-based, ties averaged) of x (no missing values).
func Ranks(x []float64) []float64 {
	n := len(x)
	idx := make([]int, n)
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool { return x[idx[a]] < x[idx[b]] })
	r := make([]float64, n)
	for i := 0; i < n; {
		j := i
		for j+1 < n && x[idx[j+1]] == x[idx[i]] {
			j++
		}
		avg := float64(i+j)/2 + 1
		for k := i; k <= j; k++ {
			r[idx[k]] = avg
		}
		i = j + 1
	}
	return r
}

// Mode returns the smallest most frequent value (NaN if all unique and n>1, as SAS reports).
func Mode(sorted []float64) float64 {
	best, bestN := math.NaN(), 1
	for i := 0; i < len(sorted); {
		j := i
		for j < len(sorted) && sorted[j] == sorted[i] {
			j++
		}
		if j-i > bestN {
			best, bestN = sorted[i], j-i
		}
		i = j
	}
	if len(sorted) == 1 {
		return sorted[0]
	}
	return best
}
