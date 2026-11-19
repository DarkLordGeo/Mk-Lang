// Why: package trees in the cache grow with every dependency version, and
// only the trees a pkg.sum still names are worth keeping.
// What: TestCachePrunePackages.
// How: puts one package tree in a temporary cache; mk cache size counts it,
// prune without --packages is a usage error, and prune --packages over a
// directory with no pkg.sum removes it.

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCachePrunePackages: mk cache size reports package trees apart, and mk
// cache prune --packages removes the trees no pkg.sum under the named
// directories names.
func TestCachePrunePackages(t *testing.T) {
	cache := filepath.Join(t.TempDir(), "c")
	t.Setenv("MK_CACHE", cache)
	tree := filepath.Join(cache, "pkg", strings.Repeat("ab", 32))
	if err := os.MkdirAll(tree, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tree, "pkg.mk"), []byte("x"), 0o444); err != nil {
		t.Fatal(err)
	}
	root := writeFiles(t, map[string]string{"main.mk": "export Main(sys: &System): int {\n    return 0\n}\n"})
	if st, out, _ := mk("cache", "size", root); st != 0 || !strings.Contains(out, "packages 1 trees 1 bytes") {
		t.Errorf("mk cache size: status %d stdout %q", st, out)
	}
	if st, _, errOut := mk("cache", "prune"); st != 2 || !strings.Contains(errOut, "--packages") {
		t.Errorf("mk cache prune: status %d stderr %q", st, errOut)
	}
	st, out, errOut := mk("cache", "prune", "--packages", t.TempDir())
	if st != 0 || out != "removed "+strings.Repeat("ab", 32)+"\n" || errOut != "" {
		t.Errorf("mk cache prune --packages: status %d stdout %q stderr %q", st, out, errOut)
	}
	if _, err := os.Stat(tree); !os.IsNotExist(err) {
		t.Errorf("the tree is still there: %v", err)
	}
}
