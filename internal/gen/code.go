// Why: a generator builds Mk code as token lists, and quotes reach it as
// templates that lower encoded; both sides must agree on one token form and
// one spelling of names and literals.
//
// What: tok (one token of a Code or Block) with TokenOrigin and OriginKind,
// decodeTemplate for the quote template (version 1, magic "mkq1"), and the
// spellers isIdent, strLit, charLit and intToks.
//
// How: a template is a list of tokens and holes; a hole names a splice index
// that the host fills by the operand's static type. Each name token records
// its origin, so resolve later knows whether to look it up in the generator's
// imports, the output module, or at a pinned item.

package gen

import (
	"encoding/binary"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"mk/internal/ast"
	"mk/internal/types"
)

// TokenOrigin says how resolve must look up the name token at Offset in the
// generated file.
type TokenOrigin struct {
	Offset uint32
	Kind   OriginKind
	// Module is the generator's module for OriginLiteral, the bound module for
	// OriginPinnedModule.
	Module types.ModuleID
	// Item is the pinned item for OriginPinnedItem.
	Item types.ItemID
}

// OriginKind tags a TokenOrigin.
type OriginKind uint8

const (
	// OriginLiteral: a name written literally in a quote. After the emitted
	// code's own lexical bindings it resolves in Module's imports, then the
	// prelude, then the universe, never in Module's own items nor in the
	// output module's.
	OriginLiteral OriginKind = iota + 1
	// OriginSpliced: an identifier from a spliced str, Gen.Ident or
	// Gen.Fresh: it resolves in the output module like handwritten code.
	OriginSpliced
	// OriginPinnedItem, OriginPinnedModule: a token of a spliced Type, a
	// resolved reference that needs no import.
	OriginPinnedItem
	OriginPinnedModule
)

// String names the origin kind.
func (k OriginKind) String() string {
	switch k {
	case OriginLiteral:
		return "literal"
	case OriginSpliced:
		return "spliced"
	case OriginPinnedItem:
		return "pinned-item"
	case OriginPinnedModule:
		return "pinned-module"
	}
	return "origin?"
}

// tok is one token of a Code or Block. Newlines are kept as
// TokNewline tokens; trivia is not. origin is set for TokIdent
// only; module and item qualify it as TokenOrigin does. tight marks the
// '|' of a spliced error union, printed without spaces as Mk
// writes types (A|B!T), where an expression's '|' takes them.
type tok struct {
	kind   ast.TokenKind
	text   string
	origin OriginKind
	tight  bool
	module types.ModuleID
	item   types.ItemID
}

// spell is the token's text: the stored text for names and literals, the
// fixed text otherwise.
func (t tok) spell() string {
	if t.text != "" {
		return t.text
	}
	return t.kind.FixedText()
}

// quoteMagic is the template's magic and version.
const quoteMagic = "mkq1"

// tmplTok is one decoded template entry: a token, or a hole (kind
// TokDollar) naming the splice index.
type tmplTok struct {
	kind ast.TokenKind
	text string
	hole int
}

// hasText reports whether a template entry of kind k carries text: names and
// literals, whose spelling varies (lower's quoteText).
func hasText(k ast.TokenKind) bool {
	return k == ast.TokIdent || k.IsLiteral()
}

