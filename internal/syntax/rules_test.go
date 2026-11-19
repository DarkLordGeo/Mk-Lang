// Why: the lexer and parser had depth-limit escapes, quadratic paths and
// wrong recoveries; each must stay fixed.
// What: regression tests, each named after the rule it pins, with a comment
// on the defect it closes.
// How: parses crafted inputs (long runs in a child process with a capped
// stack, too-deep fragments, stray bytes) and checks codes, trees and spans.

package syntax

import (
	"os"
	"os/exec"
	"runtime/debug"
	"strings"
	"testing"
	"time"

	"mk/internal/ast"
	"mk/internal/diag"
)

// countMsg counts the diagnostics whose message contains s.
func countMsg(r parsed, s string) int {
	n := 0
	for _, d := range r.diags {
		if strings.Contains(d.Message, s) {
			n++
		}
	}
	return n
}

// A run of unary '+', of lifetimes in a type, of await, or an
// alternation of them, recursed with no node, so the depth limit never
// stopped it. Each is bounded: with MaxDepth 50 the runs below give one
// "nesting too deep" or none, never a deeper Go stack than the limit allows.
func TestNodelessRecursion(t *testing.T) {
	t.Parallel()
	opts := Options{MaxDepth: 50}
	for _, c := range []struct{ name, src string }{
		{"plus", "F() { x := " + strings.Repeat("+ ", 200) + "y }\n"},
		{"lifetime", "F(x: " + strings.Repeat("'a ", 200) + "T) { }\n"},
		{"await", "F() { x := " + strings.Repeat("await ", 200) + "y }\n"},
		{"lifetime operand", "F() { x := " + strings.Repeat("'a ", 200) + "y }\n"},
	} {
		r := parse(t, c.src, opts)
		if n := countMsg(r, "nesting too deep"); n > 1 {
			t.Errorf("%s: %d depth errors: %v", c.name, n, r.codes())
		}
	}
	// `+ await + await …` is one unary '+' over a binary chain on the name
	// await (an await before '+' is not the habit): bounded by the chain's
	// height, one depth error.
	alt := parse(t, "F() { x := "+strings.Repeat("+ await ", 200)+"y }\n", opts)
	if n := countMsg(alt, "nesting too deep"); n != 1 {
		t.Errorf("alternation: %d depth errors: %v", n, alt.codes())
	}
	// A run is a loop: 10,000 '+' at the default limit is not "too deep".
	run := parse(t, "F() { x := "+strings.Repeat("+", 10_000)+" y }\n", Options{})
	if countMsg(run, "nesting too deep") != 0 {
		t.Errorf("run: %v", run.codes())
	}
}

// TestNodelessRecursionChild runs the long runs above in a child
// process whose goroutine stacks are capped at 16 MB (runtime/debug), where
// the old recursion died with "fatal error: stack overflow" (an unrecoverable
// crash, so it needs a process of its own). Each input is 1 to 4 MB.
func TestNodelessRecursionChild(t *testing.T) {
	if os.Getenv("MK_SYNTAX_NODELESS_CHILD") == "1" {
		debug.SetMaxStack(16 << 20)
		for _, src := range []string{
			"F() { x := " + strings.Repeat("+ ", 1_000_000) + "y }\n",
			"F(x: " + strings.Repeat("'a ", 1_000_000) + "T) { }\n",
			"F() { x := " + strings.Repeat("await ", 500_000) + "y }\n",
			"F() { x := " + strings.Repeat("+ await ", 500_000) + "y }\n",
		} {
			r := parse(t, src, Options{})
			if n := countMsg(r, "nesting too deep"); n > 1 {
				t.Errorf("%d depth errors", n)
			}
		}
		return
	}
	if testing.Short() {
		t.Skip("child process; skipped with -short")
	}
	t.Parallel()
	cmd := exec.Command(os.Args[0], "-test.run=^TestNodelessRecursionChild$", "-test.count=1")
	cmd.Env = append(os.Environ(), "MK_SYNTAX_NODELESS_CHILD=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("child failed: %v\n%s", err, tail(string(out), 2000))
	}
}

func tail(s string, n int) string {
	if len(s) > n {
		return s[len(s)-n:]
	}
	return s
}

