// Why: lookup order and the diagnostics of a miss (E0201, E0204, E0207,
// E1221 and their fixes) are what users see most from resolve.
// What: tests of lookup, qualified names and the reads recorded for the cache.
// How: small modules through the helpers_test.go harness; fixes are applied
// with applyProven to check that they converge.

package resolve

import (
	"strings"
	"testing"

	"mk/internal/diag"
	"mk/internal/types"
)

func TestLookupOrder(t *testing.T) {
	t.Parallel()
	w := newWorld(t, true)
	w.add("lib.fs", opt{}, "export struct File {}\n")
	b := w.add("main", opt{}, `import lib.fs
struct List {}
F[T](x: T, f: fs.File) {
    y := x
    _ = y
    _ = List()
    _ = Map()
    _ = true
    _ = int(1)
    z: T = x
    _ = z
}
`)
	b.want(t)
	cases := []struct {
		name string
		n    int
		kind RefKind
	}{
		{"x", 2, RefParam}, {"y", 1, RefLocal}, {"T", 1, RefTypeParam}, {"fs", 1, RefModule}, {"File", 0, RefItem},
		{"List", 1, RefItem}, {"Map", 0, RefItem}, {"true", 0, RefUniverse}, {"int", 0, RefPrim},
	}
	for _, c := range cases {
		if r := b.ref(c.name, c.n); r.Kind != c.kind {
			t.Errorf("%s #%d: %v, want %v", c.name, c.n, r.Kind, c.kind)
		}
	}
	if r := b.ref("List", 1); w.items.Get(types.ItemID(r.A)).Key.String() != "main.List" {
		t.Errorf("List resolved to %v, not the module's", w.items.Get(types.ItemID(r.A)).Key)
	}
}

func TestModuleHidesPrelude(t *testing.T) {
	t.Parallel()
	w := newWorld(t, true)
	b := w.add("main", opt{}, "struct Text {}\nF(t: Text) { }\n")
	b.want(t)
	if r := b.ref("Text", 1); w.items.Get(types.ItemID(r.A)).Module != b.id {
		t.Error("prelude Text not hidden")
	}
}

func TestLocalNamedAfterType(t *testing.T) {
	t.Parallel()
	w := newWorld(t, true)
	b := w.add("main", opt{}, "F() {\n    arena := Arena()\n    _ = arena\n}\n")
	b.want(t)
}

func TestTypesAndValuesDoNotCollide(t *testing.T) {
	t.Parallel()
	w := newWorld(t, false)
	b := w.add("main", opt{}, "struct Point {}\nF(point: Point) { _ = point }\n")
	b.want(t)
}

func TestE0201_UnknownName(t *testing.T) {
	t.Parallel()
	w := newWorld(t, true)
	b := w.add("main", opt{}, "F() { _ = Missing() }\n")
	b.want(t, "E0201@1:11")
	if d := b.diags[0]; fact(d, "name") != "Missing" || len(d.Fixes) != 0 {
		t.Errorf("%s", b.dump())
	}
}

func TestE0201_FixAddsImport(t *testing.T) {
	t.Parallel()
	w := newWorld(t, true)
	w.cat.exports["ReadText"] = []string{"std.fs"}
	w.cat.searched = []string{"main"}
	b := w.add("main", opt{}, "import lib\n\nF() { _ = ReadText() }\n")
	ds := b.only(diag.E0201)
	if len(ds) != 1 {
		t.Fatalf("%s", b.dump())
	}
	d := ds[0]
	if fact(d, "module") != "std.fs" || fact(d, "import") != "import std.fs" || len(d.Fixes) != 1 || d.Fixes[0].Class != diag.Proven {
		t.Fatalf("%s", b.dump())
	}
	if e := d.Fixes[0].Edits[0]; e.Text != "fs.ReadText" {
		t.Errorf("use edit %+v", e)
	}
	e := d.Fixes[0].Edits[1]
	if e.Text != "\nimport std.fs" || !e.Span.IsEmpty() || e.Span.Start != uint32(len("import lib")) {
		t.Errorf("edit %+v", e)
	}
	b2 := w.add("other", opt{}, "F() { _ = ReadText() }\n")
	e = b2.only(diag.E0201)[0].Fixes[0].Edits[1]
	if e.Text != "import std.fs\n" || e.Span.Start != 0 {
		t.Errorf("edit without imports %+v", e)
	}
	if !hasRead(b, "main.F", types.ReadExports, "main", "") {
		t.Error("no ReadExports for the searched module")
	}
}

