package sas

import (
	"fmt"
	"math"
	"strings"
	"time"
)

// DATA step functions: SAS random number generators, LAG/DIF, arrays,
// SYMGET and the expression hook (ds prefix).

// dsRNG holds the random number state of a step.
type dsRNG struct {
	mt               *dsMT
	streamInitCalled bool
	seed             float64
	streams          map[any]*float64 // per-seed RANUNI stream state (SAS: the seed continues across calls)
}

// ---------------------------------------------------------------- SAS RANxxx generators

// ranUni advances the SAS RANUNI recurrence once:
// seed = mod(397204094*seed, 2**31-1); value = seed/(2**31-1).
// The stream state lives in the caller-held seed pointer: for a variable
// seed it is the variable itself (SAS updates it); for a constant seed it
// is per-constant state shared by every call site in the DATA step.
func (r *dsRNG) ranUni(seed *float64) float64 {
	u := (uint64(*seed) * 397204094) % 2147483647
	if u == 0 {
		u = 2147483646
	}
	*seed = float64(u)
	return float64(u) / 2147483647.0
}

// stream returns the caller-visible seed pointer for a keyed stream. A
// nonpositive initial seed uses the time of day (SAS behavior).
func (r *dsRNG) stream(key any, seed0 float64) *float64 {
	if r.streams == nil {
		r.streams = map[any]*float64{}
	}
	if sp, ok := r.streams[key]; ok {
		return sp
	}
	s := seed0
	if s <= 0 {
		s = float64(time.Now().UnixNano()%1000000 + 1)
	}
	r.streams[key] = &s
	return &s
}

// ranKey builds a stream key: variable-seeded calls share the stream per
// seed variable (SAS updates the variable itself, so the state is caller
// held); constant-seeded calls share one stream per constant.
func ranKey(c *EvalCtx, call *Call) any {
	if name := firstArgName(call); name != "" {
		return "var:" + strings.ToUpper(name)
	}
	if len(call.Args) > 0 {
		v := call.Args[0].Eval(c)
		if !v.IsMissing() {
			return fmt.Sprintf("const:%d", int64(v.AsNum()))
		}
	}
	return "const:0"
}

// rannor: Box-Muller on two uniforms (documented approximation of RANNOR).
func (r *dsRNG) rannor(seed *float64) float64 {
	u1 := r.ranUni(seed)
	u2 := r.ranUni(seed)
	if u1 <= 0 {
		u1 = 1e-12
	}
	return math.Sqrt(-2*math.Log(u1)) * math.Cos(2*math.Pi*u2)
}

func (r *dsRNG) ranexp(seed *float64) float64 {
	u := r.ranUni(seed)
	if u <= 0 {
		u = 1e-12
	}
	return -math.Log(u)
}

// ranbin: inversion for small n, normal approximation for large n (SAS rule).
func (r *dsRNG) ranbin(seed *float64, n int, p float64) float64 {
	if n <= 0 || p <= 0 {
		return 0
	}
	if p >= 1 {
		return float64(n)
	}
	if n < 50 {
		u := r.ranUni(seed)
		cdf := 0.0
		for k := 0; k <= n; k++ {
			cdf += BinomPMF(k, n, p)
			if u <= cdf {
				return float64(k)
			}
		}
		return float64(n)
	}
	// normal approximation with continuity correction
	np := float64(n) * p
	sp := math.Sqrt(np * (1 - p))
	u := r.ranUni(seed)
	z := NormInv(u)
	v := math.Floor(np + sp*z + 0.5)
	if v < 0 {
		v = 0
	}
	if v > float64(n) {
		v = float64(n)
	}
	return v
}

func (r *dsRNG) ranpoi(seed *float64, lambda float64) float64 {
	if lambda <= 0 {
		return 0
	}
	if lambda < 85 {
		l := math.Exp(-lambda)
		k := 0.0
		prod := r.ranUni(seed)
		for prod > l {
			k++
			prod *= r.ranUni(seed)
		}
		return k
	}
	m := math.Floor(lambda + math.Sqrt(lambda)*NormInv(r.ranUni(seed)) + 0.5)
	if m < 0 {
		m = 0
	}
	return m
}

