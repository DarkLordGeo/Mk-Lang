// Why: scope deadlines and std.simd.kernel cross every compiler stage, from
// resolve to link, and the evaluator; each stage's wiring needs an end-to-end test.
// What: TestWire_DeadlinesNative, TestWire_DeadlinesEvaluator,
// TestWire_SimdKernelStdOnly, TestWire_DeadlineEntriesLazy and
// TestWire_FreestandingScopeNoICE.
// How: TestWire_DeadlinesNative builds a program against the embedded std
// that calls Scope.CancelAfter, Expired and Remaining under Scope and
// Scope[E], directly, through a spawned task and as fn values, and runs it
// on Windows (resolve, monomorphise, lower, link to std.task's entries);
// TestWire_DeadlinesEvaluator runs the same calls in eval's tagged mode,
// where a scope has no clock; TestWire_SimdKernelStdOnly checks the E0202
// for a user import of std.simd.kernel, which a std module may import.

package driver

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"mk/internal/diag"
	"mk/internal/mono"
	"mk/internal/types"
)

// wire8Deadlines prints 11111 with stub timers and with the timers of
// std/task/deadline.mk alike, whatever order the carriers run the spawned
// tasks in: no deadline is near, so no scope expires; Remaining is read none
// only before any deadline is armed (e's is read before Check is spawned) and
// at most the earliest deadline after one, and the else default covers the
// stubs.
const wire8Deadlines = `import std.time

error Boom

Check(s: &Scope[Boom], d: time.Duration): Boom!void {
    s.CancelAfter(d)
    if s.Expired() {
        fail Boom
    }
}

export Main(sys: &System): Boom|WriteError!void {
    var n := 0
    scope s := sys.io.Scope() {
        if s.Remaining().IsNone() {
            n += 1
        }
        s.CancelAfter(time.Seconds(3600))
        if !s.Expired() {
            n += 10
        }
        left := s.Remaining() else {
            time.Seconds(3600)
        }
        if left.Nanos() > 0 && left.Nanos() <= 3_600_000_000_000 {
            n += 100
        }
        after := Scope.CancelAfter
        after(&s, time.Seconds(7200))
        s.go Scope.CancelAfter(&s, time.Seconds(7200))
        rem := Scope.Remaining
        later := rem(&s) else {
            time.Seconds(3600)
        }
        if later.Nanos() <= 3_600_000_000_000 {
            n += 1000
        }
    }
    try scope e := sys.io.ScopeOf[Boom]() {
        // Read before the spawn: Check arms e's own deadline, which
        // Remaining counts once the task has run (wire8-2).
        unarmed := !e.Expired() && e.Remaining().IsNone()
        e.go Check(&e, time.Seconds(3600))
        if unarmed && !e.Expired() {
            n += 10000
        }
    }
    try sys.Print("{n}\n", (n))
}
`

func TestWire_DeadlinesNative(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "deadlines.mk")
	if err := os.WriteFile(file, []byte(wire8Deadlines), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, prof := range []types.Profile{types.Dev, types.Release} {
		exe := buildProgram(t, file, types.X86_64Windows, prof, t.TempDir())
		out, errOut, code := runExe(t, types.X86_64Windows, exe, "")
		if code != 0 || out != "11111\n" {
			t.Errorf("%v: exit %d, stdout %q, stderr %q; want 0 and 11111", prof, code, out, errOut)
		}
	}
}

// wire8Eval is the evaluator's half: a scope there has no clock, so
// CancelAfter arms nothing, Expired is false and Remaining is none, even
// for a zero duration; Run returns 1111.
const wire8Eval = `import std.time

error Boom

Inner(io: Io): Boom!int {
    var n := 0
    try scope e := io.ScopeOf[Boom]() {
        e.CancelAfter(time.Seconds(0))
        if !e.Expired() && e.Remaining().IsNone() {
            n = 1000
        }
    }
    return n
}

export Run(io: Io): int {
    var n := 0
    scope s := io.Scope() {
        s.CancelAfter(time.Seconds(0))
        if !s.Expired() {
            n += 1
        }
        if s.Remaining().IsNone() {
            n += 10
        }
        rem := Scope.Remaining
        if rem(&s).IsNone() {
            n += 100
        }
    }
    m := Inner(io) else {
        return -1
    }
    return n + m
}
`

