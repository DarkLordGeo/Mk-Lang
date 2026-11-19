// Why: gen prints Mk source from a generator's run; its template, layout and
// trace comments must be stable and its output must parse.
// What: tests of the quote template shared with lower, derive.Eq end to end
// over hand-built MIR, the printer's layout rules and trace comments,
// splices, E0905 and determinism.
// How: a hand-built world (deriveEq builds the generator as lower would) and
// goldens in testdata, compared by golden.

package gen

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mk/internal/ast"
	"mk/internal/mir"
	"mk/internal/syntax"
	"mk/internal/types"
)

// spellTemplate is a decoded template as "|"-separated spellings.
func spellTemplate(ts []tmplTok) string {
	var parts []string
	for _, t := range ts {
		switch {
		case t.hole >= 0:
			parts = append(parts, "$"+itoa(t.hole))
		case t.kind == ast.TokNewline:
			parts = append(parts, `\n`)
		case t.text != "":
			parts = append(parts, t.text)
		default:
			parts = append(parts, t.kind.FixedText())
		}
	}
	return strings.Join(parts, "|")
}

// TestQuoteTemplateGolden decodes lower's golden template bytes (the
// file lower's TestQuoteTemplateGolden writes from its quoteSrc).
func TestQuoteTemplateGolden(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "lower", "testdata", "quote_template.golden"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := hex.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	ts, err := decodeTemplate(b)
	if err != nil {
		t.Fatal(err)
	}
	got := spellTemplate(ts)
	want := `\n|impl|Eq|for|$0|{|\n|Eq|(|&|self|,|other|:|&|$1|)|:|bool|{|\n|return|x|==|1_0|&&|"s"|!=|r"r"|\n|}|\n|}|\n`
	if got != want {
		t.Fatalf("golden template decodes to\n%s\nwant\n%s", got, want)
	}
	// The test encoder writes the same bytes for the same text.
	enc := encodeTemplate(t, "\nimpl Eq for $0 {\nEq(&self, other: &$1): bool {\nreturn x == 1_0 && \"s\" != r\"r\"\n}\n}\n")
	if hex.EncodeToString(enc) != strings.TrimSpace(string(raw)) {
		t.Fatalf("test encoder differs from lower's golden:\n%x\n%s", enc, raw)
	}
	for _, bad := range [][]byte{[]byte("mkq2\x00"), []byte("mkq1"), []byte("mkq1\x01\x0a"), append(b, 0)} {
		if _, err := decodeTemplate(bad); err == nil {
			t.Errorf("decodeTemplate(%q) gave no error", bad)
		}
	}
}

// deriveEq builds the derive.Eq generator as lower would: locals 1
// (g: mut Gen) and 2 (t: Type).
func (w *world) deriveEq() types.ItemID {
	g := w.generator("derive", "Eq", w.typeT())
	x := w.body(g)
	tr := x.ref(2, false)
	gr := x.refPlace(x.deref(1), w.named(w.known.Gen), false)
	x.flush(0)
	body, b1 := x.api(0, "Gen:Block", cp(gr))
	fs, b2 := x.api(b1, "Type:Fields", cp(tr))
	n, i, c := x.local(types.Int), x.local(types.Int), x.local(types.Bool)
	x.assign(b2, lp(n), mir.Rvalue{Kind: mir.RvLen, Place: lp(fs)})
	x.assign(b2, lp(i), use(mir.Imm(types.Int, 0)))
	head, loop, exit := x.block(), x.block(), x.block()
	x.gotoB(b2, head)
	x.assign(head, lp(c), mir.Rvalue{Kind: mir.RvBinary, Bin: mir.Lt, A: cp(i), B: cp(n)})
	x.branch(head, cp(c), loop, exit)
	ft := w.named(w.known.Field)
	f := x.local(ft)
	x.assign(loop, lp(f), use(mir.Copy(x.b.Place(fs, mir.Projection{Kind: mir.ProjIndex, A: uint32(i), Type: ft}))))
	fr := x.ref(f, false)
	x.flush(loop)
	name, b5 := x.api(loop, "Field:Name", cp(fr))
	code, b6 := x.quote(b5, "\nif !self.$0.Eq(&other.$1) {\nreturn false\n}\n", cp(name), cp(name))
	br := x.ref(body, true)
	x.flush(b6)
	_, b7 := x.api(b6, "Block:Add", cp(br), cp(code))
	x.assign(b7, lp(i), mir.Rvalue{Kind: mir.RvBinary, Bin: mir.Add, A: cp(i), B: mir.Imm(types.Int, 1)})
	x.gotoB(b7, head)
	tn, b8 := x.api(exit, "Type:Name", cp(tr))
	q, b9 := x.quote(b8, "\nimpl Eq for $0 {\nEq(&self, other: &$1): bool {\n$2\nreturn true\n}\n}\n", cp(tn), cp(tn), cp(body))
	_, b10 := x.api(b9, "Gen:Emit", cp(1), cp(q))
	x.ret(b10)
	x.done()
	return g
}

