// Why: literal tokens have the most lexical rules (number bases, escapes,
// raw and multi-line strings), so they are kept apart from lexer.go.
//
// What: the lexer methods for numbers, chars, byte chars, strings, byte
// strings, raw strings and """ multi-line strings.
//
// How: each method finds the token's end and validates its spelling with
// the shared functions of literal.go (intForm, floatForm, decodeEscape). A
// malformed literal keeps its kind, so the parser goes on without a second
// error.

package syntax

import (
	"strings"
	"unicode/utf8"

	"mk/internal/ast"
)

// number lexes an integer or float literal. A malformed number keeps
// its literal kind so the parser continues without a second error.
func (lx *lexer) number() {
	text, start := lx.text, lx.pos
	n := uint32(len(text))
	i := start
	kind := ast.TokInt
	if text[i] == '0' && i+1 < n && strings.IndexByte("xobXOB", text[i+1]) >= 0 {
		i += 2
		for i < n && isIdentByte(text[i]) {
			i++
		}
	} else {
		for i < n && (isDigit(text[i]) || text[i] == '_') {
			i++
		}
		if i+1 < n && text[i] == '.' && isDigit(text[i+1]) {
			kind = ast.TokFloat
			i++
			for i < n && (isDigit(text[i]) || text[i] == '_') {
				i++
			}
		} else if i < n && text[i] == '.' && (i+1 >= n || text[i+1] != '.' && !isIdentStart(text[i+1])) && !lx.memberAfter(i+1) {
			// `1.` with no digit after the dot: neither a range nor a
			// member (a chain broken after the dot names one on the next
			// line), so a float missing its fraction.
			lx.pos = i + 1
			lx.emit(ast.TokFloat, start, i+1)
			lx.errAt(start, i+1, "a float needs digits on both sides of `.`: write `"+text[start:i+1]+"0`")
			return
		}
		if i < n && (text[i] == 'e' || text[i] == 'E') {
			j := i + 1
			if j < n && (text[j] == '+' || text[j] == '-') {
				j++
			}
			if j < n && isDigit(text[j]) {
				kind = ast.TokFloat
				i = j
				for i < n && (isDigit(text[i]) || text[i] == '_') {
					i++
				}
			} else if j > i+1 || j >= n || !isIdentByte(text[j]) {
				// 1e+ or 1e at a non-name byte: an exponent with no digits.
				kind = ast.TokFloat
				lx.pos = j
				lx.emit(kind, start, j)
				lx.errAt(start, j, "exponent needs digits")
				return
			}
		}
		// A suffix: the whole identifier run joins the token.
		for i < n && isIdentByte(text[i]) {
			i++
		}
	}
	lx.pos = i
	lx.emit(kind, start, i)
	raw := text[start:i]
	var msg string
	if kind == ast.TokInt {
		_, _, msg = intForm(raw)
	} else {
		_, msg = floatForm(raw)
	}
	if msg != "" {
		lx.errAt(start, i, msg)
	}
}

// memberAfter is true when the first byte after spaces and newlines at i
// may continue a member chain: a name that is not a keyword (`1.` then
// `Eq(&x)` on the next line is a chain broken after the dot), or a comment,
// which leaves the dot to the parser as before.
func (lx *lexer) memberAfter(i uint32) bool {
	text := lx.text
	n := uint32(len(text))
	for i < n && (text[i] == ' ' || text[i] == '\t' || text[i] == '\r' || text[i] == '\n') {
		i++
	}
	if i >= n {
		return false
	}
	if text[i] == '/' {
		return true
	}
	if !isIdentStart(text[i]) {
		return false
	}
	e := i
	for e < n && isIdentByte(text[e]) {
		e++
	}
	return identKind(text[i:e]) == ast.TokIdent
}

// operandEnded is true when the last significant token ends an operand, so a
// '.' after it is a member step (`t.0` stays the parser's).
func (lx *lexer) operandEnded() bool {
	switch lx.last {
	case ast.TokIdent, ast.TokRParen, ast.TokRBracket, ast.TokRBrace, ast.TokQuestion:
		return true
	}
	return lx.last.IsLiteral()
}

