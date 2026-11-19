// Why: check reads each module's direct imports from types.Module.Imports,
// so the driver must set them from the import graph, warm as cold.
// What: TestModuleImportsFromGraph, TestE0001_Alternatives and
// TestFuncReuseSliceImplReads, over slicePkg.
// How: checks packages cold and from the cache and compares the Imports
// lists; checks that E0001's fixes are of class Alternatives; and edits
// Slice impls to see which functions are rechecked.

package driver

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"mk/internal/diag"
	"mk/internal/toolchain"
	"mk/internal/types"
)

// importPaths is the paths of m's Imports, in their order.
func importPaths(s *Session, m *types.Module) []string {
	var out []string
	for _, id := range m.Imports {
		out = append(out, s.w.items.Module(id).Path)
	}
	return out
}

// TestModuleImportsFromGraph: after mk check, every module's Imports are
// its direct imports, each once (two files importing one module), in path
// order (written out of order), with std.core for a module that sees the
// prelude (check reads these; the driver does not set check's
// Config.Imports). std.core itself does not list itself. A warm check that
// loads lib and mid from the cache gives the same lists, and an edit that
// adds an import to lib makes lib miss (its stored imports differ) while the
// lists follow the edit.
func TestModuleImportsFromGraph(t *testing.T) {
	files := pkgFiles(map[string]string{
		"lib/lib.mk":   "export One(): int {\n    return 1\n}\n",
		"other/o.mk":   "export Two(): int {\n    return 2\n}\n",
		"mid/a.mk":     "import other\nimport lib\n\nexport Three(): int {\n    return lib.One() + other.Two()\n}\n",
		"mid/b.mk":     "import lib\n\nexport Four(): int {\n    return lib.One() + 3\n}\n",
		"app/app.mk":   "import mid\n\nexport Use(): int {\n    return mid.Three()\n}\n",
		"solo/solo.mk": "export Five(): int {\n    return 5\n}\n",
	})
	cfg := Config{Root: writeTree(t, files), CacheDir: t.TempDir(), StdDir: fakeStd(t), Target: types.X86_64Windows}
	want := map[string][]string{
		"lib":   {"std.core"},
		"other": {"std.core"},
		"mid":   {"lib", "other", "std.core"},
		"app":   {"mid", "std.core"},
		"solo":  {"std.core"},
	}
	run := func(name string, want map[string][]string) Result {
		t.Helper()
		s, err := NewSession(cfg)
		if err != nil {
			t.Fatal(err)
		}
		r := s.Check()
		if len(r.Report.Diags) > 0 {
			t.Fatalf("%s: %s", name, r.Report.Text(r.Sources))
		}
		for path, w := range want {
			m := s.pkgB.byName[path]
			if m == nil || m.mod == nil {
				t.Fatalf("%s: no module %s", name, path)
			}
			if got := importPaths(s, m.mod); !slices.Equal(got, w) {
				t.Errorf("%s: %s.Imports = %q, want %q", name, path, got, w)
			}
		}
		core := s.stdB.byName[preludeModule]
		if slices.Contains(importPaths(s, core.mod), preludeModule) {
			t.Errorf("%s: std.core imports itself: %q", name, importPaths(s, core.mod))
		}
		for _, m := range append(slices.Clone(s.stdB.mods), s.pkgB.mods...) {
			ps := importPaths(s, m.mod)
			if !slices.IsSorted(ps) || len(slices.Compact(slices.Clone(ps))) != len(ps) {
				t.Errorf("%s: %s.Imports %q not sorted or not unique", name, m.path, ps)
			}
		}
		return r
	}
	cold := run("cold", want)
	if got := pkgOnly(cold.Trace.ModulesFromCache); len(got) != 0 {
		t.Errorf("cold: from cache %q", got)
	}
	warm := run("warm", want)
	for _, p := range []string{"lib", "mid", "app"} {
		if !slices.Contains(warm.Trace.ModulesFromCache, p) {
			t.Errorf("warm: %s not from the cache (%q)", p, warm.Trace.ModulesFromCache)
		}
	}
	lib := filepath.Join(cfg.Root, "lib", "lib.mk")
	edit := "import solo\n\n" + strings.Replace(files["lib/lib.mk"], "return 1", "return solo.Five() - 4", 1)
	if err := os.WriteFile(lib, []byte(edit), 0o644); err != nil {
		t.Fatal(err)
	}
	want["lib"] = []string{"solo", "std.core"}
	edited := run("edited", want)
	if slices.Contains(edited.Trace.ModulesFromCache, "lib") {
		t.Errorf("edited: lib from the cache with a new import (%q)", edited.Trace.ModulesFromCache)
	}
}