func TestE0201_Candidates(t *testing.T) {
	t.Parallel()
	w := newWorld(t, true)
	w.cat.exports["Error"] = []string{"std.fs", "std.net"}
	b := w.add("main", opt{}, "F(e: Error) { }\n")
	d := b.only(diag.E0201)[0]
	if fact(d, "candidates") != "[std.fs std.net]" || len(d.Fixes) != 0 || len(d.Alternatives) != 2 {
		t.Errorf("%s", b.dump())
	}
}

func TestE0201_ConsequenceGrouping(t *testing.T) {
	t.Parallel()
	w := newWorld(t, true)
	b := w.add("main", opt{}, "F() {\n    _ = Nope\n    _ = Nope\n}\n")
	ds := b.only(diag.E0201)
	if len(ds) != 2 || ds[0].Cause != nil || ds[1].Cause == nil || ds[1].Cause.At != ds[0].At {
		t.Errorf("%s", b.dump())
	}
}

func TestE0207_FoldedUse(t *testing.T) {
	t.Parallel()
	w := newWorld(t, true)
	b := w.add("main", opt{}, "F(max_users: int) { _ = maxUsers\n _ = list() }\n")
	b.want(t, "E0207@1:25", "E0207@2:6")
	for _, d := range b.diags {
		if len(d.Fixes) != 1 || d.Fixes[0].Class != diag.Proven {
			t.Errorf("%s", b.dump())
		}
	}
	if r := b.ref("maxUsers", 0); r.Kind != RefParam {
		t.Errorf("folded use resolves to %v", r.Kind)
	}
}

func TestSomeNoneFold(t *testing.T) {
	t.Parallel()
	w := newWorld(t, true)
	b := w.add("main", opt{}, "F(): ?int { return None }\nG(): ?int { return Some(1) }\nH(): bool { return True }\n")
	b.want(t, "E0207@1:20", "E0207@2:20", "E0207@3:20")
	for _, d := range b.diags {
		if fact(d, "declared") == "" || fact(d, "declared_at") != "" {
			t.Errorf("%s", b.dump())
		}
	}
}

func TestOkInValuePosition(t *testing.T) {
	t.Parallel()
	w := newWorld(t, true)
	b := w.add("main", opt{}, `F(): OutOfMemory!int { return ok(1) }
G(): OutOfMemory!int { return Ok(1) }
H(x: OutOfMemory!int) {
    match x {
        Ok(v) { _ = v }
        err(e) { _ = e }
    }
}
`)
	b.want(t, "E0201@1:31", "E0207@2:31", "E0207@5:9")
	d := b.only(diag.E0201)[0]
	if fact(d, "reason") != "pattern-head" || len(d.Fixes) != 0 {
		t.Errorf("ok(1): %s", b.dump())
	}
	ds := b.only(diag.E0207)
	if len(ds[0].Fixes) != 0 || len(ds[0].Alternatives) == 0 {
		t.Errorf("Ok(1): %s", b.dump())
	}
	if len(ds[1].Fixes) != 1 || ds[1].Fixes[0].Edits[0].Text != "ok" {
		t.Errorf("Ok(v): %s", b.dump())
	}
}

func TestOkIsAName(t *testing.T) {
	t.Parallel()
	w := newWorld(t, false)
	b := w.add("main", opt{}, "Register(ok: bool): bool { return ok }\n")
	b.want(t)
}

