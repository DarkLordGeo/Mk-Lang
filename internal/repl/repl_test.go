// Why: mk repl is a long-lived session over the evaluator, so state, errors
// and limits must behave across many entries, not only in one.
// What: session tests: expressions, declarations, std and package
// imports, :type, :query and other commands, errors, fuel, Ctrl+C, the binary.
// How: scripted sessions through stdin and stdout pipes; TestMain shares one
// cache directory so std is checked cold once per test binary.

package repl

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"mk/internal/types"
)

// TestMain gives the tests one cache directory when MK_CACHE is unset, so
// std is checked cold once per test binary, not once per session.
func TestMain(m *testing.M) {
	os.Exit(runWithCache(m))
}

func runWithCache(m *testing.M) int {
	if os.Getenv("MK_CACHE") == "" {
		dir, err := os.MkdirTemp("", "mk-repl-test-cache-")
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		defer os.RemoveAll(dir)
		os.Setenv("MK_CACHE", dir)
	}
	return m.Run()
}

// pipeSession runs script through Run with stdin and stdout as OS pipes.
func pipeSession(t *testing.T, opt Options, script string) (stdout, stderr string, code int) {
	t.Helper()
	inR, inW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		io.WriteString(inW, script)
		inW.Close()
	}()
	got := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(outR)
		got <- string(b)
	}()
	var e bytes.Buffer
	code = Run(inR, outW, &e, opt)
	outW.Close()
	inR.Close()
	return <-got, e.String(), code
}

// scanLine reads one line within two minutes, so a missing answer fails the
// test instead of hanging it until the test binary's timeout.
func scanLine(t *testing.T, sc *bufio.Scanner) (string, bool) {
	t.Helper()
	type line struct {
		text string
		ok   bool
	}
	ch := make(chan line, 1)
	go func() {
		ok := sc.Scan()
		ch <- line{sc.Text(), ok}
	}()
	select {
	case l := <-ch:
		return l.text, l.ok
	case <-time.After(2 * time.Minute):
		t.Fatal("no line within 2 minutes")
	}
	return "", false
}

