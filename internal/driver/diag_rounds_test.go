// Why: diagnostics, their fixes and mk fix's rounds must be right, proven
// and idempotent, or mk fix makes programs worse.
// What: tests of the for-in and release rounds, clause fixes and their
// placement, the recompile check of fixes (grouped, bounded, explained),
// mk query callers and uses, signature text in diagnostics, and mk fix
// --canonical over the whole std.
// How: each test checks or fixes a small program (t6Source, verifyWith)
// and compares the diagnostics, fixes, rewrites and recheck counts.

package driver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mk/internal/diag"
	"mk/internal/format"
	"mk/internal/source"
	"mk/internal/types"
)

// TestCanonicalForIn_SliceImplementerLeft: a value whose type implements Slice[T]
// and has a Next iterates as a slice in `for x in`, so `var it := Make();
// for x := it.Next()` (which walks Next: 3 2 1) is not the same program as
// `for x in Make()` (1 2 3). The round leaves it; an iterator with no Slice
// impl beside it is still rewritten, in a generic body too.
func TestCanonicalForIn_SliceImplementerLeft(t *testing.T) {
	t.Parallel()
	src := `struct Rev {
    xs: [3]int
    pos: int
}

impl Slice[int] for Rev {
    Items(&self): []int {
        return self.xs[..]
    }
    ItemsMut(mut self): mut []int {
        return mut self.xs[..]
    }
}

Rev.Next(mut self): ?int {
    if self.pos >= 3 {
        return none
    }
    v := self.xs[2 - self.pos]
    self.pos += 1
    return some(v)
}

Make(): Rev {
    return Rev(xs: [1, 2, 3], pos: 0)
}

struct Count {
    n: int
}

Count.Next(mut self): ?int {
    if self.n == 0 {
        return none
    }
    self.n -= 1
    return some(self.n)
}

struct Gen[T] {
    n: int
}

Gen[T].Next(mut self): ?int {
    if self.n == 0 {
        return none
    }
    self.n -= 1
    return some(self.n)
}

MakeGen[T](): Gen[T] {
    return Gen[T](n: 2)
}

Each[T](): int {
    var total := 0
    var g := MakeGen[T]()
    for x := g.Next() {
        total += x
    }
    return total
}

Digits(): int {
    var total := 0
    var it := Make()
    for x := it.Next() {
        total = total * 10 + x
    }
    var c := Count(n: 2)
    for x := c.Next() {
        total += x
    }
    return total
}
`
	got, rounds := canonicalFix(t, src)
	if len(rounds) != 1 || rounds[0].Phase != "for-in" || len(rounds[0].Applied) != 2 {
		t.Fatalf("rounds %+v\n%s", rounds, got)
	}
	// The generic body's Gen[T] mentions T: its facts are asked in the
	// unit's environment (Facts of an open type is an ICE).
	wantLines(t, got, "    var it := Make()\n    for x := it.Next() {\n", "    for x in Count(n: 2) {\n", "    for x in MakeGen[T]() {\n")
}

