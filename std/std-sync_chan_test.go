// Why: Chan and Mutex must keep their invariants under real scheduling, which
// only whole programs on the runtime exercise.
// What: Chan and Mutex tests: invariants C1 to C5 of sync_chan.mk, consume on
// success, cancel versus handoff, Close, Free, and sync_mutex.mk's lock word.
// How: builds Mk programs and forces interleavings with Yield on the
// cooperative scheduler; stress programs take a seed and vary the yields.

package std

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// syncChanLib is shared by the channel programs: helpers that turn a
// fallible call into a value, and a linear token whose loss or double
// delivery the counts would show.
const syncChanLib = `import std.atomic

linear struct Tok {
    s: int
    q: int
}

Take(t: Tok): int {
    Tok(s, q) := t
    return s * 100000 + q
}

RecvOr(c: &Chan[int], io: Io, d: int): int {
    x := c.Recv(io) else {
        return d
    }
    return x
}

SendOk(c: &Chan[int], io: Io, x: int): bool {
    c.Send(io, x) else {
        return false
    }
    return true
}

Rand(x: u64): u64 {
    var y := x
    y ^= y << 13
    y ^= y >> 7
    y ^= y << 17
    return y
}
`

// TestSTDSYNC_ChanCancelVersusHandoff: the cancel interleavings on one
// carrier. (1) A receiver parks at capacity 0, Main hands it 42, then
// cancels the scope before the receiver runs: the value is delivered (C4:
// the state the sender set wins over the cancel). (2) A sender parks, Main
// takes its 7 and cancels: the Send succeeds. (3) In a cancelled scope
// every Send and Recv is Canceled, whether or not it would wait (cancellation
// is checked before every Io operation with Canceled in its set): Send with
// room, Send on a full channel, Recv with nothing and Recv after a NoCancel
// Send buffered a value; the NoCancel Send succeeds and its value is there
// after the block.
func TestSTDSYNC_ChanCancelVersusHandoff(t *testing.T) {
	t.Parallel()
	for _, flags := range [][]string{nil, {"--release"}} {
		wantSync(t, buildSync(t, syncCancelHandoffSrc, flags...), 0, syncCancelHandoffWant)
	}
}

// syncCancelHandoffSrc is TestSTDSYNC_ChanCancelVersusHandoff's program.
const syncCancelHandoffSrc = syncChanLib + `
Receiver(c: &Chan[int], io: Io, got: &atomic.AtomicInt) {
    got.Store(i64(RecvOr(c, io, -1)))
}

Sender(c: &Chan[int], io: Io, res: &atomic.AtomicInt) {
    if SendOk(c, io, 7) {
        res.Store(1)
    } else {
        res.Store(-1)
    }
}

export Main(sys: &System): WriteError|OutOfMemory!void {
    var out := sys.Stdout()
    c := try Chan[int].New(sys.arena.Alloc(), 0)
    got := atomic.AtomicInt.New(0)
    res := atomic.AtomicInt.New(0)
    scope s := sys.io.Scope() {
        s.go Receiver(&c, sys.io, &got)
        sys.io.Yield() else {}
        _ = SendOk(&c, sys.io, 42)
        s.Cancel()
    }
    var x := 0
    scope s2 := sys.io.Scope() {
        s2.go Sender(&c, sys.io, &res)
        sys.io.Yield() else {}
        x = RecvOr(&c, sys.io, -1)
        s2.Cancel()
    }
    b := try Chan[int].New(sys.arena.Alloc(), 1)
    var r1 := false
    var r2 := true
    var r3 := 0
    var r4 := 0
    var r5 := false
    scope s3 := sys.io.Scope() {
        s3.Cancel()
        r1 = SendOk(&b, sys.io, 5)
        r3 = RecvOr(&b, sys.io, -1)
        r5 = SendOk(&b, sys.io.NoCancel(), 6)
        r2 = SendOk(&b, sys.io, 8)
        r4 = RecvOr(&b, sys.io, -1)
    }
    y := RecvOr(&b, sys.io, -1)
    try Print(mut out, "{g} {r} {x} {r1} {r2} {r3} {r4} {r5} {y}\n", (g: got.Load(), r: res.Load(), x, r1, r2, r3, r4, r5, y))
}
`