// toType converted a left-nested '|' operand twice, doubling the
// nodes per level. 25 levels now make a few hundred nodes.
func TestToTypeLinear(t *testing.T) {
	t.Parallel()
	x := "a"
	for i := 0; i < 25; i++ {
		x = "(" + x + " | b)"
	}
	start := time.Now()
	r := parse(t, "type T = G["+x+" | ?U]\n", Options{})
	el := time.Since(start)
	if len(r.diags) != 0 {
		t.Fatalf("%v", r.codes())
	}
	if r.file.NodeCount >= 1000 {
		t.Errorf("NodeCount %d for 25 levels", r.file.NodeCount)
	}
	if !timingRun() {
		t.Logf("%s: the 2 s bound is not asserted (took %v)", timingOff, el)
	} else if el > 2*time.Second {
		t.Errorf("took %v", el)
	}
}

// A Term inside a block nested in a signature ends a statement, so it is
// hashed; only a Term at the signature's own brace level is continuation
// layout.
func TestItemHashSeesTermsInSignatureBlocks(t *testing.T) {
	t.Parallel()
	pairs := [][2]string{
		{"F(xs: []int) requires All(xs, fn(x: int): bool { y := x\n-1\nreturn y > 0 }) { }\n",
			"F(xs: []int) requires All(xs, fn(x: int): bool { y := x -1\nreturn y > 0 }) { }\n"},
		{"F(x: [N() else { y := 1\n-2 }]int) { }\n",
			"F(x: [N() else { y := 1 -2 }]int) { }\n"},
	}
	for i, pr := range pairs {
		a := parse(t, pr[0], Options{})
		b := parse(t, pr[1], Options{})
		if len(a.diags) != 0 || len(b.diags) != 0 {
			t.Fatalf("pair %d: %v / %v", i, a.codes(), b.codes())
		}
		if a.tree() == b.tree() {
			t.Fatalf("pair %d: the trees are equal", i)
		}
		if ItemHash(a.file, a.src.Text, a.file.Items[0]) == ItemHash(b.file, b.src.Text, b.file.Items[0]) {
			t.Errorf("pair %d: equal ItemHash", i)
		}
		if FileHash(a.file, a.src.Text) == FileHash(b.file, b.src.Text) {
			t.Errorf("pair %d: equal FileHash", i)
		}
	}
	// Continuation still hashes as layout, with a block in the contract.
	c1 := itemHash(t, "F(a: &A): &A from a requires G(fn() { H() }) { return a }\n", 0)
	c2 := itemHash(t, "F(a: &A): &A\nfrom a\nrequires G(fn() { H() })\n{\n    return a\n}\n", 0)
	if c1 != c2 {
		t.Error("continuation lines changed the hash")
	}
}

// FileHash walks the continuation ranges with one cursor, so it
// costs about what hashing every item costs (it was O(items x Terms)).
func TestFileHashLinear(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 32_000; i++ {
		b.WriteString("F" + itoa(i) + "(a: &A): &A\nfrom a\n{\n    x := a\n    return x\n}\n")
	}
	r := parse(t, b.String(), Options{})
	if len(r.diags) != 0 {
		t.Fatalf("%v", r.codes()[:1])
	}
	start := time.Now()
	for _, it := range r.file.Items {
		ItemHash(r.file, r.src.Text, it)
	}
	items := time.Since(start)
	start = time.Now()
	FileHash(r.file, r.src.Text)
	file := time.Since(start)
	t.Logf("192,000 lines: ItemHash over all items %v, FileHash %v", items, file)
	if !timingRun() {
		t.Logf("%s: the FileHash against ItemHash ratio is not asserted", timingOff)
	} else if file > 4*items+200*time.Millisecond {
		t.Errorf("FileHash %v against ItemHash total %v", file, items)
	}
}

