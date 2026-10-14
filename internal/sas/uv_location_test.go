package sas

import (
	"math"
	"testing"
)

// TestUVSignedRankTies pins the Wilcoxon signed-rank exact distribution
// (n <= 20): ties in |d| are handled with average ranks and the exact
// conditional distribution over sign flips of the midranks. Brute-force
// enumeration (not scipy, whose exact method ignores tie patterns)
// confirms p = 70/2048 for the tied data set below.
func TestUVSignedRankTies(t *testing.T) {
	// diffs with one zero (dropped) and a tied pair of |0.2|
	x := []float64{-2.3, 0.3, -3.3, 0.0, -2.7, 0.2, -2.5, -0.2, 1.2, -0.9, -1.6, -2.2}
	s, p := uvSignedRank(x, 0)
	// T+ = 0.3(rank 3) + 0.2(rank 1.5) + 1.2(rank 5) = 9.5, n = 11
	if math.Abs(s-(-23.5)) > 1e-9 {
		t.Errorf("S = %v, want -23.5", s)
	}
	want := 70.0 / 2048.0
	if math.Abs(p-want) > 1e-12 {
		t.Errorf("exact conditional p = %v, want %v (70/2048)", p, want)
	}
}

// TestUVSignedRankNoTies checks the tie-free exact distribution against
// R's psignrank / scipy exact: diffs with n = 8, S = 1, p = 0.9453125.
func TestUVSignedRankNoTies(t *testing.T) {
	x := []float64{1.2, -3.4, 2.1, -0.7, 0.5, -1.8, 2.6, -0.3}
	s, p := uvSignedRank(x, 0)
	if math.Abs(s-1) > 1e-9 {
		t.Errorf("S = %v, want 1", s)
	}
	if math.Abs(p-0.9453125) > 1e-9 {
		t.Errorf("p = %v, want 0.9453125", p)
	}
}

// TestUVSignTest pins the sign test: M = (np - nm)/2 zeros dropped,
// two-sided binomial p.
func TestUVSignTest(t *testing.T) {
	x := []float64{-2.3, 0.3, -3.3, 0.0, -2.7, 0.2, -2.5, -0.2, 1.2, -0.9, -1.6, -2.2}
	m, p := uvSignTest(x, 0)
	// 3 positive, 8 negative among 11 non-zero
	if math.Abs(m-(-2.5)) > 1e-9 {
		t.Errorf("M = %v, want -2.5", m)
	}
	// 2 * P(X <= 3 | Binomial(11, 0.5)) = 2 * 232/2048
	want := 2 * 232.0 / 2048.0
	if math.Abs(p-want) > 1e-12 {
		t.Errorf("p = %v, want %v", p, want)
	}
}
