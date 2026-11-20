// Why: cmd/mk is the user's whole interface to the compiler, so its exit
// statuses and which stream gets what are part of the contract.
// What: usage and flag errors, version, mk test, mk check, mk corpus, --json
// and --timings placement, panics (exit 3), and an end-to-end run of the
// built binary.
// How: most tests call the command table in-process through mk(); TestEndToEnd
// builds the binary and runs it. JSON goes to stdout for check and to stderr
// for run.

package main

import (
	"bytes"
	"flag"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"mk/internal/driver"
)

// -update is accepted so go test ./... -update runs.
var _ = flag.Bool("update", false, "no golden files in this package")

func mk(args ...string) (status int, stdout, stderr string) {
	var o, e bytes.Buffer
	status = run(args, &o, &e)
	return status, o.String(), e.String()
}

func TestUsageExit2(t *testing.T) {
	st, out, errOut := mk()
	if st != 2 || out != "" || !strings.Contains(errOut, "usage: mk") {
		t.Errorf("mk: status %d stdout %q stderr %q", st, out, errOut)
	}
	if st, _, errOut := mk("frobnicate"); st != 2 || !strings.Contains(errOut, "unknown command") {
		t.Errorf("unknown command: status %d stderr %q", st, errOut)
	}
	if st, out, _ := mk("help"); st != 0 || !strings.Contains(out, "mk check") {
		t.Errorf("help: status %d", st)
	}
}

func TestUnknownFlagExit2(t *testing.T) {
	for _, args := range [][]string{{"check", "--bogus"}, {"corpus", "--bogus"}, {"version", "--bogus"},
		{"check", "a.mk", "b.mk"}, {"check", "a.mk", "--", "x"}, {"check", "--target=x86_64-plan9", "."}} {
		if st, _, _ := mk(args...); st != 2 {
			t.Errorf("%q: status %d, want 2", args, st)
		}
	}
}

func TestVersionPrintsToolchain(t *testing.T) {
	st, out, _ := mk("version")
	// The release and the commit first, then the build line.
	if st != 0 || !strings.HasPrefix(out, "mk "+driver.Release+" commit ") || !strings.Contains(out, "\nmk build ") || !strings.Contains(out, runtime.Version()) {
		t.Errorf("status %d out %q", st, out)
	}
	if !strings.Contains(out, "LLVM") && !strings.Contains(out, "toolchain:") {
		t.Errorf("no toolchain line: %q", out)
	}
}

func TestTestRunFlagAfterPath(t *testing.T) {
	parse := func(args ...string) (testFlags, string, error) {
		var tf testFlags
		fs := newTestFlagSet(io.Discard, &tf)
		p, _, err := parseArgs(fs, args, false)
		return tf, p, err
	}
	a, pa, errA := parse("./pkg", "-run", "Foo")
	b, pb, errB := parse("-run", "Foo", "./pkg")
	if errA != nil || errB != nil || pa != "./pkg" || pb != "./pkg" || a.run != "Foo" || b.run != "Foo" {
		t.Errorf("after path: %q %q %v; before: %q %q %v", pa, a.run, errA, pb, b.run, errB)
	}
	c, pc, errC := parse("--json", "./pkg", "--timeout=2s", "-run", "Bar")
	if errC != nil || pc != "./pkg" || !c.json || c.timeout != "2s" || c.run != "Bar" {
		t.Errorf("mixed: %q %+v %v", pc, c, errC)
	}
	if _, _, err := parse("./a", "./b"); err == nil {
		t.Error("two paths accepted")
	}
	if _, _, err := parse("./a", "--", "x"); err == nil {
		t.Error("text after -- accepted for mk test")
	}
	// mk run keeps everything after -- for the program.
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	rel := fs.Bool("release", false, "")
	p, rest, err := parseArgs(fs, []string{"./pkg", "--release", "--", "-x", "./y"}, true)
	if err != nil || p != "./pkg" || !*rel || strings.Join(rest, " ") != "-x ./y" {
		t.Errorf("run: %q %v %q %v", p, *rel, rest, err)
	}
}