// A call or an index on a local named throw or let is legal Mk, so the word
// habits stay away from it.
func TestWordHabitsStayNames(t *testing.T) {
	t.Parallel()
	for _, src := range []string{
		"F(throw: fn(int)) {\n    throw(1)\n}\n",
		"F(throw: mut []int) {\n    throw[0] = 1\n}\n",
		"F(let: fn(int)) {\n    let(1)\n}\n",
		"F(let: fn(int)) {\n    let (1)\n}\n",
		"F(throw: int) {\n    throw = 2\n    x := throw + 1\n}\n",
	} {
		if r := parse(t, src, Options{}); len(r.diags) != 0 {
			t.Errorf("%q: %v", src, r.codes())
		}
	}
	th := parse(t, "F() {\n    throw E\n}\n", Options{})
	if th.codeList() != "E1210" {
		t.Errorf("throw E: %v", th.codes())
	}
	let := parse(t, "F() {\n    let (a, b) = e\n}\n", Options{})
	if let.codeList() != "E1207" || factText(let.diags[0], "target") != "    (a, b) := e" {
		t.Errorf("let (a, b): %v %q", let.codes(), factText(let.diags[0], "target"))
	}
}

// The throw alternative is the parsed value, without a comment or
// the enclosing '}'.
func TestE1210_ThrowAlternative(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ src, alt string }{
		{"F() { throw e }\n", "fail e"},
		{"F() {\n    throw E // boom\n}\n", "fail E"},
		{"F() {\n    throw E(code: 1)\n}\n", "fail E(code: 1)"},
	} {
		r := parse(t, c.src, Options{})
		if r.codeList() != "E1210" {
			t.Fatalf("%q: %v", c.src, r.codes())
		}
		if alts := r.diags[0].Alternatives; len(alts) != 1 || alts[0] != c.alt {
			t.Errorf("%q: alternatives %q, want %q", c.src, alts, c.alt)
		}
	}
}

// Interface and impl member names live in a type's namespace and
// may be universe names.
func TestE0102_MemberNamesExempt(t *testing.T) {
	t.Parallel()
	for _, src := range []string{
		"interface I {\n    Cast(x: int): int\n    Assert(x: int): int\n    Target(): int\n}\n",
		"impl I for T {\n    Cast(x: int): int { return x }\n    Assert(x: int): int { return x }\n    Target(): int { return 1 }\n}\n",
		"T.Cast(self) { }\n",
		"enum E {\n    Target\n}\n",
	} {
		if r := parse(t, src, Options{}); len(r.diags) != 0 {
			t.Errorf("%q: %v", src, r.codes())
		}
	}
	top := parse(t, "Cast() { }\n", Options{})
	if top.codeList() != "E0102" {
		t.Errorf("top-level Cast: %v", top.codes())
	}
	ext := parse(t, "extern Cast(x: int): int\n", Options{})
	if ext.codeList() != "E0102" {
		t.Errorf("extern Cast: %v", ext.codes())
	}
}

// A bare-name field at any depth of an alternative binds, so it
// is E0101: an alternative binds nothing.
func TestNestedBindingInAlternative(t *testing.T) {
	t.Parallel()
	for _, src := range []string{
		"F(s: S) { match s { .A(x: .C(y)) | .B(x: _) { } } }\n",
		"F(s: S) { match s { some(.C(y)) | none { } } }\n",
		"F(s: S) { (x: .A(v: .C(y)) | .B) := s }\n",
		"F(s: S) { (x: P(y) | Q) := s }\n",
		"F(s: S) { match s { .A(x: .B(y: .C | .D(z))) | .E { } } }\n",
	} {
		r := parse(t, src, Options{})
		if r.codeList() != "E0101" || countMsg(r, "alternatives bind nothing") != 1 {
			t.Errorf("%q: %v", src, r.codes())
		}
	}
	for _, src := range []string{
		"F(s: S) { match s { .A(x: .C(y: _)) | .B(x: _) { } } }\n",
		"F(s: S) { match s { some(.C) | none { } } }\n",
		"F(s: S) { match s { .A(x) { } } }\n",
	} {
		if r := parse(t, src, Options{}); len(r.diags) != 0 {
			t.Errorf("%q: %v", src, r.codes())
		}
	}
}