// rangam: GS transform (for shape < 1, boost); documented approximation.
func (r *dsRNG) rangam(seed *float64, a float64) float64 {
	if a <= 0 {
		return 0
	}
	if a < 1 {
		u := r.ranUni(seed)
		return r.rangam(seed, a+1) * math.Pow(u, 1.0/a)
	}
	d := a - 1.0/3
	c := 1 / math.Sqrt(9*d)
	for {
		x := r.rannor(seed)
		v := math.Pow(1+c*x, 3)
		if v <= 0 {
			continue
		}
		u := r.ranUni(seed)
		if u < 1-0.0331*x*x*x*x {
			return d * v
		}
		if math.Log(u) < 0.5*x*x+d*(1-v+math.Log(v)) {
			return d * v
		}
	}
}

func (r *dsRNG) rantbl(seed *float64, ps []float64) float64 {
	u := r.ranUni(seed)
	sum := 0.0
	for i, p := range ps {
		sum += p
		if u <= sum && p > 0 {
			return float64(i + 1)
		}
	}
	// trailing mass goes to a pseudo category like SAS
	if sum < 1 && u > sum {
		return float64(len(ps) + 1)
	}
	return float64(len(ps))
}

func (r *dsRNG) ranFamily(name string, seed *float64, args []float64) float64 {
	switch name {
	case "RANBIN":
		if len(args) >= 2 {
			return r.ranbin(seed, int(args[0]), args[1])
		}
	case "RANPOI":
		if len(args) >= 1 {
			return r.ranpoi(seed, args[0])
		}
	case "RANGAM":
		if len(args) >= 1 {
			return r.rangam(seed, args[0])
		}
	case "RANTBL":
		return r.rantbl(seed, args)
	}
	return 0
}

// ---------------------------------------------------------------- MT19937 for RAND()

type dsMT struct {
	mt  [624]uint32
	idx int
}

func dsNewMT(seed float64) *dsMT {
	m := &dsMT{}
	s := uint32(int64(seed))
	if seed == 0 {
		s = uint32(time.Now().UnixNano())
	}
	m.mt[0] = s
	for i := 1; i < 624; i++ {
		m.mt[i] = 1812433253*(m.mt[i-1]^(m.mt[i-1]>>30)) + uint32(i)
	}
	m.idx = 624
	return m
}

func (m *dsMT) next() uint32 {
	if m.idx >= 624 {
		for i := 0; i < 624; i++ {
			y := (m.mt[i] & 0x80000000) | (m.mt[(i+1)%624] & 0x7fffffff)
			n := m.mt[(i+397)%624] ^ (y >> 1)
			if y&1 != 0 {
				n ^= 0x9908b0df
			}
			m.mt[i] = n
		}
		m.idx = 0
	}
	y := m.mt[m.idx]
	m.idx++
	y ^= y >> 11
	y ^= (y << 7) & 0x9d2c5680
	y ^= (y << 15) & 0xefc60000
	y ^= y >> 18
	return y
}

func (r *dsRNG) streamInit(seed float64) {
	r.mt = dsNewMT(seed)
	r.streamInitCalled = true
	r.seed = seed
}

// randUniform returns a uniform via MT (used by RAND).
func (r *dsRNG) randUniform() float64 {
	if r.mt == nil {
		r.streamInit(0)
	}
	return float64(r.mt.next()) / 4294967296.0
}

func (r *dsRNG) randNormal() float64 {
	u1 := r.randUniform()
	u2 := r.randUniform()
	if u1 <= 0 {
		u1 = 1e-12
	}
	return math.Sqrt(-2*math.Log(u1)) * math.Cos(2*math.Pi*u2)
}

// ---------------------------------------------------------------- hook

