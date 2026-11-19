// Why: a build that one test makes and later tests reuse cannot be removed by any
// single test, and leaked build directories once filled the disk.
// What: packageTempDir and its tempRegistry, TestPackageTempDirsAreRemoved
// and TestNoUnownedTempDirs.
// How: directories live as long as the test binary; TestMain removes them
// after m.Run, so a run leaves nothing under TMP.

package std

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
)

// tempRegistry holds the directories made by its mkdir until removeAll.
type tempRegistry struct {
	sync.Mutex
	dirs []string
}

// mkdir makes a new directory under the OS temp directory, named from
// prefix, and records it for removeAll.
func (r *tempRegistry) mkdir(prefix string) (string, error) {
	d, err := os.MkdirTemp("", prefix)
	if err != nil {
		return "", err
	}
	r.Lock()
	r.dirs = append(r.dirs, d)
	r.Unlock()
	return d, nil
}

// removeAll removes every recorded directory and forgets them.
func (r *tempRegistry) removeAll() {
	r.Lock()
	dirs := r.dirs
	r.dirs = nil
	r.Unlock()
	for _, d := range dirs {
		os.RemoveAll(d)
	}
}

// packageTemps is the registry TestMain empties after every test has run.
var packageTemps tempRegistry

// packageTempDir is os.MkdirTemp("", prefix) for a directory shared past
// the test that made it; TestMain removes it.
func packageTempDir(prefix string) (string, error) {
	return packageTemps.mkdir(prefix)
}

func TestPackageTempDirsAreRemoved(t *testing.T) {
	var r tempRegistry
	var made []string
	for _, p := range []string{"mksd7a", "mksd7b"} {
		d, err := r.mkdir(p)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, "f"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		made = append(made, d)
	}
	r.removeAll()
	for _, d := range made {
		if _, err := os.Stat(d); !os.IsNotExist(err) {
			t.Errorf("%s still exists after removeAll (err %v)", d, err)
		}
	}
	if len(r.dirs) != 0 {
		t.Errorf("removeAll left %d recorded directories", len(r.dirs))
	}
}

// TestNoUnownedTempDirs: every os.MkdirTemp("", ...) in this package's
// tests is either packageTempDir's, removed by TestMain, or followed by a
// Cleanup or deferred RemoveAll within a few lines.
func TestNoUnownedTempDirs(t *testing.T) {
	files, err := filepath.Glob("*_test.go")
	if err != nil {
		t.Fatal(err)
	}
	mk := regexp.MustCompile(`os\.MkdirTemp\(""`)
	for _, f := range files {
		if f == "std-defects_tempdir_test.go" {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n")
		for i, line := range lines {
			if !mk.MatchString(line) || strings.HasPrefix(strings.TrimSpace(line), "//") {
				continue
			}
			end := min(i+8, len(lines))
			owned := false
			for _, l := range lines[i:end] {
				if strings.Contains(l, "RemoveAll(") {
					owned = true
				}
			}
			if !owned {
				t.Errorf("%s:%d: os.MkdirTemp with no RemoveAll near it: use packageTempDir, t.TempDir, or remove it", f, i+1)
			}
		}
	}
}
