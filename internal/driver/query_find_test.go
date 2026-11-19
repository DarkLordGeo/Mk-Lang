// Why: mk query answers editors and readers; a wrong find, members, stack
// or lookup answer sends them to the wrong code.
// What: tests of `find` over a word that names a module, `stack` through a
// Slice[T] namespace function as mono instantiates it, `members` of one
// module's type, and the editor's exact lookup.
// How: runs mk query over small packages and std and compares the items
// listed.

package driver

import (
	"strings"
	"testing"
)

// TestFindNamesModuleItems: `mk query find csv` lists
// std.csv's top-level names, though none of them has csv in its name.
func TestFindNamesModuleItems(t *testing.T) {
	dir := testPkg(t, map[string]string{"q.mk": querySource})
	a := queryOne(t, dir, "find", "csv")
	answerHas(t, a, "Records  ", "WriteRecord  ", "std.csv")
	if text := a.Text(); strings.Contains(text, "no name matches") {
		t.Errorf("find csv found nothing:\n%s", text)
	}
}

const sliceStackSource = `Copy(dst: mut []u8, src: []u8) {
    dst.CopyFrom(src)
}

CopyList(l: mut List[u8], src: []u8) {
    l.CopyFrom(src)
}

CopyGen[T: Plain](l: mut List[T], src: []T) {
    l.CopyFrom(src)
}

Items[S: Slice[u8]](s: mut S): int {
    return s.ItemsMut().Len()
}

export Main(sys: &System) {
    var a: [4]u8 = [1, 2, 3, 4]
    b: [4]u8 = [5, 6, 7, 8]
    Copy(mut a[..], b[..])
}
`

// TestStackThroughSliceFunction: `self.ItemsMut()` in Slice[T].CopyFrom
// is no call once mono makes self a slice, so a caller has a static bound;
// on a List the implementer's ItemsMut, which mono calls first, is in the
// chain; an interface call on a type parameter is still not followed.
func TestStackThroughSliceFunction(t *testing.T) {
	dir := testPkg(t, map[string]string{"q.mk": sliceStackSource})
	answerHas(t, queryOne(t, dir, "stack", "Main"), "bounded: no recursion",
		"deepest direct chain: 4 frame(s): Main -> Copy -> Slice.CopyFrom -> Fail")
	answerHas(t, queryOne(t, dir, "stack", "CopyList"), "bounded: no recursion", "CopyList -> std.core#impl(std.core.Slice for std.core.List):ItemsMut")
	answerHas(t, queryOne(t, dir, "stack", "CopyGen"), "bounded: no recursion", "CopyGen -> std.core#impl(std.core.Slice for std.core.List):ItemsMut")
	answerHas(t, queryOne(t, dir, "stack", "Items"), "no static bound: 1 indirect call(s), first q.mk:14")
}

const twoPoolsSource = `import std.mem

export struct Pool {
    export certs: []str
}

export Pool.New(certs: []str): Pool from certs {
    return Pool(certs: certs)
}

Hold(p: &mem.Pool[int]): int {
    return p.Len()
}
`

// TestMembersOfOneModule: `members Pool` lists the package's
// Pool and its one method, not std.mem's Pool[T] methods, and `members
// mem.Pool` lists std.mem's alone: a method belongs to the type that owns
// it, not to every type of the same name.
func TestMembersOfOneModule(t *testing.T) {
	dir := testPkg(t, map[string]string{"q.mk": twoPoolsSource})
	own := queryOne(t, dir, "members", "Pool")
	answerHas(t, own, "export certs: []str", "New(certs: []str): Pool from certs")
	for _, l := range own.Answer {
		for _, m := range []string{"Add(", "GetPair(", "Sweep(", "Len("} {
			if strings.Contains(l, m) {
				t.Errorf("members Pool lists std.mem's %s: %s", m, l)
			}
		}
	}
	mem := queryOne(t, dir, "members", "mem.Pool")
	answerHas(t, mem, "Add(", "Len(&self): int", "Sweep(")
	for _, l := range mem.Answer {
		if strings.Contains(l, "certs") {
			t.Errorf("members mem.Pool lists the package's Pool: %s", l)
		}
	}
}

const outLenSource = `struct Out {
    at: int
}

Out.Len(&self): int {
    return self.at
}

Count(buf: mut []u8): int {
    var out := buf
    return out.Len()
}

export Main(sys: &System) {
    var b: [4]u8 = [1, 2, 3, 4]
    Assert(Count(mut b[..]) == Out(at: 4).Len())
}
`

// TestLookupExactNeverFolds: the editor's lookup takes the text at the
// cursor as written, so `out.Len` (a slice local's method) is not the
// package's `Out.Len`, which mk query's case-folded pass would find (as
// std.tls's Out.Len was found for `out.Len()` in std.compress).
func TestLookupExactNeverFolds(t *testing.T) {
	s, _, _ := lspSession(t, writeTree(t, map[string]string{"q.mk": outLenSource}), t.TempDir(), false)
	if it, ok := s.Lookup("out.Len"); !ok || it.Owner != "Out" {
		t.Errorf("Lookup(out.Len), case-folded = %+v, %v; want Out.Len", it, ok)
	}
	if it, ok := s.LookupExact("out.Len"); ok {
		t.Errorf("LookupExact(out.Len) = %+v, want no item", it)
	}
	if it, ok := s.LookupExact("Out.Len"); !ok || it.Owner != "Out" || it.Name != "Len" {
		t.Errorf("LookupExact(Out.Len) = %+v, %v", it, ok)
	}
}
