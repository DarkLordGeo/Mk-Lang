// Why: Chan and Mutex must survive cancellation at any await point and run
// the same on several carriers and on Linux.
// What: the cancel sweep, cancel-ahead-of-waiter, Send-waits and Io-in-struct
// tests, multi-carrier benchmarks, and TestSTDSYNC_Linux in WSL.
// How: seeded Mk programs on 1 and 8 carriers; the Linux test cross-builds
// them and runs one shell script per profile under one wsl.exe client.

package std

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// syncSweepSrc: Main runs one round for each k in 0..K. A round starts two
// senders of M linear tokens each and three lockers of a Mutex (each holds
// the lock across a Yield, so the others spin and park on it) in scope s,
// and two receivers in an outer scope r that drain the channel until it is
// closed; Main yields k times and cancels s. On one carrier k walks the
// cancel over every await point of the workload in turn: a Send or Lock at
// its cancellation point, a Send parked on a full or rendezvous channel,
// a Lock parked on the word, a Recv parked on an empty channel (mode 1:
// the receivers are in s too, so they are cancelled while parked), and
// after the end (no effect). Each round checks:
// every token was received once or kept once by its failed sender (C5),
// the id sums agree, the Mutex count equals the successful Locks, the
// mutex is free afterwards, and the channel frees (no parked cell left).
// Output: "fails kept_total zero_kept_rounds".
const syncSweepSrc = `import std.atomic

linear struct Tok {
    s: int
    q: int
}

Take(t: Tok): int {
    Tok(s, q) := t
    return s * 1000 + q
}

Sender(c: &Chan[Tok], io: Io, id: int, m: int, kept: &atomic.AtomicInt, sum: &atomic.AtomicInt) {
    for q in 0..m {
        t := Tok(s: id, q: q)
        c.Send(io, t) else {
            _ = sum.Add(i64(Take(t)))
            _ = kept.Add(1)
            for r in q + 1..m {
                _ = sum.Add(i64(id * 1000 + r))
                _ = kept.Add(1)
            }
            return
        }
        io.Yield() else {}
    }
}

Receiver(c: &Chan[Tok], io: Io, n: &atomic.AtomicInt, sum: &atomic.AtomicInt) {
    for {
        t := c.Recv(io) else {
            return
        }
        _ = sum.Add(i64(Take(t)))
        _ = n.Add(1)
    }
}

Locker(mu: &Mutex[int], io: Io, got: &atomic.AtomicInt) {
    for _ in 0..10 {
        var g := mu.Lock(io) else {
            return
        }
        *g.Mut() += 1
        _ = got.Add(1)
        io.Yield() else {}
        g.Unlock()
    }
}

// Round returns 1 when a check failed, else 0; kept gets the kept count.
Round(sys: &System, k: int, cap: int, mode: int, kept: &atomic.AtomicInt): OutOfMemory!int {
    a := sys.heap.Alloc()
    c := try Chan[Tok].New(a, cap)
    mu := Mutex[int].New(0)
    n := atomic.AtomicInt.New(0)
    sum := atomic.AtomicInt.New(0)
    ksum := atomic.AtomicInt.New(0)
    got := atomic.AtomicInt.New(0)
    kept.Store(0)
    var left := 0
    scope r := sys.io.Scope() {
        if mode == 0 {
            r.go Receiver(&c, sys.io, &n, &sum)
            r.go Receiver(&c, sys.io, &n, &sum)
        }
        scope s := sys.io.Scope() {
            if mode == 1 {
                s.go Receiver(&c, sys.io, &n, &sum)
                s.go Receiver(&c, sys.io, &n, &sum)
            }
            s.go Sender(&c, sys.io, 1, 20, kept, &ksum)
            s.go Sender(&c, sys.io, 2, 20, kept, &ksum)
            for _ in 0..3 {
                s.go Locker(&mu, sys.io, &got)
            }
            for _ in 0..k {
                sys.io.Yield() else {}
            }
            s.Cancel()
        }
        c.Close()
    }
    // Mode 1: what the cancelled receivers left in the ring.
    for {
        t := c.Recv(sys.io) else {
            break
        }
        _ = sum.Add(i64(Take(t)))
        left += 1
    }
    c.FreeEmpty(a)
    var g := mu.Lock(sys.io) else {
        return 1
    }
    g.Unlock()
    var want := 0
    for id in 1..3 {
        for q in 0..20 {
            want += id * 1000 + q
        }
    }
    var bad := 0
    if n.Load() + i64(left) + kept.Load() != 40 {
        bad = 1
    }
    if sum.Load() + ksum.Load() != i64(want) {
        bad = 1
    }
    if i64(mu.Into()) != got.Load() {
        bad = 1
    }
    return bad
}

export Main(sys: &System): ParseError|OutOfMemory|WriteError!void {
    cap := int(try sys.args[1].ParseInt())
    mode := int(try sys.args[2].ParseInt())
    rounds := int(try sys.args[3].ParseInt())
    kept := atomic.AtomicInt.New(0)
    var fails := 0
    var total: i64 = 0
    var zero := 0
    var out := sys.Stdout()
    for k in 0..rounds {
        f := try Round(sys, k, cap, mode, &kept)
        if f != 0 {
            try Print(mut out, "fail k={k}\n", (k))
        }
        fails += f
        total += kept.Load()
        if kept.Load() == 0 {
            zero += 1
        }
    }
    try Print(mut out, "{fails} {total} {zero}\n", (fails, total, zero))
}
`

