// Why: Windows passes a command line, not an argument list, so std.proc's
// quoting, environment and PATH search must match what os/exec does.
// What: TestProc_OracleArgs, OracleEnv, OracleSearch and BatchArgs (Windows
// only; on Linux execve's argv needs no quoting, see TestProc_SpawnLinuxBlocks).
// How: one Mk program spawns a Go helper that prints what it received, for
// each case of a spec file; os/exec runs the same cases; outputs must be equal.

package std

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// proc3Helper is the Go program the cases start. "args" prints each later
// argument in hex, one per line; "env" prints, sorted, each environment
// entry whose name starts with MK_P3 in any case, in hex.
const proc3Helper = `package main

import (
	"encoding/hex"
	"fmt"
	"os"
	"sort"
	"strings"
)

func main() {
	if len(os.Args) < 2 {
		os.Exit(2)
	}
	switch os.Args[1] {
	case "args":
		fmt.Printf("n=%d\n", len(os.Args)-2)
		for _, a := range os.Args[2:] {
			fmt.Println(hex.EncodeToString([]byte(a)))
		}
	case "env":
		var es []string
		for _, e := range os.Environ() {
			if strings.HasPrefix(strings.ToUpper(e), "MK_P3") {
				es = append(es, e)
			}
		}
		sort.Strings(es)
		for _, e := range es {
			fmt.Println(hex.EncodeToString([]byte(e)))
		}
	case "exe":
		// Which copy of the helper runs (P3X-3).
		p, _ := os.Executable()
		fmt.Println(strings.ToLower(p))
	case "sysroot":
		// Whether the environment names SYSTEMROOT, in any case (P3X-8).
		v, ok := "", false
		for _, e := range os.Environ() {
			if i := strings.IndexByte(e, '='); i > 0 && strings.EqualFold(e[:i], "SYSTEMROOT") {
				v, ok = e[i+1:], true
			}
		}
		if ok {
			fmt.Println("SYSTEMROOT=" + v)
		} else {
			fmt.Println("SYSTEMROOT unset")
		}
	}
}
`

// proc3Spawner is the Mk program. Its arguments: the spec file, then the
// mode: "args" (each line: the program and its arguments, in hex, spawned
// with the environment inherited), "dir" (each line: Command.dir, then
// the program and its arguments), or "extend", "replace" and "rootenv"
// (each line: the helper, then environment entries in hex, spawned with
// "env"; rootenv is replace with "sysroot" instead of "env"). For
// each case it prints "=== <code>" and the child's stdout, or
// "=== error <name>".
const proc3Spawner = `import std.fs
import std.proc

Nib(c: u8): int {
    if c >= 97 {
        return int(c) - 87
    }
    return int(c) - 48
}

// Unhex decodes x followed by hex digits.
Unhex(a: &Arena, s: str): str from a {
    b := s.AsBytes()
    var out := a.Slice[u8]((b.Len() - 1) / 2) else {
        proc.Exit(4)
    }
    for i in 0..out.Len() {
        out[i] = Trunc[u8](Nib(b[2 * i + 1]) * 16 + Nib(b[2 * i + 2]))
    }
    return str.From(out[..]) else {
        proc.Exit(5)
    }
}

Name(e: proc.Error): str {
    return match e {
        proc.NotFound { "NotFound" }
        proc.Denied { "Denied" }
        proc.Os(code: c) { if c == 0 { "Os0" } else { "Os" } }
        proc.Unsupported { "Unsupported" }
        _ { "other" }
    }
}

export Main(sys: &System): int {
    io := sys.io
    spec := fs.ReadText(sys.arena, io, sys.args[1]) else {
        return 3
    }
    mode := sys.args[2]
    var out := sys.Stdout()
    var buf: [65536]u8 = [0; 65536]
    for line in spec.Lines() {
        var words: [64]str = [""; 64]
        var n := 0
        for w in line.Words() {
            if n == 0 && (mode == "extend" || mode == "replace" || mode == "rootenv") {
                words[0] = Unhex(sys.arena, w)
                words[1] = if mode == "rootenv" { "sysroot" } else { "env" }
                n = 2
            } else {
                words[n] = Unhex(sys.arena, w)
                n += 1
            }
        }
        var env: [64]str = [""; 64]
        var c := proc.DefaultCommand
        c.stdout = proc.Stdio.Pipe
        c.stdin = proc.Stdio.Null
        c.args = words[0..n]
        if mode == "dir" {
            c.dir = some(words[0])
            c.args = words[1..n]
        }
        if mode == "extend" || mode == "replace" || mode == "rootenv" {
            for i in 2..n {
                env[i - 2] = words[i]
            }
            c.args = words[0..2]
            c.env = if mode == "extend" { proc.Env.Extend(vars: env[0..n - 2]) } else { proc.Env.Replace(vars: env[0..n - 2]) }
            (child, stdin, stdout, stderr) := proc.Spawn(sys.arena, io, &c) else e {
                out.Str("=== error ") else { return 6 }
                out.Str(Name(e)) else { return 6 }
                out.Str("\n") else { return 6 }
                continue
            }
            Report(sys, mut out, child, stdin, stdout, stderr, mut buf)
            continue
        }
        (child, stdin, stdout, stderr) := proc.Spawn(sys.arena, io, &c) else e {
            out.Str("=== error ") else { return 6 }
            out.Str(Name(e)) else { return 6 }
            out.Str("\n") else { return 6 }
            continue
        }
        Report(sys, mut out, child, stdin, stdout, stderr, mut buf)
    }
    out.Flush() else {
        return 7
    }
    return 0
}

Report(sys: &System, out: mut Writer, child: proc.Child, stdin: ?proc.PipeWriter, stdout: ?proc.PipeReader, stderr: ?proc.PipeReader, buf: mut [65536]u8) {
    if w := stdin {
        w.Close()
    }
    if r := stderr {
        r.Close()
    }
    r := stdout.Must()
    var k := 0
    for k < 65536 {
        m := r.Read(sys.io, mut buf[k..]) else {
            break
        }
        if m == 0 {
            break
        }
        k += m
    }
    r.Close()
    st := child.Wait(sys.io) else {
        proc.Exit(8)
    }
    code := st.code else { -1 }
    head := sys.arena.Fmt("=== {code}\n", (code)) else { proc.Exit(9) }
    out.Str(head) else { proc.Exit(9) }
    out.Bytes(buf[0..k]) else { proc.Exit(9) }
}
`