func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run with -update)", err)
	}
	if got != strings.ReplaceAll(string(want), "\r\n", "\n") {
		t.Fatalf("%s differs:\n--- got\n%s\n--- want\n%s", path, got, want)
	}
}

func noDiags(t *testing.T, out *Output) {
	t.Helper()
	for _, d := range out.Diags {
		t.Errorf("unexpected %s at %v: %s %v", d.Code, d.At, d.Message, d.Facts)
	}
}

// TestDeriveEqShape: derive.Eq over struct User { name: str, age:
// int } prints testdata/derive_eq.golden, Eq's closing brace in column 4
// and the impl's in column 0; the output parses with no diagnostic.
func TestDeriveEqShape(t *testing.T) {
	w := newWorld(t)
	_, user := w.structType("main", "User", false, "name", types.Str, "age", types.Int)
	eq := w.deriveEq()
	out := w.context(1).Run(w.module("main"), []Line{w.line(0, "derive.Eq", eq, typeArg(user))})
	noDiags(t, out)
	golden(t, "derive_eq.golden", out.Text())
	if !strings.Contains(out.Text(), "\n    }\n}\n") {
		t.Errorf("closing braces not at their opening lines' columns:\n%s", out.Text())
	}
	if out.AST == nil || len(out.AST.Items) != 1 {
		t.Fatalf("parsed output: %+v", out.AST)
	}
	if _, ok := out.AST.Items[0].(*ast.ImplDecl); !ok {
		t.Fatalf("output item is %T, want an impl", out.AST.Items[0])
	}
	if len(out.Items) != 1 || out.Items[0].Ordinal != 1 || out.Items[0].Gen == out.Items[0].Emitter {
		t.Fatalf("item origins %+v", out.Items)
	}
	it := out.Items[0]
	if got := out.File.Text[it.Output.Start:it.Output.End]; !strings.HasPrefix(got, "impl Eq for User {") || !strings.HasSuffix(got, "}") {
		t.Fatalf("item output span covers %q", got)
	}
	if !out.Lines[0].Ran || out.Lines[0].Steps == 0 {
		t.Fatalf("line result %+v", out.Lines[0])
	}
}

// TestTraceComments: one trace comment per item, no line numbers.
func TestTraceComments(t *testing.T) {
	w := newWorld(t)
	_, user := w.structType("main", "User", false, "name", types.Str)
	eq := w.deriveEq()
	out := w.context(1).Run(w.module("main"), []Line{w.line(0, "derive.Eq", eq, typeArg(user))})
	noDiags(t, out)
	lines := strings.Split(out.Text(), "\n")
	if !strings.HasPrefix(lines[0], "// Generated from the gen lines of main for x86_64-windows.") || lines[2] != "// gen derive.Eq#1 for User" {
		t.Fatalf("header and trace:\n%s", out.Text())
	}
	if strings.Count(out.Text(), "// gen ") != 1 {
		t.Fatalf("trace comments:\n%s", out.Text())
	}
}

// TestE0905_ParseErrorInOutput and TestE0905_GenInOutput: an output that
// does not parse, and a gen or import in it, are E0905 at the gen line,
// phase of the inner, facts output, emitter, gen, inner; the bad item is
// dropped from Output.AST.
func TestE0905_ParseErrorInOutput(t *testing.T) {
	for _, c := range []struct{ name, text string }{
		{"parse", "\nstruct S {\na: \n}\n"},
		{"gen", "\ngen derive.Eq(S)\n"},
		{"import", "\nimport std.fs\n"},
	} {
		t.Run(c.name, func(t *testing.T) {
			w := newWorld(t)
			g := w.generator("lib", "Bad")
			x := w.body(g)
			q, b1 := x.quote(0, c.text)
			_, b2 := x.api(b1, "Gen:Emit", cp(1), cp(q))
			x.ret(b2)
			x.done()
			l := w.line(0, "lib.Bad", g)
			out := w.context(1).Run(w.module("main"), []Line{l})
			if len(out.Diags) == 0 {
				t.Fatalf("no diagnostic for\n%s", out.Text())
			}
			for _, d := range out.Diags {
				if d.Code.String() != "E0905" || d.At != l.Span {
					t.Fatalf("%s at %v, want E0905 at the gen line %v", d.Code, d.At, l.Span)
				}
				for _, k := range []string{"output", "emitter", "gen", "inner"} {
					if _, ok := d.FactValue(k); !ok {
						t.Errorf("E0905 lacks fact %s", k)
					}
				}
			}
			if len(out.AST.Items) != 0 {
				t.Fatalf("bad items kept: %d", len(out.AST.Items))
			}
		})
	}
}

