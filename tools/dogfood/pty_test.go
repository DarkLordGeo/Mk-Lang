// Why: terminal cases and scratch directories cross processes, terminals
// and WSL, where cleanup and time limits fail quietly.
// What: TestPtyEndToEnd and the TestH8* tests of ptyrun's build,
// limits, signals and scratch cleanup, with the throwaway program ptyMain.
// How: builds ptyrun and a throwaway dogfood on the host; the Linux test
// starts wsl.exe only with MK_LINUX=1, under tools/wsl-lock.ps1.

package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

// TestPtyCaseResolves: a .pty file makes a terminal case, per target
// and profile like every case file, and a terminal case with .in, .err or
// .sim fails to resolve, since the program's streams are the terminal.
func TestPtyCaseResolves(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, map[string]string{
		"term.pty":              "expect 1\n",
		"term.x86_64-linux.pty": "expect 2\n",
		"term.args":             "$WORK/transcript\n",
		"term.out":              "ready\n",
		"plain.out":             "x\n",
		"bad.pty":               "expect 1\n",
		"bad.in":                "typed\n",
		"badsim.pty":            "expect 1\n",
		"badsim.sim":            "seed 1\n",
		"baderr.pty":            "expect 1\n",
		"baderr.release.err":    "x\n",
	})
	cases, err := loadCases(dir)
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]*Case{}
	for _, c := range cases {
		by[c.Name] = c
	}
	s, err := by["term"].resolve(targetWindows, "dev")
	if err != nil || filepath.Base(s.Pty) != "term.pty" {
		t.Fatalf("term on Windows: %+v, %v", s, err)
	}
	if s, err := by["term"].resolve(targetLinux, "dev"); err != nil || filepath.Base(s.Pty) != "term.x86_64-linux.pty" {
		t.Errorf("term on Linux: %+v, %v", s, err)
	}
	if s, err := by["plain"].resolve(targetWindows, "dev"); err != nil || s.Pty != "" {
		t.Errorf("plain: %+v, %v", s, err)
	}
	for _, n := range []string{"bad", "badsim"} {
		if _, err := by[n].resolve(targetWindows, "dev"); err == nil || !strings.Contains(err.Error(), "a terminal case (.pty)") {
			t.Errorf("%s: %v, want a refusal", n, err)
		}
	}
	if _, err := by["baderr"].resolve(targetWindows, "dev"); err != nil {
		t.Errorf("baderr in dev, whose .err is release only: %v", err)
	}
	if _, err := by["baderr"].resolve(targetWindows, "release"); err == nil {
		t.Error("baderr in release: no refusal")
	}
}

// TestPtyArgv: ptyrun's command line, its limit the case's time less
// the margin, at least a second.
func TestPtyArgv(t *testing.T) {
	got := ptyArgv("pr", 30*time.Second, "w.pty-raw", "w/transcript", "c/x.pty", []string{"prog", "a b"})
	want := []string{"pr", "--limit", "27000", "--raw", "w.pty-raw", "--transcript", "w/transcript", "--script", "c/x.pty", "--", "prog", "a b"}
	if !slices.Equal(got, want) {
		t.Errorf("ptyArgv = %q\nwant %q", got, want)
	}
	if got := ptyArgv("pr", time.Second, "r", "t", "s", []string{"p"}); got[2] != "1000" {
		t.Errorf("a second left: --limit %s, want 1000", got[2])
	}
}

// TestPtyResult: ptyrun's status line becomes the case's exit code,
// the transcript its stdout, and an error line or any other output a
// failure that names ptyrun.
func TestPtyResult(t *testing.T) {
	work := t.TempDir()
	write(t, work, map[string]string{transcriptName: "ready\ngot:x\n"})
	r := ptyResult(result{stdout: []byte("exit 3\n")}, work)
	if r.err != "" || r.code != 3 || string(r.stdout) != "ready\ngot:x\n" {
		t.Errorf("exit 3: %+v", r)
	}
	if r := ptyResult(result{stdout: []byte("signal Terminate\n")}, work); r.err != "" || r.code != 143 {
		t.Errorf("signal Terminate: %+v", r)
	}
	if r := ptyResult(result{stdout: []byte("error script line 2: expect: the time ran out; the transcript has 1 lines\n"), code: 2}, work); !strings.HasPrefix(r.err, "ptyrun: script line 2") {
		t.Errorf("error line: %+v", r)
	}
	for _, out := range []string{"", "exit x\n", "signal Kill\n", "hello\n"} {
		if r := ptyResult(result{stdout: []byte(out)}, work); r.err == "" {
			t.Errorf("%q: no error", out)
		}
	}
	if r := ptyResult(result{stdout: []byte("exit 0\n"), code: 2}, work); r.err == "" {
		t.Error("exit 0 printed while ptyrun exited 2: no error")
	}
	if r := ptyResult(result{timedOut: true}, work); !r.timedOut {
		t.Error("a timeout is not kept")
	}
	empty := t.TempDir()
	if r := ptyResult(result{stdout: []byte("exit 0\n")}, empty); r.err != "" || len(r.stdout) != 0 {
		t.Errorf("no transcript: %+v", r)
	}
}