// syncSweepRounds covers the workload on one carrier: it ends within about
// 30 of Main's yields, so the last rounds cancel after the end.
const syncSweepRounds = "80"

// checkSweep wants "0 kept zero" with kept > 0 (some cancel landed before
// the senders were done) and zero > 0 (some round ran to the end).
func checkSweep(t *testing.T, env, args []string, r nativeRun) {
	t.Helper()
	var fails, kept, zero int
	if _, err := fmt.Sscanf(r.stdout, "%d %d %d\n", &fails, &kept, &zero); err != nil || r.code != 0 || fails != 0 || kept == 0 || zero == 0 {
		t.Errorf("%v %v: exit %d, stdout %q: want 0 failed rounds, some tokens kept, some rounds uncancelled\nstderr:\n%s", env, args, r.code, r.stdout, r.stderr)
	}
}

// TestSTDSYNC_CancelSweep: syncSweepSrc in mode 0 (the receivers are not
// cancelled, so every parked sender is eventually served and the program
// ends; a sender or locker parked when the cancel lands returns Canceled). Capacities 0, 1 and 4; one carrier
// (the deterministic sweep) and 8 carriers with jitter over the seeds; dev
// and release.
func TestSTDSYNC_CancelSweep(t *testing.T) {
	t.Parallel()
	for _, flags := range [][]string{nil, {"--release"}} {
		exe := buildSync(t, syncSweepSrc, flags...)
		for _, cap := range []string{"0", "1", "4"} {
			args := []string{cap, "0", syncSweepRounds}
			r, hung := runSyncEnv(t, exe, syncTimeout, syncOne, args...)
			if hung {
				t.Fatalf("%v: no exit within %v (a lost wakeup?)\nstdout:\n%s", args, syncTimeout, r.stdout)
			}
			checkSweep(t, syncOne, args, r)
			for seed := 1; seed <= syncSeeds(); seed++ {
				env := syncStressEnv(8, seed)
				r, hung := runSyncEnv(t, exe, syncTimeout, env, args...)
				if hung {
					t.Fatalf("%v %v: no exit within %v (a lost wakeup?)\nstdout:\n%s", env, args, syncTimeout, r.stdout)
				}
				// On 8 carriers the senders may finish before any cancel, so
				// only the failed-round count is checked.
				if !strings.HasPrefix(r.stdout, "0 ") || r.code != 0 {
					t.Errorf("%v %v: exit %d, stdout %q\nstderr:\n%s", env, args, r.code, r.stdout, r.stderr)
				}
			}
		}
	}
}