// lines splits output into lines, dropping the final empty one.
func lines(s string) []string {
	s = strings.TrimSuffix(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

func wantLines(t *testing.T, got string, want ...string) {
	t.Helper()
	g := lines(got)
	if strings.Join(g, "\n") != strings.Join(want, "\n") {
		t.Errorf("stdout:\n%s\nwant:\n%s", strings.Join(g, "\n"), strings.Join(want, "\n"))
	}
}

// standalone is a session over std alone, from a working directory with no
// pkg.mk (the test's own directory has none).
func standalone(t *testing.T) Options {
	t.Helper()
	if _, err := os.Stat("pkg.mk"); err == nil {
		t.Fatal("the test directory holds a pkg.mk")
	}
	return Options{}
}

// TestScriptedSession: values of expressions, a function declared
// across lines and then called, a struct, :type, :query over the session's
// own declaration, a redefinition that replaces the first, and the end of
// input ending the session with status 0.
func TestScriptedSession(t *testing.T) {
	script := `1 + 2
"hi"
'c'
3 > 2
-7
Sq(x: int): int {
    return x * x
}
Sq(12)
:type Sq(2) > 3
:type "x"
:query sig Sq
struct P {
    x: int
    y: int
}
P(x: 1, y: 2).y
Sq(x: int): int { return x + 1 }
Sq(1)
(1 +
    2) * 3
`
	out, errOut, code := pipeSession(t, standalone(t), script)
	// No entry has a diagnostic of its own; std may hold an error of its
	// own while it is being edited, reported once at the start and outside
	// every entry.
	if code != 0 || strings.Contains(errOut, "<repl") {
		t.Fatalf("status %d, stderr:\n%s", code, errOut)
	}
	wantLines(t, out, "3", `"hi"`, "'c'", "true", "-7", "144", "bool", "str", "Sq(x: int): int", "module main", "2", "2", "9")
}

// TestErrorsKeepSessionAlive: a name error, a type error, a syntax
// error and an Io call are reported on stderr at the entry's own line and
// column, nothing of them is kept, and the next entries still run.
func TestErrorsKeepSessionAlive(t *testing.T) {
	script := `nope + 1
1 + "x"
Bad(): int { return true }
Bad()
1 2
Print(1)
:decls
40 + 2
`
	out, errOut, code := pipeSession(t, standalone(t), script)
	if code != 0 {
		t.Fatalf("status %d", code)
	}
	wantLines(t, out, "42")
	for _, w := range []string{"<repl 1>:1:1: E0201", "<repl 2>:", "E0301", "<repl 3>:1:21: E0301", "<repl 4>:1:1: E0201", "<repl 5>:", "E0101", "<repl 6>:1:1: E0304"} {
		if !strings.Contains(errOut, w) {
			t.Errorf("stderr lacks %q:\n%s", w, errOut)
		}
	}
	if strings.Contains(errOut, "repl.mk:") || strings.Contains(errOut, ".mk-repl-") {
		t.Errorf("a scratch-file path reached the user:\n%s", errOut)
	}
}

// TestFuelLimit: evaluation runs under compile-time fuel. The
// default limit (4,000,000 steps) stops a loop that never ends; :fuel sets
// a lower one that stops a long loop the default would finish; E0902 names
// the steps and the session goes on; :fuel default restores the default.
func TestFuelLimit(t *testing.T) {
	script := `Forever(): int {
    for {
    }
    return 0
}
Count(n: int): int {
    var i := 0
    for i < n {
        i += 1
    }
    return i
}
Forever()
Count(1000)
:fuel 500
:fuel
Count(1000) // the fuel clause goes before this comment
:fuel default
Count(1000)
:fuel
`
	out, errOut, code := pipeSession(t, standalone(t), script)
	if code != 0 {
		t.Fatalf("status %d, stderr:\n%s", code, errOut)
	}
	def := fmt.Sprint(types.DefaultFuel) + ", the default"
	wantLines(t, out, "1000", "500", "1000", def)
	if n := strings.Count(errOut, ": E0902 "); n != 2 {
		t.Errorf("want 2 E0902 (Forever at the default, Count at 500), got %d:\n%s", n, errOut)
	}
	for _, w := range []string{"fuel: " + fmt.Sprint(types.DefaultFuel), "fuel: 500", ":fuel <steps> (now 500)"} {
		if !strings.Contains(errOut, w) {
			t.Errorf("stderr lacks %q:\n%s", w, errOut)
		}
	}
	// Options.Fuel (mk repl --fuel) sets the session's limit from the start.
	out, errOut, _ = pipeSession(t, Options{Fuel: 500}, "Count(n: int): int {\n    var i := 0\n    for i < n {\n        i += 1\n    }\n    return i\n}\nCount(1000)\nCount(3)\n")
	wantLines(t, out, "3")
	if !strings.Contains(errOut, "E0902") {
		t.Errorf("--fuel 500: no E0902:\n%s", errOut)
	}
}

// TestStdImports: an import is kept once it resolves, written only
// while something uses it (an unused import is E0208, which would stop
// every later evaluation), and an unknown module is refused.
func TestStdImports(t *testing.T) {
	script := `import std.math
1 + 1
import std.nosuch
Twice(x: int): int { return 2 * x }
Twice(4)
"math.Abs is not a call"
:decls
`
	out, errOut, code := pipeSession(t, standalone(t), script)
	if code != 0 {
		t.Fatalf("status %d", code)
	}
	wantLines(t, out, "2", "8", `"math.Abs is not a call"`, "import std.math", "Twice(x: int): int { return 2 * x }")
	// Only the session's own diagnostics are counted: std may hold an error
	// of its own while it is being edited, reported once at the start.
	if !strings.Contains(errOut, "<repl 3>:1:8: E0202") || strings.Count("\n"+errOut, "\n<repl ") != 1 {
		t.Errorf("want exactly one E0202 for std.nosuch:\n%s", errOut)
	}
}

// writeTree writes files under a new directory.
func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for p, text := range files {
		f := filepath.Join(dir, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(f), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(f, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

const testManifest = `const Package = pkg.Package(
    name: "pk",
    bins: [pkg.Bin(name: "pk", root: ".", link_script: "", cpu: "", subsystem: "console")],
    deps: [],
    c_sources: [],
    c_imports: [],
    asm_sources: [],
    targets: [],
    unsafe_modules: [],
    links: [],
)
`

// TestPackageImports: in a package the session sits in the root
// module, so it reaches the root module's private items and imports the
// package's modules; :query answers about them; the package's files are
// unchanged and the scratch file is gone after the session.
func TestPackageImports(t *testing.T) {
	files := map[string]string{
		"pkg.mk":        testManifest,
		"main.mk":       "import src.util\n\nexport Main(sys: &System) {\n    _ = Helper(util.Twice(1))\n}\n\nHelper(x: int): int {\n    return x + 100\n}\n",
		"src/util/u.mk": "export Twice(x: int): int {\n    return 2 * x\n}\n",
	}
	dir := writeTree(t, files)
	script := `import src.util
util.Twice(21)
Helper(util.Twice(1))
util.Nope(1)
:query sig util.Twice
`
	out, errOut, code := pipeSession(t, Options{Root: dir}, script)
	if code != 0 {
		t.Fatalf("status %d, stderr:\n%s", code, errOut)
	}
	wantLines(t, out, "42", "102", "export Twice(x: int): int", "module src.util")
	if !strings.Contains(errOut, "<repl 4>:1:6: E0201") {
		t.Errorf("stderr:\n%s", errOut)
	}
	for p, text := range files {
		got, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(p)))
		if err != nil || string(got) != text {
			t.Errorf("%s changed: %q %v", p, got, err)
		}
	}
	ents, _ := os.ReadDir(dir)
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), ".mk-repl-") {
			t.Errorf("scratch file left behind: %s", e.Name())
		}
	}
}

