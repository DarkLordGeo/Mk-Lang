// Why: std.simd lies outside the prelude's import closure, so its
// KnownItems fields could stay NoItem when startSemantics read Known before
// std.simd had items.
// What: TestSimdKnownItemsInDriverBuild, TestNoSimdModuleLeavesFieldsNoItem
// and TestSimdKnownItemsInCorpusSession, over simdStdFiles and wantSimdKnown.
// How: builds against a stand-in std with std.simd and checks the fields
// are set once std.simd is resolved (refreshKnown), cold and from the
// module cache.

package driver

import (
	"path/filepath"
	"slices"
	"testing"

	"mk/internal/types"
)

// simdStdFiles is the stand-in std with a std.simd holding the four items
// KnownItems names, declared as std/simd/vec.mk declares them.
func simdStdFiles() map[string]string {
	files := fakeStdFiles()
	files["simd/vec.mk"] = "export interface Lane {\n}\n\n" +
		"export struct Vec[T: Lane, const N: int] {\n    lanes: [N]T\n}\n\n" +
		"export struct Mask[T: Lane, const N: int] {\n    lanes: [N]T\n}\n\n" +
		"export struct CpuFeatures {\n    bits: [2]u64\n}\n"
	return files
}

// wantSimdKnown checks the four fields against the items' stable keys.
func wantSimdKnown(t *testing.T, s *Session, set bool) {
	t.Helper()
	k := s.w.sem5.knownCopy()
	for _, f := range []struct {
		name string
		got  types.ItemID
	}{{"Vec", k.Vec}, {"Mask", k.Mask}, {"Lane", k.Lane}, {"CpuFeatures", k.CpuFeatures}} {
		id, ok := s.w.items.ByKey(types.ItemKey("std.simd", f.name))
		switch {
		case set && (!ok || f.got != id):
			t.Errorf("KnownItems.%s = %v, want std.simd.%s (%v, found %v)", f.name, f.got, f.name, id, ok)
		case !set && f.got != types.NoItem:
			t.Errorf("KnownItems.%s = %v with no std.simd", f.name, f.got)
		}
	}
}

func TestSimdKnownItemsInDriverBuild(t *testing.T) {
	stdDir := writeTree(t, simdStdFiles())
	root := writeTree(t, pkgFiles(map[string]string{"a/a.mk": "import std.simd\n\n" +
		"export Same(v: simd.Vec[i32, 4]): simd.Vec[i32, 4] {\n    return v\n}\n"}))
	cfg := Config{Root: root, StdDir: stdDir, CacheDir: filepath.Join(t.TempDir(), "cache")}
	for _, run := range []string{"cold", "warm"} {
		t.Run(run, func(t *testing.T) {
			s, err := NewSession(cfg)
			if err != nil {
				t.Fatal(err)
			}
			r := s.Check()
			clean(t, r)
			// The warm build takes std.simd from the cache: the refresh after
			// a module hit's decode is the path under test there.
			if hit := slices.Contains(r.Trace.ModulesFromCache, "std.simd"); hit != (run == "warm") {
				t.Errorf("%s: std.simd from the cache %v (resolved %q, from cache %q)", run, hit, r.Trace.ModulesResolved, r.Trace.ModulesFromCache)
			}
			wantSimdKnown(t, s, true)
		})
	}
}

func TestNoSimdModuleLeavesFieldsNoItem(t *testing.T) {
	s, err := NewSession(Config{Root: writeTree(t, pkgFiles(map[string]string{"a/a.mk": "export F(): int {\n    return 1\n}\n"})),
		StdDir: fakeStd(t), NoCache: true})
	if err != nil {
		t.Fatal(err)
	}
	clean(t, s.Check())
	wantSimdKnown(t, s, false)
}

func TestSimdKnownItemsInCorpusSession(t *testing.T) {
	c, err := NewCorpusSession(Config{StdDir: writeTree(t, simdStdFiles())})
	if err != nil {
		t.Fatal(err)
	}
	k := c.w.sem5.knownCopy()
	if id, ok := c.w.items.ByKey(types.ItemKey("std.simd", "Vec")); !ok || k.Vec != id || k.Mask == types.NoItem ||
		k.Lane == types.NoItem || k.CpuFeatures == types.NoItem {
		t.Errorf("corpus session KnownItems: Vec %v Mask %v Lane %v CpuFeatures %v", k.Vec, k.Mask, k.Lane, k.CpuFeatures)
	}
}
