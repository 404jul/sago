package sas

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// FormatSpec is a parsed SAS format or informat such as BEST12. 8.2 $20. DATE9.
type FormatSpec struct {
	Name string // upper case, without '$' and width, e.g. "BEST", "DOLLAR", "" for w.d
	Char bool   // '$' prefix
	W, D int
	HasW bool
	HasD bool
}

// ParseFormat parses text like "best12.", "8.2", "$15.", "date9.", "dollar10.2".
// Returns nil if text is not a valid format spec.
func ParseFormat(text string) *FormatSpec {
	t := strings.TrimSpace(text)
	if t == "" {
		return nil
	}
	f := &FormatSpec{}
	if strings.HasPrefix(t, "$") {
		f.Char = true
		t = t[1:]
	}
	if !strings.Contains(t, ".") {
		return nil
	}
	i := 0
	for i < len(t) && (unicode.IsLetter(rune(t[i])) || t[i] == '_') {
		i++
	}
	// names may end in digits (e.g. MMDDYY, YYMMDD have none, but B8601DA does); take
	// letters then the trailing number before '.' as width.
	f.Name = strings.ToUpper(t[:i])
	rest := t[i:]
	dot := strings.Index(rest, ".")
	if dot < 0 {
		return nil
	}
	if ws := rest[:dot]; ws != "" {
		w, err := strconv.Atoi(ws)
		if err != nil {
			return nil
		}
		f.W, f.HasW = w, true
	}
	if ds := rest[dot+1:]; ds != "" {
		d, err := strconv.Atoi(ds)
		if err != nil {
			return nil
		}
		f.D, f.HasD = d, true
	}
	if f.Name == "CHAR" {
		f.Char, f.Name = true, ""
	}
	return f
}

func (f *FormatSpec) String() string {
	if f == nil {
		return ""
	}
	s := ""
	if f.Char {
		s = "$"
	}
	s += f.Name
	if f.HasW {
		s += strconv.Itoa(f.W)
	}
	s += "."
	if f.HasD {
		s += strconv.Itoa(f.D)
	}
	return s
}