// dsHook intercepts array references, LAG/DIF, RNG functions and SYMGET.
func dsHook(st *dsStep) func(c *EvalCtx, call *Call, args []Expr) (Value, bool) {
	return func(c *EvalCtx, call *Call, args []Expr) (Value, bool) {
		switch {
		case call.Braces || st.arrays[call.Name] != nil:
			if a := st.arrays[call.Name]; a != nil && len(args) >= 1 {
				i := int(args[0].Eval(c).AsNum())
				v, ok := a.elem(st, i)
				if !ok {
					st.runtimeError(fmt.Sprintf("Array subscript %d out of range for array %s.", i, a.name))
					return Missing, true
				}
				return st.pdv[v.slot], true
			}
		case call.Name == "DIM" || call.Name == "HBOUND" || call.Name == "LBOUND":
			if len(args) == 1 {
				if ref, ok := args[0].(*Call); ok && ref.Braces {
					if a := st.arrays[ref.Name]; a != nil {
						return NumV(float64(a.bound)), true
					}
				}
				if v := st.lookup(firstArgName(call)); v != nil {
					// DIM of an array name given as identifier
					if a := st.arrays[strings.ToUpper(v.Name)]; a != nil {
						return NumV(float64(a.bound)), true
					}
				}
			}
		case strings.HasPrefix(call.Name, "LAG") || strings.HasPrefix(call.Name, "DIF"):
			n := 1
			if call.Name != "LAG" && call.Name != "DIF" {
				fmt.Sscanf(call.Name[3:], "%d", &n)
			}
			if n < 1 {
				n = 1
			}
			key := call
			q, _ := c.State[key].(*[]Value)
			if q == nil {
				qv := make([]Value, n)
				for i := range qv {
					qv[i] = Missing
				}
				q = &qv
				c.State[key] = q
			}
			var x Value
			if len(args) > 0 {
				x = args[0].Eval(c)
			}
			out := (*q)[0]
			*q = append((*q)[1:], x)
			if strings.HasPrefix(call.Name, "DIF") {
				cur := x.AsNum()
				prev := out.AsNum()
				if math.IsNaN(cur) || math.IsNaN(prev) {
					return Missing, true
				}
				return NumV(cur - prev), true
			}
			return out, true
		case call.Name == "SYMGET":
			if len(args) > 0 {
				key := strings.ToUpper(strings.TrimSpace(args[0].Eval(c).AsStr()))
				if v, ok := st.s.Macros[key]; ok {
					return StrV(v), true
				}
			}
			return StrV(""), true
		case call.Name == "RANUNI" || call.Name == "UNIFORM":
			if len(args) >= 1 {
				seed := args[0].Eval(c).AsNum()
				return NumV(st.rng.ranUni(st.rng.stream(ranKey(c, call), seed))), true
			}
		case call.Name == "RANNOR" || call.Name == "NORMAL":
			if len(args) >= 1 {
				seed := args[0].Eval(c).AsNum()
				return NumV(st.rng.rannor(st.rng.stream(ranKey(c, call), seed))), true
			}
		case call.Name == "RANEXP":
			if len(args) >= 1 {
				seed := args[0].Eval(c).AsNum()
				return NumV(st.rng.ranexp(st.rng.stream(ranKey(c, call), seed))), true
			}
		case call.Name == "RANBIN":
			if len(args) >= 3 {
				seed := args[0].Eval(c).AsNum()
				n := int(args[1].Eval(c).AsNum())
				p := args[2].Eval(c).AsNum()
				return NumV(st.rng.ranbin(st.rng.stream(ranKey(c, call), seed), n, p)), true
			}
		case call.Name == "RANPOI":
			if len(args) >= 2 {
				seed := args[0].Eval(c).AsNum()
				l := args[1].Eval(c).AsNum()
				return NumV(st.rng.ranpoi(st.rng.stream(ranKey(c, call), seed), l)), true
			}
		case call.Name == "RANGAM":
			if len(args) >= 2 {
				seed := args[0].Eval(c).AsNum()
				a := args[1].Eval(c).AsNum()
				return NumV(st.rng.rangam(st.rng.stream(ranKey(c, call), seed), a)), true
			}
		case call.Name == "RANTBL":
			if len(args) >= 1 {
				seed := args[0].Eval(c).AsNum()
				var ps []float64
				for _, a := range args[1:] {
					ps = append(ps, a.Eval(c).AsNum())
				}
				return NumV(st.rng.rantbl(st.rng.stream(ranKey(c, call), seed), ps)), true
			}
		case call.Name == "RAND":
			if len(args) >= 1 {
				dist := strings.ToUpper(strings.TrimSpace(args[0].Eval(c).AsStr()))
				p := func(i int) float64 {
					if 1+i < len(args) {
						return args[1+i].Eval(c).AsNum()
					}
					return 0
				}
				switch dist {
				case "UNIFORM":
					return NumV(st.rng.randUniform()), true
				case "NORMAL", "GAUSS":
					// SAS RAND('NORMAL') has no parameters: standard normal
					return NumV(st.rng.randNormal()), true
				case "BERNOULLI":
					pr := p(0)
					if st.rng.randUniform() < pr {
						return NumV(1), true
					}
					return NumV(0), true
				case "BINOMIAL":
					pr, n := p(0), int(p(1))
					u := st.rng.randUniform()
					cdf := 0.0
					for k := 0; k <= n; k++ {
						cdf += BinomPMF(k, n, pr)
						if u <= cdf {
							return NumV(float64(k)), true
						}
					}
					return NumV(float64(n)), true
				case "POISSON":
					l := p(0)
					if l < 85 {
						lg := math.Exp(-l)
						k := 0.0
						prod := st.rng.randUniform()
						for prod > lg {
							k++
							prod *= st.rng.randUniform()
						}
						return NumV(k), true
					}
					m := math.Floor(l + math.Sqrt(l)*st.rng.randNormal() + 0.5)
					if m < 0 {
						m = 0
					}
					return NumV(m), true
				case "EXPONENTIAL":
					u := st.rng.randUniform()
					if u <= 0 {
						u = 1e-12
					}
					return NumV(-math.Log(u)), true
				case "TABLE":
					u := st.rng.randUniform()
					sum := 0.0
					for i := range args[1:] {
						sum += args[1+i].Eval(c).AsNum()
						if u <= sum {
							return NumV(float64(i + 1)), true
						}
					}
					return NumV(float64(len(args))), true
				case "INTEGER":
					lo, hi := p(0), p(1)
					if hi < lo {
						lo, hi = hi, lo
					}
					return NumV(math.Floor(lo + st.rng.randUniform()*(hi-lo+1))), true
				case "GAMMA":
					sd := 1.0
					return NumV(st.rng.rangam(&sd, p(0))), true
				}
			}
			return Missing, true
		}
		return Missing, false
	}
}

