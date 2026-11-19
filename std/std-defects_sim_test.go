// Why: std.sim must schedule the main task like any other, and give one run per seed.
// What: regression tests for std.sim defects found by programs written on Mk.
// How: each test (named after the defect's id) builds and runs a minimal repro
// under a Sim and checks its output.

package std

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// raft01Src is a repro: the main task hands each value to a task
// it spawned and waits for the answer, on the native Io and on a Sim's.
const raft01Src = `import std.sim

Echo(io: Io, inbox: &Chan[int], outbox: &Chan[int]) {
    for _ in 0..3 {
        x := inbox.Recv(io) else {
            return
        }
        outbox.Send(io, x * 10) else {
            return
        }
    }
}

Run(sys: &System, io: Io): OutOfMemory|Canceled|Closed|WriteError!void {
    a := sys.heap.Alloc()
    inbox := try Chan[int].New(a, 1)
    defer inbox.Free(a)
    outbox := try Chan[int].New(a, 1)
    defer outbox.Free(a)
    var total := 0
    scope s := io.Scope() {
        s.go Echo(io, &inbox, &outbox)
        for i in 1..4 {
            try inbox.Send(io, i)
            total += try outbox.Recv(io)
        }
    }
    try sys.Print("total {total}\n", (total))
}

export Main(sys: &System): OutOfMemory|Canceled|Closed|WriteError!void {
    try sys.Print("sys.io: ")
    try Run(sys, sys.io)
    var sm := try sim.Sim.New(sys.arena, 1)
    defer sm.Free()
    try sys.Print("sim: ")
    try Run(sys, sm.Io())
}
`

// stuckSrc is a true deadlock with a live task: the main task waits on
// outbox while the run's one task waits on inbox, and nobody sends. Both
// waits answer Canceled at once, and the scope joins.
const stuckSrc = `import std.sim

Wait(io: Io, inbox: &Chan[int]) {
    _ = inbox.Recv(io) else {
        return
    }
}

export Main(sys: &System): OutOfMemory|Canceled|Closed|WriteError!void {
    var sm := try sim.Sim.New(sys.arena, 1)
    defer sm.Free()
    io := sm.Io()
    a := sys.heap.Alloc()
    inbox := try Chan[int].New(a, 1)
    defer inbox.Free(a)
    outbox := try Chan[int].New(a, 1)
    defer outbox.Free(a)
    scope s := io.Scope() {
        s.go Wait(io, &inbox)
        _ = outbox.Recv(io) else e {
            try sys.Print("main: {e}\n", (e))
        }
    }
    try sys.Print("joined\n")
}
`

// A handoff from the main task to a task it spawned is no
// deadlock under a Sim: a main task that a wake returned to owes the run a
// note, so no choice (and no deadlock) is made before its next leaf. One
// carrier and several; and a true deadlock with a live task still answers
// Canceled at once instead of hanging.
func TestMainHandoffUnderSim(t *testing.T) {
	for _, n := range []string{"1", "4", "16"} {
		t.Run("carriers="+n, func(t *testing.T) {
			t.Setenv("MK_CARRIERS", n)
			dfWant(t, map[string]string{"main.mk": raft01Src}, "sys.io: total 60\nsim: total 60\n")
			dfWant(t, map[string]string{"main.mk": stuckSrc}, "main: Canceled\njoined\n")
		})
	}
}

// raft06Src is a repro: a Recorder over a Sim of seed 7, one
// Bind and its Close; it prints the log's digest.
const raft06Src = `import std.hash
import std.net
import std.sim

export Main(sys: &System): OutOfMemory|net.Error|WriteError!void {
    var sm := try sim.Sim.New(sys.arena, 7)
    defer sm.Free()
    rec := try sim.Recorder.New(sm.Io())
    defer rec.Free()
    d := try net.Bind(rec.Io(), "127.0.0.1:7001")
    d.Close()
    e := try net.Bind(rec.Io(), "127.0.0.1:7002")
    e.Close()
    log := try rec.Log(sys.arena)
    try sys.Print("log {n} bytes, digest {x:016x}\n", (n: log.Len(), x: hash.Xx(log)))
    var rep := try sim.Replayer.New(log)
    defer rep.Free()
    f := try net.Bind(rep.Io(), "127.0.0.1:7001")
    f.Close()
    g := try net.Bind(rep.Io(), "127.0.0.1:7002")
    g.Close()
    try sys.Print("diverged {d} done {o}\n", (d: rep.Diverged(), o: rep.Done()))
}
`