// TestPackageDiagnosticsOnce: a package with an error of its own is
// reported once when the session starts, and an entry that does not depend
// on it still gets its value. A names error in the package stops the check
// before any evaluation (the phase rule), and the entry then says so.
func TestPackageDiagnosticsOnce(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"pkg.mk":       testManifest,
		"main.mk":      "export Main(sys: &System) {\n}\n",
		"src/bad/b.mk": "export F(): int {\n    return true\n}\n",
	})
	out, errOut, code := pipeSession(t, Options{Root: dir}, "6 * 7\n")
	if code != 0 {
		t.Fatalf("status %d", code)
	}
	wantLines(t, out, "42")
	if strings.Count(errOut, ": E0301 ") != 1 || !strings.Contains(errOut, "src/bad/b.mk:2:12: E0301") {
		t.Errorf("want the package's E0301 once, at src/bad/b.mk:2:12:\n%s", errOut)
	}
	dir = writeTree(t, map[string]string{
		"pkg.mk":  testManifest,
		"main.mk": "export Main(sys: &System) {\n    _ = Missing()\n}\n",
	})
	out, errOut, _ = pipeSession(t, Options{Root: dir}, "6 * 7\n")
	wantLines(t, out)
	if !strings.Contains(errOut, "outside this session (the package's or std's) stop this entry") || strings.Count(errOut, "main.mk:2:9: E0201") != 2 {
		t.Errorf("names error in the package:\n%s", errOut)
	}
}

// TestCommands: :help, an unknown command, :reset, :quit before the
// end of input, and a .mk file root refused as a usage error (status 2).
func TestCommands(t *testing.T) {
	out, errOut, code := pipeSession(t, standalone(t), ":help\nF(): int { return 1 }\n:reset\nF()\n:nope\n:quit\n2 + 2\n")
	if code != 0 {
		t.Fatalf("status %d", code)
	}
	if !strings.Contains(out, ":query <q> [args]") || strings.Contains(out, "4") {
		t.Errorf("stdout:\n%s", out)
	}
	if !strings.Contains(errOut, "<repl 2>:1:1: E0201") || !strings.Contains(errOut, "unknown command :nope") {
		t.Errorf("stderr:\n%s", errOut)
	}
	f := filepath.Join(t.TempDir(), "a.mk")
	os.WriteFile(f, []byte("A(): int { return 1 }\n"), 0o644)
	if _, errOut, code := pipeSession(t, Options{Root: f}, "1\n"); code != 2 || !strings.Contains(errOut, "not a directory") {
		t.Errorf("file root: status %d stderr %q", code, errOut)
	}
}