// proc3Case is one case: the program and its arguments (args mode), or the
// environment entries (extend and replace modes).
type proc3Case []string

// proc3Spec writes the cases as hex lines.
func proc3Spec(t *testing.T, dir string, cases []proc3Case) string {
	t.Helper()
	var b strings.Builder
	for _, c := range cases {
		for i, w := range c {
			if i > 0 {
				b.WriteByte(' ')
			}
			b.WriteString("x" + hex.EncodeToString([]byte(w)))
		}
		b.WriteByte('\n')
	}
	p := filepath.Join(dir, fmt.Sprintf("spec%d.txt", len(cases)))
	if err := os.WriteFile(p, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// proc3Oracle runs the cases through os/exec, with the environment as the
// mode says (the Mk spawner inherits this process's): the expected output.
func proc3Oracle(t *testing.T, mode string, cases []proc3Case) string {
	t.Helper()
	var b strings.Builder
	for _, c := range cases {
		var cmd *exec.Cmd
		switch mode {
		case "args":
			cmd = exec.Command(c[0], c[1:]...)
		default:
			cmd = exec.Command(c[0], "env")
			if mode == "extend" {
				cmd.Env = append(os.Environ(), c[1:]...)
			} else {
				cmd.Env = append([]string{}, c[1:]...)
			}
		}
		var o bytes.Buffer
		cmd.Stdout = &o
		err := cmd.Run()
		var ee *exec.ExitError
		switch {
		case err == nil:
			fmt.Fprintf(&b, "=== 0\n%s", o.String())
		case errors.As(err, &ee):
			fmt.Fprintf(&b, "=== %d\n%s", ee.ExitCode(), o.String())
		case errors.Is(err, exec.ErrNotFound), errors.Is(err, exec.ErrDot), errors.Is(err, os.ErrNotExist):
			b.WriteString("=== error NotFound\n")
		default:
			fmt.Fprintf(&b, "=== error %v\n", err)
		}
	}
	return strings.ReplaceAll(b.String(), "\r\n", "\n")
}

// proc3Tools builds the helper (Go) and the spawner (Mk) into one
// directory: their paths.
func proc3Tools(t *testing.T) (dir, helper, spawner string) {
	t.Helper()
	spawner = proc3BuildSpawner(t)
	dir = t.TempDir()
	src := filepath.Join(dir, "helper.go")
	if err := os.WriteFile(src, []byte(proc3Helper), 0o644); err != nil {
		t.Fatal(err)
	}
	helper = filepath.Join(dir, "p3probe.exe")
	goTool := filepath.Join(runtime.GOROOT(), "bin", "go.exe")
	cmd := exec.Command(goTool, "build", "-o", helper, src)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GO111MODULE=off", "GOFLAGS=")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build helper: %v\n%s", err, out)
	}
	return dir, helper, spawner
}

