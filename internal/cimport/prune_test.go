// Why: a binding of a large header is mostly items a program never uses, and
// pruning them keeps checking fast.
// What: TestCImport_Prune and TestCImport_PruneAcrossImports, over PruneAll.
// How: prunes the sample binding to what a program uses and checks what stays,
// with the rest blanked line for line so positions keep.

package cimport

import (
	"strings"
	"testing"
)

// TestCImport_Prune: lazy items. Pruning the sample binding to one function
// keeps that extern and the types its signature names, with their
// accessors, blanks the rest line for line, and drops an import the kept
// items no longer use; a folded spelling still keeps its item (E0207 needs
// it); the second binding's use of the first's type keeps it there.
func TestCImport_Prune(t *testing.T) {
	res := translate(t, "x86_64-windows", map[string]string{"a.h": sampleHeader},
		Import{Name: "a", Headers: []string{"a.h"}, Macros: []string{"int FM(int x)"}})
	full := res.Modules[0]
	want := Want{}
	Uses("import c.a\n\nexport Main() {\n    x := a.open_db(\"d\", null) // a.add is a comment\n    y := a.K\n    z := a.e1\n}\n", want)
	got := PruneAll([]string{"a"}, []string{full}, want)[0]
	dumpIf(t, "a.pruned.mk", got)
	if strings.Count(got, "\n") != strings.Count(full, "\n") {
		t.Fatalf("line count %d, want %d", strings.Count(got, "\n"), strings.Count(full, "\n"))
	}
	wantLines(t, got, "export type E = i32", "export const E1: E = 5", "export struct Sqlite3 {\n}",
		"export extern open_db(name: *u8, db: **Sqlite3): i32", "export const K: i32 = 42")
	for _, gone := range []string{"export extern add(", "export struct S {", "export SU.X(", "import std.raw", "export const Ks:"} {
		if strings.Contains(got, gone) {
			t.Errorf("pruned binding keeps %q", gone)
		}
	}
	// Every kept line is at its line in the full binding.
	fl, gl := strings.Split(full, "\n"), strings.Split(got, "\n")
	for i := range gl {
		if gl[i] != "" && gl[i] != fl[i] {
			t.Fatalf("line %d moved: %q", i+1, gl[i])
		}
	}
	// A struct keeps the types of its fields and its accessors.
	want = Want{}
	Uses("import x = c.a\nUse(s: *x.S) {}\n", want)
	got = PruneAll([]string{"a"}, []string{full}, want)[0]
	for _, w := range []string{"import std.raw", "export struct S {", "export struct SU {", "export SU.X(&self): i32 {", "export S.SetC(mut self, v: u32) {"} {
		wantLines(t, got, w)
	}
	if strings.Contains(got, "export extern add(") {
		t.Error("S keeps add")
	}
	// Nothing used: nothing kept but the head comments.
	if got := PruneAll([]string{"a"}, []string{full}, Want{})[0]; strings.Contains(got, "export ") {
		t.Error("an unused binding keeps items")
	}
}

// TestCImport_PruneAcrossImports: a kept item of a later binding keeps the
// earlier binding's type it names.
func TestCImport_PruneAcrossImports(t *testing.T) {
	files := map[string]string{
		"common.h": "#ifndef COMMON_H\n#define COMMON_H\ntypedef struct Shared { int v; } Shared;\nint unused_common(void);\n#endif\n",
		"one.h":    "#include <common.h>\nint one(Shared *s);\n",
		"two.h":    "#include <common.h>\nint two(Shared s);\n",
	}
	res := translate(t, "x86_64-windows", files, Import{Name: "one", Headers: []string{"one.h"}}, Import{Name: "two", Headers: []string{"two.h"}})
	want := Want{}
	Uses("import c.two\nF() {\n    unsafe \"c\" { _ = two.two(v) }\n}\n", want)
	got := PruneAll([]string{"one", "two"}, res.Modules, want)
	wantLines(t, got[1], "import c.one", "export extern two(s: one.Shared): i32")
	wantLines(t, got[0], "export struct Shared {")
	for _, gone := range []string{"export extern one(", "export extern unused_common("} {
		if strings.Contains(got[0], gone) {
			t.Errorf("one keeps %q", gone)
		}
	}
}