func TestWire_DeadlinesEvaluator(t *testing.T) {
	c, err := NewCorpusSession(Config{})
	if err != nil {
		t.Fatal(err)
	}
	if o := soundCheck(t, c, "wire8/deadlines", wire8Eval); len(o.Report.Diags) != 0 {
		t.Fatalf("diagnostics: %s", o.Report.Text(o.Sources))
	}
	if c.Through() < diag.PhaseFlow {
		notRun(t, "embedded std: Through %v, so no unit is lowered", c.Through())
	}
	for _, seed := range []uint64{0, 1, 7} {
		r := c.RunUnit(RunSpec{Unit: "wire8/deadlines", Fn: "Run", Tag: &TagOptions{Seed: seed}})
		if r.Skipped != "" || r.Err != nil || len(r.Violations) != 0 || r.Value != 1111 {
			t.Errorf("seed %d: %+v", seed, r)
		}
	}
}

func TestWire_SimdKernelStdOnly(t *testing.T) {
	files := fakeStdFiles()
	files["simd/kernel/kernel.mk"] = "export Blocks(): int {\n    return 3\n}\n"
	files["hash/hash.mk"] = "import std.simd.kernel\n\nexport Sum(): int {\n    return kernel.Blocks()\n}\n"
	stdDir := writeTree(t, files)
	r := check(t, Config{Root: writeTree(t, map[string]string{
		"a.mk": "import std.simd.kernel\n\nexport Main(sys: &System) {\n    _ = kernel.Blocks()\n}\n"}), StdDir: stdDir})
	expectLines(t, r, "a.mk:1:8:E0202")
	if len(r.Report.Diags) == 1 {
		d := r.Report.Diags[0].Diag
		if fact(d, "reason") != "std-internal" || fact(d, "module") != "std.simd.kernel" {
			t.Errorf("reason %q module %q, want std-internal and std.simd.kernel", fact(d, "reason"), fact(d, "module"))
		}
	}
	// A std module imports it, and a user module imports that one.
	clean(t, check(t, Config{Root: writeTree(t, map[string]string{
		"a.mk": "import std.hash\n\nexport Main(sys: &System) {\n    _ = hash.Sum()\n}\n"}), StdDir: stdDir}))
	// On x86_64-freestanding the kernel is formed for std.hash (layer 0),
	// and a program's own import is still std-internal.
	use := "import std.hash\n\nexport Use(): int {\n    return hash.Sum()\n}\n"
	r = check(t, Config{Root: writeTree(t, map[string]string{"a.mk": use}), StdDir: stdDir, Target: types.X86_64Freestanding})
	for _, e := range r.Report.Diags {
		t.Errorf("freestanding std.hash: %s %s %s", diag.Location(r.Sources, e.Diag.At), e.Diag.Code, e.Diag.Message)
	}
	r = check(t, Config{Root: writeTree(t, map[string]string{
		"a.mk": "import std.simd.kernel\n\nexport Use(): int {\n    return kernel.Blocks()\n}\n"}), StdDir: stdDir, Target: types.X86_64Freestanding})
	expectLines(t, r, "a.mk:1:8:E0202")
	if len(r.Report.Diags) == 1 && fact(r.Report.Diags[0].Diag, "reason") != "std-internal" {
		t.Errorf("freestanding import: reason %q, want std-internal", fact(r.Report.Diags[0].Diag, "reason"))
	}
}

// TestWire_DeadlineEntriesLazy: the deadline entries are runtime roots
// held back until the UsesTasks fact is true (mono lazyKeys), as
// std.task.Cancel is, so hello world collects none of them, while a
// program that reads a scope's deadline, or names Scope.Expired as a value,
// has its entries.
func TestWire_DeadlineEntriesLazy(t *testing.T) {
	entries := []string{"std.task.CancelAfter", "std.task.Expired", "std.task.Remaining"}
	for _, tc := range []struct {
		name, src string
		tasks     bool
		want      []string // the entries the build collects
	}{
		{"hello", string(helloSrc(t)), false, nil},
		{"deadline", "export Main(sys: &System) {\n    scope s := sys.io.Scope() {\n        _ = s.Expired()\n    }\n}\n", true, entries},
		{"value", "export Main(sys: &System) {\n    f := Scope.Expired\n    _ = f\n}\n", true, entries},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "main.mk"), []byte(tc.src), 0o644); err != nil {
				t.Fatal(err)
			}
			s, err := NewSession(Config{Root: filepath.Join(dir, "main.mk"), CacheDir: filepath.Join(dir, ".cache")})
			if err != nil {
				t.Fatal(err)
			}
			if r := s.Check(); len(r.Report.Diags) > 0 {
				t.Fatalf("check: %s", r.Report.Text(r.Sources))
			}
			insts, ds := mono.Collect(s.w.sem5.monoEnv(s.w), s.w.buildRoots("main", s.pkgB.mods, false, false), s.w.monoOptions())
			if len(ds) > 0 {
				t.Fatalf("collect: %s", ds[0].Message)
			}
			if insts.UsesTasks() != tc.tasks {
				t.Errorf("UsesTasks %v, want %v", insts.UsesTasks(), tc.tasks)
			}
			var got []string
			for i := range insts.List {
				if k := s.w.items.Get(insts.List[i].Item).Key.String(); slices.Contains(entries, k) {
					got = append(got, k)
				}
			}
			slices.Sort(got)
			if !slices.Equal(got, tc.want) {
				t.Errorf("deadline entries collected %q, want %q", got, tc.want)
			}
		})
	}
}