func callKey(c *EvalCtx, call *Call) any { return call }

func firstArgName(call *Call) string {
	if len(call.Args) > 0 {
		if r, ok := call.Args[0].(*VarRef); ok {
			return r.Name
		}
	}
	return ""
}

// dsExpandOf implements OF variable lists for functions.
func dsExpandOf(st *dsStep) func(toks []Token) []Value {
	return func(toks []Token) []Value {
		var out []Value
		c := NewCursor(toks)
		for !c.AtEnd() {
			t := c.Next()
			if t.Kind != TIdent {
				continue
			}
			if c.Peek().IsOp("-") && c.PeekN(1).Kind == TIdent {
				if r, err := NumberedRange(t.Text, c.PeekN(1).Text); err == nil {
					c.P += 2
					for _, n := range r {
						if v := st.lookup(n); v != nil {
							out = append(out, st.pdv[v.slot])
						}
					}
					continue
				}
			}
			if c.Peek().IsOp("{", "[") {
				g, _ := c.Group()
				_ = g
				if a := st.arrays[strings.ToUpper(t.Text)]; a != nil {
					for _, v := range a.vars {
						out = append(out, st.pdv[v.slot])
					}
					continue
				}
			}
			switch strings.ToUpper(t.Text) {
			case "_NUMERIC_":
				for _, v := range st.vars {
					if !v.auto && !v.Char {
						out = append(out, st.pdv[v.slot])
					}
				}
				continue
			case "_CHARACTER_":
				for _, v := range st.vars {
					if !v.auto && v.Char {
						out = append(out, st.pdv[v.slot])
					}
				}
				continue
			}
			if v := st.lookup(t.Text); v != nil {
				out = append(out, st.pdv[v.slot])
			}
		}
		return out
	}
}
