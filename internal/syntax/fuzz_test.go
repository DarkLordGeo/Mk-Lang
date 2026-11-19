// Why: the lexer and parser read untrusted text and must never panic, hang
// or build an invalid tree, whatever the input.
// What: FuzzLex and FuzzParse, their seeds, TestParseNeverPanics and the
// regression tests of fuzz crashers.
// How: take bytes, normalize them, and assert no E0000, token coverage and
// print-back, ast.Validate, the depth limit, the same result twice, and a
// return within fuzzLimit.

package syntax

import (
	"runtime/debug"
	"strings"
	"testing"
	"time"

	"mk/internal/ast"
	"mk/internal/diag"
	"mk/internal/source"
)

// fuzzSeeds are every corpus block and the hand inputs of the tests.
func fuzzSeeds(f *testing.F) {
	for _, b := range docBlocks(f) {
		f.Add([]byte(b.text))
	}
	for _, b := range programFiles(f) {
		f.Add([]byte(b.text))
	}
	for _, s := range seedInputs {
		f.Add([]byte(s))
	}
}

// seedInputs are small inputs that reach error paths.
var seedInputs = []string{
	// Value arms, operands, loop patterns, the assert habit.
	"F() {\n x := if c { 1 } else { 2 }\n F(match x { .A { 1 } }, [if c { 1 } else {\n 2\n }])\n return 1 + if c { 1 } else { 2 }\n}\n",
	"F() {\n for (a, b) in m.Iter() { }\n for i, (a: x, b: y) in xs { }\n for P(a, ..) in &xs { }\n assert x == 1\n assert!(y)\n}\n",
	"F() { x := if c { 1 } else { 2 }.f\n y := (a: match q { }, b: if z { } else { })\n}\n",
	"", "\n", "x", "F() {", "F() { x := (1 + }\nG() { }\n", "#[derive(Eq)]\nstruct P { }\n",
	"@inline\nF() { }\n", "F() {\n x := @\n y := 2\n}\n", "fn add(a: int, b: int) -> int {\n return a + b;\n}\n",
	"F() { let mut x = &mut y; }\n", "impl Point {\n fn len(&self) -> f64 { }\n}\n",
	"F(x: E) { match x {\n.A => 1,\n.B => { g() },\n} }\n", "F() { for i in 0..=n { } }\n",
	"F() { x := a.b()?.c()? }\n", "F() { y := n as f64 }\n", "F() { v := |x| x + 1 }\n",
	"\"\"\"\n  a\n  \"\"\"\n", "'a' 'ab' '' b'x' b\"\\xff\" r\"raw\" 0x1F 1_000 1.5e9 0X1 007 1u8\n",
	"F() { (x: .A | .B) := p }\n", "Make(): fn(a: mut A, b: mut B) *a into b, *b into a {\n}\n",
	"G[[4][2]u8]()\nF(m: []int, k: int): int { return m[[k][0]] }\n", "quote { $x $(a + b) }\n",
	"F() { if (a, b) := e { } }\n", "F() { x := if c { 1 } else { 2 } }\n", "/* c */ F() { }\n",
	// The systems forms (asm, naked, align, section, extern var), whole and broken.
	"F() { unsafe \"r\" { asm(\"mov {t:r}, rbx\", t: out(reg) b, inout(\"eax\") a, out(reg) _, const 4, s: sym G, clobber_abi(\"C\"), options(nomem, nostack),) } }\n",
	"F() { asm( }\n", "F() { asm(x, in(reg) y) }\n", "F() { asm(\"\", in(reg)) }\n", "F() { asm(\"\", out() x, options()) }\n",
	"F() { asm(\"\", t: options(att)) }\n", "F() { x := asm(\"\") }\n", "F() { asm }\n", "naked G() { }\nextern naked H() { }\n",
	"align(16) struct S { x: u8 }\npacked align(8) struct T { }\nalign(4)\n", "const C: u8 = 1 fuel 9 section \".x\"\nvar V: u8 = 1 section r\".y\"\n",
	"extern var E: u8\nextern var F: u8 = 1\nextern var\n",
}

// checkParse is the fuzz property.
func checkParse(t *testing.T, data []byte) {
	text := source.Normalize(data)
	if uint64(len(text)) > source.MaxFileSize {
		return
	}
	sm := source.NewSourceMap()
	src := sm.Add("fuzz.mk", data)
	in := source.NewInterner()
	f1, d1 := ParseFile(src, in, Options{})
	for _, d := range d1 {
		if d.Code == diag.E0000 {
			t.Logf("internal error: %s; rerunning unguarded for the stack", d.Message)
			lx := lexWith(src.ID, src.Text, false)
			newParser(src, in, Options{}, lx).parseFile(&ast.File{Tokens: lx.toks})
			t.Fatalf("internal error: %s", d.Message)
		}
	}
	if err := ast.Validate(f1, src.Text); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	for _, it := range f1.Items {
		if d := treeDepth(it); d > DefaultMaxDepth {
			t.Fatalf("depth %d over the limit", d)
		}
	}
	f2, d2 := ParseFile(src, in, Options{})
	if len(d1) != len(d2) || ast.Sprint(f1, in) != ast.Sprint(f2, in) {
		t.Fatalf("two parses differ")
	}
	for i := range d1 {
		if d1[i].Code != d2[i].Code || d1[i].At != d2[i].At || d1[i].Message != d2[i].Message {
			t.Fatalf("two parses differ at diagnostic %d", i)
		}
	}
}