// TestSTDSYNC_CancelSweepParked: mode 1, the receivers in the cancelled
// scope too, so a cancel can land on a Recv parked on an empty channel and
// on a Send parked with nobody left to take its value: each must return
// Canceled, and the Send keeps x. A hang is a lost wakeup and fails.
// Capacities 0, 1 and 4, dev and release, one carrier
// (the deterministic sweep) and 1 and 8 carriers with jitter over the seeds.
func TestSTDSYNC_CancelSweepParked(t *testing.T) {
	t.Parallel()
	for _, flags := range [][]string{nil, {"--release"}} {
		exe := buildSync(t, syncSweepSrc, flags...)
		for _, cap := range []string{"0", "1", "4"} {
			args := []string{cap, "1", syncSweepRounds}
			r, hung := runSyncEnv(t, exe, syncTimeout, syncOne, args...)
			if hung {
				t.Fatalf("%v %v: no exit within %v (a lost wakeup?)\nstdout:\n%s", flags, args, syncTimeout, r.stdout)
			}
			checkSweep(t, syncOne, args, r)
			for _, n := range syncCarriers {
				for seed := 1; seed <= syncSeeds(); seed++ {
					env := syncStressEnv(n, seed)
					r, hung := runSyncEnv(t, exe, syncTimeout, env, args...)
					if hung {
						t.Fatalf("%v %v %v: no exit within %v (a lost wakeup?)\nstdout:\n%s", flags, env, args, syncTimeout, r.stdout)
					}
					if !strings.HasPrefix(r.stdout, "0 ") || r.code != 0 {
						t.Errorf("%v %v %v: exit %d, stdout %q\nstderr:\n%s", flags, env, args, r.code, r.stdout, r.stderr)
					}
				}
			}
		}
	}
}

// syncSTD10WaitSrc: Send waits for a receiver: the receiver sleeps
// 10 ms, sets a flag, then calls Recv; Send at capacity 0 returns only
// after that, so the flag is set when it does.
const syncSTD10WaitSrc = `import std.atomic
import std.time

Receiver(c: &Chan[int], io: Io, flag: &atomic.AtomicBool, got: &atomic.AtomicInt) {
    io.Sleep(time.Millis(10)) else {
        return
    }
    flag.Store(true)
    x := c.Recv(io) else {
        return
    }
    got.Store(i64(x))
}

export Main(sys: &System): Canceled|Closed|OutOfMemory|WriteError!void {
    c := try Chan[int].New(sys.arena.Alloc(), 0)
    flag := atomic.AtomicBool.New(false)
    got := atomic.AtomicInt.New(0)
    var seen := false
    scope s := sys.io.Scope() {
        s.go Receiver(&c, sys.io, &flag, &got)
        try c.Send(sys.io, 7)
        seen = flag.Load()
    }
    var out := sys.Stdout()
    try Print(mut out, "{seen} {g}\n", (seen, g: got.Load()))
}
`

// syncSTD12Src: an Io copied into a struct acts for the task that
// uses it: the child sleeps through ctx.io and then sends, while Main waits
// in Recv (if the runtime took the task from the Io, the child's sleep
// would park Main and the program would hang). A NoCancel Io passed to
// s.go stays uncancellable in the child: in a cancelled scope its Lock,
// Send and Sleep(0) succeed, while the plain Io's Sleep(0) and its Lock of
// the free mutex, cancellation points whether or not they wait, are
// Canceled. SizeOf[Io]() is 16.
const syncSTD12Src = `import std.atomic
import std.time

struct Ctx {
    io: Io
}

Work(ctx: Ctx, c: &Chan[int]) {
    ctx.io.Sleep(time.Millis(10)) else {
        return
    }
    c.Send(ctx.io, 5) else {}
}

Cleanup(io: Io, plain: Io, mu: &Mutex[int], c: &Chan[int], r: &atomic.AtomicInt) {
    var g := mu.Lock(io) else {
        r.Store(-1)
        return
    }
    *g.Mut() += 1
    g.Unlock()
    c.Send(io, 9) else {
        r.Store(-2)
        return
    }
    io.Sleep(time.Millis(0)) else {
        r.Store(-3)
        return
    }
    plain.Sleep(time.Millis(0)) else {
        var g2 := mu.Lock(plain) else {
            r.Store(1)
            return
        }
        g2.Unlock()
        r.Store(-5)
        return
    }
    r.Store(-4)
}

export Main(sys: &System): Canceled|Closed|OutOfMemory|WriteError!void {
    c := try Chan[int].New(sys.arena.Alloc(), 1)
    var x := 0
    scope s := sys.io.Scope() {
        s.go Work(Ctx(io: sys.io), &c)
        x = try c.Recv(sys.io)
    }
    mu := Mutex[int].New(0)
    r := atomic.AtomicInt.New(0)
    scope s2 := sys.io.Scope() {
        s2.Cancel()
        s2.go Cleanup(sys.io.NoCancel(), sys.io, &mu, &c, &r)
    }
    y := try c.Recv(sys.io)
    var out := sys.Stdout()
    try Print(mut out, "{x} {r} {y} {m} {n}\n", (x, r: r.Load(), y, m: mu.Into(), n: SizeOf[Io]()))
}
`

