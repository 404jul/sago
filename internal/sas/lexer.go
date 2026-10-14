package sas

import (
	"fmt"
	"math"
	"strings"
	"unicode"
)

type TokKind int

const (
	TIdent  TokKind = iota // names, keywords, lib.member, first.x, format names like best12.
	TNumber                // numeric literal; Text keeps source (e.g. "8.2", "15."); "." = missing
	TString                // quoted string; Text is unquoted content
	TOp                    // operator / punctuation
)

type Token struct {
	Kind TokKind
	Text string
	Num  float64 // TNumber value (also date/time literals)
	Line int
	// Quote is the quote char for strings ('\'' or '"'); Suffix is the literal
	// suffix for 'x'd / 'x't / 'x'dt / 'x'n literals.
	Quote  byte
	Suffix string
}

// Is reports whether the token is an identifier equal (case-insensitively) to one of words.
func (t Token) Is(words ...string) bool {
	if t.Kind != TIdent {
		return false
	}
	for _, w := range words {
		if strings.EqualFold(t.Text, w) {
			return true
		}
	}
	return false
}

// IsOp reports whether the token is the given operator.
func (t Token) IsOp(ops ...string) bool {
	if t.Kind != TOp {
		return false
	}
	for _, o := range ops {
		if t.Text == o {
			return true
		}
	}
	return false
}

// Upper returns the upper-cased token text.
func (t Token) Upper() string { return strings.ToUpper(t.Text) }

func (t Token) String() string {
	if t.Kind == TString {
		q := string(t.Quote)
		if q == "\x00" {
			q = "'"
		}
		return q + strings.ReplaceAll(t.Text, q, q+q) + q + t.Suffix
	}
	return t.Text
}

// Stmt is one SAS statement (without the terminating semicolon).
type Stmt struct {
	Toks      []Token
	Line      int
	Text      string   // source text after macro resolution
	Datalines []string // raw data lines following DATALINES/CARDS
	Label     string   // statement label ("x2x3: test ...")
}

// Keyword returns the upper-cased first identifier, or "" if none.
func (s *Stmt) Keyword() string {
	if len(s.Toks) == 0 || s.Toks[0].Kind != TIdent {
		if len(s.Toks) > 0 && s.Toks[0].IsOp("%") && len(s.Toks) > 1 {
			return "%" + s.Toks[1].Upper()
		}
		return ""
	}
	return s.Toks[0].Upper()
}

// Args returns tokens after the keyword.
func (s *Stmt) Args() []Token {
	if len(s.Toks) == 0 {
		return nil
	}
	return s.Toks[1:]
}

func (s *Stmt) String() string { return TokText(s.Toks) }

// TokText renders tokens back to source-like text.
func TokText(toks []Token) string {
	var b strings.Builder
	for i, t := range toks {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(t.String())
	}
	return b.String()
}

