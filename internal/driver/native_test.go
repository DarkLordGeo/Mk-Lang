// Why: the native back end (codegen, the toolchain jobs and the link) is
// where a program meets clang, lld and the OS; only real builds and runs
// show that executables start, exit and panic as specified.
// What: mk build and mk run of tests/programs on Windows and Linux (exit
// status, panics, stack overflow, imports, static ELF, E0001 cases), plus
// the shared helpers gateRun, notRun, linuxRun, wslGuard, needTools, runner,
// runExe and buildProgram that the package's other native tests use.
// How: builds with the embedded std into temporary directories and runs the
// result natively or through wsl.exe; Linux leaves run only with MK_LINUX=1
// under the WSL lock, and under MK_GATE=1 a skip becomes a failure.

package driver

import (
	"bytes"
	"context"
	"debug/elf"
	"debug/pe"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"mk/internal/diag"
	"mk/internal/toolchain"
	"mk/internal/types"
)

// gateRun reports whether this is the gate's run, MK_GATE=1.
func gateRun() bool { return os.Getenv("MK_GATE") == "1" }

// notRun stops a test that cannot run here: a missing clang, lld or wsl.exe,
// or a known blocker. Outside the gate it is a SKIP; under MK_GATE=1 it is a
// failure with the same text, because the gate run fails if any such test
// skipped, so the package cannot report ok while its native tests did not
// run.
func notRun(t testing.TB, format string, args ...any) {
	t.Helper()
	if gateRun() {
		t.Fatalf("MK_GATE=1, not run: "+format, args...)
		return
	}
	t.Skipf(format, args...)
}

// linuxOff is the skip reason of a leaf that would start wsl.exe while
// MK_LINUX=1 is not set. It is a plain skip, also under MK_GATE=1: the gate
// runs the Linux leaves in their own pass, under the WSL lock.
const linuxOff = "MK_LINUX=1 not set"

// linuxRun reports whether this run may start wsl.exe, MK_LINUX=1.
func linuxRun() bool { return os.Getenv("MK_LINUX") == "1" }

// wslGuard panics when a wsl.exe is about to start without MK_LINUX=1: a
// leaf that reaches it missed its linuxRun check.
//
// With MK_LINUX=1 it also panics unless the run holds the WSL lock
// (tools/wsl-lock.ps1 sets MK_WSL_LOCKED=1), so a Linux run outside the
// lock cannot happen by accident; MK_WSL_UNLOCKED_OK=1 allows one on
// purpose.
func wslGuard() {
	if !linuxRun() {
		panic("wsl.exe started without MK_LINUX=1; the leaf must skip with " + linuxOff)
	}
	if os.Getenv("MK_WSL_LOCKED") != "1" && os.Getenv("MK_WSL_UNLOCKED_OK") != "1" {
		panic("wsl.exe started with MK_LINUX=1 outside the WSL lock (MK_WSL_LOCKED is not 1): run under pwsh -NoProfile -File tools/wsl-lock.ps1, or set MK_WSL_UNLOCKED_OK=1")
	}
}

// needTools stops the test (notRun) when no LLVM is found and returns the
// tools.
func needTools(t *testing.T) *toolchain.Tools {
	t.Helper()
	tools, err := toolchain.Discover(toolchain.Options{})
	if err != nil {
		notRun(t, "no LLVM: %v (the gate fails on this skip)", err)
	}
	return tools
}

// runner is the argv prefix that runs target's executables here, or a skip.
func runner(t *testing.T, target types.Target) []string {
	t.Helper()
	prefix, ok := toolchain.Runner(target.LLVMTriple)
	if !ok {
		notRun(t, "no runner for %s on %s (the gate fails on this skip)", target.Name, runtime.GOOS)
	}
	if len(prefix) > 0 {
		if !linuxRun() {
			t.Skip(linuxOff)
		}
		// Warm WSL first: a cold start can take longer than a test allows.
		// Bounded, so a WSL that never answers stops this test (notRun)
		// instead of hanging the package until go test's own timeout.
		// Once per test binary: after a timeout, later tests stop at once.
		warmOnce.Do(func() {
			wslGuard()
			ctx, cancel := context.WithTimeout(context.Background(), runnerWarmup)
			defer cancel()
			if err := exec.CommandContext(ctx, prefix[0], append(prefix[1:], "true")...).Run(); ctx.Err() != nil {
				warmErr = fmt.Sprintf("the runner %q did not answer `true` within %v (%v)", prefix, runnerWarmup, err)
			}
		})
		if warmErr != "" {
			notRun(t, "%s", warmErr)
		}
	}
	return prefix
}

