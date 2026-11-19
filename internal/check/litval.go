// Why: check records the values of char, byte and string literals in
// Body.Consts, and reads a format string's bytes, but may not import syntax
// outside tests (the package DAG forbids it).
//
// What: litString, multiLit, litChar, unescapeLit, litEscape and hexVal.
//
// How: the parser has already accepted every literal that reaches check (a
// malformed one is its E0101), so this decoder follows syntax/literal.go's
// escapes without re-reporting.

package check

import (
	"strings"
	"unicode/utf8"

	"mk/internal/ast"
)

// litString is the bytes of a string or byte-string literal; false for a
// form it does not decode (one the parser has already reported).
func litString(kind ast.TokenKind, raw string) ([]byte, bool) {
	switch kind {
	case ast.TokStr:
		if len(raw) >= 2 && raw[0] == '"' && raw[len(raw)-1] == '"' {
			return unescapeLit(raw[1 : len(raw)-1])
		}
	case ast.TokByteStr:
		if len(raw) >= 3 && strings.HasPrefix(raw, `b"`) && raw[len(raw)-1] == '"' {
			return unescapeLit(raw[2 : len(raw)-1])
		}
	case ast.TokRawStr:
		if len(raw) >= 3 && strings.HasPrefix(raw, `r"`) && raw[len(raw)-1] == '"' {
			return []byte(raw[2 : len(raw)-1]), true
		}
	case ast.TokMultiStr:
		return multiLit(raw)
	}
	return nil, false
}

// multiLit decodes a """ literal as syntax/literal.go's multiValue does
// the opening line holds nothing after the quotes,
// the closing line's indentation is removed from every content line, a
// blank line is "\n", each content line is followed by "\n", and escapes are
// processed per line (a quote needs no escape there).
func multiLit(raw string) ([]byte, bool) {
	if len(raw) < 6 || !strings.HasPrefix(raw, `"""`) || !strings.HasSuffix(raw, `"""`) {
		return nil, false
	}
	nl := strings.IndexByte(raw, '\n')
	if nl < 0 || strings.Trim(raw[3:nl], " \t") != "" {
		return nil, false
	}
	rest := raw[nl+1 : len(raw)-3]
	last := strings.LastIndexByte(rest, '\n')
	prefix := rest[last+1:]
	if strings.Trim(prefix, " \t") != "" {
		return nil, false
	}
	out := []byte{}
	if last < 0 {
		return out, true
	}
	for _, line := range strings.Split(rest[:last], "\n") {
		if strings.Trim(line, " \t") == "" {
			out = append(out, '\n')
			continue
		}
		if !strings.HasPrefix(line, prefix) {
			return nil, false
		}
		v, ok := unescapeLit(line[len(prefix):])
		if !ok {
			return nil, false
		}
		out = append(out, v...)
		out = append(out, '\n')
	}
	return out, true
}

// litChar is the scalar of a char literal 'c' or the byte of b'c'.
func litChar(raw string) (rune, bool) {
	raw = strings.TrimPrefix(raw, "b")
	if len(raw) < 3 || raw[0] != '\'' || raw[len(raw)-1] != '\'' {
		return 0, false
	}
	body := raw[1 : len(raw)-1]
	if body[0] == '\\' {
		r, next, ok := litEscape(body, 0)
		return r, ok && next == len(body)
	}
	r, n := utf8.DecodeRuneInString(body)
	return r, n == len(body) && r != utf8.RuneError
}

func unescapeLit(body string) ([]byte, bool) {
	out := make([]byte, 0, len(body))
	for i := 0; i < len(body); {
		if body[i] != '\\' {
			out = append(out, body[i])
			i++
			continue
		}
		r, next, ok := litEscape(body, i)
		if !ok {
			return nil, false
		}
		if body[i+1] == 'x' {
			out = append(out, byte(r))
		} else {
			out = utf8.AppendRune(out, r)
		}
		i = next
	}
	return out, true
}

// litEscape decodes the escape at s[i] (a backslash).
func litEscape(s string, i int) (rune, int, bool) {
	if i+1 >= len(s) {
		return 0, 0, false
	}
	switch s[i+1] {
	case 'n':
		return '\n', i + 2, true
	case 'r':
		return '\r', i + 2, true
	case 't':
		return '\t', i + 2, true
	case '0':
		return 0, i + 2, true
	case '\\', '\'', '"':
		return rune(s[i+1]), i + 2, true
	case 'x':
		if i+4 <= len(s) {
			if h, ok := hexVal(s[i+2 : i+4]); ok {
				return h, i + 4, true
			}
		}
	case 'u':
		if i+2 < len(s) && s[i+2] == '{' {
			if j := strings.IndexByte(s[i+3:], '}'); j > 0 {
				if h, ok := hexVal(s[i+3 : i+3+j]); ok {
					return h, i + 4 + j, true
				}
			}
		}
	}
	return 0, 0, false
}

func hexVal(s string) (rune, bool) {
	var r rune
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= '0' && c <= '9':
			r = r*16 + rune(c-'0')
		case c >= 'a' && c <= 'f':
			r = r*16 + rune(c-'a'+10)
		case c >= 'A' && c <= 'F':
			r = r*16 + rune(c-'A'+10)
		default:
			return 0, false
		}
	}
	return r, s != ""
}
