package sas

import (
	"fmt"
	"math"
	"strings"
)

// PROC LOGISTIC: binary-response logistic regression fit by iteratively
// reweighted maximum likelihood (Fisher scoring, mathematically the same
// MLE as Newton-Raphson). CLASS predictors use PARAM=EFFECT (the SAS
// default) or PARAM=REF. Per SAS, the default models P(lowest ordered
// response level); DESCENDING reverses.

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
	// FREQ variable: each observation is replicated weight times (SAS uses
	// the integer part of the weight).
	freqIdx := -1
	if fq := p.First("FREQ"); fq != nil {
		if toks := fq.Args(); len(toks) > 0 && toks[0].Kind == TIdent {
			freqIdx = d.Index(toks[0].Text)
			if freqIdx < 0 {
				return fmt.Errorf("Variable %s not found.", strings.ToUpper(toks[0].Text))
			}
		}
	}
	// CLASS predictors: EFFECT coding is the SAS default; REF= level or
	// per-variable (ref='level') overrides the reference for PARAM=REF.
	classSpec := map[string]string{}
	classParam := ""
	if cs := p.First("CLASS", "CLASSES"); cs != nil {
		before, after := SplitSlash(cs.Args())
		param := strings.ToUpper(ParseOpts(after).Str("PARAM", "EFFECT"))
		if param != "REF" && param != "EFFECT" {
			return fmt.Errorf("LOGISTIC: only PARAM=REF or PARAM=EFFECT is supported for CLASS variables.")
		}
		classParam = param
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
	if classSpec == nil {
		classSpec = map[string]string{}
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
			_, isClass := classSpec[strings.ToUpper(v)]
			if d.Vars[i].Char && !isClass {
				return fmt.Errorf("LOGISTIC: character variable %s must be a CLASS variable or recoded as numeric.", strings.ToUpper(v))
			}
			fs = append(fs, i)
		}
		terms = append(terms, fs)
	}
	dataName := strings.ToUpper(p.Opts.Str("DATA", ""))
	if dataName == "" {
		dataName = d.Name
	}
	if !strings.Contains(dataName, ".") {
		dataName = "WORK." + dataName
	}
	s.Log.Note(p.Line, fmt.Sprintf("There were %d observations read from the data set %s.", len(d.Rows), dataName))
	for run, g := range groups {
		sub := d.Subset(g.Rows)
		if by != nil {
			s.Out.SetBy(by.Vars, g.Keys)
		}
		if err := lgFit(s, p, dataName, resp, desc, sub, ri, terms, classSpec, classParam, freqIdx, run+1); err != nil {
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
// (one column) or a CLASS variable (k-1 EFFECT- or REF-coded columns).
type lgFactor struct {
	name    string
	ci      int
	class   bool
	levels  []string // sorted levels (class only)
	dummies []string // level of each dummy column (omitted level left out)
	refIdx  int      // omitted level: REF= level, or the last level for EFFECT
	param   string   // "REF" | "EFFECT"
}

// designVal returns the coded value of factor f for observation level lv in
// dummy column j.
func (f lgFactor) designVal(lv string, j int) float64 {
	if lv == f.dummies[j] {
		if f.param == "REF" {
			return 1
		}
		return 1 // EFFECT: own level
	}
	if f.param == "EFFECT" && lv == f.levels[f.refIdx] {
		return -1 // EFFECT: the omitted level
	}
	return 0
}

// lgFit fits one logistic model and emits the SAS-style ODS tables.
func lgFit(s *Session, p *ProcStep, dataName, resp string, desc bool, d *Dataset, ri int, terms [][]int,
	classSpec map[string]string, classParam string, freqIdx, run int) error {
	// response levels: ascending, reversed by DESCENDING; modeled level
	// is Ordered Value 1 (SAS models the probability of the lower level)
	levels := UniqueSorted(d.Col(ri))
	if len(levels) != 2 {
		return fmt.Errorf("LOGISTIC: response variable %s must have exactly two levels, found %d.", resp, len(levels))
	}
	if desc {
		levels[0], levels[1] = levels[1], levels[0]
	}
	// FREQ weights: replicate rows (integer part); the observation count and
	// frequency sum are reported separately in the NObs table. srcOf maps
	// each expanded row back to its physical row so "used" counts stay in
	// physical observations.
	rows := d.Rows
	srcOf := make([]int, len(rows))
	for i := range srcOf {
		srcOf[i] = i
	}
	nRead := len(rows)
	sumFreqs := 0.0
	weight := make([]float64, len(rows))
	for i, row := range rows {
		if freqIdx >= 0 {
			weight[i] = float64(int(row[freqIdx].Num))
		} else {
			weight[i] = 1
		}
		sumFreqs += weight[i]
	}
	if freqIdx >= 0 {
		var expanded [][]Value
		var expSrc []int
		for pi, row := range rows {
			for k := 0; k < int(weight[pi]); k++ {
				expanded = append(expanded, row)
				expSrc = append(expSrc, pi)
			}
		}
		rows = expanded
		srcOf = expSrc
	}
	// factor descriptors
	mkFactor := func(ci int) lgFactor {
		name := d.Vars[ci].Name
		if ref, isClass := classSpec[strings.ToUpper(name)]; isClass {
			lv := UniqueSorted(d.Col(ci))
			var strs []string
			for _, v := range lv {
				strs = append(strs, FormatValue(v, nil))
			}
			refIdx := len(strs) - 1 // EFFECT default: last level
			if classParam == "REF" && ref != "" {
				for i, s := range strs {
					if strings.EqualFold(s, ref) {
						refIdx = i
					}
				}
			}
			var dummies []string
			for i, s := range strs {
				if i != refIdx {
					dummies = append(dummies, s)
				}
			}
			return lgFactor{name: name, ci: ci, class: true, levels: strs, dummies: dummies, refIdx: refIdx, param: classParam}
		}
		return lgFactor{name: name, ci: ci}
	}
	// parameter columns
	type lgParam struct {
		col       int
		varName   string
		classVal0 string
		term      int  // term index (-1 for the intercept)
		factorK   int  // class factor within the term (single-factor terms)
		levelJ    int  // dummy index j of the column in that factor
		isClass   bool
		single    bool // term is a single factor (eligible for odds ratios)
	}
	var params []lgParam
	termCols := make([][]int, len(terms))     // parameter indices per term
	termFactors := make([][]lgFactor, len(terms))
	termJs := make([][][]int, len(terms))     // [term][column][factor] dummy index
	params = append(params, lgParam{col: 0, varName: "Intercept", term: -1})
	nCol := 1
	for ti, fs := range terms {
		factors := make([]lgFactor, len(fs))
		names := make([]string, len(fs))
		specs := [][]int{{}}
		for k, ci := range fs {
			f := mkFactor(ci)
			factors[k] = f
			names[k] = f.name
			pieces := 1
			if f.class {
				pieces = len(f.levels) - 1
			}
			var next [][]int
			for _, sp := range specs {
				for j := 0; j < pieces; j++ {
					next = append(next, append(append([]int{}, sp...), j))
				}
			}
			specs = next
		}
		termFactors[ti] = factors
		single := len(fs) == 1
		for _, sp := range specs {
			pp := lgParam{col: nCol, varName: strings.Join(names, "*"), term: ti, single: single}
			if single && factors[0].class {
				pp.isClass = true
				pp.factorK = 0
				pp.levelJ = sp[0]
				pp.classVal0 = factors[0].dummies[sp[0]]
			}
			params = append(params, pp)
			termCols[ti] = append(termCols[ti], nCol)
			termJs[ti] = append(termJs[ti], sp)
			nCol++
		}
	}
	// design matrix: complete cases, intercept + predictors
	var X, y []float64
	usedPhys := map[int]bool{}
	for i, row := range rows {
		if weight[srcOf[i]] == 0 {
			continue
		}
		if row[ri].Num != row[ri].Num { // missing response
			continue
		}
		ok := true
		xr := make([]float64, nCol)
		xr[0] = 1
		for ti, fs := range terms {
			for pi, pj := range termJs[ti] {
				v := 1.0
				for k, ci := range fs {
					f := termFactors[ti][k]
					if row[ci].Num != row[ci].Num {
						ok = false
						break
					}
					if f.class {
						v *= f.designVal(FormatValue(row[ci], nil), pj[k])
					} else {
						v *= row[ci].Num
					}
				}
				xr[termCols[ti][pi]] = v
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
		usedPhys[srcOf[i]] = true
		X = append(X, xr...)
	}
	nUsed := len(usedPhys)
	sumFreqsUsed := 0.0
	for pi := range usedPhys {
		sumFreqsUsed += weight[pi]
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
	for it := 0; it < 500; it++ {
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
		// SAS's GCONV stopping point: stop when the predicted relative
		// change of -2 Log L from the next step (step'g, one Newton
		// iteration ahead of the actual change) falls below 1e-8.
		if it > 0 {
			sg := 0.0
			for a := 0; a < k; a++ {
				sg += step[a] * g[a]
			}
			if math.Abs(sg)/(math.Abs(fCur)+1e-6) < 1e-8 {
				break
			}
		}
		// backtracking line search on -2 log likelihood; convergence is
		// SAS's GCONV=1E-8: relative change of -2 Log L below 1e-8 (matching
		// SAS's default stopping point, including its residual slop)
		t := 1.0
		accepted := false
		for tries := 0; tries < 60; tries++ {
			cand := make([]float64, k)
			for a := 0; a < k; a++ {
				cand[a] = b[a] + t*step[a]
			}
			fNew := neg2LL(cand)
			if fNew <= fCur {
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
		if maxStep < 1e-11 {
			break
		}
		if it == 499 {
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
	runv := float64(run)
	procCol := func(t *Table) {
		t.Cols = append([]Col{
			{Name: "_Proc_", Label: "", Char: true, Hidden: true},
			{Name: "_Run_", Label: "", Format: "4.", Hidden: true},
		}, t.Cols...)
		for i := range t.Rows {
			t.Rows[i] = append([]Value{StrV("Logistic"), NumV(runv)}, t.Rows[i]...)
		}
	}

	// ModelInfo
	mi := &Table{ODSName: "ModelInfo", Caption: "Model Information", MissDot: true}
	mi.Cols = []Col{
		{Name: "Description", Label: "", Char: true},
		{Name: "Value", Label: "", Char: true},
		{Name: "nValue1", Label: "", Format: "8."}}
	miC := func(desc, val string) { mi.AddRow(desc, val, Missing) }
	miN := func(desc string, nv float64) { mi.AddRow(desc, formatBest(nv, 8), nv) }
	miC("Data Set", dataName)
	miC("Response Variable", resp)
	miN("Number of Response Levels", float64(len(levels)))
	if freqIdx >= 0 {
		miC("Frequency Variable", p.First("FREQ").Args()[0].Text)
	}
	miC("Model", "binary logit")
	miC("Optimization Technique", "Fisher's scoring")
	procCol(mi)
	emit(mi)

	// NObs
	no := &Table{ODSName: "NObs", Caption: "Number of Observations", MissDot: true}
	no.Cols = []Col{
		{Name: "Label", Label: "", Char: true},
		{Name: "N", Label: "", Format: "10."},
		{Name: "NObsRead", Label: "", Format: "10."},
		{Name: "NObsUsed", Label: "", Format: "10."},
		{Name: "SumFreqsRead", Label: "", Format: "12."},
		{Name: "SumFreqsUsed", Label: "", Format: "12."}}
	no.AddRow("Number of Observations Read", nRead, nRead, nUsed, sumFreqs, sumFreqsUsed)
	no.AddRow("Number of Observations Used", nUsed, nRead, nUsed, sumFreqs, sumFreqsUsed)
	no.AddRow("Sum of Frequencies Read", sumFreqs, nRead, nUsed, sumFreqs, sumFreqsUsed)
	no.AddRow("Sum of Frequencies Used", sumFreqsUsed, nRead, nUsed, sumFreqs, sumFreqsUsed)
	procCol(no)
	emit(no)

	// ResponseProfile
	rp := &Table{ODSName: "ResponseProfile", Caption: "Response Profile", MissDot: true}
	rp.Cols = []Col{
		{Name: "OrderedValue", Label: "Ordered\nValue", Format: "3."},
		{Name: "Outcome", Label: "", Char: true},
		{Name: "Count", Label: "Total\nFrequency", Format: "10."}}
	for oi, lv := range levels {
		cnt := 0.0
		for _, row := range rows {
			if FormatValue(row[ri], nil) == FormatValue(lv, nil) {
				cnt++
			}
		}
		outcome := fmt.Sprintf("%8s", FormatValue(lv, nil))
		rp.AddRow(oi+1, outcome, cnt)
	}
	procCol(rp)
	emit(rp)

	// ClassLevelInfo: one row per level with the coded design values
	if len(classSpec) > 0 {
		cl := &Table{ODSName: "ClassLevelInfo", Caption: "Class Level Information", MissDot: true}
		cl.Cols = []Col{
			{Name: "Class", Label: "", Char: true},
			{Name: "control_var", Label: "", Char: true, Hidden: true},
			{Name: "Value", Label: "", Char: true}}
		var designCols []Col
		maxDummies := 0
		for _, fs := range termFactors {
			for _, f := range fs {
				if f.class {
					maxDummies = len(f.levels) - 1
				}
			}
		}
		for j := 0; j < maxDummies; j++ {
			designCols = append(designCols, Col{Name: fmt.Sprintf("X%d", j+1), Label: "", Format: "1."})
		}
		cl.Cols = append(cl.Cols, designCols...)
		var classFactors []lgFactor
		seen := map[string]bool{}
		for _, fs := range termFactors {
			for _, f := range fs {
				if f.class && !seen[f.name] {
					seen[f.name] = true
					classFactors = append(classFactors, f)
				}
			}
		}
		for _, f := range classFactors {
			for li := range f.levels {
				lv := f.levels[li]
				row := []Value{}
				if li == 0 {
					row = append(row, StrV(f.name))
				} else {
					row = append(row, StrV(""))
				}
				row = append(row, StrV("0"), StrV(lv))
				for j := 0; j < maxDummies; j++ {
					if j < len(f.dummies) {
						row = append(row, NumV(f.designVal(lv, j)))
					} else {
						row = append(row, Missing)
					}
				}
				cl.Rows = append(cl.Rows, row)
			}
		}
		procCol(cl)
		emit(cl)
	}

	// ConvergenceStatus
	cs := &Table{ODSName: "ConvergenceStatus", Caption: "Convergence Status", MissDot: true}
	cs.Cols = []Col{
		{Name: "NullModel", Label: "", Format: "8."},
		{Name: "Reason", Label: "", Char: true},
		{Name: "Status", Label: "", Format: "8."}}
	cs.AddRow(0, "Convergence criterion (GCONV=1E-8) satisfied.", 0)
	procCol(cs)
	emit(cs)

	// FitStatistics
	p0 := 1.0
	pfit := float64(k)
	fsT := &Table{ODSName: "FitStatistics", Caption: "Model Fit Statistics", MissDot: true}
	fsT.Cols = []Col{
		{Name: "RowId", Label: "", Char: true, Hidden: true},
		{Name: "Criterion", Label: "Criterion", Char: true},
		{Name: "InterceptOnly", Label: "Intercept\\nOnly", Format: "9.4"},
		{Name: "InterceptAndCovariates", Label: "Intercept\\nand Covariates", Format: "9.4"}}
	logn := math.Log(sumFreqsUsed)
	fsT.AddRow("AIC", "AIC", ll0v+2*p0, llf+2*pfit)
	fsT.AddRow("SBC", "SC", ll0v+p0*logn, llf+pfit*logn)
	fsT.AddRow("M2LOGL", "-2 Log L", ll0v, llf)
	procCol(fsT)
	emit(fsT)

	// GlobalTests
	gt := &Table{ODSName: "GlobalTests", Caption: "Testing Global Null Hypothesis: BETA=0", MissDot: true}
	gt.Cols = []Col{
		{Name: "Test", Label: "Test", Char: true},
		{Name: "ChiSq", Label: "Chi-Square", Format: "8.4"},
		{Name: "DF", Label: "DF", Format: "3."},
		{Name: "ProbChiSq", Label: "Pr > ChiSq", Format: "PVALUE6.4"}}
	lr := ll0v - llf
	gt.AddRow("Likelihood Ratio", lr, k-1, ChiSqSF(lr, float64(k-1)))
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
	score := 0.0
	if I0inv := Invert(I0); I0inv != nil {
		for a := 0; a < k; a++ {
			for c := 0; c < k; c++ {
				score += Sc[a] * I0inv[a][c] * Sc[c]
			}
		}
	}
	gt.AddRow("Score", score, k-1, ChiSqSF(score, float64(k-1)))
	slopes := make([]int, 0, k-1)
	for j := 1; j < k; j++ {
		slopes = append(slopes, j)
	}
	wald := jointWald(b, Hinv, slopes)
	gt.AddRow("Wald", wald, k-1, ChiSqSF(wald, float64(k-1)))
	procCol(gt)
	emit(gt)

	// ModelANOVA: joint Wald test per MODEL term
	ma := &Table{ODSName: "ModelANOVA", Caption: "Analysis of Maximum Likelihood Estimates", MissDot: true}
	ma.Cols = []Col{
		{Name: "Effect", Label: "Effect", Char: true},
		{Name: "DF", Label: "DF", Format: "3."},
		{Name: "WaldChiSq", Label: "Wald\\nChi-Square", Format: "8.4"},
		{Name: "ProbChiSq", Label: "Pr > ChiSq", Format: "PVALUE6.4"}}
	for ti, fs := range terms {
		cols := termCols[ti]
		if len(cols) == 0 {
			continue
		}
		names := make([]string, len(fs))
		for k2, ci := range fs {
			names[k2] = d.Vars[ci].Name
		}
		wj := jointWald(b, Hinv, cols)
		ma.AddRow(strings.Join(names, "*"), len(cols), wj, ChiSqSF(wj, float64(len(cols))))
	}
	procCol(ma)
	emit(ma)

	// ParameterEstimates
	pe := &Table{ODSName: "ParameterEstimates", Caption: "Analysis of Maximum Likelihood Estimates", MissDot: true}
	pe.Cols = []Col{
		{Name: "Variable", Label: "Variable", Char: true},
		{Name: "ClassVal0", Label: "", Char: true},
		{Name: "DF", Label: "DF", Format: "3."},
		{Name: "Estimate", Label: "Estimate", Format: "8.4"},
		{Name: "StdErr", Label: "Standard\\nError", Format: "8.4"},
		{Name: "WaldChiSq", Label: "Wald\\nChi-Square", Format: "8.4"},
		{Name: "ProbChiSq", Label: "Pr > ChiSq", Format: "PVALUE6.4"},
		{Name: "_ESTTYPE_", Label: "", Char: true, Hidden: true}}
	for j := 0; j < k; j++ {
		pp := params[j]
		se := math.Sqrt(Hinv[j][j])
		w := (b[j] / se) * (b[j] / se)
		pe.AddRow(pp.varName, pp.classVal0, 1, b[j], se, w, ChiSqSF(w, 1), "MLE")
	}
	procCol(pe)
	emit(pe)

	// OddsRatios: level-vs-reference contrasts (single-factor terms only)
	zq := NormInv(0.975)
	or := &Table{ODSName: "OddsRatios", Caption: "Odds Ratio Estimates", MissDot: true}
	or.Cols = []Col{
		{Name: "Effect", Label: "Effect", Char: true},
		{Name: "OddsRatioEst", Label: "Point\\nEstimate", Format: "8.4"},
		{Name: "LowerCL", Label: "Lower CL", Format: "8.4"},
		{Name: "UpperCL", Label: "Upper CL", Format: "8.4"}}
	orContrast := func(j int) []float64 {
		pp := params[j]
		c := make([]float64, k)
		if !pp.isClass {
			c[j] = 1
			return c
		}
		f := termFactors[pp.term][pp.factorK]
		for m, q := range params {
			if q.term != pp.term || !q.isClass {
				continue
			}
			if f.param == "EFFECT" {
				// beta_omit = -sum(betas): L vs omit = 2*b_L + sum(b_m, m != L)
				if q.levelJ == pp.levelJ {
					c[m] = 2
				} else {
					c[m] = 1
				}
			} else if q.levelJ == pp.levelJ {
				c[m] = 1 // REF: L vs reference is just beta_L
			}
		}
		return c
	}
	for j := 1; j < k; j++ {
		pp := params[j]
		if !pp.single {
			continue
		}
		c := orContrast(j)
		est := 0.0
		for m := range c {
			est += c[m] * b[m]
		}
		varv := 0.0
		for a := range c {
			for c2 := range c {
				varv += c[a] * Hinv[a][c2] * c[c2]
			}
		}
		se := math.Sqrt(varv)
		var label string
		if pp.isClass {
			f := termFactors[pp.term][pp.factorK]
			label = fmt.Sprintf("%s  %s vs %s", pp.varName, pp.classVal0, f.levels[f.refIdx])
		} else {
			label = pp.varName
		}
		or.AddRow(label, math.Exp(est), math.Exp(est-zq*se), math.Exp(est+zq*se))
	}
	procCol(or)
	emit(or)

	// Association: pairs of (label, value) columns
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
	n1, n0 := 0.0, 0.0
	for _, v := range y {
		if v == 1 {
			n1++
		} else {
			n0++
		}
	}
	npairs := (n1 + n0) * (n1 + n0 - 1) / 2
	as := &Table{ODSName: "Association", Caption: "Association of Predicted Probabilities and Observed Responses", MissDot: true}
	as.Cols = []Col{
		{Name: "Label1", Label: "", Char: true},
		{Name: "cValue1", Label: "", Char: true},
		{Name: "nValue1", Label: "", Format: "BEST12."},
		{Name: "Label2", Label: "", Char: true},
		{Name: "cValue2", Label: "", Char: true},
		{Name: "nValue2", Label: "", Format: "BEST12."}}
	pct := func(v float64) string { return fmt.Sprintf("%5.1f", v) }
	f3 := func(v float64) string { return fmt.Sprintf("%5.3f", v) }
	as.AddRow("Percent Concordant", pct(100*conc/total), 100*conc/total, "Somers' D", f3((conc-disc)/total), (conc-disc)/total)
	as.AddRow("Percent Discordant", pct(100*disc/total), 100*disc/total, "Gamma", f3((conc-disc)/(conc+disc)), (conc-disc)/(conc+disc))
	as.AddRow("Percent Tied", pct(100*tied/total), 100*tied/total, "Tau-a", f3((conc-disc)/npairs), (conc-disc)/npairs)
	as.AddRow("Pairs", formatBest(total, 8), total, "c", f3((conc+0.5*tied)/total), (conc+0.5*tied)/total)
	procCol(as)
	emit(as)
	return nil
}

// jointWald is the Wald chi-square for H0: beta_T = 0 over the given columns.
func jointWald(bv []float64, V [][]float64, cols []int) float64 {
	m := len(cols)
	bt := make([]float64, m)
	for i, c := range cols {
		bt[i] = bv[c]
	}
	Vtt := make([][]float64, m)
	for i := range Vtt {
		Vtt[i] = make([]float64, m)
		for j2 := range Vtt {
			Vtt[i][j2] = V[cols[i]][cols[j2]]
		}
	}
	Vi := Invert(Vtt)
	if Vi == nil {
		return 0
	}
	w := 0.0
	for i := range m {
		for j2 := range m {
			w += bt[i] * Vi[i][j2] * bt[j2]
		}
	}
	return w
}
