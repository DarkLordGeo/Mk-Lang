// Why: a reader who follows an invariant's comment to its test file must
// find that file.
// What: TestRwRt_CitedTestFilesExist over the runtime modules (rwRtModules).
// How: every tests_*.mk name in a comment of those modules must exist in the
// module, or under std when the comment names another module's.

package std

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// rwRtModules are the runtime's std modules.
var rwRtModules = []string{"rt", "task", "sys", "free", "atomic", "sync", "time", "mmio"}

// rwRtMkFiles is every .mk file under rwRtModules, as paths relative
// to std with forward slashes.
func rwRtMkFiles(t *testing.T) []string {
	t.Helper()
	var files []string
	for _, m := range rwRtModules {
		err := filepath.WalkDir(m, func(p string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() && strings.HasSuffix(p, ".mk") {
				files = append(files, filepath.ToSlash(p))
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	sort.Strings(files)
	return files
}

var rwRtTestsRef = regexp.MustCompile(`\btests_\w+\.mk\b`)

// TestRwRt_CitedTestFilesExist: every tests_*.mk file named in a comment
// of rwRtModules exists in that module, or under std when the
// comment names another module's.
func TestRwRt_CitedTestFilesExist(t *testing.T) {
	have := map[string]bool{}
	err := filepath.WalkDir(".", func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasPrefix(d.Name(), "tests_") && strings.HasSuffix(p, ".mk") {
			have[d.Name()] = true
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range rwRtMkFiles(t) {
		for i, l := range rtsReadLines(t, p) {
			c := strings.Index(l, "//")
			if c < 0 {
				continue
			}
			for _, n := range rwRtTestsRef.FindAllString(l[c:], -1) {
				if !have[n] {
					t.Errorf("%s:%d cites %s, which no std module has", p, i+1, n)
				}
			}
		}
	}
}