// proc3RunSpawner runs the spawner over the cases in dir (its working
// directory), with env added to its environment: its output.
func proc3RunSpawner(t *testing.T, spawner, dir, mode string, cases []proc3Case, env ...string) string {
	t.Helper()
	spec := proc3Spec(t, dir, cases)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, spawner, spec, mode)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	var o, e bytes.Buffer
	cmd.Stdout, cmd.Stderr = &o, &e
	if err := cmd.Run(); err != nil {
		t.Fatalf("spawner %s: %v\nstderr: %s", mode, err, e.String())
	}
	return strings.ReplaceAll(o.String(), "\r\n", "\n")
}

// proc3Diff fails t at the first case whose outputs differ.
func proc3Diff(t *testing.T, what, got, want string, cases []proc3Case) {
	t.Helper()
	if got == want {
		return
	}
	g := strings.Split(got, "=== ")
	w := strings.Split(want, "=== ")
	for i := 1; i < len(g) || i < len(w); i++ {
		var gi, wi string
		if i < len(g) {
			gi = g[i]
		}
		if i < len(w) {
			wi = w[i]
		}
		if gi != wi {
			c := proc3Case{}
			if i-1 < len(cases) {
				c = cases[i-1]
			}
			t.Fatalf("%s: case %d %q:\nmk:      %q\nos/exec: %q", what, i-1, []string(c), gi, wi)
		}
	}
	t.Fatalf("%s: outputs differ\nmk: %q\nos/exec: %q", what, got, want)
}

