// Why: the lexer must reject a malformed literal and the checker must read
// its value; one set of functions for both keeps them from disagreeing.
//
// What: IntValue, FloatValue, CharValue, ByteValue and StringValue, which
// decode a literal token's text, and the validators intForm, floatForm and
// decodeEscape that the lexer calls.
//
// How: each decoder works on the raw token text: prefixes and underscores
// are stripped, escapes decoded (\xHH is one byte in a byte literal, at most
// 7F elsewhere), and a """ literal loses the closing line's indentation from
// every content line.

package syntax

import (
	"errors"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"

	"mk/internal/ast"
)

// IntValue is the exact value of a TokInt: 0 to 2^64-1. ok is false for a
// spelling the lexer reports (a bad form, a suffix, a value over 2^64-1).
func IntValue(raw string) (v uint64, ok bool) {
	base, digits, msg := intForm(raw)
	if msg != "" {
		return 0, false
	}
	v, err := strconv.ParseUint(digits, base, 64)
	return v, err == nil
}

// FloatValue is the value of a TokFloat rounded once to the given size (32 or
// 64), parsing the decimal text directly at that size (never float64 then
// float32). ok is false when the form is bad or the value overflows that size.
func FloatValue(raw string, bits int) (v float64, ok bool) {
	digits, msg := floatForm(raw)
	if msg != "" {
		return 0, false
	}
	f, err := strconv.ParseFloat(digits, bits)
	if math.IsInf(f, 0) || (err != nil && !errors.Is(err, strconv.ErrRange)) {
		return 0, false
	}
	return f, true
}

// CharValue is the Unicode scalar of a TokChar.
func CharValue(raw string) (rune, bool) {
	if len(raw) < 3 || raw[0] != '\'' || raw[len(raw)-1] != '\'' {
		return 0, false
	}
	body := raw[1 : len(raw)-1]
	var r rune
	var n int
	if body[0] == '\\' {
		v, isByte, next, msg := decodeEscape(body, 0, false)
		if msg != "" {
			return 0, false
		}
		r, n = v, next
		_ = isByte
	} else {
		var sz int
		r, sz = utf8.DecodeRuneInString(body)
		if (r == utf8.RuneError && sz <= 1) || r == '\'' || r == '\n' {
			return 0, false
		}
		n = sz
	}
	if n != len(body) {
		return 0, false
	}
	return r, true
}

// ByteValue is the byte of a TokByte.
func ByteValue(raw string) (byte, bool) {
	if len(raw) < 4 || raw[0] != 'b' || raw[1] != '\'' || raw[len(raw)-1] != '\'' {
		return 0, false
	}
	body := raw[2 : len(raw)-1]
	if body[0] == '\\' {
		v, _, next, msg := decodeEscape(body, 0, true)
		if msg != "" || next != len(body) {
			return 0, false
		}
		return byte(v), true
	}
	c := body[0]
	if len(body) != 1 || c < 0x20 || c > 0x7E || c == '\'' {
		return 0, false
	}
	return c, true
}

// StringValue is the bytes of a TokStr, TokRawStr, TokMultiStr (dedented,
// escapes processed) or TokByteStr. Str, RawStr and MultiStr values are valid
// UTF-8.
func StringValue(kind ast.TokenKind, raw string) ([]byte, bool) {
	switch kind {
	case ast.TokStr:
		if len(raw) < 2 || raw[0] != '"' || raw[len(raw)-1] != '"' {
			return nil, false
		}
		return unescape(raw[1:len(raw)-1], false)
	case ast.TokByteStr:
		if len(raw) < 3 || raw[0] != 'b' || raw[1] != '"' || raw[len(raw)-1] != '"' {
			return nil, false
		}
		return unescape(raw[2:len(raw)-1], true)
	case ast.TokRawStr:
		if len(raw) < 3 || raw[0] != 'r' || raw[1] != '"' || raw[len(raw)-1] != '"' {
			return nil, false
		}
		body := raw[2 : len(raw)-1]
		if strings.ContainsAny(body, "\"\n") || !utf8.ValidString(body) {
			return nil, false
		}
		return []byte(body), true
	case ast.TokMultiStr:
		return multiValue(raw)
	}
	return nil, false
}

// unescape processes the escapes of a one-line string body.
func unescape(body string, byteMode bool) ([]byte, bool) {
	out := make([]byte, 0, len(body))
	for i := 0; i < len(body); {
		c := body[i]
		switch {
		case c == '\\':
			v, isByte, next, msg := decodeEscape(body, i, byteMode)
			if msg != "" {
				return nil, false
			}
			if isByte {
				out = append(out, byte(v))
			} else {
				out = utf8.AppendRune(out, v)
			}
			i = next
		case c == '"' || c == '\n':
			return nil, false
		case byteMode && !(c == '\t' || (c >= 0x20 && c <= 0x7E)):
			return nil, false
		default:
			if c >= 0x80 {
				r, sz := utf8.DecodeRuneInString(body[i:])
				if r == utf8.RuneError && sz <= 1 {
					return nil, false
				}
				out = append(out, body[i:i+sz]...)
				i += sz
				continue
			}
			out = append(out, c)
			i++
		}
	}
	return out, true
}

