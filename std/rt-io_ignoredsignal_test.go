// Why: proc.WaitSignal must deliver Interrupt to a process that started
// with it ignored (an inherited SIG_IGN, or Ctrl+C disabled on Windows).
// What: rtioProbe8 (probe8.mk), TestRtIo_LinuxIgnored, and
// TestRtIo_LinuxExt4 (probe7.mk's RenameDir on ext4 instead of drvfs).
// How: starts the probe from bash with the signal ignored or not, raises it,
// and judges the lines it prints. The Windows half is rt-io_ignoredsignal_windows_test.go.
//
// The Linux tests start wsl.exe: run them only under the WSL lock,
// `pwsh -NoProfile -File tools/wsl-lock.ps1 go test -p 1 ./std`; parallel
// WSL clients hang WSL.

package std

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// rtioProbe8 is std/rtioprobe/probe8.mk of the copy.
const rtioProbe8 = `// A probe of std.sys, eighth file (std/rt-io_ignoredsignal_test.go).

import std.core
import std.raw
import std.sys

// It uses the earlier files' helpers (Sh, NewPage, Spin, Start, WaitState,
// StopAll, ResubReaper, ParkCount, Loopback, Ok, Backend).

// CtrlWatch watches Interrupt: 0, or 1 when SignalWatch refused.
export CtrlWatch(): u32 {
    if sys.SignalWatch(0) {
        return 0
    }
    return 1
}

// CtrlWait waits up to ms milliseconds (by Now) for an Interrupt, in
// PollWaits of 20 ms (the handler's PollWake ends one early), and returns
// how many arrived. Unwatched, it only sleeps.
export CtrlWait(ms: i64): u64 {
    var out: [4]*sys.Op = [raw.Null[sys.Op](); 4]
    t0 := sys.Now()
    for sys.SignalCount(0) == 0 && sys.Now() - t0 < ms * 1_000_000 {
        _ = sys.PollWait(20_000_000, mut out[..])
    }
    return sys.SignalCount(0)
}

// Handoff word of an IOCP op: the u32 at OsResub (56) of op.os, which
// starts at byte 136 of the C-layout Op (poll_iocp.mk's header).
HandWord(op: *sys.Op): *raw.Atomic[u32] {
    unsafe "the word is inside the live op" {
        return raw.FromAddr[raw.Atomic[u32]](raw.Addr(op) + 136 + 56)
    }
}

// ReissueWin is test point 10's callback, on the reaper thread that is
// inside ReissueAccept after its AcceptEx: it starts the second client's
// connect (ops[1]), so the reissued AcceptEx completes now, cancels the
// accept on odd rounds, and waits (up to 300 ms) until the other reaper
// dequeued the new packet (the handoff word 2) or handed op out (state 3,
// the defect), then 5 ms more. returned is set when op was handed out
// while this thread still held it; res counts the handoffs.
ReissueWin(arg: *u8) {
    g := Sh(arg)
    unsafe "g is the case's page; ops[0] the accept in its window, ops[1] the second connect" {
        op := raw.FromAddr[sys.Op](Trunc[uint]((*g).ops[0].Load()))
        con := raw.FromAddr[sys.Op](Trunc[uint]((*g).ops[1].Load()))
        w := HandWord(op)
        if (*w).Load() != 1 {
            // The reissuer holds the word at 1 across this point; another
            // value means the offset above is wrong.
            (*g).t1.Store(1)
        }
        Start(con)
        if (*g).flag.Load() % 2 == 1 {
            (*op).cancel.Store(1)
            sys.PollCancel(op)
        }
        deadline := sys.Now() + 300_000_000
        for (*w).Load() != 2 && (*op).state.Load() != 3 && sys.Now() < deadline {
            sys.ThreadYield()
        }
        Spin(5)
        if (*w).Load() == 2 {
            _ = (*g).res.Add(1)
        }
        if (*op).state.Load() == 3 {
            (*g).returned.Store(1)
        }
    }
}

// ReissueRace (IOCP): ReissueAccept's handoff (ResubCancel covers its cancel
// check, not the handoff). Each round: a fresh listener and two clients; the
// first accept packet reports a reset (point 5), so the reaper thread that
// dequeues it closes that connection and issues AcceptEx again; in the window
// after that AcceptEx (point 10) the second client connects and the other
// reaper dequeues the new packet while the reissuer still holds op. The op
// must not be handed out inside the window (step 2), is handed out exactly
// once (Mark; step 4), and is an accepted socket, or Canceled on odd rounds,
// where the window also cancels it (step 3). Steps 5 and 6: fewer than half
// the rounds made the handoff (the interleaving was not produced), or the
// handoff word was not where the probe looks. Other backends: 0 (io_uring's
// twin is ResubRace).
export ReissueRace(n: int): u32 {
    if Backend() != 1 {
        return 0
    }
    page := NewPage()
    if raw.IsNull(page) {
        return 10
    }
    g := Sh(page)
    for _ in 0..2 {
        if !Ok(sys.ThreadStart(ResubReaper, page, 262144)) {
            return 11
        }
    }
    var code: u32 = 0
    for i in 0..n {
        l := sys.SocketOpen(sys.Inet4, true)
        if !Ok(l) {
            code = 31
            break
        }
        lh := Trunc[uint](l.value)
        a := Loopback(0)
        var bound := Loopback(0)
        c1 := sys.SocketOpen(sys.Inet4, true)
        c2 := sys.SocketOpen(sys.Inet4, true)
        if !Ok(sys.SocketListen(lh, &a, 16)) || !Ok(sys.SocketName(lh, mut bound)) || !Ok(c1) || !Ok(c2) {
            code = 32
            break
        }
        var acc := sys.NewOp(sys.OpAccept, lh)
        acc.addr.family = sys.Inet4
        var con1 := sys.NewOp(sys.OpConnect, Trunc[uint](c1.value))
        con1.addr = bound
        var con2 := sys.NewOp(sys.OpConnect, Trunc[uint](c2.value))
        con2.addr = bound
        unsafe "g is the case's page" {
            (*g).flag.Store(Trunc[u32](i))
            (*g).ops[0].Store(u64(raw.Addr(raw.OfMut(mut acc))))
            (*g).ops[1].Store(u64(raw.Addr(raw.OfMut(mut con2))))
        }
        _ = sys.SysTestHook(5, ParkCount, raw.Null[u8](), true)
        _ = sys.SysTestHook(10, ReissueWin, page, true)
        Start(raw.OfMut(mut acc))
        Start(raw.OfMut(mut con1))
        var done := WaitState(raw.OfMut(mut acc), 5_000) && WaitState(raw.OfMut(mut con1), 5_000)
        _ = sys.SysTestHook(10, ReissueWin, page, false)
        _ = sys.SysTestHook(5, ParkCount, raw.Null[u8](), false)
        unsafe "g is the case's page" {
            // con2 is started by the window; wait for it only if it ran.
            if done && con2.state.Load() != 0 {
                done = WaitState(raw.OfMut(mut con2), 5_000)
            }
        }
        if !done {
            code = 1
        } else if Ok(acc.res) {
            _ = sys.SocketShutdownClose(Trunc[uint](acc.res.value))
        } else if acc.res.err != core.SysErr.Canceled || i % 2 == 0 {
            code = 3
        }
        _ = sys.SocketShutdownClose(Trunc[uint](c1.value))
        _ = sys.SocketShutdownClose(Trunc[uint](c2.value))
        _ = sys.SocketShutdownClose(lh)
        if !done {
            // Closing the sockets ends what was pending; let it arrive
            // before this frame goes.
            _ = WaitState(raw.OfMut(mut acc), 5_000)
            _ = WaitState(raw.OfMut(mut con1), 5_000)
            if con2.state.Load() != 0 {
                _ = WaitState(raw.OfMut(mut con2), 5_000)
            }
        }
        if code != 0 {
            break
        }
        unsafe "g is the case's page" {
            if (*g).returned.Load() != 0 {
                code = 2
                break
            }
        }
    }
    if !StopAll(g, 2) {
        return 7
    }
    unsafe "g is the case's page" {
        if code == 0 && (*g).bad.Load() != 0 {
            code = 4
        }
        if code == 0 && (*g).t1.Load() != 0 {
            code = 6
        }
        if code == 0 && Trunc[int]((*g).res.Load()) * 2 < n {
            code = 500 + Trunc[u32]((*g).res.Load())
        }
    }
    sys.Release(page, 4096)
    return code
}
`

