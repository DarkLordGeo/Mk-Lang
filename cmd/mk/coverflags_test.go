// Why: mk test --cover's flag mistakes must fail loudly, not run silently.
// What: TestCoverFlagEdges: --run matching nothing says so, and the refused
// combinations exit 2.
// How: runs mk test --cover with --run matching nothing (exit 0, a note on
// stderr) and with --fuzz (exit 2).

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCoverFlagEdges(t *testing.T) {
	dir := t.TempDir()
	src := "F(): int {\n    return 1\n}\n\ntest \"f\" {\n    Assert(F() == 1)\n}\n"
	if err := os.WriteFile(filepath.Join(dir, "main.mk"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	var so, se bytes.Buffer
	if st := run([]string{"test", "--cover", "--run", "nothing-matches", dir}, &so, &se); st != 0 || !strings.Contains(se.String(), "no test matched; no coverage written") {
		t.Errorf("--run matching nothing: status %d, stderr %q (red before: silent)", st, se.String())
	}
	se.Reset()
	if st := run([]string{"test", "--cover", "--fuzz", "f", dir}, &so, &se); st != 2 || !strings.Contains(se.String(), "--cover with --fuzz") {
		t.Errorf("--cover --fuzz: status %d, stderr %q", st, se.String())
	}
}