// leadingDotFloat lexes `.5` where no operand comes before the dot: a float
// missing its integer part, kept as a TokFloat so the parser goes on.
func (lx *lexer) leadingDotFloat(start uint32) {
	text := lx.text
	n := uint32(len(text))
	i := start + 1
	for i < n && isIdentByte(text[i]) {
		i++
	}
	lx.pos = i
	lx.emit(ast.TokFloat, start, i)
	lx.errAt(start, i, "a float needs digits on both sides of `.`: write `0"+text[start:i]+"`")
}

// quoteChar lexes at a ': a lifetime 'a (E1203 is the parser's) or a char
// literal.
func (lx *lexer) quoteChar() {
	text, start := lx.text, lx.pos
	n := uint32(len(text))
	if start+1 < n && isIdentStart(text[start+1]) {
		e := start + 2
		for e < n && isIdentByte(text[e]) {
			e++
		}
		if e >= n || text[e] != '\'' {
			lx.pos = e
			lx.emit(ast.TokLifetime, start, e)
			return
		}
	}
	lx.quoted(ast.TokChar, start, start+1, '\'', false)
}

// byteChar lexes b'…'.
func (lx *lexer) byteChar(start uint32) { lx.quoted(ast.TokByte, start, start+2, '\'', true) }

// byteStr lexes b"…".
func (lx *lexer) byteStr(start uint32) { lx.quoted(ast.TokByteStr, start, start+2, '"', true) }

// str lexes "…" or a """ multi-line literal.
func (lx *lexer) str() {
	text, start := lx.text, lx.pos
	if start+2 < uint32(len(text)) && text[start+1] == '"' && text[start+2] == '"' {
		lx.multi()
		return
	}
	lx.quoted(ast.TokStr, start, start+1, '"', false)
}

// quoted scans a one-line char, byte, string or byte-string literal whose
// body starts at body and ends at the first unescaped quote on the line.
// Only the first problem of the token is reported.
func (lx *lexer) quoted(kind ast.TokenKind, start, body uint32, quote byte, byteMode bool) {
	text := lx.text
	n := uint32(len(text))
	i := body
	items := 0
	msg := ""
	var msgAt, msgEnd uint32
	fail := func(m string, a, e uint32) {
		if msg == "" {
			msg, msgAt, msgEnd = m, a, e
		}
	}
	closed := false
	for i < n && text[i] != '\n' {
		c := text[i]
		if c == quote {
			closed = true
			i++
			break
		}
		items++
		if c == '\\' {
			_, _, next, m := decodeEscape(text[:lineEnd(text, i)], int(i), byteMode)
			if m != "" {
				fail(m, i, uint32(next))
			}
			if uint32(next) <= i {
				next = int(i) + 1
			}
			i = uint32(next)
			continue
		}
		if c >= 0x80 {
			r, sz := utf8.DecodeRuneInString(text[i:])
			switch {
			case r == utf8.RuneError && sz <= 1:
				fail("invalid UTF-8", i, i+1)
				i++
			case r == 0xFEFF:
				// A BOM anywhere past byte 0 is E0101.
				fail("byte-order mark inside the file", i, i+uint32(sz))
				i += uint32(sz)
			default:
				if byteMode {
					fail("byte literals are ASCII", i, i+uint32(sz))
				}
				i += uint32(sz)
			}
			continue
		}
		if byteMode && !(c == '\t' && kind == ast.TokByteStr) && (c < 0x20 || c > 0x7E) {
			fail("byte literals hold printable ASCII; use `\\xHH`", i, i+1)
		} else if !byteMode && c < 0x20 && c != '\t' {
			fail("control character in a literal; use an escape", i, i+1)
		}
		i++
	}
	lx.pos = i
	lx.emit(kind, start, i)
	switch {
	case !closed && (kind == ast.TokChar || kind == ast.TokByte):
		lx.errAt(start, i, "unterminated character literal")
	case !closed:
		lx.errAt(start, i, "unterminated string")
	case msg != "":
		lx.errAt(msgAt, msgEnd, msg)
	case (kind == ast.TokChar || kind == ast.TokByte) && items == 0:
		lx.errAt(start, i, "empty character literal")
	case (kind == ast.TokChar || kind == ast.TokByte) && items > 1:
		lx.errAt(start, i, "a char holds one Unicode scalar")
	}
}

