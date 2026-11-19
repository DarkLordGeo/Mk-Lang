// Why: the harness's parts can each pass alone while list, run and cover
// fail together against a real mk; this runs them all.
// What: end-to-end tests of list, run and cover on a throwaway dogfood in a
// temporary directory, never under Dogfoods/.
// How: mk is built from ./cmd/mk and the program for the host target in dev;
// its cases pass, fail and are skipped by -changed. Linux and QEMU runs are
// not started here (they need the WSL lock and QEMU).

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const e2ePkg = `const Package = pkg.Package(
    name: "hello",
    bins: [pkg.Bin(name: "hello", root: "src", link_script: "", cpu: "", subsystem: "console"), pkg.Bin(name: "client", root: "client", link_script: "", cpu: "", subsystem: "console")],
    deps: [],
    c_sources: [],
    c_imports: [],
    asm_sources: [],
    targets: [],
    unsafe_modules: [],
    links: [],
)
`

const e2eMain = `import std.hash
import std.math
import std.proc

export Main(sys: &System): (WriteError | OutOfMemory)!void {
    var out := sys.Stdout()
    if sys.args.Len() > 1 && sys.args[1] == "fail" {
        var e := sys.Stderr()
        try Print(mut e, "hello: failing on purpose\n", ())
        try e.Flush()
        proc.Exit(3)
    }
    for i in 1..sys.args.Len() {
        try Print(mut out, "arg {a}\n", (a: sys.args[i]))
    }
    s := "  hi there  ".Trim()
    c := hash.Crc32(s.AsBytes())
    try Print(mut out, "{s} {c}\n", (s: s, c: c))
}

test "min" {
    Assert(math.Min(1, 2) == 1)
}
`

// e2eClient is a second bin that no case runs: math.Max has a body
// only there.
const e2eClient = `import std.math

export Main(sys: &System): (WriteError | OutOfMemory)!void {
    var out := sys.Stdout()
    x := math.Max(sys.args.Len(), 3)
    try Print(mut out, "{x}\n", (x: x))
}
`

// e2ePad brings the dogfood to the minimum of 800 lines.
func e2ePad() string {
	var b strings.Builder
	for i := range 800 {
		fmt.Fprintf(&b, "const Pad%d: int = %d\n", i, i)
	}
	return b.String()
}

const e2eReadme = "# hello\n\n## Section 18 lines\n\n```text\nstr.Trim(self): str\n// std.hash\nhash.Crc32(data: []u8): u32\n```\n"

// buildMk builds ./cmd/mk into dir and skips when it has no LLVM toolchain.
func buildMk(t *testing.T, dir string) string {
	t.Helper()
	if testing.Short() {
		t.Skip("-short: builds mk and a program")
	}
	mk := filepath.Join(dir, "mk"+exeSuffix())
	cmd := exec.Command("go", "build", "-o", mk, "./cmd/mk")
	cmd.Dir = filepath.Join("..", "..")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build ./cmd/mk: %v\n%s", err, out)
	}
	if out, err := exec.Command(mk, "version").CombinedOutput(); err != nil || !bytes.Contains(out, []byte("clang")) {
		t.Skipf("mk version finds no LLVM toolchain (clang): %v %s", err, out)
	}
	return mk
}