// TestSTDSYNC_SendWaitsForReceiver and TestSTDSYNC_IoInStruct
// run their programs on 1 and 8 carriers, dev and release.
func TestSTDSYNC_SendWaitsForReceiver(t *testing.T) {
	t.Parallel()
	for _, flags := range [][]string{nil, {"--release"}} {
		exe := buildSync(t, syncSTD10WaitSrc, flags...)
		for _, n := range syncCarriers {
			wantSyncEnv(t, exe, syncStressEnv(n, 1), 0, "true 7\n")
		}
	}
}

func TestSTDSYNC_IoInStruct(t *testing.T) {
	t.Parallel()
	for _, flags := range [][]string{nil, {"--release"}} {
		exe := buildSync(t, syncSTD12Src, flags...)
		for _, n := range syncCarriers {
			wantSyncEnv(t, exe, syncStressEnv(n, 1), 0, "5 1 9 1 16\n")
		}
	}
}

// syncBenchContended: the multi-carrier benchmarks. 8 tasks each take a
// Mutex 250,000 times (2M Lock+Unlock in all, the critical section one
// add); 8 senders and 8 receivers move 2M ints through a capacity-64
// channel.
var syncBenchContended = []struct{ name, src, want string }{
	{"Mutex 8 tasks x 250k", `Worker(m: &Mutex[int], io: Io) {
    for _ in 0..250000 {
        var g := m.Lock(io) else {
            return
        }
        *g.Mut() += 1
        g.Unlock()
    }
}

export Main(sys: &System): WriteError!void {
    m := Mutex[int].New(0)
    scope s := sys.io.Scope() {
        for _ in 0..8 {
            s.go Worker(&m, sys.io)
        }
    }
    var out := sys.Stdout()
    try Print(mut out, "{n}\n", (n: m.Into()))
}
`, "2000000\n"},
	{"Chan cap 64, 8 senders x 8 receivers, 2M", `import std.atomic

Sender(c: &Chan[int], io: Io) {
    for i in 0..250000 {
        c.Send(io, i) else {
            return
        }
    }
}

Receiver(c: &Chan[int], io: Io, n: &atomic.AtomicInt) {
    var k: i64 = 0
    for {
        x := c.Recv(io) else {
            break
        }
        k += i64(x)
    }
    _ = n.Add(k)
}

export Main(sys: &System): WriteError|OutOfMemory!void {
    c := try Chan[int].New(sys.arena.Alloc(), 64)
    n := atomic.AtomicInt.New(0)
    scope r := sys.io.Scope() {
        for _ in 0..8 {
            r.go Receiver(&c, sys.io, &n)
        }
        scope s := sys.io.Scope() {
            for _ in 0..8 {
                s.go Sender(&c, sys.io)
            }
        }
        c.Close()
    }
    var out := sys.Stdout()
    try Print(mut out, "{n}\n", (n: n.Load()))
}
`, "249999000000\n"},
	{"AtomicInt.Add 8 tasks x 2M", `import std.atomic

Adder(a: &atomic.AtomicInt) {
    for _ in 0..2000000 {
        _ = a.Add(1)
    }
}

export Main(sys: &System): WriteError!void {
    a := atomic.AtomicInt.New(0)
    scope s := sys.io.Scope() {
        for _ in 0..8 {
            s.go Adder(&a)
        }
    }
    var out := sys.Stdout()
    try Print(mut out, "{n}\n", (n: a.Load()))
}
`, "16000000\n"},
}

// The capacity-0 form of the 8x8 channel benchmark: every value is a
// handoff to a parked receiver, the path whose wake runs outside the
// channel's lock.
func init() {
	p := syncBenchContended[1]
	syncBenchContended = append(syncBenchContended, struct{ name, src, want string }{
		"Chan cap 0, 8 senders x 8 receivers, 2M",
		strings.Replace(p.src, "Chan[int].New(sys.arena.Alloc(), 64)", "Chan[int].New(sys.arena.Alloc(), 0)", 1),
		p.want,
	})
}

