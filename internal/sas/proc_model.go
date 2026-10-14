package sas

import (
	"fmt"
	"math"
	"strings"
)

func init() { RegisterProc("MODEL", mdlRun) }

type mdlAssign struct {
	Name string
	E    Expr
}

type mdlCtx struct {
	s       *Session
	p       *ProcStep
	d       *Dataset
	parms   []string
	start   []float64
	prog    []mdlAssign
	noprint bool
}

var mdlNonProgram = map[string]bool{"PARMS": true, "PARAMETERS": true, "FIT": true, "BY": true, "WHERE": true, "LABEL": true, "FORMAT": true,
	"WEIGHT": true, "ID": true, "RUN": true, "QUIT": true, "ENDOGENOUS": true, "ENDO": true, "EXOGENOUS": true, "EXO": true, "VAR": true,
	"INSTRUMENTS": true, "OUTVARS": true, "SOLVE": true, "RESTRICT": true, "BOUNDS": true, "TEST": true, "ESTIMATE": true}

func mdlRun(s *Session, p *ProcStep) error {
	d, err := s.ProcData(p)
	if err != nil {
		return err
	}
	m := &mdlCtx{s: s, p: p, d: d, noprint: p.Opts.Has("NOPRINT")}
	for _, st := range p.Body {
		kw := st.Keyword()
		switch {
		case kw == "PARMS" || kw == "PARAMETERS":
			c := NewCursor(st.Args())
			for !c.AtEnd() {
				t := c.Next()
				if t.Kind != TIdent {
					continue
				}
				v := 0.0001
				c.AcceptOp("=")
				neg := false
				if c.Peek().IsOp("-") && c.PeekN(1).Kind == TNumber {
					c.Next()
					neg = true
				}
				if c.Peek().Kind == TNumber {
					v = c.Next().Num
					if neg {
						v = -v
					}
				}
				m.parms = append(m.parms, t.Text)
				m.start = append(m.start, v)
			}
		case len(st.Toks) >= 3 && st.Toks[0].Kind == TIdent && st.Toks[1].IsOp("="):
			e, err := ParseExpr(st.Toks[2:])
			if err != nil {
				return fmt.Errorf("Syntax error in model program: %v", err)
			}
			m.prog = append(m.prog, mdlAssign{Name: st.Toks[0].Text, E: e})
		case mdlNonProgram[kw]:
		}
	}
	if len(m.parms) == 0 {
		return fmt.Errorf("No parameters were specified (PARMS statement).")
	}
	fits := p.Stmts("FIT")
	if len(fits) == 0 {
		return fmt.Errorf("No FIT statement was specified.")
	}
	by, groups, err := s.ProcBy(p, d)
	if err != nil {
		return err
	}
	for _, g := range groups {
		if by != nil && !m.noprint {
			s.Out.SetBy(by.Vars, g.Keys)
		}
		for _, st := range fits {
			if err := m.fit(st, g.Rows); err != nil {
				return err
			}
		}
	}
	return nil
}

// eval runs the model program on one row with parameter values theta and
// returns the value assigned to dep (NaN if missing).
func (m *mdlCtx) eval(row []Value, theta []float64, dep string) float64 {
	vars := map[string]Value{}
	ctx := &EvalCtx{Sess: m.s, Get: func(name string) (Value, bool) {
		u := strings.ToUpper(name)
		if v, ok := vars[u]; ok {
			return v, true
		}
		for j, pn := range m.parms {
			if strings.EqualFold(pn, name) {
				return NumV(theta[j]), true
			}
		}
		if i := m.d.Index(name); i >= 0 {
			return row[i], true
		}
		return Missing, false
	}}
	res := math.NaN()
	for _, a := range m.prog {
		v := a.E.Eval(ctx)
		vars[strings.ToUpper(a.Name)] = v
		if strings.EqualFold(a.Name, dep) {
			res = v.AsNum()
		}
	}
	return res
}