// TestReleaseOpenTLeft: E0701 of `xs.Free(a)` whose T may be linear (an
// open type parameter) offers no FreeEmpty fix, since FreeEmpty panics on a
// non-empty HeapList[int] that Free would free; nor does an element type
// that mentions one (Pair[T]: Pair[int] was freed too). A closed linear
// element type still gets it, and mk fix --canonical rewrites only that one.
func TestReleaseOpenTLeft(t *testing.T) {
	t.Parallel()
	std := releaseStd(t)
	src := `export linear struct Tok {
    n: int
}

export Drop[T](a: &Alloc, xs: HeapList[T]) {
    xs.Free(a)
}

export struct Pair[T] {
    x: T
}

export DropPair[T](a: &Alloc, xs: HeapList[Pair[T]]) {
    xs.Free(a)
}

export DropTok(a: &Alloc) {
    xs := HeapList[Tok].New(a)
    xs.Free(a)
}
`
	file := filepath.Join(t.TempDir(), "main.mk")
	if err := os.WriteFile(file, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := Config{Root: file, StdDir: std, CacheDir: t.TempDir()}
	s, err := NewSession(cfg)
	if err != nil {
		t.Fatal(err)
	}
	r := s.Check()
	if countCode(r, diag.E0701) != 3 {
		t.Fatalf("want three E0701:\n%s", diagLines(r))
	}
	for _, e := range r.Report.Diags {
		d := e.Diag
		pos := r.Sources.Position(d.At.StartPos())
		hasFix := len(d.Fixes) == 1 && d.Fixes[0].Edits[0].Text == "FreeEmpty"
		if hasFix != (pos.Line == 19) {
			t.Errorf("line %d %q: FreeEmpty fix %v", pos.Line, d.Message, hasFix)
		}
	}
	s, _ = NewSession(cfg)
	res, err := s.Fix(FixOptions{Canonical: true})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(file)
	wantLines(t, string(got), "export Drop[T](a: &Alloc, xs: HeapList[T]) {\n    xs.Free(a)\n",
		"xs: HeapList[Pair[T]]) {\n    xs.Free(a)\n", "    xs.FreeEmpty(a)\n")
	if countCode(res.Final, diag.E0701) != 2 {
		t.Errorf("final:\n%s", diagLines(res.Final))
	}
}

// TestReleaseTwoSets: both Sets of one body are rewritten, the second
// with fresh names, no E0701 is left, a second run changes nothing, and the
// text is a fixed point of mk fmt. A body that already names old gets old2.
// Put2 itself cannot check clean: r is moved and the second call can still
// fail (E0411), which its two E0701 hid (an earlier phase) and which no
// release rewrite changes; Put3 checks clean.
func TestReleaseTwoSets(t *testing.T) {
	t.Parallel()
	std := releaseStd(t)
	src := `export linear struct Res {
    n: int
}

export Res.Free(self, a: &Alloc) {
    Res(n) := self
    _ = n
}

export Put2(a: &Alloc, m: mut HeapMap[int, Res], r: Res, q: Res): OutOfMemory!void {
    try m.Set(a, 1, r)
    try m.Set(a, 2, q)
}

export Put3(a: &Alloc, m: mut HeapMap[int, Res], r: Res, old: int): OutOfMemory!void {
    try m.Set(a, old, r)
}
`
	file := filepath.Join(t.TempDir(), "main.mk")
	if err := os.WriteFile(file, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := Config{Root: file, StdDir: std, CacheDir: t.TempDir()}
	run := func() *FixResult {
		s, err := NewSession(cfg)
		if err != nil {
			t.Fatal(err)
		}
		res, err := s.Fix(FixOptions{Canonical: true})
		if err != nil {
			t.Fatal(err)
		}
		r := res.Final.Report.Diags
		if len(r) != 1 || r[0].Diag.Code != diag.E0411 || res.Final.Sources.Position(r[0].Diag.At.StartPos()).Line != 11 {
			t.Fatalf("mk fix --canonical: %q, want only Put2's E0411 at r:\n%s", res.Stopped, diagLines(res.Final))
		}
		return res
	}
	res := run()
	got, _ := os.ReadFile(file)
	release := 0
	for _, r := range res.Rounds {
		if r.Phase == releasePhase {
			release += len(r.Applied)
		}
	}
	if release != 3 {
		t.Errorf("release rewrites %d, want 3: %+v", release, res.Rounds)
	}
	wantLines(t, string(got),
		"    old := try m.Replace(a, 1, r)\n    if o := old {\n        o.Free(a)\n    }\n",
		"    old2 := try m.Replace(a, 2, q)\n    if o2 := old2 {\n        o2.Free(a)\n    }\n",
		"    old2 := try m.Replace(a, old, r)\n    if o2 := old2 {\n        o2.Free(a)\n    }\n")
	if again := run(); len(again.Rounds) != 0 {
		t.Errorf("second run rewrote again: %+v\n%s", again.Rounds, got)
	}
	if out, err := format.Source("main.mk", got); err != nil || string(out) != string(got) {
		t.Errorf("not a fixed point of mk fmt (%v):\n%s", err, got)
	}
}

// TestImplMethodClauseNotProven: the clause E0604 asks for on an impl
// method makes its flows differ from the interface method's (E0608), so the
// fix is not proven: compiles, and the recompile check downgrades it. mk fix
// applies nothing.
func TestImplMethodClauseNotProven(t *testing.T) {
	t.Parallel()
	src := `interface Adder {
    Add(mut self, word: str): OutOfMemory!void
}

struct Index {
    words: List[str]
}

impl Adder for Index {
    Add(mut self, word: str): OutOfMemory!void {
        try self.words.Push(word)
    }
}
`
	r := checkText(t, src)
	if len(r.Report.Diags) != 1 || r.Report.Diags[0].Diag.Code != diag.E0604 {
		t.Fatalf("want one E0604:\n%s", diagLines(r))
	}
	for _, f := range r.Report.Diags[0].Diag.Fixes {
		if f.Class == diag.Proven {
			t.Errorf("the impl method's clause is offered as proven: %+v", f)
		}
	}
	v, file := verifiedCheck(t, src)
	if d := v.Report.Diags[0].Diag; len(d.Fixes) != 0 || !strings.Contains(strings.Join(d.Alternatives, "|"), "word: str into self") {
		t.Errorf("after T6: fixes %+v alternatives %q", d.Fixes, d.Alternatives)
	}
	s, err := NewSession(Config{Root: file, CacheDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	res, err := s.Fix(FixOptions{DryRun: true})
	if err != nil || len(res.Rounds) != 0 {
		t.Errorf("mk fix applied a clause: %v %+v", err, res.Rounds)
	}
}

// TestClauseBeforeCpu (in a signature, flows come before Cpu and
// Section): `*self into self` goes before a cpu clause, and the result
// checks clean; a result that is a fn type gets no edit, since a clause
// after it would be the fn type's.
func TestClauseBeforeCpu(t *testing.T) {
	t.Parallel()
	src := `struct Deep {
    a: &i32
    b: mut &i32
}

Deep.Mix(mut self) cpu "avx2" {
    *self.b = self.a
}

Id(x: int): int {
    return x
}

Deep.Get(mut self): fn(int): int {
    *self.b = self.a
    return Id
}
`
	r := checkText(t, src)
	if len(r.Report.Diags) != 2 {
		t.Fatalf("want two E0604:\n%s", diagLines(r))
	}
	if d := r.Report.Diags[1].Diag; len(d.Fixes) != 0 || factText(d, "clause") != "*self into self" {
		t.Errorf("fn-typed result: fixes %+v clause %q", d.Fixes, factText(d, "clause"))
	}
	fixed := applyFixes(r, src)
	wantLines(t, fixed, "Deep.Mix(mut self) *self into self cpu \"avx2\" {\n")
	r2 := checkText(t, fixed)
	if len(r2.Report.Diags) != 1 || !strings.Contains(r2.Report.Diags[0].Diag.Message, "self into self") {
		t.Errorf("after the fix:\n%s", diagLines(r2))
	}
}

// TestTestDirWithoutMainVerified: mk test of a directory
// with no Main rechecks each compiles fix as mk test checks (no Main
// needed), so the `try` that cannot compile in `Run(): int` is downgraded,
// not kept because both rechecks stopped at E0201.
func TestTestDirWithoutMainVerified(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	src := `error Bad

May(x: int): Bad!int {
    if x < 0 {
        fail Bad
    }
    return x
}

Run(x: int): int {
    v := May(x)
    return v
}

test "run" {
    Assert(Run(1) == 1)
}
`
	if err := os.WriteFile(filepath.Join(dir, "lib.mk"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := NewSession(Config{Root: dir, CacheDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	r, rep, err := s.Test(TestOptions{})
	if err != nil || rep != nil {
		t.Fatalf("mk test: %v report %v", err, rep)
	}
	if len(r.Report.Diags) != 1 || r.Report.Diags[0].Diag.Code != diag.E0802 {
		t.Fatalf("want one E0802:\n%s", diagLines(r))
	}
	d := r.Report.Diags[0].Diag
	for _, f := range d.Fixes {
		if f.Class == diag.Compiles {
			t.Errorf("a try that does not compile is offered as compiles: %+v", f)
		}
	}
	if !strings.Contains(strings.Join(d.Alternatives, "|"), "pass the error to the caller (not checked to compile: recompiled") {
		t.Errorf("alternatives %q", d.Alternatives)
	}
}

// TestCallersSkipsFnValues (`callers F` lists direct callers): passing
// Inc as a fn value is no call of it.
func TestCallersSkipsFnValues(t *testing.T) {
	t.Parallel()
	src := `Inc(x: int): int {
    return x + 1
}

Twice(f: fn(int): int, x: int): int {
    return f(f(x))
}

Calls(): int {
    return Inc(1)
}

export Main(sys: &System): WriteError!void {
    var out := sys.Stdout()
    n := Twice(Inc, 1) + Calls()
    try Print(mut out, "{n}\n", (n))
}
`
	a := queryText(t, src, "callers", "Inc")
	if len(a.Answer) != 2 || a.Answer[0] != "Calls  callers.mk:10" || !strings.Contains(a.Answer[1], "through fn values") {
		t.Errorf("callers Inc: %q", a.Answer)
	}
	if b := queryText(t, src, "callers", "Calls"); len(b.Answer) == 0 || b.Answer[0] != "Main  callers.mk:15" {
		t.Errorf("callers Calls: %q", b.Answer)
	}
}

// TestQueryUsesForIn: the Next a `for x in it` loop calls is a use at
// the loop's source, as an explicit it.Next() is.
func TestQueryUsesForIn(t *testing.T) {
	t.Parallel()
	src := `Total(m: &Map[str, int]): int {
    var n := 0
    for (key, value) in m.Iter() {
        _ = key
        n += *value
    }
    return n
}

Count(text: str): int {
    var n := 0
    for l in text.Lines() {
        _ = l
        n += 1
    }
    return n
}

Count2(text: str): int {
    var n := 0
    var it := text.Lines()
    for l := it.Next() {
        _ = l
        n += 1
    }
    return n
}
`
	a := queryText(t, src, "uses")
	if a.Uses == nil {
		t.Fatal("no uses listing")
	}
	got := map[string][]string{}
	for _, u := range *a.Uses {
		got[u.Item] = u.Sites
	}
	for item, want := range map[string]string{
		"std.core.Lines:Next":   "callers.mk:12:14,callers.mk:22:17",
		"std.core.MapIter:Next": "callers.mk:3:25",
	} {
		if g := strings.Join(got[item], ","); g != want {
			t.Errorf("%s: sites %s, want %s", item, g, want)
		}
	}
}

// t6Source is n functions each with a valid `try` fix (E0802) and bad ones
// whose `try` cannot compile in a function that cannot fail.
func t6Source(n, bad int) string {
	var b strings.Builder
	b.WriteString("error Bad\n\nMay(x: int): Bad!int {\n    if x < 0 {\n        fail Bad\n    }\n    return x\n}\n")
	for i := range n {
		b.WriteString("\nRun" + itoaQ(i) + "(x: int): Bad!int {\n    v := May(x)\n    return v\n}\n")
	}
	for i := range bad {
		b.WriteString("\nNo" + itoaQ(i) + "(x: int): int {\n    v := May(x)\n    return v\n}\n")
	}
	return b.String()
}

// verifyWith checks src as one file and verifies its compiles fixes with
// the given recheck budget, after edit changes the report (when not nil);
// it returns the report and the rechecks made.
func verifyWith(t *testing.T, src string, budget int, edit func(Result)) (Result, int) {
	t.Helper()
	file := filepath.Join(t.TempDir(), "main.mk")
	if err := os.WriteFile(file, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := NewSession(Config{Root: file, CacheDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	ov := &overlay{files: map[string]string{}, base: s.cfg.ReadFile}
	cfg := s.cfg
	cfg.ReadFile = ov.read
	ns, err := s.child(cfg)
	if err != nil {
		t.Fatal(err)
	}
	res := ns.Check()
	if edit != nil {
		edit(res)
	}
	n := ns.verifyFixes(res, ov, cfg, nil, budget, false)
	return res, n
}

// TestGroupedRechecks (the edit-to-diagnostic budget): seventeen valid
// `try` fixes are verified by one recheck after the base, all kept as
// compiles; with two bad ones among them, the bad are downgraded by halving
// and the good still kept, in far fewer rechecks than one per fix.
func TestGroupedRechecks(t *testing.T) {
	t.Parallel()
	r, n := verifyWith(t, t6Source(17, 0), maxVerify, nil)
	if n != 1 {
		t.Errorf("17 valid fixes took %d rechecks, want 1", n)
	}
	if got := countCompiles(r); got != 17 || countCode(r, diag.E0802) != 17 {
		t.Errorf("compiles fixes kept %d of 17:\n%s", got, diagLines(r))
	}
	r, n = verifyWith(t, t6Source(14, 2), maxVerify, nil)
	if got := countCompiles(r); got != 14 {
		t.Errorf("compiles fixes kept %d, want 14", got)
	}
	if n > 9 {
		t.Errorf("14 good and 2 bad fixes took %d rechecks", n)
	}
	for _, e := range r.Report.Diags {
		if pos := r.Sources.Position(e.Diag.At.StartPos()); len(e.Diag.Fixes) == 0 &&
			!strings.Contains(strings.Join(e.Diag.Alternatives, "|"), "(not checked to compile: recompiled, it did not improve the report)") {
			t.Errorf("line %d: alternatives %q", pos.Line, e.Diag.Alternatives)
		}
	}
}

// TestGroupedNeverWorse: group rechecks have their own cap, so a report
// whose good and bad fixes alternate still has every one of its first 16
// fixes recompiled alone if need be, as with no grouping: 8 good kept, 8
// bad downgraded as recompiled.
func TestGroupedNeverWorse(t *testing.T) {
	t.Parallel()
	var b strings.Builder
	b.WriteString("error Bad\n\nMay(x: int): Bad!int {\n    if x < 0 {\n        fail Bad\n    }\n    return x\n}\n")
	for i := range 8 {
		b.WriteString("\nRun" + itoaQ(i) + "(x: int): Bad!int {\n    v := May(x)\n    return v\n}\n")
		b.WriteString("\nNo" + itoaQ(i) + "(x: int): int {\n    v := May(x)\n    return v\n}\n")
	}
	r, n := verifyWith(t, b.String(), maxVerify, nil)
	if got := countCompiles(r); got != 8 {
		t.Errorf("compiles fixes kept %d, want 8", got)
	}
	if n > maxVerify+maxVerify/2 {
		t.Errorf("%d rechecks, over the bound of %d alone and %d grouped", n, maxVerify, maxVerify/2)
	}
	bad := 0
	for _, e := range r.Report.Diags {
		if strings.Contains(strings.Join(e.Diag.Alternatives, "|"), "(not checked to compile: recompiled, it did not improve the report)") {
			bad++
		}
	}
	if bad != 8 {
		t.Errorf("%d downgraded as recompiled, want 8:\n%s", bad, diagLines(r))
	}
}

func countCompiles(r Result) int {
	n := 0
	for _, e := range r.Report.Diags {
		for _, f := range e.Diag.Fixes {
			if f.Class == diag.Compiles {
				n++
			}
		}
	}
	return n
}

// TestUnverifiedSaysWhy: a fix that was never recompiled
// says so, past the bound or with an edit outside one package file, and
// never claims a recompile.
func TestUnverifiedSaysWhy(t *testing.T) {
	t.Parallel()
	r, n := verifyWith(t, t6Source(3, 0), 0, func(res Result) {
		d := res.Report.Diags[0].Diag
		d.Fix(diag.Fix{Class: diag.Compiles, Edits: []diag.Edit{{Span: source.Span{File: source.NoFile}, Text: "x"}}, Why: "elsewhere"})
	})
	if n != 0 || countCompiles(r) != 0 {
		t.Fatalf("budget 0: %d rechecks, %d compiles fixes", n, countCompiles(r))
	}
	for i, e := range r.Report.Diags {
		alts := strings.Join(e.Diag.Alternatives, "|")
		if strings.Contains(alts, "recompiled, it did not") || !strings.Contains(alts, "pass the error to the caller (not checked to compile: not recompiled, over the bound of 0 rechecks per report)") {
			t.Errorf("%d: %q", i, alts)
		}
		if i == 0 && !strings.Contains(alts, "elsewhere (not checked to compile: not recompiled, its edits are not in one file of the package)") {
			t.Errorf("edit outside the package: %q", alts)
		}
	}
}

// TestSignatureClauses: a signature in a diagnostic prints its clauses as
// the grammar's Flow production spells them: `from p.f, q`, `*h into c` and
// `ends self`, through borrow's flowClauses (E0602's own signature, E0604's
// and E0501's callee at a call) and check's flowText (E0304's callee).
func TestSignatureClauses(t *testing.T) {
	t.Parallel()
	src := `struct Pair {
    f: &i32
    g: &i32
}

struct Holder {
    v: &i32
}

Pick(a: &i32, b: &i32): &i32 from a {
    return b
}

First(p: &Pair, q: &i32): &i32 from p.f {
    return q
}

Two(p: &i32, q: &i32, r: &i32): &i32 from p, q {
    return r
}

Keep(h: &Holder, c: &Cell[&i32]) *h into c {
    c.Store(h.v)
}

Use(h: &Holder, c: &Cell[&i32]) {
    Keep(h, c)
}

Again(a: mut Arena): OutOfMemory!str {
    s := try a.Dup("x")
    a.Reset()
    return s
}
`
	want := []string{
		"E0602 Pick(a: &i32, b: &i32): &i32 from a",
		"E0602 First(p: &Pair, q: &i32): &i32 from p.f",
		"E0602 Two(p: &i32, q: &i32, r: &i32): &i32 from p, q",
		"E0604 Keep(h: &Holder, c: &Cell[&i32]) *h into c",
		"E0501 Reset(mut self) ends self",
	}
	sigs := func(r Result) []string {
		var out []string
		for _, e := range r.Report.Diags {
			if sig := factText(e.Diag, "signature"); sig != "" {
				out = append(out, e.Diag.Code.String()+" "+sig)
			}
		}
		return out
	}
	if got := sigs(checkText(t, src)); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("borrow signatures:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	calls := `struct Pair {
    f: &i32
    g: &i32
}

First(p: &Pair, q: &i32): &i32 from p.f, q {
    return q
}

Keep(h: &Pair, c: &Cell[&i32]) *h into c {
    c.Store(h.f)
}

Use(p: &Pair, a: mut Arena) {
    _ = First(p)
    Keep(p)
    a.Reset(1)
}
`
	want = []string{
		"E0304 First(p: &Pair, q: &i32): &i32 from p.f, q",
		"E0304 Keep(h: &Pair, c: &Cell[&i32]) *h into c",
		"E0304 Reset(mut self) ends self",
	}
	if got := sigs(checkText(t, calls)); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("check signatures:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// TestCanonicalIdempotentOnStd (each round is idempotent on std): on a copy of
// the whole std, for the Windows and the Linux target (a check only: nothing
// is built or run, so no wsl.exe), mk fix --canonical --std=std reaches
// clean, a second run with the same cache rewrites nothing, and every file
// the first run rewrote is a fixed point of mk fmt unless its text before
// the run was not one either. The first run's rewrites are logged: std is
// meant to be a fixed point of the style rounds.
func TestCanonicalIdempotentOnStd(t *testing.T) {
	if testing.Short() {
		t.Skip("-short: the whole std is fixed twice per target")
	}
	t.Parallel()
	for _, tg := range []types.Target{types.X86_64Windows, types.X86_64Linux} {
		t.Run(tg.Name, func(t *testing.T) {
			t.Parallel()
			orig := map[string][]byte{}
			dir := copyStd(t, nil, func(p string, b []byte) []byte {
				orig[p] = b
				return b
			})
			cache := t.TempDir()
			run := func() *FixResult {
				s, err := NewSession(Config{StdTests: "std", StdDir: dir, CacheDir: cache, Target: tg})
				if err != nil {
					t.Fatal(err)
				}
				res, err := s.Fix(FixOptions{Canonical: true})
				if err != nil {
					t.Fatal(err)
				}
				if res.Stopped != "clean" {
					t.Fatalf("mk fix --canonical --std=std: %q\n%s", res.Stopped, diagLines(res.Final))
				}
				return res
			}
			first := run()
			sites := 0
			for _, r := range first.Rounds {
				sites += len(r.Applied)
			}
			t.Logf("first run: %d rounds, %d rewrites in %d files", len(first.Rounds), sites, len(first.Files))
			if second := run(); len(second.Rounds) != 0 {
				t.Errorf("second run rewrote again: %+v", second.Rounds)
			}
			for _, f := range first.Files {
				rel, err := filepath.Rel(dir, f)
				if err != nil {
					t.Fatal(err)
				}
				rel = filepath.ToSlash(rel)
				got, err := os.ReadFile(f)
				if err != nil {
					t.Fatal(err)
				}
				if out, err := format.Source(rel, got); err == nil && string(out) == string(got) {
					continue
				}
				if was, err := format.Source(rel, orig[rel]); err != nil || string(was) != string(orig[rel]) {
					t.Logf("%s: not a fixed point of mk fmt, nor was it before the run", rel)
					continue
				}
				t.Errorf("%s: the rewrite is not a fixed point of mk fmt", rel)
			}
		})
	}
}