// TestWire_FreestandingScopeNoICE: on x86_64-freestanding std.task is not
// formed, so a reached scope intrinsic or s.go, called or named as a value,
// is E0202 reason freestanding naming std.task at the use (a fn value at the
// statement that names it), never E0000 "missing runtime entry". The first
// diagnostic is the one checked by position; every one must be that E0202.
func TestWire_FreestandingScopeNoICE(t *testing.T) {
	tools := needTools(t)
	entry := "export extern MkEntry() {\n    f := %s\n    _ = f\n    for {\n    }\n}\n"
	cases := []struct{ name, src, at string }{
		{"Scope.CancelAfter", fmt.Sprintf(entry, "Scope.CancelAfter"), "main.mk:2:5"},
		{"Scope.Expired", fmt.Sprintf(entry, "Scope.Expired"), "main.mk:2:5"},
		{"Scope.Remaining", fmt.Sprintf(entry, "Scope.Remaining"), "main.mk:2:5"},
		{"Scope.Cancel", fmt.Sprintf(entry, "Scope.Cancel"), "main.mk:2:5"},
		{"Io.Scope", fmt.Sprintf(entry, "Io.Scope"), "main.mk:2:5"},
		{"scope block", "F(io: Io) {\n    scope s := io.Scope() {\n        s.Cancel()\n    }\n}\n\n" + fmt.Sprintf(entry, "F"), "main.mk:2:16"},
		{"s.go", "H() {\n}\n\nG(s: &Scope) {\n    s.go H()\n}\n\n" + fmt.Sprintf(entry, "G"), "main.mk:5:5"},
		{"argument", "Use(f: fn(&Scope): bool) {\n}\n\nexport extern MkEntry() {\n    Use(Scope.Expired)\n    for {\n    }\n}\n", "main.mk:5:9"},
		// Reached only through a const's fn value (a mono relocation, no
		// site in a body): reported at Scope.Expired's declaration.
		{"const", "const C: fn(&Scope): bool = Scope.Expired\n\n" + fmt.Sprintf(entry, "C"), "io_io.mk"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			file := filepath.Join(dir, "main.mk")
			if err := os.WriteFile(file, []byte(tc.src), 0o644); err != nil {
				t.Fatal(err)
			}
			s, err := NewSession(Config{Root: file, Target: types.X86_64Freestanding, Tools: tools, CacheDir: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			r, paths, err := s.Build(filepath.Join(dir, "main"))
			if err != nil {
				t.Fatal(err)
			}
			if len(paths) != 0 || len(r.Report.Diags) == 0 {
				t.Fatalf("built %q with no diagnostic", paths)
			}
			for _, e := range r.Report.Diags {
				d := e.Diag
				if d.Code != diag.E0202 || fact(d, "reason") != "freestanding" || fact(d, "module") != "std.task" {
					t.Errorf("%s %s %s (reason %q module %q), want E0202 freestanding std.task", diag.Location(r.Sources, d.At), d.Code, d.Message, fact(d, "reason"), fact(d, "module"))
				}
			}
			if got := diag.Location(r.Sources, r.Report.Diags[0].Diag.At); !strings.HasSuffix(got, "/"+tc.at) && !strings.Contains(got, "/"+tc.at+":") {
				t.Errorf("at %s, want %s", got, tc.at)
			}
		})
	}
}