// A recording is a function of the seed: a handle is logged as
// its number in the recording, not as the runtime record's address, so
// three processes write the same bytes, and the log still replays.
func TestRecordingSameInEveryProcess(t *testing.T) {
	stdDir, err := filepath.Abs(".")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.mk"), []byte(raft06Src), 0o644); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(dir, "r6.exe")
	out, status := mkCmd(t, dir, stdDir, "build", "-o", exe, "main.mk")
	if strings.Contains(out, "no usable LLVM") {
		t.Skipf("needs the LLVM toolchain: %s", out)
	}
	if status != 0 {
		t.Fatalf("mk build: exit %d\n%s", status, out)
	}
	var first string
	for i := 0; i < 3; i++ {
		b, err := exec.Command(exe).CombinedOutput()
		got := strings.ReplaceAll(string(b), "\r\n", "\n")
		if err != nil || !strings.HasSuffix(got, "diverged false done true\n") {
			t.Fatalf("run %d: %v\n%s", i, err, got)
		}
		if i == 0 {
			first = got
		} else if got != first {
			t.Errorf("run %d's recording differs from run 0's:\n%s\nagainst\n%s", i, got, first)
		}
	}
}

// kvd08Src is a repro: tasks spawned into the outer scope, a
// Mutex and a Chan under a Sim, while the scope body (the main task) waits
// on the Chan.
const kvd08Src = `
import std.net
import std.sim
import std.time

Accept(s: &Scope, l: &net.Listener, io: Io, m: &Mutex[int] into s, c: &Chan[int] into s) {
    for {
        conn := l.Accept(io) else {
            return
        }
        s.go Handle(conn, io, m, c)
    }
}

Handle(conn: net.Stream, io: Io, m: &Mutex[int], c: &Chan[int]) {
    defer conn.Close()
    for {
        var buf: [16]u8 = [0; 16]
        n := conn.Read(io, mut buf[..]) else {
            return
        }
        if n == 0 {
            return
        }
        var g := m.Lock(io) else {
            return
        }
        *g.Mut() += 1
        g.Unlock()
        c.Send(io.NoCancel(), n) else { }
        conn.WriteAll(io, buf[..n]) else {
            return
        }
    }
}

Ping(io: Io, addr: str) {
    c := net.Dial(io, addr) else {
        return
    }
    defer c.Close()
    for _ in 0..5 {
        c.WriteAll(io, b"ping") else {
            return
        }
        var buf: [16]u8 = [0; 16]
        _ = c.Read(io, mut buf[..]) else { }
    }
}

Pings(s: &Scope, io: Io, addr: str) {
    scope p := io.Scope() {
        for _ in 0..4 {
            p.go Ping(io, addr)
        }
    }
    s.Cancel()
}

export Main(sys: &System): net.Error|OutOfMemory|WriteError!void {
    var w := try sim.Sim.New(sys.arena, 1)
    defer w.Free()
    io := w.Io()
    a := sys.heap.Alloc()
    c := try Chan[int].New(a, 256)
    defer c.Free(a)
    m := Mutex.New(0)
    t0 := time.Now(io)
    l := try net.Listen(io, "127.0.0.1:0")
    defer l.Close()
    addr := try sys.arena.Fmt("127.0.0.1:{p}", (p: l.Port()))
    var order: i64 = 0
    scope s := io.Scope() {
        s.go Accept(&s, &l, io, &m, &c)
        s.go Pings(&s, io, addr)
        for {
            n := c.Recv(io) else {
                break
            }
            order = order * 31 + i64(time.Now(io).Since(t0).Nanos() % 1000) + i64(n)
            order = order % 1000000007
        }
    }
    try sys.Print("simulated time {n} ns, order {order}\n", (n: time.Now(io).Since(t0).Nanos(), order))
}
`

