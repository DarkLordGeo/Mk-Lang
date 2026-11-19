// Why: defects found by writing real programs in Mk must stay fixed.
// What: regression tests, each named after the dogfood defect it covers:
// gen aliases, narrowing of mut parameters, the removed Or, fn literal
// flows, reslice fixes, the run-directory sweep, eval's impl lookups and
// mk query sig of a universe function.
// How: each test checks, builds or runs a small program and compares its
// diagnostics, output or files.

package driver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mk/internal/types"
)

// shapeDyn is a `gen dyn.Of(Shape)` program whose method result is
// written res, returning ret, and a function that uses ShapeDyn.
func shapeDyn(decls, res, ret string) string {
	return "import std.dyn\n\n" + decls + "interface Shape {\n    Area(&self): " + res + "\n}\n\nstruct Square {\n    side: f64\n}\n\n" +
		"impl Shape for Square {\n    Area(&self): " + res + " {\n        return " + ret + "\n    }\n}\n\ngen dyn.Of(Shape)\n\n" +
		"Use() {\n    var s := Square(side: 2.0)\n    var d := ShapeDyn.Of(mut s)\n    _ = d.Area()\n}\n"
}

// TestGenAliasErrorSet: an interface method whose error set is
// a `type` alias of this module (`type Failure = Bad`) reaches the
// generator with its result, so `gen dyn.Of` declares ShapeDyn; the stage-4
// view followed only another module's alias, and the run ended in silent
// poison (E0201 at each use, nothing at the gen line).
func TestGenAliasErrorSet(t *testing.T) {
	t.Parallel()
	const body = "error Bad\n\ntype Failure = Bad\n\nMust(): Failure!f64 {\n    return 1.0\n}\n\n"
	src := strings.Replace(shapeDyn(body, "Failure!f64", "try Must()"), "_ = d.Area()", "_ = d.Area() else { 0.0 }", 1)
	if r := checkText(t, src); len(r.Report.Diags) != 0 {
		t.Errorf("want clean:\n%s", diagLines(r))
	}
}

// TestPoisonedGenNoted: when a gen line's run ends in poison
// because it read a type the stage-4 view cannot translate (a computed
// array length), the E0201 at a use of a name it would have declared points
// at the gen line, so the reader is not left hunting.
func TestPoisonedGenNoted(t *testing.T) {
	t.Parallel()
	r := checkText(t, shapeDyn("Must(): [2 * 2]u8 {\n    return [0; 4]\n}\n\n", "[2 * 2]u8", "Must()"))
	if len(r.Report.Diags) == 0 {
		t.Fatal("want E0201 at the use of ShapeDyn")
	}
	d := r.Report.Diags[0].Diag
	ok := false
	for _, f := range d.Facts {
		ok = ok || f.Key == "gen_line"
	}
	if d.Code.String() != "E0201" || !ok {
		t.Errorf("want E0201 with a gen_line fact:\n%s", diagLines(r))
	}
}