// SplitProgram splits source into statements, handling comments, quoted
// strings, %LET / &macro references, and DATALINES/CARDS blocks.
func SplitProgram(src string, macros map[string]string, log *Log) []*Stmt {
	src = strings.ReplaceAll(src, "\r\n", "\n")
	src = strings.TrimPrefix(src, "\ufeff")
	lines := strings.Split(src, "\n")
	var stmts []*Stmt
	var cur strings.Builder
	curLine := 0
	inBlockComment := false
	var quote byte
	stmtStart := true // only whitespace seen in current statement
	inStarComment := false
	inMacroDef := 0

	flush := func(lineIdx int) (*Stmt, bool) {
		text := strings.TrimSpace(cur.String())
		cur.Reset()
		stmtStart = true
		if text == "" {
			return nil, false
		}
		text = ResolveMacros(text, macros, log)
		low := strings.ToLower(text)
		if strings.HasPrefix(low, "%macro") {
			inMacroDef++
			log.Error(curLine, "Macro definitions (%MACRO/%MEND) are not supported by sago.")
			return nil, false
		}
		if inMacroDef > 0 {
			if strings.HasPrefix(low, "%mend") {
				inMacroDef--
			}
			return nil, false
		}
		if strings.HasPrefix(low, "%let") {
			body := strings.TrimSpace(text[4:])
			name, val, ok := strings.Cut(body, "=")
			if ok {
				macros[strings.ToUpper(strings.TrimSpace(name))] = strings.TrimSpace(val)
			}
			return nil, false
		}
		if strings.HasPrefix(low, "%put") {
			log.Put(strings.TrimSpace(text[4:]))
			return nil, false
		}
		st := &Stmt{Line: curLine, Text: text}
		toks, err := Tokenize(text, curLine)
		if err != nil {
			log.Error(curLine, err.Error())
		}
		// statement label  "name: stmt"
		if len(toks) > 2 && toks[0].Kind == TIdent && toks[1].IsOp(":") && toks[2].Kind == TIdent {
			st.Label = toks[0].Text
			toks = toks[2:]
		}
		st.Toks = toks
		return st, true
	}

	for li := 0; li < len(lines); li++ {
		line := lines[li]
		lineNo := li + 1
		for i := 0; i < len(line); i++ {
			c := line[i]
			if inBlockComment {
				if c == '*' && i+1 < len(line) && line[i+1] == '/' {
					inBlockComment = false
					i++
				}
				continue
			}
			if inStarComment {
				if c == ';' {
					inStarComment = false
					stmtStart = true
				}
				continue
			}
			if quote != 0 {
				cur.WriteByte(c)
				if c == quote {
					if i+1 < len(line) && line[i+1] == quote {
						cur.WriteByte(quote)
						i++
					} else {
						quote = 0
					}
				}
				continue
			}
			if c == '/' && i+1 < len(line) && line[i+1] == '*' {
				inBlockComment = true
				i++
				continue
			}
			if stmtStart {
				if c == ' ' || c == '\t' {
					continue
				}
				if c == '*' {
					inStarComment = true
					continue
				}
				stmtStart = false
				curLine = lineNo
			}
			if c == '\'' || c == '"' {
				quote = c
				cur.WriteByte(c)
				continue
			}
			if c == ';' {
				st, ok := flush(li)
				if !ok {
					continue
				}
				stmts = append(stmts, st)
				kw := st.Keyword()
				if kw == "DATALINES" || kw == "CARDS" || kw == "LINES" || kw == "DATALINES4" || kw == "CARDS4" || kw == "LINES4" {
					four := strings.HasSuffix(kw, "4")
					// data starts on next line
					j := li + 1
					for ; j < len(lines); j++ {
						l := lines[j]
						if four {
							if strings.HasPrefix(strings.TrimSpace(l), ";;;;") {
								break
							}
						} else if strings.Contains(l, ";") {
							break
						}
						st.Datalines = append(st.Datalines, l)
					}
					li = j // the terminator line is consumed
					i = len(line)
					stmtStart = true
					break
				}
				continue
			}
			cur.WriteByte(c)
		}
		if quote == 0 && !inStarComment && !inBlockComment {
			cur.WriteByte('\n')
		} else if quote != 0 {
			cur.WriteByte('\n')
		} else if !stmtStart {
			cur.WriteByte('\n')
		}
	}
	if st, ok := flush(len(lines)); ok {
		stmts = append(stmts, st)
	}
	return stmts
}

// ResolveMacros replaces &name and &name. references (not inside single quotes).
func ResolveMacros(text string, macros map[string]string, log *Log) string {
	if !strings.Contains(text, "&") {
		return text
	}
	for pass := 0; pass < 10 && strings.Contains(text, "&"); pass++ {
		var b strings.Builder
		var quote byte
		changed := false
		for i := 0; i < len(text); i++ {
			c := text[i]
			if quote != 0 {
				b.WriteByte(c)
				if c == quote {
					quote = 0
				}
				continue
			}
			if c == '\'' {
				quote = c
				b.WriteByte(c)
				continue
			}
			if c == '&' && i+1 < len(text) && (isIdentStart(text[i+1]) || text[i+1] == '&') {
				j := i + 1
				for j < len(text) && text[j] == '&' {
					j++
				}
				k := j
				for k < len(text) && isIdentChar(text[k]) {
					k++
				}
				name := strings.ToUpper(text[j:k])
				val, ok := macros[name]
				if !ok {
					val, ok = autoMacro(name)
				}
				if !ok {
					if log != nil {
						log.Warn(0, fmt.Sprintf("Apparent symbolic reference %s not resolved.", name))
					}
					b.WriteString(text[i:k])
					i = k - 1
					continue
				}
				if k < len(text) && text[k] == '.' {
					k++
				}
				b.WriteString(val)
				i = k - 1
				changed = true
				continue
			}
			b.WriteByte(c)
		}
		text = b.String()
		if !changed {
			break
		}
	}
	return text
}

func autoMacro(name string) (string, bool) {
	switch name {
	case "SYSDATE9":
		return strings.ToUpper(nowFunc().Format("02Jan2006")), true
	case "SYSDATE":
		return strings.ToUpper(nowFunc().Format("02Jan06")), true
	case "SYSTIME":
		return nowFunc().Format("15:04"), true
	}
	return "", false
}

func isIdentStart(c byte) bool { return c == '_' || unicode.IsLetter(rune(c)) }
func isIdentChar(c byte) bool  { return c == '_' || unicode.IsLetter(rune(c)) || (c >= '0' && c <= '9') }
func isDigit(c byte) bool      { return c >= '0' && c <= '9' }

var multiOps = []string{"**", "||", "!!", "<=", ">=", "^=", "~=", "¬=", "=:", "<>", "><", "@@", "--"}