// fuzzLimit is the per-input bound for the lexer and the parser: FuzzParse
// returns within 2 s, and FuzzLex is held to the same.
const fuzzLimit = 2 * time.Second

// withinLimit runs f on its own goroutine and fails t from the test
// goroutine when f panics or has not returned after fuzzLimit: a hang is
// detected by running the target in a goroutine and failing from the test
// goroutine after the limit. f may call t.Fatalf: FailNow ends f's
// goroutine, whose deferred close still runs. A hung f is left running; the
// fuzz worker reports the input and saves it.
func withinLimit(t *testing.T, what string, f func()) {
	t.Helper()
	done := make(chan struct{})
	var caught any
	var stack []byte
	go func() {
		defer close(done)
		defer func() {
			if p := recover(); p != nil {
				caught, stack = p, debug.Stack()
			}
		}()
		f()
	}()
	select {
	case <-done:
		if stack != nil {
			t.Fatalf("%s panicked: %v\n%s", what, caught, stack)
		}
	case <-time.After(fuzzLimit):
		t.Fatalf("%s did not return within %v", what, fuzzLimit)
	}
}

// FuzzLex: no panic, no E0000, coverage and print-back, the same tokens
// twice, within fuzzLimit.
func FuzzLex(f *testing.F) {
	fuzzSeeds(f)
	f.Fuzz(func(t *testing.T, data []byte) {
		withinLimit(t, "Lex", func() { checkLex(t, data) })
	})
}

// checkLex is FuzzLex's property for one input.
func checkLex(t *testing.T, data []byte) {
	text := source.Normalize(data)
	toks, ds := Lex(1, text)
	for _, d := range ds {
		if d.Code == diag.E0000 {
			t.Fatalf("internal error: %s", d.Message)
		}
	}
	file := &ast.File{Base: ast.Base{NID: 1, Sp: source.Span{File: 1, End: uint32(len(text))}}, Tokens: toks, NodeCount: 1}
	if err := ast.Validate(file, text); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	toks2, _ := Lex(1, text)
	if len(toks) != len(toks2) {
		t.Fatalf("two lexes differ")
	}
	for i := range toks {
		if toks[i] != toks2[i] {
			t.Fatalf("two lexes differ at %d", i)
		}
	}
}

// FuzzParse: no E0000, a valid tree within the depth limit, deterministic,
// within fuzzLimit.
func FuzzParse(f *testing.F) {
	fuzzSeeds(f)
	f.Fuzz(func(t *testing.T, data []byte) {
		withinLimit(t, "ParseFile", func() { checkParse(t, data) })
	})
}

// TestParseNeverPanics runs the fuzz property over the seeds.
func TestParseNeverPanics(t *testing.T) {
	t.Parallel()
	inputs := append([]string(nil), seedInputs...)
	for _, b := range docBlocks(t) {
		inputs = append(inputs, b.text)
		// Every prefix cut at a line boundary exercises the unclosed paths.
		for i := 0; i < len(b.text); i++ {
			if b.text[i] == '\n' {
				inputs = append(inputs, b.text[:i])
			}
		}
	}
	for _, s := range inputs {
		checkParse(t, []byte(s))
	}
}

// TestFuzzPathAfterMissingName is the FuzzParse crasher 7a09553b040d077c
// and its relatives: `A.` with the last name missing and a space (or the end)
// after the dot. The missing name is an empty Ident at the next token, past
// the end of the FieldExpr; a Path built from the names' spans then ended
// there and left its PathType or PathPattern (ast.Validate). The Path now
// takes the span of the expression the names came from.
func TestFuzzPathAfterMissingName(t *testing.T) {
	t.Parallel()
	for _, text := range []string{
		"A{0[?!A. ",                           // the crasher: `!A.` as an ErrorType
		"F() { x := G[A. ]() }\n",             // a type argument, PathType
		"F() { x := G[A.B. ]() }\n",           // a longer chain
		"F() { x := G[A. [int]]() }\n",        // with type arguments
		"F(x: E) { match x {\nA. => 1\n} }\n", // a match arm, PathPattern
		"F(x: E) { match x {\nA. \n} }\n",
		"F(x: E) { if A. := x { } }\n",
		"F(x: E) { if A. (b) := x { } }\n",
		"F() { A. (b) := c }\n",
		"F() { A. {b} := c }\n",
		"F() { P(x: A. ) := v }\n",    // a cover field value, PathPattern
		"F() { P(x: A. (y)) := v }\n", // a cover call, PathPattern with fields
		"F() { P(x: A.B. ) := v }\n",
	} {
		r := parse(t, text, Options{})
		if len(r.diags) == 0 {
			t.Errorf("%q: no diagnostic for the missing name", text)
		}
		checkParse(t, []byte(text))
	}
}

