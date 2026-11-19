// Why: a SIMD cell that computes the wrong answer fast must be caught in
// the ordinary test run, not only by `verify -table simd` at publish time.
// What: TestSIMDMkCellsQuickChecksum, the differential test of the SIMD
// table's Mk cells.
// How: each cell a tree SIMD row publishes, chosen as expand chooses it
// from the variant's sources and skips, is built with the row's own mk line
// at the variant's CPU and must print the variant's quick checksum, the
// value the C, Rust, Zig and Go columns are checked against.

package main

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// TestSIMDMkCellsQuickChecksum builds every distinct (row, Mk language,
// package, CPU) of the tree's SIMD rows once and runs each variant that
// names it at its quick arguments.
func TestSIMDMkCellsQuickChecksum(t *testing.T) {
	type cell struct{ dir, lang, pkg, cpu string }
	groups := map[cell][]*row{}
	var order []cell
	for _, r := range treeRowsAll(t) {
		if r.Lane != "" || r.Table != "simd" {
			continue
		}
		for lang, pkg := range r.Sources {
			if !isMkLang(lang) {
				continue
			}
			if r.QuickSum == "" {
				t.Errorf("%s: the %s cell has no quick_checksum", r.id(), lang)
				continue
			}
			k := cell{r.Dir, lang, pkg, r.cpu()}
			if _, ok := groups[k]; !ok {
				order = append(order, k)
			}
			groups[k] = append(groups[k], r)
		}
	}
	if len(order) == 0 {
		t.Fatal("no SIMD row has an Mk cell")
	}
	if testing.Short() {
		t.Skip("builds the tree's mk and every SIMD row's Mk cell")
	}
	tc, err := findToolchain(defaultWork(), "")
	if err != nil {
		t.Fatal(err)
	}
	if tc.Go == "" || tc.Clang == "" {
		t.Skip("go or clang not found")
	}
	wd, _ := os.Getwd()
	root, err := repoRoot(wd)
	if err != nil {
		t.Fatal(err)
	}
	tag := "test-simdcells-" + strconv.Itoa(os.Getpid())
	t.Cleanup(func() { os.RemoveAll(filepath.Join(tc.Work, "bin", tag)) })
	if tc.Mk, err = testMkExe(t, tc, root, tag); err != nil {
		t.Fatal(err)
	}
	slices.SortFunc(order, func(a, b cell) int {
		return strings.Compare(a.dir+"|"+a.lang+"|"+a.pkg+"|"+a.cpu, b.dir+"|"+b.lang+"|"+b.pkg+"|"+b.cpu)
	})
	runs := 0
	for _, k := range order {
		rs := groups[k]
		bs := buildAll(tc, tag, rs[:1], func(c column) bool { return c.Lang == k.lang && !c.Budget }, func(string) {})
		if len(bs) != 1 {
			t.Errorf("%s %s: built %d columns, want 1", rs[0].id(), k.lang, len(bs))
			continue
		}
		if bs[0].Err != nil {
			t.Errorf("%s %s (%s at %s): %v", rs[0].id(), k.lang, k.pkg, k.cpu, bs[0].Err)
			continue
		}
		for _, r := range rs {
			res, got, ok := verify(bs, r.Quick, r.QuickSum)
			runs++
			if !ok {
				t.Errorf("%s %s (%s at %s), quick %v: printed %q (error %v), want %q", r.id(), k.lang, k.pkg, k.cpu, r.Quick, res[0].Sum, res[0].Err, r.QuickSum)
				continue
			}
			t.Logf("%s %s (%s at %s), quick %v: %s", r.id(), k.lang, k.pkg, k.cpu, r.Quick, got)
		}
	}
	t.Logf("%d cells built, %d variants run", len(order), runs)
}