// TestNarrowMutRefParam: inside `if T is mut i32`, a `mut T` parameter
// is narrowed through a reborrow, so `**b = **a` lowers (it was the ICE
// "lower: deref of T") and writes the caller's referent; a probe that
// stores a short-lived loan through it is still rejected (E0505).
func TestNarrowMutRefParam(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	src := "G[T](a: mut T, b: mut T) {\n    if T is mut i32 {\n        **b = **a\n    }\n}\n\n" +
		"Use(x: mut i32, y: mut i32) {\n    var p := x\n    var q := y\n    G(mut p, mut q)\n}\n\n" +
		"export Main(sys: &System) {\n    var m: i32 = 3\n    var n: i32 = 4\n    Use(mut m, mut n)\n    sys.Print(\"{n}\\n\", (n)) else {}\n}\n"
	if err := os.WriteFile(filepath.Join(dir, "main.mk"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	if out := buildRun(t, dir, t.TempDir()); out != "3\n" {
		t.Errorf("got %q, want %q", out, "3\n")
	}
	probe := "struct Two {\n    a: mut &i32\n    b: mut &i32\n}\n\nG[T](a: mut T, b: mut T) *a into b {\n    if T is mut &i32 {\n        **b = **a\n    }\n}\n\n" +
		"Two.Mix(mut self) *self into self {\n    G(mut self.a, mut self.b)\n}\n\n" +
		"Inner(x: mut &i32, y: mut &i32) {\n    z: i32 = 7\n    *x = &z\n    var t := Two(a: x, b: y)\n    t.Mix()\n}\n"
	r := checkText(t, probe)
	codes := ""
	for _, e := range r.Report.Diags {
		codes += e.Diag.Code.String() + " "
	}
	if !strings.Contains(codes, "E0505 ") {
		t.Errorf("probe: want E0505:\n%s", diagLines(r))
	}
}

// TestOrIsE0207: `Or` on `?T` and `E!T` is removed: each call is E0207,
// whose fix writes `e else { x }`, parenthesised where the call is an
// operand, proven for a quiet default; the fixed program checks. A user
// method named `Or` is not that method.
func TestOrIsE0207(t *testing.T) {
	t.Parallel()
	src := "error Bad\n\nF(o: ?int): int {\n    return o.Or(3)\n}\n\nG(o: ?int): int {\n    x := o.Or(1) + o.Or(-2)\n    return x\n}\n\n" +
		"H(): Bad!int {\n    return 1\n}\n\nK(): int {\n    return H().Or(0)\n}\n\nstruct P {\n    n: int\n}\n\nP.Or(&self): int {\n    return self.n\n}\n\nU(p: &P): int {\n    return p.Or()\n}\n"
	want := "error Bad\n\nF(o: ?int): int {\n    return o else { 3 }\n}\n\nG(o: ?int): int {\n    x := (o else { 1 }) + (o else { -2 })\n    return x\n}\n\n" +
		"H(): Bad!int {\n    return 1\n}\n\nK(): int {\n    return H() else { 0 }\n}\n\nstruct P {\n    n: int\n}\n\nP.Or(&self): int {\n    return self.n\n}\n\nU(p: &P): int {\n    return p.Or()\n}\n"
	r := checkText(t, src)
	if len(r.Report.Diags) != 4 {
		t.Fatalf("want four E0207:\n%s", diagLines(r))
	}
	type edit struct {
		start, end int
		text       string
	}
	var eds []edit
	for _, e := range r.Report.Diags {
		d := e.Diag
		if d.Code.String() != "E0207" || len(d.Fixes) != 1 || d.Fixes[0].Class.String() != "proven" {
			t.Fatalf("got %v %q fixes %v", d.Code, d.Message, d.Fixes)
		}
		for _, ed := range d.Fixes[0].Edits {
			eds = append(eds, edit{int(ed.Span.Start), int(ed.Span.End), ed.Text})
		}
	}
	got := src
	for i := len(eds) - 1; i >= 0; i-- { // the edits come in file order
		got = got[:eds[i].start] + eds[i].text + got[eds[i].end:]
	}
	if got != want {
		t.Fatalf("fixed:\n%s\nwant:\n%s", got, want)
	}
	if r2 := checkText(t, got); len(r2.Report.Diags) != 0 {
		t.Errorf("the fixed program does not check:\n%s", diagLines(r2))
	}
}

// TestLiteralTakesExpectedFlows: a fn literal written where
// a fn type is expected takes that type's flows: the literal that
// pushes an arena copy into `state` checks under the parameter's
// `*state into state`, and a store beyond the type's flows (the word
// itself, a loan of `word`) is still E0604 at the store.
func TestLiteralTakesExpectedFlows(t *testing.T) {
	t.Parallel()
	const head = "struct Names {\n    arena: &Arena\n    all: List[str]\n}\n\n" +
		"Each[S](state: mut S, const step: (fn(state: mut S, word: str): OutOfMemory!void *state into state)): OutOfMemory!void *state into state {\n    try step(state, \"one\")\n}\n\n"
	ok := checkText(t, head+"Run(a: &Arena): OutOfMemory!int {\n    var names := Names(arena: a, all: List[str].New(a))\n    try Each(mut names, fn(state: mut Names, word: str): OutOfMemory!void {\n        try state.all.Push(try state.arena.Dup(word))\n    })\n    return names.all.Len()\n}\n")
	if len(ok.Report.Diags) != 0 {
		t.Errorf("want clean:\n%s", diagLines(ok))
	}
	bad := checkText(t, head+"Run(a: &Arena): OutOfMemory!int {\n    var names := Names(arena: a, all: List[str].New(a))\n    try Each(mut names, fn(state: mut Names, word: str): OutOfMemory!void {\n        try state.all.Push(word)\n    })\n    return names.all.Len()\n}\n")
	if len(bad.Report.Diags) != 1 || bad.Report.Diags[0].Diag.Code.String() != "E0604" {
		t.Errorf("want one E0604 for the store of `word`:\n%s", diagLines(bad))
	}
	// A literal inside a literal takes its flows from the outer literal's
	// check, so it runs in a later phase (stages.go bodies).
	nested := checkText(t, head+"Run(a: &Arena): OutOfMemory!int {\n    var names := Names(arena: a, all: List[str].New(a))\n    try Each(mut names, fn(state: mut Names, word: str): OutOfMemory!void {\n        try Each(state, fn(inner: mut Names, w: str): OutOfMemory!void {\n            try inner.all.Push(try inner.arena.Dup(w))\n        })\n        try state.all.Push(try state.arena.Dup(word))\n    })\n    return names.all.Len()\n}\n")
	if len(nested.Report.Diags) != 0 {
		t.Errorf("nested literal: want clean:\n%s", diagLines(nested))
	}
}

// TestResliceFix: where a `mut []T` meets a `[]T`, E0301
// offers `xs[..]` (there is no implicit conversion), and the
// edited program checks.
func TestResliceFix(t *testing.T) {
	t.Parallel()
	const sum = "Sum(xs: []int): int {\n    return xs.Len()\n}\n\n"
	r := checkText(t, sum+"Twice(xs: mut []int): int {\n    return Sum(xs)\n}\n")
	if len(r.Report.Diags) != 1 {
		t.Fatalf("want one E0301:\n%s", diagLines(r))
	}
	d := r.Report.Diags[0].Diag
	if d.Code.String() != "E0301" || len(d.Fixes) != 1 || len(d.Fixes[0].Edits) != 1 || d.Fixes[0].Edits[0].Text != "[..]" {
		t.Fatalf("got %v %q fixes %+v", d.Code, d.Message, d.Fixes)
	}
	if ok := checkText(t, sum+"Twice(xs: mut []int): int {\n    return Sum(xs[..])\n}\n"); len(ok.Report.Diags) != 0 {
		t.Errorf("the fixed program does not check:\n%s", diagLines(ok))
	}
}

// TestRunDirSweep: a `mk-run-*` directory a killed run left (older than a
// day) is removed by the next run's sweep, so killed runs do not fill the
// temp directory; a fresh one and other names stay.
func TestRunDirSweep(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	for _, n := range []string{"mk-run-old", "mk-run-new", "other-old"} {
		if err := os.Mkdir(filepath.Join(tmp, n), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Now().Add(-48 * time.Hour)
	for _, n := range []string{"mk-run-old", "other-old"} {
		if err := os.Chtimes(filepath.Join(tmp, n), old, old); err != nil {
			t.Fatal(err)
		}
	}
	sweepStaleRunDirs(tmp, time.Now().Add(-staleRunAge))
	for n, want := range map[string]bool{"mk-run-old": false, "mk-run-new": true, "other-old": true} {
		if _, err := os.Stat(filepath.Join(tmp, n)); (err == nil) != want {
			t.Errorf("%s: present %v, want %v", n, err == nil, want)
		}
	}
}

// TestIncEvalRecordsFreeLookup: the instance body eval gets for a
// generic that folds `raw.HasFree[T]()` carries the impl lookup mono made:
// {ReadNoImpl, std.core.Free, S} for a struct S with no `impl Free`, so
// adding one invalidates a cached evaluation through it. The const G
// evaluates a non-generic body that calls HasFree directly, which eval
// resolves through the Program's FreeImpl (it was an internal error).
func TestIncEvalRecordsFreeLookup(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	man := "const Package = pkg.Package(\n    name: \"k\",\n    bins: [pkg.Bin(name: \"k\", root: \"src\", link_script: \"\", cpu: \"\", subsystem: \"console\")],\n    deps: [],\n    c_sources: [],\n    c_imports: [],\n    asm_sources: [],\n    targets: [],\n    unsafe_modules: [\"src\"],\n    links: [],\n)\n"
	src := "import std.raw\n\nstruct S {\n    n: int\n}\n\nFrees[T](): bool {\n    unsafe \"probe\" {\n        return raw.HasFree[T]()\n    }\n}\n\nconst F: bool = Frees[S]()\n\nDirect(): bool {\n    unsafe \"probe\" {\n        return raw.HasFree[S]()\n    }\n}\n\nconst G: bool = Direct()\n\nexport Main(sys: &System) {\n}\n"
	if err := os.MkdirAll(filepath.Join(dir, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pkg.mk"), []byte(man), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "src", "main.mk"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := NewSession(Config{Root: dir, CacheDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if r := s.Check(); len(r.Report.Diags) != 0 {
		t.Fatalf("want clean:\n%s", diagLines(r))
	}
	w := s.w
	var fn, sid types.ItemID
	for _, mp := range []string{"k", "k/src", "src", "k.src"} {
		if id, ok := w.items.ByKey(types.ItemKey(mp, "Frees")); ok {
			fn = id
			sid, _ = w.items.ByKey(types.ItemKey(mp, "S"))
		}
	}
	if fn == types.NoItem || sid == types.NoItem {
		t.Fatal("no Frees or S")
	}
	b := (&program{w: w, s: w.sem5}).Body(fn, []types.GenericArg{types.TypeArg(w.tab.Named(sid, nil))})
	if b.MIR == nil {
		t.Fatalf("no instance body: state %d", b.State)
	}
	found := false
	for _, r := range b.Reads {
		if r.Kind == types.ReadNoImpl && r.Key.String() == "std.core.Free" && strings.HasSuffix(r.Name, "S") {
			found = true
		}
	}
	if !found {
		t.Errorf("reads %+v lack {ReadNoImpl, std.core.Free, S}", b.Reads)
	}
}

// TestQuerySigPanic: `mk query sig Panic` answers the
// universe function's signature as the language reference spells it; it
// answered "no item named Panic".
func TestQuerySigPanic(t *testing.T) {
	t.Parallel()
	file := filepath.Join(t.TempDir(), "main.mk")
	if err := os.WriteFile(file, []byte("export Main(sys: &System) {\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := NewSession(Config{Root: file, CacheDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	a, _, err := s.Query("sig", []string{"Panic"})
	if err != nil || a == nil || !a.Found || a.Text() != "Panic(msg: str): never\n" {
		t.Fatalf("query sig Panic: %v\n%v", err, a)
	}
}