// clockRaceSrc: a task sends on a channel and then reads the Sim's clock
// fifty times, while the main task, woken by each send, reads the clock
// too. A woken main used to run beside the sending task, so the clock
// values it read depended on how the two threads interleaved.
const clockRaceSrc = `import std.sim
import std.time

Worker(io: Io, ch: &Chan[int]) {
    for i in 0..40 {
        ch.Send(io, i) else {
            return
        }
        for _ in 0..50 {
            _ = time.Now(io)
        }
    }
}

export Main(sys: &System): OutOfMemory|Canceled|Closed|WriteError!void {
    var sm := try sim.Sim.New(sys.arena, 1)
    defer sm.Free()
    io := sm.Io()
    a := sys.heap.Alloc()
    ch := try Chan[int].New(a, 1)
    defer ch.Free(a)
    t0 := time.Now(io)
    var acc: i64 = 0
    scope s := io.Scope() {
        s.go Worker(io, &ch)
        for _ in 0..40 {
            x := try ch.Recv(io)
            acc = (acc * 31 + time.Now(io).Since(t0).Nanos() / 1000 + i64(x)) % 1000000007
        }
    }
    try sys.Print("acc {acc}\n", (acc))
}
`

// simBuild builds src as main.mk once and returns the executable.
func simBuild(t *testing.T, src string) string {
	t.Helper()
	stdDir, err := filepath.Abs(".")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.mk"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(dir, "sim.exe")
	out, status := mkCmd(t, dir, stdDir, "build", "-o", exe, "main.mk")
	if strings.Contains(out, "no usable LLVM") {
		t.Skipf("needs the LLVM toolchain: %s", out)
	}
	if status != 0 {
		t.Fatalf("mk build: exit %d\n%s", status, out)
	}
	return exe
}

// simRuns runs exe n times at the given carrier count and fails unless
// every run prints want (or, with want empty, what the first run prints,
// which must start with prefix); it returns that output.
func simRuns(t *testing.T, exe, carriers string, n int, prefix, want string) string {
	t.Helper()
	for i := 0; i < n; i++ {
		got, code := cfaultRun(t, exe, "MK_CARRIERS="+carriers)
		if code != 0 || !strings.HasPrefix(got, prefix) {
			t.Fatalf("carriers %s, run %d: exit %d\n%s", carriers, i, code, got)
		}
		if want == "" {
			want = got
		} else if got != want {
			t.Errorf("carriers %s, run %d printed %q, want %q", carriers, i, got, want)
		}
	}
	return want
}

// One seed gives one run whatever the carrier count. The main
// task's Recv used to race the scheduler's choice (a wake let main run
// while a task's block was decided without it), so runs above one carrier
// differed; the main task now owes the run a note once a wake returns it
// (the fix TestMainHandoffUnderSim pins), and it resumes only once the task that woke it
// reaches its next wait, so it never reads the clock beside it. Each
// program prints at 2 and 16 carriers what it prints at 1.
func TestSimSameRunAtManyCarriers(t *testing.T) {
	k8 := simBuild(t, kvd08Src)
	want := simRuns(t, k8, "1", 1, "simulated time ", "")
	simRuns(t, k8, "16", 5, "simulated time ", want)
	clock := simBuild(t, clockRaceSrc)
	want = simRuns(t, clock, "1", 1, "acc ", "")
	simRuns(t, clock, "2", 20, "acc ", want)
	simRuns(t, clock, "16", 20, "acc ", want)
}