func TestEndToEnd(t *testing.T) {
	tmp := t.TempDir()
	mk := buildMk(t, tmp)
	dogs := filepath.Join(tmp, "Dogfoods")
	work := filepath.Join(tmp, "work")
	d := filepath.Join(dogs, "hello")
	write(t, d, map[string]string{
		"pkg.mk":          e2ePkg,
		"src/main.mk":     e2eMain,
		"src/pad.mk":      e2ePad(),
		"client/main.mk":  e2eClient,
		"README.md":       e2eReadme,
		"cases/args.args": "one\ntwo words\n$CASES/x.txt\n",
		"cases/args.out":  "arg one\narg two words\narg $CASES/x.txt\nhi there 3819140844\n",
		"cases/fail.args": "fail\n",
		"cases/fail.err":  "hello: failing on purpose\n",
		"cases/fail.code": "3\n",
		"cases/sim.sim":   "seeds 1..2\n",
		"cases/sim.out":   "arg --seed\narg $SEED\nhi there 3819140844\n",
		"cases/bench.out": "hi there 3819140844\n",
		"cases/wrong.out": "hello\n",
	})
	target := targetWindows
	if runtime.GOOS != "windows" {
		target = targetLinux
	}
	common := []string{"-dir", dogs, "-work", work, "-mk", mk}
	var out bytes.Buffer
	if code := run(append([]string{"list"}, common...), &out, &out); code != 0 || !strings.Contains(out.String(), "hello: 824 lines; bins hello client") ||
		!strings.Contains(out.String(), "std.hash | hash.Crc32(data: []u8): u32") {
		t.Fatalf("list: exit %d\n%s", code, out.String())
	}
	runArgs := append([]string{"run", "-target", target, "-profile", "dev"}, common...)
	out.Reset()
	if code := run(runArgs, &out, &out); code != 1 {
		t.Fatalf("run with a wrong case: exit %d, want 1\n%s", code, out.String())
	}
	rep := readReport(t, filepath.Join(work, "dogfood-last.json"))
	got := map[string]string{}
	for _, c := range rep.Dogfoods[0].Runs[0].Cases {
		got[c.Name] = c.Status
		if c.Name == "wrong" && (len(c.Diffs) == 0 || !strings.Contains(c.Diffs[0], "stdout differs")) {
			t.Errorf("wrong: diffs %q", c.Diffs)
		}
		if c.Name == "sim" && c.Seeds != 2 {
			t.Errorf("sim: %d seeds, want 2", c.Seeds)
		}
	}
	want := map[string]string{"args": "pass", "fail": "pass", "sim": "pass", "bench": "pass", "wrong": "fail"}
	for n, s := range want {
		if got[n] != s {
			t.Errorf("case %s: %s, want %s\n%s", n, got[n], s, out.String())
		}
	}
	if len(rep.Bench) != 1 || rep.Bench[0].Dogfood != "hello" {
		t.Errorf("bench %+v", rep.Bench)
	}
	if err := os.Remove(filepath.Join(d, "cases", "wrong.out")); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if code := run(runArgs, &out, &out); code != 0 {
		t.Fatalf("run: exit %d\n%s", code, out.String())
	}
	out.Reset()
	if code := run(append(runArgs, "-changed"), &out, &out); code != 0 || !strings.Contains(out.String(), "unchanged since its last pass") {
		t.Fatalf("run -changed after a pass: exit %d\n%s", code, out.String())
	}
	write(t, d, map[string]string{"cases/args.in": "touched\n"})
	out.Reset()
	if code := run(append(runArgs, "-changed"), &out, &out); code != 0 || strings.Contains(out.String(), "unchanged since") {
		t.Fatalf("run -changed after a change: exit %d\n%s", code, out.String())
	}
	cov := filepath.Join(tmp, "COVERAGE.md")
	out.Reset()
	if code := run(append([]string{"cover", "-target", target, "-coverage", cov}, common...), &out, &out); code != 1 {
		t.Fatalf("cover: exit %d, want 1 (most lines are gaps)\n%s", code, out.String())
	}
	b, err := os.ReadFile(cov)
	if err != nil {
		t.Fatal(err)
	}
	// The references come from mk query uses, so a line the
	// program only reaches through std is not referenced.
	if !strings.Contains(string(b), "- Static references: mk query uses --json.\n") {
		t.Errorf("COVERAGE.md does not take its references from mk query uses:\n%s", b[:min(len(b), 1200)])
	}
	if strings.Contains(string(b), "references are unknown") {
		t.Error("COVERAGE.md has lines whose references are unknown")
	}
	for _, row := range []string{"| `str.Trim(self): str` | hello | ", "| `hash.Crc32(data: []u8): u32` | hello | "} {
		i := strings.Index(string(b), row)
		if i < 0 {
			t.Errorf("COVERAGE.md has no row %q", row)
			continue
		}
		if l := string(b[i:]); !strings.Contains(l[:strings.IndexByte(l, '\n')], "covered (executed)") {
			t.Errorf("row %q is not executed: %s", row, l[:strings.IndexByte(l, '\n')])
		}
	}
	// math.Max has a body only in client, which no case runs, and
	// math.Min is called only from a test block: neither is "compiled
	// inline".
	for _, line := range []string{"math.Max[T: Ord + Plain](a: T, b: T): T", "math.Min[T: Ord + Plain](a: T, b: T): T"} {
		if got := coverRow(t, string(b), line); got != "gap: referenced, not executed by any case" {
			t.Errorf("%s: %q", line, got)
		}
	}
}

func readReport(t *testing.T, path string) *Report {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var r Report
	if err := json.Unmarshal(b, &r); err != nil {
		t.Fatal(err)
	}
	if len(r.Dogfoods) != 1 || len(r.Dogfoods[0].Runs) != 1 {
		t.Fatalf("report: %+v", r)
	}
	return &r
}