// proc3ArgCases are the fixed adversarial argument lists.
var proc3ArgCases = [][]string{
	{},
	{""},
	{"", ""},
	{"a b"},
	{`"`},
	{`\`},
	{`\\`},
	{`a\"b`},
	{`a\\"b`},
	{`a\\\"b`},
	{`a\`},
	{`a\\`},
	{`a b\`},
	{`a b\\`},
	{`"a b"`},
	{"tab\there"},
	{"new\nline"},
	{"\u00fc\u65e5\u672c\U0001F600"},
	{"%PATH%", "!x!", "^", "&", "|", "<", ">", "(", ")"},
	{" leading", "trailing ", "  "},
	{`\\server\share\`, `C:\Program Files\`},
	{`""`, `"""`, `\"\"`},
	{"=", "a=b", "--x=\"y z\""},
}

// TestProc_OracleArgs: argument lists reach a child as os/exec delivers
// them, and as sent.
//
// Across the oracle tests, one Mk program spawns, through proc.Spawn, a Go
// helper that prints the arguments and the environment it received, for each case of a spec
// file, and the same cases run through os/exec; the two outputs must be
// equal, and each case's arguments must arrive as sent. The cases: a fixed
// adversarial list (quotes, backslash runs before quotes and at the end,
// spaces, tabs, newlines, an empty argument, cmd.exe metacharacters,
// non-ASCII), 400 random lists over the same alphabet, the environment
// extended and replaced (a name given twice, a name in another case on
// Windows, an empty value), PATH search (found by name, with PATHEXT on
// Windows) and the current directory refused, and on Windows a .bat
// target given cmd.exe's metacharacters, which arrive unexpanded, and a
// line break, which is refused.
//
// Windows only: on Linux the arguments are execve's argv, which no
// quoting touches; TestProc_SpawnLinuxBlocks covers Linux.
func TestProc_OracleArgs(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows quoting; Linux is argv (TestProc_SpawnLinuxBlocks)")
	}
	dir, helper, spawner := proc3Tools(t)
	var cases []proc3Case
	for _, a := range proc3ArgCases {
		cases = append(cases, append(proc3Case{helper, "args"}, a...))
	}
	alphabet := []string{"a", "b", " ", "\t", `"`, `\`, `\\`, "\u00fc", "%", "^", "&", "\n", "="}
	r := rand.New(rand.NewSource(8))
	for range 400 {
		n := r.Intn(5)
		c := proc3Case{helper, "args"}
		for range n {
			var w strings.Builder
			for range r.Intn(8) {
				w.WriteString(alphabet[r.Intn(len(alphabet))])
			}
			c = append(c, w.String())
		}
		cases = append(cases, c)
	}
	got := proc3RunSpawner(t, spawner, dir, "args", cases)
	want := proc3Oracle(t, "args", cases)
	proc3Diff(t, "args", got, want, cases)
	// And each list arrived as sent (the oracle could share a fault).
	var exp strings.Builder
	for _, c := range cases {
		fmt.Fprintf(&exp, "=== 0\nn=%d\n", len(c)-2)
		for _, a := range c[2:] {
			exp.WriteString(hex.EncodeToString([]byte(a)) + "\n")
		}
	}
	proc3Diff(t, "args as sent", got, exp.String(), cases)
}

// TestProc_OracleEnv: the environment extended and replaced as os/exec
// builds it (the later of two entries for a name wins; on Windows a name
// matches in any case).
func TestProc_OracleEnv(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows only")
	}
	t.Setenv("MK_P3_PARENT", "parent")
	t.Setenv("MK_P3_KEEP", "keep")
	dir, helper, spawner := proc3Tools(t)
	entries := [][]string{
		{},
		{"MK_P3_A=1"},
		{"MK_P3_A=1", "MK_P3_A=2"},
		{"MK_P3_PARENT=over"},
		{"mk_p3_parent=lower"},
		{"MK_P3_E="},
		{"MK_P3_X=a=b", "MK_P3_Y=\u00fc"},
		{"MK_P3_PARENT=one", "mk_p3_PARENT=two"},
	}
	for _, mode := range []string{"extend", "replace"} {
		var cases []proc3Case
		for _, e := range entries {
			cases = append(cases, append(proc3Case{helper}, e...))
		}
		got := proc3RunSpawner(t, spawner, dir, mode, cases)
		want := proc3Oracle(t, mode, cases)
		proc3Diff(t, mode, got, want, cases)
	}
}

// TestProc_OracleSearch: a name with no separator is found in PATH (with
// PATHEXT), as exec.LookPath finds it, and never in the current
// directory, where exec.LookPath answers ErrDot.
func TestProc_OracleSearch(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows only")
	}
	dir, helper, spawner := proc3Tools(t)
	bin := t.TempDir()
	b, err := os.ReadFile(helper)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "p3found.exe"), b, 0o755); err != nil {
		t.Fatal(err)
	}
	// p3here.exe is in the spawner's working directory only.
	if err := os.WriteFile(filepath.Join(dir, "p3here.exe"), b, 0o755); err != nil {
		t.Fatal(err)
	}
	// An empty entry and "." name the current directory to SearchPathW
	// and to a shell; neither may find p3here.
	sep := string(os.PathListSeparator)
	path := bin + sep + sep + "." + sep + os.Getenv("PATH")
	t.Setenv("PATH", path)
	cases := []proc3Case{
		{"p3found", "args", "x"},
		{"p3found.exe", "args", "y"},
		{"P3FOUND", "args", "z"},
		{"p3here", "args"},
		{"p3here.exe", "args"},
		{"p3-no-such-program", "args"},
		{`.\p3here.exe`, "args", "dot"},
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
	if !strings.Contains(got, "=== error NotFound\n=== error NotFound\n=== error NotFound\n") {
		t.Fatalf("the current directory was searched:\n%s", got)
	}
}

// TestProc_BatchArgs: a .bat target, found by name through PATHEXT or by
// path, gets each argument that holds one of cmd.exe's metacharacters as
// one quoted token, unexpanded (the script's echo %1 prints it with its
// quotes, and no variable leaks), and an argument with a line break is
// refused before anything runs (Os(0)); Rust's Command since 1.77.2 is
// the rule.
func TestProc_BatchArgs(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows only")
	}
	dir, _, spawner := proc3Tools(t)
	bat := filepath.Join(dir, "p3echo.bat")
	if err := os.WriteFile(bat, []byte("@echo off\r\necho %1\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MK_P3_SECRET", "leaked")
	args := []string{"a&b", "%MK_P3_SECRET%", "x|y", "a b", "q\"q", "^", "<>", "!MK_P3_SECRET!", "end\\"}
	var cases []proc3Case
	for i, a := range args {
		prog := bat
		if i%2 == 1 {
			// By name: p3echo is p3echo.bat through PATHEXT.
			prog = "p3echo"
		}
		cases = append(cases, proc3Case{prog, a})
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	cases = append(cases, proc3Case{bat, "line\nbreak"}, proc3Case{bat, "cr\rx"})
	got := proc3RunSpawner(t, spawner, dir, "args", cases)
	parts := strings.Split(got, "=== ")[1:]
	if len(parts) != len(cases) {
		t.Fatalf("%d answers for %d cases:\n%s", len(parts), len(cases), got)
	}
	for i, a := range args {
		if strings.Contains(parts[i], "leaked") {
			t.Fatalf("case %q expanded a variable: %q", a, parts[i])
		}
		if !strings.HasPrefix(parts[i], "0\n\"") {
			t.Fatalf("case %q: %q", a, parts[i])
		}
	}
	// The arguments with no quote or backslash come back as one token,
	// quoted, exactly.
	for i, a := range args {
		if strings.ContainsAny(a, "\"\\") {
			continue
		}
		if want := "0\n\"" + a + "\"\n"; parts[i] != want {
			t.Fatalf("case %q: got %q, want %q", a, parts[i], want)
		}
	}
	for _, p := range parts[len(args):] {
		if p != "error Os0\n" {
			t.Fatalf("a line break was not refused: %q", p)
		}
	}
}