// chat04Src is a repro: a server task accepts and writes, and
// the main task dials and reads, under a Sim.
const chat04Src = `
import std.atomic
import std.net
import std.sim

// The server task writes first; the client only reads.
Greet(l: &net.Listener, io: Io, stage: &atomic.AtomicInt) {
    conn := l.Accept(io) else {
        stage.Store(-1)
        return
    }
    defer conn.Close()
    conn.WriteAll(io, b"hi") else e {
        code := match e {
            net.Reset { -42 }
            Canceled { -43 }
            _ { -49 }
        }
        stage.Store(i64(code))
        return
    }
    stage.Store(4)
}

Run(a: &Arena, seed: u64, stage: &atomic.AtomicInt): net.Error|OutOfMemory!int {
    var s := try sim.Sim.New(a, seed)
    defer s.Free()
    io := s.Io()
    l := try net.Listen(io, "127.0.0.1:7123")
    defer l.Close()
    var got := 0
    scope t := io.Scope() {
        t.go Greet(&l, io, stage)
        c := net.Dial(io, "127.0.0.1:7123") else {
            t.Cancel()
            return -2
        }
        defer c.Close()
        var buf: [8]u8 = [0; 8]
        got = c.Read(io, mut buf[..]) else e {
            match e {
                net.Reset { -22 }
                Canceled { -23 }
                _ { -29 }
            }
        }
    }
    return got
}

export Main(sys: &System): net.Error|OutOfMemory|WriteError!void {
    for seed in 0..3 {
        stage := atomic.AtomicInt.New(0)
        n := Run(sys.arena, Cast[u64](seed), &stage) else e {
            try sys.Print("seed {seed}: {e}\n", (seed, e))
            continue
        }
        try sys.Print("seed {seed}: client read {n}, server stage {st}\n", (seed, n, st: stage.Load()))
    }
}
`

// netStuckSrc: main reads a connection whose server task reads too, and
// nobody writes: a deadlock, answered at once with Canceled on both sides.
const netStuckSrc = `import std.net
import std.sim

Serve(l: &net.Listener, io: Io) {
    conn := l.Accept(io) else {
        return
    }
    defer conn.Close()
    var buf: [8]u8 = [0; 8]
    _ = conn.Read(io, mut buf[..]) else {
        return
    }
}

export Main(sys: &System): net.Error|OutOfMemory|WriteError!void {
    var s := try sim.Sim.New(sys.arena, 3)
    defer s.Free()
    io := s.Io()
    l := try net.Listen(io, "127.0.0.1:7124")
    defer l.Close()
    scope t := io.Scope() {
        t.go Serve(&l, io)
        c := try net.Dial(io, "127.0.0.1:7124")
        defer c.Close()
        var buf: [8]u8 = [0; 8]
        _ = c.Read(io, mut buf[..]) else e {
            try sys.Print("main: {e}\n", (e))
        }
    }
    try sys.Print("joined\n")
}
`

// Under a Sim, the main task's Read on a dialled stream waits
// for the server task's write (a native wait the run's network events
// wake), instead of answering a deadlock's Canceled at once and closing the
// stream under the writer, whose write then failed with Reset. A true
// deadlock still answers Canceled.
func TestMainReadWaitsForTheServer(t *testing.T) {
	want := "seed 0: client read 2, server stage 4\nseed 1: client read 2, server stage 4\nseed 2: client read 2, server stage 4\n"
	dfWant(t, map[string]string{"main.mk": chat04Src}, want)
	dfWant(t, map[string]string{"main.mk": netStuckSrc}, "main: Canceled\njoined\n")
}

// chatAcceptSrc: the main task accepts a connection that a task it
// spawned dials, then reads what that task writes, under a Sim.
const chatAcceptSrc = `import std.net
import std.sim

Client(io: Io) {
    c := net.Dial(io, "127.0.0.1:7200") else {
        return
    }
    defer c.Close()
    c.WriteAll(io, b"hi") else {
        return
    }
}

export Main(sys: &System): net.Error|OutOfMemory|WriteError!void {
    var s := try sim.Sim.New(sys.arena, 1)
    defer s.Free()
    io := s.Io()
    l := try net.Listen(io, "127.0.0.1:7200")
    defer l.Close()
    scope t := io.Scope() {
        t.go Client(io)
        conn := l.Accept(io) else e {
            try sys.Print("main accept: {e}\n", (e))
            return
        }
        defer conn.Close()
        var buf: [8]u8 = [0; 8]
        n := conn.Read(io, mut buf[..]) else e {
            try sys.Print("main read: {e}\n", (e))
            return
        }
        try sys.Print("main read {n} bytes\n", (n))
    }
}
`

