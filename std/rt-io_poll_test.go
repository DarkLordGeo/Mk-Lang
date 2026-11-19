// Why: std.sys's poller and sockets must behave the same on IOCP, io_uring
// and epoll before any scheduler runs on top of them.
// What: TestRt_PollerWindows, TestRt_PollerLinux, buildRTIOProbe and the
// WSL helpers (needWSL, runWSL, wslPath) the other rt-io tests share.
// How: a probe module in a copy of std drives PollSubmit, PollCancel and
// PollWait from Main with no other task; on Linux it runs once with io_uring
// and once with MK_IO=epoll, and both must agree on every case.
//
// The Linux leaves start wsl.exe: run them only under the WSL lock,
// `pwsh -NoProfile -File tools/wsl-lock.ps1 go test -p 1 ./std`; parallel
// WSL clients hang WSL. They never run in parallel with each other.

package std

import (
	"bytes"
	"context"
	"debug/pe"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// rtioMain runs every case once; its arguments are the stress seeds, the
// rounds per seed and the ping-pong round trips.
const rtioMain = `import std.rtioprobe

export Main(sys: &System): WriteError|ParseError!void {
    seeds := try sys.args[1].ParseInt()
    rounds := try sys.args[2].ParseInt()
    pings := try sys.args[3].ParseInt()
    leaks := try sys.args[4].ParseInt()
    var out := sys.Stdout()
    b := rtioprobe.Backend()
    try Print(mut out, "backend {b}\n", (b))
    // First of the threaded cases: the semaphore holds no spare count yet.
    pg := rtioprobe.ParkGap(10)
    try Print(mut out, "parkgap {pg}\n", (pg))
    // The review findings' regressions (std/rt-io_probe3_test.go).
    ho := rtioprobe.Handoff()
    try Print(mut out, "handoff {ho}\n", (ho))
    sh := rtioprobe.SemHuge()
    try Print(mut out, "semhuge {sh}\n", (sh))
    hw := rtioprobe.HugeWait()
    try Print(mut out, "hugewait {hw}\n", (hw))
    ws := rtioprobe.WakeSteal(300)
    try Print(mut out, "wakesteal {ws}\n", (ws))
    dr := rtioprobe.DoneRace(Trunc[int](rounds / 6))
    try Print(mut out, "donerace {dr}\n", (dr))
    se := rtioprobe.SockEpipe()
    try Print(mut out, "sockepipe {se}\n", (se))
    ks := rtioprobe.KindSock()
    try Print(mut out, "kindsock {ks}\n", (ks))
    pr := rtioprobe.PosRead()
    try Print(mut out, "posread {pr}\n", (pr))
    i6 := rtioprobe.Inet6()
    try Print(mut out, "inet6 {i6}\n", (i6))
    sr := rtioprobe.SigRace()
    try Print(mut out, "sigrace {sr}\n", (sr))
    it := rtioprobe.Interrupt()
    try Print(mut out, "interrupt {it}\n", (it))
    rr := rtioprobe.ResubRace(40)
    try Print(mut out, "resubrace {rr}\n", (rr))
    gs := rtioprobe.GenStale()
    try Print(mut out, "genstale {gs}\n", (gs))
    hd := rtioprobe.HrDirty()
    try Print(mut out, "hrdirty {hd}\n", (hd))
    rs := rtioprobe.Resolve()
    try Print(mut out, "resolve {rs}\n", (rs))
    pc := rtioprobe.PosCost(20000)
    try Print(mut out, "poscost {pc}\n", (pc))
    e := rtioprobe.Echo()
    try Print(mut out, "echo {e}\n", (e))
    c := rtioprobe.CancelRecv()
    try Print(mut out, "cancelrecv {c}\n", (c))
    ca := rtioprobe.CancelAccept()
    try Print(mut out, "cancelaccept {ca}\n", (ca))
    w := rtioprobe.Wake()
    try Print(mut out, "wake {w}\n", (w))
    a := rtioprobe.AddrInUse()
    try Print(mut out, "addrinuse {a}\n", (a))
    m := rtioprobe.Many()
    try Print(mut out, "many {m}\n", (m))
    f := rtioprobe.File()
    try Print(mut out, "file {f}\n", (f))
    r := rtioprobe.Refused()
    try Print(mut out, "refused {r}\n", (r))
    var bad: u32 = 0
    for s in 0..seeds {
        x := rtioprobe.CancelStress(Trunc[u64](s + 1) *% 0x9E37_79B9_7F4A_7C15, Trunc[int](rounds))
        if x != 0 && bad == 0 {
            bad = x
            try Print(mut out, "stressseed {s}\n", (s))
        }
    }
    try Print(mut out, "cancelstress {bad}\n", (bad))
    var tbad: u32 = 0
    for s in 0..seeds / 4 {
        x := rtioprobe.ThreadStress(Trunc[u64](s + 7) *% 0xBF58_476D_1CE4_E5B9, Trunc[int](1 + s % 4), Trunc[int](rounds))
        if x != 0 && tbad == 0 {
            tbad = x
            try Print(mut out, "threadseed {s}\n", (s))
        }
    }
    try Print(mut out, "threadstress {tbad}\n", (tbad))
    po := rtioprobe.PathOps()
    try Print(mut out, "pathops {po}\n", (po))
    ei := rtioprobe.EdgeInject()
    try Print(mut out, "edgeinject {ei}\n", (ei))
    lk := rtioprobe.Leaks(Trunc[int](leaks))
    try Print(mut out, "leaks {lk}\n", (lk))
    wr := rtioprobe.WaitRes(20)
    try Print(mut out, "waitres {wr}\n", (wr))
    wc := rtioprobe.WaitCost(20000)
    try Print(mut out, "waitcost {wc}\n", (wc))
    rd := rtioprobe.RemoveDir()
    try Print(mut out, "removedir {rd}\n", (rd))
    ph := rtioprobe.Paths()
    try Print(mut out, "paths {ph}\n", (ph))
    ns := rtioprobe.PingPong(Trunc[int](pings))
    try Print(mut out, "pingpong {ns}\n", (ns))
    try out.Flush()
}
`

// rtioCases are the lines whose value must be 0.
var rtioCases = []string{"echo", "cancelrecv", "cancelaccept", "wake", "addrinuse", "many", "file", "refused", "cancelstress", "threadstress", "pathops", "edgeinject", "parkgap", "leaks", "paths", "removedir",
	// std/rt-io_probe3_test.go, one per fixed defect.
	"handoff", "semhuge", "hugewait", "wakesteal", "donerace", "sockepipe", "kindsock", "posread", "inet6", "sigrace", "interrupt", "resubrace", "genstale", "hrdirty", "resolve"}

const (
	rtioSeeds  = 32
	rtioRounds = 300
	rtioPings  = 20000
	rtioLeaks  = 200
)

// buildRTIOProbe builds the probe for target (empty: the host) and returns
// the executable's path.
func buildRTIOProbe(t *testing.T, target string) string {
	t.Helper()
	manifestPath := "pkg.mk"
	if src := os.Getenv("MK_RTIO_STD"); src != "" {
		manifestPath = filepath.Join(src, "pkg.mk")
	}
	manifest, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	// The probe uses unsafe: append it to unsafe_modules, whatever the list
	// holds.
	text := string(manifest)
	at := strings.Index(text, "unsafe_modules: [")
	end := -1
	if at >= 0 {
		end = strings.Index(text[at:], "]")
	}
	if end < 0 {
		t.Fatal("pkg.mk: no unsafe_modules list")
	}
	m := text[:at+end] + `, "rtioprobe"` + text[at+end:]
	root := t.TempDir()
	fixtures := map[string]string{"pkg.mk": m, "rtioprobe/probe.mk": rtioProbe, "rtioprobe/probe2.mk": rtioProbe2, "rtioprobe/probe3.mk": rtioProbe3}
	if err := os.MkdirAll(filepath.Join(root, "std", "rtioprobe"), 0o755); err != nil {
		t.Fatal(err)
	}
	var stdDir string
	if src := os.Getenv("MK_RTIO_STD"); src != "" {
		// A std of the caller's choosing (for example git HEAD with only
		// some files changed, or a mutant).
		stdDir = filepath.Join(root, "std")
		if err := os.CopyFS(stdDir, os.DirFS(src)); err != nil {
			t.Fatal(err)
		}
		for p, text := range fixtures {
			if err := os.WriteFile(filepath.Join(stdDir, filepath.FromSlash(p)), []byte(text), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	} else {
		stdDir = copyStd(t, root, fixtures)
	}
	if err := os.WriteFile(filepath.Join(root, "main.mk"), []byte(rtioMain), 0o644); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(root, "probe")
	args := []string{"build"}
	if target != "" {
		args = append(args, "--target", target)
	} else if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	args = append(args, "-o", exe, "main.mk")
	out, status := mkCmd(t, root, stdDir, args...)
	if status != 0 {
		if strings.Contains(out, "no usable LLVM") {
			t.Skipf("needs the LLVM toolchain: %s", out)
		}
		t.Fatalf("mk build: exit %d\n%s", status, out)
	}
	return exe
}

// rtioMkdir makes the empty directories rtio_dir (RemoveDir) and rtio_rd0
// to rtio_rd39 (ResubRace) beside exe (std.sys has no call that makes one).
func rtioMkdir(t *testing.T, exe string) {
	t.Helper()
	names := []string{"rtio_dir"}
	for i := 0; i < 40; i++ {
		names = append(names, fmt.Sprintf("rtio_rd%d", i))
	}
	for _, n := range names {
		if err := os.Mkdir(filepath.Join(filepath.Dir(exe), n), 0o755); err != nil && !os.IsExist(err) {
			t.Fatal(err)
		}
	}
}

// runOpenStdin runs exe like runNative, but with a standard input that
// stays open and empty until the process exits, so a read of it blocks
// (the Interrupt case interrupts one).
func runOpenStdin(t *testing.T, exe string, args ...string) nativeRun {
	t.Helper()
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer pw.Close()
	ctx, cancel := context.WithTimeout(context.Background(), runTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, args...)
	cmd.Dir = filepath.Dir(exe)
	cmd.Stdin = pr
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	start := time.Now()
	err = cmd.Run()
	pr.Close()
	r := nativeRun{stdout: out.String(), stderr: errb.String(), took: time.Since(start)}
	if ctx.Err() != nil {
		t.Fatalf("%s: killed after %s\nstdout:\n%s\nstderr:\n%s", filepath.Base(exe), runTimeout, r.stdout, r.stderr)
	}
	if ee, ok := err.(*exec.ExitError); ok {
		r.code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return r
}

// rtioJudge checks one run's output (the backend, every case 0) and logs
// the ping-pong time per round trip; it returns the lines by name.
func rtioJudge(t *testing.T, out, backend string) map[string]string {
	t.Helper()
	got := map[string]string{}
	for _, l := range strings.Split(strings.TrimSpace(strings.ReplaceAll(out, "\r\n", "\n")), "\n") {
		if k, v, ok := strings.Cut(l, " "); ok {
			got[k] = v
		}
	}
	if got["backend"] != backend {
		t.Errorf("backend %q, want %s\n%s", got["backend"], backend, out)
	}
	for _, c := range rtioCases {
		if got[c] != "0" {
			if !rtioJudged(c) {
				t.Logf("%s: %q (%s: the case has a wall-clock step, so it is not judged)", c, got[c], timingOff)
				continue
			}
			t.Errorf("%s: %q, want 0 (else the number of the failed step)\n%s", c, got[c], out)
		}
	}
	if pc, err := strconv.ParseInt(got["poscost"], 10, 64); err == nil {
		t.Logf("backend %s: a positioned 4 KiB ReadSync of a cached file costs %d ns (20,000 reads)", backend, pc)
	}
	if wc, err := strconv.ParseInt(got["waitcost"], 10, 64); err == nil {
		t.Logf("backend %s: a PollWait(10 ms) that returns at once (PollWake first) costs %d ns with the wake (20,000 waits)", backend, wc)
	}
	if wr, err := strconv.ParseInt(got["waitres"], 10, 64); err == nil {
		t.Logf("backend %s: a 1 ms PollWait with nothing to complete lasts %v on average (20 waits)", backend, time.Duration(wr))
	}
	if ns, err := strconv.ParseInt(got["pingpong"], 10, 64); err != nil || ns < 0 {
		t.Errorf("pingpong %q\n%s", got["pingpong"], out)
	} else {
		t.Logf("backend %s: %d one-byte loopback round trips in %v, %d ns each (std.sys alone, one thread)",
			backend, rtioPings, time.Duration(ns), ns/rtioPings)
	}
	return got
}

// TestRt_PollerWindows: IOCP files and sockets, cancellation in flight
// (RT-Io-CancelInFlight), the cancel races of CancelStress over 32 seeds,
// exactly-once delivery of every completion, and only KERNEL32.dll in the
// import table (ws2_32 is loaded at run time, on the first socket call).
func TestRt_PollerWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("the IOCP backend runs on Windows")
	}
	t.Parallel()
	exe := buildRTIOProbe(t, "")
	rtioMkdir(t, exe)
	r := runOpenStdin(t, exe, strconv.Itoa(rtioSeeds), strconv.Itoa(rtioRounds), strconv.Itoa(rtioPings), strconv.Itoa(rtioLeaks))
	if r.code != 0 {
		t.Fatalf("exit %d\nstdout:\n%s\nstderr:\n%s", r.code, r.stdout, r.stderr)
	}
	rtioJudge(t, r.stdout, "1")
	f, err := pe.Open(exe)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	// ImportedLibraries reads nothing from lld's import directory, so the
	// DLLs are taken from the imported symbols ("name:dll").
	syms, err := f.ImportedSymbols()
	if err != nil {
		t.Fatal(err)
	}
	dlls := map[string]bool{}
	for _, s := range syms {
		if _, dll, ok := strings.Cut(s, ":"); ok {
			dlls[strings.ToLower(dll)] = true
		}
	}
	if len(dlls) != 1 || !dlls["kernel32.dll"] {
		t.Errorf("imports from %v, want KERNEL32.dll only", dlls)
	}
}

var rtioWSL struct {
	once sync.Once
	why  string
}

// needWSL skips (fatal under MK_GATE=1) unless `wsl.exe -e true` answers
// within 30 s, probed once per test binary: a WSL that does not answer
// stops all Linux work, with no retry.
func needWSL(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "windows" {
		t.Skip("the Linux leaves run from Windows through wsl.exe")
	}
	linuxGate(t)
	rtioWSL.once.Do(func() {
		wslGuard()
		p, err := exec.LookPath("wsl.exe")
		if err != nil {
			rtioWSL.why = "wsl.exe not found"
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, p, "-e", "true")
		cmd.WaitDelay = 5 * time.Second
		if err := cmd.Run(); err != nil {
			rtioWSL.why = fmt.Sprintf("`wsl.exe -e true` did not answer within 30 s: %v", err)
		}
	})
	if rtioWSL.why != "" {
		if os.Getenv("MK_GATE") == "1" {
			t.Fatalf("needs WSL: %s", rtioWSL.why)
		}
		t.Skipf("needs WSL: %s", rtioWSL.why)
	}
}

// wslPath is the /mnt form of a Windows path.
func wslPath(p string) string {
	p = filepath.ToSlash(p)
	if len(p) > 2 && p[1] == ':' {
		return "/mnt/" + strings.ToLower(p[:1]) + p[2:]
	}
	return p
}

// runWSL runs the Linux executable exe with env (NAME=VALUE) and args
// through wsl.exe, bounded by 5 minutes.
func runWSL(t *testing.T, exe string, env []string, args ...string) (string, string, int) {
	t.Helper()
	wslGuard()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	argv := append([]string{"--cd", wslPath(filepath.Dir(exe)), "--", "env"}, env...)
	argv = append(argv, "./"+filepath.Base(exe))
	argv = append(argv, args...)
	wslGuard()
	cmd := exec.CommandContext(ctx, "wsl.exe", argv...)
	cmd.WaitDelay = 10 * time.Second
	var o, e bytes.Buffer
	cmd.Stdout, cmd.Stderr = &o, &e
	err := cmd.Run()
	status := 0
	if ee, ok := err.(*exec.ExitError); ok {
		status = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("wsl.exe %q: %v", argv, err)
	}
	return o.String(), e.String(), status
}

// TestRt_PollerLinux: the same probe on x86_64-linux with io_uring and
// with MK_IO=epoll (the ping-pong writes the moment it reads, so a lost
// edge hangs it), and the two backends agree on every case.
func TestRt_PollerLinux(t *testing.T) {
	needWSL(t)
	exe := buildRTIOProbe(t, "x86_64-linux")
	args := []string{strconv.Itoa(rtioSeeds), strconv.Itoa(rtioRounds), strconv.Itoa(rtioPings), strconv.Itoa(rtioLeaks)}
	var outs [2]map[string]string
	for i, b := range []struct{ name, env, backend string }{{"io_uring", "MK_IO=auto", "2"}, {"epoll", "MK_IO=epoll", "3"}} {
		rtioMkdir(t, exe)
		stdout, stderr, code := runWSL(t, exe, []string{b.env}, args...)
		if code != 0 {
			t.Errorf("%s: exit %d\nstdout:\n%s\nstderr:\n%s", b.name, code, stdout, stderr)
			continue
		}
		outs[i] = rtioJudge(t, stdout, b.backend)
	}
	if outs[0] != nil && outs[1] != nil {
		for _, c := range rtioCases {
			if outs[0][c] != outs[1][c] && rtioJudged(c) {
				t.Errorf("%s: io_uring %q, epoll %q; the backends must agree", c, outs[0][c], outs[1][c])
			}
		}
	}
}