func (m *mdlCtx) fit(st *Stmt, rows [][]Value) error {
	before, after := SplitSlash(st.Args())
	o := ParseOpts(after)
	var deps []string
	for _, t := range before {
		if t.Kind == TIdent {
			deps = append(deps, t.Text)
		}
	}
	if len(deps) == 0 {
		for _, a := range m.prog {
			if m.d.Index(a.Name) >= 0 {
				deps = append(deps, a.Name)
			}
		}
	}
	for _, dep := range deps {
		di := m.d.Index(dep)
		if di < 0 {
			return fmt.Errorf("Variable %s not found.", strings.ToUpper(dep))
		}
		has := false
		for _, a := range m.prog {
			has = has || strings.EqualFold(a.Name, dep)
		}
		if !has {
			return fmt.Errorf("The equation for %s is not defined.", strings.ToUpper(dep))
		}
		dep = m.d.Vars[di].Name
		// usable rows: dep nonmissing and prediction defined at start values
		var use [][]Value
		var y []float64
		for _, r := range rows {
			if r[di].IsMissing() || math.IsNaN(m.eval(r, m.start, dep)) {
				continue
			}
			use = append(use, r)
			y = append(y, r[di].Num)
		}
		n, k := len(use), len(m.parms)
		if n <= k {
			return fmt.Errorf("Not enough observations to fit %s.", dep)
		}
		theta, iters, conv := m.gaussNewton(use, y, dep)
		pred := func(th []float64) []float64 {
			out := make([]float64, n)
			for i, r := range use {
				out[i] = m.eval(r, th, dep)
			}
			return out
		}
		yh := pred(theta)
		J := m.jacobian(use, theta, dep)
		e := make([]float64, n)
		sse := 0.0
		for i := range y {
			e[i] = y[i] - yh[i]
			sse += e[i] * e[i]
		}
		dfe := n - k
		mse := sse / float64(dfe)
		r2 := 1 - sse/CSS(y)
		adj := 1 - float64(n-1)/float64(dfe)*(1-r2)
		ols := FitOLS(J, e, nil)
		if conv {
			m.s.Log.Notef(st.Line, "At OLS Iteration %d CONVERGE=0.001 Criteria Met.", iters)
		} else {
			m.s.Log.Warn(st.Line, fmt.Sprintf("The OLS estimation did not converge after %d iterations.", iters))
		}
		if m.noprint {
			continue
		}
		nt := &Table{ODSName: "ObsUsed", Caption: "Number of Observations", RowHeader: 1, Cols: []Col{{Name: "Label", Char: true}, {Name: "N"}}}
		nt.AddRow("Used", n)
		nt.AddRow("Missing", len(rows)-n)
		m.s.Out.AddTable(nt)
		rt := &Table{ODSName: "ResidSummary", Caption: "Nonlinear OLS Summary of Residual Errors", RowHeader: 1, Cols: []Col{
			{Name: "Equation", Label: "Equation", Char: true}, {Name: "ModelDF", Label: "DF Model"}, {Name: "ErrorDF", Label: "DF Error"},
			{Name: "SSE", Label: "SSE"}, {Name: "MSE", Label: "MSE"}, {Name: "RootMSE", Label: "Root MSE"},
			{Name: "RSquare", Label: "R-Square", Format: "8.4"}, {Name: "AdjRSq", Label: "Adj R-Sq", Format: "8.4"}}}
		rt.AddRow(dep, k, dfe, sse, mse, math.Sqrt(mse), r2, adj)
		m.s.Out.AddTable(rt)
		pt := &Table{ODSName: "ParameterEstimates", Caption: "Nonlinear OLS Parameter Estimates", RowHeader: 1, Cols: []Col{
			{Name: "Parameter", Label: "Parameter", Char: true}, {Name: "Estimate", Label: "Estimate"}, {Name: "StdErr", Label: "Approx Std Err"},
			{Name: "tValue", Label: "t Value", Format: lmFmtT}, {Name: "Probt", Label: "Approx Pr > |t|", Format: lmFmtP}}}
		for j, pn := range m.parms {
			if ols.Aliased[j] {
				pt.AddRow(pn, theta[j], nil, nil, nil)
				continue
			}
			se := math.Sqrt(ols.XtXInv[j][j] * mse)
			tv := theta[j] / se
			pt.AddRow(pn, theta[j], se, tv, TTwoSided(tv, float64(dfe)))
		}
		m.s.Out.AddTable(pt)
		white := o.Has("WHITE")
		bop, bp := o.Get("BREUSCH")
		if white || bp {
			ht := &Table{ODSName: "HetTest", Caption: "Heteroscedasticity Test", RowHeader: 1, Cols: []Col{
				{Name: "Equation", Label: "Equation", Char: true}, {Name: "Test", Label: "Test", Char: true}, {Name: "Statistic", Label: "Statistic", Format: "8.2"},
				{Name: "DF", Label: "DF"}, {Name: "ProbChiSq", Label: "Pr > ChiSq", Format: lmFmtP}, {Name: "Variables", Label: "Variables", Char: true}}}
			u := make([]float64, n)
			for i := range e {
				u[i] = e[i] * e[i]
			}
			eqn := dep
			if white {
				stat, df := mdlWhite(J, u)
				ht.AddRow(eqn, "White's Test", stat, df, ChiSqSF(stat, float64(df)), "Cross of all vars")
				eqn = ""
			}
			if bp {
				var names []string
				var Z [][]float64
				for _, t := range bop.Inner() {
					if t.Kind == TNumber && t.Num == 1 {
						names = append(names, "1")
						continue
					}
					if t.Kind != TIdent {
						continue
					}
					if m.d.Index(t.Text) < 0 {
						return fmt.Errorf("Variable %s not found.", strings.ToUpper(t.Text))
					}
					names = append(names, t.Text)
				}
				for _, r := range use {
					z := []float64{1}
					for _, nm := range names {
						if nm != "1" {
							z = append(z, r[m.d.Index(nm)].Num)
						}
					}
					Z = append(Z, z)
				}
				stat, df := mdlNR2(Z, u)
				ht.AddRow(eqn, "Breusch-Pagan", stat, df, ChiSqSF(stat, float64(df)), strings.Join(names, ", "))
			}
			m.s.Out.AddTable(ht)
		}
	}
	return nil
}

