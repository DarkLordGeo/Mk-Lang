// Why: the gate runs `mk corpus --all --strict -v`, so --strict must turn
// an unexplained PENDING into a failing exit status.
// What: tests of `mk corpus --strict` through a freshly built mk, with
// buildMk, runMk and missingStdModule.
// How: a one-block root whose block imports a std module std lacks.
//
// `mk corpus --strict` is --no-pending, so a PENDING unit that pending.txt
// does not explain is FAIL and the exit status is 1, and a root with no
// pending unit still exits 0.

package corpus

import (
	"bytes"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// buildMk builds this repository's mk for the host into a temporary
// directory.
func buildMk(t *testing.T, root string) string {
	t.Helper()
	gobin := goCommand(t, "to build mk")
	exe := filepath.Join(t.TempDir(), "mk.exe")
	cmd := exec.Command(gobin, "build", "-o", exe, "./cmd/mk")
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build ./cmd/mk: %v\n%s", err, out)
	}
	return exe
}

// runMk runs mk with args and returns its stdout, stderr and exit status.
func runMk(t *testing.T, exe string, args ...string) (stdout, stderr string, status int) {
	t.Helper()
	var o, e bytes.Buffer
	cmd := exec.Command(exe, args...)
	cmd.Stdout, cmd.Stderr = &o, &e
	err := cmd.Run()
	if x, ok := err.(*exec.ExitError); ok {
		return o.String(), e.String(), x.ExitCode()
	}
	if err != nil {
		t.Fatalf("mk %v: %v", args, err)
	}
	return o.String(), e.String(), 0
}

// missingStdModule is a std module the reference lists that the
// embedded std does not declare yet, or "".
func missingStdModule(t *testing.T) string {
	for _, m := range designRun(t).Std.State.Missing {
		if strings.HasPrefix(m, "std.") && !strings.ContainsAny(m, " ()") {
			return m
		}
	}
	return ""
}

func TestCorpusStrictCommand(t *testing.T) {
	exe := buildMk(t, repoRoot(t))

	t.Run("nothing-pending", func(t *testing.T) {
		root := unitRoot(t, "A() { }\n")
		plain, _, st := runMk(t, exe, "corpus", "--root", root)
		for _, flag := range []string{"--strict", "--no-pending"} {
			out, errOut, status := runMk(t, exe, "corpus", flag, "-v", "--root", root)
			if status != 0 || errOut != "" || out != plain || st != 0 || !strings.Contains(out, "pass 2, fail 0, pending 0") {
				t.Errorf("mk corpus %s -v: exit %d (plain %d), stderr %q\nstdout %q\nplain  %q", flag, status, st, errOut, out, plain)
			}
		}
	})

	t.Run("unexplained-pending", func(t *testing.T) {
		m := missingStdModule(t)
		if m == "" {
			t.Skip("the embedded std declares every module the reference lists, so no unit is pending on std; the fake-compiler half is TestNoPendingFailsUnlisted")
		}
		// An ok block importing a std module std lacks is PENDING
		// "std: <module> undeclared" (rule 7).
		root := unitRoot(t, "import "+m+"\n\nA() { }\n")
		out, errOut, status := runMk(t, exe, "corpus", "--root", root)
		want := "doc/reference.md:4 ok PENDING std: " + m + " undeclared"
		if status != 0 || errOut != "" || !strings.Contains(out, want+"\n") || !strings.Contains(out, "pass 1, fail 0, pending 1 (listed 0)") {
			t.Fatalf("mk corpus: exit %d, stderr %q, stdout %q; want exit 0 and %q", status, errOut, out, want)
		}
		var strictOut string
		for _, flag := range []string{"--strict", "--no-pending"} {
			out, errOut, status := runMk(t, exe, "corpus", flag, "--root", root)
			want := "doc/reference.md:4 ok FAIL unexplained pending: std: " + m + " undeclared\n"
			if status != 1 || errOut != "" || !strings.Contains(out, want) || !strings.Contains(out, "pass 1, fail 1, pending 0 (listed 0)") {
				t.Errorf("mk corpus %s: exit %d, stderr %q, stdout %q; want exit 1 and %q", flag, status, errOut, out, want)
			}
			if flag == "--strict" {
				strictOut = out
			} else if out != strictOut {
				t.Errorf("--strict and --no-pending reports differ:\n%s", firstDiff([]byte(strictOut), []byte(out)))
			}
		}
	})
}