const syncCancelHandoffWant = "42 1 7 false false -1 -1 true 6\n"

// TestSTDSYNC_RecvBufferedInCancelledScope: a capacity-3 channel holds
// three values. In a cancelled scope every Recv with the plain Io is
// Canceled although values are buffered, and so is a Lock of a free Mutex
// and of a held one: cancellation is checked before every Io operation with
// Canceled in its error set, and keeps returning Canceled until the block
// exits. A NoCancel Io then drains 10 20 30.
func TestSTDSYNC_RecvBufferedInCancelledScope(t *testing.T) {
	t.Parallel()
	exe := buildSync(t, syncRV5Src)
	wantSync(t, exe, 0, syncRV5Want)
	for seed := 1; seed <= syncSeeds(); seed++ {
		wantSyncEnv(t, exe, syncStressEnv(8, seed), 0, syncRV5Want)
	}
}

// syncRV5Src is TestSTDSYNC_RecvBufferedInCancelledScope's program.
var syncRV5Src = strings.TrimPrefix(syncChanLib, "import std.atomic\n") + `
LockVal(m: &Mutex[int], io: Io): int {
    var g := m.Lock(io) else {
        return -1
    }
    *g.Mut() += 1
    v := *g.Ref()
    g.Unlock()
    return v
}

export Main(sys: &System): Canceled|Closed|WriteError|OutOfMemory!void {
    c := try Chan[int].New(sys.arena.Alloc(), 3)
    for i in 1..4 {
        try c.Send(sys.io, i * 10)
    }
    m := Mutex[int].New(0)
    held := Mutex[int].New(0)
    var hg := try held.Lock(sys.io)
    var a := 0
    var b := 0
    var d := 0
    var e := 0
    var free := 0
    var busy := 0
    var x1 := 0
    var x2 := 0
    var x3 := 0
    scope s := sys.io.Scope() {
        s.Cancel()
        a = RecvOr(&c, sys.io, -1)
        b = RecvOr(&c, sys.io, -1)
        d = RecvOr(&c, sys.io, -1)
        e = RecvOr(&c, sys.io, -1)
        free = LockVal(&m, sys.io)
        busy = LockVal(&held, sys.io)
        x1 = RecvOr(&c, sys.io.NoCancel(), -1)
        x2 = RecvOr(&c, sys.io.NoCancel(), -1)
        x3 = RecvOr(&c, sys.io.NoCancel(), -1)
    }
    hg.Unlock()
    var out := sys.Stdout()
    try Print(mut out, "{a} {b} {d} {e} {free} {busy} {x1} {x2} {x3}\n", (a, b, d, e, free, busy, x1, x2, x3))
}
`

const syncRV5Want = "-1 -1 -1 -1 -1 -1 10 20 30\n"

// chanConserveWant is chanConserve's output for 8 senders of m tokens.
func chanConserveWant(m int64) string {
	var ids int64
	for s := int64(0); s < 8; s++ {
		for q := int64(0); q < m; q++ {
			ids += s*100000 + q
		}
	}
	return fmt.Sprintf("%d %d false\n", 8*m, ids)
}