func TestE1209_NullNil(t *testing.T) {
	t.Parallel()
	w := newWorld(t, false)
	b := w.add("main", opt{}, "F(): ?int { return null }\nG(): ?int { return nil }\n")
	b.want(t, "E1209@1:20", "E1209@2:20")
	if d := b.diags[0]; len(d.Fixes) != 1 || d.Fixes[0].Edits[0].Text != "none" || fact(d, "target") != "F(): ?int { return none }" || fact(d, "habits") != "[E1209]" {
		t.Errorf("%s", b.dump())
	}
}

func TestE1221_Synonyms(t *testing.T) {
	t.Parallel()
	w := newWorld(t, true)
	b := w.add("main", opt{}, "F(v: Vec[int],\n  s: String,\n  n: usize) {\n  fmt.Println(n)\n}\n")
	b.want(t, "E1221@1:6", "E1221@2:6", "E1221@3:6", "E1221@4:3")
	for _, d := range b.diags {
		if fact(d, "mk_name") == "" || fact(d, "habits") != "[E1221]" {
			t.Errorf("%s", b.dump())
		}
	}
	if d := b.diags[3]; len(d.Fixes) != 0 || len(d.Alternatives) == 0 {
		t.Errorf("fmt.Println: %s", b.dump())
	}
}

// TestHabitsMergedPerLine: the name habits of one line are one diagnostic
// with the leftmost code, every code in facts.habits and the rewritten line
// in facts.target.
func TestHabitsMergedPerLine(t *testing.T) {
	t.Parallel()
	w := newWorld(t, true)
	b := w.add("main", opt{}, "F() { x: Vec[usize] = nil\n _ = x }\nG(): ?int { return nil }\nH(): ?int { y: ?usize = None\n return y }\n")
	b.want(t, "E1221@1:10", "E1209@3:20", "E1221@4:17")
	at := map[string]*diag.Diagnostic{}
	for _, d := range b.diags {
		at[b.lc(d.At)] = d
	}
	d := at["1:10"]
	if fact(d, "habits") != "[E1221 E1209]" || fact(d, "target") != "F() { x: List[int] = none" {
		t.Errorf("line 1: %s", b.dump())
	}
	if len(d.Fixes) != 1 || len(d.Fixes[0].Edits) != 3 {
		t.Errorf("line 1 fix: %s", b.dump())
	}
	if d := at["4:17"]; fact(d, "habits") != "[E1221 E0207]" || fact(d, "target") != "H(): ?int { y: ?int = none" {
		t.Errorf("line 4: %s", b.dump())
	}
	src := "F() { x: Vec[usize] = nil\n _ = x }\n"
	w2 := newWorld(t, true)
	b2 := w2.add("main", opt{}, src)
	w3 := newWorld(t, true)
	fixed := applyProven(t, w2, b2, src)
	if b3 := w3.add("main", opt{}, fixed); len(b3.diags) != 0 {
		t.Errorf("%q: %s", fixed, b3.dump())
	}
}

// TestHabitsMergedAfterSettle: in a module with gen lines every miss is
// pending, so the habits of a line are merged over Settle's retries.
func TestHabitsMergedAfterSettle(t *testing.T) {
	t.Parallel()
	w := newWorld(t, true)
	w.add("std.derive", opt{std: true}, "export Eq(g: int)\n")
	b := w.add("main", opt{noSettle: true}, "import std.derive\n\nstruct User {}\ngen derive.Eq(User)\nF() { x: Vec[usize] = nil\n _ = x }\n")
	b.m.AddGenerated(nil)
	b.settle()
	b.want(t, "E1221@5:10")
	if d := b.diags[0]; fact(d, "habits") != "[E1221 E1209]" {
		t.Errorf("%s", b.dump())
	}
}

