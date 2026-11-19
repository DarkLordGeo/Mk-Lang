// Why: the lexer decides where statements end and which bytes are legal, so
// every later phase depends on its tokens.
// What: tests of the newline rule, the bracket stack, bad bytes, comments,
// keywords, numbers, characters, strings and multi-line literals.
// How: lexes short texts and compares the token kinds (kinds, sigKinds) and
// the diagnostics (lexDiags); each test is named after the rule it pins.

package syntax

import (
	"strings"
	"testing"

	"mk/internal/ast"
	"mk/internal/diag"
)

// kinds renders the token kinds of text, trivia included, space separated.
func kinds(text string) string {
	toks, _ := Lex(1, text)
	var out []string
	for _, t := range toks {
		out = append(out, t.Kind.String())
	}
	return strings.Join(out, " ")
}

// sigKinds renders only the significant tokens (Term and Bad included).
func sigKinds(text string) string {
	toks, _ := Lex(1, text)
	var out []string
	for _, t := range toks {
		if !t.Kind.IsTrivia() && t.Kind != ast.TokEOF {
			out = append(out, t.Kind.String())
		}
	}
	return strings.Join(out, " ")
}

func lexDiags(text string) []*diag.Diagnostic {
	_, ds := Lex(1, text)
	return ds
}

func TestNewlineRule(t *testing.T) {
	t.Parallel()
	cases := []struct{ in, want string }{
		{"x := 1 // n\n", `Ident Whitespace ":=" Whitespace Int Whitespace Comment Term Newline`},
		{"a +\nb\n", `Ident "+" Ident Term`},
		{"x.\nF()\n", `Ident "." Ident "(" ")" Term`},
		{"f(\na,\nb)\n", `Ident "(" Ident "," Ident ")" Term`},
		{"{\nx\n}\n", `"{" Ident Term "}" Term`},
		{"\n\n// c\n", ``},
	}
	for _, c := range cases {
		got := sigKinds(c.in)
		if c.in == cases[0].in {
			got = kinds(c.in)
			got = strings.TrimSuffix(got, " EOF")
		}
		if got != c.want {
			t.Errorf("%q: got %s, want %s", c.in, got, c.want)
		}
	}
}

func TestTokensThatEndAStatement(t *testing.T) {
	t.Parallel()
	for _, s := range []string{"x", "1", "1.5", "'a'", "\"s\"", ")", "]", "}", "return", "break", "continue"} {
		text := s + "\n"
		if s == ")" || s == "]" || s == "}" {
			text = map[string]string{")": "(x)\n", "]": "[x]\n", "}": "{ }\n"}[s]
		}
		if !strings.HasSuffix(sigKinds(text), "Term") {
			t.Errorf("%q: no Term: %s", text, sigKinds(text))
		}
	}
	for _, s := range []string{"x +", "x :=", "x.", "x,", "_", "&", "fn"} {
		text := s + "\ny\n"
		if strings.Count(sigKinds(text), "Term") != 1 {
			t.Errorf("%q: want one Term (after y): %s", text, sigKinds(text))
		}
	}
}

func TestBracketStack(t *testing.T) {
	t.Parallel()
	// Newlines inside ( ) and [ ] are ignored; { restores them.
	if got := sigKinds("f(a,\n fn() {\n x\n })\n"); got != `Ident "(" Ident "," "fn" "(" ")" "{" Ident Term "}" ")" Term` {
		t.Errorf("got %s", got)
	}
	ds := lexDiags("f(a]\n")
	if len(ds) != 2 || ds[0].Message != "unmatched `]`" || ds[1].Message != "unclosed `(`" {
		t.Errorf("unmatched: %v", messages(ds))
	}
	ds = lexDiags("F() { x := (1 + }\n")
	if len(ds) != 1 || ds[0].Message != "unmatched `}`" {
		t.Errorf("pop down: %v", messages(ds))
	}
}

func messages(ds []*diag.Diagnostic) []string {
	var out []string
	for _, d := range ds {
		out = append(out, d.Code.String()+" "+d.Message)
	}
	return out
}

