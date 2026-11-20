// Why: every later stage, the tooling and the cache work from tokens, so
// the lexer must keep every byte (comments and whitespace included) and
// decide where statements end without the parser's help.
//
// What: Lex and lexWith; the lexer state with its bracket stack (frame);
// comments, identifiers and keywords (identKind), punctuation by maximal
// munch, and one TokBad for each foreign byte or form (/* */, #[...]).
//
// How: a newline ends a statement (an emitted Term) only when the innermost
// open bracket is a '{' or none is open, and the last token can end a
// statement. A closer pops the stack down to its matching opener when one
// is open and is ignored otherwise, so one stray bracket cannot swallow the
// rest of the file.

package syntax

import (
	"unicode/utf8"

	"mk/internal/ast"
	"mk/internal/diag"
	"mk/internal/source"
)

// frame is one open bracket of the lexer's stack.
type frame struct {
	kind      byte   // '(', '[' or '{'
	at        uint32 // offset of the opener
	synthetic bool   // a fragment's initial statement frame: never popped or reported
}

// lexer is the state of one pass over a file's bytes.
type lexer struct {
	file          source.FileID
	text          string
	pos           uint32
	toks          []ast.Token
	stack         []frame
	last          ast.TokenKind // last significant token emitted; TokInvalid at start
	lastBackslash bool          // last is a TokBad holding a lone '\'
	diags         []*diag.Diagnostic
	lexAt         []uint32 // offsets of the token-local E0101s, ascending
	openAt        uint32   // offset of the unclosed opener reported at EOF; noOffset when none
}

const noOffset = ^uint32(0)

// Lex tokenizes the normalized text of one file (source.Normalize has
// stripped a leading BOM and turned CRLF into LF). The result covers text
// exactly and ends with TokEOF at len(text), with TokTerm inserted by the
// newline rule. The diagnostics are lexical E0101s in offset order, all in
// phase parse. Lex never panics on any input.
func Lex(file source.FileID, text string) ([]ast.Token, []*diag.Diagnostic) {
	lx := lexWith(file, text, false)
	return lx.toks, lx.diags
}

// lexWith runs the lexer; stmtFrame opens a synthetic statement frame first,
// for a statement fragment.
func lexWith(file source.FileID, text string, stmtFrame bool) *lexer {
	lx := &lexer{file: file, text: text, openAt: noOffset}
	lx.toks = make([]ast.Token, 0, len(text)/3+16)
	if stmtFrame {
		lx.stack = append(lx.stack, frame{kind: '{', synthetic: true})
	}
	lx.run()
	return lx
}

// errAt reports a lexical E0101 over [start, end).
func (lx *lexer) errAt(start, end uint32, msg string) *diag.Diagnostic {
	d := diag.New(diag.E0101, source.Span{File: lx.file, Start: start, End: end}, msg)
	lx.diags = append(lx.diags, d)
	lx.lexAt = append(lx.lexAt, start)
	return d
}

// emit appends a token over [start, end) and tracks the last significant kind.
func (lx *lexer) emit(k ast.TokenKind, start, end uint32) {
	lx.toks = append(lx.toks, ast.Token{Kind: k, Start: start, Len: end - start})
	if !k.IsTrivia() {
		lx.last = k
		lx.lastBackslash = false
	}
}

// emitBad appends a TokBad and reports msg over it.
func (lx *lexer) emitBad(start, end uint32, msg string) *diag.Diagnostic {
	lx.emit(ast.TokBad, start, end)
	return lx.errAt(start, end, msg)
}

// stmtMode is true when a newline may end a statement: the innermost frame is
// a '{' or there is none (newlines inside ( ) and [ ] are ignored).
func (lx *lexer) stmtMode() bool {
	return len(lx.stack) == 0 || lx.stack[len(lx.stack)-1].kind == '{'
}

// endsStmt is the newline rule's test on the last significant token: a
// token that can end a statement, or a TokBad other than a lone '\'.
func (lx *lexer) endsStmt() bool {
	if lx.last == ast.TokBad {
		return !lx.lastBackslash
	}
	return lx.last.CanEndStatement()
}