func TestE1221_SharedNeedsImport(t *testing.T) {
	t.Parallel()
	w := newWorld(t, true)
	b := w.add("main", opt{}, "F(r: Rc[int]) { }\n")
	d := b.only(diag.E1221)[0]
	if fact(d, "import") != "import std.sync" || len(d.Fixes) != 1 || len(d.Fixes[0].Edits) != 2 {
		t.Errorf("%s", b.dump())
	}
}

func TestBoxIsPrelude(t *testing.T) {
	t.Parallel()
	w := newWorld(t, true)
	w.add("main", opt{}, "F(b: Box[int]) { }\n").want(t)
}

func TestProvenFixesConverge(t *testing.T) {
	t.Parallel()
	srcs := []string{
		"F(max_users: int) { _ = maxUsers }\n",
		"F(): ?int { return None }\n",
		"F(): ?int { return null }\n",
		"F(v: Vec[int]) { }\n",
		"F(r: Rc[int]) { }\n",
		"F(x: OutOfMemory!int) {\n    match x {\n        Err(e) { _ = e }\n        ok(v) { _ = v }\n    }\n}\n",
		"struct point {}\nF(p: point) { _ = p }\n",
	}
	for _, src := range srcs {
		w := newWorld(t, true)
		w.add("std.sync", opt{std: true}, "export struct Shared[T] {}\n")
		b := w.add("main", opt{}, src)
		fixed := applyProven(t, w, b, src)
		w2 := newWorld(t, true)
		w2.add("std.sync", opt{std: true}, "export struct Shared[T] {}\n")
		b2 := w2.add("main", opt{}, fixed)
		for _, d := range b.diags {
			for _, d2 := range b2.diags {
				if d2.Code == d.Code && d2.At.Start == d.At.Start {
					t.Errorf("%q -> %q: %v remains\n%s", src, fixed, d.Code, b2.dump())
				}
			}
		}
	}
}

// applyProven applies every proven fix of a single-file module.
func applyProven(t *testing.T, w *world, b *built, src string) string {
	type edit struct {
		start, end uint32
		text       string
	}
	var edits []edit
	for _, d := range b.diags {
		for _, f := range d.Fixes {
			if f.Class != diag.Proven {
				continue
			}
			for _, e := range f.Edits {
				edits = append(edits, edit{e.Span.Start, e.Span.End, e.Text})
			}
		}
	}
	for i := 1; i < len(edits); i++ {
		for j := i; j > 0 && edits[j].start > edits[j-1].start; j-- {
			edits[j], edits[j-1] = edits[j-1], edits[j]
		}
	}
	out := src
	last := uint32(len(src) + 1)
	for _, e := range edits {
		if e.end > last {
			continue
		}
		out = out[:e.start] + e.text + out[e.end:]
		last = e.start
	}
	return out
}

// TestPathNotReserved: `Path` is an ordinary prelude type, not a
// reserved name. A prelude that does not declare it gives the ordinary
// E0201, with no reason reserved; a prelude that declares it resolves to it;
// a module's own `Path` is unaffected.
func TestPathNotReserved(t *testing.T) {
	t.Parallel()
	w := newWorld(t, true)
	b := w.add("main", opt{}, "F(p: Path) { }\n")
	b.want(t, "E0201@1:6")
	if d := b.diags[0]; fact(d, "reason") == "reserved" {
		t.Errorf("%s", b.dump())
	}
	w.add("own", opt{}, "struct Path {}\nF(p: Path) { }\n").want(t)

	w2 := newWorld(t, false)
	w2.cfg.PreludePath, w2.cfg.PreludeNames = "std.core", preludeNames
	w2.s = NewSession(w2.cfg)
	w2.core = w2.add("std.core", opt{std: true, noPrelude: true}, fakePrelude()+"export struct Path {}\n").id
	w2.add("main", opt{}, "F(p: Path) { }\n").want(t)
}