// pub before impl, import, gen or test is deleted, not turned into export
// (which is E0101 there), and the item is not marked exported. Every E1215
// target reparses clean.
func TestE1215_TargetsReparse(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ src, target string }{
		{"pub impl I for T { }\n", "impl I for T { }"},
		{"pub import a.b\n", "import a.b"},
		{"pub test \"t\" { }\n", "test \"t\" { }"},
		{"pub(crate) impl I for T { }\n", "impl I for T { }"},
		{"pub F() { }\n", "export F() { }"},
		{"pub struct S { }\n", "export struct S { }"},
	} {
		r := parse(t, c.src, Options{})
		if r.codeList() != "E1215" {
			t.Fatalf("%q: %v", c.src, r.codes())
		}
		if got := factText(r.diags[0], "target"); got != c.target {
			t.Errorf("%q: target %q, want %q", c.src, got, c.target)
		}
		fixed := applyFix(c.src, r.diags[0])
		if re := parse(t, fixed, Options{}); len(re.diags) != 0 {
			t.Errorf("%q: fixed %q gives %v", c.src, fixed, re.codes())
		}
		it := r.file.Items[0]
		switch it.(type) {
		case *ast.ImplDecl, *ast.Import, *ast.TestItem:
			if it.Info().Export {
				t.Errorf("%q: %T marked exported", c.src, it)
			}
		default:
			if !it.Info().Export {
				t.Errorf("%q: %T not exported", c.src, it)
			}
		}
	}
}

// The per-line merge is linear in the hits (it rescanned every
// hit per line and rebuilt the line once per edit).
func TestMergeLinear(t *testing.T) {
	var b strings.Builder
	b.WriteString("F() {\n")
	for i := 0; i < 32_000; i++ {
		b.WriteString("    x := 1;\n")
	}
	b.WriteString("}\n")
	start := time.Now()
	r := parse(t, b.String(), Options{})
	el := time.Since(start)
	n := 0
	for _, d := range r.diags {
		if d.Code == diag.E1206 {
			n++
		}
	}
	t.Logf("32,000 habit lines parsed in %v", el)
	if n != 32_000 || len(r.diags) != 32_000 {
		t.Errorf("%d E1206 of %d diagnostics", n, len(r.diags))
	}
	if !timingRun() {
		t.Logf("%s: the 5 s bound is not asserted", timingOff)
	} else if el > 5*time.Second {
		t.Errorf("took %v", el)
	}
	start = time.Now()
	one := parse(t, "F() { x := "+strings.Repeat("await ", 100_000)+"y }\n", Options{})
	el = time.Since(start)
	t.Logf("one line of 100,000 await parsed in %v", el)
	if one.codeList() != "E1204" || (timingRun() && el > 5*time.Second) {
		t.Errorf("await line: %d diagnostics, %v", len(one.diags), el)
	}
	if !timingRun() {
		t.Logf("%s: the await line's 5 s bound is not asserted", timingOff)
	}
}

// After the depth error the item resynchronizes from its start,
// so the closers the deep productions left are not each "expected an item".
func TestDepthOneDiagnostic(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ name, src string }{
		{"match", "F() { " + strings.Repeat("match x { _ { ", 400) + strings.Repeat("} }", 400) + " }\n"},
		{"pattern", "F(s: S) { match s { " + strings.Repeat(".A(x: ", 1200) + "_" + strings.Repeat(")", 1200) + " { } } }\n"},
		{"quote", "F() { x := " + strings.Repeat("quote { $(", 2000) + "y" + strings.Repeat(") }", 2000) + " }\n"},
		{"ifs", "F() { " + strings.Repeat("if c { ", 1001) + strings.Repeat("} ", 1001) + "}\n"},
	} {
		r := parse(t, c.src+"G() { }\n", Options{})
		if countMsg(r, "nesting too deep") != 1 || countMsg(r, "expected an item") != 0 {
			t.Errorf("%s: %v", c.name, firstCodes(r, 5))
		}
		// "quotes do not nest" is an error of its own, before the depth.
		if want := 1 + countMsg(r, "quotes do not nest"); len(r.diags) != want {
			t.Errorf("%s: %d diagnostics: %v", c.name, len(r.diags), firstCodes(r, 5))
		}
		if len(r.file.Items) != 2 {
			t.Fatalf("%s: %d items", c.name, len(r.file.Items))
		}
		if _, ok := r.file.Items[0].(*ast.BadItem); !ok {
			t.Errorf("%s: first item %T", c.name, r.file.Items[0])
		}
		if _, ok := r.file.Items[1].(*ast.FuncDecl); !ok {
			t.Errorf("%s: second item %T", c.name, r.file.Items[1])
		}
	}
}

func firstCodes(r parsed, n int) []string {
	cs := r.codes()
	if len(cs) > n {
		cs = cs[:n]
	}
	return cs
}

