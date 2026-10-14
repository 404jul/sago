package sas

import (
	"fmt"
	"math"
	"strings"
)

// PROC GENMOD (gm prefix): generalized linear models with
// DIST=POISSON and LINK=LOG (log-linear regression), optionally with an
// OFFSET= exposure variable. Maximum likelihood by Newton-Raphson.
// ODS tables: GOF (Criteria For Assessing Goodness Of Fit),
// ParameterEstimates.

func init() { RegisterProc("GENMOD", gmGenmod) }

func gmGenmod(s *Session, p *ProcStep) error {
	d, err := s.ProcData(p)
	if err != nil {
		return err
	}
	mst := p.First("MODEL")
	if mst == nil || len(mst.Args()) == 0 {
		return fmt.Errorf("The MODEL statement is required.")
	}
	txt := TokText(mst.Args())
	k := strings.Index(txt, "=")
	if k < 0 {
		return fmt.Errorf("MODEL statement must be of the form response=effects.")
	}
	respName := strings.TrimSpace(txt[:k])
	rhs := strings.TrimSpace(txt[k+1:])
	// MODEL options after '/' override PROC-level DIST=/LINK=/OFFSET=
	modelOpts := &Opts{}
	if q := strings.Index(rhs, "/"); q >= 0 {
		toks, terr := Tokenize(rhs[q+1:], 0)
		if terr == nil {
			modelOpts = ParseOpts(toks)
		}
		rhs = strings.TrimSpace(rhs[:q])
	}
	yi := d.Index(respName)
	if yi < 0 {
		return fmt.Errorf("Variable %s not found.", strings.ToUpper(respName))
	}
	if rhs == "" {
		return fmt.Errorf("No explanatory variables on the MODEL statement.")
	}
	var names []string
	for _, f := range strings.Fields(rhs) {
		names = append(names, f)
	}
	cols := make([]int, len(names))
	for i, n := range names {
		cols[i] = d.Index(n)
		if cols[i] < 0 {
			return fmt.Errorf("Variable %s not found.", strings.ToUpper(n))
		}
	}
	// options: DIST=, LINK=, OFFSET= (MODEL options override PROC options)
	procOpts := p.Opts
	dist := strings.ToUpper(modelOpts.Str("DIST", procOpts.Str("ERROR", "POISSON")))
	link := strings.ToUpper(modelOpts.Str("LINK", procOpts.Str("LINK", "LOG")))
	if dist != "POISSON" || link != "LOG" {
		return fmt.Errorf("Only DIST=POISSON with LINK=LOG is supported (got DIST=%s LINK=%s).", dist, link)
	}
	oi := -1
	if on := modelOpts.Str("OFFSET", procOpts.Str("OFFSET", "")); on != "" {
		oi = d.Index(on)
		if oi < 0 {
			return fmt.Errorf("Variable %s not found.", strings.ToUpper(on))
		}
	}

	type gmObs struct {
		y, off float64
		x      []float64
	}
	var obs []gmObs
	for _, r := range d.Rows {
		if r[yi].IsMissing() {
			continue
		}
		x := make([]float64, len(cols)+1)
		x[0] = 1
		skip := false
		for i, c := range cols {
			if r[c].IsMissing() {
				skip = true
				break
			}
			x[i+1] = r[c].Num
		}
		if skip {
			continue
		}
		off := 0.0
		if oi >= 0 {
			if r[oi].IsMissing() {
				continue
			}
			off = r[oi].Num
		}
		if r[yi].Num < 0 {
			return fmt.Errorf("Nonpositive response values are invalid for DIST=POISSON.")
		}
		obs = append(obs, gmObs{y: r[yi].Num, off: off, x: x})
	}
	n := len(obs)
	if n == 0 {
		return fmt.Errorf("No valid observations for the model.")
	}
	nvar := len(names) + 1

	// Newton-Raphson on the Poisson log-likelihood:
	//   logL = sum(y*eta - exp(eta) - lgamma(y+1)), eta = off + x'beta
	//   score = X'(y - mu), info = X'W X with W = diag(mu)
	beta := make([]float64, nvar)
	llKernel, llFull := 0.0, 0.0
	for iter := 0; iter < 100; iter++ {
		score := make([]float64, nvar)
		info := make([][]float64, nvar)
		for i := range info {
			info[i] = make([]float64, nvar)
		}
		llKernel, llFull = 0.0, 0.0
		for _, o := range obs {
			eta := o.off
			for i := 0; i < nvar; i++ {
				eta += beta[i] * o.x[i]
			}
			mu := math.Exp(eta)
			llKernel += o.y*eta - mu
			llFull += o.y*eta - mu - lgammaF(o.y+1)
			for i := 0; i < nvar; i++ {
				score[i] += (o.y - mu) * o.x[i]
				for j := 0; j < nvar; j++ {
					info[i][j] += mu * o.x[i] * o.x[j]
				}
			}
		}
		// step = info^-1 score with step halving
		A := make([][]float64, nvar)
		b := append([]float64{}, score...)
		for i := range A {
			A[i] = append([]float64{}, info[i]...)
		}
		sing := false
		for piv := 0; piv < nvar; piv++ {
			if math.Abs(A[piv][piv]) < 1e-12 {
				sing = true
				break
			}
			for r := piv + 1; r < nvar; r++ {
				f := A[r][piv] / A[piv][piv]
				for l := piv; l < nvar; l++ {
					A[r][l] -= f * A[piv][l]
				}
				b[r] -= f * b[piv]
			}
		}
		if sing {
			return fmt.Errorf("The information matrix is singular.")
		}
		step := make([]float64, nvar)
		for i := nvar - 1; i >= 0; i-- {
			sv := b[i]
			for l := i + 1; l < nvar; l++ {
				sv -= A[i][l] * step[l]
			}
			step[i] = sv / A[i][i]
		}
		if math.Abs(dot(score, step)) < 1e-10 {
			break
		}
		for i := range beta {
			beta[i] += step[i]
		}
	}
	// recompute at the final beta (the loop above may lag one iteration)
	{
		score := make([]float64, nvar)
		info := make([][]float64, nvar)
		for i := range info {
			info[i] = make([]float64, nvar)
		}
		llKernel, llFull = 0.0, 0.0
		dev, pearson := 0.0, 0.0
		for _, o := range obs {
			eta := o.off
			for i := 0; i < nvar; i++ {
				eta += beta[i] * o.x[i]
			}
			mu := math.Exp(eta)
			llKernel += o.y*eta - mu
			llFull += o.y*eta - mu - lgammaF(o.y+1)
			if o.y > 0 {
				dev += 2 * (o.y*math.Log(o.y/mu) - (o.y - mu))
			} else {
				dev += 2 * mu
			}
			pearson += (o.y - mu) * (o.y - mu) / mu
			for i := 0; i < nvar; i++ {
				score[i] += (o.y - mu) * o.x[i]
				for j := 0; j < nvar; j++ {
					info[i][j] += mu * o.x[i] * o.x[j]
				}
			}
		}
		inv := phMatInv(info)
		df := float64(n - nvar)
		t := &Table{ODSName: "GOF", Caption: "Criteria For Assessing Goodness Of Fit", MissDot: true}
		t.Cols = []Col{
			{Name: "Criterion", Label: "Criterion", Char: true},
			{Name: "DF", Label: "DF", Format: "6."},
			{Name: "Value", Label: "Value", Format: "12.4"},
			{Name: "ValueDF", Label: "Value/DF", Format: "10.4"},
		}
		t.RowHeader = 1
		t.AddRow("Deviance", df, dev, dev/df)
		t.AddRow("Scaled Deviance", df, dev, dev/df)
		t.AddRow("Pearson Chi-Square", df, pearson, pearson/df)
		t.AddRow("Scaled Pearson X2", df, pearson, pearson/df)
		t.AddRow("Log Likelihood", Missing, llKernel)
		t.AddRow("Full Log Likelihood", Missing, llFull)
		npar := float64(nvar)
		aic := -2*llFull + 2*npar
		aicc := aic + 2*npar*(npar+1)/(float64(n)-npar-1)
		bic := -2*llFull + npar*math.Log(float64(n))
		t.AddRow("AIC (smaller is better)", Missing, aic)
		t.AddRow("AICC (smaller is better)", Missing, aicc)
		t.AddRow("BIC (smaller is better)", Missing, bic)
		s.Out.AddTable(t)

		t2 := &Table{ODSName: "ParameterEstimates", Caption: "Analysis Of Maximum Likelihood Parameter Estimates", MissDot: true}
		t2.Cols = []Col{
			{Name: "Parameter", Label: "Parameter", Char: true},
			{Name: "DF", Label: "DF", Format: "4."},
			{Name: "Estimate", Label: "Estimate", Format: "10.4"},
			{Name: "StdErr", Label: "Standard\nError", Format: "10.4"},
			{Name: "WaldChiSq", Label: "Wald\nChi-Square", Format: "8.4"},
			{Name: "ProbChiSq", Label: "Pr > ChiSq", Format: "PVALUE6.4"},
		}
		t2.RowHeader = 1
		t2.AddRow("Intercept", 1.0, beta[0], math.Sqrt(inv[0][0]), beta[0]*beta[0]/inv[0][0], ChiSqSF(beta[0]*beta[0]/inv[0][0], 1))
		for i, nme := range names {
			est := beta[i+1]
			se := math.Sqrt(inv[i+1][i+1])
			t2.AddRow(nme, 1.0, est, se, est*est/(se*se), ChiSqSF(est*est/(se*se), 1))
		}
		s.Out.AddTable(t2)
	}
	return nil
}

func lgammaF(x float64) float64 {
	v, _ := math.Lgamma(x)
	return v
}