// TestTmpPlaceholder: {tmp} in .args or .env marks the case, expands
// to the run's scratch directory, and is written back in outputs; a mode
// test case may not name it.
func TestTmpPlaceholder(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, map[string]string{
		"a.args":    "--admin\n{tmp}/admin.sock\n",
		"b.env":     "STATE={tmp}\n",
		"c.args":    "plain\n",
		"d.sim":     "mode test\nseed 1\n",
		"d.env":     "X={tmp}\n",
		"e.pty":     "expect 1\n",
		"e.args":    "{tmp}\n",
		"e.targets": "x86_64-linux\n",
	})
	cases, err := loadCases(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"a": true, "b": true, "c": false, "e": true}
	for _, c := range cases {
		s, err := c.resolve(targetLinux, "dev")
		if c.Name == "d" {
			if err == nil || !strings.Contains(err.Error(), "{tmp}") {
				t.Errorf("d: %v, want a refusal of {tmp} in mode test", err)
			}
			continue
		}
		if err != nil || s.UsesTmp != want[c.Name] {
			t.Errorf("%s: UsesTmp %v (%v), want %v", c.Name, s.UsesTmp, err, want[c.Name])
		}
	}
	x := Subst{Cases: "/mnt/v/c", Work: "/mnt/v/w", Tmp: "/tmp/mk-run-1/kvd.dev.admin"}
	if got := x.expand("{tmp}/admin.sock"); got != "/tmp/mk-run-1/kvd.dev.admin/admin.sock" {
		t.Errorf("expand: %q", got)
	}
	if got := x.normalize([]byte("listening on /tmp/mk-run-1/kvd.dev.admin/admin.sock\n")); string(got) != "listening on {tmp}/admin.sock\n" {
		t.Errorf("normalize: %q", got)
	}
	if got := (Subst{}).expand("{tmp}"); got != "{tmp}" {
		t.Errorf("no scratch directory: %q, want {tmp} kept", got)
	}
}

// TestLinuxJobScratch: a Linux job whose case names {tmp} gets
// /tmp/mk-<run>/<dogfood>.<profile>.<case>[.<seed>] as its scratch
// directory, in its arguments and as Job.Tmp, while its working directory
// stays the Windows one; a terminal case's job runs ptyrun with the
// Linux paths of its files.
func TestLinuxJobScratch(t *testing.T) {
	h := newHarness(&bytes.Buffer{})
	h.scratch = `V:\w\dogfood\runs\run-42`
	h.ptyrun = map[string]string{targetLinux: `V:\w\dogfood\runs\run-42\bin\ptyrun\x86_64-linux\ptyrun`}
	d := &Dogfood{Name: "kvd", Bins: []string{"kvd"}}
	rr := &RunReport{Target: targetLinux, Profile: "release", exes: map[string]string{"kvd": `V:\w\bin\kvd`}}
	c := &Case{Name: "admin", Dir: `D:\src\mk\Dogfoods\kvd\cases`}
	s := &Spec{Args: []string{"--admin", "{tmp}/a.sock", "$WORK/db"}, UsesTmp: true}
	work := `V:\w\work\kvd\x86_64-linux-release\admin`
	j, x, ok := h.linuxJob(d, s, rr, c, work, "7", &CaseReport{})
	if !ok {
		t.Fatal("linuxJob failed")
	}
	tmp := "/tmp/mk-run-42/kvd.release.admin.7"
	if j.Tmp != tmp || x.Tmp != tmp || j.Dir != work {
		t.Errorf("Tmp %q, Subst.Tmp %q, Dir %q", j.Tmp, x.Tmp, j.Dir)
	}
	want := []string{"wsl.exe", "--cd", work, "-e", "/mnt/v/w/bin/kvd", "--admin", tmp + "/a.sock", "/mnt/v/w/work/kvd/x86_64-linux-release/admin/db", "--seed", "7"}
	if !slices.Equal(j.Argv, want) {
		t.Errorf("argv %q\nwant %q", j.Argv, want)
	}
	s = &Spec{Args: []string{"x"}, Pty: `D:\src\mk\Dogfoods\kvd\cases\admin.pty`}
	j, _, ok = h.linuxJob(d, s, rr, c, work, "", &CaseReport{})
	if !ok {
		t.Fatal("linuxJob of a terminal case failed")
	}
	if j.Tmp != "" || !slices.Equal(j.Argv[:5], []string{"wsl.exe", "--cd", work, "-e", "/mnt/v/w/dogfood/runs/run-42/bin/ptyrun/x86_64-linux/ptyrun"}) ||
		!slices.Contains(j.Argv, "/mnt/d/src/mk/Dogfoods/kvd/cases/admin.pty") || !slices.Contains(j.Argv, "/mnt/v/w/work/kvd/x86_64-linux-release/admin/transcript") ||
		!slices.Equal(j.Argv[len(j.Argv)-3:], []string{"--", "/mnt/v/w/bin/kvd", "x"}) {
		t.Errorf("terminal case argv %q", j.Argv)
	}
}

