// Why: habits (Rust, C, Go and Python forms written in Mk) must get their own
// code and a fix that really parses, or the hints mislead.
// What: tests of the habit codes E1203 to E1222 and E0609's closures, mostly
// one per code, plus the merging and target rules.
// How: each case asserts the code, the merged habits, the target byte for
// byte, and that applying the proven fix gives a file that parses with no
// diagnostic.

package syntax

import (
	"sort"
	"strings"
	"testing"

	"mk/internal/ast"
	"mk/internal/diag"
)

// hcase is one habit expectation for the first diagnostic of src.
type hcase struct {
	name, src   string
	code        string // the diagnostic's code
	habits      string // "[E1205 E1211]"
	target      string // "" means no target fact
	alts        []string
	noReparse   bool // the proven fix is not expected to give a clean file
	count       int  // number of diagnostics; 0 means 1
	opts        Options
	wantNoFixes bool
}

// applyFix applies a diagnostic's proven edits to text.
func applyFix(text string, d *diag.Diagnostic) string {
	var es []diag.Edit
	for _, f := range d.Fixes {
		if f.Class == diag.Proven {
			es = append(es, f.Edits...)
		}
	}
	sort.SliceStable(es, func(i, j int) bool {
		if es[i].Span.Start != es[j].Span.Start {
			return es[i].Span.Start > es[j].Span.Start
		}
		return es[i].Span.End > es[j].Span.End
	})
	for _, e := range es {
		text = text[:e.Span.Start] + e.Text + text[e.Span.End:]
	}
	return text
}

func runHabits(t *testing.T, cases []hcase) {
	t.Helper()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			r := parse(t, c.src, c.opts)
			if c.count < 0 {
				if len(r.diags) != 0 {
					t.Errorf("want no diagnostic:\n  %s", strings.Join(r.codes(), "\n  "))
				}
				return
			}
			want := c.count
			if want == 0 {
				want = 1
			}
			if len(r.diags) != want {
				t.Fatalf("%d diagnostics, want %d:\n  %s", len(r.diags), want, strings.Join(r.codes(), "\n  "))
			}
			d := r.diags[0]
			if d.Code.String() != c.code {
				t.Errorf("code %v, want %s", d.Code, c.code)
			}
			if d.Phase != diag.PhaseParse {
				t.Errorf("phase %v, want parse", d.Phase)
			}
			if c.habits != "" && factText(d, "habits") != c.habits {
				t.Errorf("habits %s, want %s", factText(d, "habits"), c.habits)
			}
			if got := factText(d, "target"); got != c.target {
				t.Errorf("target %q, want %q", got, c.target)
			}
			if c.alts != nil && strings.Join(d.Alternatives, "|") != strings.Join(c.alts, "|") {
				t.Errorf("alternatives %q, want %q", d.Alternatives, c.alts)
			}
			if c.wantNoFixes && len(d.Fixes) > 0 {
				t.Errorf("unexpected fixes %v", d.Fixes)
			}
			if len(d.Fixes) > 0 && !c.noReparse && want == 1 {
				fixed := applyFix(r.src.Text, d)
				if again := parse(t, fixed, c.opts); len(again.diags) != 0 {
					t.Errorf("fixed text does not reparse cleanly:\n%s\n  %s", fixed, strings.Join(again.codes(), "\n  "))
				}
			}
		})
	}
}

// TestWholeTargetLine: one diagnostic per line with the whole target.
func TestWholeTargetLine(t *testing.T) {
	runHabits(t, []hcase{
		{name: "fn arrow", src: "fn add(a: int, b: int) -> int {\n    return a + b\n}\n", code: "E1205", habits: "[E1205 E1211]", target: "Add(a: int, b: int): int {"},
		{name: "push", src: "F() {\n    v.push(x);\n}\n", code: "E1206", habits: "[E1206]", target: "    v.push(x)"},
		{name: "snake", src: "def add_two(a: int) {\n}\n", code: "E1205", target: "AddTwo(a: int) {"},
	})
}

// TestOneDiagnosticPerLine: several habits on one line merge into one
// diagnostic.
func TestOneDiagnosticPerLine(t *testing.T) {
	runHabits(t, []hcase{
		{name: "let mut amp", src: "F() {\n    let mut x = &mut y;\n}\n", code: "E1207", habits: "[E1207 E1212 E1206]", target: "    var x := mut y"},
	})
}