// chanConserve: S senders each send M linear tokens (s, q) and yield at
// seeded points; R receivers take them; the sender scope is cancelled
// after a seeded number of Main's yields when cancel is 1, and a sender
// whose Send fails keeps its token and counts it and the rest as kept.
// Every token is received once or kept once (C5): count + kept = S*M and
// the sums of their ids add up to the sum of all ids. With one receiver
// each sender's tokens arrive in order (FIFO, C1's queues).
const chanConserve = syncChanLib + `
Sender(c: &Chan[Tok], io: Io, id: int, seed: int, m: int, kept: &atomic.AtomicInt, kept_sum: &atomic.AtomicInt) {
    var x: u64 = Cast[u64](seed * 7919 + id + 1)
    for q in 0..m {
        t := Tok(s: id, q: q)
        c.Send(io, t) else {
            _ = kept_sum.Add(i64(Take(t)))
            _ = kept.Add(1)
            for r in q + 1..m {
                _ = kept_sum.Add(i64(id * 100000 + r))
                _ = kept.Add(1)
            }
            return
        }
        x = Rand(x)
        if x % 4 == 0 {
            io.Yield() else {}
        }
    }
}

Receiver(c: &Chan[Tok], io: Io, seed: int, n: &atomic.AtomicInt, sum: &atomic.AtomicInt, fifo: bool, bad: &atomic.AtomicBool) {
    var last := [-1; 8]
    var x: u64 = Cast[u64](seed + 77)
    for {
        t := c.Recv(io) else {
            return
        }
        Tok(s, q) := t
        if fifo {
            if q <= last[s] {
                bad.Store(true)
            }
            last[s] = q
        }
        _ = sum.Add(i64(s * 100000 + q))
        _ = n.Add(1)
        x = Rand(x)
        if x % 3 == 0 {
            io.Yield() else {}
        }
    }
}

export Main(sys: &System): ParseError|WriteError|OutOfMemory!void {
    cap := try sys.args[1].ParseInt()
    seed := int(try sys.args[2].ParseInt())
    recv := int(try sys.args[3].ParseInt())
    cancel := try sys.args[4].ParseInt()
    m := int(try sys.args[5].ParseInt())
    c := try Chan[Tok].New(sys.arena.Alloc(), int(cap))
    n := atomic.AtomicInt.New(0)
    sum := atomic.AtomicInt.New(0)
    kept := atomic.AtomicInt.New(0)
    kept_sum := atomic.AtomicInt.New(0)
    bad := atomic.AtomicBool.New(false)
    scope r := sys.io.Scope() {
        for _ in 0..recv {
            r.go Receiver(&c, sys.io, seed, &n, &sum, recv == 1, &bad)
        }
        scope s := sys.io.Scope() {
            for id in 0..8 {
                s.go Sender(&c, sys.io, id, seed, m, &kept, &kept_sum)
            }
            if cancel == 1 {
                for _ in 0..(seed % 50) * 20 {
                    sys.io.Yield() else {}
                }
                s.Cancel()
            }
        }
        c.Close()
    }
    // FREE
    var out := sys.Stdout()
    total := n.Load() + kept.Load()
    ids := sum.Load() + kept_sum.Load()
    try Print(mut out, "{total} {ids} {bad}\n", (total, ids, bad: bad.Load()))
}
`

// TestSTDSYNC_ChanValuesConserved: chanConserve with a plain token (the
// ?T cell of a plain T) over capacities 0, 1 and 7, one and eight
// receivers, with and without a cancel, 1 and 8 carriers with the
// runtime's jitter on, syncSeeds() seeds each, at dev and release.
func TestSTDSYNC_ChanValuesConserved(t *testing.T) {
	t.Parallel()
	want := chanConserveWant(500)
	plain := strings.Replace(strings.Replace(chanConserve, "linear struct Tok", "struct Tok", 1), "    // FREE\n", "", 1)
	for _, flags := range [][]string{nil, {"--release"}} {
		exe := buildSync(t, plain, flags...)
		for _, n := range syncCarriers {
			for _, cap := range []string{"0", "1", "7"} {
				for _, recv := range []string{"1", "8"} {
					for _, cancel := range []string{"0", "1"} {
						for seed := 1; seed <= syncSeeds(); seed++ {
							wantSyncEnv(t, exe, syncStressEnv(n, seed), 0, want, cap, fmt.Sprint(seed), recv, cancel, "500")
						}
					}
				}
			}
		}
	}
}

// TestSTDSYNC_ChanValuesConservedLinear: values are conserved at full
// scale: 8 senders and 8 receivers, 10,000 linear
// tokens each, a seeded cancel of the senders' scope, 1 and 8 carriers,
// capacities 0 and 3, and Chan.FreeEmpty at the end, so the compiler also proves
// each token consumed once on every path (C5).
func TestSTDSYNC_ChanValuesConservedLinear(t *testing.T) {
	t.Parallel()
	src := strings.Replace(chanConserve, "    // FREE\n", "    c.FreeEmpty(sys.arena.Alloc())\n", 1)
	want := chanConserveWant(10000)
	for _, flags := range [][]string{nil, {"--release"}} {
		exe := buildSync(t, src, flags...)
		for _, n := range syncCarriers {
			for _, cap := range []string{"0", "3"} {
				for seed := 1; seed <= syncSeeds(); seed++ {
					wantSyncEnv(t, exe, syncStressEnv(n, seed), 0, want, cap, fmt.Sprint(seed), "8", "1", "10000")
				}
			}
		}
	}
}