// The one-line `"""abc"""` is one E0101 and ends on its line.
func TestOneLineTripleQuote(t *testing.T) {
	t.Parallel()
	r := parse(t, "F() {\n    x := \"\"\"abc\"\"\"\n    y := 2\n}\n", Options{})
	if r.codeList() != "E0101" || !strings.Contains(r.diags[0].Message, "text after the opening") {
		t.Fatalf("%v", r.codes())
	}
	body := r.file.Items[0].(*ast.FuncDecl).Body
	if len(body.Stmts) != 2 {
		t.Fatalf("%d statements", len(body.Stmts))
	}
	if _, ok := body.Stmts[1].(*ast.BindStmt); !ok {
		t.Errorf("second statement %T", body.Stmts[1])
	}
	// Text after the opening with no closing on the line keeps the literal
	// open; an unterminated one is still one diagnostic.
	u := parse(t, "F() {\n    x := \"\"\"abc\n    def\n", Options{})
	if countMsg(u, "multi-line")+countMsg(u, "text after the opening") != 1 {
		t.Errorf("unterminated: %v", u.codes())
	}
	// parseUnsafe decodes a TokMultiStr reason; the one-line shape must not
	// break the decoder.
	v, ok := StringValue(ast.TokMultiStr, "\"\"\"abc\"\"\"")
	t.Logf("one-line value %q, %v", v, ok)
	if r := parse(t, "F() {\n    unsafe \"\"\"why\"\"\" { }\n}\n", Options{}); r.codeList() != "E0101" {
		t.Errorf("unsafe reason: %v", r.codes())
	}
}

// A lifetime parameter list `<'a>` after a function name is one
// E1203 that deletes the list, and the whole Rust line's target reparses.
func TestE1203_LifetimeList(t *testing.T) {
	t.Parallel()
	runHabits(t, []hcase{
		{name: "list", src: "F<'a>(x: &'a T) { }\n", code: "E1203", habits: "[E1203]", target: "F(x: &T) { }"},
		{name: "two", src: "F<'a, 'b>(x: &'a T, y: &'b T) { }\n", code: "E1203", habits: "[E1203]", target: "F(x: &T, y: &T) { }"},
		{name: "rust", src: "fn f<'a>(x: &'a T) -> &'a T { return x }\n", code: "E1205", habits: "[E1205 E1203 E1211]",
			target: "F(x: &T): &T { return x }"},
	})
}

// The second-from merge deletes only the second from and its
// separator; a flow between the two stays.
func TestMergeFixKeepsOtherFlows(t *testing.T) {
	t.Parallel()
	for _, src := range []string{
		"F(a: &A, b: mut B, c: &A): &A from a *a into b from c { return a }\n",
		"F(a: &A, b: mut B, c: &A): &A from a, *a into b, from c { return a }\n",
		// With the second from on a continuation line: deleting that line
		// leaves the signature on one line, so the body's '{' stays on it.
		"F(a: &A, b: mut B, c: &A): &A from a *a into b\nfrom c { return a }\n",
	} {
		r := parse(t, src, Options{})
		if r.codeList() != "E0101" || len(r.diags[0].Fixes) != 1 {
			t.Fatalf("%q: %v", src, r.codes())
		}
		fixed := applyFix(src, r.diags[0])
		re := parse(t, fixed, Options{})
		if len(re.diags) != 0 {
			t.Fatalf("%q: fixed %q: %v", src, fixed, re.codes())
		}
		sig := re.file.Items[0].(*ast.FuncDecl).Sig
		var from *ast.FromFlow
		stores := 0
		for _, f := range sig.Flows {
			switch f := f.(type) {
			case *ast.FromFlow:
				if from != nil {
					t.Errorf("%q: two from flows after the fix", fixed)
				}
				from = f
			case *ast.StoreFlow:
				stores++
			}
		}
		if from == nil || len(from.Sources) != 2 || stores != 1 {
			t.Errorf("%q: fixed %q has flows %d, store %d", src, fixed, len(sig.Flows), stores)
		}
	}
}