// multiValue decodes a """ literal: each content line with the
// closing line's indentation removed, each followed by "\n", escapes
// processed per line.
func multiValue(raw string) ([]byte, bool) {
	if len(raw) < 6 || !strings.HasPrefix(raw, `"""`) || !strings.HasSuffix(raw, `"""`) {
		return nil, false
	}
	nl := strings.IndexByte(raw, '\n')
	if nl < 0 {
		return nil, false
	}
	if strings.Trim(raw[3:nl], " \t") != "" {
		return nil, false
	}
	rest := raw[nl+1 : len(raw)-3]
	// rest is the content lines, each ending in "\n", then the closing line's
	// indentation.
	last := strings.LastIndexByte(rest, '\n')
	prefix := rest[last+1:]
	if strings.Trim(prefix, " \t") != "" {
		return nil, false
	}
	var out []byte
	if last < 0 {
		return []byte{}, true
	}
	for _, line := range strings.Split(rest[:last], "\n") {
		if strings.Trim(line, " \t") == "" {
			out = append(out, '\n')
			continue
		}
		if !strings.HasPrefix(line, prefix) {
			return nil, false
		}
		v, ok := unescapeMulti(line[len(prefix):])
		if !ok {
			return nil, false
		}
		out = append(out, v...)
		out = append(out, '\n')
	}
	if out == nil {
		out = []byte{}
	}
	return out, true
}

// unescapeMulti processes one content line of a """ literal; quotes are
// content there.
func unescapeMulti(line string) ([]byte, bool) {
	out := make([]byte, 0, len(line))
	for i := 0; i < len(line); {
		if line[i] == '\\' {
			if i+1 < len(line) && line[i+1] == '"' {
				out = append(out, '"')
				i += 2
				continue
			}
			v, _, next, msg := decodeEscape(line, i, false)
			if msg != "" {
				return nil, false
			}
			out = utf8.AppendRune(out, v)
			i = next
			continue
		}
		r, sz := utf8.DecodeRuneInString(line[i:])
		if r == utf8.RuneError && sz <= 1 {
			return nil, false
		}
		out = append(out, line[i:i+sz]...)
		i += sz
	}
	return out, true
}

// decodeEscape decodes the escape starting at s[i] == '\\'. isByte is
// true for a \xHH in a byte literal or string, whose value is one byte; in
// other literals \xHH is at most 7F. next is the index after the escape; msg
// is non-empty for an invalid escape.
func decodeEscape(s string, i int, byteMode bool) (v rune, isByte bool, next int, msg string) {
	if i+1 >= len(s) {
		return 0, false, len(s), "invalid escape: `\\` at the end"
	}
	switch s[i+1] {
	case 'n':
		return '\n', false, i + 2, ""
	case 'r':
		return '\r', false, i + 2, ""
	case 't':
		return '\t', false, i + 2, ""
	case '0':
		return 0, false, i + 2, ""
	case '\\':
		return '\\', false, i + 2, ""
	case '\'':
		return '\'', false, i + 2, ""
	case '"':
		return '"', false, i + 2, ""
	case 'x':
		if i+4 > len(s) {
			return 0, false, len(s), "`\\x` takes two hex digits"
		}
		h1, ok1 := hexDigit(s[i+2])
		h2, ok2 := hexDigit(s[i+3])
		if !ok1 || !ok2 {
			return 0, false, i + 2, "`\\x` takes two hex digits"
		}
		b := rune(h1*16 + h2)
		if !byteMode && b > 0x7F {
			return 0, false, i + 4, "`\\x` is at most 7F outside byte literals; use `\\u{…}`"
		}
		return b, byteMode, i + 4, ""
	case 'u':
		if byteMode {
			return 0, false, i + 2, "byte literals take no `\\u`"
		}
		if i+2 >= len(s) || s[i+2] != '{' {
			return 0, false, i + 2, "write `\\u{…}` with one to six hex digits"
		}
		j := i + 3
		var r rune
		for j < len(s) && s[j] != '}' {
			h, ok := hexDigit(s[j])
			if !ok || j-(i+3) >= 6 {
				return 0, false, j, "write `\\u{…}` with one to six hex digits"
			}
			r = r*16 + rune(h)
			j++
		}
		if j >= len(s) || j == i+3 {
			return 0, false, j, "write `\\u{…}` with one to six hex digits"
		}
		if !utf8.ValidRune(r) {
			return 0, false, j + 1, "`\\u{…}` must name a Unicode scalar"
		}
		return r, false, j + 1, ""
	}
	return 0, false, i + 2, "unknown escape"
}