// TestE0001_Alternatives: every kind of E0001 the driver builds for a tool
// carries alternatives (E0001's fix class), written as instructions with
// no path of this machine in them: a tool that ran and failed (rerun the
// command, another LLVM), no LLVM found (install one, name one), a tool
// that is not an error of the toolchain package, an LLVM that lacks a tool,
// an executable that cannot be replaced, an unknown --cpu and a reachable
// function with no body. The real no-LLVM build gives the install ones.
func TestE0001_Alternatives(t *testing.T) {
	failed := &toolchain.ToolError{Tool: "clang", Command: "clang --target=x -S", Stderr: "boom", ExitCode: 1}
	missing := &toolchain.MissingError{Searched: []string{`C:\nowhere`}, Missing: []string{"clang"}}
	cases := []struct {
		name string
		d    *diag.Diagnostic
		want []string
	}{
		{"tool failed", toolErrIn(failed, "clang", "clang could not run", ""), []string{altRunCommand, altOtherLLVM}},
		{"no LLVM", toolErr(missing, "no usable LLVM toolchain: "+missing.Error()), []string{altInstallLLVM, altLLVMFlag}},
		{"other error", toolErr(errors.New("cannot start"), "x"), []string{altInstallLLVM, altLLVMFlag}},
		{"link", linkErr(&toolchain.ToolError{Tool: "lld-link", Command: "lld-link a.obj", Stderr: "lld-link: error: undefined symbol: F\n", ExitCode: 1}, ""),
			[]string{altRunCommand, altOtherLLVM}},
	}
	for _, c := range cases {
		if !slices.Equal(c.d.Alternatives, c.want) {
			t.Errorf("%s: alternatives %q, want %q", c.name, c.d.Alternatives, c.want)
		}
	}
	all := []string{altInstallLLVM, altLLVMFlag, altRunCommand, altOtherLLVM, altAddTools, altCloseExe, altOtherOut,
		altCPUName, altNoCPU, altGiveBody, altNoCall}
	for _, a := range all {
		if strings.ContainsAny(a, `:\`) || strings.Contains(a, "/v/") || strings.Contains(a, "Program Files") {
			t.Errorf("alternative %q names a path", a)
		}
	}
	if _, d := placeExe(filepath.Join(t.TempDir(), "absent.tmp"), filepath.Join(t.TempDir(), "out.exe")); len(d) != 1 ||
		!slices.Equal(d[0].Alternatives, []string{altCloseExe, altOtherOut}) {
		t.Errorf("placeExe: %v", d)
	}

	dir := t.TempDir()
	src := filepath.Join(dir, "seven.mk")
	if err := os.WriteFile(src, []byte("export Main(sys: &System): int {\n    return 7\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := NewSession(Config{Root: src, LLVMDir: filepath.Join(dir, "nollvm"), StdDir: fakeStd(t)})
	if err != nil {
		t.Fatal(err)
	}
	r, _, err := s.Build(filepath.Join(dir, "seven.exe"))
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Report.Diags) != 1 || r.Report.Diags[0].Diag.Code != diag.E0001 ||
		!slices.Equal(r.Report.Diags[0].Diag.Alternatives, []string{altInstallLLVM, altLLVMFlag}) {
		t.Errorf("no LLVM build:\n%s", r.Report.Text(r.Sources))
	}
}

// slicePkg: b declares T and U, U implementing Slice[int]; a indexes a U
// (GetU reads ReadImpl(Slice, b.U)) and has functions that index nothing.
func slicePkg() map[string]string {
	return map[string]string{
		"pkg.mk": manifest(nil),
		"b/b.mk": "export struct T {\n    export a: [2]int\n}\n\nexport struct U {\n    export a: [2]int\n}\n\n" +
			"impl Slice[int] for U {\n    Items(&self): []int {\n        return self.a[..]\n    }\n\n" +
			"    ItemsMut(mut self): mut []int {\n        return mut self.a[..]\n    }\n}\n",
		"a/a.mk": "import b\n\nexport GetU(u: &b.U): int {\n    return u[0]\n}\n\nexport FirstT(t: &b.T): int {\n    return t.a[0]\n}\n\n" +
			"export Other(): int {\n    return 1\n}\n",
	}
}

// TestFuncReuseSliceImplReads (the invalidation table's impl row, per
// function): with Slice impls out of the function prefix, adding impl
// Slice[int] for T misses a (b's interface changed) but rechecks none of a's
// functions: none indexes a T, and GetU's ReadImpl(Slice, b.U) still holds.
// Removing U's impl rechecks GetU, which then fails exactly as a cold check
// does, and still reuses the others.
func TestFuncReuseSliceImplReads(t *testing.T) {
	implT := "\nimpl Slice[int] for T {\n    Items(&self): []int {\n        return self.a[..]\n    }\n\n" +
		"    ItemsMut(mut self): mut []int {\n        return mut self.a[..]\n    }\n}\n"
	warm, cold := warmAfter(t, slicePkg(), "b/b.mk", func(s string) string { return s + implT })
	clean(t, cold)
	if !bytes.Equal(jsonOf(t, warm), jsonOf(t, cold)) {
		t.Errorf("warm --json %s, cold %s", jsonOf(t, warm), jsonOf(t, cold))
	}
	if got := pkgOnly(warm.Trace.ModulesResolved); !slices.Contains(got, "a") {
		t.Errorf("missed %q, want a among them (b's interface changed)", got)
	}
	for _, k := range pkgItems(warm.Trace.ItemsChecked) {
		if strings.HasPrefix(k, "a.") {
			t.Errorf("rechecked %s after a Slice impl for T (from cache %q)", k, pkgItems(warm.Trace.ItemsFromCache))
		}
	}
	for _, k := range []string{"a.GetU", "a.FirstT", "a.Other"} {
		if !slices.Contains(pkgItems(warm.Trace.ItemsFromCache), k) {
			t.Errorf("%s not from the cache (%q)", k, pkgItems(warm.Trace.ItemsFromCache))
		}
	}

	files := slicePkg()
	i := strings.Index(files["b/b.mk"], "impl Slice[int] for U")
	warm, cold = warmAfter(t, files, "b/b.mk", func(s string) string { return s[:i] })
	if len(cold.Report.Diags) == 0 {
		t.Fatal("GetU checks with no Slice impl for U; the test needs its error")
	}
	if !bytes.Equal(jsonOf(t, warm), jsonOf(t, cold)) {
		t.Errorf("impl removed: warm --json %s, cold %s", jsonOf(t, warm), jsonOf(t, cold))
	}
	if got := pkgItems(warm.Trace.ItemsChecked); !slices.Contains(got, "a.GetU") {
		t.Errorf("impl removed: GetU not rechecked (%q)", got)
	}
}