// TestWrapTmp: the scratch shell goes between -e and the program
// that boundInVM bounded, so `timeout` ends the program and the shell
// still removes the directory; the sweep names only a run's root.
func TestWrapTmp(t *testing.T) {
	argv, _ := boundInVM([]string{`C:\wsl.exe`, "--cd", "w", "-e", "/mnt/v/p", "a"}, 20)
	got := wrapTmp(argv, "/tmp/mk-run-1/x.dev.c")
	want := []string{`C:\wsl.exe`, "--cd", "w", "-e", "sh", "-c", tmpScript, "sh", "/tmp/mk-run-1/x.dev.c", "timeout", "-k", "5", "20", "/mnt/v/p", "a"}
	if !slices.Equal(got, want) {
		t.Errorf("wrapTmp = %q\nwant %q", got, want)
	}
	if got := wrapTmp([]string{"mk", "test"}, "/tmp/mk-run-1/x"); len(got) != 2 {
		t.Errorf("no -e: %q", got)
	}
	for _, s := range []string{`case $d in /tmp/mk-run-*/?*)`, `MK_LINUX_TMP=$d "$@"`, `rm -rf -- "$d"`, "exit $c"} {
		if !strings.Contains(tmpScript, s) {
			t.Errorf("tmpScript lacks %q", s)
		}
	}
	if got := sweepArgv("wsl", "/tmp/mk-run-1"); got[len(got)-1] != "/tmp/mk-run-1" || !strings.Contains(got[4], "/tmp/mk-run-*)") {
		t.Errorf("sweepArgv = %q", got)
	}
}

