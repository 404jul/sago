package sas

import (
	"fmt"
	"math"
	"strings"
	"unicode"
)

func argNum(c *EvalCtx, args []Value, i int) float64 {
	if i >= len(args) {
		return math.NaN()
	}
	return toNum(c, args[i])
}

func argStr(args []Value, i int) string {
	if i >= len(args) {
		return ""
	}
	if args[i].Char {
		return args[i].Str
	}
	return numToCharAuto(args[i].Num)
}

func invalid(c *EvalCtx, call *Call) Value {
	c.InvalidOps++
	return Missing
}

func num1(f func(float64) float64) Func {
	return func(c *EvalCtx, call *Call, args []Value) Value {
		x := argNum(c, args, 0)
		if math.IsNaN(x) {
			c.MissingOps++
			return Missing
		}
		r := f(x)
		if math.IsNaN(r) || math.IsInf(r, 0) {
			return invalid(c, call)
		}
		return NumV(r)
	}
}

// nonMissingNums collects numeric arguments, skipping missing.
func nonMissingNums(c *EvalCtx, args []Value) []float64 {
	var out []float64
	for i := range args {
		x := argNum(c, args, i)
		if !math.IsNaN(x) {
			out = append(out, x)
		}
	}
	return out
}

func stat(f func([]float64) float64, minN int) Func {
	return func(c *EvalCtx, call *Call, args []Value) Value {
		xs := nonMissingNums(c, args)
		if len(xs) < minN {
			return Missing
		}
		r := f(xs)
		if math.IsNaN(r) {
			return Missing
		}
		return NumV(r)
	}
}

func roundTo(x, unit float64) float64 {
	if unit == 0 || math.IsNaN(unit) {
		unit = 1
	}
	q := x / unit
	// SAS rounds half away from zero with fuzz
	r := math.Round(q + math.Copysign(1e-12*math.Max(1, math.Abs(q)), q))
	v := r * unit
	// clean representation for decimal units
	if unit < 1 {
		dec := math.Round(-math.Log10(unit))
		p := math.Pow(10, dec)
		v = math.Round(v*p) / p
	}
	return v
}