// runnerWarmup bounds runner's warm-up: a cold WSL start takes seconds;
// a wedged one never returns.
const runnerWarmup = 90 * time.Second

var (
	warmOnce sync.Once
	warmErr  string // set when the warm-up timed out
)

// runExe runs exe with stdin and returns stdout, stderr and the status.
func runExe(t *testing.T, target types.Target, exe string, stdin string, args ...string) (string, string, int) {
	t.Helper()
	prefix := runner(t, target)
	argv := append(append([]string(nil), prefix...), toolchain.HostPathFor(prefix, exe))
	argv = append(argv, args...)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	var o, e bytes.Buffer
	cmd.Stdin, cmd.Stdout, cmd.Stderr = strings.NewReader(stdin), &o, &e
	err := cmd.Run()
	code := 0
	if err != nil {
		var ee *exec.ExitError
		if !errors.As(err, &ee) {
			t.Fatalf("%q: %v", argv, err)
		}
		code = ee.ExitCode()
	}
	return o.String(), e.String(), code
}

// buildProgram builds one file with the embedded std for target and
// profile into dir and returns the executable's path. These tests
// exercise stages 8 to 10.
func buildProgram(t *testing.T, file string, target types.Target, prof types.Profile, dir string) string {
	t.Helper()
	tools := needTools(t)
	s, err := NewSession(Config{Root: file, Target: target, Profile: prof, Tools: tools, CacheDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, strings.TrimSuffix(filepath.Base(file), ".mk")+exeSuffix(target))
	r, paths, err := s.Build(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Report.Diags) > 0 || len(paths) != 1 {
		skipKnownBlockers(t, r)
		t.Fatalf("build %s %s %v: %d paths\n%s", file, target.Name, prof, len(paths), r.Report.Text(r.Sources))
	}
	return paths[0]
}

// knownBlockers are internal errors of other packages that stop a native
// build today, by a substring of the E0000's what fact, each with the
// package that owns the fix. A build whose every diagnostic is one of them
// skips with the list (visible as SKIP, never PASS; a failure under
// MK_GATE=1, notRun); any other diagnostic fails. An entry no test meets is
// dead text: remove it when the fix lands. The list is empty; it is kept for
// the next such blocker.
var knownBlockers = []struct{ what, owner string }{}

// blockedBy returns the owning packages of the known blockers r holds, or nil when
// some diagnostic of r is not a known blocker.
func blockedBy(r Result) []string {
	var owners []string
	for _, e := range r.Report.Diags {
		what := ""
		if e.Diag.Code.String() == "E0000" {
			what = e.Diag.Message
		}
		found := ""
		for _, kb := range knownBlockers {
			if what != "" && strings.Contains(what, kb.what) {
				found = kb.owner
			}
		}
		if found == "" {
			return nil
		}
		if !slices.Contains(owners, found) {
			owners = append(owners, found)
		}
	}
	return owners
}

func skipKnownBlockers(t *testing.T, r Result) {
	t.Helper()
	if owners := blockedBy(r); len(owners) > 0 {
		notRun(t, "BLOCKED: %s", strings.Join(owners, "; "))
	}
}

func testBuildFizz(t *testing.T, target types.Target) {
	want, err := os.ReadFile(filepath.Join("..", "..", "tests", "programs", "fizz.out"))
	if err != nil {
		t.Fatal(err)
	}
	for _, prof := range []types.Profile{types.Dev, types.Release} {
		t.Run(prof.String(), func(t *testing.T) {
			exe := buildProgram(t, filepath.Join("..", "..", "tests", "programs", "fizz.mk"), target, prof, t.TempDir())
			out, errOut, code := runExe(t, target, exe, "")
			if code != 0 || out != string(want) {
				t.Errorf("exit %d, stdout %q, stderr %q; want exit 0 and fizz.out", code, out, errOut)
			}
		})
	}
}

func TestBuildFizzWindows(t *testing.T) { testBuildFizz(t, types.X86_64Windows) }
func TestBuildFizzLinux(t *testing.T)   { testBuildFizz(t, types.X86_64Linux) }

// TestBuildMainExitStatus: Main's int result is the process's exit status
// through the runtime's entry, CallMain and Finish.
func TestBuildMainExitStatus(t *testing.T) {
	for _, target := range []types.Target{types.X86_64Windows, types.X86_64Linux} {
		t.Run(target.Name, func(t *testing.T) {
			dir := t.TempDir()
			src := filepath.Join(dir, "seven.mk")
			if err := os.WriteFile(src, []byte("export Main(sys: &System): int {\n    return 7\n}\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			exe := buildProgram(t, src, target, types.Dev, dir)
			out, errOut, code := runExe(t, target, exe, "")
			if code != 7 || out != "" {
				t.Errorf("exit %d, stdout %q, stderr %q; want exit 7", code, out, errOut)
			}
		})
	}
}

// programs is tests/programs, from this package's directory.
var programs = filepath.Join("..", "..", "tests", "programs")

// wantPanic checks a panic's report and status: exit 101, and stderr equal
// to the .err file's line byte for byte. The site's file is
// package-relative: buildProgram gives an absolute root, and the package
// root of a single file is its directory, so the site cites the file's name;
// only diagnostics cite the path as given. dropBacktrace is a panic report
// up to its "backtrace:" line, as the corpus reads one
// (corpus.DropBacktrace): the frames vary with the profile and the target.
func dropBacktrace(s string) string {
	if strings.HasPrefix(s, "backtrace:") {
		return ""
	}
	if i := strings.Index(s, "\nbacktrace:"); i >= 0 {
		return s[:i+1]
	}
	return s
}

func wantPanic(t *testing.T, errFile, stderr string, code int) {
	t.Helper()
	raw, err := os.ReadFile(errFile)
	if err != nil {
		t.Fatal(err)
	}
	want := strings.ReplaceAll(string(raw), "\r\n", "\n")
	if !strings.HasSuffix(want, "\n") {
		want += "\n"
	}
	ok := code == 101 && dropBacktrace(stderr) == want
	if !ok {
		t.Errorf("exit %d, stderr %q; want exit 101 and %q", code, stderr, want)
	}
}

// TestPanicExits101: an integer overflow prints its one-line report and
// exits 101 (tests/programs/panics/overflow.mk).
func TestPanicExits101(t *testing.T) {
	for _, target := range []types.Target{types.X86_64Windows, types.X86_64Linux} {
		for _, prof := range []types.Profile{types.Dev, types.Release} {
			t.Run(target.Name+"/"+prof.String(), func(t *testing.T) {
				exe := buildProgram(t, filepath.Join(programs, "panics", "overflow.mk"), target, prof, t.TempDir())
				_, errOut, code := runExe(t, target, exe, "")
				wantPanic(t, filepath.Join(programs, "panics", "overflow.err"), errOut, code)
			})
		}
	}
}

// TestStackOverflowIsPanic: unbounded recursion on the main task is the
// stack-limit panic, not a crash (tests/programs/panics/stack_main.mk).
func TestStackOverflowIsPanic(t *testing.T) {
	for _, target := range []types.Target{types.X86_64Windows, types.X86_64Linux} {
		for _, prof := range []types.Profile{types.Dev, types.Release} {
			t.Run(target.Name+"/"+prof.String(), func(t *testing.T) {
				exe := buildProgram(t, filepath.Join(programs, "panics", "stack_main.mk"), target, prof, t.TempDir())
				_, errOut, code := runExe(t, target, exe, "")
				if strings.Contains(errOut, " at ") {
					wantPanic(t, filepath.Join(programs, "panics", "stack_main.err"), errOut, code)
					return
				}
				// The site comes from FuncTable's rows of user functions,
				// which codegen emits with the positions pack; a build
				// without them reports no site.
				if code != 101 || dropBacktrace(errOut) != "panic: stack limit\n" {
					t.Errorf("exit %d, stderr %q; want exit 101 and the stack-limit panic", code, errOut)
				}
				t.Logf("no site in the report (codegen T17): %q", errOut)
			})
		}
	}
}

// TestImportsOnlyKernel32: a Windows executable imports kernel32.dll and
// nothing else (the import library comes from std/sys/windows/kernel32.def);
// dev and release.
func TestImportsOnlyKernel32(t *testing.T) {
	for _, prof := range []types.Profile{types.Dev, types.Release} {
		t.Run(prof.String(), func(t *testing.T) {
			exe := buildProgram(t, filepath.Join(programs, "panics", "overflow.mk"), types.X86_64Windows, prof, t.TempDir())
			f, err := pe.Open(exe)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			syms, err := f.ImportedSymbols()
			if err != nil {
				t.Fatal(err)
			}
			dlls := map[string]bool{}
			for _, s := range syms {
				if i := strings.LastIndex(s, ":"); i >= 0 {
					dlls[strings.ToLower(s[i+1:])] = true
				}
			}
			if len(dlls) != 1 || !dlls["kernel32.dll"] {
				t.Errorf("imports %v, want only kernel32.dll", dlls)
			}
		})
	}
}

// TestLinuxStaticNoInterp: a Linux executable is static ELF: ET_EXEC, no
// PT_INTERP and no PT_DYNAMIC; dev and release.
func TestLinuxStaticNoInterp(t *testing.T) {
	for _, prof := range []types.Profile{types.Dev, types.Release} {
		t.Run(prof.String(), func(t *testing.T) {
			exe := buildProgram(t, filepath.Join(programs, "panics", "overflow.mk"), types.X86_64Linux, prof, t.TempDir())
			f, err := elf.Open(exe)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			if f.Type != elf.ET_EXEC {
				t.Errorf("type %v, want ET_EXEC", f.Type)
			}
			for _, p := range f.Progs {
				if p.Type == elf.PT_INTERP || p.Type == elf.PT_DYNAMIC {
					t.Errorf("program header %v", p.Type)
				}
			}
		})
	}
}

// TestRunPassesExitStatus: mk run's status is the program's, and
// the executable is placed under the cache's bin directory by content.
func TestRunPassesExitStatus(t *testing.T) {
	tools := needTools(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "seven.mk")
	if err := os.WriteFile(src, []byte("export Main(sys: &System): int {\n    return 7\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cache := filepath.Join(dir, "cache")
	for i := 0; i < 2; i++ {
		s, err := NewSession(Config{Root: src, Tools: tools, CacheDir: cache})
		if err != nil {
			t.Fatal(err)
		}
		r, status, err := s.Run(nil, RunIO{Stdout: io.Discard, Stderr: io.Discard})
		if err != nil {
			t.Fatal(err)
		}
		skipKnownBlockers(t, r)
		if len(r.Report.Diags) > 0 || status != 7 {
			t.Fatalf("run %d: status %d\n%s", i, status, r.Report.Text(r.Sources))
		}
	}
	ents, _ := os.ReadDir(binOf(cache))
	if len(ents) != 1 {
		t.Errorf("the store's bin holds %d entries after two identical runs, want 1", len(ents))
	}
}

// TestRunNamedBin: with two bins, mk run needs --bin (a usage error naming
// both), and --bin=b runs b.
func TestRunNamedBin(t *testing.T) {
	tools := needTools(t)
	files := map[string]string{
		"pkg.mk": manifest(map[string]string{"bins": `[pkg.Bin(name: "a", root: "a", link_script: "", cpu: "", subsystem: "console"), pkg.Bin(name: "b", root: "b", link_script: "", cpu: "", subsystem: "console")]`}),
		"a/a.mk": "export Main(sys: &System): int {\n    return 3\n}\n",
		"b/b.mk": "export Main(sys: &System): int {\n    return 4\n}\n",
	}
	dir := writeTree(t, files)
	s, err := NewSession(Config{Root: dir, Tools: tools, CacheDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = s.Run(nil, RunIO{})
	var ue *UsageError
	if !errors.As(err, &ue) || !strings.Contains(ue.Msg, "a, b") {
		t.Errorf("no --bin: err %v, want a usage error naming a, b", err)
	}
	s, err = NewSession(Config{Root: dir, Bin: "b", Tools: tools, CacheDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	r, status, err := s.Run(nil, RunIO{Stdout: io.Discard, Stderr: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	skipKnownBlockers(t, r)
	if status != 4 {
		t.Errorf("--bin=b: status %d, want 4\n%s", status, r.Report.Text(r.Sources))
	}
}

// TestE0001_NoLLVM: a native build with no usable LLVM is one E0001 naming
// the searched directories, exit 2, and nothing is written.
func TestE0001_NoLLVM(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "seven.mk")
	if err := os.WriteFile(src, []byte("export Main(sys: &System): int {\n    return 7\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := NewSession(Config{Root: src, LLVMDir: filepath.Join(dir, "nollvm")})
	if err != nil {
		t.Fatal(err)
	}
	r, paths, err := s.Build(filepath.Join(dir, "seven.exe"))
	if err != nil {
		t.Fatal(err)
	}
	if got := codes(r); len(got) != 1 || got[0] != "E0001" || ExitStatus(r.Report) != 2 || len(paths) != 0 {
		t.Errorf("codes %q, exit %d, paths %q\n%s", got, ExitStatus(r.Report), paths, r.Report.Text(r.Sources))
	}
	if !strings.Contains(r.Report.Diags[0].Diag.Message, "nollvm") {
		t.Errorf("message %q does not name the searched directory", r.Report.Diags[0].Diag.Message)
	}
}

// unitSession is a corpus session on the embedded std (flow-clean, so
// units reach flow and are built), for the tests of building and running.
func unitSession(t *testing.T) *CorpusSession {
	t.Helper()
	tools := needTools(t)
	c, err := NewCorpusSession(Config{Tools: tools, CacheDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if c.Through() != diag.PhaseFlow {
		t.Fatalf("Through %v, want flow\n%s", c.Through(), c.Std().Report.Text(c.Std().Sources))
	}
	return c
}

// unitOutcome runs one unit with mode and skips on a known blocker.
func unitOutcome(t *testing.T, c *CorpusSession, name, src string, mode UnitMode) UnitOutcome {
	t.Helper()
	out := c.CheckUnit(UnitInput{Name: name, Files: []UnitFile{{"block.mk", src}}, Mode: mode})
	if len(out.Report.Diags) > 0 {
		skipKnownBlockers(t, Result{Report: out.Report, Sources: out.Sources})
		t.Fatalf("%s:\n%s", name, out.Report.Text(out.Sources))
	}
	return out
}

// TestCorpusUnitRunPanic: a unit whose Main panics runs and reports 101
// (the corpus judge fails it).
func TestCorpusUnitRunPanic(t *testing.T) {
	c := unitSession(t)
	out := unitOutcome(t, c, "panic#1", "export Main(sys: &System): int {\n    big := 9223372036854775807\n    return big + sys.args.Len()\n}\n", UnitRun)
	if !out.Built || !out.Ran || out.RunStatus != 101 || out.TimedOut {
		t.Errorf("Built %v Ran %v RunStatus %d TimedOut %v; want a run with status 101", out.Built, out.Ran, out.RunStatus, out.TimedOut)
	}
	// UnitBuild builds and does not run; a unit with no Main builds its
	// functions and runs nothing.
	out = unitOutcome(t, c, "build#1", "export Main(sys: &System): int {\n    return 3\n}\n", UnitBuild)
	if !out.Built || out.Ran {
		t.Errorf("UnitBuild: Built %v Ran %v", out.Built, out.Ran)
	}
	out = unitOutcome(t, c, "nomain#1", "F(x: int): int {\n    return x + 1\n}\n", UnitRun)
	if !out.Built || out.Ran {
		t.Errorf("no Main: Built %v Ran %v", out.Built, out.Ran)
	}
}

// TestCorpusUnitRunTimeout: a unit whose Main never returns is killed after
// the 10 s timeout, so it cannot hang the corpus.
func TestCorpusUnitRunTimeout(t *testing.T) {
	if testing.Short() {
		t.Skip("waits for the 10 s timeout")
	}
	c := unitSession(t)
	start := time.Now()
	out := unitOutcome(t, c, "loop#1", "export Main(sys: &System) {\n    var n := 0\n    for {\n        n = n ^ 1\n    }\n}\n", UnitRun)
	if !out.Ran || !out.TimedOut || out.RunStatus != -1 {
		t.Errorf("Ran %v TimedOut %v RunStatus %d; want a run killed at the timeout", out.Ran, out.TimedOut, out.RunStatus)
	}
	if d := time.Since(start); d > unitRunTimeout+20*time.Second {
		t.Errorf("took %v", d)
	}
}

// TestE0001_UnresolvedExtern: an extern that no import library and no
// object defines fails at link with one E0001 whose tool is the linker and
// whose symbol fact names it, exit 2, and no executable.
func TestE0001_UnresolvedExtern(t *testing.T) {
	tools := needTools(t)
	files := map[string]string{
		"pkg.mk": manifest(map[string]string{"bins": `[pkg.Bin(name: "x", root: "src", link_script: "", cpu: "", subsystem: "console")]`, "unsafe_modules": `["src"]`}),
		"src/main.mk": "extern MkNoSuchSymbol()\n\nexport Main(sys: &System) {\n" +
			"    unsafe \"the test needs an unresolved extern\" {\n        MkNoSuchSymbol()\n    }\n}\n",
	}
	for _, target := range []types.Target{types.X86_64Windows, types.X86_64Linux} {
		t.Run(target.Name, func(t *testing.T) {
			dir := writeTree(t, files)
			s, err := NewSession(Config{Root: dir, Target: target, Tools: tools})
			if err != nil {
				t.Fatal(err)
			}
			out := filepath.Join(dir, "x"+exeSuffix(target))
			r, paths, err := s.Build(out)
			if err != nil {
				t.Fatal(err)
			}
			if len(r.Report.Diags) != 1 || codes(r)[0] != "E0001" {
				skipKnownBlockers(t, r)
				t.Fatalf("want one E0001\n%s", r.Report.Text(r.Sources))
			}
			d := r.Report.Diags[0].Diag
			linker := "lld-link"
			if target.OS != types.OSWindows {
				linker = "ld.lld"
			}
			if fact(d, "symbol") != "MkNoSuchSymbol" || !strings.Contains(fact(d, "tool"), linker) {
				t.Errorf("symbol %q tool %q, want MkNoSuchSymbol and %s\n%s", fact(d, "symbol"), fact(d, "tool"), linker, r.Report.Text(r.Sources))
			}
			if ExitStatus(r.Report) != 2 || len(paths) != 0 {
				t.Errorf("exit %d paths %q, want 2 and none", ExitStatus(r.Report), paths)
			}
			if _, err := os.Stat(out); err == nil {
				t.Error("a failed link wrote the executable")
			}
		})
	}
}

// TestE0001_NoBody: a reachable bodiless function that is neither
// extern nor a std intrinsic is the E0001 no-body error. In a normal build
// every bodiless std function is an intrinsic, so the case arises through
// the corpus's trusted test prelude: a unit whose Main calls Cond() is built
// but not linked or run, and RunSkipped holds the E0001's message.
func TestE0001_NoBody(t *testing.T) {
	c := unitSession(t)
	out := c.CheckUnit(UnitInput{Name: "nobody#1", Prelude: "prelude.mk", Mode: UnitRun, Files: []UnitFile{
		{"block.mk", "export Main(sys: &System): int {\n    if Cond() {\n        return 1\n    }\n    return 0\n}\n"},
		{"prelude.mk", "Cond(): bool\n"}}})
	if len(out.Report.Diags) > 0 {
		skipKnownBlockers(t, Result{Report: out.Report, Sources: out.Sources})
		t.Fatalf("nobody#1:\n%s", out.Report.Text(out.Sources))
	}
	if !out.Built || out.Ran || !strings.Contains(out.RunSkipped, "Cond") || !strings.Contains(out.RunSkipped, "has no body") {
		t.Errorf("Built %v Ran %v RunSkipped %q; want built, not run, skipped naming Cond", out.Built, out.Ran, out.RunSkipped)
	}
}

// TestContractBodiesHashed: a function with a requires clause builds.
// Its signature hash (mk.sig.v1) hashes each contract body,
// so the session's Hasher reads bodies from the body store; with no body
// accessor mono's instance keys raised "iface: no body for
// main.Half#requires0" (tests/programs/panics/contract.mk). In dev the
// clause fails and the program panics with 101.
func TestContractBodiesHashed(t *testing.T) {
	for _, target := range []types.Target{types.X86_64Windows, types.X86_64Linux} {
		t.Run(target.Name, func(t *testing.T) {
			exe := buildProgram(t, filepath.Join(programs, "panics", "contract.mk"), target, types.Dev, t.TempDir())
			_, errOut, code := runExe(t, target, exe, "")
			if code != 101 || !strings.HasPrefix(errOut, "panic: contract") {
				t.Errorf("exit %d, stderr %q; want exit 101 and a contract panic", code, errOut)
			}
		})
	}
}