// TestFuzzFlowAfterContract is the FuzzParse crasher 1c7326dab702c699,
// `A ensures 0 ends`, and every clause order around it. A Signature has
// the flows, then the contracts; a flow after a contract is E0101 "flows come
// before contracts". The recovery appended that flow to Signature.Flows, which
// EachChild visits before Contracts, so the tree left source order
// (ast.Validate: a Contract starting before its previous sibling ends). The
// misplaced flows are now read and dropped; the contracts stay.
func TestFuzzFlowAfterContract(t *testing.T) {
	t.Parallel()
	const order = "flows come before contracts"
	for _, c := range []struct {
		src          string
		msg          string // first diagnostic's message, "" for none
		flows, contr int
	}{
		// The grammar's order, which parses clean.
		{"F(a: &A): &A from a ensures x {\n}\n", "", 1, 1},
		{"F(a: &A): &A from a, ends a *a into b requires x ensures y {\n}\n", "", 3, 2},
		{"F(a: &A): &A requires x ensures y requires z {\n}\n", "", 0, 3},
		{"F(a: &A): &A\n    from a\n    requires x\n    ensures y\n{\n}\n", "", 1, 2},
		{"F(a: &A)\n    *a into b\n    ensures x\n{\n}\n", "", 1, 1},
		// A flow after a contract, on the line or on a continuation line.
		{"F(a: &A): &A ensures x from a {\n}\n", order, 0, 1},
		{"F(a: &A): &A ensures x ends a {\n}\n", order, 0, 1},
		{"F(a: &A): &A requires x ends a ensures y {\n}\n", order, 0, 2},
		{"F(a: &A): &A requires x ends a from b ensures y {\n}\n", order, 0, 2},
		{"F(a: &A): &A requires x ends a, from b {\n}\n", order, 0, 1},
		{"F(a: &A): &A requires x ends a ends b {\n}\n", order, 0, 1},
		{"F(a: &A): &A requires x ends a: int {\n}\n", order, 0, 1},
		{"F(a: &A): &A from a requires x from b {\n}\n", order, 1, 1},
		{"F(a: &A): &A from a ensures x ends a ensures y {\n}\n", order, 1, 2},
		{"F(a: &A): &A requires x\n    ends a\n{\n}\n", order, 0, 1},
		{"F(a: &A): &A requires x\n    *a into b\n{\n}\n", order, 0, 1},
		{"F(a: &A): &A requires x ends a", order, 0, 1},
		// On the contract's line a `*` is a multiplication, `x * a`.
		{"F(a: &A): &A requires x *a into b {\n}\n", "function needs a body", 0, 1},
		// Flows and contracts before the result type.
		{"F(a: &A) ends a: int requires x {\n}\n", "flows come after the result type", 1, 0},
		{"F(a: &A) requires x: int {\n}\n", "function needs a body", 0, 1},
	} {
		r := parse(t, c.src, Options{})
		checkParse(t, []byte(c.src))
		switch {
		case c.msg == "" && len(r.diags) != 0:
			t.Errorf("%q: %v, want none", c.src, r.codes())
		case c.msg != "" && (r.codeList() != "E0101" || !strings.Contains(r.diags[0].Message, c.msg)):
			t.Errorf("%q: %v, want one E0101 %q", c.src, r.codes(), c.msg)
		}
		if len(r.file.Items) == 0 {
			t.Errorf("%q: no item", c.src)
			continue
		}
		fd, ok := r.file.Items[0].(*ast.FuncDecl)
		if !ok {
			t.Errorf("%q: item is %T", c.src, r.file.Items[0])
			continue
		}
		if len(fd.Sig.Flows) != c.flows || len(fd.Sig.Contracts) != c.contr {
			t.Errorf("%q: %d flows, %d contracts, want %d, %d", c.src, len(fd.Sig.Flows), len(fd.Sig.Contracts), c.flows, c.contr)
		}
	}
	// The crasher itself: not a signature (no `(`), one E0101, a valid tree.
	r := parse(t, "A ensures 0 ends", Options{})
	if r.codeList() != "E0101" {
		t.Errorf("crasher: %v, want one E0101", r.codes())
	}
	checkParse(t, []byte("A ensures 0 ends"))
}