func (lx *lexer) term(at uint32) { lx.emit(ast.TokTerm, at, at) }

func isIdentStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isIdentByte(c byte) bool { return isIdentStart(c) || (c >= '0' && c <= '9') }

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

// run is the main loop.
func (lx *lexer) run() {
	text := lx.text
	n := uint32(len(text))
	for lx.pos < n {
		start := lx.pos
		c := text[start]
		switch {
		case c == ' ' || c == '\t':
			e := start + 1
			for e < n && (text[e] == ' ' || text[e] == '\t') {
				e++
			}
			lx.pos = e
			lx.emit(ast.TokWhitespace, start, e)
		case c == '\n':
			if lx.stmtMode() && lx.endsStmt() && lx.last != ast.TokTerm {
				lx.term(start)
			}
			lx.pos = start + 1
			lx.emit(ast.TokNewline, start, start+1)
		case c == '/' && start+1 < n && text[start+1] == '/':
			lx.comment()
		case c == '/' && start+1 < n && text[start+1] == '*':
			lx.blockComment()
		case isIdentStart(c):
			lx.ident()
		case isDigit(c):
			lx.number()
		case c == '\'':
			lx.quoteChar()
		case c == '"':
			lx.str()
		case c >= 0x80:
			lx.nonASCII()
		default:
			lx.punct()
		}
	}
	for i := len(lx.stack) - 1; i >= 0; i-- {
		if !lx.stack[i].synthetic {
			f := lx.stack[i]
			lx.errAt(f.at, f.at+1, "unclosed `"+string(f.kind)+"`")
			lx.openAt = f.at
			break
		}
	}
	if lx.last != ast.TokTerm && lx.last != ast.TokInvalid {
		lx.term(n)
	}
	lx.emit(ast.TokEOF, n, n)
}

// comment lexes // to the end of the line: exactly three slashes not
// followed by a fourth is a doc comment.
func (lx *lexer) comment() {
	text, start := lx.text, lx.pos
	e := start
	for e < uint32(len(text)) && text[e] != '\n' {
		e++
	}
	k := ast.TokComment
	if e-start >= 3 && text[start+2] == '/' && (e-start == 3 || text[start+3] != '/') {
		k = ast.TokDocComment
	}
	lx.checkContent(start, e)
	lx.pos = e
	lx.emit(k, start, e)
}

// blockComment makes /* … */ one TokBad: to the matching */ on any
// line, or to the end of the line when there is none.
func (lx *lexer) blockComment() {
	text, start := lx.text, lx.pos
	e := uint32(len(text))
	found := false
	for i := start + 2; i+1 < uint32(len(text)); i++ {
		if text[i] == '*' && text[i+1] == '/' {
			e, found = i+2, true
			break
		}
	}
	if !found {
		e = start
		for e < uint32(len(text)) && text[e] != '\n' {
			e++
		}
	}
	lx.pos = e
	lx.emitBad(start, e, "Mk has no block comments: use //")
}

// checkContent reports the first byte in [start, end) of a comment or literal
// that is rejected anywhere in a file: invalid UTF-8, U+FEFF, a lone CR, a
// form feed or a vertical tab. The token is kept. Only the first problem is
// reported, as for invalid UTF-8.
func (lx *lexer) checkContent(start, end uint32) {
	s := lx.text[start:end]
	for i := 0; i < len(s); {
		c := s[i]
		if c < 0x80 {
			if c == 0x0D || c == 0x0C || c == 0x0B { // CR, form feed, vertical tab
				at := start + uint32(i)
				msg := "form feed and vertical tab are not whitespace in Mk"
				if c == 0x0D {
					msg = "a carriage return must be followed by a newline"
				}
				lx.errAt(at, at+1, msg)
				return
			}
			i++
			continue
		}
		r, sz := utf8.DecodeRuneInString(s[i:])
		at := start + uint32(i)
		switch {
		case r == utf8.RuneError && sz <= 1:
			lx.errAt(at, at+1, "invalid UTF-8")
			return
		case r == 0xFEFF:
			lx.errAt(at, at+uint32(sz), "byte-order mark inside the file")
			return
		}
		i += sz
	}
}

