// Why: funccache.go reuses a function's check results across edits; reuse
// is only sound if it gives a cold check's bytes and never outlives a read.
// What: TestFuncReuseSeesSignatureChange, TestFuncReuseReplaysDiagnostics,
// TestFuncReuseImplReads and TestImplSetFollowsImpls, over warmAfter.
// How: a reused function must give the bytes a cold check gives,
// diagnostics included, and a function whose reads changed must not be
// reused; each test edits a file and compares warm with fresh-cache cold.

package driver

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"mk/internal/source"
	"mk/internal/types"
)

// warmAfter checks files cold, applies edit to path, checks warm in the same
// cache and cold in a fresh one, and returns both results.
func warmAfter(t *testing.T, files map[string]string, path string, edit func(string) string) (Result, Result) {
	t.Helper()
	dir := writeTree(t, files)
	cfg := Config{Root: dir, CacheDir: t.TempDir(), Target: types.X86_64Windows, BuildID: source.HashString("test", "mk")}
	frontCheck(t, cfg)
	if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(path)), []byte(edit(files[path])), 0o644); err != nil {
		t.Fatal(err)
	}
	warm := frontCheck(t, cfg)
	fresh := cfg
	fresh.CacheDir = t.TempDir()
	return warm, frontCheck(t, fresh)
}

// TestFuncReuseSeesSignatureChange: b.G gains a parameter and a.Q's call is
// left alone. a.Q read b.G's signature, so it is rechecked and reports the
// arity error a cold check reports; a.P, which did not read it, is reused.
func TestFuncReuseSeesSignatureChange(t *testing.T) {
	warm, cold := warmAfter(t, invPkg(), "b/b.mk", replace("export G(x: int): int {\n    return x * 3", "export G(x: int, y: int): int {\n    return x * y"))
	if len(cold.Report.Diags) == 0 {
		t.Fatal("the cold check of the edited source is clean; the test needs an error in a.Q")
	}
	if !bytes.Equal(jsonOf(t, warm), jsonOf(t, cold)) {
		t.Errorf("warm --json %s\ncold %s", jsonOf(t, warm), jsonOf(t, cold))
	}
	checked := pkgItems(warm.Trace.ItemsChecked)
	if !slices.Contains(checked, "a.Q") || slices.Contains(checked, "a.P") {
		t.Errorf("rechecked %q: want a.Q and not a.P", checked)
	}
}

// TestFuncReuseReplaysDiagnostics: b.F has an E0501 (stage 7 poisons only
// its body, so b is stored). An edit of b.G's body misses b; b.F is
// reused and its E0501 is replayed, so the warm --json equals the cold one.
func TestFuncReuseReplaysDiagnostics(t *testing.T) {
	files := invPkg()
	files["b/b.mk"] = strings.Replace(files["b/b.mk"], "export F(x: int): int {\n    return x + 1\n}",
		"export F(x: int): int {\n    var n := x\n    p := mut n\n    q := mut n\n    *p += 1\n    *q += 1\n    return n\n}", 1)
	warm, cold := warmAfter(t, files, "b/b.mk", replace("return x * 3", "return x * 4"))
	if got := codes(cold); !slices.Equal(got, []string{"E0501"}) {
		t.Fatalf("cold codes %q, want E0501\n%s", got, cold.Report.Text(cold.Sources))
	}
	if !bytes.Equal(jsonOf(t, warm), jsonOf(t, cold)) {
		t.Errorf("warm --json %s\ncold %s", jsonOf(t, warm), jsonOf(t, cold))
	}
	if got := pkgItems(warm.Trace.ItemsFromCache); !slices.Contains(got, "b.F") {
		t.Errorf("b.F not reused (from cache %q, rechecked %q)", got, pkgItems(warm.Trace.ItemsChecked))
	}
	if got := pkgItems(warm.Trace.ItemsChecked); !slices.Contains(got, "b.G") || slices.Contains(got, "b.F") {
		t.Errorf("rechecked %q: want b.G and not b.F", got)
	}
}

