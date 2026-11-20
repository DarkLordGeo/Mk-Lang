// Why: `mk query` should answer std's names even where there is no package,
// as in an empty scratch directory.
// What: TestQueryStdWithoutPackage, TestQueryStdInBrokenPackage.
// How: runs mk query members in an empty directory, over a one-file root of
// its own, and checks the PriorityQueue head; an explicit --root to such a
// directory is still a usage error.

package main

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestQueryStdWithoutPackage: from a directory with no .mk file and no
// pkg.mk, `mk query members coll.PriorityQueue` lists the members, after
// the head with its bounds (`type PriorityQueue[T: Ord]`); it was
// "root . holds no .mk file and no pkg.mk". An explicit --root to such a
// directory is still the usage error.
func TestQueryStdWithoutPackage(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	st, out, errOut := mk("query", "members", "coll.PriorityQueue")
	if st != 0 || !strings.HasPrefix(out, "type PriorityQueue[T: Ord]\n") || !strings.Contains(out, "Push(mut self, x: T into self)") {
		t.Errorf("status %d stdout %q stderr %q", st, out, errOut)
	}
	if st, _, errOut := mk("query", "members", "coll.PriorityQueue", "--root", dir); st != 2 || !strings.Contains(errOut, "holds no .mk file") {
		t.Errorf("--root: status %d stderr %q", st, errOut)
	}
}

// TestQueryStdInBrokenPackage: in a package that does not parse, `find`
// and `members` answer std's names, with the package's diagnostics on
// stderr and a closing note; another query still says the package does not
// check far enough.
func TestQueryStdInBrokenPackage(t *testing.T) {
	f := filepath.Join(writeFiles(t, map[string]string{"main.mk": "export Main(sys: &System) {\n    x := (\n}\n"}), "main.mk")
	st, out, errOut := mk("query", "find", "append", "--root", f)
	if st != 0 || !strings.Contains(out, "List.Push  func  std.core") || !strings.Contains(out, "(the package does not parse: these are std's names alone; run mk check)") || !strings.Contains(errOut, "E0101") {
		t.Errorf("find: status %d stdout %q stderr %q", st, out, errOut)
	}
	if st, out, errOut := mk("query", "members", "coll.PriorityQueue", "--root", f, "--json"); st != 0 || !strings.Contains(out, "Push(mut self, x: T into self)") || !strings.Contains(out, `"std_only":true`) {
		t.Errorf("members: status %d stdout %q stderr %q", st, out, errOut)
	}
	if st, _, errOut := mk("query", "sig", "Main", "--root", f); st != 1 || !strings.Contains(errOut, "does not check far enough") {
		t.Errorf("sig: status %d stderr %q", st, errOut)
	}
}
