// Why: the lexer, parser, formatter and editor tools all speak in tokens;
// one token type that covers the text exactly, trivia included, lets any of
// them print the file back byte for byte.
//
// What: TokenKind (every terminal of doc/grammar.md, the keywords, the
// contextual words, the newline rule's classes and the habit tokens), Token,
// TokRange, and the predicates IsKeyword, IsTrivia, IsLiteral, IsAssignOp,
// IsHabit and CanEndStatement, with Keyword.
//
// How: a token is a kind, a start offset and a length into the file's
// normalized text. TokTerm is a zero-width statement terminator the newline
// rule emits. New kinds are appended at the end, so no older kind is
// renumbered.

package ast

import (
	"strconv"

	"mk/internal/source"
)

// TokenKind is the kind of one token. The zero value TokInvalid means "no token".
// The numbering is internal to one compiler build: it is never written to a
// file, a hash or output (token hashes write FixedText or the token's text).
type TokenKind uint8

const (
	// TokInvalid is the zero TokenKind: no token.
	TokInvalid TokenKind = iota

	// TokBad holds bytes no lexical rule accepts (reported E0101); it keeps the
	// token list lossless.
	TokBad
	// TokEOF is the zero-width token at the end of every file.
	TokEOF

	// Trivia: kept in the token list, skipped by the parser.

	// TokWhitespace is a run of ' ' and '\t'. A lone '\r' is a TokBad token.
	TokWhitespace
	// TokNewline is exactly one '\n'.
	TokNewline
	// TokComment is "//" to the end of the line, not including the '\n'.
	TokComment
	// TokDocComment is "///" to the end of the line; it documents the next item.
	TokDocComment

	// TokTerm is the zero-width statement terminator the newline rule emits
	// before the TokNewline that ends a statement, and before a '}' that ends
	// one. It is not trivia.
	TokTerm

	// Names and literals. Literal values are decoded by the checker.

	TokIdent      // a name; contextual words are Idents too
	TokUnderscore // _
	TokInt        // 1_000, 0xFF, 0o17, 0b101
	TokFloat      // 1.5, 1e9
	TokChar       // 'a'
	TokByte       // b'a'
	TokStr        // "a\n"
	TokRawStr     // r"raw"
	TokMultiStr   // """ ... """
	TokByteStr    // b"bytes"

	// Keywords, one kind each, in doc/reference.md's order.

	TokImport
	TokExport
	TokStruct
	TokEnum
	TokError // error
	TokInterface
	TokImpl
	TokType
	TokConst
	TokVar
	TokLinear
	TokView
	TokGen
	TokQuote
	TokTest
	TokExtern
	TokIf
	TokElse
	TokFor
	TokIn
	TokMatch
	TokReturn
	TokBreak
	TokContinue
	TokDefer
	TokOnerr
	TokFail
	TokTry
	TokScope
	TokGo
	TokUnsafe
	TokMut
	TokFn
	TokIs
	TokRequires
	TokEnsures
	TokInvariant

	// TokFields is the word fields where it starts a ForSource. The lexer
	// emits TokIdent; the parser rewrites the token's kind to TokFields when it
	// builds a FieldsSource, so the token list records the keyword use.
	TokFields

	// Brackets and punctuation.

	TokLParen   // (
	TokRParen   // )
	TokLBracket // [
	TokRBracket // ]
	TokLBrace   // {
	TokRBrace   // }
	TokComma    // ,
	TokColon    // :
	TokColonEq  // :=
	TokDot      // .
	TokDotDot   // ..
	TokEllipsis // ...
	TokSemi     // ; (only in [e; n]; elsewhere E1206)
	TokDollar   // $ (splices in quote)
	TokQuestion // ? (only in types; postfix is E1208)
	TokBang     // !
	TokTilde    // ~

	// Operators.

	TokAmp       // &
	TokPipe      // |
	TokCaret     // ^
	TokStar      // *
	TokSlash     // /
	TokPercent   // %
	TokPlus      // +
	TokMinus     // -
	TokPlusWrap  // +%
	TokMinusWrap // -%
	TokStarWrap  // *%
	TokShl       // <<
	TokShr       // >>
	TokAmpAmp    // &&
	TokPipePipe  // ||
	TokAssign    // =
	TokEqEq      // ==
	TokNotEq     // !=
	TokLt        // <
	TokLtEq      // <=
	TokGt        // >
	TokGtEq      // >=

	// Compound assignment.

	TokPlusEq      // +=
	TokMinusEq     // -=
	TokStarEq      // *=
	TokSlashEq     // /=
	TokPercentEq   // %=
	TokPlusWrapEq  // +%=
	TokMinusWrapEq // -%=
	TokStarWrapEq  // *%=
	TokShlEq       // <<=
	TokShrEq       // >>=
	TokAmpEq       // &=
	TokPipeEq      // |=
	TokCaretEq     // ^=

	// Habit tokens, lexed whole so E1211, E1214, E1213 and E1203 can name
	// them and the token list stays lossless.

	TokArrow      // ->
	TokFatArrow   // =>
	TokColonColon // ::
	TokLifetime   // 'a

	// TokAsm is the keyword asm. It is appended after the last kind, so no
	// older kind is renumbered: FirstKeyword..LastKeyword keep naming the 37
	// older keywords, and IsKeyword and Keyword know TokAsm as well.
	TokAsm

	// TokBecome is the keyword become, appended after TokAsm, so no older
	// kind is renumbered. doc/reference.md's keyword list has it, and
	// IsKeyword and Keyword know it with TokAsm: 39 keywords.
	TokBecome

	numTokenKinds
)