// implPkg is the impl-read harness: b declares Show, T and U with an impl
// Show for U; a's UseU satisfies Use's bound through that impl, so its
// check reads ReadImpl(b.Show, b.U).
func implPkg() map[string]string {
	return map[string]string{
		"pkg.mk": manifest(nil),
		"b/b.mk": "export interface Show {\n    Show(&self): int\n}\n\nexport struct T {\n    export a: int\n}\n\n" +
			"export struct U {\n    export b: int\n}\n\nimpl Show for U {\n    Show(&self): int {\n        return self.b\n    }\n}\n",
		"a/a.mk": "import b\n\nexport Use[X: b.Show](x: &X): int {\n    return x.Show()\n}\n\n" +
			"export UseU(u: &b.U): int {\n    return Use(u)\n}\n\nexport Other(): int {\n    return 1\n}\n",
	}
}

// TestFuncReuseImplReads (the impl row of the invalidation table, per
// function, implreads.go): an impl added for T misses a (b's interface
// changed) but rechecks none of a's functions: UseU's impl read of (Show, U)
// still holds, since U's impl set is unchanged. Before implreads.go every
// function of a was rechecked. (b's impl method for U is rechecked too, b
// being the edited module.)
func TestFuncReuseImplReads(t *testing.T) {
	warm, cold := warmAfter(t, implPkg(), "b/b.mk", func(s string) string {
		return s + "\nimpl Show for T {\n    Show(&self): int {\n        return self.a\n    }\n}\n"
	})
	clean(t, cold)
	if !bytes.Equal(jsonOf(t, warm), jsonOf(t, cold)) {
		t.Errorf("warm --json %s, cold %s", jsonOf(t, warm), jsonOf(t, cold))
	}
	if got := pkgOnly(warm.Trace.ModulesResolved); !slices.Equal(got, []string{"a", "b"}) {
		t.Errorf("missed %q, want [a b]", got)
	}
	for _, k := range pkgItems(warm.Trace.ItemsChecked) {
		if strings.HasPrefix(k, "a.") {
			t.Errorf("rechecked %s (from cache %q)", k, pkgItems(warm.Trace.ItemsFromCache))
		}
	}
	if got := pkgItems(warm.Trace.ItemsFromCache); !slices.Contains(got, "a.UseU") {
		t.Errorf("from cache %q, want a.UseU among them", got)
	}
	// An impl method is reused like any function (its parent is the impl):
	// b's own impl Show for U is not rechecked.
	if got := pkgItems(warm.Trace.ItemsFromCache); !slices.Contains(got, "b#impl(b.Show for b.U):Show") {
		t.Errorf("from cache %q, want b's impl Show for U method among them", got)
	}
	t.Logf("checked %q, from cache %q", pkgItems(warm.Trace.ItemsChecked), pkgItems(warm.Trace.ItemsFromCache))
}

// TestImplSetFollowsImpls: the impl set of (Show, T) changes when an impl
// Show for T is added, the set of (Show, U) does not, and a target whose
// constructor cannot be read ("&T") takes every impl of Show.
func TestImplSetFollowsImpls(t *testing.T) {
	sets := func(files map[string]string) map[string]source.Hash {
		s, err := NewSession(Config{Root: writeTree(t, files), CacheDir: t.TempDir(), Target: types.X86_64Windows})
		if err != nil {
			t.Fatal(err)
		}
		clean(t, s.Check())
		a := s.pkgB.byName["a"]
		fw := s.w.front.world(s.w, a, s.w.sem5.hash, nil).(*frontWorld)
		out := map[string]source.Hash{}
		// TypeString spells a named type by its name alone, as check's
		// impl reads do.
		for _, target := range []string{"T", "U", "&T"} {
			h, ok := fw.implSet(types.ItemKey("b", "Show"), target)
			if !ok {
				t.Fatalf("no impl set for %s", target)
			}
			out[target] = h
		}
		return out
	}
	files := implPkg()
	before := sets(files)
	files["b/b.mk"] += "\nimpl Show for T {\n    Show(&self): int {\n        return self.a\n    }\n}\n"
	after := sets(files)
	if before["U"] != after["U"] {
		t.Error("the impl set of (Show, U) changed")
	}
	if before["T"] == after["T"] || before["&T"] == after["&T"] {
		t.Error("the impl set of (Show, T) or (Show, &T) did not change")
	}
	for s, want := range map[string]string{"b.T": "b.T", "std.core.List[int]": "std.core.List", "int": "int", "&b.T": "", "[]u8": "", "(int, str)": ""} {
		if got, _ := targetCtor(s); got != want {
			t.Errorf("targetCtor(%q) = %q, want %q", s, got, want)
		}
	}
}