// ident lexes a name, a keyword, _, or a prefixed literal r"…", b"…", b'…'.
// A non-ASCII scalar joins the identifier bytes around it into one TokBad.
func (lx *lexer) ident() {
	text, start := lx.text, lx.pos
	n := uint32(len(text))
	e := start + 1
	for e < n && isIdentByte(text[e]) {
		e++
	}
	if e < n && text[e] >= 0x80 && lx.joinsIdent(e) {
		lx.badIdent(start, e)
		return
	}
	word := text[start:e]
	if e < n && e == start+1 {
		switch {
		case word == "r" && text[e] == '"':
			lx.rawStr(start)
			return
		case word == "b" && text[e] == '"':
			lx.byteStr(start)
			return
		case word == "b" && text[e] == '\'':
			lx.byteChar(start)
			return
		}
	}
	lx.pos = e
	lx.emit(identKind(word), start, e)
}

// joinsIdent is true when the byte at i starts a valid non-ASCII scalar that
// is not a BOM, so it continues an identifier as part of one bad token.
func (lx *lexer) joinsIdent(i uint32) bool {
	r, sz := utf8.DecodeRuneInString(lx.text[i:])
	return !(r == utf8.RuneError && sz <= 1) && r != 0xFEFF
}

// badIdent extends a run of identifier bytes and valid non-ASCII scalars from
// start and makes it one TokBad.
func (lx *lexer) badIdent(start, e uint32) {
	text := lx.text
	n := uint32(len(text))
	for e < n {
		if isIdentByte(text[e]) {
			e++
			continue
		}
		if text[e] >= 0x80 && lx.joinsIdent(e) {
			_, sz := utf8.DecodeRuneInString(text[e:])
			e += uint32(sz)
			continue
		}
		break
	}
	lx.pos = e
	lx.emitBad(start, e, "identifiers are ASCII; non-ASCII text belongs in a comment or a string")
}

// nonASCII lexes a byte at or above 0x80 outside a comment or literal.
func (lx *lexer) nonASCII() {
	text, start := lx.text, lx.pos
	r, sz := utf8.DecodeRuneInString(text[start:])
	switch {
	case r == utf8.RuneError && sz <= 1:
		lx.pos = start + 1
		lx.emitBad(start, start+1, "invalid UTF-8")
	case r == 0xFEFF:
		lx.pos = start + uint32(sz)
		lx.emitBad(start, lx.pos, "byte-order mark inside the file")
	default:
		lx.badIdent(start, start+uint32(sz))
	}
}

// identKind is the kind of an identifier run: a keyword, _, or TokIdent.
// TestKeywordSwitchMatchesAST checks it against ast.Keyword.
func identKind(s string) ast.TokenKind {
	switch len(s) {
	case 1:
		if s == "_" {
			return ast.TokUnderscore
		}
	case 2:
		switch s {
		case "if":
			return ast.TokIf
		case "in":
			return ast.TokIn
		case "go":
			return ast.TokGo
		case "fn":
			return ast.TokFn
		case "is":
			return ast.TokIs
		}
	case 3:
		switch s {
		case "var":
			return ast.TokVar
		case "gen":
			return ast.TokGen
		case "for":
			return ast.TokFor
		case "try":
			return ast.TokTry
		case "mut":
			return ast.TokMut
		case "asm":
			return ast.TokAsm
		}
	case 4:
		switch s {
		case "enum":
			return ast.TokEnum
		case "impl":
			return ast.TokImpl
		case "type":
			return ast.TokType
		case "view":
			return ast.TokView
		case "test":
			return ast.TokTest
		case "else":
			return ast.TokElse
		case "fail":
			return ast.TokFail
		}
	case 5:
		switch s {
		case "error":
			return ast.TokError
		case "const":
			return ast.TokConst
		case "quote":
			return ast.TokQuote
		case "match":
			return ast.TokMatch
		case "break":
			return ast.TokBreak
		case "defer":
			return ast.TokDefer
		case "onerr":
			return ast.TokOnerr
		case "scope":
			return ast.TokScope
		}
	case 6:
		switch s {
		case "import":
			return ast.TokImport
		case "export":
			return ast.TokExport
		case "struct":
			return ast.TokStruct
		case "linear":
			return ast.TokLinear
		case "extern":
			return ast.TokExtern
		case "return":
			return ast.TokReturn
		case "unsafe":
			return ast.TokUnsafe
		case "become":
			return ast.TokBecome
		}
	case 7:
		if s == "ensures" {
			return ast.TokEnsures
		}
	case 8:
		switch s {
		case "continue":
			return ast.TokContinue
		case "requires":
			return ast.TokRequires
		}
	case 9:
		switch s {
		case "interface":
			return ast.TokInterface
		case "invariant":
			return ast.TokInvariant
		}
	}
	return ast.TokIdent
}