// FormatValue renders a value using a format (nil = default: BEST12. for
// numbers, as-is for characters). Result is not padded, except where SAS
// semantics require it.
func FormatValue(v Value, f *FormatSpec) string {
	if v.Char {
		s := strings.TrimRight(v.Str, " ")
		if f != nil && f.Name == "QUOTE" {
			s = `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
		}
		if f != nil && f.HasW && f.W < len(s) {
			s = s[:f.W]
		}
		return s
	}
	x := v.Num
	if math.IsNaN(x) {
		return "."
	}
	if f == nil {
		return formatBest(x, 12)
	}
	w := f.W
	switch f.Name {
	case "", "F":
		if !f.HasW {
			return formatBest(x, 12)
		}
		return formatFixed(x, w, f.D)
	case "BEST", "BESTD":
		if !f.HasW {
			w = 12
		}
		return formatBest(x, w)
	case "COMMA", "DOLLAR", "COMMAX", "DOLLARX":
		s := commaFixed(math.Abs(x), f.D)
		if strings.HasPrefix(f.Name, "DOLLAR") {
			s = "$" + s
		}
		if x < 0 {
			s = "-" + s
		}
		return s
	case "PERCENT":
		d := f.D
		s := strconv.FormatFloat(x*100, 'f', d, 64) + "%"
		if x < 0 {
			s = "(" + strconv.FormatFloat(-x*100, 'f', d, 64) + "%)"
		}
		return s
	case "PVALUE":
		d := f.D
		if !f.HasD {
			d = 4
		}
		// SAS PVALUEw.d rounds half-up at d decimals; only values that
		// round to zero (p < 0.5*10^-d) print as <.00..1.
		if x < 0.5*math.Pow(10, -float64(d)) {
			return "<." + strings.Repeat("0", d-1) + "1"
		}
		s := strconv.FormatFloat(x, 'f', d, 64)
		return strings.TrimPrefix(s, "0")
	case "E":
		d := w - 7
		if d < 1 {
			d = 1
		}
		if f.HasD {
			d = f.D
		}
		return strings.ToUpper(strconv.FormatFloat(x, 'E', d, 64))
	case "Z":
		s := strconv.FormatFloat(x, 'f', f.D, 64)
		for len(s) < w {
			if strings.HasPrefix(s, "-") {
				s = "-0" + s[1:]
			} else {
				s = "0" + s
			}
		}
		return s
	case "DATE", "MMDDYY", "DDMMYY", "YYMMDD", "MONYY", "YEAR", "WORDDATE", "WEEKDATE", "DAY", "MONTH", "JULIAN", "MMDDYYS", "YYMMDDN":
		return formatDate(x, f)
	case "TIME", "HHMM":
		return formatTime(x, f)
	case "DATETIME":
		t := sasEpoch.Add(time.Duration(math.Floor(x)) * time.Second)
		return strings.ToUpper(t.Format("02Jan2006:15:04:05"))
	}
	if f.HasW {
		return formatFixed(x, w, f.D)
	}
	return formatBest(x, 12)
}

// FormatByName parses and applies a format string ("" = default).
func FormatByName(v Value, name string) string {
	if name == "" {
		return FormatValue(v, nil)
	}
	return FormatValue(v, ParseFormat(name))
}

// formatBest mimics BESTw.: the shortest representation that fits the width,
// preferring fixed notation with the leading "0." kept for magnitudes
// >= 1e-4 and scientific notation for smaller ones.
func formatBest(x float64, w int) string {
	if math.IsNaN(x) || math.IsInf(x, 0) {
		return "."
	}
	if x == 0 {
		return "0"
	}
	ax := math.Abs(x)
	fixed := func(d int) string {
		s := strconv.FormatFloat(x, 'f', d, 64)
		if strings.Contains(s, ".") {
			s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
		}
		return s
	}
	if ax >= 1e-4 {
		// most decimals that fit (d=0 only for whole numbers: a fractional
		// value rounded to an integer is a fit failure)
		for d := w; d >= 0; d-- {
			if d == 0 && x != math.Trunc(x) {
				continue
			}
			if s := fixed(d); len(s) <= w {
				return s
			}
		}
		// tight width: drop the leading zero ("0.5" -> ".5")
		for d := w; d >= 1; d-- {
			s := trimLeadingZero(fixed(d), w)
			if len(s) <= w {
				return s
			}
		}
	}
	return sciBest(x, w)
}

// sciBest picks the most precise scientific form that fits w characters.
func sciBest(x float64, w int) string {
	for d := w; d >= 0; d-- {
		s := strconv.FormatFloat(x, 'E', d, 64)
		mant, exp, _ := strings.Cut(s, "E")
		if strings.Contains(mant, ".") {
			mant = strings.TrimRight(strings.TrimRight(mant, "0"), ".")
		}
		e, _ := strconv.Atoi(exp)
		s = mant + "E" + strconv.Itoa(e)
		if len(s) <= w {
			return s
		}
	}
	return strings.Repeat("*", w)
}

func sigDigits(s string) int {
	n := 0
	started := false
	for _, c := range s {
		if c >= '1' && c <= '9' {
			started = true
		}
		if started && c >= '0' && c <= '9' {
			n++
		}
	}
	return n
}

// SAS drops the leading zero of 0.xxx when it does not fit; we keep it when it fits.
func trimLeadingZero(s string, w int) string {
	if len(s) <= w {
		return s
	}
	if strings.HasPrefix(s, "0.") {
		return s[1:]
	}
	if strings.HasPrefix(s, "-0.") {
		return "-" + s[2:]
	}
	return s
}

func formatFixed(x float64, w, d int) string {
	s := strconv.FormatFloat(x, 'f', d, 64)
	if s == "-0" || (strings.HasPrefix(s, "-0.") && strings.Trim(s[3:], "0") == "") {
		s = s[1:]
	}
	if w > 0 && len(s) > w {
		if t := trimLeadingZero(s, w); len(t) <= w {
			return t
		}
		return formatBest(x, w)
	}
	return s
}

func commaFixed(x float64, d int) string {
	s := strconv.FormatFloat(x, 'f', d, 64)
	intPart, frac, hasFrac := strings.Cut(s, ".")
	var b strings.Builder
	for i, c := range intPart {
		if i > 0 && (len(intPart)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(c)
	}
	if hasFrac {
		b.WriteByte('.')
		b.WriteString(frac)
	}
	return b.String()
}

// SAS dates are days since 1960-01-01; times are seconds.
var sasEpoch = time.Date(1960, 1, 1, 0, 0, 0, 0, time.UTC)

func DateToTime(days float64) time.Time {
	return sasEpoch.AddDate(0, 0, int(math.Floor(days)))
}

func TimeToDate(t time.Time) float64 {
	t = time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
	return math.Round(t.Sub(sasEpoch).Hours() / 24)
}

// MDY returns the SAS date for month/day/year (NaN if invalid).
func MDY(m, d, y int) float64 {
	if m < 1 || m > 12 || d < 1 || d > 31 {
		return math.NaN()
	}
	t := time.Date(y, time.Month(m), d, 0, 0, 0, 0, time.UTC)
	if t.Day() != d {
		return math.NaN()
	}
	return TimeToDate(t)
}

func formatDate(x float64, f *FormatSpec) string {
	t := DateToTime(x)
	up := strings.ToUpper
	w := f.W
	switch f.Name {
	case "DATE":
		if f.HasW && w <= 7 {
			return up(t.Format("02Jan06"))
		}
		return up(t.Format("02Jan2006"))
	case "MMDDYY", "MMDDYYS":
		if f.HasW && w <= 8 {
			return t.Format("01/02/06")
		}
		return t.Format("01/02/2006")
	case "DDMMYY":
		if f.HasW && w <= 8 {
			return t.Format("02/01/06")
		}
		return t.Format("02/01/2006")
	case "YYMMDD":
		if f.HasW && w <= 8 {
			return t.Format("06-01-02")
		}
		return t.Format("2006-01-02")
	case "YYMMDDN":
		return t.Format("20060102")
	case "MONYY":
		if f.HasW && w <= 5 {
			return up(t.Format("Jan06"))
		}
		return up(t.Format("Jan2006"))
	case "YEAR":
		return t.Format("2006")
	case "MONTH":
		return strconv.Itoa(int(t.Month()))
	case "DAY":
		return strconv.Itoa(t.Day())
	case "WORDDATE":
		return t.Format("January 2, 2006")
	case "WEEKDATE":
		return t.Format("Monday, January 2, 2006")
	case "JULIAN":
		return fmt.Sprintf("%s%03d", t.Format("06"), t.YearDay())
	}
	return t.Format("2006-01-02")
}

func formatTime(x float64, f *FormatSpec) string {
	s := int(math.Floor(x))
	h, m, sec := s/3600, (s%3600)/60, s%60
	if f.Name == "HHMM" {
		return fmt.Sprintf("%d:%02d", h, m)
	}
	return fmt.Sprintf("%d:%02d:%02d", h, m, sec)
}

// parseNumber parses a numeric string the way the default numeric informat does.
func parseNumber(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	if s == "" || s == "." {
		return math.NaN(), s == "." || s == ""
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return math.NaN(), false
	}
	return f, true
}

// ApplyInformat converts raw text to a value using an informat (nil = default
// numeric w.d / character). ok=false means invalid data (value is missing).
func ApplyInformat(raw string, f *FormatSpec, char bool) (Value, bool) {
	if char || (f != nil && f.Char) {
		s := raw
		if f != nil && f.Name == "" {
			// $w. reads leading blanks trimmed for list input; keep as-is otherwise
			s = strings.TrimLeft(s, " ")
		}
		return StrV(strings.TrimRight(s, " ")), true
	}
	s := strings.TrimSpace(raw)
	if s == "" || s == "." {
		return Missing, true
	}
	name := ""
	if f != nil {
		name = f.Name
	}
	switch name {
	case "COMMA", "DOLLAR", "COMMAX", "DOLLARX", "NLNUM":
		neg := strings.HasPrefix(s, "(") && strings.HasSuffix(s, ")")
		s = strings.NewReplacer(",", "", "$", "", "(", "", ")", "", "%", "").Replace(s)
		x, ok := parseNumber(s)
		if neg {
			x = -x
		}
		return NumV(x), ok
	case "PERCENT":
		s = strings.TrimSuffix(s, "%")
		x, ok := parseNumber(s)
		return NumV(x / 100), ok
	case "DATE", "MMDDYY", "DDMMYY", "YYMMDD", "ANYDTDTE", "MONYY", "YYMMDDN":
		d, ok := parseDateInformat(s, name)
		return NumV(d), ok
	case "TIME":
		parts := strings.Split(s, ":")
		tot := 0.0
		for i, p := range parts {
			v, ok := parseNumber(p)
			if !ok {
				return Missing, false
			}
			tot += v * math.Pow(60, float64(2-i))
		}
		return NumV(tot), true
	}
	x, ok := parseNumber(s)
	if ok && f != nil && f.HasD && f.D > 0 && !strings.ContainsAny(s, ".eE") {
		x /= math.Pow(10, float64(f.D))
	}
	return NumV(x), ok
}

var monthAbbr = map[string]int{"JAN": 1, "FEB": 2, "MAR": 3, "APR": 4, "MAY": 5, "JUN": 6, "JUL": 7, "AUG": 8, "SEP": 9, "OCT": 10, "NOV": 11, "DEC": 12}

func parseDateInformat(s, name string) (float64, bool) {
	u := strings.ToUpper(strings.TrimSpace(s))
	year := func(y int, digits int) int {
		if digits <= 2 { // YEARCUTOFF=1926
			if y < 26 {
				return 2000 + y
			}
			return 1900 + y
		}
		return y
	}
	// DATE: ddMONyy(yy)
	if len(u) >= 7 && unicode.IsLetter(rune(u[2])) || (len(u) >= 6 && unicode.IsLetter(rune(u[1])) && unicode.IsDigit(rune(u[0]))) {
		i := 0
		for i < len(u) && unicode.IsDigit(rune(u[i])) {
			i++
		}
		d, _ := strconv.Atoi(u[:i])
		if i+3 <= len(u) {
			m := monthAbbr[u[i:i+3]]
			ys := strings.TrimLeft(u[i+3:], "-/ ")
			y, err := strconv.Atoi(ys)
			if m > 0 && err == nil {
				return MDY(m, d, year(y, len(ys))), true
			}
		}
		return math.NaN(), false
	}
	if name == "MONYY" && len(u) >= 5 {
		m := monthAbbr[u[:3]]
		y, err := strconv.Atoi(u[3:])
		if m > 0 && err == nil {
			return MDY(m, 1, year(y, len(u)-3)), true
		}
	}
	sep := func(r rune) bool { return r == '/' || r == '-' || r == '.' || r == ' ' }
	parts := strings.FieldsFunc(u, sep)
	if len(parts) == 1 && len(u) == 8 { // compact
		if name == "YYMMDD" || name == "YYMMDDN" {
			parts = []string{u[:4], u[4:6], u[6:]}
		} else if name == "DDMMYY" {
			parts = []string{u[:2], u[2:4], u[4:]}
		} else {
			parts = []string{u[:2], u[2:4], u[4:]}
		}
	}
	if len(parts) != 3 {
		return math.NaN(), false
	}
	n := make([]int, 3)
	for i, p := range parts {
		v, err := strconv.Atoi(p)
		if err != nil {
			return math.NaN(), false
		}
		n[i] = v
	}
	var r float64
	switch name {
	case "YYMMDD", "YYMMDDN":
		r = MDY(n[1], n[2], year(n[0], len(parts[0])))
	case "DDMMYY":
		r = MDY(n[1], n[0], year(n[2], len(parts[2])))
	default:
		if len(parts[0]) == 4 {
			r = MDY(n[1], n[2], n[0])
		} else {
			r = MDY(n[0], n[1], year(n[2], len(parts[2])))
		}
	}
	return r, !math.IsNaN(r)
}