// TestSTDSYNC_ChanCloseReleasesParked: Close releases every parked waiter
// with Closed (C2). Two senders park on a full capacity-1 channel and keep
// their linear tokens; three receivers park on an empty one. The buffered
// value is still delivered after Close, then Closed; Close twice is one
// Close; Send after Close is Closed and keeps x.
func TestSTDSYNC_ChanCloseReleasesParked(t *testing.T) {
	t.Parallel()
	src := syncCloseParkedSrc
	// One sender fills the slot (which one is the scheduler's choice); the
	// other two park and get Closed, keeping their tokens, which each count
	// id*100000 + 1: the kept sum plus the delivered id plus 1 is 300003.
	// The plain form, then the linear one with the channel freed after the
	// block (a spawned loan ends with its scope).
	const want = syncCloseParkedWant
	wantSync(t, buildSync(t, strings.Replace(src, "linear struct Tok", "struct Tok", 1)), 0, want)
	lin := strings.Replace(src, "    late := SendOk", "    full.FreeEmpty(sys.arena.Alloc())\n    late := SendOk", 1)
	wantSync(t, buildSync(t, lin), 0, want)
}

// syncCloseParkedSrc is TestSTDSYNC_ChanCloseReleasesParked's program, with
// a linear Tok; syncCloseParkedWant its output.
const syncCloseParkedSrc = syncChanLib + `
Sender(c: &Chan[Tok], io: Io, id: int, kept: &atomic.AtomicInt) {
    t := Tok(s: id, q: 0)
    c.Send(io, t) else {
        _ = kept.Add(i64(Take(t)) + 1)
        return
    }
}

RecvTok(c: &Chan[Tok], io: Io): int {
    t := c.Recv(io) else {
        return -1
    }
    return Take(t)
}

Receiver(c: &Chan[int], io: Io, closed: &atomic.AtomicInt) {
    if RecvOr(c, io, -1) == -1 {
        _ = closed.Add(1)
    }
}

export Main(sys: &System): WriteError|OutOfMemory!void {
    empty := try Chan[int].New(sys.arena.Alloc(), 4)
    full := try Chan[Tok].New(sys.arena.Alloc(), 1)
    kept := atomic.AtomicInt.New(0)
    closed := atomic.AtomicInt.New(0)
    var first := 0
    var after := 0
    scope s := sys.io.Scope() {
        for id in 0..3 {
            s.go Sender(&full, sys.io, id, &kept)
            s.go Receiver(&empty, sys.io, &closed)
        }
        for _ in 0..10 {
            sys.io.Yield() else {}
        }
        full.Close()
        full.Close()
        empty.Close()
    }
    first = RecvTok(&full, sys.io)
    after = RecvTok(&full, sys.io)
    late := SendOk(&empty, sys.io, 1)
    var out := sys.Stdout()
    try Print(mut out, "{all} {nk} {c} {after} {late}\n", (all: kept.Load() + i64(first) + 1, nk: kept.Load() % 100000, c: closed.Load(), after, late))
}
`

const syncCloseParkedWant = "300003 2 3 -1 false\n"

// TestSTDSYNC_ChanFree: plain buffered values are dropped; the
// wrong allocator panics with code 12; a channel of a linear T that is not
// Free is freed with FreeEmpty (Free is E0701), which panics with
// code 11 while a value is buffered and frees an empty one.
func TestSTDSYNC_ChanFree(t *testing.T) {
	t.Parallel()
	exe := buildSync(t, strings.TrimPrefix(syncChanLib, "import std.atomic\n")+`
export Main(sys: &System): ParseError|OutOfMemory!void {
    k := try sys.args[1].ParseInt()
    a := sys.heap.Alloc()
    if k == 0 {
        c := try Chan[int].New(a, 4)
        _ = SendOk(&c, sys.io, 1)
        _ = SendOk(&c, sys.io, 2)
        c.Free(a)
    } else if k == 1 {
        c := try Chan[int].New(a, 4)
        c.Free(sys.arena.Alloc())
    } else if k == 2 {
        c := try Chan[Tok].New(a, 4)
        t := Tok(s: 1, q: 1)
        c.Send(sys.io, t) else {
            _ = Take(t)
        }
        c.FreeEmpty(a)
    } else {
        c := try Chan[Tok].New(a, 0)
        c.FreeEmpty(a)
    }
}
`)
	wantSync(t, exe, 0, "", "0")
	wantPanic(t, exe, "Chan.Free: wrong allocator", "1")
	wantPanic(t, exe, "Chan.FreeEmpty of a channel holding values", "2")
	wantSync(t, exe, 0, "", "3")
}