func init() {
	F := Funcs
	F["ABS"] = num1(math.Abs)
	F["SQRT"] = num1(func(x float64) float64 {
		if x < 0 {
			return math.NaN()
		}
		return math.Sqrt(x)
	})
	F["EXP"] = num1(math.Exp)
	F["LOG"] = num1(func(x float64) float64 {
		if x <= 0 {
			return math.NaN()
		}
		return math.Log(x)
	})
	F["LOG10"] = num1(func(x float64) float64 {
		if x <= 0 {
			return math.NaN()
		}
		return math.Log10(x)
	})
	F["LOG2"] = num1(func(x float64) float64 {
		if x <= 0 {
			return math.NaN()
		}
		return math.Log2(x)
	})
	F["LOG1PX"] = num1(math.Log1p)
	F["SIN"] = num1(math.Sin)
	F["COS"] = num1(math.Cos)
	F["TAN"] = num1(math.Tan)
	F["ARSIN"] = num1(math.Asin)
	F["ARCOS"] = num1(math.Acos)
	F["ATAN"] = num1(math.Atan)
	F["SINH"] = num1(math.Sinh)
	F["COSH"] = num1(math.Cosh)
	F["TANH"] = num1(math.Tanh)
	F["ATANH"] = num1(math.Atanh)
	F["INT"] = num1(math.Trunc)
	F["INTZ"] = num1(math.Trunc)
	F["CEIL"] = num1(func(x float64) float64 { return math.Ceil(x - 1e-12*math.Max(1, math.Abs(x))) })
	F["FLOOR"] = num1(func(x float64) float64 { return math.Floor(x + 1e-12*math.Max(1, math.Abs(x))) })
	F["CEILZ"] = num1(math.Ceil)
	F["FLOORZ"] = num1(math.Floor)
	F["SIGN"] = num1(func(x float64) float64 {
		switch {
		case x > 0:
			return 1
		case x < 0:
			return -1
		}
		return 0
	})
	F["GAMMA"] = num1(math.Gamma)
	F["LGAMMA"] = num1(func(x float64) float64 { v, _ := math.Lgamma(x); return v })
	F["DIGAMMA"] = num1(digamma)
	F["FACT"] = num1(func(x float64) float64 { return math.Round(math.Gamma(x + 1)) })
	F["ROUND"] = func(c *EvalCtx, call *Call, args []Value) Value {
		x := argNum(c, args, 0)
		if math.IsNaN(x) {
			return Missing
		}
		unit := 1.0
		if len(args) > 1 {
			unit = argNum(c, args, 1)
		}
		return NumV(roundTo(x, unit))
	}
	F["MOD"] = func(c *EvalCtx, call *Call, args []Value) Value {
		a, b := argNum(c, args, 0), argNum(c, args, 1)
		if math.IsNaN(a) || math.IsNaN(b) {
			return Missing
		}
		if b == 0 {
			return invalid(c, call)
		}
		return NumV(math.Mod(a, b))
	}
	F["COMB"] = func(c *EvalCtx, call *Call, args []Value) Value {
		n, k := argNum(c, args, 0), argNum(c, args, 1)
		return NumV(math.Round(math.Exp(LogChoose(int(n), int(k)))))
	}
	F["PERM"] = func(c *EvalCtx, call *Call, args []Value) Value {
		n, k := argNum(c, args, 0), argNum(c, args, 1)
		if len(args) < 2 {
			k = n
		}
		a, _ := math.Lgamma(n + 1)
		b, _ := math.Lgamma(n - k + 1)
		return NumV(math.Round(math.Exp(a - b)))
	}
	F["BETA"] = func(c *EvalCtx, call *Call, args []Value) Value {
		return NumV(math.Exp(LogBeta(argNum(c, args, 0), argNum(c, args, 1))))
	}

	// descriptive statistics (missing values ignored)
	F["SUM"] = stat(Sum, 1)
	F["MEAN"] = stat(Mean, 1)
	F["MIN"] = stat(func(x []float64) float64 {
		m := x[0]
		for _, v := range x {
			m = math.Min(m, v)
		}
		return m
	}, 1)
	F["MAX"] = stat(func(x []float64) float64 {
		m := x[0]
		for _, v := range x {
			m = math.Max(m, v)
		}
		return m
	}, 1)
	F["RANGE"] = stat(func(x []float64) float64 {
		lo, hi := x[0], x[0]
		for _, v := range x {
			lo, hi = math.Min(lo, v), math.Max(hi, v)
		}
		return hi - lo
	}, 1)
	F["STD"] = stat(StdDev, 2)
	F["VAR"] = stat(Variance, 2)
	F["STDERR"] = stat(func(x []float64) float64 { return StdDev(x) / math.Sqrt(float64(len(x))) }, 2)
	F["CV"] = stat(func(x []float64) float64 { return StdDev(x) / Mean(x) * 100 }, 2)
	F["CSS"] = stat(CSS, 1)
	F["USS"] = stat(func(x []float64) float64 {
		s := 0.0
		for _, v := range x {
			s += v * v
		}
		return s
	}, 1)
	F["SKEWNESS"] = stat(Skewness, 3)
	F["KURTOSIS"] = stat(Kurtosis, 4)
	F["MEDIAN"] = stat(func(x []float64) float64 { return Percentile(Sorted(x), 50) }, 1)
	F["PCTL"] = func(c *EvalCtx, call *Call, args []Value) Value {
		p := argNum(c, args, 0)
		xs := nonMissingNums(c, args[1:])
		if len(xs) == 0 || math.IsNaN(p) {
			return Missing
		}
		return NumV(Percentile(Sorted(xs), p))
	}
	F["N"] = func(c *EvalCtx, call *Call, args []Value) Value { return NumV(float64(len(nonMissingNums(c, args)))) }
	F["NMISS"] = func(c *EvalCtx, call *Call, args []Value) Value {
		n := 0
		for _, a := range args {
			if a.IsMissing() {
				n++
			}
		}
		return NumV(float64(n))
	}
	F["CMISS"] = F["NMISS"]
	F["MISSING"] = func(c *EvalCtx, call *Call, args []Value) Value {
		return BoolV(len(args) > 0 && args[0].IsMissing())
	}
	F["COALESCE"] = func(c *EvalCtx, call *Call, args []Value) Value {
		for i := range args {
			if x := argNum(c, args, i); !math.IsNaN(x) {
				return NumV(x)
			}
		}
		return Missing
	}
	F["COALESCEC"] = func(c *EvalCtx, call *Call, args []Value) Value {
		for i := range args {
			if s := argStr(args, i); strings.TrimSpace(s) != "" {
				return StrV(s)
			}
		}
		return StrV("")
	}
	F["IFN"] = func(c *EvalCtx, call *Call, args []Value) Value {
		if len(args) < 3 {
			return Missing
		}
		if args[0].Truth() {
			return NumV(argNum(c, args, 1))
		}
		return NumV(argNum(c, args, 2))
	}
	F["IFC"] = func(c *EvalCtx, call *Call, args []Value) Value {
		if len(args) < 3 {
			return StrV("")
		}
		if args[0].Truth() {
			return StrV(argStr(args, 1))
		}
		return StrV(argStr(args, 2))
	}

	// character functions
	F["UPCASE"] = func(c *EvalCtx, call *Call, args []Value) Value { return StrV(strings.ToUpper(argStr(args, 0))) }
	F["LOWCASE"] = func(c *EvalCtx, call *Call, args []Value) Value { return StrV(strings.ToLower(argStr(args, 0))) }
	F["PROPCASE"] = func(c *EvalCtx, call *Call, args []Value) Value {
		s := []rune(strings.ToLower(argStr(args, 0)))
		up := true
		for i, r := range s {
			if up && unicode.IsLetter(r) {
				s[i] = unicode.ToUpper(r)
			}
			up = r == ' ' || r == '-' || r == '/' || r == '(' || r == '.' || r == '\t'
		}
		return StrV(string(s))
	}
	F["TRIM"] = func(c *EvalCtx, call *Call, args []Value) Value {
		s := strings.TrimRight(argStr(args, 0), " ")
		if s == "" {
			s = " "
		}
		return StrV(s)
	}
	F["TRIMN"] = func(c *EvalCtx, call *Call, args []Value) Value { return StrV(strings.TrimRight(argStr(args, 0), " ")) }
	F["STRIP"] = func(c *EvalCtx, call *Call, args []Value) Value { return StrV(strings.TrimSpace(argStr(args, 0))) }
	F["LEFT"] = func(c *EvalCtx, call *Call, args []Value) Value {
		s := argStr(args, 0)
		t := strings.TrimLeft(s, " ")
		return StrV(t + strings.Repeat(" ", len(s)-len(t)))
	}
	F["RIGHT"] = func(c *EvalCtx, call *Call, args []Value) Value {
		s := argStr(args, 0)
		t := strings.TrimRight(s, " ")
		return StrV(strings.Repeat(" ", len(s)-len(t)) + t)
	}
	F["LENGTH"] = func(c *EvalCtx, call *Call, args []Value) Value {
		n := len(strings.TrimRight(argStr(args, 0), " "))
		if n == 0 {
			n = 1
		}
		return NumV(float64(n))
	}
	F["LENGTHN"] = func(c *EvalCtx, call *Call, args []Value) Value {
		return NumV(float64(len(strings.TrimRight(argStr(args, 0), " "))))
	}
	F["LENGTHC"] = func(c *EvalCtx, call *Call, args []Value) Value { return NumV(float64(len(argStr(args, 0)))) }
	F["SUBSTR"] = func(c *EvalCtx, call *Call, args []Value) Value {
		s := argStr(args, 0)
		p := int(argNum(c, args, 1))
		if p < 1 || p > len(s)+1 {
			c.InvalidOps++
			return StrV("")
		}
		end := len(s)
		if len(args) > 2 {
			n := int(argNum(c, args, 2))
			if p-1+n < end {
				end = p - 1 + n
			}
			if end < p-1 {
				end = p - 1
			}
		}
		return StrV(s[p-1 : end])
	}
	F["SUBSTRN"] = F["SUBSTR"]
	F["INDEX"] = func(c *EvalCtx, call *Call, args []Value) Value {
		return NumV(float64(strings.Index(argStr(args, 0), argStr(args, 1)) + 1))
	}
	F["FIND"] = func(c *EvalCtx, call *Call, args []Value) Value {
		s, sub := argStr(args, 0), argStr(args, 1)
		if len(args) > 2 && args[2].Char && strings.ContainsAny(strings.ToLower(args[2].Str), "i") {
			s, sub = strings.ToLower(s), strings.ToLower(sub)
		}
		return NumV(float64(strings.Index(s, sub) + 1))
	}
	F["INDEXC"] = func(c *EvalCtx, call *Call, args []Value) Value {
		return NumV(float64(strings.IndexAny(argStr(args, 0), argStr(args, 1)) + 1))
	}
	F["SCAN"] = func(c *EvalCtx, call *Call, args []Value) Value {
		s := argStr(args, 0)
		n := int(argNum(c, args, 1))
		delims := " !$%&()*+,-./;<^|"
		if len(args) > 2 {
			delims = argStr(args, 2)
		}
		words := strings.FieldsFunc(s, func(r rune) bool { return strings.ContainsRune(delims, r) })
		if n < 0 {
			n = len(words) + n + 1
		}
		if n < 1 || n > len(words) {
			return StrV("")
		}
		return StrV(words[n-1])
	}
	F["COUNTW"] = func(c *EvalCtx, call *Call, args []Value) Value {
		delims := " !$%&()*+,-./;<^|"
		if len(args) > 1 {
			delims = argStr(args, 1)
		}
		return NumV(float64(len(strings.FieldsFunc(argStr(args, 0), func(r rune) bool { return strings.ContainsRune(delims, r) }))))
	}
	F["COMPRESS"] = func(c *EvalCtx, call *Call, args []Value) Value {
		s := argStr(args, 0)
		chars := " "
		if len(args) > 1 {
			chars = argStr(args, 1)
		}
		mods := ""
		if len(args) > 2 {
			mods = strings.ToLower(argStr(args, 2))
		}
		var b strings.Builder
		for _, r := range s {
			rm := strings.ContainsRune(chars, r)
			if strings.Contains(mods, "d") && unicode.IsDigit(r) {
				rm = true
			}
			if strings.Contains(mods, "a") && unicode.IsLetter(r) {
				rm = true
			}
			if strings.Contains(mods, "s") && unicode.IsSpace(r) {
				rm = true
			}
			if strings.Contains(mods, "p") && unicode.IsPunct(r) {
				rm = true
			}
			if strings.Contains(mods, "k") {
				rm = !rm
			}
			if !rm {
				b.WriteRune(r)
			}
		}
		return StrV(b.String())
	}
	F["CAT"] = func(c *EvalCtx, call *Call, args []Value) Value {
		var b strings.Builder
		for i := range args {
			b.WriteString(argStr(args, i))
		}
		return StrV(b.String())
	}
	F["CATS"] = func(c *EvalCtx, call *Call, args []Value) Value {
		var b strings.Builder
		for _, a := range args {
			b.WriteString(strings.TrimSpace(a.AsStr()))
		}
		return StrV(b.String())
	}
	F["CATT"] = func(c *EvalCtx, call *Call, args []Value) Value {
		var b strings.Builder
		for i := range args {
			b.WriteString(strings.TrimRight(argStr(args, i), " "))
		}
		return StrV(b.String())
	}
	F["CATX"] = func(c *EvalCtx, call *Call, args []Value) Value {
		if len(args) == 0 {
			return StrV("")
		}
		sep := argStr(args, 0)
		var parts []string
		for _, a := range args[1:] {
			if s := strings.TrimSpace(a.AsStr()); s != "" && !(a.IsMissing() && !a.Char) {
				parts = append(parts, s)
			}
		}
		return StrV(strings.Join(parts, sep))
	}
	F["TRANWRD"] = func(c *EvalCtx, call *Call, args []Value) Value {
		from := argStr(args, 1)
		if from == "" {
			return StrV(argStr(args, 0))
		}
		return StrV(strings.ReplaceAll(argStr(args, 0), from, argStr(args, 2)))
	}
	F["TRANSTRN"] = F["TRANWRD"]
	F["TRANSLATE"] = func(c *EvalCtx, call *Call, args []Value) Value {
		s := []byte(argStr(args, 0))
		for k := 1; k+1 < len(args); k += 2 {
			to, from := argStr(args, k), argStr(args, k+1)
			for i := range s {
				if j := strings.IndexByte(from, s[i]); j >= 0 {
					if j < len(to) {
						s[i] = to[j]
					} else {
						s[i] = ' '
					}
				}
			}
		}
		return StrV(string(s))
	}
	F["REVERSE"] = func(c *EvalCtx, call *Call, args []Value) Value {
		r := []rune(argStr(args, 0))
		for i, j := 0, len(r)-1; i < j; i, j = i+1, j-1 {
			r[i], r[j] = r[j], r[i]
		}
		return StrV(string(r))
	}
	F["REPEAT"] = func(c *EvalCtx, call *Call, args []Value) Value {
		n := int(argNum(c, args, 1))
		if n < 0 {
			n = 0
		}
		return StrV(strings.Repeat(argStr(args, 0), n+1))
	}
	F["COUNT"] = func(c *EvalCtx, call *Call, args []Value) Value {
		sub := argStr(args, 1)
		if sub == "" {
			return NumV(0)
		}
		return NumV(float64(strings.Count(argStr(args, 0), sub)))
	}
	F["BYTE"] = func(c *EvalCtx, call *Call, args []Value) Value { return StrV(string(rune(int(argNum(c, args, 0))))) }
	F["RANK"] = func(c *EvalCtx, call *Call, args []Value) Value {
		s := argStr(args, 0)
		if s == "" {
			return NumV(0)
		}
		return NumV(float64(s[0]))
	}
	F["PUT"] = func(c *EvalCtx, call *Call, args []Value) Value {
		if len(args) < 2 {
			return StrV("")
		}
		f := ParseFormat(args[1].Str)
		s := FormatValue(args[0], f)
		if f != nil && f.HasW && !args[0].Char {
			// numeric formats are right-aligned in width w
			if len(s) < f.W {
				s = strings.Repeat(" ", f.W-len(s)) + s
			}
		}
		return StrV(s)
	}
	F["PUTN"] = F["PUT"]
	F["PUTC"] = F["PUT"]
	F["INPUT"] = func(c *EvalCtx, call *Call, args []Value) Value {
		if len(args) < 2 {
			return Missing
		}
		f := ParseFormat(args[1].Str)
		v, ok := ApplyInformat(argStr(args, 0), f, f != nil && f.Char)
		if !ok {
			c.InvalidOps++
		}
		return v
	}
	F["INPUTN"] = F["INPUT"]
	F["INPUTC"] = F["INPUT"]

	// probability functions
	F["PROBNORM"] = num1(NormCDF)
	F["PROBIT"] = num1(func(p float64) float64 {
		if p <= 0 || p >= 1 {
			return math.NaN()
		}
		return NormInv(p)
	})
	F["PROBT"] = func(c *EvalCtx, call *Call, args []Value) Value {
		// SAS PROBT is the UPPER tail: P(T > t)
		return NumV(TSF(argNum(c, args, 0), argNum(c, args, 1)))
	}
	F["PROBCHI"] = func(c *EvalCtx, call *Call, args []Value) Value {
		// SAS PROBCHI is the UPPER tail: P(X > x)
		return NumV(ChiSqSF(argNum(c, args, 0), argNum(c, args, 1)))
	}
	F["PROBF"] = func(c *EvalCtx, call *Call, args []Value) Value {
		// SAS PROBF is the UPPER tail: P(F > f)
		return NumV(FSF(argNum(c, args, 0), argNum(c, args, 1), argNum(c, args, 2)))
	}
	F["PROBBNML"] = func(c *EvalCtx, call *Call, args []Value) Value {
		return NumV(BinomCDF(int(argNum(c, args, 2)), int(argNum(c, args, 1)), argNum(c, args, 0)))
	}
	F["POISSON"] = func(c *EvalCtx, call *Call, args []Value) Value {
		return NumV(PoissonCDF(int(argNum(c, args, 1)), argNum(c, args, 0)))
	}
	F["TINV"] = func(c *EvalCtx, call *Call, args []Value) Value {
		return NumV(TInv(argNum(c, args, 0), argNum(c, args, 1)))
	}
	F["CINV"] = func(c *EvalCtx, call *Call, args []Value) Value {
		return NumV(ChiSqInv(argNum(c, args, 0), argNum(c, args, 1)))
	}
	F["FINV"] = func(c *EvalCtx, call *Call, args []Value) Value {
		return NumV(FInv(argNum(c, args, 0), argNum(c, args, 1), argNum(c, args, 2)))
	}
	F["BETAINV"] = func(c *EvalCtx, call *Call, args []Value) Value {
		return NumV(BetaInv(argNum(c, args, 0), argNum(c, args, 1), argNum(c, args, 2)))
	}
	F["CDF"] = distFunc("CDF")
	F["PDF"] = distFunc("PDF")
	F["SDF"] = distFunc("SDF")
	F["QUANTILE"] = distFunc("QUANTILE")
	F["LOGPDF"] = func(c *EvalCtx, call *Call, args []Value) Value {
		v := distFunc("PDF")(c, call, args)
		if v.IsMissing() || v.Num <= 0 {
			return Missing
		}
		return NumV(math.Log(v.Num))
	}

	// dates
	F["MDY"] = func(c *EvalCtx, call *Call, args []Value) Value {
		return NumV(MDY(int(argNum(c, args, 0)), int(argNum(c, args, 1)), int(argNum(c, args, 2))))
	}
	F["TODAY"] = func(c *EvalCtx, call *Call, args []Value) Value { return NumV(TimeToDate(nowFunc())) }
	F["DATE"] = F["TODAY"]
	datePart := func(f func(d float64) float64) Func {
		return num1(f)
	}
	F["YEAR"] = datePart(func(d float64) float64 { return float64(DateToTime(d).Year()) })
	F["MONTH"] = datePart(func(d float64) float64 { return float64(DateToTime(d).Month()) })
	F["DAY"] = datePart(func(d float64) float64 { return float64(DateToTime(d).Day()) })
	F["QTR"] = datePart(func(d float64) float64 { return float64((DateToTime(d).Month()-1)/3 + 1) })
	F["WEEKDAY"] = datePart(func(d float64) float64 { return float64(DateToTime(d).Weekday()) + 1 })
	F["DATEPART"] = num1(func(x float64) float64 { return math.Floor(x / 86400) })
	F["TIMEPART"] = num1(func(x float64) float64 { return math.Mod(x, 86400) })
	F["DHMS"] = func(c *EvalCtx, call *Call, args []Value) Value {
		return NumV(argNum(c, args, 0)*86400 + argNum(c, args, 1)*3600 + argNum(c, args, 2)*60 + argNum(c, args, 3))
	}
	F["HMS"] = func(c *EvalCtx, call *Call, args []Value) Value {
		return NumV(argNum(c, args, 0)*3600 + argNum(c, args, 1)*60 + argNum(c, args, 2))
	}
	F["INTCK"] = func(c *EvalCtx, call *Call, args []Value) Value {
		iv := strings.ToUpper(strings.TrimSpace(argStr(args, 0)))
		a, b := argNum(c, args, 1), argNum(c, args, 2)
		if math.IsNaN(a) || math.IsNaN(b) {
			return Missing
		}
		ta, tb := DateToTime(a), DateToTime(b)
		switch iv {
		case "DAY":
			return NumV(math.Floor(b) - math.Floor(a))
		case "WEEK":
			return NumV(math.Floor((b+4)/7) - math.Floor((a+4)/7))
		case "MONTH":
			return NumV(float64((tb.Year()-ta.Year())*12 + int(tb.Month()) - int(ta.Month())))
		case "QTR":
			return NumV(float64((tb.Year()-ta.Year())*4 + (int(tb.Month())-1)/3 - (int(ta.Month())-1)/3))
		case "YEAR":
			return NumV(float64(tb.Year() - ta.Year()))
		}
		return invalid(c, call)
	}
	F["INTNX"] = func(c *EvalCtx, call *Call, args []Value) Value {
		iv := strings.ToUpper(strings.TrimSpace(argStr(args, 0)))
		d, n := argNum(c, args, 1), int(argNum(c, args, 2))
		if math.IsNaN(d) {
			return Missing
		}
		t := DateToTime(d)
		switch iv {
		case "DAY":
			return NumV(math.Floor(d) + float64(n))
		case "WEEK":
			return NumV(math.Floor(d) - float64(t.Weekday()) + float64(7*n))
		case "MONTH":
			tot := t.Year()*12 + int(t.Month()) - 1 + n
			return NumV(MDY(tot%12+1, 1, tot/12))
		case "YEAR":
			return NumV(MDY(1, 1, t.Year()+n))
		}
		return invalid(c, call)
	}
}