func hexDigit(c byte) (int, bool) {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0'), true
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10, true
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10, true
	}
	return 0, false
}

// intForm validates the Mk spelling of an integer and returns its
// base and the digits without prefix and underscores; msg names the first
// problem.
func intForm(raw string) (base int, digits, msg string) {
	if raw == "" || !isDigit(raw[0]) {
		return 0, "", "expected a number"
	}
	base = 10
	body := raw
	prefixed := false
	if len(raw) >= 2 && raw[0] == '0' {
		switch raw[1] {
		case 'x':
			base = 16
		case 'o':
			base = 8
		case 'b':
			base = 2
		case 'X', 'O', 'B':
			return 0, "", "number prefixes are lowercase: `0" + strings.ToLower(raw[1:2]) + "`"
		}
		if base != 10 {
			body, prefixed = raw[2:], true
		}
	}
	var b strings.Builder
	for i := 0; i < len(body); i++ {
		c := body[i]
		if c == '_' {
			prevDigit := i > 0 && body[i-1] != '_'
			nextDigit := i+1 < len(body) && body[i+1] != '_' && isBaseDigit(body[i+1], base)
			if !(nextDigit && (prevDigit || (prefixed && i == 0))) {
				if i+1 < len(body) && isIdentStart(body[i+1]) && body[i+1] != '_' && !isBaseDigit(body[i+1], base) {
					return 0, "", suffixMsg
				}
				return 0, "", "`_` goes only between digits"
			}
			continue
		}
		if !isBaseDigit(c, base) {
			if isDigit(c) {
				return 0, "", "digit `" + string(c) + "` is not valid in base " + strconv.Itoa(base)
			}
			return 0, "", suffixMsg
		}
		b.WriteByte(c)
	}
	digits = b.String()
	if digits == "" {
		return 0, "", "a number prefix needs digits"
	}
	if base == 10 && len(digits) > 1 && digits[0] == '0' {
		return 0, "", leadingZeroMsg
	}
	if _, err := strconv.ParseUint(digits, base, 64); err != nil {
		return 0, "", "integer literal larger than 2^64-1"
	}
	return base, digits, ""
}

const (
	suffixMsg      = "literals have no suffix: write `x: u8 = 1` or `u8(1)`"
	leadingZeroMsg = "no leading zeros: Mk has no octal by `0`; write `0o17`"
)

func isBaseDigit(c byte, base int) bool {
	switch base {
	case 2:
		return c == '0' || c == '1'
	case 8:
		return c >= '0' && c <= '7'
	case 16:
		_, ok := hexDigit(c)
		return ok
	}
	return isDigit(c)
}

// floatForm validates the Mk spelling of a float and returns it
// without underscores.
func floatForm(raw string) (digits, msg string) {
	i := 0
	var b strings.Builder
	part := func() (int, string) {
		n := 0
		start := i
		for i < len(raw) && (isDigit(raw[i]) || raw[i] == '_') {
			if raw[i] == '_' {
				if i == start || i+1 >= len(raw) || !isDigit(raw[i+1]) || !isDigit(raw[i-1]) {
					if i+1 < len(raw) && isIdentStart(raw[i+1]) {
						return n, suffixMsg
					}
					return n, "`_` goes only between digits"
				}
			} else {
				b.WriteByte(raw[i])
				n++
			}
			i++
		}
		return n, ""
	}
	n, m := part()
	if m != "" {
		return "", m
	}
	if n == 0 {
		return "", "expected a number"
	}
	if n > 1 && raw[0] == '0' {
		return "", leadingZeroMsg
	}
	seen := false
	if i < len(raw) && raw[i] == '.' {
		b.WriteByte('.')
		i++
		n, m = part()
		if m != "" {
			return "", m
		}
		if n == 0 {
			return "", "a float needs digits after `.`"
		}
		seen = true
	}
	if i < len(raw) && (raw[i] == 'e' || raw[i] == 'E') {
		b.WriteByte('e')
		i++
		if i < len(raw) && (raw[i] == '+' || raw[i] == '-') {
			b.WriteByte(raw[i])
			i++
		}
		n, m = part()
		if m != "" {
			return "", m
		}
		if n == 0 {
			return "", "exponent needs digits"
		}
		seen = true
	}
	if i < len(raw) {
		return "", suffixMsg
	}
	if !seen {
		return "", "expected a float"
	}
	f, err := strconv.ParseFloat(b.String(), 64)
	if math.IsInf(f, 0) || (err != nil && !errors.Is(err, strconv.ErrRange)) {
		return "", "float literal out of range"
	}
	return b.String(), ""
}