// TestE1208_ChainTargets: a postfix ? chain becomes nested try.
func TestE1208_ChainTargets(t *testing.T) {
	runHabits(t, []hcase{
		{name: "one", src: "F() {\n    x := f(x)?\n}\n", code: "E1208", target: "    x := try f(x)"},
		{name: "two", src: "F() {\n    x := a.b()?.c()?\n}\n", code: "E1208", target: "    x := try (try a.b()).c()"},
		{name: "inner", src: "F() {\n    x := a.b()?.c()\n}\n", code: "E1208", target: "    x := (try a.b()).c()"},
		{name: "not a call", src: "F() {\n    x := y?\n}\n", code: "E1208", target: "", alts: []string{"try y", "y else { return … }"}, wantNoFixes: true},
	})
}

// TestE1206_TargetsReparse: stray semicolons, and the forms with alternatives only.
func TestE1206_TargetsReparse(t *testing.T) {
	runHabits(t, []hcase{
		{name: "brackets", src: "F() { G(a; b) }\n", code: "E1206", target: "F() { G(a, b) }"},
		{name: "end", src: "F() {\n    x := 1;\n}\n", code: "E1206", target: "    x := 1"},
		{name: "two", src: "F() {\n    a := 1; b := 2\n}\n", code: "E1206", target: "    a := 1\n    b := 2"},
		{name: "array type", src: "F(x: [u8; 4]) {\n}\n", code: "E1206", target: "", alts: []string{"[4]u8"}, wantNoFixes: true},
		{name: "c for", src: "F(n: int) {\n    for i := 0; i < n; i += 1 {\n    }\n}\n", code: "E1206", habits: "[E1206]", target: "",
			alts: []string{"for i in a..b {", "for cond {"}, wantNoFixes: true},
	})
}

// TestCStyleFor: a C-style for gets one E1206 at the first ';' with
// alternatives only, the parser skips to the '{', and the body parses as a
// normal block. The head starts like a BindCond, so the rule runs before the
// head kinds; the first case is the conformance unit
// tests/conformance/codes/E1206_c_for.mk.
func TestCStyleFor(t *testing.T) {
	cases := []struct {
		name, src, want string
		bodyStmts       int
	}{
		{name: "conformance unit", src: "Sum(n: int): int {\n    var s := 0\n    for i := 0; i < n; i += 1 { // <- E1206\n        s += i\n    }\n    return s\n}\n",
			want: "E1206@3:15 Mk has no C-style `for`", bodyStmts: 1},
		{name: "empty parts", src: "F() {\n    for ;; {\n        G()\n    }\n}\n", want: "E1206@2:9 Mk has no C-style `for`", bodyStmts: 1},
		{name: "no init", src: "F(n: int) {\n    for ; i < n; i += 1 {\n    }\n}\n", want: "E1206@2:9 Mk has no C-style `for`"},
		{name: "call in head", src: "F() {\n    for i := G(a, b); i < H(c); i += 1 {\n        X()\n        Y()\n    }\n}\n",
			want: "E1206@2:21 Mk has no C-style `for`", bodyStmts: 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := parse(t, c.src, Options{})
			if got := strings.Join(r.codes(), "\n"); got != c.want {
				t.Fatalf("diagnostics:\n  %s\nwant\n  %s", got, c.want)
			}
			d := r.diags[0]
			if strings.Join(d.Alternatives, "|") != "for i in a..b {|for cond {" || len(d.Fixes) != 0 || factText(d, "target") != "" {
				t.Errorf("alternatives %q, fixes %v, target %q", d.Alternatives, d.Fixes, factText(d, "target"))
			}
			var loop *ast.ForStmt
			ast.Inspect(r.file, func(n ast.Node) bool {
				if f, ok := n.(*ast.ForStmt); ok && loop == nil {
					loop = f
				}
				return true
			})
			if loop == nil || loop.Body == nil {
				t.Fatalf("no for statement with a body:\n%s", r.tree())
			}
			if _, ok := loop.Head.(*ast.BadExpr); !ok {
				t.Errorf("head %T, want *ast.BadExpr", loop.Head)
			}
			if len(loop.Body.Stmts) != c.bodyStmts {
				t.Errorf("body has %d statements, want %d:\n%s", len(loop.Body.Stmts), c.bodyStmts, r.tree())
			}
		})
	}
	// Not C-style: a ';' inside brackets stays semiInBrackets's, and the
	// ordinary heads are unchanged.
	for _, c := range []struct{ name, src, want string }{
		{name: "semi in call", src: "F() {\n    for x in G(a; b) {\n    }\n}\n", want: "E1206@2:17 separate with `,`, not `;`"},
		{name: "bind cond", src: "F() {\n    for i := G() {\n    }\n}\n", want: ""},
		{name: "bare", src: "F() {\n    for {\n    }\n}\n", want: ""},
		{name: "range", src: "F(n: int) {\n    for i in 0..n {\n    }\n}\n", want: ""},
		{name: "semi in body", src: "F() {\n    for c {\n        x := 1;\n    }\n}\n", want: "E1206@3:15 Mk ends statements with a newline, not `;`"},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := parse(t, c.src, Options{})
			if got := strings.Join(r.codes(), "\n"); got != c.want {
				t.Errorf("diagnostics:\n  %s\nwant\n  %s", got, c.want)
			}
		})
	}
}