func TestJSONToStdoutForCheck(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "bad.mk")
	if err := os.WriteFile(file, []byte("import nothing.here\n\nG() {\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	st, out, errOut := mk("check", "--json", file)
	if st != 1 || !strings.HasPrefix(out, `{"version":1,"phase":"imports","diagnostics":[{"code":"E0202"`) || errOut != "" {
		t.Errorf("json: status %d stdout %q stderr %q", st, out, errOut)
	}
	st, out, errOut = mk("check", file)
	if st != 1 || out != "" || !strings.Contains(errOut, "E0202 unknown-module") {
		t.Errorf("text: status %d stdout %q stderr %q", st, out, errOut)
	}
	good := filepath.Join(dir, "good.mk")
	if err := os.WriteFile(good, []byte("G() {\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// The clean document's subject is the stream, not std: an empty std
	// (MK_STD) keeps std's own diagnostics out of it.
	t.Setenv("MK_STD", t.TempDir())
	if st, out, errOut := mk("check", "--json", good); st != 0 || out != "{\"version\":1,\"phase\":null,\"diagnostics\":[],\"omitted\":0}\n" || errOut != "" {
		t.Errorf("clean: status %d stdout %q stderr %q", st, out, errOut)
	}
}

func TestEndToEnd(t *testing.T) {
	// The built binary, as a user runs it.
	if testing.Short() {
		t.Skip("builds mk")
	}
	exe := filepath.Join(t.TempDir(), "mk")
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	if out, err := exec.Command("go", "build", "-o", exe, ".").CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	cmd := exec.Command(exe, "check", "--json", "../../tests/conformance/codes/E0202.mk")
	var o, e bytes.Buffer
	cmd.Stdout, cmd.Stderr = &o, &e
	err := cmd.Run()
	if ee, ok := err.(*exec.ExitError); !ok || ee.ExitCode() != 1 || !strings.Contains(o.String(), `"code":"E0202"`) || e.Len() != 0 {
		t.Errorf("mk check --json: %v stdout %q stderr %q", err, o.String(), e.String())
	}
	cmd = exec.Command(exe, "corpus")
	o.Reset()
	e.Reset()
	cmd.Stdout, cmd.Stderr = &o, &e
	err = cmd.Run()
	// mk corpus: the root is found from the working directory, the report
	// is on stdout, stderr is empty, and the exit status is 0 or 1 as the
	// totals line says (it depends on the state of the repository's docs,
	// which this test does not fix).
	status := 0
	if ee, ok := err.(*exec.ExitError); ok {
		status = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("mk corpus: %v", err)
	}
	report := o.String()
	want := 0
	if !strings.Contains(report, "\npass ") {
		t.Errorf("mk corpus: no totals line in %q", report)
	} else if !strings.Contains(report, ", fail 0, ") {
		want = 1
	}
	if status != want || e.Len() != 0 || !strings.Contains(report, " blocks: ") || !strings.Contains(report, "\nparsed: ") {
		t.Errorf("mk corpus: status %d (want %d) stdout %q stderr %q", status, want, report, e.String())
	}
	collisionEndToEnd(t, exe)
}

// collisionEndToEnd runs two path collisions through the built binary: a
// package holding std/core/c.mk, and `mk check std/core/c.mk` from a
// directory holding one, beside an MK_STD with core/c.mk, crashed with a Go
// panic and exit 2 (SourceMap.Add's duplicate-path ICE). Now the first is a
// user diagnostic and the second a clean check; neither prints a stack.
func collisionEndToEnd(t *testing.T, exe string) {
	std := writeFiles(t, map[string]string{"core/c.mk": "export C(): int {\n    return 1\n}\n"})
	pkgMk := "const Package = pkg.Package(\n    name: \"p\",\n    bins: [],\n    deps: [],\n    c_sources: [],\n" +
		"    c_imports: [],\n    asm_sources: [],\n    targets: [],\n    unsafe_modules: [],\n    links: [],\n)\n"
	pkg := writeFiles(t, map[string]string{"pkg.mk": pkgMk, "std/core/c.mk": "export C(): int {\n    return 1\n}\n"})
	for _, c := range []struct {
		dir, arg string
		want     int
	}{{pkg, ".", 1}, {pkg, "std/core/c.mk", 0}} {
		cmd := exec.Command(exe, "check", c.arg)
		cmd.Dir = c.dir
		cmd.Env = append(os.Environ(), "MK_STD="+std)
		var o, e bytes.Buffer
		cmd.Stdout, cmd.Stderr = &o, &e
		err := cmd.Run()
		status := 0
		if ee, ok := err.(*exec.ExitError); ok {
			status = ee.ExitCode()
		} else if err != nil {
			t.Fatal(err)
		}
		if status != c.want || strings.Contains(e.String(), "goroutine") || strings.Contains(e.String(), "panic") {
			t.Errorf("mk check %s: status %d (want %d) stderr %q", c.arg, status, c.want, e.String())
		}
	}
}

// writeFiles writes slash-path files under a new temporary directory.
func writeFiles(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for p, text := range files {
		full := filepath.Join(dir, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// TestCorpusAcceptsLLVMFlag: --llvm applies to every command and
// --max-diagnostics and --timings to every compiling one; mk corpus once
// refused all three (exit 2), and mk check parsed --llvm but dropped it.
func TestCorpusAcceptsLLVMFlag(t *testing.T) {
	root := writeCorpusRoot(t, "// verdict: parse\nF() {\n}\n")
	st, out, errOut := mk("corpus", "--llvm", t.TempDir(), "--max-diagnostics", "5", "--timings", "--root", root)
	if st != 0 || !strings.Contains(out, "pass 2, fail 0") || strings.Contains(errOut, "not defined") {
		t.Errorf("status %d stdout %q stderr %q", st, out, errOut)
	}
	c := corpusFlags{llvm: "L", maxDiags: -1}
	if cfg := c.driverConfig(io.Discard); cfg.LLVMDir != "L" || cfg.MaxDiags != 0 || cfg.Timings != nil {
		t.Errorf("corpus driver config %+v", cfg)
	}
	cf := compileFlags{llvm: "L", maxDiags: -1}
	if cfg, ok := cf.config("x.mk", io.Discard); !ok || cfg.LLVMDir != "L" {
		t.Errorf("check config LLVMDir %q", cfg.LLVMDir)
	}
}

// TestPanicExits3: a panic outside every driver Guard still exits 3 with
// one line on stderr. The command table is swapped for one that panics.
func TestPanicExits3(t *testing.T) {
	saved := commands
	defer func() { commands = saved }()
	commands = []command{{"boom", "boom", func([]string, io.Writer, io.Writer) int { panic("boom") }}}
	st, _, errOut := mk("boom")
	if st != 3 || !strings.Contains(errOut, "internal compiler error") || strings.Contains(errOut, "goroutine") {
		t.Errorf("status %d stderr %q", st, errOut)
	}
}

// writeCorpusRoot writes a repository root whose doc/01-test.md holds the
// given mk blocks, beside a doc/reference.md that holds only the test
// prelude (one parse block), so mk corpus's exit status does not depend on
// the state of the real docs.
func writeCorpusRoot(t *testing.T, blocks ...string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "doc"), 0o755); err != nil {
		t.Fatal(err)
	}
	ref := "# Reference\n\n## 19. Test prelude\n\n```mk\n// verdict: parse\nstruct Point {\n    x: i32\n}\n```\n"
	text := "# Test\n\n"
	for _, b := range blocks {
		text += "```mk\n" + b + "```\n\n"
	}
	for name, body := range map[string]string{"reference.md": ref, "01-test.md": text} {
		if err := os.WriteFile(filepath.Join(root, "doc", name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// TestCorpusCommandWired: mk corpus runs corpus.Run on its flags, writes the
// report to stdout and returns corpus.ExitStatus (0 nothing failed, 1 a unit
// failed); a root, pattern, extraction or usage error is exit 2 on stderr.
func TestCorpusCommandWired(t *testing.T) {
	good := "// verdict: parse\nF() {\n}\n"
	bad := "// verdict: parse\nF( {\n"
	root := writeCorpusRoot(t, good)
	st, out, errOut := mk("corpus", "--root", root)
	if st != 0 || errOut != "" || !strings.HasPrefix(out, "2 blocks: 0 ok, 2 parse, 0 reject\nparsed: 2 of 2\n") ||
		!strings.Contains(out, "doc/01-test.md:4 parse PASS\n") || !strings.Contains(out, "pass 2, fail 0, pending 0") {
		t.Errorf("passing root: status %d stdout %q stderr %q", st, out, errOut)
	}
	root = writeCorpusRoot(t, good, bad)
	st, out, errOut = mk("corpus", "--root", root)
	if st != 1 || errOut != "" || !strings.Contains(out, "parsed: 2 of 3\n") ||
		!strings.Contains(out, "doc/01-test.md:10 parse FAIL") || !strings.Contains(out, "pass 2, fail 1, pending 0") {
		t.Errorf("failing root: status %d stdout %q stderr %q", st, out, errOut)
	}
	// -v (tools/gate runs `mk corpus -v`) is accepted, keeps one line per unit
	// and changes no byte of the report or the status.
	if vst, vout, verr := mk("corpus", "-v", "--root", root); vst != st || vout != out || verr != "" {
		t.Errorf("-v: status %d stdout %q stderr %q, want status %d and the report without -v", vst, vout, verr, st)
	}
	st, out, errOut = mk("corpus", "--only", "doc/01-test.md:4", root)
	if st != 2 || out != "" || !strings.Contains(errOut, "takes no path") {
		t.Errorf("path argument: status %d stdout %q stderr %q", st, out, errOut)
	}
	st, out, errOut = mk("corpus", "--json", "--only", "doc/01-test.md:4", "--root", root)
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	if st != 0 || errOut != "" || len(lines) != 2 || !strings.HasPrefix(lines[0], `{"unit":"doc/01-test.md:4"`) ||
		!strings.HasPrefix(lines[1], `{"summary":{"blocks":1,`) {
		t.Errorf("--json --only: status %d stdout %q stderr %q", st, out, errOut)
	}
	if st, out, errOut := mk("corpus", "--only", "[", "--root", root); st != 2 || out != "" || !strings.Contains(errOut, "--only") {
		t.Errorf("bad pattern: status %d stdout %q stderr %q", st, out, errOut)
	}
	if st, out, errOut := mk("corpus", "--root", t.TempDir()); st != 2 || out != "" || !strings.Contains(errOut, "mk corpus: ") {
		t.Errorf("no root: status %d stdout %q stderr %q", st, out, errOut)
	}
	unclosed := writeCorpusRoot(t)
	if err := os.WriteFile(filepath.Join(unclosed, "doc", "01-test.md"), []byte("```mk\n// verdict: parse\nF() {\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if st, out, errOut := mk("corpus", "--root", unclosed); st != 2 || out != "" || !strings.Contains(errOut, "mk corpus: ") {
		t.Errorf("extraction error: status %d stdout %q stderr %q", st, out, errOut)
	}
}

// TestCorpusJobsReachDriver: mk corpus -j N bounds the corpus run's
// driver workers, not only corpus.Options.Jobs; a zero Jobs would normalize to
// runtime.NumCPU().
func TestCorpusJobsReachDriver(t *testing.T) {
	var c corpusFlags
	fs := newCorpusFlagSet(io.Discard, &c)
	if _, _, err := parseArgs(fs, []string{"-j", "1"}, false); err != nil {
		t.Fatal(err)
	}
	if cfg := c.driverConfig(io.Discard); cfg.Jobs != 1 {
		t.Errorf("corpus driver config Jobs %d, want 1", cfg.Jobs)
	}
}

// TestTimingsOutsideDiagnostics: --timings writes per-stage lines to stderr
// after the report and never into the --json document.
func TestTimingsOutsideDiagnostics(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "good.mk")
	if err := os.WriteFile(good, []byte("G() {\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MK_STD", t.TempDir())
	st, out, errOut := mk("check", "--json", "--timings", good)
	if st != 0 || out != "{\"version\":1,\"phase\":null,\"diagnostics\":[],\"omitted\":0}\n" {
		t.Errorf("status %d stdout %q", st, out)
	}
	for _, stage := range []string{"stage manifest ", "stage parse ", "stage resolve ", "stage signatures, bodies, flow "} {
		if !strings.Contains(errOut, stage) {
			t.Errorf("stderr %q lacks %q", errOut, stage)
		}
	}
	if st, _, errOut := mk("check", "--json", good); st != 0 || errOut != "" {
		t.Errorf("without --timings: status %d stderr %q", st, errOut)
	}
}

// TestJSONToStderrForRun: mk run writes its --json document to
// stderr (stdout is the program's), and a compile failure runs nothing.
func TestJSONToStderrForRun(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "bad.mk")
	if err := os.WriteFile(file, []byte("import nothing.here\n\nexport Main(sys: &System) {\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	st, out, errOut := mk("run", "--json", file)
	if st != 1 || out != "" || !strings.HasPrefix(errOut, `{"version":1,"phase":"imports","diagnostics":[{"code":"E0202"`) {
		t.Errorf("run --json: status %d stdout %q stderr %q", st, out, errOut)
	}
	st, out, errOut = mk("build", "--json", "-o", filepath.Join(dir, "bad.exe"), file)
	if st != 1 || errOut != "" || !strings.HasPrefix(out, `{"version":1,"phase":"imports"`) {
		t.Errorf("build --json: status %d stdout %q stderr %q", st, out, errOut)
	}
	if _, err := os.Stat(filepath.Join(dir, "bad.exe")); err == nil {
		t.Error("a failed build wrote its executable")
	}
}

// TestMkTestCommand: mk test prints PASS and FAIL lines and the summary on
// stdout and exits 1 when a test failed; -run after the path selects;
// --json gives one JSON object; a bad --timeout exits 2.
func TestMkTestCommand(t *testing.T) {
	if _, err := os.Stat(`C:\Program Files\LLVM\bin\clang.exe`); err != nil && os.Getenv("MK_LLVM") == "" {
		if _, err := exec.LookPath("clang"); err != nil {
			t.Skip("no clang")
		}
	}
	dir := t.TempDir()
	file := filepath.Join(dir, "t.mk")
	src := "test \"good\" {\n    Assert(true)\n}\n\ntest \"bad\" {\n    Assert(false)\n}\n"
	if err := os.WriteFile(file, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	st, out, errOut := mk("test", file)
	if st != 1 || !strings.Contains(out, "PASS main \"good\"\nFAIL main \"bad\": panic (exit 101)\n") || !strings.HasSuffix(out, "1 passed, 1 failed\n") {
		t.Errorf("mk test: status %d stdout %q stderr %q", st, out, errOut)
	}
	st, out, errOut = mk("test", file, "-run", "good")
	if st != 0 || out != "PASS main \"good\"\n1 passed, 0 failed\n" {
		t.Errorf("mk test -run good: status %d stdout %q stderr %q", st, out, errOut)
	}
	st, out, _ = mk("test", "--json", "-run", "bad", file)
	if st != 1 || !strings.HasPrefix(out, `{"tests":[{"module":"main","name":"bad","result":"panic","status":101,`) {
		t.Errorf("mk test --json: status %d stdout %q", st, out)
	}
	if st, _, _ := mk("test", "--timeout=soon", file); st != 2 {
		t.Errorf("bad --timeout: status %d", st)
	}
	// --std: std's own test blocks, no path (Config.StdTests).
	t.Setenv("MK_CACHE", t.TempDir())
	st, out, errOut = mk("test", "--std=std.rt", "-run", "RT-F16-HeapZero")
	if st != 0 || !strings.HasPrefix(out, "PASS std.rt \"RT-F16-HeapZero") || !strings.HasSuffix(out, "1 passed, 0 failed\n") {
		t.Errorf("mk test --std=std.rt: status %d stdout %q stderr %q", st, out, errOut)
	}
	if st, _, errOut := mk("test", "--std=std.rt", file); st != 2 || !strings.Contains(errOut, "takes no path") {
		t.Errorf("mk test --std with a path: status %d stderr %q", st, errOut)
	}
}

// TestCheckWritesGenView: mk check of a package writes the gen/ view of a
// module with a gen line, and reports a hand edit of it as E0904 (exit 1),
// leaving the edit in place.
func TestCheckWritesGenView(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"pkg.mk": "const Package = pkg.Package(\n    name: \"p\",\n    bins: [],\n    deps: [],\n    c_sources: [],\n" +
			"    c_imports: [],\n    asm_sources: [],\n    targets: [],\n    unsafe_modules: [],\n    links: [],\n)\n",
		"g/g.mk": "export Make(g: mut Gen) {\n    g.Emit(quote {\n        export Helper(): int {\n            return 1\n        }\n    })\n}\n",
		"a/a.mk": "import g\n\ngen g.Make()\n",
	})
	if st, _, errOut := mk("check", "--target=x86_64-windows", dir); st != 0 {
		t.Fatalf("check: status %d\n%s", st, errOut)
	}
	view := filepath.Join(dir, "a", "gen", "x86_64-windows", "gen.mk")
	b, err := os.ReadFile(view)
	if err != nil || !strings.Contains(string(b), "export Helper(): int") {
		t.Fatalf("view %q, %v", b, err)
	}
	edited := strings.Replace(string(b), "return 1", "return 5", 1)
	if err := os.WriteFile(view, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	st, out, _ := mk("check", "--json", "--target=x86_64-windows", dir)
	if st != 1 || !strings.Contains(out, `"code":"E0904"`) || !strings.Contains(out, `"file":"a/gen/x86_64-windows/gen.mk"`) {
		t.Errorf("edited view: status %d\n%s", st, out)
	}
	if now, _ := os.ReadFile(view); string(now) != edited {
		t.Errorf("mk check overwrote the hand edit")
	}
}

// TestInterfaceHeadNotValueExit1: `Slice[int].Fill(...)` names an
// interface applied to arguments where a value is wanted. mk check reports
// E0301 and exits 1; it was once a silent Poison and an ICE in lower (exit 3).
func TestInterfaceHeadNotValueExit1(t *testing.T) {
	file := filepath.Join(t.TempDir(), "fill.mk")
	src := "export Main(sys: &System): void {\n    var xs := [1, 2, 3]\n    Slice[int].Fill(mut xs[..], 7)\n}\n"
	if err := os.WriteFile(file, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	st, out, errOut := mk("check", file)
	if st != 1 || out != "" || !strings.Contains(errOut, "E0301") || strings.Contains(errOut, "E0000") {
		t.Errorf("status %d (want 1) stdout %q stderr %q", st, out, errOut)
	}
}