// TestSTDSYNC_ChanEdges: a channel of a zero-size T carries values; a
// capacity whose ring size overflows int is OutOfMemory, not a panic; a
// negative capacity panics with code 8.
func TestSTDSYNC_ChanEdges(t *testing.T) {
	t.Parallel()
	exe := buildSync(t, `struct Unit {
}

export Main(sys: &System): WriteError|OutOfMemory!void {
    var out := sys.Stdout()
    u := try Chan[Unit].New(sys.arena.Alloc(), 4)
    var n := 0
    for _ in 0..3 {
        u.Send(sys.io, Unit()) else {}
    }
    for _ in 0..3 {
        _ = u.Recv(sys.io) else {
            break
        }
        n += 1
    }
    try Print(mut out, "{n}\n", (n))
    _ = Chan[int].New(sys.arena.Alloc(), 9223372036854775807) else {
        try Print(mut out, "oom\n", ())
        return
    }
    try Print(mut out, "no oom\n", ())
}
`)
	wantSync(t, exe, 0, "3\noom\n")
}

// TestSTDSYNC_ChanLinearRoundTrip: a linear T through the ring on one task
// (Send, Send, Recv, Recv, FreeEmpty). The cell and handoff paths with a
// linear T run in the gap-gated tests.
func TestSTDSYNC_ChanLinearRoundTrip(t *testing.T) {
	t.Parallel()
	exe := buildSync(t, syncChanLib+`
SendTok(c: &Chan[Tok], io: Io, s: int): bool {
    t := Tok(s: s, q: 0)
    c.Send(io, t) else {
        _ = Take(t)
        return false
    }
    return true
}

RecvTok(c: &Chan[Tok], io: Io): int {
    t := c.Recv(io) else {
        return -1
    }
    return Take(t)
}

export Main(sys: &System): WriteError|OutOfMemory!void {
    a := sys.heap.Alloc()
    n := atomic.AtomicInt.New(0)
    c := try Chan[Tok].New(a, 2)
    ok1 := SendTok(&c, sys.io, 1)
    ok2 := SendTok(&c, sys.io, 2)
    x := RecvTok(&c, sys.io)
    y := RecvTok(&c, sys.io)
    _ = n.Add(1)
    c.FreeEmpty(a)
    var out := sys.Stdout()
    try Print(mut out, "{ok1} {ok2} {x} {y} {n}\n", (ok1, ok2, x, y, n: n.Load()))
}
`)
	wantSync(t, exe, 0, "true true 100000 200000 1\n")
}

// TestSTDSYNC_ChanParkedCancel: a receiver parked at capacity 0 and a sender
// parked on a full channel are cancelled while parked: each returns
// Canceled, the sender keeps its value, and both cells are unlinked, so the
// next Send and Recv pair with each other. A hang is the lost wakeup this
// test exists to catch, so it fails. One carrier (the parks happen before the cancel), then 8
// carriers with jitter over the seeds, where a cancel that lands before a
// task parks gives the same output (IoTable.wait checks the scope before it
// parks); dev and release.
func TestSTDSYNC_ChanParkedCancel(t *testing.T) {
	t.Parallel()
	syncRunEverywhere(t, syncChanParkedCancelSrc, "-1 -1 1\n")
}