// TestSTDSYNC_BenchCarriers: TestSTDSYNC_Bench's single-task programs and
// syncBenchContended, release, whole-process wall times with 1 and 8
// carriers, MK_BENCH_RUNS rounds (default 7) in which every program and
// carrier count runs once in turn (interleaved, so a burst of load from
// another process hits all of them alike); it logs min and median with the
// machine. Fails only on a wrong result.
func TestSTDSYNC_BenchCarriers(t *testing.T) {
	if os.Getenv("MK_BENCH") == "" {
		t.Skip("set MK_BENCH=1 to time the stress programs")
	}
	rounds := 7
	if n, err := strconv.Atoi(os.Getenv("MK_BENCH_RUNS")); err == nil && n > 0 {
		rounds = n
	}
	progs := append(append([]struct{ name, src, want string }{}, syncBenchSingle...), syncBenchContended...)
	exes := make([]string, len(progs))
	for i, p := range progs {
		exes[i] = buildSync(t, p.src, "--release")
	}
	took := map[string][]time.Duration{}
	for range rounds {
		for i, p := range progs {
			for _, n := range syncCarriers {
				r := wantSyncEnv(t, exes[i], []string{fmt.Sprintf("MK_CARRIERS=%d", n)}, 0, p.want)
				key := fmt.Sprintf("%s, %d carriers", p.name, n)
				took[key] = append(took[key], r.took)
			}
		}
	}
	var report strings.Builder
	for _, p := range progs {
		for _, n := range syncCarriers {
			key := fmt.Sprintf("%s, %d carriers", p.name, n)
			ts := slices.Clone(took[key])
			slices.Sort(ts)
			fmt.Fprintf(&report, "%s: min %v, median %v of %d%s", key, ts[0].Round(time.Millisecond), ts[len(ts)/2].Round(time.Millisecond), len(ts), "\n")
		}
	}
	t.Log("\n" + report.String())
}

// syncLinuxCase is one run of a sync program on x86_64-linux. one marks a
// program whose output depends on the schedule beyond one carrier (it runs
// with MK_CARRIERS=1 only); check names a check other than equal stdout:
// "sweep" (no failed round) or "starve" (checkStarve).
type syncLinuxCase struct {
	name  string
	src   string
	args  []string
	code  int
	want  string // stdout, or for code 101 a substring of stderr
	one   bool
	check string
}