// TestPtyEndToEnd runs a throwaway dogfood's terminal cases on the
// host through ptyrun (built from tools/dogfood/ptyrun), and a case with
// a {tmp} scratch directory: the transcript and the exit code are judged,
// a script that waits for a line that never comes fails naming ptyrun,
// and the scratch directory is gone afterwards. A program that exits
// before an expect is met fails the case at once, naming its exit; a
// resize and a paste reach the program, and a program still running when
// ptyrun's limit passes after the script is killed.
func TestPtyEndToEnd(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("the host's terminal cases run here on Windows; Linux's run through the WSL lock (tools/wsl-lock.ps1)")
	}
	tmp := t.TempDir()
	mk := buildMk(t, tmp)
	dogs := filepath.Join(tmp, "Dogfoods")
	work := filepath.Join(tmp, "work")
	d := filepath.Join(dogs, "term")
	write(t, d, map[string]string{
		"pkg.mk":                strings.ReplaceAll(e2ePkg, "hello", "term"),
		"src/main.mk":           ptyMain,
		"src/pad.mk":            e2ePad(),
		"client/main.mk":        e2eClient,
		"README.md":             "# term\n",
		"cases/tty.args":        "tty\n$WORK/transcript\n",
		"cases/tty.pty":         "# wait for the program, then type two lines\nexpect 1\nkeys hello\\r\nexpect 2\nresize 100 30\nkey q\n",
		"cases/tty.out":         "ready\ngot:hello\ngot:quit\n",
		"cases/tty.code":        "3\n",
		"cases/stuck.args":      "tty\n$WORK/transcript\n",
		"cases/stuck.pty":       "size 40 10\nexpect 1\nkeys hello\\r\nexpect 5\n",
		"cases/stuck.out":       "ready\ngot:hello\n",
		"cases/scratch.args":    "tmp\n{tmp}\n",
		"cases/scratch.out":     "wrote {tmp}/f.txt\n",
		"cases/scratch.targets": "x86_64-windows x86_64-linux\n",
		"cases/early.args":      "exit7\n",
		"cases/early.pty":       "expect 1\n",
		"cases/early.code":      "7\n",
		"cases/raw.args":        "term\n$WORK/transcript\n",
		"cases/raw.pty":         "expect 1\nkeys hello\\r\nexpect 2\npaste xyz\nexpect 3\nresize 100 30\nexpect 4\nkeys quit\\r\n",
		"cases/raw.out":         "ready\ngot:hello\npaste:xyz\nsize 100 30\ngot:quit\n",
		"cases/raw.code":        "3\n",
	})
	// `key q` is no key name: the first run fails to parse the script.
	common := []string{"-dir", dogs, "-work", work, "-mk", mk}
	runArgs := append([]string{"run", "-target", targetWindows, "-profile", "dev"}, common...)
	var out bytes.Buffer
	if code := run(runArgs, &out, &out); code != 1 {
		t.Fatalf("run: exit %d, want 1\n%s", code, out.String())
	}
	rep := readReport(t, filepath.Join(work, "dogfood-last.json"))
	got := map[string]*CaseReport{}
	for _, c := range rep.Dogfoods[0].Runs[0].Cases {
		got[c.Name] = c
	}
	if c := got["tty"]; c == nil || c.Status != "fail" || len(c.Diffs) == 0 || !strings.Contains(c.Diffs[0], "ptyrun: the script") {
		t.Errorf("tty with a bad key: %+v", c)
	}
	if c := got["stuck"]; c == nil || c.Status != "fail" || len(c.Diffs) == 0 || !strings.Contains(c.Diffs[0], "ptyrun: script line 4: expect") {
		t.Errorf("stuck: %+v", c)
	}
	if c := got["scratch"]; c == nil || c.Status != "pass" {
		t.Errorf("scratch: %+v\n%s", c, out.String())
	}
	// Not "the time ran out" after the limit, but the program's exit.
	if c := got["early"]; c == nil || c.Status != "fail" || len(c.Diffs) == 0 || !strings.Contains(c.Diffs[0], "ptyrun: script line 1: expect: the program ended first (exit 7)") {
		t.Errorf("early: %+v", c)
	}
	if c := got["raw"]; c == nil || c.Status != "pass" {
		t.Errorf("raw (keys, a paste and a resize in raw mode): %+v", c)
	}
	// The failed run keeps its scratch; the case's working directory
	// is there, and its {tmp} directory beside it is gone.
	runs, _ := filepath.Glob(filepath.Join(work, "dogfood", "runs", "run-*"))
	if len(runs) != 1 {
		t.Fatalf("the failed run's scratch: %q, want one", runs)
	}
	caseDir := filepath.Join(runs[0], "work", "term", targetWindows+"-dev", "scratch")
	if _, err := os.Stat(caseDir); err != nil {
		t.Errorf("the scratch case's working directory: %v", err)
	}
	if _, err := os.Stat(caseDir + ".tmp"); !os.IsNotExist(err) {
		t.Errorf("the scratch case's {tmp} directory is still there (%v)", err)
	}
	// ptyrun alone, with a program that never exits after the
	// script: killed when --limit passes.
	ptyrun := filepath.Join(runs[0], "bin", "ptyrun", targetWindows, "ptyrun.exe")
	prog := filepath.Join(runs[0], "bin", "term", targetWindows+"-dev", "term.exe")
	dir := t.TempDir()
	write(t, dir, map[string]string{"s.pty": "expect 1\n"})
	tr := filepath.Join(dir, "transcript")
	r := runProgram([]string{ptyrun, "--limit", "1500", "--transcript", tr, "--script", filepath.Join(dir, "s.pty"), "--", prog, "tty", tr}, dir, os.Environ(), "", time.Minute)
	if got := strings.TrimSpace(string(r.stdout)); r.code != 2 || got != "error the program did not exit within --limit; it was killed" {
		t.Errorf("a program that outlives --limit after the script: exit %d, %q (%s)", r.code, got, r.err)
	}
	// ptyrun removes a transcript left by an
	// earlier run before it starts the program. A stale three-line file
	// would meet `expect 3` at once; the program here exits 7 and writes
	// nothing, so the expect must fail naming that exit, and the stale
	// file must be gone.
	write(t, dir, map[string]string{"stale.pty": "expect 3\n", "transcript": "old\nold\nold\n"})
	r = runProgram([]string{ptyrun, "--limit", "10000", "--transcript", tr, "--script", filepath.Join(dir, "stale.pty"), "--", prog, "exit7"}, dir, os.Environ(), "", time.Minute)
	if got := strings.TrimSpace(string(r.stdout)); r.code != 2 || !strings.HasPrefix(got, "error script line 1: expect: the program ended first (exit 7)") {
		t.Errorf("a stale transcript of three lines: exit %d, %q (%s); want the expect to wait for this run's lines", r.code, got, r.err)
	}
	if _, err := os.Stat(tr); !os.IsNotExist(err) {
		t.Errorf("the stale transcript is still there (%v)", err)
	}
	write(t, d, map[string]string{"cases/tty.pty": "expect 1\nkeys hello\\r\nexpect 2\nresize 100 30\nkeys quit\nkey enter\n"})
	if err := os.Remove(filepath.Join(d, "cases", "stuck.pty")); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"stuck.args", "stuck.out", "early.args", "early.pty", "early.code"} {
		os.Remove(filepath.Join(d, "cases", f))
	}
	out.Reset()
	if code := run(runArgs, &out, &out); code != 0 {
		t.Fatalf("run: exit %d\n%s", code, out.String())
	}
	rep = readReport(t, filepath.Join(work, "dogfood-last.json"))
	for _, c := range rep.Dogfoods[0].Runs[0].Cases {
		if c.Status != "pass" {
			t.Errorf("%s: %s %q", c.Name, c.Status, c.Diffs)
		}
		if c.Name == "tty" && !strings.Contains(c.Command, "ptyrun") {
			t.Errorf("tty ran %q, not through ptyrun", c.Command)
		}
	}
}

