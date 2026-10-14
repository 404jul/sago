package sas

import "math"

// OLSFit is the result of a least-squares fit via the sweep operator.
type OLSFit struct {
	Beta    []float64   // aliased coefficients are 0
	Aliased []bool      // columns found linearly dependent on earlier ones
	XtXInv  [][]float64 // generalized inverse (G2) of X'X; zero rows/cols for aliased
	SSE     float64
	Rank    int
	N       int
	DFE     int
}

// Sweep applies the sweep operator to symmetric matrix a at pivot k (in place).
func Sweep(a [][]float64, k int) {
	n := len(a)
	d := a[k][k]
	for j := range n {
		a[k][j] /= d
	}
	for i := range n {
		if i == k {
			continue
		}
		b := a[i][k]
		for j := range n {
			a[i][j] -= b * a[k][j]
		}
		a[i][k] = -b / d
	}
	a[k][k] = 1 / d
}

// FitOLS fits y = X b by least squares (w optional weights, nil = 1). Columns
// that are (near) linear combinations of earlier columns are aliased (beta 0),
// like SAS's G2 inverse.
func FitOLS(X [][]float64, y []float64, w []float64) *OLSFit {
	n := len(y)
	p := 0
	if n > 0 {
		p = len(X[0])
	}
	m := p + 1
	a := make([][]float64, m)
	for i := range a {
		a[i] = make([]float64, m)
	}
	for r := range n {
		wt := 1.0
		if w != nil {
			wt = w[r]
		}
		row := X[r]
		for i := range p {
			xi := row[i] * wt
			for j := i; j < p; j++ {
				a[i][j] += xi * row[j]
			}
			a[i][p] += xi * y[r]
		}
		a[p][p] += wt * y[r] * y[r]
	}
	for i := range m {
		for j := range i {
			a[i][j] = a[j][i]
		}
	}
	orig := make([]float64, p)
	for i := range p {
		orig[i] = a[i][i]
	}
	fit := &OLSFit{Aliased: make([]bool, p), N: n}
	for k := range p {
		tol := 1e-9 * orig[k]
		if orig[k] == 0 || a[k][k] <= tol || math.Abs(a[k][k]) < 1e-300 {
			fit.Aliased[k] = true
			continue
		}
		Sweep(a, k)
		fit.Rank++
	}
	fit.Beta = make([]float64, p)
	fit.XtXInv = make([][]float64, p)
	for i := range p {
		fit.XtXInv[i] = make([]float64, p)
		if fit.Aliased[i] {
			continue
		}
		fit.Beta[i] = a[i][p]
		for j := range p {
			if !fit.Aliased[j] {
				fit.XtXInv[i][j] = a[i][j]
			}
		}
	}
	fit.SSE = a[p][p]
	if fit.SSE < 0 && fit.SSE > -1e-9 {
		fit.SSE = 0
	}
	fit.DFE = n - fit.Rank
	return fit
}

// Predict returns X b for one row.
func (f *OLSFit) Predict(row []float64) float64 {
	s := 0.0
	for i, b := range f.Beta {
		s += b * row[i]
	}
	return s
}

// Invert returns the inverse of a symmetric positive-definite matrix (nil if singular).
func Invert(m [][]float64) [][]float64 {
	n := len(m)
	a := make([][]float64, n)
	for i := range m {
		a[i] = append([]float64(nil), m[i]...)
	}
	for k := range n {
		if math.Abs(a[k][k]) < 1e-14 {
			return nil
		}
		Sweep(a, k)
	}
	return a
}