func TestTermBeforeBrace(t *testing.T) {
	t.Parallel()
	if got := sigKinds("{ return x }"); got != `"{" "return" Ident Term "}" Term` {
		t.Errorf("got %s", got)
	}
	if got := sigKinds("{ }"); got != `"{" "}" Term` {
		t.Errorf("got %s", got)
	}
	if got := sigKinds("{ x := 1\n}"); got != `"{" Ident ":=" Int Term "}" Term` {
		t.Errorf("got %s", got)
	}
}

func TestTermAtEOF(t *testing.T) {
	t.Parallel()
	if got := sigKinds("x"); got != "Ident Term" {
		t.Errorf("got %s", got)
	}
	if got := sigKinds(""); got != "" {
		t.Errorf("empty: %s", got)
	}
	if got := sigKinds("x +"); got != `Ident "+" Term` {
		t.Errorf("got %s", got)
	}
}

func TestIsLineTerm(t *testing.T) {
	t.Parallel()
	toks, _ := Lex(1, "{ x }\ny\nz")
	var got []bool
	for i, tk := range toks {
		if tk.Kind == ast.TokTerm {
			got = append(got, IsLineTerm(toks, i))
		}
	}
	// before '}': synthetic; after '}' at the newline: real; after y: real; EOF: synthetic.
	want := []bool{false, true, true, false}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Term %d: got %v, want %v", i, got[i], want[i])
		}
	}
}

func TestBadTokenEndsLine(t *testing.T) {
	t.Parallel()
	if got := sigKinds("x := @\ny := 2\n"); got != `Ident ":=" Bad Term Ident ":=" Int Term` {
		t.Errorf("@: %s", got)
	}
	if got := sigKinds("x := a \\\n+ b\n"); got != `Ident ":=" Ident Bad "+" Ident Term` {
		t.Errorf("backslash: %s", got)
	}
	for _, s := range []string{"#[derive(Eq)]", "#![x]", "@inline(always)", "café"} {
		if got := sigKinds(s); got != "Bad Term" {
			t.Errorf("%q: %s", s, got)
		}
	}
}

func TestInvalidUTF8(t *testing.T) {
	t.Parallel()
	ds := lexDiags("x := \xff\n")
	if len(ds) != 1 || ds[0].Message != "invalid UTF-8" || ds[0].At.Start != 5 {
		t.Errorf("got %v", messages(ds))
	}
	ds = lexDiags("// a \xff b\n")
	if len(ds) != 1 || ds[0].Message != "invalid UTF-8" || ds[0].At.Start != 5 {
		t.Errorf("comment: %v", messages(ds))
	}
}

func TestNonASCIIOutsideLiterals(t *testing.T) {
	t.Parallel()
	if ds := lexDiags("x := \"é\" // ü\n"); len(ds) != 0 {
		t.Errorf("allowed: %v", messages(ds))
	}
	if ds := lexDiags("é := 1\n"); len(ds) != 1 {
		t.Errorf("ident: %v", messages(ds))
	}
	if ds := lexDiags("x := b\"é\"\n"); len(ds) != 1 {
		t.Errorf("byte string: %v", messages(ds))
	}
}

func TestLoneCR(t *testing.T) {
	t.Parallel()
	ds := lexDiags("x\ry\n")
	if len(ds) != 1 || !strings.Contains(ds[0].Message, "carriage return") {
		t.Errorf("got %v", messages(ds))
	}
	for _, s := range []string{"a\fb", "a\vb"} {
		if ds := lexDiags(s); len(ds) != 1 {
			t.Errorf("%q: %v", s, messages(ds))
		}
	}
}

func TestInnerBOM(t *testing.T) {
	t.Parallel()
	ds := lexDiags("x\xEF\xBB\xBF\n")
	if len(ds) != 1 || ds[0].Message != "byte-order mark inside the file" {
		t.Errorf("got %v", messages(ds))
	}
}