// rtio55Main: "race" runs ReissueRace over 40 rounds and prints "reissue
// R" (0: passed); "once" watches Interrupt and prints "watch W" (0: true);
// "watch" watches Interrupt, prints "ready", waits up to 10 s
// for one and prints how many came ("got N"); "plain" prints "ready",
// sleeps 3 s without watching and prints "survived" (an Interrupt that
// arrives kills it first: the default action).
const rtio55Main = `import std.rtioprobe

export Main(sys: &System): WriteError!void {
    var out := sys.Stdout()
    if sys.args[1] == "race" {
        r := rtioprobe.ReissueRace(40)
        try Print(mut out, "reissue {r}\n", (r))
        try out.Flush()
        return
    }
    if sys.args[1] == "once" {
        w := rtioprobe.CtrlWatch()
        try Print(mut out, "watch {w}\n", (w))
        try out.Flush()
        return
    }
    if sys.args[1] == "watch" {
        w := rtioprobe.CtrlWatch()
        try Print(mut out, "watch {w}\n", (w))
        try out.Str("ready\n")
        try out.Flush()
        n := rtioprobe.CtrlWait(10_000)
        try Print(mut out, "got {n}\n", (n))
        try out.Flush()
        return
    }
    try out.Str("ready\n")
    try out.Flush()
    _ = rtioprobe.CtrlWait(3_000)
    try out.Str("survived\n")
    try out.Flush()
}
`