// decodeTemplate reads template bytes of version 1. A malformed
// template is a compiler gap between lower and gen, so the error is an ICE
// at the caller.
func decodeTemplate(b []byte) ([]tmplTok, error) {
	if !strings.HasPrefix(string(b), quoteMagic) {
		return nil, fmt.Errorf("quote template: bad magic")
	}
	b = b[len(quoteMagic):]
	next := func() (uint64, error) {
		v, n := binary.Uvarint(b)
		if n <= 0 {
			return 0, fmt.Errorf("quote template: truncated uvarint")
		}
		b = b[n:]
		return v, nil
	}
	n, err := next()
	if err != nil {
		return nil, err
	}
	if n > uint64(len(b)) {
		return nil, fmt.Errorf("quote template: %d entries in %d bytes", n, len(b))
	}
	out := make([]tmplTok, 0, n)
	for range n {
		k, err := next()
		if err != nil {
			return nil, err
		}
		kind := ast.TokenKind(k)
		t := tmplTok{kind: kind, hole: -1}
		switch {
		case kind == ast.TokDollar:
			h, err := next()
			if err != nil {
				return nil, err
			}
			t.hole = int(h)
		case hasText(kind):
			l, err := next()
			if err != nil {
				return nil, err
			}
			if l > uint64(len(b)) {
				return nil, fmt.Errorf("quote template: text of %d bytes past the end", l)
			}
			t.text, b = string(b[:l]), b[l:]
		case kind == ast.TokNewline:
		case kind.FixedText() == "":
			return nil, fmt.Errorf("quote template: token kind %s has no text", kind)
		}
		out = append(out, t)
	}
	if len(b) != 0 {
		return nil, fmt.Errorf("quote template: %d bytes after the last entry", len(b))
	}
	return out, nil
}

// isIdent reports whether s is one Mk identifier token that is
// not a keyword: [A-Za-z_][A-Za-z0-9_]*, and not "_" alone (TokUnderscore).
func isIdent(s string) bool {
	if s == "" || s == "_" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		letter := c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
		if !letter && (i == 0 || c < '0' || c > '9') {
			return false
		}
	}
	_, kw := ast.Keyword(s)
	return !kw
}

// identError is the message of a str that is not an identifier: a
// bug in the generator, E0901 with the message as the panic fact.
func identError(what, s string) string {
	if _, kw := ast.Keyword(s); kw {
		return fmt.Sprintf("%s: %q is a keyword, not an identifier", what, s)
	}
	return fmt.Sprintf("%s: %q is not an identifier", what, s)
}

// strLit spells s as one str literal token, escaped as the Mk lexer reads
// literals: \\ \" \n \r \t \0 and \u{X} for other control characters and
// invalid bytes' replacement. Valid UTF-8 text is kept as written.
func strLit(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		escapeRune(&b, r, '"')
	}
	b.WriteByte('"')
	return b.String()
}

// charLit spells r as one char literal token.
func charLit(r rune) string {
	var b strings.Builder
	b.WriteByte('\'')
	escapeRune(&b, r, '\'')
	b.WriteByte('\'')
	return b.String()
}

func escapeRune(b *strings.Builder, r rune, quote rune) {
	switch r {
	case '\\':
		b.WriteString(`\\`)
	case '\n':
		b.WriteString(`\n`)
	case '\r':
		b.WriteString(`\r`)
	case '\t':
		b.WriteString(`\t`)
	case 0:
		b.WriteString(`\0`)
	default:
		switch {
		case r == quote:
			b.WriteByte('\\')
			b.WriteRune(r)
		case r < 0x20 || r == 0x7f || !utf8.ValidRune(r):
			b.WriteString(`\u{` + strconv.FormatUint(uint64(r), 16) + `}`)
		default:
			b.WriteRune(r)
		}
	}
}

// intToks spells an integer as a decimal literal, a negative value as `-`
// then the literal, as the splice table spells an integer.
func intToks(v int64, signed bool, u uint64) []tok {
	if !signed {
		return []tok{{kind: ast.TokInt, text: strconv.FormatUint(u, 10)}}
	}
	if v < 0 {
		mag := uint64(-(v + 1)) + 1
		return []tok{{kind: ast.TokMinus}, {kind: ast.TokInt, text: strconv.FormatUint(mag, 10)}}
	}
	return []tok{{kind: ast.TokInt, text: strconv.FormatInt(v, 10)}}
}

// identTok is a spliced identifier (OriginSpliced).
func identTok(s string) tok { return tok{kind: ast.TokIdent, text: s, origin: OriginSpliced} }
