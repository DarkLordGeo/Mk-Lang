// Why: diagnostics and their fixes are how users learn the borrow rules;
// each message, fix class and query answer here must be present and right.
// What: the TestT*_ tests (an else binding with no internal error, E1223's
// assert fix, E0609's recipe, mk explain, callers, uses, flow clause fixes,
// E0505's position, pkg.Bin's subsystem, the else default) and the
// provided-format query tests, over checkText and t1Program.
// How: checks small programs, runs mk fix, explain and query, and compares
// diagnostics, fix classes, rewritten text and JSON answers.

package driver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mk/internal/diag"
	"mk/internal/source"
	"mk/internal/types"
)

// checkText checks one file holding src and returns the session's result.
func checkText(t *testing.T, src string) Result {
	t.Helper()
	file := filepath.Join(t.TempDir(), "main.mk")
	if err := os.WriteFile(file, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := NewSession(Config{Root: file, CacheDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	return s.Check()
}

func diagLines(r Result) string {
	var b strings.Builder
	for _, e := range r.Report.Diags {
		b.WriteString(e.Diag.Code.String() + " " + e.Diag.Message + "\n")
	}
	return b.String()
}

// TestElseBindingNoICE: a handler that binds the error, uses it, and
// returns. It checks clean, since lower's elseExpr binds the error of an
// E!T value that is no call.
func TestElseBindingNoICE(t *testing.T) {
	t.Parallel()
	r := checkText(t, `error Bad

Code(r: Bad!int): int {
    v := r else e {
        _ = e
        return 1
    }
    return v
}
`)
	if len(r.Report.Diags) != 0 {
		t.Errorf("ice.mk:\n%s", diagLines(r))
	}
}

// TestAssertHabit: `assert x == y` is E1223 with the proven fix
// Assert(x == y), and mk fix applies it.
func TestAssertHabit(t *testing.T) {
	t.Parallel()
	src := `Same(x: int, y: int) {
    assert x == y
}
`
	r := checkText(t, src)
	if len(r.Report.Diags) != 1 {
		t.Fatalf("want one diagnostic:\n%s", diagLines(r))
	}
	d := r.Report.Diags[0].Diag
	if d.Code != diag.E1223 || len(d.Fixes) == 0 || d.Fixes[0].Class != diag.Proven {
		t.Fatalf("got %v %q fixes %+v", d.Code, d.Message, d.Fixes)
	}
	dir := t.TempDir()
	file := filepath.Join(dir, "main.mk")
	if err := os.WriteFile(file, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := NewSession(Config{Root: file, CacheDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	res, err := s.Fix(FixOptions{})
	if err != nil || res.Stopped != "clean" {
		t.Fatalf("mk fix: %v, stopped %q:\n%s", err, res.Stopped, diagLines(res.Final))
	}
	got, _ := os.ReadFile(file)
	if !strings.Contains(string(got), "    Assert(x == y)\n") {
		t.Errorf("after mk fix:\n%s", got)
	}
}

// TestE0609NamesRecipe: every E0609 message ends with the
// stored-callback sentence.
func TestE0609NamesRecipe(t *testing.T) {
	t.Parallel()
	r := checkText(t, `Count(xs: []i32, keep: fn(&i32): bool): int {
    var n := 0
    for x in xs {
        if keep(&x) {
            n += 1
        }
    }
    return n
}

Above(xs: []i32, limit: i32): int {
    return Count(xs, fn(x: &i32): bool { return *x > limit })
}
`)
	if len(r.Report.Diags) != 1 || r.Report.Diags[0].Diag.Code != diag.E0609 {
		t.Fatalf("want one E0609:\n%s", diagLines(r))
	}
	if m := r.Report.Diags[0].Diag.Message; !strings.HasSuffix(m, "to store a callback, store a `fn` value and its plain state; see the cookbook's stored-callback recipe") {
		t.Errorf("message %q", m)
	}
}

// TestExplainFlowWhole: a flow code's examples print whole, so E0604's
// accepted block shows the `*self into self` clause, and every flow code's
// text holds its full corpus blocks.
func TestExplainFlowWhole(t *testing.T) {
	e, _ := Explain("E0604")
	if !strings.Contains(e.Text(), "Deep.Mix(mut self) *self into self {") {
		t.Errorf("mk explain E0604:\n%s", e.Text())
	}
	for _, row := range diag.Catalogue() {
		e, _ := Explain(row.Code.String())
		if e.Phase != diag.PhaseFlow.String() {
			continue
		}
		txt := e.Text()
		for _, x := range []ExplainExample{e.Reject, e.Accept} {
			if x.At == "" {
				continue
			}
			for _, l := range strings.Split(strings.TrimRight(x.Text, "\n"), "\n") {
				if !strings.Contains(txt, "    "+l+"\n") {
					t.Errorf("%s: line %q of %s missing", e.Code, l, x.At)
					break
				}
			}
		}
	}
}

const t7Program = `import std.fs

Twice(n: int): int {
    return n * 2
}

export Main(sys: &System): WriteError!void {
    var out := sys.Stdout()
    x := Twice(2)
    try Print(mut out, "hi {x}\n", (x))
    s := "abc"
    _ = s.Len()
    _ = fs.Exists
}
`

func queryText(t *testing.T, src, q string, args ...string) *QueryAnswer {
	t.Helper()
	file := filepath.Join(t.TempDir(), "callers.mk")
	if err := os.WriteFile(file, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := NewSession(Config{Root: file, CacheDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	a, res, err := s.Query(q, args)
	if err != nil || a == nil {
		t.Fatalf("query %s: %v\n%s", q, err, diagLines(res))
	}
	return a
}

// TestCallersOfStd: callers Print answers Main, as callers Twice did, and
// a primitive's method answers too.
func TestCallersOfStd(t *testing.T) {
	t.Parallel()
	src := strings.Replace(t7Program, "    _ = fs.Exists\n", "", 1)
	src = strings.Replace(src, "import std.fs\n\n", "", 1)
	for q, want := range map[string]string{"Print": "Main  callers.mk:8", "Twice": "Main  callers.mk:7", "str.Len": "Main  callers.mk:10"} {
		a := queryText(t, src, "callers", q)
		if len(a.Answer) == 0 || a.Answer[0] != want {
			t.Errorf("callers %s: %q, want first line %q", q, a.Answer, want)
		}
	}
}

// TestQueryUses: every std item the package references, resolved, by
// its key, with each site, in the JSON tools/dogfood reads.
func TestQueryUses(t *testing.T) {
	t.Parallel()
	src := strings.Replace(t7Program, "    _ = fs.Exists\n", "", 1)
	src = strings.Replace(src, "import std.fs\n\n", "", 1)
	a := queryText(t, src, "uses")
	if a.Uses == nil {
		t.Fatal("no uses listing")
	}
	got := map[string][]string{}
	for _, u := range *a.Uses {
		got[u.Item] = u.Sites
	}
	for item, site := range map[string]string{
		"std.core.Print":         "callers.mk:8:9",
		"std.core.System":        "callers.mk:5:19",
		"std.core.System:Stdout": "callers.mk:6:20",
		"std.core.WriteError":    "callers.mk:5:28",
		"std.core.str:Len":       "callers.mk:10:11",
	} {
		if len(got[item]) != 1 || got[item][0] != site {
			t.Errorf("%s: sites %v, want [%s]", item, got[item], site)
		}
	}
	if _, ok := got["main.Twice"]; ok {
		t.Error("a package item is listed")
	}
	j := string(a.JSON())
	if !strings.Contains(j, `"uses":[{"item":"std.core.Print","sites":["callers.mk:8:9"]}`) {
		t.Errorf("JSON %s", j)
	}
	// An empty listing still has the array (tools/dogfood requires it).
	b := queryText(t, "Id(n: int): int {\n    return n\n}\n", "uses")
	if !strings.Contains(string(b.JSON()), `"uses":[]`) {
		t.Errorf("empty JSON %s", b.JSON())
	}
}

// t1Program holds the flow shapes: a method storing its parameter
// (word: str into self), a wrapper whose callee stores its parameter
// (v: &i32 into h), and a method storing its own contents (*self into
// self).
const t1Program = `struct Index {
    words: List[str]
}

Index.Add(mut self, word: str): OutOfMemory!void {
    try self.words.Push(word)
}

struct Holder {
    slot: &Cell[&i32]
}

Put(c: &Cell[&i32], v: &i32 into c) {
    c.Store(v)
}

Stash(h: &Holder, v: &i32) {
    Put(h.slot, v)
}

struct Deep {
    a: &i32
    b: mut &i32
}

Deep.Mix(mut self) {
    *self.b = self.a
}
`

// verifiedCheck is CheckVerified over one file holding src.
func verifiedCheck(t *testing.T, src string) (Result, string) {
	t.Helper()
	file := filepath.Join(t.TempDir(), "main.mk")
	if err := os.WriteFile(file, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := NewSession(Config{Root: file, CacheDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	r, err := s.CheckVerified()
	if err != nil {
		t.Fatal(err)
	}
	return r, file
}

// applyFixes applies the first fix of every diagnostic to src (offsets
// index the normalized text, which src is).
func applyFixes(r Result, src string) string {
	var eds []diag.Edit
	for _, e := range r.Report.Diags {
		if len(e.Diag.Fixes) > 0 {
			eds = append(eds, e.Diag.Fixes[0].Edits...)
		}
	}
	for i := 1; i < len(eds); i++ {
		for j := i; j > 0 && eds[j].Span.Start > eds[j-1].Span.Start; j-- {
			eds[j], eds[j-1] = eds[j-1], eds[j]
		}
	}
	for _, e := range eds {
		src = src[:e.Span.Start] + e.Text + src[e.Span.End:]
	}
	return src
}

// TestE0604PrintsClause: E0604 ends with the clause to write, holds it
// in `clause`, points `write_at` at the parameter, and its fix is that
// edit (kept by the compiles-fix recompilation); the three edits make the file check
// clean. The test also checks that the callee's signature prints with its
// into clause.
func TestE0604PrintsClause(t *testing.T) {
	t.Parallel()
	r, _ := verifiedCheck(t, t1Program)
	want := []string{"word: str into self", "v: &i32 into h", "*self into self"}
	if len(r.Report.Diags) != len(want) {
		t.Fatalf("want %d E0604:\n%s", len(want), diagLines(r))
	}
	for i, e := range r.Report.Diags {
		d := e.Diag
		if d.Code != diag.E0604 || !strings.HasSuffix(d.Message, "; write `"+want[i]+"`") || factText(d, "clause") != want[i] || len(d.Fixes) != 1 {
			t.Errorf("%d: %v %q %+v fixes %d", i, d.Code, d.Message, d.Facts, len(d.Fixes))
		}
	}
	if sig := factText(r.Report.Diags[1].Diag, "signature"); sig != "Put(c: &Cell[&i32], v: &i32 into c)" {
		t.Errorf("T3: signature %q", sig)
	}
	fixed := applyFixes(r, t1Program)
	for _, w := range []string{"word: str into self)", "v: &i32 into h)", "Deep.Mix(mut self) *self into self {"} {
		if !strings.Contains(fixed, w) {
			t.Errorf("fixed text lacks %q:\n%s", w, fixed)
		}
	}
	if r2 := checkText(t, fixed); len(r2.Report.Diags) != 0 {
		t.Errorf("after the fixes:\n%s", diagLines(r2))
	}
}

// TestWrapperInto: the wrapper Stash, whose callee Put stores v, gets
// `v: &i32 into h`. The fix is proven for a private function, so mk fix
// applies it.
func TestWrapperInto(t *testing.T) {
	t.Parallel()
	src := t1Program[strings.Index(t1Program, "struct Holder"):strings.Index(t1Program, "struct Deep")]
	r, file := verifiedCheck(t, src)
	if len(r.Report.Diags) != 1 || len(r.Report.Diags[0].Diag.Fixes) != 1 {
		t.Fatalf("want one E0604 with a fix:\n%s", diagLines(r))
	}
	f := r.Report.Diags[0].Diag.Fixes[0]
	if len(f.Edits) != 1 || f.Edits[0].Text != " into h" {
		t.Fatalf("fix %+v", f)
	}
	s, err := NewSession(Config{Root: file, CacheDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	res, err := s.Fix(FixOptions{})
	if err != nil {
		t.Fatal(err)
	}
	// The clause is proven for a private function, so one mk fix
	// run writes it and the package checks clean.
	if f.Class != diag.Proven || res.Stopped != "clean" {
		t.Errorf("class %v, mk fix stopped %q", f.Class, res.Stopped)
	}
}

// TestFiveParserMethods: five private parser methods, each passing a
// token on to the next, the last storing it into self, and a function
// calling the first. Each clause moves E0604 one call up, and one mk fix run
// writes all six and checks clean. An exported method keeps its fix
// compiles, which mk fix does not apply.
func TestFiveParserMethods(t *testing.T) {
	t.Parallel()
	src := `struct Parser {
    last: &str
}

Parser.Keep(mut self, t: &str) {
    self.last = t
}

Parser.Word(mut self, t: &str) {
    self.Keep(t)
}

Parser.Name(mut self, t: &str) {
    self.Word(t)
}

Parser.Expr(mut self, t: &str) {
    self.Name(t)
}

Parser.Stmt(mut self, t: &str) {
    self.Expr(t)
}

Run(p: mut Parser, src: &str) {
    p.Stmt(src)
}
`
	file := filepath.Join(t.TempDir(), "main.mk")
	if err := os.WriteFile(file, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := NewSession(Config{Root: file, CacheDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	res, err := s.Fix(FixOptions{})
	if err != nil || res.Stopped != "clean" || len(res.Rounds) != 6 {
		t.Fatalf("mk fix: %v %q, %d rounds", err, res.Stopped, len(res.Rounds))
	}
	got, _ := os.ReadFile(file)
	for _, m := range []string{"Keep", "Word", "Name", "Expr", "Stmt"} {
		if !strings.Contains(string(got), "Parser."+m+"(mut self, t: &str into self) {") {
			t.Errorf("%s has no clause:\n%s", m, got)
		}
	}
	if !strings.Contains(string(got), "Run(p: mut Parser, src: &str into p) {") {
		t.Errorf("Run has no clause:\n%s", got)
	}
	// Exported: compiles, offered and not applied.
	exp := strings.Replace(src, "Parser.Keep(", "export Parser.Keep(", 1)
	exp = strings.Replace(exp, "struct Parser {", "export view struct Parser {", 1)
	r, _ := verifiedCheck(t, exp)
	if len(r.Report.Diags) != 1 || len(r.Report.Diags[0].Diag.Fixes) != 1 || r.Report.Diags[0].Diag.Fixes[0].Class != diag.Compiles {
		t.Errorf("exported Keep: want E0604 with a compiles fix:\n%s", diagLines(r))
	}
}

// TestCompilesRecompiled: `try` offered as a compiles fix in Main, whose
// set lacks the call's errors, does not compile, so it becomes an
// alternative beside handling it and widening the set.
func TestCompilesRecompiled(t *testing.T) {
	t.Parallel()
	r, _ := verifiedCheck(t, `import std.fs

export Main(sys: &System): WriteError!void {
    s := fs.ReadText(sys.arena, sys.io, "x.txt")
    var out := sys.Stdout()
    try Print(mut out, "{s}", (s))
}
`)
	if len(r.Report.Diags) != 1 || r.Report.Diags[0].Diag.Code != diag.E0802 {
		t.Fatalf("want one E0802:\n%s", diagLines(r))
	}
	d := r.Report.Diags[0].Diag
	if len(d.Fixes) != 0 || len(d.Alternatives) != 3 || !strings.Contains(strings.Join(d.Alternatives, "|"), "pass the error to the caller (not checked to compile") {
		t.Errorf("fixes %+v alternatives %q", d.Fixes, d.Alternatives)
	}
	// A compiles fix that does compile stays one.
	r2, _ := verifiedCheck(t, `Main2(): OutOfMemory!int {
    return Half(4)
}

Half(n: int): OutOfMemory!int {
    return n / 2
}

Use(): OutOfMemory!void {
    _ = Half(2)
    x := Half(3)
    _ = x
}
`)
	for _, e := range r2.Report.Diags {
		if e.Diag.Code == diag.E0802 && (len(e.Diag.Fixes) != 1 || e.Diag.Fixes[0].Class != diag.Compiles) {
			t.Errorf("a try that compiles was downgraded: %+v", e.Diag)
		}
	}
	if !strings.Contains(diagLines(r2), "E0802") {
		t.Errorf("no E0802 to keep:\n%s", diagLines(r2))
	}
}

// TestE0505AtStore: the scope-end E0505 points at the statement that
// stores the loan, with the loan's creation as the note created_at, when
// the two are lines apart (here twelve lines).
func TestE0505AtStore(t *testing.T) {
	t.Parallel()
	src, err := os.ReadFile(filepath.Join("testdata", "t2_store.mk"))
	if err != nil {
		t.Fatal(err)
	}
	r := checkText(t, string(src))
	if len(r.Report.Diags) != 1 || r.Report.Diags[0].Diag.Code != diag.E0505 {
		t.Fatalf("want one E0505:\n%s", diagLines(r))
	}
	d := r.Report.Diags[0].Diag
	p := r.Sources.Position(d.At.StartPos())
	c := factAt(d, "created_at")
	if p.Line != 23 || c.File == 0 || r.Sources.Position(c.StartPos()).Line != 14 {
		t.Errorf("at line %d, created_at %+v: %s", p.Line, c, d.Message)
	}
}

func factAt(d *diag.Diagnostic, key string) source.Span {
	for _, f := range d.Facts {
		if f.Key == key {
			return f.Value.At
		}
	}
	return source.Span{}
}

// TestBinSubsystem: pkg.Bin's subsystem is "", "console" or, on
// Windows, "windows"; anything else is E0202 invalid.
func TestBinSubsystem(t *testing.T) {
	t.Parallel()
	dir := writeTree(t, map[string]string{"src/m.mk": ""})
	tree := diskTree(dir, "", os.ReadFile)
	for _, c := range []struct {
		target types.Target
		sub    string
		reason string // "" when accepted
	}{
		{types.X86_64Windows, "", ""},
		{types.X86_64Windows, "console", ""},
		{types.X86_64Linux, "console", ""},
		{types.X86_64Windows, "gui", "invalid"},
		{types.X86_64Linux, "windows", "invalid"},
		{types.X86_64Windows, "windows", ""},
	} {
		m := &Manifest{Bins: []Bin{{Name: "k", Root: "src", Subsystem: c.sub}}, binSrc: []int{0}}
		ds := validateBins(m, tree, c.target)
		switch {
		case c.reason == "" && len(ds) > 0:
			t.Errorf("%s %q: %s", c.target.Name, c.sub, ds[0].Message)
		case c.reason != "" && (len(ds) != 1 || ds[0].Code != diag.E0202 || factText(ds[0], "reason") != c.reason || factText(ds[0], "field") != "subsystem"):
			t.Errorf("%s %q: want one E0202 %s, got %d", c.target.Name, c.sub, c.reason, len(ds))
		}
	}
}

// TestCanonicalElseDefault: mk fix rewrites `x := e.Or(v)` to `x := e else { v }`
// for a v whose evaluation cannot be seen (E0207's proven fix;
// `Or(Def())`'s fix is compiles, so it is not in the program), and a second
// run changes nothing.
func TestCanonicalElseDefault(t *testing.T) {
	t.Parallel()
	src := "Find(xs: []int, k: int): ?int {\n    for x in xs {\n        if x == k {\n            return some(x)\n        }\n    }\n    return none\n}\n\nDef(): int {\n    return 7\n}\n\nPick(xs: []int): int {\n    a := Find(xs, 1).Or(0)\n    var b := Find(xs, 2).Or(-1)\n    c: int = Find(xs, 3).Or(a)\n    return a + b + c + Def()\n}\n"
	file := filepath.Join(t.TempDir(), "main.mk")
	if err := os.WriteFile(file, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	run := func() *FixResult {
		s, err := NewSession(Config{Root: file, CacheDir: t.TempDir()})
		if err != nil {
			t.Fatal(err)
		}
		res, err := s.Fix(FixOptions{Canonical: true})
		if err != nil || res.Stopped != "clean" {
			t.Fatalf("mk fix --canonical: %v %q", err, res.Stopped)
		}
		return res
	}
	res := run()
	if len(res.Rounds) == 0 || len(res.Rounds[0].Applied) != 3 {
		t.Fatalf("rounds %+v", res.Rounds)
	}
	got, _ := os.ReadFile(file)
	for _, w := range []string{"a := Find(xs, 1) else { 0 }\n", "var b := Find(xs, 2) else { -1 }\n", "c: int = Find(xs, 3) else { a }\n"} {
		if !strings.Contains(string(got), w) {
			t.Errorf("missing %q in\n%s", w, got)
		}
	}
	if res := run(); len(res.Rounds) != 0 || len(res.Files) != 0 {
		t.Errorf("second run: %+v", res.Rounds)
	}
}

// TestMembersProvidedFormat: mk query members of an error lists
// the compiler's Format impl, noted as provided, with its method.
func TestMembersProvidedFormat(t *testing.T) {
	t.Parallel()
	a := queryText(t, "error NotFound(id: i64)\n\nShow(e: NotFound): i64 {\n    return e.id\n}\n", "members", "NotFound")
	want := []string{"export id: i64", "impl Format for NotFound (provided by the compiler)", "    Format(&self, w: mut Writer): WriteError!void"}
	if strings.Join(a.Answer, "\n") != strings.Join(want, "\n") {
		t.Errorf("members NotFound: %q", a.Answer)
	}
}

// TestClauseMovesUp: a clause fix that moves the error to the caller
// is progress, so it is kept (proven for a private method; an exported
// one's fix stays compiles).
func TestClauseMovesUp(t *testing.T) {
	t.Parallel()
	r, _ := verifiedCheck(t, "struct Deep {\n    a: &i32\n    b: mut &i32\n}\n\nDeep.Mix(mut self) {\n    *self.b = self.a\n}\n\nDeep.Run(mut self) {\n    self.Mix()\n}\n")
	if len(r.Report.Diags) != 1 || len(r.Report.Diags[0].Diag.Fixes) != 1 || r.Report.Diags[0].Diag.Fixes[0].Class != diag.Proven {
		t.Errorf("want E0604 with its proven fix:\n%s", diagLines(r))
	}
}

// TestSigProvidedFormat: mk query sig E.Format answers the
// compiler's Format of an error, noted as provided; hover asks the same.
func TestSigProvidedFormat(t *testing.T) {
	t.Parallel()
	a := queryText(t, "error NotFound(id: i64)\n\nShow(e: NotFound): i64 {\n    return e.id\n}\n", "sig", "NotFound.Format")
	want := []string{"Format(&self, w: mut Writer): WriteError!void", "impl Format for NotFound (provided by the compiler)", "module main"}
	if !a.Found || strings.Join(a.Answer, "\n") != strings.Join(want, "\n") {
		t.Errorf("sig NotFound.Format: %v %q", a.Found, a.Answer)
	}
}