// syncLinuxCases are the sync programs as the Windows tests run them
// (with the cancel-ahead, parked-cancel, cancel-versus-
// handoff, Close and fairness programs, so cancellation of a parked wait
// runs through both Linux pollers' wakes).
func syncLinuxCases() []syncLinuxCase {
	plain := strings.Replace(strings.Replace(chanConserve, "linear struct Tok", "struct Tok", 1), "    // FREE\n", "", 1)
	linear := strings.Replace(chanConserve, "    // FREE\n", "    c.FreeEmpty(sys.arena.Alloc())\n", 1)
	cs := []syncLinuxCase{
		{name: "conserve-plain-cap0", src: plain, args: []string{"0", "3", "8", "1", "500"}, want: chanConserveWant(500)},
		{name: "conserve-plain-cap7-fifo", src: plain, args: []string{"7", "5", "1", "0", "500"}, want: chanConserveWant(500)},
		{name: "conserve-linear-cap0", src: linear, args: []string{"0", "2", "8", "1", "10000"}, want: chanConserveWant(10000)},
		{name: "conserve-linear-cap3", src: linear, args: []string{"3", "4", "8", "1", "10000"}, want: chanConserveWant(10000)},
		{name: "shared", src: syncSharedSrc, want: "16000\n"},
		{name: "mutex-contended", src: syncMutexContendedSrc, want: "160000\n"},
		{name: "atomic-contended", src: syncAtomicContendedSrc, want: "32016000 false\n"},
		{name: "std10-wait", src: syncSTD10WaitSrc, want: "true 7\n"},
		{name: "std12", src: syncSTD12Src, want: "5 1 9 1 16\n"},
		{name: "cancel-ahead", src: syncCancelAheadSrc, want: syncCancelAheadWant},
		{name: "chan-parked-cancel", src: syncChanParkedCancelSrc, want: "-1 -1 1\n"},
		{name: "mutex-parked-cancel", src: syncMutexParkedCancelSrc, want: "-1\n"},
		{name: "cancel-versus-handoff", src: syncCancelHandoffSrc, want: syncCancelHandoffWant, one: true},
		{name: "close-releases-parked", src: strings.Replace(syncCloseParkedSrc, "linear struct Tok", "struct Tok", 1), want: syncCloseParkedWant, one: true},
		{name: "rv5-buffered-in-cancelled-scope", src: syncRV5Src, want: syncRV5Want},
		{name: "rv1-no-starvation", src: syncStarveSrc, args: []string{fmt.Sprint(syncStarveN)}, check: "starve"},
	}
	for _, cap := range []string{"0", "1", "4"} {
		cs = append(cs,
			syncLinuxCase{name: "sweep-cap" + cap, src: syncSweepSrc, args: []string{cap, "0", syncSweepRounds}, check: "sweep"},
			syncLinuxCase{name: "sweep-parked-cap" + cap, src: syncSweepSrc, args: []string{cap, "1", syncSweepRounds}, check: "sweep"})
	}
	for _, cap := range []string{"0", "3"} {
		cs = append(cs, syncLinuxCase{name: "close-mid-traffic-cap" + cap, src: syncCloseMidSrc, args: []string{cap, "5"}, want: syncCloseMidWant()})
	}
	for i, p := range syncBenchContended {
		cs = append(cs, syncLinuxCase{name: fmt.Sprintf("bench-%d-%s", i, strings.Fields(p.name)[0]), src: p.src, want: p.want})
	}
	return cs
}

// syncLinuxRun is one run of a case in the generated script: carriers,
// seed (MK_TEST_SEED) and I/O backend.
type syncLinuxRun struct {
	n, seed int
	io      string
}

// syncLinuxRuns are a case's runs: one carrier with seed 1, and unless
// the case is one-carrier only, 8 carriers over seeds 1 to syncSeeds();
// each under the default backend and MK_IO=epoll.
func syncLinuxRuns(c syncLinuxCase) []syncLinuxRun {
	var rs []syncLinuxRun
	for _, io := range []string{"auto", "epoll"} {
		rs = append(rs, syncLinuxRun{1, 1, io})
		if !c.one {
			for seed := 1; seed <= syncSeeds(); seed++ {
				rs = append(rs, syncLinuxRun{8, seed, io})
			}
		}
	}
	return rs
}

// syncWSL is whether `wsl.exe -e true` answered within 30 s, asked once
// per test binary. Only TestSTDSYNC_Linux starts wsl.exe here, one client
// per profile; go test of package std runs under tools/wsl-lock.ps1 with
// -p 1, as parallel WSL clients can hang WSL. As rtsNeedWSL: a missing or silent WSL skips the
// test naming the reason ("Linux not verified"), and fails under MK_GATE=1.
var syncWSL struct {
	asked bool
	why   string
}

func syncNeedWSL(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "windows" {
		t.Skip("the Linux leg runs from Windows through wsl.exe")
	}
	linuxGate(t)
	if !syncWSL.asked {
		syncWSL.asked = true
		if _, err := exec.LookPath("wsl.exe"); err != nil {
			syncWSL.why = "wsl.exe not found (Linux not verified)"
		} else {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			wslGuard()
			if err := exec.CommandContext(ctx, "wsl.exe", "-e", "true").Run(); err != nil {
				syncWSL.why = fmt.Sprintf("`wsl.exe -e true` did not answer within 30 s: %v (Linux not verified)", err)
			}
		}
	}
	if syncWSL.why != "" {
		if os.Getenv("MK_GATE") == "1" {
			t.Fatalf("needs WSL: %s", syncWSL.why)
		}
		t.Skip(syncWSL.why)
	}
}

// syncWSLPath is the /mnt form of a Windows path.
func syncWSLPath(p string) string {
	p = filepath.ToSlash(p)
	if len(p) > 2 && p[1] == ':' {
		return "/mnt/" + strings.ToLower(p[:1]) + p[2:]
	}
	return p
}

