// Why: regression tests for std.proc bugs and doc facts that once went wrong.
// What: TestProc_DriveRelativeInDir, P3X8_ReplaceGivesNoSystemRoot,
// P3X9_DetachDocStack, P3X12_NotFoundDoc and P3X11_FilterMatchesNone.
// How: os/exec is the oracle for program paths and environments; the doc tests
// read the .mk files and check the doc lines against the facts they state.

package std

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// TestProc_DriveRelativeInDir: args[0] = <drive>:bin\p3x3.exe with
// Command.dir = "sub" starts sub\bin\p3x3.exe, as os/exec does (its
// joinExeDirAndFName makes dir absolute first), not bin\p3x3.exe of the
// current directory.
func TestProc_DriveRelativeInDir(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows only: a drive-relative path")
	}
	dir, helper, spawner := proc3Tools(t)
	b, err := os.ReadFile(helper)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{filepath.Join(dir, "bin"), filepath.Join(dir, "sub", "bin")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, "p3x3.exe"), b, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	prog := filepath.VolumeName(dir) + `bin\p3x3.exe`
	if len(prog) != len(`C:bin\p3x3.exe`) {
		t.Skipf("the temporary directory %q has no drive letter", dir)
	}
	cases := []proc3Case{{"sub", prog, "exe"}, {`.\sub`, prog, "exe"}}
	got := proc3RunSpawner(t, spawner, dir, "dir", cases)
	// os/exec, from the same working directory.
	old, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	var want strings.Builder
	for _, c := range cases {
		cmd := exec.Command(c[1], c[2:]...)
		cmd.Dir = c[0]
		var o bytes.Buffer
		cmd.Stdout = &o
		if err := cmd.Run(); err != nil {
			os.Chdir(old)
			t.Fatalf("os/exec %q in %q: %v", c[1], c[0], err)
		}
		want.WriteString("=== 0\n" + strings.ReplaceAll(o.String(), "\r\n", "\n"))
	}
	if err := os.Chdir(old); err != nil {
		t.Fatal(err)
	}
	inSub := strings.ToLower(filepath.Join(dir, "sub", "bin", "p3x3.exe"))
	if !strings.Contains(want.String(), inSub) {
		t.Fatalf("os/exec did not start the copy in dir (the finding's premise): %q", want.String())
	}
	proc3Diff(t, "drive-relative in dir", got, want.String(), cases)
}

// TestProc_ReplaceGivesNoSystemRoot pins the one way Env.Replace
// differs from os/exec on Windows: os/exec adds SYSTEMROOT to a replaced
// environment that lacks it (exec.Cmd's addCriticalEnv), and proc.Spawn
// gives exactly the entries named ("Exactly vars"). A change either way
// fails here and is a deliberate change of behaviour.
func TestProc_ReplaceGivesNoSystemRoot(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows only: os/exec adds SYSTEMROOT on Windows")
	}
	root := os.Getenv("SYSTEMROOT")
	if root == "" {
		t.Skip("this process has no SYSTEMROOT")
	}
	dir, helper, spawner := proc3Tools(t)
	cases := []proc3Case{{helper, "MK_P3_A=1"}, {helper}}
	got := proc3RunSpawner(t, spawner, dir, "rootenv", cases)
	if want := "=== 0\nSYSTEMROOT unset\n=== 0\nSYSTEMROOT unset\n"; got != want {
		t.Fatalf("proc.Spawn with Env.Replace: got %q, want %q", got, want)
	}
	for _, c := range cases {
		cmd := exec.Command(helper, "sysroot")
		cmd.Env = append([]string{}, c[1:]...)
		out, err := cmd.Output()
		if err != nil {
			t.Fatal(err)
		}
		if g := strings.TrimSpace(string(out)); !strings.EqualFold(g, "SYSTEMROOT="+root) {
			t.Fatalf("os/exec no longer adds SYSTEMROOT (the divergence this test pins): %q", g)
		}
	}
}

// TestProc_DetachDocStack: Child.Detach's doc states the reaper
// thread's stack as std.rt's ReaperStack sets it.
func TestProc_DetachDocStack(t *testing.T) {
	rt, err := os.ReadFile(filepath.FromSlash("rt/proc.mk"))
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`const ReaperStack: int = (\d+)`).FindSubmatch(rt)
	if m == nil {
		t.Fatal("std/rt/proc.mk has no ReaperStack")
	}
	n, _ := strconv.Atoi(string(m[1]))
	child, err := os.ReadFile(filepath.FromSlash("proc/child.mk"))
	if err != nil {
		t.Fatal(err)
	}
	doc := proc3DocOf(t, string(child), "export Child.Detach(")
	d := regexp.MustCompile(`a (\d+) (KiB|MiB)\s+(?:///\s+)?stack`).FindStringSubmatch(doc)
	if d == nil {
		t.Fatalf("Child.Detach's doc states no stack size:\n%s", doc)
	}
	k, _ := strconv.Atoi(d[1])
	unit := 1024
	if d[2] == "MiB" {
		unit = 1 << 20
	}
	if k*unit != n {
		t.Fatalf("Child.Detach's doc says %s %s, ReaperStack is %d bytes", d[1], d[2], n)
	}
}

// TestProc_NotFoundDoc: proc.NotFound's doc names the missing
// directory, which Spawn and Run answer NotFound for.
func TestProc_NotFoundDoc(t *testing.T) {
	b, err := os.ReadFile(filepath.FromSlash("proc/errors.mk"))
	if err != nil {
		t.Fatal(err)
	}
	doc := proc3DocOf(t, string(b), "export error NotFound")
	if !strings.Contains(strings.Join(strings.Fields(strings.ReplaceAll(doc, "///", "")), " "), "dir names no directory") {
		t.Fatalf("NotFound's doc does not name a missing dir:\n%s", doc)
	}
}

// TestProc_FilterMatchesNone: a block filter that matches no block
// is an error, so a run that tests nothing cannot pass.
func TestProc_FilterMatchesNone(t *testing.T) {
	_, blocks := proc3Rewrite(t)
	if _, err := proc3Select(blocks, "zz-none"); err == nil {
		t.Fatal("MK_PROC3_ONLY=zz-none was accepted")
	}
	if _, err := proc3Select(blocks, `""`); err == nil {
		t.Fatal(`MK_PROC3_ONLY="" (two quote marks) was accepted`)
	}
	idx, err := proc3Select(blocks, "")
	if err != nil || len(idx) != len(blocks) {
		t.Fatalf("the empty filter: %d of %d blocks, %v", len(idx), len(blocks), err)
	}
	one := "two tasks' WriteAll of 1 MiB each to one PipeWriter"
	if idx, err := proc3Select(blocks, one); err != nil || len(idx) != 1 {
		t.Fatalf("%q: %d blocks, %v", one, len(idx), err)
	}
}

// proc3DocOf is the run of /// lines just above the line of src that
// starts with decl.
func proc3DocOf(t *testing.T, src, decl string) string {
	t.Helper()
	lines := strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n")
	for i, l := range lines {
		if !strings.HasPrefix(l, decl) {
			continue
		}
		j := i
		for j > 0 && strings.HasPrefix(lines[j-1], "///") {
			j--
		}
		return strings.Join(lines[j:i], "\n")
	}
	t.Fatalf("no line starts with %q", decl)
	return ""
}