// ptyMain is the throwaway terminal program: `tty FILE` writes "ready",
// then "got:LINE" for each line typed (ended by CR or LF), to FILE, and
// exits 3 after "quit"; `term FILE` does the same in raw mode through
// std.term, and also writes "paste:TEXT" for a bracketed paste and "size
// C R" for each new size (so a resize or a paste that does nothing
// shows); `exit7` exits 7 at once; `tmp DIR` writes DIR/f.txt and
// says so.
const ptyMain = `import std.fs
import std.proc
import std.term
import std.time

export Main(sys: &System): (fs.Error | ReadError | WriteError)!int {
    io := sys.io
    if sys.args.Len() == 2 && sys.args[1] == "exit7" {
        proc.Exit(7)
    }
    if sys.args.Len() == 3 && sys.args[1] == "term" {
        return TermMain(sys, sys.args[2])
    }
    if sys.args.Len() == 3 && sys.args[1] == "tmp" {
        var p := Text.New(sys.arena)
        p.Push(sys.args[2]).Must()
        p.Push("/f.txt").Must()
        name := p.Str()
        try fs.WriteAll(io, name, "x".AsBytes())
        try sys.Print("wrote {name}\n", (name))
        return 0
    }
    if sys.args.Len() != 3 || sys.args[1] != "tty" {
        proc.Exit(2)
    }
    var t := try fs.Create(io, sys.args[2])
    onerr t.Abandon()
    try t.WriteAll(io, "ready\n".AsBytes())
    var input := sys.Stdin()
    var b: [256]u8 = [0; 256]
    var n := 0
    var code := 0
    for {
        k := try input.Read(mut b[n..n + 1])
        if k == 0 {
            break
        }
        if b[n] != 10 && b[n] != 13 {
            n += 1
            continue
        }
        if n == 0 {
            continue
        }
        try t.WriteAll(io, "got:".AsBytes())
        try t.WriteAll(io, b[0..n])
        try t.WriteAll(io, "\n".AsBytes())
        if n == 4 && b[0] == b'q' {
            code = 3
            break
        }
        n = 0
    }
    try t.Close(io)
    return code
}

// TermMain is the term mode: raw keys, pastes and resizes, each a line.
TermMain(sys: &System, path: str): int {
    io := sys.io
    var t := term.Open(io) else {
        proc.Exit(70)
    }
    t.Raw(io) else {
        proc.Exit(71)
    }
    // What the terminal queued at start (a pseudo-console's first size) is
    // not the script's.
    for {
        var quiet := false
        scope s := io.Scope() {
            s.CancelAfter(time.Millis(200))
            _ = t.Read(sys.arena, io) else {
                quiet = true
            }
        }
        if quiet {
            break
        }
    }
    var buf: [256]u8 = [0; 256]
    var w := t.Writer(io, mut buf[..])
    term.BracketedPaste(mut w, true) else {
        proc.Exit(72)
    }
    w.Flush() else {
        proc.Exit(72)
    }
    // Every failure from here exits: the terminal is open.
    var f := fs.Create(io, path) else {
        proc.Exit(74)
    }
    f.WriteAll(io, "ready\n".AsBytes()) else {
        proc.Exit(74)
    }
    var line := Text.New(sys.arena)
    var at_cols := 80
    var at_rows := 24
    var code := 0
    for {
        e := t.Read(sys.arena, io) else {
            proc.Exit(73)
        }
        var out := ""
        match e {
            .Key(key, ch) {
                match key {
                    .Char { line.PushChar(ch).Must() }
                    .Enter {
                        out = sys.arena.Fmt("got:{l}\n", (l: line.Str())).Must()
                        if line.Str() == "quit" {
                            code = 3
                        }
                        line = Text.New(sys.arena)
                    }
                    _ {}
                }
            }
            .Paste(text) { out = sys.arena.Fmt("paste:{text}\n", (text)).Must() }
            .Resize(cols, rows) {
                if cols != at_cols || rows != at_rows {
                    at_cols = cols
                    at_rows = rows
                    out = sys.arena.Fmt("size {cols} {rows}\n", (cols, rows)).Must()
                }
            }
            _ {}
        }
        f.WriteAll(io, out.AsBytes()) else {
            proc.Exit(74)
        }
        if code != 0 {
            break
        }
    }
    f.Close(io) else {
        proc.Exit(74)
    }
    t.Close(io)
    return code
}
`