// TestE1220_TargetsReparse: an inclusive range ..= becomes an exclusive one.
func TestE1220_TargetsReparse(t *testing.T) {
	runHabits(t, []hcase{
		{name: "plain", src: "F() {\n    for i in 0..=n {\n    }\n}\n", code: "E1220", target: "    for i in 0..n + 1 {"},
		{name: "loose", src: "F() {\n    for i in 0..=m.Get(k) else { return } {\n    }\n}\n", code: "E1220", target: "    for i in 0..(m.Get(k) else { return }) + 1 {"},
		{name: "slice", src: "F() {\n    x := xs[1..=n]\n}\n", code: "E1220", target: "    x := xs[1..n + 1]"},
	})
}

// TestE1214_ArmsWithCommas: match arms written with => and commas.
func TestE1214_ArmsWithCommas(t *testing.T) {
	runHabits(t, []hcase{
		{name: "arms", src: "F(x: E) { match x {\n.A => 1,\n.B => { g() },\n} }\n", code: "E1214", target: ".A { 1 }", count: 2},
	})
}

// TestMultiLineTarget: an impl block's target spans several lines.
func TestMultiLineTarget(t *testing.T) {
	runHabits(t, []hcase{
		{name: "impl", src: "impl Point {\n    fn len(&self) -> f64 { return 0 }\n    fn zero() -> int { return 0 }\n}\n", code: "E1219",
			target: "    Point.Len(&self): f64 { return 0 }\n    Point.Zero(): int { return 0 }", count: 3, noReparse: true},
	})
	r := parse(t, "impl Point {\n    fn len(&self) -> f64 { return 0 }\n}\n", Options{})
	if len(r.diags) != 2 || factText(r.diags[1], "target") != "    Point.Len(&self): f64 { return 0 }" {
		t.Errorf("member line: %v %q", r.codes(), factText(r.diags[len(r.diags)-1], "target"))
	}
}

// TestNoTargetWithoutEdit: a habit with no edit has no target.
func TestNoTargetWithoutEdit(t *testing.T) {
	runHabits(t, []hcase{
		// A try block's E1210 is a habit with alternatives and no edit.
		{name: "try block", src: "F() {\n    try {\n    }\n}\n", code: "E1210", habits: "[E1210]", target: ""},
	})
}

// TestAsCastAlternatives: an `as` cast offers Cast[T] and a conversion.
func TestAsCastAlternatives(t *testing.T) {
	runHabits(t, []hcase{
		{name: "as", src: "F() {\n    y := n as f64\n}\n", code: "E1222", target: "    y := Cast[f64](n)", alts: []string{"Cast[f64](n)", "f64(n)"}, wantNoFixes: true},
	})
}

// TestLetThenType: let bindings, only the parse-phase rewrite.
func TestLetThenType(t *testing.T) {
	runHabits(t, []hcase{
		{name: "let", src: "F() {\n    let one = 1\n}\n", code: "E1207", target: "    one := 1"},
		{name: "let typed", src: "F() {\n    let x: i32 = 1\n}\n", code: "E1207", target: "    x: i32 = 1"},
		{name: "let mut typed", src: "F() {\n    let mut x: i32 = 1\n}\n", code: "E1207", target: "    var x: i32 = 1"},
		{name: "let tuple", src: "F() {\n    let (a, b) = e\n}\n", code: "E1207", target: "    (a, b) := e"},
	})
}

// TestE1203_Lifetimes: a lifetime in a reference type is dropped.
func TestE1203_Lifetimes(t *testing.T) {
	runHabits(t, []hcase{{name: "ref", src: "F(a: &'a T) {\n}\n", code: "E1203", target: "F(a: &T) {"}})
}

// TestE1204_Async: async and await are dropped.
func TestE1204_Async(t *testing.T) {
	runHabits(t, []hcase{
		{name: "async fn", src: "async fn Get() {\n}\n", code: "E1204", habits: "[E1204 E1205]", target: "Get() {"},
		{name: "await", src: "F() {\n    x := await G()\n}\n", code: "E1204", target: "    x := G()"},
	})
}