// TestMkReplProcess: the built mk binary, `mk repl` with stdin and
// stdout as pipes from another process: values on stdout, a diagnostic on
// stderr, status 0 at the end of input, no prompt when stdin is a pipe.
func TestMkReplProcess(t *testing.T) {
	if testing.Short() {
		t.Skip("builds mk")
	}
	exe := filepath.Join(t.TempDir(), "mk")
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	gobin := filepath.Join(runtime.GOROOT(), "bin", "go")
	if out, err := exec.Command(gobin, "build", "-o", exe, "mk/cmd/mk").CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	cmd := exec.Command(exe, "repl")
	cmd.Dir = t.TempDir()
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var e bytes.Buffer
	cmd.Stderr = &e
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	sc := bufio.NewScanner(stdout)
	// One entry at a time: each answer is read before the next is written,
	// as a person or a script drives the session.
	ask := func(entry, want string) {
		t.Helper()
		io.WriteString(stdin, entry+"\n")
		got, ok := scanLine(t, sc)
		if !ok {
			t.Fatalf("%s: no answer (stderr %q)", entry, e.String())
		}
		if got != want {
			t.Errorf("%s: got %q, want %q", entry, got, want)
		}
	}
	ask("1 + 2", "3")
	io.WriteString(stdin, "undefinedName\n")
	ask("Twice(x: int): int { return 2 * x }\nTwice(21)", "42")
	ask(":type Twice(1)", "int")
	stdin.Close()
	if got, ok := scanLine(t, sc); ok {
		t.Errorf("unexpected output %q", got)
	}
	if err := cmd.Wait(); err != nil {
		t.Errorf("mk repl: %v (stderr %q)", err, e.String())
	}
	if !strings.Contains(e.String(), "<repl 2>:1:1: E0201") {
		t.Errorf("stderr: %q", e.String())
	}
}

// TestInterruptClosesSession: ServeUntil (mk repl's Ctrl+C path)
// returns 130 when stop fires while the session waits for input, and the
// scratch file is gone. The root is a directory of .mk files with no Main
// (directory mode, a bin root), so the session's empty Main is what lets
// A() + 1 evaluate.
func TestInterruptClosesSession(t *testing.T) {
	dir := writeTree(t, map[string]string{"a.mk": "A(): int {\n    return 1\n}\n"})
	s, err := New(Options{Root: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	inR, inW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer inW.Close()
	defer inR.Close()
	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer outW.Close()
	sc := bufio.NewScanner(outR)
	stop := make(chan os.Signal, 1)
	code := make(chan int, 1)
	var e bytes.Buffer
	go func() { code <- s.ServeUntil(inR, outW, &e, stop) }()
	io.WriteString(inW, "A() + 1\n")
	if got, ok := scanLine(t, sc); !ok || got != "2" {
		t.Fatalf("A() + 1: %q (stderr %q)", got, e.String())
	}
	scratch := s.ScratchFile()
	stop <- os.Interrupt
	if c := <-code; c != 130 {
		t.Errorf("status %d, want 130", c)
	}
	if _, err := os.Stat(scratch); err == nil || scratch == "" {
		t.Error("scratch file left behind")
	}
	if _, err := os.Stat(filepath.Join(dir, "a.mk")); err != nil {
		t.Errorf("the directory's own file: %v", err)
	}
}

// TestSyntaxInFunctionBody: a function whose body does not parse is
// reported at the body's line, not as an expression that stops at the
// signature, and is not kept.
func TestSyntaxInFunctionBody(t *testing.T) {
	out, errOut, code := pipeSession(t, standalone(t), "G(): int {\n    return 1 2\n}\nG()\n")
	if code != 0 {
		t.Fatalf("status %d", code)
	}
	wantLines(t, out)
	if !strings.Contains(errOut, "<repl 1>:2:") || !strings.Contains(errOut, "<repl 2>:1:1: E0201") {
		t.Errorf("stderr:\n%s", errOut)
	}
}