// buildRTIO55 builds the probe8.mk probe for target (empty: the host), over
// the tree's std or over MK_RTIO_STD (for mutants), and returns the
// executable's path.
func buildRTIO55(t *testing.T, target string) string {
	t.Helper()
	src := os.Getenv("MK_RTIO_STD")
	manifestPath := "pkg.mk"
	if src != "" {
		manifestPath = filepath.Join(src, "pkg.mk")
	}
	manifest, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(manifest)
	at := strings.Index(text, "unsafe_modules: [")
	end := -1
	if at >= 0 {
		end = strings.Index(text[at:], "]")
	}
	if end < 0 {
		t.Fatal("pkg.mk: no unsafe_modules list")
	}
	fixtures := map[string]string{
		"pkg.mk":              text[:at+end] + `, "rtioprobe"` + text[at+end:],
		"rtioprobe/probe.mk":  rtioProbe,
		"rtioprobe/probe2.mk": rtioProbe2,
		"rtioprobe/probe3.mk": rtioProbe3,
		"rtioprobe/probe4.mk": rtioProbe4,
		"rtioprobe/probe5.mk": rtioProbe5,
		"rtioprobe/probe6.mk": rtioProbe6,
		"rtioprobe/probe7.mk": rtioProbe7,
		"rtioprobe/probe8.mk": rtioProbe8,
	}
	root := t.TempDir()
	var stdDir string
	if src != "" {
		stdDir = filepath.Join(root, "std")
		if err := os.CopyFS(stdDir, os.DirFS(src)); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(stdDir, "rtioprobe"), 0o755); err != nil {
			t.Fatal(err)
		}
		for p, text := range fixtures {
			if err := os.WriteFile(filepath.Join(stdDir, filepath.FromSlash(p)), []byte(text), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	} else {
		if err := os.MkdirAll(filepath.Join(root, "std", "rtioprobe"), 0o755); err != nil {
			t.Fatal(err)
		}
		stdDir = copyStd(t, root, fixtures)
	}
	if err := os.WriteFile(filepath.Join(root, "main.mk"), []byte(rtio55Main), 0o644); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(root, "probe55")
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

// rtio55Lines splits a run's output into its lines by first word.
func rtio55Lines(out string) map[string]string {
	got := map[string]string{}
	for _, l := range strings.Split(strings.TrimSpace(strings.ReplaceAll(out, "\r\n", "\n")), "\n") {
		k, v, _ := strings.Cut(l, " ")
		got[k] = v
	}
	return got
}

// wslSh runs script under sh in WSL (one wsl.exe), bounded by 5 minutes.
func wslSh(t *testing.T, script string) (string, string, int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	wslGuard()
	cmd := exec.CommandContext(ctx, "wsl.exe", "-e", "sh", "-c", script)
	cmd.WaitDelay = 10 * time.Second
	var o, e bytes.Buffer
	cmd.Stdout, cmd.Stderr = &o, &e
	err := cmd.Run()
	status := 0
	if ee, ok := err.(*exec.ExitError); ok {
		status = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("wsl.exe sh -c: %v", err)
	}
	return o.String(), e.String(), status
}

// rtio55LinuxScript starts the probe in mode with SIGINT ignored (an empty
// trap on INT, which a child inherits as SIG_IGN; a non-interactive sh ignores it in
// background jobs anyway) and sends SIGINT every 100 ms until it exits, at
// most 12 s. It prints the probe's output and then "exit N".
// With ignore false it starts it under job control instead (set -m), so
// the background probe keeps SIGINT's default action.
func rtio55LinuxScript(exe, env, mode string, ignore bool) string {
	start := "trap '' INT; "
	if !ignore {
		start = "set -m; "
	}
	// Separate commands, not an && list: "a && b > f &" would run the whole
	// list in a background subshell, so $! would not be the probe and the
	// cd would not reach the cat.
	return "cd '" + wslPath(filepath.Dir(exe)) + "' || exit 9; " + start +
		"env " + env + " ./" + filepath.Base(exe) + " " + mode + " > rtio55." + mode + ".out 2>&1 & pid=$!; " +
		"i=0; while [ $i -lt 120 ] && kill -0 $pid 2>/dev/null; do sleep 0.1; kill -INT $pid 2>/dev/null; i=$((i+1)); done; " +
		"wait $pid; c=$?; cat rtio55." + mode + ".out; echo \"exit $c\""
}

// TestRtIo_LinuxIgnored: with SIGINT inherited as ignored, a watching
// probe still gets it (the handler replaces SIG_IGN) and a probe that does
// not watch survives every SIGINT, on io_uring and epoll: the Windows test's
// answers. Control: under job control (SIGINT not ignored) the unwatching
// probe dies of it (130), so the script really delivers it.
func TestRtIo_LinuxIgnored(t *testing.T) {
	if os.Getenv("MK_RTIO_BUILD_ONLY") != "" {
		t.Logf("built %s", buildRTIO55(t, "x86_64-linux"))
		return
	}
	needWSL(t)
	exe := buildRTIO55(t, "x86_64-linux")
	for _, b := range []string{"MK_IO=auto", "MK_IO=epoll"} {
		out, stderr, code := wslSh(t, rtio55LinuxScript(exe, b, "plain", false))
		got := rtio55Lines(out)
		if code != 0 || got["exit"] != "130" || strings.Contains(out, "survived") {
			t.Errorf("%s plain (SIGINT not ignored): want exit 130 before survived\nstdout:\n%s\nstderr:\n%s", b, out, stderr)
		}
		out, stderr, code = wslSh(t, rtio55LinuxScript(exe, b, "watch", true))
		got = rtio55Lines(out)
		if code != 0 || got["watch"] != "0" || got["got"] == "" || got["got"] == "0" || got["exit"] != "0" {
			t.Errorf("%s watch (SIGINT ignored at start): want watch 0, got >= 1, exit 0\nstdout:\n%s\nstderr:\n%s", b, out, stderr)
		}
		out, stderr, code = wslSh(t, rtio55LinuxScript(exe, b, "plain", true))
		got = rtio55Lines(out)
		if code != 0 || got["exit"] != "0" || !strings.Contains(out, "survived") {
			t.Errorf("%s plain (SIGINT ignored, not watched): want survived, exit 0\nstdout:\n%s\nstderr:\n%s", b, out, stderr)
		}
	}
}

// TestRtIo_LinuxExt4: probe7.mk's cases (RenameDir among them) with the
// probe and its fixtures in a directory of the Linux file system (under
// $HOME in WSL, not drvfs), on io_uring and epoll; the file system type is
// logged from df -T, and the backends must agree.
func TestRtIo_LinuxExt4(t *testing.T) {
	if os.Getenv("MK_RTIO_BUILD_ONLY") != "" {
		t.Logf("built %s", buildRTIO54(t, "x86_64-linux"))
		return
	}
	needWSL(t)
	exe := buildRTIO54(t, "x86_64-linux")
	fixtures := "rm -rf rtio_rfile rtio_rdir rtio_rdir2 rtio_rfull && mkdir rtio_rdir rtio_rdir2 rtio_rfull && " +
		"printf '\\001' > rtio_rfile && printf '\\001' > rtio_rfull/x"
	script := "set -e; d=$(mktemp -d \"$HOME/rtio55.XXXXXX\"); cp '" + wslPath(exe) + "' \"$d/\"; cd \"$d\"; " +
		"echo \"fs $(df -T . | tail -1 | awk '{print $2}')\"; " +
		"for b in auto epoll; do " + fixtures + "; echo \"run $b\"; MK_IO=$b ./" + filepath.Base(exe) + " || echo \"exit $?\"; done; " +
		"cd /; rm -rf \"$d\""
	out, stderr, code := wslSh(t, script)
	if code != 0 {
		t.Fatalf("exit %d\nstdout:\n%s\nstderr:\n%s", code, out, stderr)
	}
	text := strings.ReplaceAll(out, "\r\n", "\n")
	fs, _, _ := strings.Cut(strings.TrimPrefix(text, "fs "), "\n")
	t.Logf("file system: %s", fs)
	if fs == "9p" || fs == "drvfs" || fs == "" {
		t.Errorf("the probe ran on %q, not a Linux file system", fs)
	}
	parts := strings.Split(text, "run ")
	if len(parts) != 3 {
		t.Fatalf("want two runs\n%s", out)
	}
	var outs [2]map[string]string
	for i, p := range parts[1:] {
		_, body, _ := strings.Cut(p, "\n")
		if strings.Contains(body, "\nexit ") || strings.HasPrefix(body, "exit ") {
			t.Errorf("run %d failed\n%s", i, body)
			continue
		}
		outs[i] = rtio54Judge(t, body, []string{"2", "3"}[i])
		t.Logf("run %d: renamedir %s (os %s)", i, outs[i]["renamedir"], outs[i]["renameos"])
	}
	if outs[0] != nil && outs[1] != nil {
		for _, c := range append(rtio54Cases, "renameos") {
			if outs[0][c] != outs[1][c] {
				t.Errorf("%s: io_uring %q, epoll %q; the backends must agree", c, outs[0][c], outs[1][c])
			}
		}
	}
}