func TestPreludeScopeIsExactlyReferenceList(t *testing.T) {
	t.Parallel()
	w := newWorld(t, true)
	w.cat.exports["Duration"] = []string{"std.time"}
	b := w.add("main", opt{}, "F(d: Duration, t: IoTable) { }\n")
	b.want(t, "E0201@1:6", "E0201@1:19")
	if d := b.diags[0]; fact(d, "import") != "import std.time" {
		t.Errorf("%s", b.dump())
	}
}

func TestSelfOutsideMethod(t *testing.T) {
	t.Parallel()
	w := newWorld(t, false)
	b := w.add("main", opt{}, "struct P {}\nP.M(&self) { _ = self }\nF() { _ = self }\n")
	b.want(t, "E0201@3:11")
	if r := b.ref("self", 0); r.Kind != RefParam || r.A != 1 {
		t.Errorf("self in a method: %+v", r)
	}
}

func TestSelfTypeOnlyInImpl(t *testing.T) {
	t.Parallel()
	w := newWorld(t, true)
	b := w.add("main", opt{}, "struct P {}\nimpl Eq for P { Same(&self, o: &Self): bool { return true } }\nF(x: Self) { }\n")
	b.want(t, "E0201@3:6")
	if r := b.ref("Self", 0); r.Kind != RefSelfType {
		t.Errorf("Self in impl: %+v", r)
	}
}

func TestE0204_PrivateItem(t *testing.T) {
	t.Parallel()
	w := newWorld(t, false)
	w.add("lib", opt{}, "struct Hidden {}\nexport struct Shown {}\n")
	b := w.add("main", opt{}, "import lib\n\nF(a: lib.Hidden, b: lib.Shown) { }\n")
	b.want(t, "E0204@3:10")
	d := b.diags[0]
	if fact(d, "item") != "Hidden" || fact(d, "module") != "lib" || fact(d, "declared_at") == "" {
		t.Errorf("%s", b.dump())
	}
	if !hasRead(b, "main.F", types.ReadSignature, "lib.Hidden", "") {
		t.Error("no ReadSignature on E0204 (R-29)")
	}
}

func TestE0201_MissingInModule(t *testing.T) {
	t.Parallel()
	w := newWorld(t, false)
	w.add("lib", opt{}, "export struct Shown {}\n")
	b := w.add("main", opt{}, "import lib\n\nF(a: lib.Gone) { }\n")
	b.want(t, "E0201@3:10")
	if d := b.diags[0]; fact(d, "module") != "lib" || fact(d, "name") != "Gone" {
		t.Errorf("%s", b.dump())
	}
	if !hasRead(b, "main.F", types.ReadNoItem, "lib", "Gone") {
		t.Error("no ReadNoItem for the qualified miss")
	}
}

func TestE0207_FoldedInModule(t *testing.T) {
	t.Parallel()
	w := newWorld(t, false)
	w.add("lib", opt{}, "export struct ReadBuf {}\n")
	b := w.add("main", opt{}, "import lib\n\nF(a: lib.Readbuf) { }\n")
	b.want(t, "E0207@3:10")
}

func TestQualifiedSegs(t *testing.T) {
	t.Parallel()
	w := newWorld(t, false)
	w.add("lib.fs", opt{}, "export struct File {}\n")
	b := w.add("main", opt{}, "import lib.fs\nstruct Point {}\n\nF(x: Point) {\n    _ = fs.File.Open\n    _ = Point.Origin\n    _ = x.len\n}\n")
	b.want(t)
	fes := fieldExprRefs(b)
	want := map[string]Ref{
		"fs.File":      {Kind: RefItem, Segs: 2},
		"fs.File.Open": {Kind: RefDeferred, Segs: 2},
		"Point.Origin": {Kind: RefDeferred, Segs: 1},
		"x.len":        {Kind: RefDeferred, Segs: 1},
	}
	for k, r := range want {
		got, ok := fes[k]
		if !ok || got.Kind != r.Kind || got.Segs != r.Segs {
			t.Errorf("%s: %+v, want %+v", k, got, r)
		}
	}
}

