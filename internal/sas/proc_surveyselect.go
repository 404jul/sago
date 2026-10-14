package sas

import (
	"fmt"
	"math"
	"math/rand/v2"
	"strings"
	"time"
)

func init() { RegisterProc("SURVEYSELECT", bpSurveySelect) }

func bpSurveySelect(s *Session, p *ProcStep) error {
	d, err := s.ProcData(p)
	if err != nil {
		return err
	}
	o := p.Opts
	method := strings.ToUpper(o.Str("METHOD", o.Str("M", "SRS")))
	if method != "SRS" && method != "URS" && method != "SYS" {
		return fmt.Errorf("METHOD=%s is not supported (SRS, URS, SYS).", method)
	}
	N := len(d.Rows)
	n := -1
	if o.Has("SAMPSIZE", "N") {
		n = int(o.Num("SAMPSIZE", o.Num("N", -1)))
	} else if o.Has("SAMPRATE", "RATE") {
		rate := o.Num("SAMPRATE", o.Num("RATE", -1))
		if rate > 1 {
			rate /= 100
		}
		n = int(math.Ceil(rate*float64(N) - 1e-9))
	}
	if n < 0 {
		return fmt.Errorf("The sample size or sampling rate must be specified (SAMPSIZE= or SAMPRATE=).")
	}
	if method != "URS" && n > N {
		return fmt.Errorf("The sample size, %d, is greater than the number of sampling units, %d.", n, N)
	}
	reps := int(o.Num("REPS", 1))
	if reps < 1 {
		reps = 1
	}
	seed := int64(o.Num("SEED", 0))
	if seed <= 0 {
		seed = time.Now().UnixNano() % 2147483647
	}
	rng := rand.New(rand.NewPCG(uint64(seed), 0x5a6f5345))
	outhits := o.Has("OUTHITS")

	out := &Dataset{Label: d.Label}
	if reps > 1 {
		out.Vars = append(out.Vars, &Var{Name: "Replicate", Length: 8, Label: "Sample Replicate Number"})
	}
	for _, v := range d.Vars {
		out.Vars = append(out.Vars, v.Clone())
	}
	prob := float64(n) / float64(N)
	weight := float64(N) / float64(n)
	if method == "URS" {
		out.Vars = append(out.Vars,
			&Var{Name: "NumberHits", Length: 8, Label: "Number of Hits"},
			&Var{Name: "ExpectedHits", Length: 8, Label: "Expected Number of Hits"},
			&Var{Name: "SamplingWeight", Length: 8, Label: "Sampling Weight"})
	} else {
		out.Vars = append(out.Vars,
			&Var{Name: "SelectionProb", Length: 8, Label: "Probability of Selection"},
			&Var{Name: "SamplingWeight", Length: 8, Label: "Sampling Weight"})
	}
	width := len(out.Vars)
	emit := func(rep int, r []Value, hits int) {
		row := make([]Value, 0, width)
		if reps > 1 {
			row = append(row, NumV(float64(rep)))
		}
		row = append(row, r...)
		if method == "URS" {
			row = append(row, NumV(float64(hits)), NumV(prob), NumV(weight))
		} else {
			row = append(row, NumV(prob), NumV(weight))
		}
		out.Rows = append(out.Rows, row)
	}
	hits := make([]int, N)
	for rep := 1; rep <= reps; rep++ {
		clear(hits)
		switch method {
		case "URS":
			for range n {
				hits[rng.IntN(N)]++
			}
		case "SRS":
			// selection sampling (Knuth Algorithm S): keeps input order
			need := n
			for i := 0; i < N && need > 0; i++ {
				if rng.IntN(N-i) < need {
					hits[i] = 1
					need--
				}
			}
		case "SYS":
			k := float64(N) / float64(n)
			start := rng.Float64() * k
			for j := range n {
				idx := int(math.Floor(start + float64(j)*k))
				if idx < N {
					hits[idx] = 1
				}
			}
		}
		for i, h := range hits {
			if h == 0 {
				continue
			}
			if method == "URS" && outhits {
				for range h {
					emit(rep, d.Rows[i], h)
				}
			} else {
				emit(rep, d.Rows[i], h)
			}
		}
	}

	if !o.Has("NOPRINT") {
		methodName := map[string]string{"SRS": "Simple Random Sampling", "URS": "Unrestricted Random Sampling", "SYS": "Systematic Random Sampling"}[method]
		t := &Table{ODSName: "Summary", RowHeader: 1, Cols: []Col{{Name: "Label1", Label: ""}, {Name: "cValue1", Label: "", Char: true}}}
		t.AddRow("Selection Method", methodName)
		t.AddRow("Input Data Set", strings.ToUpper(d.Name))
		t.AddRow("Random Number Seed", fmt.Sprint(seed))
		t.AddRow("Sample Size", fmt.Sprint(n))
		if method == "URS" {
			t.AddRow("Expected Number of Hits", FormatByName(NumV(prob), "BEST8."))
		} else {
			t.AddRow("Selection Probability", FormatByName(NumV(prob), "BEST8."))
		}
		t.AddRow("Sampling Weight", FormatByName(NumV(weight), "BEST8."))
		if reps > 1 {
			t.AddRow("Number of Replicates", fmt.Sprint(reps))
		}
		if op, ok := o.Get("OUT"); ok {
			t.AddRow("Output Data Set", strings.ToUpper(op.Str()))
		}
		s.Out.AddTable(t)
	}
	s.Log.Notef(p.Line, "The sample size is %d; random number seed is %d.", n, seed)
	ref := &DSRef{Name: bpNextDataName(), Line: p.Line, Opts: &Opts{}}
	if op, ok := o.Get("OUT"); ok {
		if ref, err = DSRefFromOpt(op); err != nil {
			return err
		}
		ref.Line = p.Line
	}
	return s.WriteDS(ref, out)
}
