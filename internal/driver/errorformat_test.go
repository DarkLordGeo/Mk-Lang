// Why: an error returned from Main or a test must print with its fields, or
// the user sees main.Bad and not what went wrong.
// What: TestMainErrorPrintsItsFormat and
// TestTestErrorPrintsItsFormat, over notes10Source.
// How: runs programs whose Main fails with chosen errors and compares
// stderr and the exit status.

package driver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mk/internal/types"
)

// notes10Source fails Main with the error its first argument names, after
// writing a line of its own to stderr.
const notes10Source = `import std.fs

error Bad(code: i64)
error Quiet

export Main(sys: &System): Bad|Quiet|OutOfMemory|fs.Error!void {
    var w := sys.Stderr()
    _ = w.Str("before\n") else {}
    if sys.args.Len() > 1 {
        if sys.args[1] == "quiet" {
            fail Quiet
        }
        if sys.args[1] == "oom" {
            fail OutOfMemory
        }
        if sys.args[1] == "fs" {
            fail fs.NotFound
        }
    }
    try Seven()
}

Seven(): Bad!void {
    fail Bad(code: 7)
}
`

// TestMainErrorPrintsItsFormat: an error Main returns is
// reported as error: then its display name's module prefix, then the error
// as {e} formats it: main.Bad(code: 7), where Finish alone printed
// main.Bad. A fieldless error, a prelude error and a std error print as
// before; the program's own stderr comes first; the exit status stays 1. A
// Main of E!int keeps its int as the exit status. That dev's trace rows
// still follow the line, with no row for the wrapper, is
// TestSitePathInTraceRows's (trace-deep-dev.err).
func TestMainErrorPrintsItsFormat(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	src := filepath.Join(dir, "main.mk")
	if err := os.WriteFile(src, []byte(notes10Source), 0o644); err != nil {
		t.Fatal(err)
	}
	host := HostTarget()
	for _, prof := range []types.Profile{types.Dev, types.Release} {
		exe := buildProgram(t, src, host, prof, t.TempDir())
		for _, c := range []struct{ arg, line string }{
			{"", "error: main.Bad(code: 7)\n"},
			{"quiet", "error: main.Quiet\n"},
			{"oom", "error: OutOfMemory\n"},
			{"fs", "error: fs.NotFound\n"},
		} {
			var args []string
			if c.arg != "" {
				args = []string{c.arg}
			}
			_, errOut, code := runExe(t, host, exe, "", args...)
			if code != 1 || !strings.HasPrefix(errOut, "before\n"+c.line) {
				t.Errorf("%v %q: exit %d, stderr %q; want exit 1 and stderr starting %q", prof, c.arg, code, errOut, "before\n"+c.line)
			}
			if prof == types.Release && errOut != "before\n"+c.line {
				t.Errorf("%v %q: stderr %q; want only %q", prof, c.arg, errOut, "before\n"+c.line)
			}
		}
	}

	status := filepath.Join(t.TempDir(), "status.mk")
	if err := os.WriteFile(status, []byte("error Bad(code: i64)\n\nexport Main(sys: &System): Bad!int {\n    if sys.args.Len() > 1 {\n        fail Bad(code: 1)\n    }\n    return 5\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	exe := buildProgram(t, status, host, types.Release, t.TempDir())
	if out, errOut, code := runExe(t, host, exe, ""); code != 5 || out != "" || errOut != "" {
		t.Errorf("Main returning 5: exit %d, stdout %q, stderr %q; want exit 5 and no output", code, out, errOut)
	}
}

// TestTestErrorPrintsItsFormat: mk test reports a failing
// test's error the same way, through the runner's Main.
func TestTestErrorPrintsItsFormat(t *testing.T) {
	t.Parallel()
	src := "error Bad(code: i64)\n\ntest \"fails\" {\n    fail Bad(code: 7)\n}\n\ntest \"passes\" {\n    Assert(true)\n}\n"
	dir := testPkg(t, map[string]string{"t.mk": src})
	rep := mkTest(t, Config{Root: filepath.Join(dir, "t.mk"), Profile: types.TestProfile}, TestOptions{})
	if got, want := results(rep), "main fails error\nmain passes pass"; got != want {
		t.Fatalf("results:\n%s\nwant:\n%s", got, want)
	}
	if e := rep.Tests[0]; e.Status != 1 || !strings.Contains(e.Output, "error: main.Bad(code: 7)\n") {
		t.Errorf("failing test: status %d, output %q; want status 1 and error: main.Bad(code: 7)", e.Status, e.Output)
	}
}