// TestSTDSYNC_Linux cross-builds every syncLinuxCases program for
// x86_64-linux, dev and release, and runs each in WSL with 1 and 8
// carriers, with the default I/O backend (io_uring where the kernel has
// it) and MK_IO=epoll, under `timeout 120`; one shell script per profile,
// so one wsl.exe client at a time. Not parallel: it is the only test here
// that starts wsl.exe.
func TestSTDSYNC_Linux(t *testing.T) {
	syncNeedWSL(t)
	cases := syncLinuxCases()
	for _, profile := range []string{"dev", "release"} {
		flags := []string{"--target", "x86_64-linux"}
		if profile == "release" {
			flags = append(flags, "--release")
		}
		dir := t.TempDir()
		var sh strings.Builder
		sh.WriteString("#!/bin/sh\ncd \"$(dirname \"$0\")\"\n")
		exes := map[string]string{}
		for i, c := range cases {
			exe, ok := exes[c.src]
			if !ok {
				built := buildSync(t, c.src, flags...)
				exe = fmt.Sprintf("p%d", i)
				b, err := os.ReadFile(built)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, exe), b, 0o755); err != nil {
					t.Fatal(err)
				}
				exes[c.src] = exe
			}
			for _, r := range syncLinuxRuns(c) {
				tag := fmt.Sprintf("%d-%d-%d-%s", i, r.n, r.seed, r.io)
				envIo := ""
				if r.io == "epoll" {
					envIo = "MK_IO=epoll "
				}
				fmt.Fprintf(&sh, "chmod +x ./%s; %sMK_CARRIERS=%d MK_TEST_SEED=%d timeout 120 ./%s %s >out-%s.txt 2>err-%s.txt; echo \"%s $?\"\n",
					exe, envIo, r.n, r.seed, exe, strings.Join(c.args, " "), tag, tag, tag)
			}
		}
		script := filepath.Join(dir, "run.sh")
		if err := os.WriteFile(script, []byte(sh.String()), 0o755); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		wslGuard()
		cmd := exec.CommandContext(ctx, "wsl.exe", "-e", "sh", syncWSLPath(script))
		cmd.WaitDelay = 10 * time.Second
		out, err := cmd.Output()
		cancel()
		if err != nil {
			t.Fatalf("%s: wsl.exe: %v\n%s", profile, err, out)
		}
		got := map[string]string{}
		for _, line := range strings.Split(strings.TrimSpace(strings.ReplaceAll(string(out), "\r\n", "\n")), "\n") {
			if f := strings.Fields(line); len(f) == 2 {
				got[f[0]] = f[1]
			}
		}
		for i, c := range cases {
			outs := map[string]string{}
			for _, r := range syncLinuxRuns(c) {
				tag := fmt.Sprintf("%d-%d-%d-%s", i, r.n, r.seed, r.io)
				t.Run(fmt.Sprintf("%s/%s/carriers=%d/seed=%d/io=%s", profile, c.name, r.n, r.seed, r.io), func(t *testing.T) {
					st, ok := got[tag]
					if !ok {
						t.Fatal("no result line")
					}
					o, _ := os.ReadFile(filepath.Join(dir, "out-"+tag+".txt"))
					e, _ := os.ReadFile(filepath.Join(dir, "err-"+tag+".txt"))
					if st == "124" {
						t.Fatalf("no exit within 120 s (a lost wakeup?)\nstdout:\n%s\nstderr:\n%s", o, e)
					}
					if st != fmt.Sprint(c.code) {
						t.Fatalf("exit %s, want %d\nstdout:\n%s\nstderr:\n%s", st, c.code, o, e)
					}
					switch c.check {
					case "sweep":
						if !strings.HasPrefix(string(o), "0 ") {
							t.Fatalf("stdout %q: want 0 failed rounds", o)
						}
					case "starve":
						checkStarve(t, []string{tag}, nativeRun{stdout: string(o), stderr: string(e)})
					default:
						if string(o) != c.want {
							t.Fatalf("stdout %q, want %q\nstderr:\n%s", o, c.want, e)
						}
						// Both backends give the same output for the same run.
						k := fmt.Sprintf("%d-%d", r.n, r.seed)
						if prev, ok := outs[k]; ok && prev != string(o) {
							t.Fatalf("epoll and the default backend differ: %q and %q", prev, o)
						}
						outs[k] = string(o)
					}
				})
			}
		}
	}
}