func lineEnd(text string, i uint32) int {
	if j := strings.IndexByte(text[i:], '\n'); j >= 0 {
		return int(i) + j
	}
	return len(text)
}

// rawStr lexes r"…": no escapes, one line, no quote inside.
func (lx *lexer) rawStr(start uint32) {
	text := lx.text
	n := uint32(len(text))
	i := start + 2
	for i < n && text[i] != '"' && text[i] != '\n' {
		i++
	}
	if i < n && text[i] == '"' {
		i++
		lx.pos = i
		lx.emit(ast.TokRawStr, start, i)
		lx.checkContent(start, i)
		return
	}
	lx.pos = i
	lx.emit(ast.TokRawStr, start, i)
	lx.errAt(start, i, "a raw string is one line: use `\"\"\"` for several")
}

// multi lexes a """ literal: the opening line holds nothing else, the
// closing """ starts its line, and its indentation prefixes every non-blank
// content line.
func (lx *lexer) multi() {
	text, start := lx.text, lx.pos
	n := uint32(len(text))
	report := func(a, e uint32, m string) {
		lx.errAt(a, e, m)
	}
	reported := false
	once := func(a, e uint32, m string) {
		if !reported {
			reported = true
			report(a, e, m)
		}
	}
	i := start + 3
	for i < n && (text[i] == ' ' || text[i] == '\t') {
		i++
	}
	if i < n && text[i] != '\n' {
		once(i, i+1, "text after the opening `\"\"\"`")
		// The one-line form `"""abc"""` (Python, Kotlin): the token ends after
		// a closing `"""` on the opening line, so the rest of the file is not
		// swallowed and the line has one diagnostic.
		for i < n && text[i] != '\n' {
			if text[i] == '\\' && i+1 < n && text[i+1] != '\n' {
				i += 2
				continue
			}
			if i+3 <= n && text[i:i+3] == `"""` {
				lx.pos = i + 3
				lx.emit(ast.TokMultiStr, start, i+3)
				return
			}
			i++
		}
		i = min(i, n)
	}
	type line struct{ start, end uint32 }
	var lines []line
	end := uint32(0)
	prefix := ""
	for i < n {
		// i is at a '\n': the next line starts after it.
		ls := i + 1
		le := ls
		for le < n && text[le] != '\n' {
			le++
		}
		j := ls
		for j < le && (text[j] == ' ' || text[j] == '\t') {
			j++
		}
		if j+2 < n && j+3 <= le && text[j:j+3] == `"""` {
			prefix = text[ls:j]
			end = j + 3
			break
		}
		// A content line: look for an unescaped """ that is not first.
		for k := j; k < le; k++ {
			if text[k] == '\\' {
				k++
				continue
			}
			if k+3 <= le && text[k:k+3] == `"""` {
				once(k, k+3, "a closing `\"\"\"` must start its line (write `\\\"\"\"` in content)")
				break
			}
		}
		lines = append(lines, line{ls, le})
		i = le
	}
	if end == 0 {
		lx.pos = n
		lx.emit(ast.TokMultiStr, start, n)
		once(start, start+3, "unterminated multi-line string")
		return
	}
	lx.pos = end
	lx.emit(ast.TokMultiStr, start, end)
	if !reported {
		before := len(lx.diags)
		lx.checkContent(start, end)
		reported = len(lx.diags) > before
	}
	for _, l := range lines {
		s := text[l.start:l.end]
		if strings.Trim(s, " \t") == "" {
			continue
		}
		if !strings.HasPrefix(s, prefix) {
			once(l.start, l.end, "line does not start with the closing line's indentation")
			break
		}
		for k := l.start + uint32(len(prefix)); k < l.end; k++ {
			if text[k] != '\\' {
				continue
			}
			if k+1 < l.end && text[k+1] == '"' {
				k++
				continue
			}
			_, _, next, m := decodeEscape(text[:l.end], int(k), false)
			if m != "" {
				once(k, uint32(next), m)
				break
			}
			k = uint32(next) - 1
		}
	}
}
