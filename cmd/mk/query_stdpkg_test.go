// Why: `mk query` should answer std's names even where there is no package,
// as in an empty scratch directory.
// What: TestQueryStdWithoutPackage.
// How: runs mk query members in an empty directory, over a one-file root of
// its own, and checks the PriorityQueue head; an explicit --root to such a
// directory is still a usage error.

package main

import (
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