// TestPtyrunInDigest: a dogfood with a terminal case is selected by
// -changed when ptyrun's sources change, and ptyrun's std imports are
// read from its sources.
func TestPtyrunInDigest(t *testing.T) {
	root := t.TempDir()
	write(t, root, map[string]string{
		"tools/dogfood/ptyrun/src/main.mk": "import std.fs\nimport std.proc\n",
		"d/cases/a.pty":                    "expect 1\n",
		"e/cases/a.out":                    "x\n",
	})
	h := newHarness(&bytes.Buffer{})
	h.root = root
	if got := ptyrunImports(h.ptyrunDir()); !slices.Equal(got, []string{"std.fs", "std.proc"}) {
		t.Errorf("ptyrunImports = %q", got)
	}
	if !hasPtyCase(filepath.Join(root, "d")) || hasPtyCase(filepath.Join(root, "e")) {
		t.Error("hasPtyCase")
	}
	d := &Dogfood{Name: "d", Dir: filepath.Join(root, "d")}
	e := &Dogfood{Name: "e", Dir: filepath.Join(root, "e")}
	d0, e0 := h.digest(d), h.digest(e)
	write(t, root, map[string]string{"tools/dogfood/ptyrun/src/main.mk": "import std.fs\nimport std.proc\n// changed\n"})
	h.digests = map[string]string{}
	if h.digest(d) == d0 {
		t.Error("a change to ptyrun leaves a terminal dogfood's digest unchanged")
	}
	if h.digest(e) != e0 {
		t.Error("a change to ptyrun changes the digest of a dogfood with no terminal case")
	}
}

