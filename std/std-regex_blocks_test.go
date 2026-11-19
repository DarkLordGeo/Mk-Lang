// Why: std/regex/tests_regex.mk checks internals no exported call shows
// (both DFAs, the reversed class, the backtracker's hand-offs to the Pike VM).
// What: a runner for mk test --std=std.regex.
// How: every block, in dev and release builds.

package std

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSTDREGEX_TestBlocks: every block of tests_regex.mk passes, dev and
// release.
func TestSTDREGEX_TestBlocks(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("regex", "tests_regex.mk"))
	if err != nil {
		t.Fatal(err)
	}
	names := testNames(string(src))
	if len(names) < 7 {
		t.Fatalf("tests_regex.mk has %d blocks, want at least 7", len(names))
	}
	for _, flags := range [][]string{nil, {"--release"}} {
		root := t.TempDir()
		stdDir := regexStdCopy(t, root)
		out, _ := mkCmd(t, root, stdDir, append([]string{"test", "--std=std.regex"}, flags...)...)
		if strings.Contains(out, "no usable LLVM") {
			t.Skipf("needs the LLVM toolchain: %s", out)
		}
		res := parseStdTest(out, "std.regex")
		for _, n := range names {
			if r, ok := res[n]; !ok || !r.pass {
				t.Errorf("%v: block %q: %+v\n%s", flags, n, r, out)
			}
		}
	}
}