func digamma(x float64) float64 {
	r := 0.0
	for x < 6 {
		r -= 1 / x
		x++
	}
	f := 1 / (x * x)
	return r + math.Log(x) - 0.5/x - f*(1.0/12-f*(1.0/120-f*(1.0/252-f*(1.0/240-f/132))))
}

// distFunc implements CDF/PDF/SDF/QUANTILE('dist', x, params...).
func distFunc(kind string) Func {
	return func(c *EvalCtx, call *Call, args []Value) Value {
		if len(args) < 2 {
			return invalid(c, call)
		}
		dist := strings.ToUpper(strings.TrimSpace(argStr(args, 0)))
		x := argNum(c, args, 1)
		if math.IsNaN(x) {
			return Missing
		}
		p := func(i int, def float64) float64 {
			if 2+i < len(args) {
				return argNum(c, args, 2+i)
			}
			return def
		}
		var cdf, pdf, q func(float64) float64
		discrete := false
		switch dist {
		case "NORMAL", "NORM", "GAUSS", "GAUSSIAN":
			mu, sd := p(0, 0), p(1, 1)
			cdf = func(x float64) float64 { return NormCDF((x - mu) / sd) }
			pdf = func(x float64) float64 { return NormPDF((x-mu)/sd) / sd }
			q = func(pp float64) float64 { return mu + sd*NormInv(pp) }
		case "T":
			df := p(0, math.NaN())
			cdf = func(x float64) float64 { return TCDF(x, df) }
			pdf = func(x float64) float64 { return TPDF(x, df) }
			q = func(pp float64) float64 { return TInv(pp, df) }
		case "CHISQ", "CHISQUARE", "CHISQUARED":
			df := p(0, math.NaN())
			cdf = func(x float64) float64 { return ChiSqCDF(x, df) }
			pdf = func(x float64) float64 { return ChiSqPDF(x, df) }
			q = func(pp float64) float64 { return ChiSqInv(pp, df) }
		case "F":
			d1, d2 := p(0, math.NaN()), p(1, math.NaN())
			cdf = func(x float64) float64 { return FCDF(x, d1, d2) }
			pdf = func(x float64) float64 { return FPDF(x, d1, d2) }
			q = func(pp float64) float64 { return FInv(pp, d1, d2) }
		case "UNIFORM":
			a, b := p(0, 0), p(1, 1)
			cdf = func(x float64) float64 { return math.Max(0, math.Min(1, (x-a)/(b-a))) }
			pdf = func(x float64) float64 {
				if x < a || x > b {
					return 0
				}
				return 1 / (b - a)
			}
			q = func(pp float64) float64 { return a + pp*(b-a) }
		case "EXPONENTIAL", "EXPO":
			sc := p(0, 1)
			cdf = func(x float64) float64 { return math.Max(0, 1-math.Exp(-x/sc)) }
			pdf = func(x float64) float64 {
				if x < 0 {
					return 0
				}
				return math.Exp(-x/sc) / sc
			}
			q = func(pp float64) float64 { return -sc * math.Log1p(-pp) }
		case "GAMMA":
			a, sc := p(0, 1), p(1, 1)
			cdf = func(x float64) float64 { return RegGammaP(a, x/sc) }
			pdf = func(x float64) float64 {
				lg, _ := math.Lgamma(a)
				return math.Exp((a-1)*math.Log(x/sc)-x/sc-lg) / sc
			}
			q = func(pp float64) float64 {
				return invertMonotone(func(x float64) float64 { return RegGammaP(a, x/sc) }, pp, 0, a*sc+10, false)
			}
		case "BETA":
			a, b := p(0, 1), p(1, 1)
			cdf = func(x float64) float64 { return RegIncBeta(a, b, x) }
			pdf = func(x float64) float64 { return math.Exp((a-1)*math.Log(x) + (b-1)*math.Log1p(-x) - LogBeta(a, b)) }
			q = func(pp float64) float64 { return BetaInv(pp, a, b) }
		case "LOGNORMAL":
			mu, sd := p(0, 0), p(1, 1)
			cdf = func(x float64) float64 {
				if x <= 0 {
					return 0
				}
				return NormCDF((math.Log(x) - mu) / sd)
			}
			pdf = func(x float64) float64 {
				if x <= 0 {
					return 0
				}
				return NormPDF((math.Log(x)-mu)/sd) / (sd * x)
			}
			q = func(pp float64) float64 { return math.Exp(mu + sd*NormInv(pp)) }
		case "BINOMIAL", "BINOM":
			discrete = true
			pr, n := p(0, 0.5), int(p(1, 1))
			cdf = func(x float64) float64 { return BinomCDF(int(math.Floor(x)), n, pr) }
			pdf = func(x float64) float64 {
				if x != math.Floor(x) {
					return 0
				}
				return BinomPMF(int(x), n, pr)
			}
		case "POISSON":
			discrete = true
			l := p(0, 1)
			cdf = func(x float64) float64 { return PoissonCDF(int(math.Floor(x)), l) }
			pdf = func(x float64) float64 {
				if x != math.Floor(x) {
					return 0
				}
				return PoissonPMF(int(x), l)
			}
		case "BERNOULLI":
			discrete = true
			pr := p(0, 0.5)
			cdf = func(x float64) float64 {
				switch {
				case x < 0:
					return 0
				case x < 1:
					return 1 - pr
				}
				return 1
			}
			pdf = func(x float64) float64 {
				switch x {
				case 0:
					return 1 - pr
				case 1:
					return pr
				}
				return 0
			}
		case "GEOMETRIC":
			discrete = true
			pr := p(0, 0.5)
			cdf = func(x float64) float64 {
				if x < 0 {
					return 0
				}
				return 1 - math.Pow(1-pr, math.Floor(x)+1)
			}
			pdf = func(x float64) float64 {
				if x < 0 || x != math.Floor(x) {
					return 0
				}
				return pr * math.Pow(1-pr, x)
			}
		default:
			if c.Sess != nil {
				c.Sess.Log.Error(c.Line, fmt.Sprintf("Distribution %s is not supported by %s.", dist, kind))
			}
			return Missing
		}
		switch kind {
		case "CDF":
			return NumV(cdf(x))
		case "SDF":
			return NumV(1 - cdf(x))
		case "PDF":
			return NumV(pdf(x))
		case "QUANTILE":
			if x < 0 || x > 1 {
				return invalid(c, call)
			}
			if discrete {
				// smallest k with CDF(k) >= p
				k := 0.0
				for cdf(k) < x-1e-12 && k < 1e7 {
					k++
				}
				return NumV(k)
			}
			return NumV(q(x))
		}
		return Missing
	}
}