// TestPtyrunBuiltWithTheDogfood: ptyrun is built in the
// build phase, with the dogfood's own executables, so its build (2 s here,
// by the fake mk) is charged to no case's time and not to the suite's
// budget, as on Linux. Only the load-proof direction is timed.
func TestPtyrunBuiltWithTheDogfood(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "x")
	files := dogfoodFiles("x")
	files["cases/term.pty"] = "expect 1\n"
	files["cases/term.code"] = "0\n"
	write(t, dir, files)
	fakeEnv(t, "DFH_PTYRUN_BUILD", "2s")
	d, _ := loadDogfood(dir, nil)
	cases, err := loadCases(filepath.Join(dir, "cases"))
	if err != nil {
		t.Fatal(err)
	}
	h := testHarness(t, t.TempDir())
	rr := &RunReport{Target: hostTarget(), Profile: "dev"}
	start := time.Now()
	if !h.buildRun(d, rr, false) {
		t.Fatalf("buildRun: %s", rr.Reason)
	}
	if took := time.Since(start); took < 2*time.Second || h.ptyrun[rr.Target] == "" || strings.HasPrefix(h.ptyrun[rr.Target], "!") {
		t.Fatalf("the build phase took %v and left ptyrun %q: ptyrun was not built there", took, h.ptyrun[rr.Target])
	}
	built := h.ptyrun[rr.Target]
	h.runCases(d, cases, rr)
	if c := rr.Cases[0]; c.Status != "pass" || !strings.Contains(c.Command, built) {
		t.Errorf("the terminal case: %s %q, ran %q, want ptyrun %s", c.Status, c.Diffs, c.Command, built)
	}
	// A dogfood with no terminal case builds no ptyrun.
	h2 := testHarness(t, t.TempDir())
	plain := filepath.Join(t.TempDir(), "y")
	write(t, plain, dogfoodFiles("y"))
	d2, _ := loadDogfood(plain, nil)
	if !h2.buildRun(d2, &RunReport{Target: hostTarget(), Profile: "dev"}, false) || len(h2.ptyrun) != 0 {
		t.Errorf("a dogfood with no .pty case built ptyrun: %v", h2.ptyrun)
	}
}

// TestLinuxPtyLimitFollowsTheBudget: on Linux the
// exec-jobs child sets a terminal case's ptyrun --limit from the time the
// job has left, less the margin, as the host does, so `timeout` never ends
// ptyrun before its own limit; a program argument spelled --limit is left
// alone.
func TestLinuxPtyLimitFollowsTheBudget(t *testing.T) {
	h := newHarness(&bytes.Buffer{})
	h.scratch = `V:\w\dogfood\runs\run-42`
	h.ptyrun = map[string]string{targetLinux: `V:\w\dogfood\runs\run-42\bin\ptyrun\x86_64-linux\ptyrun`}
	d := &Dogfood{Name: "kvd", Bins: []string{"kvd"}}
	rr := &RunReport{Target: targetLinux, Profile: "dev", exes: map[string]string{"kvd": `V:\w\bin\kvd`}}
	c := &Case{Name: "term", Dir: `D:\src\mk\Dogfoods\kvd\cases`}
	s := &Spec{Args: []string{"--limit", "5"}, Pty: `D:\src\mk\Dogfoods\kvd\cases\term.pty`}
	j, _, ok := h.linuxJob(d, s, rr, c, `V:\w\work\kvd\term`, "", &CaseReport{})
	if !ok || j.PtyLimit == 0 || j.Argv[j.PtyLimit-1] != "--limit" {
		t.Fatalf("linuxJob: ok %v, PtyLimit %d in %q", ok, j.PtyLimit, j.Argv)
	}
	for _, tc := range []struct {
		left float64
		want string
	}{{10, "7000"}, {30, "27000"}, {2, "1000"}} {
		argv, vm := jobArgv(j, `C:\wsl.exe`, tc.left)
		i := slices.Index(argv, "--limit")
		if i < 0 || argv[i+1] != tc.want || vm != max(1, int(tc.left)) {
			t.Errorf("%v s left: %q (VM limit %d), want ptyrun --limit %s", tc.left, argv, vm, tc.want)
		}
		if argv[0] != `C:\wsl.exe` || !slices.Equal(argv[len(argv)-2:], []string{"--limit", "5"}) {
			t.Errorf("%v s left: %q, want wsl.exe's path and the program's own --limit 5", tc.left, argv)
		}
	}
	s.Pty = ""
	j, _, _ = h.linuxJob(d, s, rr, c, `V:\w\work\kvd\term`, "", &CaseReport{})
	if argv, _ := jobArgv(j, `C:\wsl.exe`, 10); j.PtyLimit != 0 || !slices.Equal(argv[len(argv)-2:], []string{"--limit", "5"}) {
		t.Errorf("a plain case: PtyLimit %d, argv %q", j.PtyLimit, argv)
	}
}

