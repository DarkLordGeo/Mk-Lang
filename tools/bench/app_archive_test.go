// Why: every column of the app/archive row must refuse a broken archive (the
// row's README), and the Go twin is the one this package can build and run.
// What: TestAppArchiveRefusals.
// How: the row's tamper tool (tests/bench/app/archive/testdata/tamper.go)
// breaks a small archive the twin wrote; the twin's open must refuse each
// copy with an error of its own and exit 1.

package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestAppArchiveRefusals: the twin reads the payload without preallocating
// the manifest's total. The case that tells the two reads apart is a total
// of 2^62 (huge, and total at 2^62): make cannot allocate it and panics with
// exit 2, the limited read refuses with exit 1. A total of 2^30 is refused
// by both and checks only the message; a total of 2^45, which makes a
// preallocating read commit memory to the machine's limit, is run by hand
// only.
func TestAppArchiveRefusals(t *testing.T) {
	root := repoRootForTest(t)
	src := filepath.Join(root, "tests", "bench", "app", "archive", "testdata")
	dir := t.TempDir()
	goExe := filepath.Join(runtime.GOROOT(), "bin", "go")
	exe := func(name string) string {
		if runtime.GOOS == "windows" {
			return name + ".exe"
		}
		return name
	}
	archive, tamper := filepath.Join(dir, exe("archive")), filepath.Join(dir, exe("tamper"))
	for _, b := range [][2]string{{archive, "archive.go"}, {tamper, "tamper.go"}} {
		cmd := exec.Command(goExe, "build", "-o", b[0], filepath.Join(src, b[1]))
		cmd.Env = append(os.Environ(), "GOFLAGS=")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("go build %s: %v\n%s", b[1], err, out)
		}
	}
	tree := filepath.Join(dir, "tree")
	for name, text := range map[string]string{
		"a.txt":       strings.Repeat("alpha\n", 300),
		"b/c.txt":     strings.Repeat("the quick brown fox\n", 200),
		"b/.hidden":   "",
		"b/d/e.bin":   "\x00\x01\x02\xff",
		"Z.txt":       "capital\r\n",
		"b/d/f12.txt": strings.Repeat("0123456789", 50),
	} {
		writeFile(t, filepath.Join(tree, filepath.FromSlash(name)), text)
	}
	good := filepath.Join(dir, "good.mka")
	run := func(args ...string) (string, string, int) {
		cmd := exec.Command(archive, args...)
		var stdout, stderr strings.Builder
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		err := cmd.Run()
		code := 0
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			code = ee.ExitCode()
		} else if err != nil {
			t.Fatal(err)
		}
		return stdout.String(), stderr.String(), code
	}
	if _, e, code := run("create", tree, good); code != 0 {
		t.Fatalf("create: exit %d: %s", code, e)
	}
	out, e, code := run("open", good)
	if code != 0 || !strings.HasSuffix(out, "ok: 6 files, 6313 bytes\n") {
		t.Fatalf("open of the good archive: exit %d, stdout %q, stderr %q", code, out, e)
	}
	cases := []struct {
		mode []string
		err  string
	}{
		{[]string{"seal"}, "the seal does not open"},
		{[]string{"sig"}, "a bad signature"},
		{[]string{"data"}, "a file's digest differs"},
		{[]string{"long"}, "longer than the manifest's size"},
		{[]string{"short"}, "a cut archive"},
		{[]string{"mtop"}, "a cut archive"},
		{[]string{"neg"}, "a bad manifest line"},
		{[]string{"wrap"}, "value out of range"},
		{[]string{"i64sum"}, "a bad manifest line"},
		{[]string{"huge"}, "shorter than the manifest's size"},
		{[]string{"total", fmt.Sprint(int64(1) << 62)}, "shorter than the manifest's size"},
		{[]string{"total", fmt.Sprint(int64(1) << 30)}, "shorter than the manifest's size"},
	}
	for _, c := range cases {
		name := strings.Join(c.mode, "-")
		bad := filepath.Join(dir, "bad-"+name+".mka")
		cmd := exec.Command(tamper, append([]string{good, bad}, c.mode...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("tamper %s: %v\n%s", name, err, out)
		}
		out, e, code := run("open", bad)
		if code != 1 || !strings.HasPrefix(e, "archive: ") || !strings.Contains(e, c.err) || out != "" {
			t.Errorf("%s: exit %d, stderr %q, stdout %q; want exit 1 with %q", name, code, e, out, c.err)
		}
	}
	// The empty archive is valid, and the tool refuses an unknown mode.
	empty := filepath.Join(dir, "empty.mka")
	if out, err := exec.Command(tamper, good, empty, "empty").CombinedOutput(); err != nil {
		t.Fatalf("tamper empty: %v\n%s", err, out)
	}
	if out, e, code := run("open", empty); code != 0 || out != "ok: 0 files, 0 bytes\n" {
		t.Errorf("empty: exit %d, stdout %q, stderr %q", code, out, e)
	}
	if err := exec.Command(tamper, good, empty, "nosuch").Run(); err == nil {
		t.Error("tamper accepted an unknown mode")
	}
}