// chatRecvFromSrc: the main task receives a datagram that a task it
// spawned sends, under a Sim.
const chatRecvFromSrc = `import std.net
import std.sim

Sender(io: Io, to: net.Addr) {
    d := net.Bind(io, "127.0.0.1:7301") else {
        return
    }
    defer d.Close()
    d.Send(io, b"hi", to) else {
        return
    }
}

export Main(sys: &System): net.Error|OutOfMemory|WriteError!void {
    var s := try sim.Sim.New(sys.arena, 1)
    defer s.Free()
    io := s.Io()
    d := try net.Bind(io, "127.0.0.1:7300")
    defer d.Close()
    to := try net.ParseAddr("127.0.0.1:7300")
    scope t := io.Scope() {
        t.go Sender(io, to)
        var buf: [8]u8 = [0; 8]
        got := d.Recv(io, mut buf[..]) else e {
            try sys.Print("main recv: {e}\n", (e))
            return
        }
        try sys.Print("main got {n} bytes\n", (n: got.n))
    }
}
`

// acceptStuckSrc: true deadlocks of the main task's Accept and Recv: with
// no task in the run, and with a live task that never dials or sends.
// Each answers Canceled at once, and each scope joins.
const acceptStuckSrc = `import std.net
import std.sim

Wait(io: Io, inbox: &Chan[int]) {
    _ = inbox.Recv(io) else {
        return
    }
}

TryAccept(sys: &System, io: Io, l: &net.Listener, who: str): WriteError!void {
    c := l.Accept(io) else e {
        try sys.Print("{who} accept: {e}\n", (who, e))
        return
    }
    c.Close()
}

export Main(sys: &System): net.Error|OutOfMemory|WriteError!void {
    var s := try sim.Sim.New(sys.arena, 1)
    defer s.Free()
    io := s.Io()
    a := sys.heap.Alloc()
    inbox := try Chan[int].New(a, 1)
    defer inbox.Free(a)
    l := try net.Listen(io, "127.0.0.1:7201")
    defer l.Close()
    d := try net.Bind(io, "127.0.0.1:7302")
    defer d.Close()
    try TryAccept(sys, io, &l, "alone")
    scope t := io.Scope() {
        t.go Wait(io, &inbox)
        try TryAccept(sys, io, &l, "main")
    }
    scope t := io.Scope() {
        t.go Wait(io, &inbox)
        var buf: [8]u8 = [0; 8]
        _ = d.Recv(io, mut buf[..]) else e {
            try sys.Print("main recv: {e}\n", (e))
        }
    }
    try sys.Print("joined\n")
}
`

// The accept and datagram forms: under a Sim, the main task's
// Listener.Accept and Datagram.Recv wait for a task of the run that is
// about to dial or send, as its stream Read does, instead of answering a
// deadlock's Canceled at once. True deadlocks still answer Canceled.
func TestMainAcceptAndRecvFromWait(t *testing.T) {
	for _, n := range []string{"1", "4", "16"} {
		t.Run("carriers="+n, func(t *testing.T) {
			t.Setenv("MK_CARRIERS", n)
			dfWant(t, map[string]string{"main.mk": chatAcceptSrc}, "main read 2 bytes\n")
			dfWant(t, map[string]string{"main.mk": chatRecvFromSrc}, "main got 2 bytes\n")
			dfWant(t, map[string]string{"main.mk": acceptStuckSrc},
				"alone accept: Canceled\nmain accept: Canceled\nmain recv: Canceled\njoined\n")
		})
	}
}