// A stray ')' inside a quote is the lexer's one E0101; the quote
// counts brackets with the lexer's rule and ends at its own '}'.
func TestQuoteStrayCloser(t *testing.T) {
	t.Parallel()
	r := parse(t, "F() { x := quote { ) } }\nG() { }\n", Options{})
	if r.codeList() != "E0101" || !strings.Contains(r.diags[0].Message, "unmatched") {
		t.Fatalf("%v", r.codes())
	}
	if len(r.file.Items) != 2 {
		t.Fatalf("%d items", len(r.file.Items))
	}
	if fd, ok := r.file.Items[1].(*ast.FuncDecl); !ok || r.in.Str(fd.Sig.Name.Name.Name) != "G" {
		t.Errorf("second item %T", r.file.Items[1])
	}
	ok := parse(t, "F() { x := quote { (a) [b] { c } } }\n", Options{})
	if len(ok.diags) != 0 {
		t.Errorf("balanced: %v", ok.codes())
	}
}

// A keyword declared as `kw: T = e`, a keyword record field and a
// keyword for-in name are each one E0102, read as a name.
func TestE0102_KeywordDeclForms(t *testing.T) {
	t.Parallel()
	for _, src := range []string{
		"F() {\n    type: int = 3\n}\n",
		"F() {\n    x := (type: 1)\n}\n",
		"F(xs: []int) {\n    for type in xs { }\n}\n",
		"F(xs: []int) {\n    for i, type in xs { }\n}\n",
		"F() {\n    go := 1\n}\n",
	} {
		r := parse(t, src, Options{})
		if r.codeList() != "E0102" {
			t.Errorf("%q: %v", src, r.codes())
		}
	}
}

// ParseFragment discards a too-deep subtree as ParseFile does, so
// no node it returns is deeper than DefaultMaxDepth.
func TestParseFragment_DepthLimit(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name string
		src  string
		kind FragmentKind
	}{
		{"expr parens", strings.Repeat("(", 3000) + "x" + strings.Repeat(")", 3000), FragExpr},
		{"expr chain", "x" + strings.Repeat(" + x", 5000), FragExpr},
		{"type", strings.Repeat("?", 3000) + "T", FragType},
		{"stmts", "a := 1\nx := " + strings.Repeat("(", 3000) + "x" + strings.Repeat(")", 3000) + "\nb := 2\n", FragStmts},
	} {
		_, nodes, ds := fragment(t, c.src, c.kind)
		deep := 0
		for _, d := range ds {
			if strings.Contains(d, "nesting too deep") {
				deep++
			}
		}
		if deep != 1 || len(ds) != 1 {
			t.Errorf("%s: %v", c.name, ds)
		}
		bad := 0
		for _, n := range nodes {
			if d := treeDepth(n); d > DefaultMaxDepth {
				t.Errorf("%s: node depth %d", c.name, d)
			}
			switch n.(type) {
			case *ast.BadExpr, *ast.BadType, *ast.BadStmt:
				bad++
			}
		}
		if bad != 1 {
			t.Errorf("%s: %d Bad nodes of %d", c.name, bad, len(nodes))
		}
		if c.kind == FragStmts && len(nodes) != 3 {
			t.Errorf("%s: %d statements", c.name, len(nodes))
		}
	}
}

// A range outside a for head or a slice index is E0101 with its own message,
// not the generic one.
func TestRangeOutsideFor(t *testing.T) {
	t.Parallel()
	for _, src := range []string{
		"F() { x := 0..n }\n",
		"F() { G(0..n) }\n",
		"F() { if a..b { } }\n",
	} {
		r := parse(t, src, Options{})
		if r.codeList() != "E0101" || r.diags[0].Message != rangeMsg {
			t.Errorf("%q: %v", src, r.codes())
		}
	}
	for _, src := range []string{"F(xs: []int) { for i in 0..n { } }\n", "F(xs: []int) { y := xs[1..] }\n"} {
		if r := parse(t, src, Options{}); len(r.diags) != 0 {
			t.Errorf("%q: %v", src, r.codes())
		}
	}
}

func TestRangeKeyword(t *testing.T) {
	t.Parallel()
	for _, src := range []string{
		"F(xs: []int) { for i := range xs { } }\n",
		"F(xs: []int) { for i, x := range xs { G(x) } }\n",
		"F(xs: []int) { for _, x := range xs { } }\n",
	} {
		r := parse(t, src, Options{})
		if r.codeList() != "E0101" || !strings.Contains(r.diags[0].Message, "write `for i, x in xs {`") {
			t.Errorf("%q: %v", src, r.codes())
		}
	}
	if r := parse(t, "F() { for range := G() { } }\n", Options{}); len(r.diags) != 0 {
		t.Errorf("a name range: %v", r.codes())
	}
}

