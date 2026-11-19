// Why: refapi.go is a hand-kept copy of std's reference parser, so it must
// be checked against the real reference to stay equal.
// What: tests of refapi.go against doc/reference.md as it is on disk: the
// routing TestAPIRouting checks in std, unique line IDs, and
// functions told from declarations.
// How: parses ../../doc/reference.md and checks known lines.

package main

import (
	"os"
	"path/filepath"
	"testing"
)

// referenceItems parses the library surface of the repository's
// doc/reference.md.
func referenceItems(t *testing.T) []Item {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "doc", "reference.md"))
	if err != nil {
		t.Fatal(err)
	}
	items, err := parseReferenceAPI(string(b))
	if err != nil {
		t.Fatal(err)
	}
	if len(items) < 300 {
		t.Fatalf("the library surface parsed to %d items; want the whole block", len(items))
	}
	return items
}

// TestRefapiRoutesAsStd: the copy routes the lines std's
// TestAPIRouting names to the same modules (the router is the same).
func TestRefapiRoutesAsStd(t *testing.T) {
	items := referenceItems(t)
	want := map[string]string{
		"AtomicInt.New(n: i64): AtomicInt":                     "std.atomic",
		"error Os(code: i64)":                                  "std.fs",
		"fs.Rename(io: Io, src: str, dst: str): fs.Error!void": "std.fs",
		"Duration.Nanos(self): i64":                            "std.time",
		"Mutex[T].New(x: T): Mutex[T]":                         "std.core",
		"Shared[T].Ref(&self): &T":                             "std.sync",
		"enum Os {":                                            "std.core",
		"enum Signal {":                                        "std.proc",
		"Listener.Port(&self): int":                            "std.net",
		"math.Sqrt(x: f64): f64":                               "std.math",
		"Cell[T].Load(&self): T":                               "std.core",
	}
	seen := map[string]bool{}
	for _, it := range items {
		if m, ok := want[it.Text]; ok && !seen[it.Text] {
			seen[it.Text] = true
			if it.Mod != m {
				t.Errorf("%q routes to %s, want %s", it.Text, it.Mod, m)
			}
		}
	}
	for l := range want {
		if !seen[l] {
			t.Errorf("the reference's library surface has no line %q (update this table with std's TestAPIRouting)", l)
		}
	}
}

// TestRefapiIDsUnique: no two library lines share an ID, so
// COVERAGE.md, EXEMPT.md and the READMEs name each line once.
func TestRefapiIDsUnique(t *testing.T) {
	seen := map[string]bool{}
	for _, it := range referenceItems(t) {
		if seen[it.ID()] {
			t.Errorf("two library-surface lines have the ID %q", it.ID())
		}
		seen[it.ID()] = true
	}
}

// TestFuncOfItem: lines that declare functions name the function the
// build's symbols name; declarations name none.
func TestFuncOfItem(t *testing.T) {
	items := referenceItems(t)
	want := map[string]string{
		"str.Trim(self): str": "std.core.str:Trim",
		"fs.Rename(io: Io, src: str, dst: str): fs.Error!void": "std.fs.Rename",
		"math.Max[T: Ord + Plain](a: T, b: T): T":              "std.math.Max",
		"Listener.Port(&self): int":                            "std.net.Listener:Port",
		"System.Stdout(&self): Writer":                         "std.core.System:Stdout",
		"free.Heap(): &Heap":                                   "std.free.Heap",
	}
	got := map[string]bool{}
	for k := range items {
		it := &items[k]
		f, ok := funcOfItem(it)
		if w, listed := want[it.Text]; listed {
			got[it.Text] = true
			if !ok || f.String() != w {
				t.Errorf("%q: function %v (%v), want %s", it.Text, f, ok, w)
			}
		}
		if (it.Block || it.Text[:5] == "error" || it.Text[:4] == "type") && ok {
			t.Errorf("%q declares no function, got %v", it.Text, f)
		}
	}
	for l := range want {
		if !got[l] {
			t.Errorf("the reference's library surface has no line %q", l)
		}
	}
}