// NumTokenKinds is one past the last TokenKind, for tables indexed by kind.
const NumTokenKinds = int(numTokenKinds)

// FirstKeyword and LastKeyword bound the first 37 keywords (TokFields is
// not one). The 38th, TokAsm, and the 39th, TokBecome, lie past them:
// IsKeyword is the predicate for all 39.
const (
	FirstKeyword = TokImport
	LastKeyword  = TokInvariant
)

var tokenText = [numTokenKinds]string{
	TokInvalid: "", TokBad: "", TokEOF: "", TokWhitespace: "", TokNewline: "\n", TokComment: "", TokDocComment: "", TokTerm: "",
	TokIdent: "", TokUnderscore: "_", TokInt: "", TokFloat: "", TokChar: "", TokByte: "", TokStr: "", TokRawStr: "", TokMultiStr: "", TokByteStr: "",

	TokImport: "import", TokExport: "export", TokStruct: "struct", TokEnum: "enum", TokError: "error",
	TokInterface: "interface", TokImpl: "impl", TokType: "type", TokConst: "const", TokVar: "var",
	TokLinear: "linear", TokView: "view", TokGen: "gen", TokQuote: "quote", TokTest: "test", TokExtern: "extern",
	TokIf: "if", TokElse: "else", TokFor: "for", TokIn: "in", TokMatch: "match", TokReturn: "return",
	TokBreak: "break", TokContinue: "continue", TokDefer: "defer", TokOnerr: "onerr", TokFail: "fail",
	TokTry: "try", TokScope: "scope", TokGo: "go", TokUnsafe: "unsafe", TokMut: "mut", TokFn: "fn", TokIs: "is",
	TokRequires: "requires", TokEnsures: "ensures", TokInvariant: "invariant",
	TokFields: "fields",

	TokLParen: "(", TokRParen: ")", TokLBracket: "[", TokRBracket: "]", TokLBrace: "{", TokRBrace: "}",
	TokComma: ",", TokColon: ":", TokColonEq: ":=", TokDot: ".", TokDotDot: "..", TokEllipsis: "...",
	TokSemi: ";", TokDollar: "$", TokQuestion: "?", TokBang: "!", TokTilde: "~",

	TokAmp: "&", TokPipe: "|", TokCaret: "^", TokStar: "*", TokSlash: "/", TokPercent: "%", TokPlus: "+", TokMinus: "-",
	TokPlusWrap: "+%", TokMinusWrap: "-%", TokStarWrap: "*%", TokShl: "<<", TokShr: ">>", TokAmpAmp: "&&", TokPipePipe: "||",
	TokAssign: "=", TokEqEq: "==", TokNotEq: "!=", TokLt: "<", TokLtEq: "<=", TokGt: ">", TokGtEq: ">=",

	TokPlusEq: "+=", TokMinusEq: "-=", TokStarEq: "*=", TokSlashEq: "/=", TokPercentEq: "%=",
	TokPlusWrapEq: "+%=", TokMinusWrapEq: "-%=", TokStarWrapEq: "*%=", TokShlEq: "<<=", TokShrEq: ">>=",
	TokAmpEq: "&=", TokPipeEq: "|=", TokCaretEq: "^=",

	TokArrow: "->", TokFatArrow: "=>", TokColonColon: "::", TokLifetime: "",

	TokAsm: "asm",

	TokBecome: "become",
}