// syncChanParkedCancelSrc is TestSTDSYNC_ChanParkedCancel's program.
const syncChanParkedCancelSrc = syncChanLib + `
Receiver(c: &Chan[int], io: Io, got: &atomic.AtomicInt) {
    got.Store(i64(RecvOr(c, io, -1)))
}

Sender(c: &Chan[int], io: Io, res: &atomic.AtomicInt) {
    if SendOk(c, io, 9) {
        res.Store(1)
    } else {
        res.Store(-1)
    }
}

export Main(sys: &System): WriteError|OutOfMemory!void {
    c := try Chan[int].New(sys.arena.Alloc(), 0)
    f := try Chan[int].New(sys.arena.Alloc(), 1)
    _ = SendOk(&f, sys.io, 1)
    got := atomic.AtomicInt.New(0)
    res := atomic.AtomicInt.New(0)
    scope s := sys.io.Scope() {
        s.go Receiver(&c, sys.io, &got)
        s.go Sender(&f, sys.io, &res)
        for _ in 0..5 {
            sys.io.Yield() else {}
        }
        s.Cancel()
    }
    a := RecvOr(&f, sys.io, -1)
    var out := sys.Stdout()
    try Print(mut out, "{g} {r} {a}\n", (g: got.Load(), r: res.Load(), a))
}
`

// syncRunEverywhere builds src at dev and release and wants stdout from it
// on one carrier and on 8 carriers over syncSeeds() seeds; a run that does
// not end within syncTimeout fails.
func syncRunEverywhere(t *testing.T, src, want string, args ...string) {
	t.Helper()
	for _, flags := range [][]string{nil, {"--release"}} {
		exe := buildSync(t, src, flags...)
		wantSync(t, exe, 0, want, args...)
		for seed := 1; seed <= syncSeeds(); seed++ {
			wantSyncEnv(t, exe, syncStressEnv(8, seed), 0, want, args...)
		}
	}
}

// syncMutexContendedSrc is TestSTDSYNC_MutexContended's program.
const syncMutexContendedSrc = `Worker(m: &Mutex[int], io: Io, k: int) {
    for i in 0..20000 {
        var g := m.Lock(io) else {
            return
        }
        *g.Mut() += 1
        if i % 7 == k {
            io.Yield() else {}
        }
        g.Unlock()
    }
}

export Main(sys: &System): WriteError!void {
    var out := sys.Stdout()
    m := Mutex[int].New(0)
    scope s := sys.io.Scope() {
        for k in 0..8 {
            s.go Worker(&m, sys.io, k)
        }
    }
    try Print(mut out, "{n}\n", (n: m.Into()))
}
`

// TestSTDSYNC_MutexContended: 8 tasks each take the lock 20,000 times and
// yield inside the critical section at different points, so the others
// spin, park on the word (state 2) and are woken one at a time; no
// increment is lost and the program ends (no lost wakeup), at dev and
// release, with 1 and 8 carriers and the runtime's jitter on.
func TestSTDSYNC_MutexContended(t *testing.T) {
	t.Parallel()
	for _, flags := range [][]string{nil, {"--release"}} {
		exe := buildSync(t, syncMutexContendedSrc, flags...)
		for _, n := range syncCarriers {
			for seed := 1; seed <= syncSeeds(); seed++ {
				wantSyncEnv(t, exe, syncStressEnv(n, seed), 0, "160000\n")
			}
		}
	}
}

// TestSTDSYNC_MutexCancelPoint: in a cancelled scope Lock returns Canceled
// even on a free mutex, and a NoCancel Io takes it; a Guard's Store
// replaces the value and Into gives it back. Store over a linear T is E0701
// at check; std.core's tests replace it through Mut instead.
func TestSTDSYNC_MutexCancelPoint(t *testing.T) {
	t.Parallel()
	exe := buildSync(t, `TryLock(m: &Mutex[int], io: Io): int {
    var g := m.Lock(io) else {
        return -1
    }
    v := *g.Ref()
    g.Store(v + 10)
    g.Unlock()
    return v
}

export Main(sys: &System): WriteError!void {
    m := Mutex[int].New(5)
    var a := 0
    var b := 0
    scope s := sys.io.Scope() {
        s.Cancel()
        a = TryLock(&m, sys.io)
        b = TryLock(&m, sys.io.NoCancel())
    }
    c := TryLock(&m, sys.io)
    var out := sys.Stdout()
    try Print(mut out, "{a} {b} {c} {d}\n", (a, b, c, d: m.Into()))
}
`)
	wantSync(t, exe, 0, "-1 5 15 25\n")
}

// TestSTDSYNC_MutexParkedCancel: a task parked on a held Mutex is
// cancelled: Lock returns Canceled without the lock, its cell leaves the
// queue, and the holder's Unlock then leaves the mutex free for the next
// Lock. A hang fails, as in TestSTDSYNC_ChanParkedCancel, on
// one carrier and on 8 over the seeds, dev and release.
func TestSTDSYNC_MutexParkedCancel(t *testing.T) {
	t.Parallel()
	syncRunEverywhere(t, syncMutexParkedCancelSrc, "-1\n")
}

