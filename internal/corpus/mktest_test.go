// Why: `mk test` must report each test's outcome and exit status exactly,
// leaks included.
// What: TestMkTest over tests/programs/testblocks, with mkTestCase and
// mkTestWant.
// How: reads `mk test --json` and compares every test's result and its
// child's exit status with the expected table.
//
// TestMkTest runs this repository's `mk test --json` on each file of
// tests/programs/testblocks and checks, per test as named, its result and
// its child's exit status: a passing file exits 0, and a failing assertion,
// a panic or a leak (the counting heap's report, exit 102) makes mk test
// exit 1. Each test runs in its own child process: a panicking test does
// not stop the next one, and every test has its own exit status.

package corpus

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

// mkTestCase is one test of `mk test --json`'s report.
type mkTestCase struct {
	Module string `json:"module"`
	Name   string `json:"name"`
	Result string `json:"result"`
	Status int    `json:"status"`
	Output string `json:"output"`
}

// mkTestWant is one test's expected outcome: its result, and its child's
// exit status.
type mkTestWant struct {
	name   string
	result string
	status int
}

func TestMkTest(t *testing.T) {
	root := repoRoot(t)
	needLLVM(t)
	exe := buildMk(t, root)
	for _, c := range []struct {
		file  string
		exit  int
		tests []mkTestWant
	}{
		{"pass.mk", 0, []mkTestWant{{"len", "pass", 0}, {"with memory", "pass", 0}, {"push and free", "pass", 0}}},
		{"fail.mk", 1, []mkTestWant{{"holds", "pass", 0}, {"fails", "panic", 101}}},
		{"panic.mk", 1, []mkTestWant{{"panics", "panic", 101}, {"after", "pass", 0}}},
		{"leak.mk", 1, []mkTestWant{{"leaks", "leak", 102}}},
	} {
		t.Run(strings.TrimSuffix(c.file, ".mk"), func(t *testing.T) {
			path := filepath.Join(root, filepath.FromSlash(ProgramsDir+"/testblocks/"+c.file))
			stdout, stderr, status := runMk(t, exe, "test", "--json", path)
			var rep struct {
				Tests          []mkTestCase `json:"tests"`
				Passed, Failed int
			}
			if err := json.Unmarshal([]byte(stdout), &rep); err != nil {
				t.Fatalf("mk test --json %s: exit %d, not a report (%v)\nstdout %q\nstderr %q", c.file, status, err, stdout, stderr)
			}
			if status != c.exit {
				t.Errorf("mk test %s: exit %d, want %d\n%s", c.file, status, c.exit, stdout)
			}
			if len(rep.Tests) != len(c.tests) {
				t.Fatalf("mk test %s: %d tests, want %d: %+v", c.file, len(rep.Tests), len(c.tests), rep.Tests)
			}
			passed := 0
			for i, w := range c.tests {
				g := rep.Tests[i]
				if g.Name != w.name || g.Result != w.result || g.Status != w.status {
					t.Errorf("test %d: %q %s (status %d), want %q %s (status %d); output %q", i, g.Name, g.Result, g.Status, w.name, w.result, w.status, g.Output)
				}
				if w.result == "pass" {
					passed++
				} else if g.Output == "" {
					t.Errorf("test %q (%s) printed nothing; its report is the child's output", g.Name, g.Result)
				}
				if w.result == "leak" && !strings.Contains(g.Output, "leak") {
					t.Errorf("test %q: the counting heap's report does not name the leak: %q", g.Name, g.Output)
				}
			}
			if rep.Passed != passed || rep.Failed != len(c.tests)-passed {
				t.Errorf("passed %d, failed %d; want %d and %d", rep.Passed, rep.Failed, passed, len(c.tests)-passed)
			}
		})
	}
}
