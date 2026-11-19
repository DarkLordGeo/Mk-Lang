// Why: an error from mk new should name the command once, not twice.
// What: TestNew_ErrorPrefixOnce.
// How: runs mk new on a directory that exists and counts "mk new:" in stderr.

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNew_ErrorPrefixOnce(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "taken"), 0o755); err != nil {
		t.Fatal(err)
	}
	wd, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(wd)
	st, _, errOut := mk("new", "taken")
	if st == 0 || strings.Count(errOut, "mk new:") != 1 {
		t.Errorf("status %d stderr %q", st, errOut)
	}
}