func TestBlockCommentIsBad(t *testing.T) {
	t.Parallel()
	ds := lexDiags("/* a\nb */ x\n")
	if len(ds) != 1 || ds[0].Message != "Mk has no block comments: use //" {
		t.Errorf("got %v", messages(ds))
	}
	if got := sigKinds("/* a\nb */ x\n"); got != "Bad Ident Term" {
		t.Errorf("got %s", got)
	}
}

func TestDocComments(t *testing.T) {
	t.Parallel()
	cases := map[string]ast.TokenKind{"/// d": ast.TokDocComment, "///": ast.TokDocComment, "// c": ast.TokComment,
		"//// c": ast.TokComment, "//": ast.TokComment}
	for in, want := range cases {
		toks, _ := Lex(1, in)
		if toks[0].Kind != want {
			t.Errorf("%q: got %v, want %v", in, toks[0].Kind, want)
		}
	}
}

func TestKeywordSwitchMatchesAST(t *testing.T) {
	t.Parallel()
	for k := ast.FirstKeyword; k <= ast.LastKeyword; k++ {
		s := k.FixedText()
		if got := identKind(s); got != k {
			t.Errorf("%q: got %v, want %v", s, got, k)
		}
		if w, ok := ast.Keyword(s); !ok || w != k {
			t.Errorf("ast.Keyword(%q) disagrees", s)
		}
	}
	for _, k := range []ast.TokenKind{ast.TokAsm, ast.TokBecome} {
		if got := identKind(k.FixedText()); got != k {
			t.Errorf("%q: got %v, want %v", k.FixedText(), got, k)
		}
	}
	for _, s := range []string{"from", "into", "ends", "fuel", "packed", "self", "Self", "result", "fields", "x", "Import", "become_"} {
		if got := identKind(s); got != ast.TokIdent {
			t.Errorf("%q: got %v, want Ident", s, got)
		}
	}
}

func TestContextualWordsAreIdents(t *testing.T) {
	t.Parallel()
	if got := sigKinds("from into ends fuel packed self Self result fields"); got != strings.Repeat("Ident ", 9)+"Term" {
		t.Errorf("got %s", got)
	}
}

func TestUnderscore(t *testing.T) {
	t.Parallel()
	if got := sigKinds("_ _x x_"); got != "Underscore Ident Ident Term" {
		t.Errorf("got %s", got)
	}
	// _ cannot end a statement.
	if got := sigKinds("_\n"); got != "Underscore Term" {
		t.Errorf("got %s", got)
	}
}

func TestLiteralPrefixes(t *testing.T) {
	t.Parallel()
	if got := sigKinds(`r"a" b"a" b'a' r "a" b x`); got != "RawStr ByteStr Byte Ident Str Ident Ident Term" {
		t.Errorf("got %s", got)
	}
}

func TestMaximalMunch(t *testing.T) {
	t.Parallel()
	in := "... +%= -%= *%= <<= >>= := .. +% -% *% << >> && || == != <= >= += -= *= /= %= &= |= ^= -> => ::"
	want := `"..." "+%=" "-%=" "*%=" "<<=" ">>=" ":=" ".." "+%" "-%" "*%" "<<" ">>" "&&" "||" "==" "!=" "<=" ">=" "+=" "-=" "*=" "/=" "%=" "&=" "|=" "^=" "->" "=>" "::" Term`
	if got := sigKinds(in); got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
	if got := sigKinds("..= x++ **"); got != `".." "=" Ident "+" "+" "*" "*" Term` {
		t.Errorf("got %s", got)
	}
}

func TestDollar(t *testing.T) {
	t.Parallel()
	if got := sigKinds("$x"); got != `"$" Ident Term` {
		t.Errorf("got %s", got)
	}
}

