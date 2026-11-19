// Why: the package commands register themselves outside main.go's table, so
// this checks that the command line reaches them.
// What: TestPkgCommandsRegistered.
// How: each command with wrong arguments exits 2; mk new and mk tree run on a
// fresh package. Their logic is tested in internal/driver (pkgcmd_test.go).

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestPkgCommandsRegistered: the package commands register themselves (no
// row in main.go's table) and take their arguments; their logic is tested in
// internal/driver (pkgcmd_test.go).
func TestPkgCommandsRegistered(t *testing.T) {
	for _, args := range [][]string{{"new"}, {"install"}, {"remove"}, {"why"}, {"tree", "a", "b"}, {"update", "a", "b", "c"}} {
		if st, _, errOut := mk(args...); st != 2 || errOut == "" {
			t.Errorf("mk %s: status %d stderr %q", strings.Join(args, " "), st, errOut)
		}
	}
	dir := t.TempDir()
	t.Chdir(dir)
	st, out, errOut := mk("new", "hello")
	if st != 0 || !strings.Contains(out, "created") {
		t.Fatalf("mk new: %d %q %q", st, out, errOut)
	}
	if _, err := os.Stat(filepath.Join(dir, "hello", "src", "main.mk")); err != nil {
		t.Error(err)
	}
	if st, out, _ := mk("tree", filepath.Join(dir, "hello")); st != 0 || strings.TrimSpace(out) != "hello" {
		t.Errorf("mk tree: %d %q", st, out)
	}
}
