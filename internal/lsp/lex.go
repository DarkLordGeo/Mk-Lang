// Why: the server must answer on text that does not compile, so it needs its own
// scanner; internal/lsp may not import the compiler's syntax packages.
//
// What: token, scan and code (the tokens without comments), and the word sets
// keywords, flowWords and primTypes.
//
// How: a scanner by the language's lexical rules with the token classes of the
// Go-style Mk highlighter. It never fails: an unterminated literal ends at
// its line's end, an unknown byte is punctuation.

package lsp

// tokKind is a scanned token's kind.
type tokKind uint8

const (
	tIdent tokKind = iota
	tNumber
	tString
	tComment
	tPunct
)

// tokClass is a token's highlight class: the highlighter's c, s, n, k, f,
// t, in tokenLegend's order, or classNone.
type tokClass int8

const (
	classNone    tokClass = -1
	classComment tokClass = 0
	classString  tokClass = 1
	classNumber  tokClass = 2
	classKeyword tokClass = 3
	classFlow    tokClass = 4
	classType    tokClass = 5
)

// token is one scanned token: [Start, End) bytes of the scanned text.
type token struct {
	Kind       tokKind
	Start, End int
	Doc        bool // a `///` comment
	NL         bool // the first token of its line
}

// keywords, flowWords and primTypes are the highlighter's three tables.
var keywords = setOf("import export struct enum error interface impl type const var linear view gen quote test extern if else for in match return break continue defer onerr fail try scope go unsafe asm become mut fn is requires ensures invariant")
var flowWords = setOf("from into ends")
var primTypes = setOf("i8 i16 i32 i64 u8 u16 u32 u64 int uint f32 f64 bool char void never str")

func setOf(words string) map[string]bool {
	m := map[string]bool{}
	start := 0
	for i := 0; i <= len(words); i++ {
		if i == len(words) || words[i] == ' ' {
			if i > start {
				m[words[start:i]] = true
			}
			start = i + 1
		}
	}
	return m
}

func isIdentStart(c byte) bool {
	return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= 0x80
}

func isIdentByte(c byte) bool { return isIdentStart(c) || c >= '0' && c <= '9' }

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

// scan returns text's tokens in order.
func scan(text string) []token {
	var toks []token
	nl := true
	i, n := 0, len(text)
	for i < n {
		c := text[i]
		switch {
		case c == '\n':
			nl = true
			i++
			continue
		case c == ' ' || c == '\t' || c == '\r':
			i++
			continue
		}
		t := token{Start: i, NL: nl}
		nl = false
		switch {
		case c == '/' && i+1 < n && text[i+1] == '/':
			j := lineEnd(text, i)
			t.Kind, t.End = tComment, j
			t.Doc = j-i >= 3 && text[i+2] == '/' && (j-i == 3 || text[i+3] != '/')
		case c == '"' && i+2 < n && text[i+1] == '"' && text[i+2] == '"':
			j := indexFrom(text, i+3, `"""`)
			if j < 0 {
				j = n
			} else {
				j += 3
			}
			t.Kind, t.End = tString, j
		case (c == 'r' || c == 'b') && i+1 < n && (text[i+1] == '"' || c == 'b' && text[i+1] == '\''):
			t.Kind, t.End = tString, quoted(text, i+1, c == 'r')
		case c == '"' || c == '\'':
			t.Kind, t.End = tString, quoted(text, i, false)
		case isDigit(c):
			j := i + 1
			for j < n && (isIdentByte(text[j]) || text[j] == '.' && j+1 < n && isDigit(text[j+1]) && text[j-1] != '.') {
				j++
			}
			t.Kind, t.End = tNumber, j
		case isIdentStart(c):
			j := i + 1
			for j < n && isIdentByte(text[j]) {
				j++
			}
			t.Kind, t.End = tIdent, j
		default:
			t.Kind, t.End = tPunct, i+1
		}
		toks = append(toks, t)
		i = t.End
	}
	return toks
}

// lineEnd is the offset of the '\n' ending i's line, or len(text).
func lineEnd(text string, i int) int {
	for i < len(text) && text[i] != '\n' {
		i++
	}
	return i
}

func indexFrom(text string, i int, sub string) int {
	for ; i+len(sub) <= len(text); i++ {
		if text[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// quoted is the end of the literal whose opening quote is at i: just past
// the closing quote, or the line's end when there is none. A raw literal
// has no escapes.
func quoted(text string, i int, raw bool) int {
	q := text[i]
	j := i + 1
	for j < len(text) && text[j] != '\n' {
		switch {
		case text[j] == '\\' && !raw && j+1 < len(text) && text[j+1] != '\n':
			j += 2
			continue
		case text[j] == q:
			return j + 1
		}
		j++
	}
	return j
}

// class is the highlight class of tok in text.
func class(text string, t token) tokClass {
	switch t.Kind {
	case tComment:
		return classComment
	case tString:
		return classString
	case tNumber:
		return classNumber
	case tIdent:
		w := text[t.Start:t.End]
		switch {
		case keywords[w]:
			return classKeyword
		case flowWords[w]:
			return classFlow
		case primTypes[w]:
			return classType
		}
	}
	return classNone
}

// tokenAt is the index of the identifier token of toks that holds or ends
// at off, or -1.
func tokenAt(toks []token, off int) int {
	lo, hi := 0, len(toks)
	for lo < hi {
		m := (lo + hi) / 2
		if toks[m].End < off {
			lo = m + 1
		} else {
			hi = m
		}
	}
	for i := lo; i < len(toks) && toks[i].Start <= off; i++ {
		if toks[i].Kind == tIdent && toks[i].Start <= off && off <= toks[i].End {
			return i
		}
	}
	return -1
}

// code drops comments, for the scans that read the program. A `//` comment
// runs to its line's end, so the token after one starts a line already.
func code(toks []token) []token {
	out := make([]token, 0, len(toks))
	for _, t := range toks {
		if t.Kind != tComment {
			out = append(out, t)
		}
	}
	return out
}
