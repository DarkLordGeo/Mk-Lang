// Why: Windows' PATH search has two rules that are easy to miss: a ';' inside
// a quoted entry, and a PATHEXT entry written without its dot.
// What: TestProc_QuotedPathEntry and TestProc_PathextNoDot.
// How: Go's os/exec is the oracle (filepath.SplitList, exec.LookPath's
// pathExt): the same cases through proc.Spawn and exec.Command answer alike.

package std

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// proc3SearchCase runs cases through the spawner and os/exec, with PATH
// and PATHEXT as given (an empty pathext leaves it unset), and fails at
// the first case they answer differently. It returns the spawner's output.
func proc3SearchCase(t *testing.T, spawner, dir, path, pathext string, cases []proc3Case) string {
	t.Helper()
	t.Setenv("PATH", path)
	if pathext != "" {
		t.Setenv("PATHEXT", pathext)
	}
	got := proc3RunSpawner(t, spawner, dir, "args", cases)
	old, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	want := proc3Oracle(t, "args", cases)
	if err := os.Chdir(old); err != nil {
		t.Fatal(err)
	}
	proc3Diff(t, "search", got, want, cases)
	return got
}

// TestProc_QuotedPathEntry: a quoted PATH entry holding a ';' is one
// directory. "<bin>;x" (quoted) names no directory, so p3found in bin is
// not found through it; a directory whose name holds a ';', quoted in
// PATH, is searched whole.
func TestProc_QuotedPathEntry(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows only")
	}
	dir, helper, spawner := proc3Tools(t)
	b, err := os.ReadFile(helper)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	semi := filepath.Join(root, "a;b")
	empty := filepath.Join(root, "empty")
	for _, d := range []string{bin, semi, empty} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(bin, "p3found.exe"), b, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(semi, "p3semi.exe"), b, 0o755); err != nil {
		t.Fatal(err)
	}
	sys := os.Getenv("PATH")
	cases := []proc3Case{
		{"p3found", "args", "q"},
		{"p3found.exe", "args", "q"},
	}
	got := proc3SearchCase(t, spawner, dir, `"`+bin+`;x";`+empty+";"+sys, "", cases)
	if got != "=== error NotFound\n=== error NotFound\n" {
		t.Fatalf("a quoted entry was split at its ';':\n%s", got)
	}
	cases = []proc3Case{
		{"p3semi", "args", "s"},
		{"p3found", "args", "f"},
	}
	got = proc3SearchCase(t, spawner, dir, `"`+semi+`";"`+bin+`";`+sys, "", cases)
	if strings.Contains(got, "error") {
		t.Fatalf("a quoted entry was not searched:\n%s", got)
	}
}

// TestProc_PathextNoDot: PATHEXT entries written without a dot are
// used with one, and a PATHEXT with no entry at all tries the name as
// written only.
func TestProc_PathextNoDot(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows only")
	}
	dir, helper, spawner := proc3Tools(t)
	b, err := os.ReadFile(helper)
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "p3found.exe"), b, 0o755); err != nil {
		t.Fatal(err)
	}
	path := bin + ";" + os.Getenv("PATH")
	cases := []proc3Case{{"p3found", "args", "e"}}
	got := proc3SearchCase(t, spawner, dir, path, "EXE;.BAT", cases)
	if got != "=== 0\nn=1\n65\n" {
		t.Fatalf("PATHEXT=EXE did not find p3found.exe:\n%s", got)
	}
	cases = []proc3Case{{"p3found", "args"}, {"p3found.exe", "args"}}
	proc3SearchCase(t, spawner, dir, path, ";", cases)
}
