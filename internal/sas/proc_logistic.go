package sas

import (
	"fmt"
	"math"
	"strings"
)

// PROC LOGISTIC: binary-response logistic regression fit by
// Newton-Raphson maximum likelihood. Numeric predictors only
// (CLASS variables must be pre-recoded as indicators). Per SAS, the
// default models P(lowest ordered response level); DESCENDING reverses.

func init() { RegisterProc("LOGISTIC", lgRun) }

func lgRun(s *Session, p *ProcStep) error {
	d, err := s.ProcData(p)
	if err != nil {
		return err
	}
	by, groups, err := s.ProcBy(p, d)
	if err != nil {
		return err
	}
	// CLASS predictors: reference (dummy) coding only. Default reference
	// is the last sorted level; per-variable (ref='level') or global
	// REF= overrides; PARAM= must be REF.
	classSpec := map[string]string{}
	if cs := p.First("CLASS", "CLASSES"); cs != nil {
		before, after := SplitSlash(cs.Args())
		param := strings.ToUpper(ParseOpts(after).Str("PARAM", "REF"))
		if param != "REF" {
			return fmt.Errorf("LOGISTIC: only PARAM=REF is supported for CLASS variables.")
		}
		globalRef := ""
		if op, ok := ParseOpts(after).Get("REF"); ok {
			globalRef = op.Str()
		}
		c := NewCursor(before)
		for !c.AtEnd() {
			t := c.Next()
			if t.Kind != TIdent {
				continue
			}
			name := strings.ToUpper(t.Text)
			ref := globalRef
			// per-variable (ref='level') suboption
			if c.Peek().IsOp("(") {
				c.P++ // consume (
				for !c.AtEnd() && !c.Peek().IsOp(")") {
					tk := c.Next()
					if tk.Upper() == "REF" && c.AcceptOp("=") {
						ref = c.Next().Text
					}
				}
				if c.Peek().IsOp(")") {
					c.Next()
				}
			}
			classSpec[name] = ref
		}
	}
	desc := p.Opts.Has("DESCENDING", "DESC")
	models := p.Stmts("MODEL")
	if len(models) == 0 {
		return fmt.Errorf("no MODEL statement.")
	}
	st := models[0]
	resp, preds, ok := lgParseModel(st.Args())
	if !ok {
		return fmt.Errorf("invalid MODEL statement.")
	}
	ri := d.Index(resp)
	if ri < 0 {
		return fmt.Errorf("Variable %s not found.", strings.ToUpper(resp))
	}
	var terms [][]int
	for _, term := range preds {
		var fs []int
		for _, v := range strings.Split(term, "\x00") {
			i := d.Index(v)
			if i < 0 {
				return fmt.Errorf("Variable %s not found.", strings.ToUpper(v))
			}
			if d.Vars[i].Char && classSpec[strings.ToUpper(v)] == "" {
				return fmt.Errorf("LOGISTIC: character variable %s must be a CLASS variable or recoded as numeric.", strings.ToUpper(v))
			}
			fs = append(fs, i)
		}
		terms = append(terms, fs)
	}
	for _, g := range groups {
		sub := d.Subset(g.Rows)
		if by != nil {
			s.Out.SetBy(by.Vars, g.Keys)
		}
		if err := lgFit(s, d.Vars[ri].Name, desc, sub, ri, terms, classSpec); err != nil {
			return err
		}
	}
	return nil
}