var tokenNames = [numTokenKinds]string{
	TokInvalid: "Invalid", TokBad: "Bad", TokEOF: "EOF", TokWhitespace: "Whitespace", TokNewline: "Newline",
	TokComment: "Comment", TokDocComment: "DocComment", TokTerm: "Term", TokIdent: "Ident", TokUnderscore: "Underscore",
	TokInt: "Int", TokFloat: "Float", TokChar: "Char", TokByte: "Byte", TokStr: "Str", TokRawStr: "RawStr",
	TokMultiStr: "MultiStr", TokByteStr: "ByteStr", TokLifetime: "Lifetime",
}

// String names the kind: the spelling for fixed tokens ("struct", ":="), else
// a name ("Ident", "Term"). TokFor debugging and E0101's expected list.
func (k TokenKind) String() string {
	if k >= numTokenKinds {
		return "TokenKind(" + strconv.Itoa(int(k)) + ")"
	}
	if n := tokenNames[k]; n != "" {
		return n
	}
	return strconv.Quote(tokenText[k])
}

// FixedText is the spelling of a keyword, punctuation, operator, assignment or
// fixed habit token, and "\n" for TokNewline; "" for names, literals, other
// trivia, TokBad, TokLifetime, TokTerm and TokEOF (the last two have empty text).
func (k TokenKind) FixedText() string {
	if k >= numTokenKinds {
		return ""
	}
	return tokenText[k]
}

// IsTrivia reports whether the parser skips the kind: TokWhitespace, TokNewline,
// TokComment, TokDocComment. TokTerm is not trivia.
func (k TokenKind) IsTrivia() bool { return k >= TokWhitespace && k <= TokDocComment }

// IsKeyword reports whether k is one of the 39 keywords:
// FirstKeyword..LastKeyword, TokAsm and TokBecome.
func (k TokenKind) IsKeyword() bool {
	return k >= FirstKeyword && k <= LastKeyword || k == TokAsm || k == TokBecome
}

// IsLiteral reports whether k is a number, character or string literal.
func (k TokenKind) IsLiteral() bool { return k >= TokInt && k <= TokByteStr }

// IsAssignOp reports whether k is "=" or a compound assignment (the
// grammar's AssignOp).
func (k TokenKind) IsAssignOp() bool { return k == TokAssign || (k >= TokPlusEq && k <= TokCaretEq) }

// IsHabit reports whether k is one of the habit tokens -> => :: 'a.
func (k TokenKind) IsHabit() bool { return k >= TokArrow && k <= TokLifetime }

// CanEndStatement reports whether a newline after a token of this kind ends
// the statement: a name, a literal, ')', ']', '}', return,
// break, continue. Nothing else.
func (k TokenKind) CanEndStatement() bool {
	switch k {
	case TokIdent, TokRParen, TokRBracket, TokRBrace, TokReturn, TokBreak, TokContinue:
		return true
	}
	return k.IsLiteral()
}

// keywords maps each keyword's spelling to its kind.
var keywords = func() map[string]TokenKind {
	m := make(map[string]TokenKind, int(LastKeyword-FirstKeyword)+2)
	for k := FirstKeyword; k <= LastKeyword; k++ {
		m[tokenText[k]] = k
	}
	m[tokenText[TokAsm]] = TokAsm
	m[tokenText[TokBecome]] = TokBecome
	return m
}()

// Keyword returns the keyword kind spelled s, and false for every other word,
// including the contextual words from into ends fuel packed self Self result
// and the word fields.
func Keyword(s string) (TokenKind, bool) {
	k, ok := keywords[s]
	return k, ok
}

// Token is one token of a file: its kind and its bytes [Start, Start+Len) of
// the file's normalized text. The token list of a file covers every byte
// exactly once, trivia included, and ends with TokEOF at the end of the text, so
// printing every token's text gives the file back. TokTerm and TokEOF
// have Len 0.
type Token struct {
	Kind  TokenKind
	Start uint32
	Len   uint32
}

// End is the offset just past the token.
func (t Token) End() uint32 { return t.Start + t.Len }

// Span is the token's span in file.
func (t Token) Span(file source.FileID) source.Span {
	return source.Span{File: file, Start: t.Start, End: t.Start + t.Len}
}

// Text is the token's text in the file's normalized text.
func (t Token) Text(text string) string { return text[t.Start : t.Start+t.Len] }

// TokRange is the half-open range [First, End) of indices into a file's token
// list. The zero TokRange is empty.
type TokRange struct {
	First uint32
	End   uint32
}

// Len is the number of tokens in the range.
func (r TokRange) Len() uint32 { return r.End - r.First }