// syncCancelAheadSrc: a waiter cancelled while parked ahead of another
// leaves the other served (C3). Mutex: W1 (scope s, cancelled) and W2
// (scope r, not cancelled) park on the lock Main holds; s is cancelled, W1
// returns Canceled, Main unlocks, and W2 must get the lock. Receive: R1 (s)
// and R2 (r) park on a rendezvous channel in that order; s is cancelled,
// and Main's Send must reach R2. Send: S1 (s) and S2 (r) park on a full
// capacity-1 channel; s is cancelled, S1 keeps its value, and Main
// receives the buffered value, then S2's. Output: "w1 w2 r1 r2 s1 x y".
const syncCancelAheadSrc = `import std.atomic

Locker(m: &Mutex[int], io: Io, r: &atomic.AtomicInt) {
    var g := m.Lock(io) else {
        r.Store(-1)
        return
    }
    *g.Mut() += 1
    g.Unlock()
    r.Store(1)
}

Getter(c: &Chan[int], io: Io, r: &atomic.AtomicInt) {
    x := c.Recv(io) else {
        r.Store(-1)
        return
    }
    r.Store(i64(x))
}

Putter(c: &Chan[int], io: Io, v: int, r: &atomic.AtomicInt) {
    c.Send(io, v) else {
        r.Store(-1)
        return
    }
    r.Store(1)
}

Settle(io: Io) {
    for _ in 0..20 {
        io.Yield() else {}
    }
}

export Main(sys: &System): Canceled|Closed|OutOfMemory|WriteError!void {
    io := sys.io
    m := Mutex[int].New(0)
    w1 := atomic.AtomicInt.New(0)
    w2 := atomic.AtomicInt.New(0)
    var g := try m.Lock(io)
    scope r := io.Scope() {
        scope s := io.Scope() {
            s.go Locker(&m, io, &w1)
            Settle(io)
            r.go Locker(&m, io, &w2)
            Settle(io)
            s.Cancel()
        }
        g.Unlock()
    }
    c := try Chan[int].New(sys.arena.Alloc(), 0)
    r1 := atomic.AtomicInt.New(0)
    r2 := atomic.AtomicInt.New(0)
    scope r := io.Scope() {
        scope s := io.Scope() {
            s.go Getter(&c, io, &r1)
            Settle(io)
            r.go Getter(&c, io, &r2)
            Settle(io)
            s.Cancel()
        }
        try c.Send(io, 5)
    }
    f := try Chan[int].New(sys.arena.Alloc(), 1)
    try f.Send(io, 1)
    s1 := atomic.AtomicInt.New(0)
    s2 := atomic.AtomicInt.New(0)
    var x := 0
    var y := 0
    scope r := io.Scope() {
        scope s := io.Scope() {
            s.go Putter(&f, io, 7, &s1)
            Settle(io)
            r.go Putter(&f, io, 9, &s2)
            Settle(io)
            s.Cancel()
        }
        x = try f.Recv(io)
        y = try f.Recv(io)
    }
    var out := sys.Stdout()
    try Print(mut out, "{a} {b} {c} {d} {e} {x} {y} {n}\n", (a: w1.Load(), b: w2.Load(), c: r1.Load(), d: r2.Load(), e: s1.Load(), x, y, n: m.Into()))
}
`

// syncCancelAheadWant is syncCancelAheadSrc's output on every schedule.
const syncCancelAheadWant = "-1 1 -1 5 -1 1 9 1\n"

// TestSTDSYNC_CancelAheadOfWaiter runs syncCancelAheadSrc on one carrier
// (the order of the parks is the program's) and on 8 carriers with jitter,
// where the Settle yields make that order likely but not certain, so the
// result is the same either way; dev and release.
func TestSTDSYNC_CancelAheadOfWaiter(t *testing.T) {
	t.Parallel()
	const want = syncCancelAheadWant
	for _, flags := range [][]string{nil, {"--release"}} {
		exe := buildSync(t, syncCancelAheadSrc, flags...)
		wantSync(t, exe, 0, want)
		for seed := 1; seed <= syncSeeds(); seed++ {
			wantSyncEnv(t, exe, syncStressEnv(8, seed), 0, want)
		}
	}
}