// syncMutexParkedCancelSrc is TestSTDSYNC_MutexParkedCancel's program.
const syncMutexParkedCancelSrc = `import std.atomic

Waiter(m: &Mutex[int], io: Io, r: &atomic.AtomicInt) {
    var g := m.Lock(io) else {
        r.Store(-1)
        return
    }
    g.Unlock()
    r.Store(1)
}

export Main(sys: &System): Canceled|WriteError!void {
    m := Mutex[int].New(0)
    r := atomic.AtomicInt.New(0)
    var g := try m.Lock(sys.io)
    scope s := sys.io.Scope() {
        s.go Waiter(&m, sys.io, &r)
        for _ in 0..5 {
            sys.io.Yield() else {}
        }
        s.Cancel()
    }
    g.Unlock()
    var g2 := try m.Lock(sys.io)
    g2.Unlock()
    var out := sys.Stdout()
    try Print(mut out, "{r}\n", (r: r.Load()))
}
`

// syncBenchSingle are the single-task (and two-task) timing programs.
var syncBenchSingle = []struct{ name, src, want string }{
	{"rendezvous 1M", `Pump(c: &Chan[int], io: Io) {
    for i in 0..1000000 {
        c.Send(io, i) else {
            return
        }
    }
    c.Close()
}

export Main(sys: &System): WriteError|OutOfMemory!void {
    c := try Chan[int].New(sys.arena.Alloc(), 0)
    var total := 0
    scope s := sys.io.Scope() {
        s.go Pump(&c, sys.io)
        for {
            x := c.Recv(sys.io) else {
                break
            }
            total += x
        }
    }
    var out := sys.Stdout()
    try Print(mut out, "{total}\n", (total))
}
`, "499999500000\n"},
	{"8 parked senders, capacity 1, 200k", `Sender(c: &Chan[int], io: Io) {
    for i in 0..25000 {
        c.Send(io, i) else {
            return
        }
    }
}

export Main(sys: &System): WriteError|OutOfMemory!void {
    c := try Chan[int].New(sys.arena.Alloc(), 1)
    var n := 0
    scope s := sys.io.Scope() {
        for _ in 0..8 {
            s.go Sender(&c, sys.io)
        }
        for _ in 0..200000 {
            x := c.Recv(sys.io) else {
                break
            }
            n += x
        }
    }
    var out := sys.Stdout()
    try Print(mut out, "{n}\n", (n))
}
`, "2499900000\n"},
	{"Mutex uncontended 10M", `export Main(sys: &System): WriteError!void {
    m := Mutex[int].New(0)
    for _ in 0..10000000 {
        var g := m.Lock(sys.io) else {
            return
        }
        *g.Mut() += 1
        g.Unlock()
    }
    var out := sys.Stdout()
    try Print(mut out, "{n}\n", (n: m.Into()))
}
`, "10000000\n"},
	{"AtomicInt.Add 10M", `import std.atomic

export Main(sys: &System): WriteError!void {
    a := atomic.AtomicInt.New(0)
    for _ in 0..10000000 {
        _ = a.Add(1)
    }
    var out := sys.Stdout()
    try Print(mut out, "{n}\n", (n: a.Load()))
}
`, "10000000\n"},
}

// TestSTDSYNC_Bench: channel and mutex timings, logged with the machine, on
// one carrier (TestSTDSYNC_BenchCarriers adds 8 and the contended programs),
// at release. Each program's wall time includes process start (an empty Main
// is measured the same way). It fails only on a wrong result.
func TestSTDSYNC_Bench(t *testing.T) {
	t.Parallel()
	var report strings.Builder
	for _, p := range syncBenchSingle {
		exe := buildSync(t, p.src, "--release")
		best := time.Duration(1 << 62)
		for range 3 {
			r := wantSync(t, exe, 0, p.want)
			best = min(best, r.took)
		}
		fmt.Fprintf(&report, "%s: best of 3 %v\n", p.name, best.Round(time.Millisecond))
	}
	t.Log("\n" + report.String())
}