func TestDecimalInts(t *testing.T) {
	t.Parallel()
	ok := []string{"0", "1", "1_000", "18446744073709551615"}
	bad := map[string]string{
		"007":                  leadingZeroMsg,
		"1__0":                 "`_` goes only between digits",
		"1_":                   "`_` goes only between digits",
		"18446744073709551616": "integer literal larger than 2^64-1",
	}
	for _, s := range ok {
		if ds := lexDiags(s); len(ds) != 0 {
			t.Errorf("%q: %v", s, messages(ds))
		}
	}
	for s, msg := range bad {
		ds := lexDiags(s)
		if len(ds) != 1 || ds[0].Message != msg {
			t.Errorf("%q: got %v, want %q", s, messages(ds), msg)
		}
	}
}

func TestPrefixedInts(t *testing.T) {
	t.Parallel()
	for _, s := range []string{"0xFF", "0x_ff", "0o17", "0b101", "0b1_0"} {
		if ds := lexDiags(s); len(ds) != 0 {
			t.Errorf("%q: %v", s, messages(ds))
		}
	}
	for s, msg := range map[string]string{
		"0XFF": "number prefixes are lowercase: `0x`",
		"0b12": "digit `2` is not valid in base 2",
		"0x":   "a number prefix needs digits",
		"0o8":  "digit `8` is not valid in base 8",
	} {
		ds := lexDiags(s)
		if len(ds) != 1 || ds[0].Message != msg {
			t.Errorf("%q: got %v, want %q", s, messages(ds), msg)
		}
	}
}

func TestFloats(t *testing.T) {
	t.Parallel()
	for _, s := range []string{"1.5", "1e9", "1.5e-3", "1_0.2_5", "0.5"} {
		toks, ds := Lex(1, s)
		if len(ds) != 0 || toks[0].Kind != ast.TokFloat {
			t.Errorf("%q: %v %v", s, toks[0].Kind, messages(ds))
		}
	}
	for s, msg := range map[string]string{"1e": "exponent needs digits", "00.5": leadingZeroMsg, "1e999": "float literal out of range"} {
		ds := lexDiags(s)
		if len(ds) != 1 || ds[0].Message != msg {
			t.Errorf("%q: got %v, want %q", s, messages(ds), msg)
		}
	}
}

func TestDotAfterNumber(t *testing.T) {
	t.Parallel()
	if got := sigKinds("0..n 1.Len() x.0"); got != `Int ".." Ident Int "." Ident "(" ")" Ident "." Int Term` {
		t.Errorf("got %s", got)
	}
}

func TestLiteralSuffixes(t *testing.T) {
	t.Parallel()
	for _, s := range []string{"1u8", "1.0f32", "0xFFu8", "1_u32", "0x1p3"} {
		toks, ds := Lex(1, s)
		if len(ds) != 1 || ds[0].Message != suffixMsg || toks[0].Len != uint32(len(s)) {
			t.Errorf("%q: %v (first token %d bytes)", s, messages(ds), toks[0].Len)
		}
	}
}

func TestChars(t *testing.T) {
	t.Parallel()
	for _, s := range []string{`'a'`, `'é'`, `'\n'`, `'\u{1F600}'`, `'\''`, `'_'`} {
		toks, ds := Lex(1, s)
		if len(ds) != 0 || toks[0].Kind != ast.TokChar {
			t.Errorf("%q: %v %v", s, toks[0].Kind, messages(ds))
		}
	}
	for s, msg := range map[string]string{
		`''`:     "empty character literal",
		`'ab'`:   "a char holds one Unicode scalar",
		"'1\n":   "unterminated character literal",
		`'\x80'`: "`\\x` is at most 7F outside byte literals; use `\\u{…}`",
	} {
		ds := lexDiags(s)
		if len(ds) != 1 || ds[0].Message != msg {
			t.Errorf("%q: got %v, want %q", s, messages(ds), msg)
		}
	}
}

func TestBytes(t *testing.T) {
	t.Parallel()
	for _, s := range []string{`b'a'`, `b'\xff'`, `b'\n'`} {
		if ds := lexDiags(s); len(ds) != 0 {
			t.Errorf("%q: %v", s, messages(ds))
		}
	}
	for _, s := range []string{`b'é'`, `b'\u{41}'`} {
		if ds := lexDiags(s); len(ds) != 1 {
			t.Errorf("%q: %v", s, messages(ds))
		}
	}
}