// punct lexes punctuation by maximal munch, the bracket stack and the
// Term before a '}', and the foreign bytes @ # ` \ \r \f \v.
func (lx *lexer) punct() {
	text, start := lx.text, lx.pos
	n := uint32(len(text))
	c := text[start]
	at := func(i uint32) byte {
		if start+i < n {
			return text[start+i]
		}
		return 0
	}
	c1, c2 := at(1), at(2)
	one := func(k ast.TokenKind) { lx.pos = start + 1; lx.emit(k, start, start+1) }
	two := func(k ast.TokenKind) { lx.pos = start + 2; lx.emit(k, start, start+2) }
	three := func(k ast.TokenKind) { lx.pos = start + 3; lx.emit(k, start, start+3) }
	switch c {
	case '(', '[', '{':
		lx.stack = append(lx.stack, frame{kind: c, at: start})
		one(map3(c, ast.TokLParen, ast.TokLBracket, ast.TokLBrace))
	case ')', ']', '}':
		lx.closer(c)
	case ',':
		one(ast.TokComma)
	case ';':
		one(ast.TokSemi)
	case '$':
		one(ast.TokDollar)
	case '?':
		one(ast.TokQuestion)
	case '~':
		one(ast.TokTilde)
	case '.':
		switch {
		case c1 == '.' && c2 == '.':
			three(ast.TokEllipsis)
		case c1 == '.':
			two(ast.TokDotDot)
		case isDigit(c1) && !lx.operandEnded():
			lx.leadingDotFloat(start)
		default:
			one(ast.TokDot)
		}
	case ':':
		switch c1 {
		case '=':
			two(ast.TokColonEq)
		case ':':
			two(ast.TokColonColon)
		default:
			one(ast.TokColon)
		}
	case '!':
		if c1 == '=' {
			two(ast.TokNotEq)
		} else {
			one(ast.TokBang)
		}
	case '=':
		switch c1 {
		case '=':
			two(ast.TokEqEq)
		case '>':
			two(ast.TokFatArrow)
		default:
			one(ast.TokAssign)
		}
	case '<':
		switch {
		case c1 == '<' && c2 == '=':
			three(ast.TokShlEq)
		case c1 == '<':
			two(ast.TokShl)
		case c1 == '=':
			two(ast.TokLtEq)
		default:
			one(ast.TokLt)
		}
	case '>':
		switch {
		case c1 == '>' && c2 == '=':
			three(ast.TokShrEq)
		case c1 == '>':
			two(ast.TokShr)
		case c1 == '=':
			two(ast.TokGtEq)
		default:
			one(ast.TokGt)
		}
	case '+', '-', '*':
		lx.arith(c, c1, c2)
	case '/':
		if c1 == '=' {
			two(ast.TokSlashEq)
		} else {
			one(ast.TokSlash)
		}
	case '%':
		if c1 == '=' {
			two(ast.TokPercentEq)
		} else {
			one(ast.TokPercent)
		}
	case '^':
		if c1 == '=' {
			two(ast.TokCaretEq)
		} else {
			one(ast.TokCaret)
		}
	case '&':
		switch c1 {
		case '&':
			two(ast.TokAmpAmp)
		case '=':
			two(ast.TokAmpEq)
		default:
			one(ast.TokAmp)
		}
	case '|':
		switch c1 {
		case '|':
			two(ast.TokPipePipe)
		case '=':
			two(ast.TokPipeEq)
		default:
			one(ast.TokPipe)
		}
	default:
		lx.foreign(c)
	}
}