// mdlNR2 returns n*R^2 of the regression of u on Z (Z includes a constant column) and the DF (rank-1).
func mdlNR2(Z [][]float64, u []float64) (float64, int) {
	f := lmFitModel(Z, u, nil, true, make([]int, len(Z[0])), 1)
	r2 := 1 - f.SSE/f.TSS
	return float64(len(u)) * r2, f.Rank - 1
}

// mdlWhite computes White's test: regression of squared residuals on a
// constant and all unique products of the Jacobian columns.
func mdlWhite(J [][]float64, u []float64) (float64, int) {
	p := len(J[0])
	n := len(J)
	var cols [][]float64
	same := func(a, b []float64) bool {
		for i := range a {
			if math.Abs(a[i]-b[i]) > 1e-12*(1+math.Abs(a[i])) {
				return false
			}
		}
		return true
	}
	isConst := func(a []float64) bool {
		for i := range a {
			if math.Abs(a[i]-a[0]) > 1e-12*(1+math.Abs(a[0])) {
				return false
			}
		}
		return true
	}
	add := func(c []float64) {
		if isConst(c) {
			return
		}
		for _, x := range cols {
			if same(x, c) {
				return
			}
		}
		cols = append(cols, c)
	}
	for a := range p {
		c := make([]float64, n)
		for i := range n {
			c[i] = J[i][a]
		}
		add(c)
	}
	for a := range p {
		for b := a; b < p; b++ {
			c := make([]float64, n)
			for i := range n {
				c[i] = J[i][a] * J[i][b]
			}
			add(c)
		}
	}
	Z := make([][]float64, n)
	for i := range n {
		Z[i] = []float64{1}
		for _, c := range cols {
			Z[i] = append(Z[i], c[i])
		}
	}
	return mdlNR2(Z, u)
}

func (m *mdlCtx) jacobian(use [][]Value, theta []float64, dep string) [][]float64 {
	n, k := len(use), len(theta)
	J := make([][]float64, n)
	for i := range J {
		J[i] = make([]float64, k)
	}
	th := append([]float64(nil), theta...)
	for j := range k {
		h := 1e-6 * math.Max(math.Abs(theta[j]), 1e-2)
		th[j] = theta[j] + h
		for i, r := range use {
			fp := m.eval(r, th, dep)
			th[j] = theta[j] - h
			fm := m.eval(r, th, dep)
			th[j] = theta[j] + h
			J[i][j] = (fp - fm) / (2 * h)
		}
		th[j] = theta[j]
	}
	return J
}

// gaussNewton minimizes the residual sum of squares (Gauss-Newton with step halving).
func (m *mdlCtx) gaussNewton(use [][]Value, y []float64, dep string) ([]float64, int, bool) {
	theta := append([]float64(nil), m.start...)
	n := len(use)
	sseAt := func(th []float64) (float64, []float64) {
		yh := make([]float64, n)
		s := 0.0
		for i, r := range use {
			yh[i] = m.eval(r, th, dep)
			d := y[i] - yh[i]
			s += d * d
		}
		return s, yh
	}
	sse, yh := sseAt(theta)
	for it := 1; it <= 200; it++ {
		J := m.jacobian(use, theta, dep)
		e := make([]float64, n)
		for i := range e {
			e[i] = y[i] - yh[i]
		}
		f := FitOLS(J, e, nil)
		// R convergence criterion (SAS): sqrt(e'J (J'J)^- J'e / e'e)
		explained := sse - f.SSE
		r := 0.0
		if sse > 0 {
			r = math.Sqrt(math.Max(explained, 0) / sse)
		}
		if r < 1e-10 {
			return theta, it - 1, true
		}
		step := 1.0
		improved := false
		for range 30 {
			nt := make([]float64, len(theta))
			for j := range theta {
				nt[j] = theta[j] + step*f.Beta[j]
			}
			ns, ny := sseAt(nt)
			if !math.IsNaN(ns) && ns <= sse {
				theta, sse, yh = nt, ns, ny
				improved = true
				break
			}
			step /= 2
		}
		if !improved {
			return theta, it, r < 1e-3
		}
	}
	return theta, 200, false
}