// TestE1210_ExceptionWords: throw, panic! and try/catch.
func TestE1210_ExceptionWords(t *testing.T) {
	runHabits(t, []hcase{
		{name: "throw", src: "F() {\n    throw E\n}\n", code: "E1210", target: "", alts: []string{"fail E"}},
		{name: "panic", src: "F() {\n    panic!(\"x\")\n}\n", code: "E1210", target: ""},
		{name: "try block", src: "F() {\n    try {\n    } catch e {\n    }\n}\n", code: "E1210", count: 2},
	})
}

// TestE1213_DoubleColon: a :: path becomes a dotted one.
func TestE1213_DoubleColon(t *testing.T) {
	runHabits(t, []hcase{{name: "path", src: "F() {\n    x := std::io::Read()\n}\n", code: "E1213", target: "    x := std.io.Read()"}})
}

// TestE1215_Pub: pub becomes export.
func TestE1215_Pub(t *testing.T) {
	runHabits(t, []hcase{
		{name: "item", src: "pub struct S { }\n", code: "E1215", target: "export struct S { }"},
		{name: "crate", src: "pub(crate) F() {\n}\n", code: "E1215", target: "export F() {"},
		{name: "member", src: "struct S {\n    pub x: int\n}\n", code: "E1215", target: "    export x: int"},
	})
}

// TestE1216_Loops: while and loop become for; the words stay legal names.
func TestE1216_Loops(t *testing.T) {
	runHabits(t, []hcase{
		{name: "while", src: "F() {\n    while c {\n    }\n}\n", code: "E1216", target: "    for c {"},
		{name: "loop", src: "F() {\n    loop {\n    }\n}\n", code: "E1216", target: "    for {"},
		{name: "while let", src: "F() {\n    while let Some(x) = it.Next() {\n    }\n}\n", code: "E1216", target: "    for x := it.Next() {"},
		{name: "names stay legal", src: "F() {\n    loop := 1\n    while := loop\n    pub(x)\n    nil := 2\n}\n", code: "", count: -1},
	})
}

// TestE1217_IfLet: if let becomes a binding condition.
func TestE1217_IfLet(t *testing.T) {
	runHabits(t, []hcase{{name: "if let", src: "F() {\n    if let Some(x) = e {\n    }\n}\n", code: "E1217", target: "    if x := e {"}})
}

// TestE1221_Macros: the macro forms, phase parse.
func TestE1221_Macros(t *testing.T) {
	runHabits(t, []hcase{{name: "println", src: "F() {\n    println!(\"x\")\n}\n", code: "E1221", target: ""}})
	runCases(t, []pcase{{name: "other macro", src: "F() {\n    foo!(x)\n}\n", codes: "E0101", msg: "Mk has no macros"}})
}

// TestE0609_Closures: Mk has no closures; |x| forms are E0609.
func TestE0609_Closures(t *testing.T) {
	runHabits(t, []hcase{
		{name: "pipes", src: "F() {\n    g := |x| x + 1\n}\n", code: "E0609", target: ""},
		{name: "empty", src: "F() {\n    g := || 1\n}\n", code: "E0609"},
	})
}

// TestForeignForms: E0101 with a hint, no code of their own.
func TestForeignForms(t *testing.T) {
	runCases(t, []pcase{
		{name: "mut bind", src: "F() {\n    mut x := 1\n}\n", codes: "E0101", msg: "write `var x := e`"},
		{name: "plusplus", src: "F() {\n    x++\n}\n", codes: "E0101", msg: "x += 1"},
		{name: "minusminus prefix", src: "F() {\n    y := --x\n}\n", codes: "E0101"},
		{name: "caret", src: "F() {\n    y := ^x\n}\n", codes: "E0101", msg: "write `~x`"},
		{name: "power", src: "F() {\n    y := a ** b\n}\n", codes: "E0101", msg: "no power operator"},
		{name: "use", src: "use a::b\n", codes: "E0101", msg: "write `import a.b`"},
		{name: "switch", src: "F() {\n    switch e {\n    case 1:\n    }\n}\n", codes: "E0101", msg: "use `match`"},
		{name: "multi assign", src: "F() {\n    a, b := G()\n}\n", codes: "E0101", msg: "write `(a, b) := F()`"},
		{name: "return two", src: "F() {\n    return a, b\n}\n", codes: "E0101", msg: "return a record"},
		{name: "unary plus", src: "F() {\n    y := +x\n}\n", codes: "E0101", msg: "unary plus"},
	})
}

// TestAngleBrackets: facts.target rewrites <…> to […].
func TestAngleBrackets(t *testing.T) {
	r := parse(t, "F(a: List<int>) {\n}\n", Options{})
	if r.codeList() != "E0101" || factText(r.diags[0], "target") != "F(a: List[int]) {" {
		t.Errorf("%v %q", r.codes(), factText(r.diags[0], "target"))
	}
}