func TestEscapes(t *testing.T) {
	t.Parallel()
	for _, s := range []string{`"\n\r\t\0\\\'\""`, `"\x41\u{10FFFF}"`} {
		if ds := lexDiags(s); len(ds) != 0 {
			t.Errorf("%q: %v", s, messages(ds))
		}
	}
	for _, s := range []string{`"\a"`, `"\U00000041"`, "\"\\u0041\"", `"\u{D800}"`, `"\u{1234567}"`, `"\x4"`} {
		if ds := lexDiags(s); len(ds) != 1 {
			t.Errorf("%q: %v", s, messages(ds))
		}
	}
}

func TestLifetimes(t *testing.T) {
	t.Parallel()
	if got := sigKinds("&'a T 'static"); got != `"&" Lifetime Ident Lifetime Term` {
		t.Errorf("got %s", got)
	}
}

func TestStrings(t *testing.T) {
	t.Parallel()
	ds := lexDiags("\"abc\nx\n")
	if len(ds) != 1 || ds[0].Message != "unterminated string" {
		t.Errorf("got %v", messages(ds))
	}
}

func TestRawStrings(t *testing.T) {
	t.Parallel()
	if ds := lexDiags(`r"a\b"`); len(ds) != 0 {
		t.Errorf("%v", messages(ds))
	}
	if ds := lexDiags("r\"a\nb\""); len(ds) < 1 || !strings.HasPrefix(ds[0].Message, "a raw string is one line") {
		t.Errorf("%v", messages(ds))
	}
}

func TestByteStrings(t *testing.T) {
	t.Parallel()
	if ds := lexDiags(`b"a\xff\t"`); len(ds) != 0 {
		t.Errorf("%v", messages(ds))
	}
	if ds := lexDiags(`b"\u{41}"`); len(ds) != 1 {
		t.Errorf("%v", messages(ds))
	}
}

func TestMultiLine(t *testing.T) {
	t.Parallel()
	good := "f(\"\"\"\n    a\n\n      b\n    \"\"\")\n"
	toks, ds := Lex(1, good)
	if len(ds) != 0 {
		t.Fatalf("%v", messages(ds))
	}
	var lit ast.Token
	for _, tk := range toks {
		if tk.Kind == ast.TokMultiStr {
			lit = tk
		}
	}
	v, ok := StringValue(ast.TokMultiStr, lit.Text(good))
	if !ok || string(v) != "a\n\n  b\n" {
		t.Errorf("value %q %v", v, ok)
	}
	if v, ok := StringValue(ast.TokMultiStr, "\"\"\"\n\"\"\""); !ok || string(v) != "" {
		t.Errorf("empty: %q %v", v, ok)
	}
	for in, msg := range map[string]string{
		"\"\"\" x\n\"\"\"":            "text after the opening `\"\"\"`",
		"\"\"\"\n a \"\"\" b\n\"\"\"": "a closing `\"\"\"` must start its line (write `\\\"\"\"` in content)",
		"\"\"\"\nab\n  \"\"\"":        "line does not start with the closing line's indentation",
		"\"\"\"\nab\n":                "unterminated multi-line string",
	} {
		ds := lexDiags(in)
		if len(ds) != 1 || ds[0].Message != msg {
			t.Errorf("%q: got %v, want %q", in, messages(ds), msg)
		}
	}
	if ds := lexDiags("\"\"\"\n a \\\"\"\" b\n \"\"\""); len(ds) != 0 {
		t.Errorf("escaped quotes: %v", messages(ds))
	}
}

func TestNewlinesInMultiLine(t *testing.T) {
	t.Parallel()
	if got := sigKinds("x := \"\"\"\n a\n \"\"\"\ny := 1\n"); got != `Ident ":=" MultiStr Term Ident ":=" Int Term` {
		t.Errorf("got %s", got)
	}
}