// TestUnnamedSignalFailsNamingPtyrun: proc.Status
// names Hangup, Interrupt and Terminate, which count as 128 plus their
// numbers; a Linux program ended by any other signal fails the case
// naming ptyrun and what happened, never with a made-up code.
func TestUnnamedSignalFailsNamingPtyrun(t *testing.T) {
	work := t.TempDir()
	for name, code := range map[string]int{"Hangup": 129, "Interrupt": 130, "Terminate": 143} {
		if r := ptyResult(result{stdout: []byte("signal " + name + "\n")}, work); r.err != "" || r.code != code {
			t.Errorf("signal %s: %+v, want code %d", name, r, code)
		}
	}
	line := "error the program was ended by a signal other than Hangup, Interrupt or Terminate, which proc.Status does not name\n"
	if r := ptyResult(result{stdout: []byte(line), code: 2}, work); !strings.HasPrefix(r.err, "ptyrun: the program was ended by a signal other than Hangup, Interrupt or Terminate") {
		t.Errorf("an unnamed signal: %+v", r)
	}
	src, err := os.ReadFile(filepath.Join("ptyrun", "src", "main.mk"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(src), strings.TrimPrefix(strings.TrimSuffix(line, "\n"), "error ")) {
		t.Error("ptyrun's main.mk no longer prints the unnamed-signal line this test reads")
	}
}

// TestLinuxScratchRemovedAfterTimeout: a Linux
// job whose program outlives its time is ended by `timeout`, and its
// scratch shell still removes the job's directory, leaving a sibling the
// program made under the run's root; then a batch through runJobs ends
// with the sweep, which removes the root and the sibling. Starts wsl.exe:
// MK_LINUX=1 only, under tools/wsl-lock.ps1 with go test -p 1.
func TestLinuxScratchRemovedAfterTimeout(t *testing.T) {
	if os.Getenv("MK_LINUX") != "1" {
		t.Skip("MK_LINUX=1 not set: this leaf starts wsl.exe (under tools/wsl-lock.ps1, go test -p 1)")
	}
	if runtime.GOOS != "windows" {
		t.Skip("Linux jobs run through wsl.exe from Windows")
	}
	if os.Getenv("MK_WSL_LOCKED") != "1" {
		t.Fatal("MK_LINUX=1 without the WSL lock: run under tools/wsl-lock.ps1")
	}
	if msg := probeWSL(); msg != "" {
		t.Fatalf("not verified: WSL did not answer: %s", msg)
	}
	wsl, err := exec.LookPath("wsl.exe")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	root := fmt.Sprintf("/tmp/mk-run-h87%d", time.Now().UnixNano())
	defer runProgram([]string{wsl, "-e", "rm", "-rf", "--", root}, dir, os.Environ(), "", probeLimit)
	look := func(p string) string {
		r := runProgram([]string{wsl, "-e", "sh", "-c", `if test -e "$1"; then echo there; else echo gone; fi`, "sh", p}, dir, os.Environ(), "", probeLimit)
		return strings.TrimSpace(string(r.stdout))
	}
	// The scratch shell alone: the job's own argv, no sweep.
	prog := `mkdir -p "$MK_LINUX_TMP/../sibling" && touch "$MK_LINUX_TMP/f" && echo made && exec sleep 60`
	j := Job{Argv: []string{"wsl.exe", "--cd", dir, "-e", "sh", "-c", prog}, Dir: dir, Tmp: root + "/h87.dev.c"}
	argv, vm := jobArgv(j, wsl, 2)
	r := runProgram(argv, dir, os.Environ(), "", time.Duration(vm)*time.Second+vmGrace)
	if !strings.Contains(string(r.stdout), "made") || r.code != 124 {
		t.Errorf("the job: code %d, stdout %q, stderr %q, err %q; want made, then timeout's 124", r.code, r.stdout, r.stderr, r.err)
	}
	if got := look(j.Tmp); got != "gone" {
		t.Errorf("after the timeout, the job's scratch directory %s: %q, want gone (the shell removes it)", j.Tmp, got)
	}
	if got := look(root + "/sibling"); got != "there" {
		t.Fatalf("the sibling under %s: %q, want there before the sweep", root, got)
	}
	// A batch through runJobs ends with the sweep of the root it used.
	j2 := Job{Argv: []string{"wsl.exe", "--cd", dir, "-e", "true"}, Dir: dir, Case: "c", CaseSeconds: 5, Suite: "s", SuiteSeconds: 60, Tmp: root + "/h87.dev.d"}
	res := runJobs([]Job{j2}, func() string { return "" })
	if len(res.Results) != 1 || res.Results[0].Code != 0 || res.Results[0].Err != "" {
		t.Errorf("the batch: %+v", res)
	}
	if got := look(root); got != "gone" {
		t.Errorf("after the batch's sweep, %s: %q, want gone", root, got)
	}
}
