// Why: a local whose initializer both arms of the next if overwrite is dead
// code that mk fix's value-arm round misses.
// What: TestRwIo_NoDeadInitializerBeforeTwoArms over std's I/O modules.
// How: runs deadInitSites (rw-core_docs_test.go) over every library file.

package std

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRwIo_NoDeadInitializerBeforeTwoArms runs rw-core_docs_test.go's detector
// (deadInitSites: a local whose initializer both arms of the next if
// overwrite, which mk fix's value-arm round misses) over the directories
// of fs, net, proc, log, cli, http and sim, so the shape cannot hide
// behind a clean mk fix run.
func TestRwIo_NoDeadInitializerBeforeTwoArms(t *testing.T) {
	t.Parallel()
	for _, dir := range []string{"fs", "net", "proc", "log", "cli", "http", "sim"} {
		err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(p, ".mk") {
				return err
			}
			lines := strings.Split(strings.ReplaceAll(readText(t, filepath.ToSlash(p)), "\r\n", "\n"), "\n")
			for _, n := range deadInitSites(lines) {
				t.Errorf("%s:%d: this local is chosen by the if below and assigned nowhere else: write x := if ... { ... } else { ... } (J7)", filepath.ToSlash(p), n)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}