func TestImportUsedNoRace(t *testing.T) {
	t.Parallel()
	w := newWorld(t, false)
	w.add("std.fs", opt{std: true}, "export struct File {}\n")
	var sb strings.Builder
	sb.WriteString("import std.fs\n\n")
	for i := 0; i < 64; i++ {
		sb.WriteString("F" + itoa(i) + "(f: fs.File) { }\n")
	}
	b := w.add("main", opt{workers: 8}, sb.String())
	b.want(t)
	if !b.info.Files[0].Imports[0].Used {
		t.Error("binding not used")
	}
}

func TestE0208_UnusedImport(t *testing.T) {
	t.Parallel()
	w := newWorld(t, false)
	w.add("std.fs", opt{std: true}, "export struct File {}\n")
	b := w.add("main", opt{}, "import std.fs\n\nF() {}\n")
	b.want(t, "E0208@1:1")
	d := b.diags[0]
	if d.Phase != diag.PhaseTypes || len(d.Fixes) != 1 || d.Fixes[0].Edits[0].Span.End != uint32(len("import std.fs\n")) {
		t.Errorf("%s %+v", b.dump(), d.Fixes)
	}
	w.add("poisoned", opt{}, "import std.gone\n\nF() {}\n").want(t)
}

func TestReadNoItemOnPreludeHit(t *testing.T) {
	t.Parallel()
	w := newWorld(t, true)
	b := w.add("main", opt{}, "F(l: List[int]) { }\n")
	if !hasRead(b, "main.F", types.ReadNoItem, "main", "List") || !hasRead(b, "main.F", types.ReadSignature, "std.core.List", "") {
		t.Errorf("reads %v", readsOf(b, "main.F"))
	}
}

func TestReadNoItemOnQualifiedMiss(t *testing.T) {
	t.Parallel()
	w := newWorld(t, false)
	w.add("lib", opt{}, "export struct A {}\n")
	b := w.add("main", opt{}, "import lib\n\nF(x: lib.B) { }\n")
	if !hasRead(b, "main.F", types.ReadNoItem, "lib", "B") {
		t.Errorf("reads %v", readsOf(b, "main.F"))
	}
}

func TestReadExportsOnCandidateSearch(t *testing.T) {
	t.Parallel()
	w := newWorld(t, false)
	w.cat.searched = []string{"app.a", "app.b"}
	b := w.add("main", opt{}, "F(x: Nowhere) { }\n")
	if !hasRead(b, "main.F", types.ReadExports, "app.a", "") || !hasRead(b, "main.F", types.ReadExports, "app.b", "") {
		t.Errorf("reads %v", readsOf(b, "main.F"))
	}
}

func TestNoReadsForLocals(t *testing.T) {
	t.Parallel()
	w := newWorld(t, false)
	b := w.add("main", opt{}, "F[T](x: T) { y := x\n _ = y }\n")
	if rs := readsOf(b, "main.F"); len(rs) != 0 {
		t.Errorf("reads %v", rs)
	}
}

func TestPrivateGenerator(t *testing.T) {
	t.Parallel()
	w := newWorld(t, false)
	w.add("std.derive", opt{std: true}, "Eq(g: int)\nexport Hash(g: int)\n")
	b := w.add("main", opt{}, "import std.derive\n\nstruct User {}\ngen derive.Eq(User)\ngen derive.Hash(User)\n", "F() {}\n")
	b.want(t, "E0204@4:12")
	if !hasRead(b, `main#gen(0)`, types.ReadSignature, "std.derive.Eq", "") {
		t.Errorf("reads %v", readsOf(b, "main#gen(0)"))
	}
	if g := b.info.Gens[1]; g.Generator != w.item("std.derive.Hash").ID {
		t.Errorf("gen line 1 generator %d", g.Generator)
	}
	if r := b.ref("User", 1); r.Kind != RefItem {
		t.Errorf("gen argument: %+v", r)
	}
}