func map3(c byte, a, b, d ast.TokenKind) ast.TokenKind {
	switch c {
	case '(', ')':
		return a
	case '[', ']':
		return b
	}
	return d
}

// arith lexes + - * and their wrapping, compound and arrow forms.
func (lx *lexer) arith(c, c1, c2 byte) {
	start := lx.pos
	var plain, wrap, wrapEq, eq ast.TokenKind
	switch c {
	case '+':
		plain, wrap, wrapEq, eq = ast.TokPlus, ast.TokPlusWrap, ast.TokPlusWrapEq, ast.TokPlusEq
	case '-':
		plain, wrap, wrapEq, eq = ast.TokMinus, ast.TokMinusWrap, ast.TokMinusWrapEq, ast.TokMinusEq
	default:
		plain, wrap, wrapEq, eq = ast.TokStar, ast.TokStarWrap, ast.TokStarWrapEq, ast.TokStarEq
	}
	k, l := plain, uint32(1)
	switch {
	case c1 == '%' && c2 == '=':
		k, l = wrapEq, 3
	case c1 == '%':
		k, l = wrap, 2
	case c1 == '=':
		k, l = eq, 2
	case c == '-' && c1 == '>':
		k, l = ast.TokArrow, 2
	}
	lx.pos = start + l
	lx.emit(k, start, start+l)
}

// closer handles ) ] } against the bracket stack.
func (lx *lexer) closer(c byte) {
	start := lx.pos
	open := byte('{')
	switch c {
	case ')':
		open = '('
	case ']':
		open = '['
	}
	kind := map3(c, ast.TokRParen, ast.TokRBracket, ast.TokRBrace)
	match := -1
	for i := len(lx.stack) - 1; i >= 0; i-- {
		if lx.stack[i].synthetic {
			break
		}
		if lx.stack[i].kind == open {
			match = i
			break
		}
	}
	top := len(lx.stack) - 1
	if match < 0 || match != top {
		lx.errAt(start, start+1, "unmatched `"+string(c)+"`")
	}
	if match >= 0 {
		lx.stack = lx.stack[:match]
		if c == '}' && lx.last != ast.TokLBrace && lx.last != ast.TokTerm {
			lx.term(start)
		}
	}
	lx.pos = start + 1
	lx.emit(kind, start, start+1)
}

// foreign lexes a byte no rule accepts into one TokBad, taking
// in a whole #[…] or @name(…) so the parser meets one token.
func (lx *lexer) foreign(c byte) {
	text, start := lx.text, lx.pos
	n := uint32(len(text))
	e := start + 1
	msg := ""
	switch c {
	case '\r':
		msg = "a carriage return must be followed by a newline"
	case '\f', '\v':
		msg = "form feed and vertical tab are not whitespace in Mk"
	case '#':
		msg = "Mk has no attributes"
		i := e
		if i < n && text[i] == '!' {
			i++
		}
		if i < n && text[i] == '[' {
			depth := 0
			for i < n && text[i] != '\n' {
				if text[i] == '[' {
					depth++
				} else if text[i] == ']' {
					depth--
					if depth == 0 {
						i++
						break
					}
				}
				i++
			}
			e = i
		}
	case '@':
		msg = "Mk has no attributes"
		for e < n && isIdentByte(text[e]) {
			e++
		}
		if e < n && text[e] == '(' {
			depth, i := 0, e
			for i < n && text[i] != '\n' {
				if text[i] == '(' {
					depth++
				} else if text[i] == ')' {
					depth--
					if depth == 0 {
						e = i + 1
						break
					}
				}
				i++
			}
		}
	case '`':
		msg = "Mk has no backquotes"
	case '\\':
		msg = "a backslash is only an escape inside a literal"
	default:
		msg = "unexpected byte"
	}
	lx.pos = e
	lx.emitBad(start, e, msg)
	if c == '\\' && e == start+1 {
		lx.lastBackslash = true
	}
}