// Tokenize splits a statement's text into tokens.
func Tokenize(text string, line int) ([]Token, error) {
	var toks []Token
	ln := line
	n := len(text)
	for i := 0; i < n; {
		c := text[i]
		if c == '\n' {
			ln++
			i++
			continue
		}
		if c == ' ' || c == '\t' || c == '\r' || c == '\f' {
			i++
			continue
		}
		start := i
		switch {
		case isIdentStart(c):
			for i < n && isIdentChar(text[i]) {
				i++
			}
			// dotted continuation: lib.member, first.x, best12., date9., dollar8.2
			for i < n && text[i] == '.' {
				i++
				for i < n && isIdentChar(text[i]) {
					i++
				}
			}
			toks = append(toks, Token{Kind: TIdent, Text: text[start:i], Line: ln})
		case isDigit(c) || (c == '.' && i+1 < n && isDigit(text[i+1])):
			for i < n && isDigit(text[i]) {
				i++
			}
			if i < n && text[i] == '.' {
				i++
				for i < n && isDigit(text[i]) {
					i++
				}
			}
			if i < n && (text[i] == 'e' || text[i] == 'E') {
				j := i + 1
				if j < n && (text[j] == '+' || text[j] == '-') {
					j++
				}
				if j < n && isDigit(text[j]) {
					for j < n && isDigit(text[j]) {
						j++
					}
					i = j
				}
			}
			s := text[start:i]
			f, _ := parseNumber(strings.TrimSuffix(s, "."))
			if strings.HasSuffix(s, ".") && s != "." {
				f, _ = parseNumber(s[:len(s)-1])
			}
			// number immediately followed by letters (e.g. 1e, 10b) -> include as ident-ish token
			toks = append(toks, Token{Kind: TNumber, Text: s, Num: f, Line: ln})
		case c == '.':
			// missing value or special missing (.A ... .Z, ._)
			i++
			if i < n && (unicode.IsLetter(rune(text[i])) || text[i] == '_') && (i+1 >= n || !isIdentChar(text[i+1])) {
				i++
			}
			toks = append(toks, Token{Kind: TNumber, Text: ".", Num: math.NaN(), Line: ln})
		case c == '\'' || c == '"':
			q := c
			var b strings.Builder
			i++
			closed := false
			for i < n {
				if text[i] == q {
					if i+1 < n && text[i+1] == q {
						b.WriteByte(q)
						i += 2
						continue
					}
					i++
					closed = true
					break
				}
				if text[i] == '\n' {
					ln++
				}
				b.WriteByte(text[i])
				i++
			}
			if !closed {
				return toks, fmt.Errorf("unterminated quoted string")
			}
			tok := Token{Kind: TString, Text: b.String(), Line: ln, Quote: q}
			// literal suffixes: 'd 'dt 't 'n 'x
			j := i
			for j < n && unicode.IsLetter(rune(text[j])) {
				j++
			}
			suf := strings.ToLower(text[i:j])
			switch suf {
			case "d":
				d, ok := parseDateInformat(tok.Text, "DATE")
				if !ok {
					return toks, fmt.Errorf("invalid date constant '%s'd", tok.Text)
				}
				tok = Token{Kind: TNumber, Text: "'" + tok.Text + "'d", Num: d, Line: ln}
				i = j
			case "t":
				v, ok := ApplyInformat(tok.Text, &FormatSpec{Name: "TIME"}, false)
				if !ok {
					return toks, fmt.Errorf("invalid time constant '%s't", tok.Text)
				}
				tok = Token{Kind: TNumber, Text: "'" + tok.Text + "'t", Num: v.Num, Line: ln}
				i = j
			case "dt":
				dpart, tpart, _ := strings.Cut(tok.Text, ":")
				d, ok := parseDateInformat(dpart, "DATE")
				tv, ok2 := ApplyInformat(tpart, &FormatSpec{Name: "TIME"}, false)
				if !ok || !ok2 {
					return toks, fmt.Errorf("invalid datetime constant '%s'dt", tok.Text)
				}
				tok = Token{Kind: TNumber, Text: "'" + tok.Text + "'dt", Num: d*86400 + tv.Num, Line: ln}
				i = j
			case "n":
				tok = Token{Kind: TIdent, Text: tok.Text, Line: ln}
				i = j
			}
			toks = append(toks, tok)
		default:
			matched := false
			for _, op := range multiOps {
				if strings.HasPrefix(text[i:], op) {
					// "--" only as a var-list operator between names; treat "a - -b" via spaces
					toks = append(toks, Token{Kind: TOp, Text: op, Line: ln})
					i += len(op)
					matched = true
					break
				}
			}
			if !matched {
				r := text[i]
				if r >= 0x80 { // UTF-8 multibyte, e.g. ¬
					_, size := decodeRune(text[i:])
					toks = append(toks, Token{Kind: TOp, Text: text[i : i+size], Line: ln})
					i += size
				} else {
					toks = append(toks, Token{Kind: TOp, Text: string(r), Line: ln})
					i++
				}
			}
		}
	}
	return toks, nil
}

func decodeRune(s string) (rune, int) {
	for i, r := range s {
		if i == 0 {
			size := len(string(r))
			return r, size
		}
	}
	return 0, 1
}