// lgParseModel splits MODEL args into response and predictor names.
func lgParseModel(toks []Token) (string, []string, bool) {
	c := NewCursor(toks)
	if c.AtEnd() || c.Peek().Kind != TIdent {
		return "", nil, false
	}
	resp := c.Next().Text
	if !c.AcceptOp("=") {
		return "", nil, false
	}
	var rhs []Token
	for !c.AtEnd() {
		if c.Peek().IsOp("/") {
			break
		}
		rhs = append(rhs, c.Next())
	}
	// expand bar/star notation: a|b -> a, b, a*b
	alternatives := [][]Token{}
	cur := []Token{}
	depth := 0
	for _, t := range rhs {
		switch {
		case t.IsOp("("):
			depth++
			cur = append(cur, t)
		case t.IsOp(")"):
			depth--
			cur = append(cur, t)
		case t.IsOp("|") && depth == 0:
			alternatives = append(alternatives, cur)
			cur = nil
		default:
			cur = append(cur, t)
		}
	}
	alternatives = append(alternatives, cur)
	// no bar: plain adjacent identifiers are separate single-variable
	// terms; factors joined by * form one product term
	if len(alternatives) == 1 {
		var preds []string
		cc := NewCursor(alternatives[0])
		var term []string
		flush := func() {
			if len(term) > 0 {
				preds = append(preds, strings.Join(term, "\x00"))
				term = nil
			}
		}
		for !cc.AtEnd() {
			t := cc.Next()
			switch {
			case t.Kind == TIdent:
				// a new identifier right after another identifier (no *)
				// starts a new term; after * it continues the product
				if len(term) > 0 && term[len(term)-1] != "\x00" {
					flush()
				}
				term = append(term, t.Text)
			case t.IsOp("*"):
				term = append(term, "\x00")
			default:
				flush()
			}
		}
		flush()
		return resp, preds, true
	}
	if len(alternatives) > 3 {
		return "", nil, false
	}
	// all non-empty subsets (SAS bar expansion), product order preserved
	var preds []string
	for mask := 1; mask < 1<<len(alternatives); mask++ {
		var prod []string
		for b := 0; b < len(alternatives); b++ {
			if mask&(1<<b) == 0 {
				continue
			}
			cc := NewCursor(alternatives[b])
			for !cc.AtEnd() {
				t := cc.Next()
				if t.Kind == TIdent {
					prod = append(prod, t.Text)
				} else if t.IsOp("*") {
					prod = append(prod, "*")
				}
			}
		}
		// drop leading/trailing/duplicate stars
		var clean []string
		for _, p := range prod {
			if p == "*" && (len(clean) == 0 || clean[len(clean)-1] == "*") {
				continue
			}
			clean = append(clean, p)
		}
		if len(clean) > 0 && clean[len(clean)-1] == "*" {
			clean = clean[:len(clean)-1]
		}
		preds = append(preds, strings.Join(clean, "\x00"))
	}
	return resp, preds, true
}

// lgFactor describes one factor of a MODEL term: a numeric variable
// (one column) or a CLASS variable (k-1 reference-coded dummies).
type lgFactor struct {
	name   string
	ci     int
	class  bool
	levels []string // sorted formatted levels (class only)
	ref    string   // reference level (class only)
}