func TestE1211_ArrowElsewhere(t *testing.T) {
	t.Parallel()
	r := parse(t, "F() { x := a->b }\n", Options{})
	if r.codeList() != "E1211" {
		t.Fatalf("%v", r.codes())
	}
	d := r.diags[0]
	if len(d.Alternatives) != 1 || d.Alternatives[0] != "a.b" || len(d.Fixes) != 0 {
		t.Errorf("alternatives %q, fixes %d", d.Alternatives, len(d.Fixes))
	}
	if _, ok := r.file.Items[0].(*ast.FuncDecl).Body.Stmts[0].(*ast.BindStmt).Value.(*ast.FieldExpr); !ok {
		t.Error("a->b is not read as a field step")
	}
}

func TestE1203_LifetimeOperand(t *testing.T) {
	t.Parallel()
	// With no operand after the lifetime, deleting it would leave none: the
	// hit has the char literal as an alternative and no fix.
	for _, src := range []string{
		"F() { x := 'a }\n",
		"F() {\n    x := 'a\n    y := 2\n}\n",
	} {
		r := parse(t, src, Options{})
		if r.codeList() != "E1203" {
			t.Fatalf("%q: %v", src, r.codes())
		}
		d := r.diags[0]
		if len(d.Fixes) != 0 || len(d.Alternatives) != 1 || d.Alternatives[0] != "'a'" || factText(d, "target") != "" {
			t.Errorf("%q: fixes %d, alternatives %q, target %q", src, len(d.Fixes), d.Alternatives, factText(d, "target"))
		}
	}
	runHabits(t, []hcase{
		{name: "before operand", src: "F() { x := &'a y }\n", code: "E1203", habits: "[E1203]", target: "F() { x := &y }"},
	})
}

// U+FEFF, a lone CR, a form feed and a vertical tab are E0101
// anywhere in the file, comments and literals included; the
// token is kept and the first offending byte is reported, once.
func TestInnerBOMEverywhere(t *testing.T) {
	t.Parallel()
	const bom = "\xEF\xBB\xBF"
	for _, src := range []string{
		"// a" + bom + "b\n",
		"x := \"a" + bom + "b\"\n",
		"x := r\"a" + bom + "b\"\n",
		"x := '" + bom + "'\n",
		"x := \"\"\"\n    a" + bom + "b\n    \"\"\"\n",
	} {
		lexOne(t, src, strings.Index(src, bom), "byte-order mark inside the file")
	}
}

func TestLoneCREverywhere(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		src string
		at  byte
		msg string
	}{
		{"// a\rb\n", '\r', "carriage return"},
		{"// a\fb\n", '\f', "form feed"},
		{"// a\vb\n", '\v', "form feed"},
		{"x := r\"a\rb\"\n", '\r', "carriage return"},
		{"x := \"\"\"\n    a\rb\n    \"\"\"\n", '\r', "carriage return"},
		{"x := \"a\rb\"\n", '\r', "control character"},
	} {
		lexOne(t, c.src, strings.IndexByte(c.src, c.at), c.msg)
	}
	// Valid non-ASCII stays legal in comments and literals.
	for _, src := range []string{"// café\n", "x := \"café\"\n", "x := r\"café\"\n", "x := \"\"\"\n    café\n    \"\"\"\n"} {
		if ds := lexDiags(src); len(ds) != 0 {
			t.Errorf("%q: %v", src, messages(ds))
		}
	}
}

// lexOne asserts that src lexes with exactly one E0101, at byte at, whose
// message contains msg.
func lexOne(t *testing.T, src string, at int, msg string) {
	t.Helper()
	ds := lexDiags(src)
	if len(ds) != 1 || ds[0].Code != diag.E0101 || int(ds[0].At.Start) != at || !strings.Contains(ds[0].Message, msg) {
		var got []string
		for _, d := range ds {
			got = append(got, d.Code.String()+"@"+itoa(int(d.At.Start))+" "+d.Message)
		}
		t.Errorf("%q: got %v, want one E0101@%d containing %q", src, got, at, msg)
	}
}
