// Why: the runtime's own test blocks (std/rt/tests_*.mk, std/task/tests_*.mk)
// only run through the driver, so this test is how a failing block shows up
// in go test.
// What: TestStdRuntimeTests, stdExpect, stdEnvTag and judgeStdTest.
// How: runs mk test --std on x86_64-windows and x86_64-linux, and on the
// host's own target when it is another (a Mac, an ARM machine); a block's name
// may ask for an exit status and output ("[exit <code>: <text>]") or an
// environment ("[env K=V ...]"), which runs it alone with that environment.

package driver

import (
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"mk/internal/types"
)

// stdExpect is a block's expected end, from a name ending in "[exit <code>:
// <text>]" (as std/rt-sched_harness_test.go reads it):
// that exit status and output containing the text. Other blocks pass.
var stdExpect = regexp.MustCompile(`\[exit ([0-9]+): ([^\]]*)\]$`)

// stdEnvTag is a block's environment, from "[env K=V ...]" in its name.
var stdEnvTag = regexp.MustCompile(`\[env ([^\]]*)\]`)

// TestStdRuntimeTests: every test block of std.rt and std.task under
// mk test --std, on x86_64-windows and x86_64-linux. A block that asks for
// an environment runs alone with it (through WSLENV for a WSL child).
func TestStdRuntimeTests(t *testing.T) {
	targets := []types.Target{types.X86_64Windows, types.X86_64Linux}
	if h := HostTarget(); h.Name != targets[0].Name && h.Name != targets[1].Name {
		targets = append(targets, h) // the host's own target, run natively
	}
	for _, target := range targets {
		t.Run(target.Name, func(t *testing.T) {
			prefix := runner(t, target)
			cache := t.TempDir()
			for _, mod := range []string{"std.rt", "std.task"} {
				rep := mkTest(t, Config{StdTests: mod, Target: target, CacheDir: cache}, TestOptions{})
				if len(rep.Tests) == 0 {
					t.Errorf("%s: no test blocks", mod)
				}
				for _, tc := range rep.Tests {
					if tc.Module != mod {
						t.Errorf("--std=%s ran %s %q", mod, tc.Module, tc.Name)
					}
					m := stdEnvTag.FindStringSubmatch(tc.Name)
					timed := stdTimingBlock(tc.Name) && !timingRun()
					if m == nil {
						if timed {
							t.Logf("%s %q: %s, status %d (not judged, %s: its verdict depends on wall-clock time)", tc.Module, tc.Name, tc.Result, tc.Status, timingOff)
							continue
						}
						judgeStdTest(t, tc, len(prefix) > 0)
						continue
					}
					// Run it again, alone, with its environment.
					t.Run(tc.Name, func(t *testing.T) {
						if timed {
							t.Skip(timingOff + " (the block's verdict depends on wall-clock time)")
						}
						var keys []string
						for _, kv := range strings.Fields(m[1]) {
							k, v, _ := strings.Cut(kv, "=")
							t.Setenv(k, v)
							keys = append(keys, k)
						}
						if len(prefix) > 0 {
							wslenv := strings.Join(keys, ":")
							if old := os.Getenv("WSLENV"); old != "" {
								wslenv = old + ":" + wslenv
							}
							t.Setenv("WSLENV", wslenv)
						}
						one := mkTest(t, Config{StdTests: mod, Target: target, CacheDir: cache}, TestOptions{Run: tc.Name})
						if len(one.Tests) != 1 {
							t.Fatalf("-run %q selected %d tests", tc.Name, len(one.Tests))
						}
						judgeStdTest(t, one.Tests[0], len(prefix) > 0)
					})
				}
			}
		})
	}
}

// judgeStdTest fails t unless tc ended as its name asks. A name asks for a
// death by signal n as the shell reports it, 128+n (143 for
// SIGTERM); a Linux child run through the runner (viaRunner: wsl.exe -e)
// is reported with the bare n instead (measured: `wsl.exe -e sh -c 'kill
// -TERM $$'` exits 15), so n stands for 128+n there.
// Native runs stay exact; std/rt_term_test.go reads the shell's 128+n.
func judgeStdTest(t *testing.T, tc TestCase, viaRunner bool) {
	t.Helper()
	if tc.Status == 0 {
		// A block that prints "skip: <reason>" first and ends with status 0
		// skips by design on this target (as internal/stdtest's stdSkipped).
		first, _, _ := strings.Cut(strings.TrimLeft(tc.Output, "\r\n"), "\n")
		if reason, ok := strings.CutPrefix(strings.TrimRight(first, "\r"), "skip: "); ok {
			t.Logf("%s %q: skipped: %s", tc.Module, tc.Name, reason)
			return
		}
	}
	code, text := 0, ""
	if m := stdExpect.FindStringSubmatch(tc.Name); m != nil {
		code, _ = strconv.Atoi(m[1])
		text = m[2]
	}
	if viaRunner && code > 128 && code < 128+65 && tc.Status == code-128 {
		code = tc.Status
	}
	if tc.Status != code || !strings.Contains(tc.Output, text) {
		t.Errorf("%s %q: %s, status %d, want %d with %q in the output\n%s", tc.Module, tc.Name, tc.Result, tc.Status, code, text, tc.Output)
	}
}