// pollSrc: a task sends to the main task, sleeps 5 ms and sets a flag,
// while main, woken by the send, polls the flag with the Sim's Sleep or
// Yield (POLL), with no bound on the loop.
const pollSrc = `import std.atomic
import std.sim
import std.time

Worker(io: Io, ch: &Chan[int], flag: &atomic.AtomicInt) {
    ch.Send(io, 1) else {
        return
    }
    io.Sleep(time.Millis(5)) else { }
    flag.Store(1)
}

export Main(sys: &System): OutOfMemory|Canceled|Closed|WriteError!void {
    var sm := try sim.Sim.New(sys.arena, 1)
    defer sm.Free()
    io := sm.Io()
    a := sys.heap.Alloc()
    ch := try Chan[int].New(a, 1)
    defer ch.Free(a)
    flag := atomic.AtomicInt.New(0)
    var polls := 0
    scope s := io.Scope() {
        s.go Worker(io, &ch, &flag)
        _ = try ch.Recv(io)
        for flag.Load() == 0 {
            POLL else { }
            polls += 1
        }
    }
    try sys.Print("polls {polls} flag {f}\n", (polls, f: flag.Load()))
}
`

// pollBirthSrc: main polls with Sleep right after the spawn, before any
// other wait.
const pollBirthSrc = `import std.atomic
import std.sim
import std.time

Worker(io: Io, flag: &atomic.AtomicInt) {
    io.Sleep(time.Millis(5)) else { }
    flag.Store(1)
}

export Main(sys: &System): OutOfMemory|Canceled|WriteError!void {
    var sm := try sim.Sim.New(sys.arena, 1)
    defer sm.Free()
    io := sm.Io()
    flag := atomic.AtomicInt.New(0)
    var polls := 0
    scope s := io.Scope() {
        s.go Worker(io, &flag)
        for flag.Load() == 0 {
            io.Sleep(time.Millis(1)) else { }
            polls += 1
        }
    }
    try sys.Print("polls {polls} flag {f}\n", (polls, f: flag.Load()))
}
`

// spinSrc: a task yields in a loop until main, sleeping 1 ms, stops it: the
// sleeping main is not starved by a task that is always ready, and the
// task runs while main sleeps.
const spinSrc = `import std.atomic
import std.sim
import std.time

Spin(io: Io, stop: &atomic.AtomicInt, n: &atomic.AtomicInt) {
    for stop.Load() == 0 {
        io.Yield() else { }
        _ = n.Add(1)
    }
}

export Main(sys: &System): OutOfMemory|Canceled|WriteError!void {
    var sm := try sim.Sim.New(sys.arena, 1)
    defer sm.Free()
    io := sm.Io()
    stop := atomic.AtomicInt.New(0)
    n := atomic.AtomicInt.New(0)
    t0 := time.Now(io)
    scope s := io.Scope() {
        s.go Spin(io, &stop, &n)
        io.Sleep(time.Millis(1)) else { }
        stop.Store(1)
    }
    try sys.Print("spins {k} after {us} us\n", (k: n.Load(), us: time.Now(io).Since(t0).Nanos() / 1000))
}
`

// The polling forms of the main-task handoff: the main task's Sleep and Yield on a
// Sim's Io are waits on the run, so a main that a wake made pending lets
// the run go on while it polls (it froze the run, and the clock passed a
// sleeper's deadline without waking it). A main Yield is a sleep of 1 us,
// so main polls 5,001 times while the task sleeps 5 ms; a sleeping main
// wakes at its deadline before the tasks then ready, so a task that
// yields forever does not starve it.
func TestMainSleepAndYieldAreWaits(t *testing.T) {
	sleep := strings.Replace(pollSrc, "POLL", "io.Sleep(time.Millis(1))", 1)
	yield := strings.Replace(pollSrc, "POLL", "io.Yield()", 1)
	for _, n := range []string{"1", "4", "16"} {
		t.Run("carriers="+n, func(t *testing.T) {
			t.Setenv("MK_CARRIERS", n)
			dfWant(t, map[string]string{"main.mk": sleep}, "polls 6 flag 1\n")
			dfWant(t, map[string]string{"main.mk": yield}, "polls 5001 flag 1\n")
			dfWant(t, map[string]string{"main.mk": pollBirthSrc}, "polls 6 flag 1\n")
			dfWant(t, map[string]string{"main.mk": spinSrc}, "spins 1000 after 1001 us\n")
		})
	}
}