// lgFit fits one logistic model and emits the SAS-style tables.
func lgFit(s *Session, resp string, desc bool, d *Dataset, ri int, terms [][]int, classSpec map[string]string) error {
	// response levels: ascending, reversed by DESCENDING; modeled level
	// is Ordered Value 1 (SAS models the probability of the lower level)
	levels := UniqueSorted(d.Col(ri))
	if len(levels) != 2 {
		return fmt.Errorf("LOGISTIC: response variable %s must have exactly two levels, found %d.", resp, len(levels))
	}
	if desc {
		levels[0], levels[1] = levels[1], levels[0]
	}
	// factor descriptors: numeric vars contribute 1 column; CLASS vars
	// contribute k-1 reference-coded dummies (reference = REF= level or
	// the last sorted level, per SAS PARAM=REF)
	mkFactor := func(ci int) lgFactor {
		name := d.Vars[ci].Name
		if ref, isClass := classSpec[strings.ToUpper(name)]; isClass {
			lv := UniqueSorted(d.Col(ci))
			var strs []string
			for _, v := range lv {
				strs = append(strs, FormatValue(v, nil))
			}
			refIdx := len(strs) - 1 // SAS PARAM=REF default: last level
			if ref != "" {
				for i, s := range strs {
					if strings.EqualFold(s, ref) {
						refIdx = i
					}
				}
			}
			return lgFactor{name: name, ci: ci, class: true, levels: strs, ref: strs[refIdx]}
		}
		return lgFactor{name: name, ci: ci}
	}
	// term columns: a term with c CLASS factors and any number of numeric
	// factors gets (product of class dummy counts) columns; each column is
	// the product of one class indicator per class factor and the numeric
	// factor values. Parameter name joins the pieces with "*".
	type lgComb struct {
		col    int
		name   string
		levels []string // chosen level per factor ("" for numeric factors)
	}
	termCombos := make([][]lgComb, len(terms))
	termFactors := make([][]lgFactor, len(terms))
	paramNames := []string{"Intercept"}
	nCol := 1
	for ti, fs := range terms {
		factors := make([]lgFactor, len(fs))
		combos := []lgComb{{name: ""}}
		for k, ci := range fs {
			f := mkFactor(ci)
			factors[k] = f
			var pieces []string
			if f.class {
				for _, lv := range f.levels {
					if lv != f.ref {
						pieces = append(pieces, lv)
					}
				}
			} else {
				pieces = []string{""}
			}
			next := make([]lgComb, 0, len(combos)*len(pieces))
			for _, cb := range combos {
				for _, pc := range pieces {
					nc := cb
					nc.levels = append(append([]string{}, cb.levels...), pc)
					nm := cb.name
					if pc != "" {
						if nm != "" {
							nm += "*"
						}
						nm += f.name + " " + pc
					} else {
						if nm != "" {
							nm += "*"
						}
						nm += f.name
					}
					nc.name = nm
					next = append(next, nc)
				}
			}
			combos = next
		}
		for i := range combos {
			combos[i].col = nCol
			nCol++
		}
		termCombos[ti] = combos
		termFactors[ti] = factors
		for _, cb := range combos {
			paramNames = append(paramNames, cb.name)
		}
	}
	// design matrix: complete cases, intercept + predictors
	var X, y []float64
	for _, row := range d.Rows {
		if row[ri].Num != row[ri].Num { // missing response
			continue
		}
		ok := true
		xr := make([]float64, nCol)
		xr[0] = 1
		for ti, fs := range terms {
			for _, cb := range termCombos[ti] {
				v := 1.0
				for k, ci := range fs {
					f := termFactors[ti][k]
					if row[ci].Num != row[ci].Num {
						ok = false
						break
					}
					if f.class {
						if FormatValue(row[ci], nil) != cb.levels[k] {
							v = 0
						}
					} else {
						v *= row[ci].Num
					}
				}
				xr[cb.col] = v
			}
			if !ok {
				break
			}
		}
		if !ok {
			continue
		}
		if FormatValue(row[ri], nil) == FormatValue(levels[0], nil) {
			y = append(y, 1)
		} else {
			y = append(y, 0)
		}
		X = append(X, xr...)
	}
	n := len(y)
	if n == 0 {
		return fmt.Errorf("LOGISTIC: no observations.")
	}
	k := nCol

	logit := func(eta float64) float64 {
		if eta > 500 {
			return 1
		}
		if eta < -500 {
			return 0
		}
		return 1 / (1 + math.Exp(-eta))
	}
	neg2LL := func(b []float64) float64 {
		ll := 0.0
		for i := 0; i < n; i++ {
			eta := 0.0
			for j := 0; j < k; j++ {
				eta += X[i*k+j] * b[j]
			}
			pr := logit(eta)
			if y[i] == 1 {
				if pr <= 0 {
					return math.Inf(1)
				}
				ll += math.Log(pr)
			} else {
				if pr >= 1 {
					return math.Inf(1)
				}
				ll += math.Log1p(-pr)
			}
		}
		return -2 * ll
	}
	ll0 := func() float64 {
		pbar := 0.0
		for _, v := range y {
			pbar += v
		}
		pbar /= float64(n)
		ll := 0.0
		for i := 0; i < n; i++ {
			pr := pbar
			if y[i] == 1 {
				ll += math.Log(pr)
			} else {
				ll += math.Log1p(-pr)
			}
		}
		return -2 * ll
	}
	ll0v := ll0()

	// Newton-Raphson with step halving: start at intercept = logit(ybar),
	// slopes 0; each accepted step must decrease -2 log likelihood
	// (the logistic log likelihood is concave, so this converges to the
	// MLE even where the raw Newton step overshoots).
	b := make([]float64, k)
	pbar0 := 0.0
	for _, v := range y {
		pbar0 += v
	}
	pbar0 /= float64(n)
	if pbar0 > 0 && pbar0 < 1 {
		b[0] = math.Log(pbar0 / (1 - pbar0))
	}
	fCur := neg2LL(b)
	for it := 0; it < 200; it++ {
		H := make([]float64, k*k)
		g := make([]float64, k)
		for i := 0; i < n; i++ {
			eta := 0.0
			for j := 0; j < k; j++ {
				eta += X[i*k+j] * b[j]
			}
			pr := logit(eta)
			w := pr * (1 - pr)
			if w < 1e-12 {
				w = 1e-12
			}
			r := y[i] - pr
			for a := 0; a < k; a++ {
				g[a] += X[i*k+a] * r
				for c := 0; c < k; c++ {
					H[a*k+c] += X[i*k+a] * X[i*k+c] * w
				}
			}
		}
		Hm := make([][]float64, k)
		for a := 0; a < k; a++ {
			Hm[a] = H[a*k : (a+1)*k]
		}
		Hinv := Invert(Hm)
		if Hinv == nil {
			return fmt.Errorf("LOGISTIC: the information matrix is singular (complete separation?).")
		}
		step := make([]float64, k)
		for a := 0; a < k; a++ {
			for c := 0; c < k; c++ {
				step[a] += Hinv[a][c] * g[c]
			}
		}
		// backtracking line search on -2 log likelihood
		t := 1.0
		accepted := false
		for tries := 0; tries < 60; tries++ {
			cand := make([]float64, k)
			for a := 0; a < k; a++ {
				cand[a] = b[a] + t*step[a]
			}
			fNew := neg2LL(cand)
			if fNew < fCur || math.Abs(fNew-fCur) < 1e-14 {
				copy(b, cand)
				fCur = fNew
				accepted = true
				break
			}
			t /= 2
		}
		maxStep := 0.0
		for a := 0; a < k; a++ {
			if v := math.Abs(t * step[a]); v > maxStep {
				maxStep = v
			}
		}
		if !accepted {
			break // no further improvement available
		}
		if maxStep < 1e-9 {
			break
		}
		if it == 199 {
			return fmt.Errorf("LOGISTIC: the model did not converge.")
		}
	}
	llf := neg2LL(b)

	// covariance at the optimum
	pv := make([]float64, n)
	for i := 0; i < n; i++ {
		eta := 0.0
		for j := 0; j < k; j++ {
			eta += X[i*k+j] * b[j]
		}
		pv[i] = logit(eta)
	}
	H := make([][]float64, k)
	for a := 0; a < k; a++ {
		H[a] = make([]float64, k)
	}
	for i := 0; i < n; i++ {
		w := pv[i] * (1 - pv[i])
		for a := 0; a < k; a++ {
			for c := 0; c < k; c++ {
				H[a][c] += X[i*k+a] * X[i*k+c] * w
			}
		}
	}
	Hinv := Invert(H)
	if Hinv == nil {
		return fmt.Errorf("LOGISTIC: the information matrix is singular.")
	}
	emit := func(t *Table) { s.Out.AddTable(t) }
	mi := &Table{ODSName: "ModelInfo", Caption: "Model Information", MissDot: true}
	mi.Cols = []Col{{Name: "Label1", Label: "", Char: true}, {Name: "cValue1", Label: "", Char: true}}
	mi.AddRow("Response Variable", resp)
	mi.AddRow("Number of Response Levels", "2")
	mi.AddRow("Probability Modeled", fmt.Sprintf("%s=%s", resp, cellLabel(levels[0])))
	mi.AddRow("Optimization Technique", "Newton-Raphson ridge")
	emit(mi)

	rp := &Table{ODSName: "ResponseProfile", Caption: "Response Profile", MissDot: true}
	rp.Cols = []Col{{Name: "OrderedValue", Label: "Ordered\nValue", Format: "3."},
		{Name: resp, Label: resp, Char: levels[0].Str != ""}, {Name: "Frequency", Label: "Total\nFrequency", Format: "10."}}
	lab1 := func(v Value) string { return FormatValue(v, nil) }
	modeledIsStr := levels[0].Str != ""
	_ = lab1
	_ = modeledIsStr
	for oi, lv := range levels {
		cnt := 0.0
		for _, row := range d.Rows {
			if FormatValue(row[ri], nil) == FormatValue(lv, nil) {
				cnt++
			}
		}
		rp.AddRow(oi+1, lv, cnt)
	}
	rp.Notes = append(rp.Notes, fmt.Sprintf("Probability modeled is %s=%s.", resp, FormatValue(levels[0], nil)))
	emit(rp)

	fs := &Table{ODSName: "FitStatistics", Caption: "Model Fit Statistics", MissDot: true}
	fs.Cols = []Col{{Name: "Criterion", Label: "Criterion", Char: true},
		{Name: "InterceptOnly", Label: "Intercept\nOnly", Format: "9.4"},
		{Name: "InterceptCovariates", Label: "Intercept\nand Covariates", Format: "9.4"}}
	aic := llf + 2*float64(k)
	sc := llf + float64(k)*math.Log(float64(n))
	fs.AddRow("AIC", ll0v+2*1, aic)
	fs.AddRow("SC", ll0v+1*math.Log(float64(n)), sc)
	fs.AddRow("-2 Log L", ll0v, llf)
	emit(fs)

	// score test at the null
	pbar := 0.0
	for _, v := range y {
		pbar += v
	}
	pbar /= float64(n)
	Sc := make([]float64, k)
	I0 := make([][]float64, k)
	for a := 0; a < k; a++ {
		I0[a] = make([]float64, k)
	}
	for i := 0; i < n; i++ {
		r := y[i] - pbar
		w := pbar * (1 - pbar)
		for a := 0; a < k; a++ {
			Sc[a] += X[i*k+a] * r
			for c := 0; c < k; c++ {
				I0[a][c] += X[i*k+a] * X[i*k+c] * w
			}
		}
	}
	I0inv := Invert(I0)
	score := 0.0
	if I0inv != nil {
		for a := 0; a < k; a++ {
			for c := 0; c < k; c++ {
				score += Sc[a] * I0inv[a][c] * Sc[c]
			}
		}
	}
	lr := ll0v - llf
	wald := 0.0
	for j := 1; j < k; j++ {
		se := math.Sqrt(Hinv[j][j])
		wald += (b[j] / se) * (b[j] / se)
	}
	gt := &Table{ODSName: "GlobalTests", Caption: "Testing Global Null Hypothesis: BETA=0", MissDot: true}
	gt.Cols = []Col{{Name: "Test", Label: "Test", Char: true}, {Name: "DF", Label: "DF", Format: "3."},
		{Name: "ChiSq", Label: "Chi-Square", Format: "8.4"}, {Name: "ProbChiSq", Label: "Pr > ChiSq", Format: "PVALUE6.4"}}
	gt.AddRow("Likelihood Ratio", k-1, lr, ChiSqSF(lr, float64(k-1)))
	gt.AddRow("Score", k-1, score, ChiSqSF(score, float64(k-1)))
	gt.AddRow("Wald", k-1, wald, ChiSqSF(wald, float64(k-1)))
	emit(gt)

	pe := &Table{ODSName: "ParameterEstimates", Caption: "Analysis of Maximum Likelihood Estimates", MissDot: true}
	pe.Cols = []Col{{Name: "Parameter", Label: "Parameter", Char: true}, {Name: "DF", Label: "DF", Format: "3."},
		{Name: "Estimate", Label: "Estimate", Format: "8.4"}, {Name: "StdErr", Label: "Standard\nError", Format: "8.4"},
		{Name: "WaldChiSq", Label: "Wald\nChi-Square", Format: "8.4"}, {Name: "ProbChiSq", Label: "Pr > ChiSq", Format: "PVALUE6.4"}}
	for j := 0; j < k; j++ {
		se := math.Sqrt(Hinv[j][j])
		w := (b[j] / se) * (b[j] / se)
		pe.AddRow(paramNames[j], 1, b[j], se, w, ChiSqSF(w, 1))
	}
	emit(pe)

	zq := NormInv(0.975)
	or := &Table{ODSName: "OddsRatios", Caption: "Odds Ratio Estimates", MissDot: true}
	or.Cols = []Col{{Name: "Effect", Label: "Effect", Char: true}, {Name: "Estimate", Label: "Point\nEstimate", Format: "8.4"},
		{Name: "LowerCL", Label: "Lower CL", Format: "8.4"}, {Name: "UpperCL", Label: "Upper CL", Format: "8.4"}}
	or.Spans = []Span{{Label: "95% Wald Conf Limits", From: 2, To: 3}}
	for j := 1; j < k; j++ {
		se := math.Sqrt(Hinv[j][j])
		or.AddRow(paramNames[j], math.Exp(b[j]), math.Exp(b[j]-zq*se), math.Exp(b[j]+zq*se))
	}
	emit(or)

	conc, disc, tied := 0.0, 0.0, 0.0
	for i := 0; i < n; i++ {
		if y[i] != 1 {
			continue
		}
		for j := 0; j < n; j++ {
			if y[j] != 0 {
				continue
			}
			switch {
			case pv[i] > pv[j]:
				conc++
			case pv[i] < pv[j]:
				disc++
			default:
				tied++
			}
		}
	}
	total := conc + disc + tied
	npairs := float64(n) * (float64(n) - 1) / 2
	as := &Table{ODSName: "Association", Caption: "Association of Predicted Probabilities and Observed Responses", MissDot: true}
	as.Cols = []Col{{Name: "Label1", Label: "", Char: true}, {Name: "nValue1", Label: "", Format: "BEST12."}}
	as.AddRow("Percent Concordant", 100*conc/total)
	as.AddRow("Percent Discordant", 100*disc/total)
	as.AddRow("Percent Tied", 100*tied/total)
	as.AddRow("Pairs", total)
	as.AddRow("Somers' D", (conc-disc)/total)
	as.AddRow("Gamma", (conc-disc)/(conc+disc))
	as.AddRow("Tau-a", (conc-disc)/npairs)
	as.AddRow("c", (conc+0.5*tied)/total)
	emit(as)
	return nil
}
