// Why: a request id in a comment names process, not a rule, and tells a
// reader nothing.
// What: TestCoreNew_NoRequestIdsInComments over std/core, std/mem and
// std/raw; TestStdNoProcessResidue scans the whole of std.
// How: fails on any comment that matches coreNewRequestID, `R-` and a number.

package std

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var coreNewRequestID = regexp.MustCompile(`\bR-[0-9]+\b`)

func TestCoreNew_NoRequestIdsInComments(t *testing.T) {
	t.Parallel()
	for _, dir := range []string{"core", "mem", "raw"} {
		files, err := filepath.Glob(filepath.Join(dir, "*.mk"))
		if err != nil || len(files) == 0 {
			t.Fatalf("%s: no .mk files (%v)", dir, err)
		}
		for _, f := range files {
			b, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			for i, line := range strings.Split(string(b), "\n") {
				c := strings.Index(line, "//")
				if c < 0 {
					continue
				}
				if id := coreNewRequestID.FindString(line[c:]); id != "" {
					t.Errorf("%s:%d: the comment cites the request id %s: say the reason instead", filepath.ToSlash(f), i+1, id)
				}
			}
		}
	}
}