// TestSplices: each operand type of the splice table.
func TestSplices(t *testing.T) {
	w := newWorld(t)
	_, user := w.structType("lib", "User", true)
	g := w.generator("lib", "S", w.typeT())
	x := w.body(g)
	tr := x.ref(2, false)
	gr := x.refPlace(x.deref(1), w.named(w.known.Gen), false)
	x.flush(0)
	code, b1 := x.api(0, "Gen:Str", cp(gr), w.strConst("a\"b\n"))
	blk, b2 := x.api(b1, "Gen:Block", cp(gr))
	one, b3 := x.quote(b2, "x := 1")
	br := x.ref(blk, true)
	x.flush(b3)
	_, b4 := x.api(b3, "Block:Add", cp(br), cp(one))
	_ = tr
	q, b5 := x.quote(b4, "const K = F($0, $1, $2, $3, $4, $5, $6)\nT() {\n$7\n}\n",
		w.strConst("name"), mir.Imm(types.Int, uint64(1<<64-5)), mir.Imm(types.U8, 200), mir.Imm(types.Bool, 1),
		mir.Imm(types.Char, '\''), cp(code), cp(2), cp(blk))
	_, b6 := x.api(b5, "Gen:Emit", cp(1), cp(q))
	x.ret(b6)
	x.done()
	out := w.context(1).Run(w.module("main"), []Line{w.line(0, "lib.S", g, typeArg(user))})
	text := out.Text()
	want := "const K = F(name, -5, 200, true, '\\'', \"a\\\"b\\n\", lib.User)"
	if !strings.Contains(text, want) {
		t.Fatalf("splices:\n%s\nwant %s", text, want)
	}
	if !strings.Contains(text, "T() {\n    x := 1\n}") {
		t.Fatalf("block splice:\n%s", text)
	}
	// The pinned type's tokens carry their origins.
	off := uint32(strings.Index(text, "lib.User"))
	var kinds []OriginKind
	for _, o := range out.Origins {
		if o.Offset == off || o.Offset == off+4 {
			kinds = append(kinds, o.Kind)
		}
	}
	if len(kinds) != 2 || kinds[0] != OriginPinnedModule || kinds[1] != OriginPinnedItem {
		t.Fatalf("origins of lib.User: %v", kinds)
	}
	if o := out.Origins[0]; o.Kind != OriginLiteral || o.Module != w.module("lib") {
		t.Fatalf("the quote's literal name K: %+v", o)
	}
	// A str that is not an identifier is E0901 at the gen line.
	w2 := newWorld(t)
	g2 := w2.generator("lib", "B")
	x2 := w2.body(g2)
	q2, c1 := x2.quote(0, "const $0 = 1", w2.strConst("not ok"))
	_, c2 := x2.api(c1, "Gen:Emit", cp(1), cp(q2))
	x2.ret(c2)
	x2.done()
	l := w2.line(0, "lib.B", g2)
	out2 := w2.context(1).Run(w2.module("main"), []Line{l})
	if len(out2.Diags) != 1 || out2.Diags[0].Code.String() != "E0901" || out2.Diags[0].At != l.Span || out2.File != nil {
		t.Fatalf("bad identifier splice: %+v", out2.Diags)
	}
}

// TestClosingBraceIndent: printer unit cases.
func TestClosingBraceIndent(t *testing.T) {
	lex := func(s string) []tok {
		lt, _ := syntax.Lex(0, s)
		var ts []tok
		for _, k := range lt {
			switch {
			case k.Kind == ast.TokWhitespace || k.Kind == ast.TokTerm || k.Kind == ast.TokEOF:
			case hasText(k.Kind):
				ts = append(ts, tok{kind: k.Kind, text: k.Text(s)})
			default:
				ts = append(ts, tok{kind: k.Kind})
			}
		}
		return ts
	}
	for _, c := range []struct{ in, want string }{
		{"F() {\nif a {\nb()\n} else {\nc()\n}\n}", "F() {\n    if a {\n        b()\n    } else {\n        c()\n    }\n}"},
		{"F() {\nx()\n\n\n\n}", "F() {\n    x()\n}"},
		{"F() {\na()\n\nb()\n}", "F() {\n    a()\n\n    b()\n}"},
		{"G(xs: []int, p: &mut [4]u8, e: E!int): ?*T {\nreturn -1 + a - b\n}", "G(xs: []int, p: &mut [4]u8, e: E!int): ?*T {\n    return -1 + a - b\n}"},
		{"H() {\nx := a[i] & b\ny := !c && List[int].New()\n}", "H() {\n    x := a[i] & b\n    y := !c && List[int].New()\n}"},
	} {
		var p printer
		p.tokens(lex(c.in))
		if got := p.b.String(); got != c.want {
			t.Errorf("print(%q) =\n%s\nwant\n%s", c.in, got, c.want)
		}
	}
}

// BenchmarkGenDeriveEqHandBuilt: gen derive.Eq(User) over a 10-field struct, from
// bind to parsed output, over hand-built MIR. BenchmarkGenDeriveEq
// (mkderive_test.go) is the same over std's lowered derive.Eq.
func BenchmarkGenDeriveEqHandBuilt(b *testing.B) {
	w := newWorld(b)
	var fs []any
	for i := range 10 {
		fs = append(fs, "f"+itoa(i), types.Int)
	}
	_, user := w.structType("main", "User", false, fs...)
	eq := w.deriveEq()
	c := w.context(1)
	b.ResetTimer()
	for b.Loop() {
		out := c.Run(w.module("main"), []Line{w.line(0, "derive.Eq", eq, typeArg(user))})
		if len(out.Diags) != 0 || out.AST == nil {
			b.Fatal("derive.Eq failed")
		}
	}
}
