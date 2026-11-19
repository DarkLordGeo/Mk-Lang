// Why: past bugs in Mutex and Chan (starvation, depth-dependent cost, Close
// races) must stay fixed.
// What: regression tests for Mutex fairness and worst wait, Lock cost at any
// scope depth, Close racing live traffic, the ordering comment at every
// atomic site, and std.atomic's and std.sync's test blocks under mk test.
// How: timed and seeded Mk programs; coverage checks turn a branch into a
// panic in a scratch copy of std and require a run to reach it.

package std

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// syncStarveSrc: Hog takes the lock n times and yields inside every
// critical section; Late yields once (so Hog holds the lock), then locks
// once and records the count it sees. With barging and no handoff, on one
// carrier Late is woken by every unlock but Hog, still running, takes the
// lock back first, so Late saw n. Output:
// "seen n".
const syncStarveSrc = `import std.atomic

Hog(m: &Mutex[int], io: Io, n: int) {
    for _ in 0..n {
        var g := m.Lock(io) else {
            return
        }
        *g.Mut() += 1
        io.Yield() else {}
        g.Unlock()
    }
}

Late(m: &Mutex[int], io: Io, seen: &atomic.AtomicInt) {
    io.Yield() else {}
    var g := m.Lock(io) else {
        return
    }
    seen.Store(i64(*g.Ref()))
    g.Unlock()
}

export Main(sys: &System): ParseError|WriteError!void {
    n := int(try sys.args[1].ParseInt())
    m := Mutex[int].New(0)
    seen := atomic.AtomicInt.New(-1)
    scope s := sys.io.Scope() {
        s.go Hog(&m, sys.io, n)
        s.go Late(&m, sys.io, &seen)
    }
    var out := sys.Stdout()
    try Print(mut out, "{s} {n}\n", (s: seen.Load(), n: m.Into()))
}
`

// syncStarveN is Hog's count: a fraction of a second of Hog cycles on one
// carrier, far more than the 1 ms after which Late is handed
// the lock.
const syncStarveN = 200000

// checkStarve wants Late to have got the lock well before Hog's end.
func checkStarve(t *testing.T, env []string, r nativeRun) {
	t.Helper()
	var seen, n int
	if _, err := fmt.Sscanf(r.stdout, "%d %d\n", &seen, &n); err != nil || r.code != 0 || n != syncStarveN || seen < 0 || seen >= syncStarveN/2 {
		t.Errorf("%v: exit %d, stdout %q: want Late to get the lock before half of Hog's %d cycles (C-SYNC-RV1)\nstderr:\n%s", env, r.code, r.stdout, syncStarveN, r.stderr)
	}
}

// TestSTDSYNC_MutexNoStarvation: one carrier, dev and release: a purely
// barging lock would starve Late for all of Hog's cycles; with
// sync_mutex.mk's 1 ms handoff Late gets it within about 1 ms. 8
// carriers with jitter over the seeds as well.
func TestSTDSYNC_MutexNoStarvation(t *testing.T) {
	t.Parallel()
	for _, flags := range [][]string{nil, {"--release"}} {
		exe := buildSync(t, syncStarveSrc, flags...)
		r, hung := runSyncEnv(t, exe, syncTimeout, syncOne, fmt.Sprint(syncStarveN))
		if hung {
			t.Fatalf("no exit within %v", syncTimeout)
		}
		checkStarve(t, syncOne, r)
		t.Logf("%v 1 carrier: %s", flags, strings.TrimSpace(r.stdout))
		for seed := 1; seed <= syncSeeds(); seed++ {
			env := syncStressEnv(8, seed)
			r, hung := runSyncEnv(t, exe, syncTimeout, env, fmt.Sprint(syncStarveN))
			if hung {
				t.Fatalf("%v: no exit within %v", env, syncTimeout)
			}
			checkStarve(t, env, r)
		}
	}
}

// syncLatencySrc: 7 hammer tasks lock, add and unlock in a loop until a
// flag is set; one more task locks 200 times, reading the clock around
// each Lock, and prints the largest wait in microseconds, then sets the
// flag.
const syncLatencySrc = `import std.atomic
import std.time

Hammer(m: &Mutex[int], io: Io, stop: &atomic.AtomicBool) {
    for !stop.Load() {
        var g := m.Lock(io) else {
            return
        }
        *g.Mut() += 1
        g.Unlock()
    }
}

Probe(m: &Mutex[int], io: Io, stop: &atomic.AtomicBool, worst: &atomic.AtomicInt) {
    var w: i64 = 0
    for _ in 0..200 {
        a := time.Now(io)
        var g := m.Lock(io) else {
            break
        }
        d := time.Now(io).Since(a).Nanos()
        if d > w {
            w = d
        }
        g.Unlock()
        io.Yield() else {}
    }
    worst.Store(w / 1000)
    stop.Store(true)
}

export Main(sys: &System): WriteError!void {
    m := Mutex[int].New(0)
    stop := atomic.AtomicBool.New(false)
    worst := atomic.AtomicInt.New(0)
    scope s := sys.io.Scope() {
        for _ in 0..7 {
            s.go Hammer(&m, sys.io, &stop)
        }
        s.go Probe(&m, sys.io, &stop, &worst)
    }
    var out := sys.Stdout()
    try Print(mut out, "{w}\n", (w: worst.Load()))
}
`

// TestSTDSYNC_MutexMaxLatency: the probe's worst Lock
// wait while 7 tasks hammer the lock at 8 carriers, release, over the
// seeds. The bound is 1 ms of starvation threshold plus one critical
// section plus the scheduling delay of a loaded 16-CPU machine, so the test
// fails only above 50 ms and logs the worst wait it saw.
func TestSTDSYNC_MutexMaxLatency(t *testing.T) {
	t.Parallel()
	exe := buildSync(t, syncLatencySrc, "--release")
	var worst int64
	for seed := 1; seed <= syncSeeds(); seed++ {
		env := syncStressEnv(8, seed)
		r, hung := runSyncEnv(t, exe, syncTimeout, env)
		if hung {
			t.Fatalf("%v: no exit within %v", env, syncTimeout)
		}
		us, err := strconv.ParseInt(strings.TrimSpace(r.stdout), 10, 64)
		if err != nil || r.code != 0 {
			t.Fatalf("%v: exit %d, stdout %q\nstderr:\n%s", env, r.code, r.stdout, r.stderr)
		}
		worst = max(worst, us)
		if us > 50000 && timingRun() {
			t.Errorf("%v: worst Lock wait %d us, want under 50 ms", env, us)
		}
	}
	t.Logf("worst Lock wait over %d seeds at 8 carriers: %d us", syncSeeds(), worst)
	if !timingRun() {
		t.Logf("%s: the 50 ms bound is not asserted", timingOff)
	}
}

// syncDepthSrc: Nest spawns a task d scopes deep, which runs n uncontended
// Lock + add + Unlock pairs and n Send + Recv pairs on a capacity-64
// channel with room, none of which waits.
const syncDepthSrc = `import std.atomic

Work(m: &Mutex[int], c: &Chan[int], io: Io, n: int): int {
    var sum := 0
    for i in 0..n {
        var g := m.Lock(io) else {
            return -1
        }
        *g.Mut() += 1
        g.Unlock()
        c.Send(io, i) else {
            return -2
        }
        x := c.Recv(io) else {
            return -3
        }
        sum += x
    }
    return sum
}

Nest(m: &Mutex[int], c: &Chan[int], io: Io, d: int, n: int, r: &atomic.AtomicInt) {
    if d == 0 {
        r.Store(i64(Work(m, c, io, n)))
        return
    }
    scope s := io.Scope() {
        s.go Nest(m, c, io, d - 1, n, r)
    }
}

export Main(sys: &System): ParseError|OutOfMemory|WriteError!void {
    d := int(try sys.args[1].ParseInt())
    n := int(try sys.args[2].ParseInt())
    m := Mutex[int].New(0)
    c := try Chan[int].New(sys.arena.Alloc(), 64)
    r := atomic.AtomicInt.New(0)
    Nest(&m, &c, sys.io, d, n, &r)
    var out := sys.Stdout()
    try Print(mut out, "{v}\n", (v: r.Load() + i64(m.Into())))
}
`

// TestSTDSYNC_LockCostIndependentOfDepth: 3M rounds of an uncontended
// Lock/Unlock and a Send/Recv with room, release, one carrier, at scope
// depth 0 and 64, best of 5 whole-process wall times each, interleaved. The
// cancellation point does not walk the scope chain, so depth 64 must cost
// under 1.5x depth 0.
func TestSTDSYNC_LockCostIndependentOfDepth(t *testing.T) {
	t.Parallel()
	exe := buildSync(t, syncDepthSrc, "--release")
	const n = 3000000
	want := fmt.Sprintf("%d\n", n+n*(n-1)/2)
	best := map[string]time.Duration{"0": 1 << 62, "64": 1 << 62}
	rounds := 5
	if !timingRun() {
		rounds = 1 // the outputs are judged; the ratio is not
	}
	for range rounds {
		for _, d := range []string{"0", "64"} {
			r := wantSync(t, exe, 0, want, d, fmt.Sprint(n))
			best[d] = min(best[d], r.took)
		}
	}
	t.Logf("3M Lock/Unlock + Send/Recv, 1 carrier, best of 5: depth 0 %v, depth 64 %v", best["0"].Round(time.Millisecond), best["64"].Round(time.Millisecond))
	if !timingRun() {
		t.Logf("%s: the 1.5x ratio is not asserted", timingOff)
	} else if float64(best["64"]) > 1.5*float64(best["0"]) {
		t.Errorf("depth 64 took %v, over 1.5x depth 0's %v", best["64"], best["0"])
	}
}

// syncCloseMidSrc: 4 receivers and 8 senders of 2,000 linear
// tokens each run while a Closer task yields a seeded number of times and
// then closes the channel mid-traffic. A sender whose Send is Closed keeps
// its token and counts the rest as kept; after the scope Main drains what
// the ring still holds (Recv after Close) and frees the channel. Every
// token is received, drained or kept exactly once (C2, C5). Output:
// "total ids".
const syncCloseMidSrc = `import std.atomic

linear struct Tok {
    s: int
    q: int
}

Take(t: Tok): int {
    Tok(s, q) := t
    return s * 100000 + q
}

Sender(c: &Chan[Tok], io: Io, id: int, kept: &atomic.AtomicInt, ksum: &atomic.AtomicInt) {
    for q in 0..2000 {
        t := Tok(s: id, q: q)
        c.Send(io, t) else {
            _ = ksum.Add(i64(Take(t)))
            _ = kept.Add(1)
            for r in q + 1..2000 {
                _ = ksum.Add(i64(id * 100000 + r))
                _ = kept.Add(1)
            }
            return
        }
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

Closer(c: &Chan[Tok], io: Io, k: int) {
    for _ in 0..k {
        io.Yield() else {}
    }
    c.Close()
}

export Main(sys: &System): ParseError|OutOfMemory|WriteError!void {
    cap := int(try sys.args[1].ParseInt())
    seed := int(try sys.args[2].ParseInt())
    a := sys.heap.Alloc()
    c := try Chan[Tok].New(a, cap)
    n := atomic.AtomicInt.New(0)
    sum := atomic.AtomicInt.New(0)
    kept := atomic.AtomicInt.New(0)
    ksum := atomic.AtomicInt.New(0)
    scope s := sys.io.Scope() {
        for _ in 0..4 {
            s.go Receiver(&c, sys.io, &n, &sum)
        }
        for id in 0..8 {
            s.go Sender(&c, sys.io, id, &kept, &ksum)
        }
        s.go Closer(&c, sys.io, seed * 37 % 400)
    }
    var left: i64 = 0
    for {
        t := c.Recv(sys.io) else {
            break
        }
        _ = sum.Add(i64(Take(t)))
        left += 1
    }
    c.FreeEmpty(a)
    var out := sys.Stdout()
    try Print(mut out, "{total} {ids}\n", (total: n.Load() + kept.Load() + left, ids: sum.Load() + ksum.Load()))
}
`

// syncCloseMidWant is syncCloseMidSrc's output: 16,000 tokens and their id
// sum.
func syncCloseMidWant() string {
	var ids int64
	for s := int64(0); s < 8; s++ {
		for q := int64(0); q < 2000; q++ {
			ids += s*100000 + q
		}
	}
	return fmt.Sprintf("16000 %d\n", ids)
}

// TestSTDSYNC_ChanCloseMidTraffic: syncCloseMidSrc at capacities 0, 1 and 3,
// 1 and 8 carriers with jitter, syncSeeds() seeds, dev and release.
func TestSTDSYNC_ChanCloseMidTraffic(t *testing.T) {
	t.Parallel()
	want := syncCloseMidWant()
	for _, flags := range [][]string{nil, {"--release"}} {
		exe := buildSync(t, syncCloseMidSrc, flags...)
		for _, n := range syncCarriers {
			for _, cap := range []string{"0", "1", "3"} {
				for seed := 1; seed <= syncSeeds(); seed++ {
					wantSyncEnv(t, exe, syncStressEnv(n, seed), 0, want, cap, fmt.Sprint(seed))
				}
			}
		}
	}
}

// syncCloseProbes are instrumented copies of sync_chan.mk for
// TestSTDSYNC_CloseLockBranchesRun: each turns one branch that only a
// Close racing a lock holder reaches into a panic with its own message, so
// a run that reaches it exits 101 naming it.
var syncCloseProbes = []struct{ name, old, new string }{
	// The texts follow ChanLockNoIo (it retries a slow path that failed)
	// and ChanTabIo.
	{"ChanLockNoIo parks", "            MutexLockSlow(lock, ChanTabIo(st), false) else {\n",
		"            raw.Fail(99, \"probe: ChanLockNoIo parks\")\n            MutexLockSlow(lock, ChanTabIo(st), false) else {\n"},
	{"ChanUnlockNoIo wakes", "        MutexUnlockSlow(lock, ChanTabIo(st))\n",
		"        raw.Fail(99, \"probe: ChanUnlockNoIo wakes\")\n"},
}

// TestSTDSYNC_CloseLockBranchesRun: the coverage half. In a
// scratch copy, one branch at a time becomes a panic: ChanLockNoIo's park
// (Close finds the lock held, and a task has parked on it before) and
// ChanUnlockNoIo's slow unlock (a task queued on the lock while Close held
// it). The channel's lock is the Mutex's queue lock.
// syncCloseMidSrc at 8 carriers, release, capacities 0 and 3, seeds 1 to
// 3x syncSeeds() and then on until the branch is reached, at most 300 runs:
// each branch must be reached at least once. The park is the rarer of the
// two.
func TestSTDSYNC_CloseLockBranchesRun(t *testing.T) {
	t.Parallel()
	for _, p := range syncCloseProbes {
		exe := buildSyncPatched(t, syncCloseMidSrc, func(stdDir string) {
			f := filepath.Join(stdDir, "core", "sync_chan.mk")
			b, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(b), p.old) {
				t.Fatalf("probe %q: the branch text is not in sync_chan.mk", p.name)
			}
			if err := os.WriteFile(f, []byte(strings.Replace(string(b), p.old, p.new, 1)), 0o644); err != nil {
				t.Fatal(err)
			}
		}, "--release")
		hits, runs := 0, 0
		for seed := 1; runs < 300 && (seed <= 3*syncSeeds() || hits == 0); seed++ {
			for _, cap := range []string{"0", "3"} {
				r, hung := runSyncEnv(t, exe, syncTimeout, syncStressEnv(8, seed), cap, fmt.Sprint(seed))
				if hung {
					t.Fatalf("%s cap %s seed %d: no exit within %v", p.name, cap, seed, syncTimeout)
				}
				runs++
				if r.code == 101 && strings.Contains(r.stderr, "probe: "+p.name) {
					hits++
				} else if r.code != 0 {
					t.Fatalf("%s cap %s seed %d: exit %d\nstderr:\n%s", p.name, cap, seed, r.code, r.stderr)
				}
			}
		}
		t.Logf("%s: reached in %d of %d runs", p.name, hits, runs)
		if hits == 0 {
			t.Errorf("%s: never reached in %d runs", p.name, runs)
		}
	}
}

// syncAtomicSites matches a raw.Atomic operation in the sync files.
var syncAtomicSites = regexp.MustCompile(`\.(Load|Store|Swap|Cas|Add)\(`)

// syncEdgeWords is what an ordering comment names.
var syncEdgeWords = regexp.MustCompile(`(?i)\b(edge|ordering|orders|acquire|release)`)

// TestSTDSYNC_EveryAtomicNamesItsEdge: every atomic operation in the
// sync .mk files has, in its function's doc
// comment or in a comment of the body above it, a comment that names the
// edge it creates (or says it orders nothing).
func TestSTDSYNC_EveryAtomicNamesItsEdge(t *testing.T) {
	files := []string{"core/sync_mutex.mk", "core/sync_chan.mk", "core/mem_chan.mk", "atomic/atomic.mk", "sync/mem_shared.mk"}
	for _, f := range files {
		b, err := os.ReadFile(filepath.FromSlash(f))
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n")
		for i, line := range lines {
			code := strings.TrimSpace(line)
			if strings.HasPrefix(code, "//") || strings.HasPrefix(code, "export ") || !syncAtomicSites.MatchString(code) {
				continue
			}
			// The function's start: the last line at column 0 that opens it.
			start := i
			for start > 0 && !(len(lines[start]) > 0 && lines[start][0] != ' ' && lines[start][0] != '}' && lines[start][0] != '/' && lines[start][0] != '\t') {
				start--
			}
			for start > 0 && strings.HasPrefix(lines[start-1], "//") {
				start--
			}
			ok := false
			for j := start; j < i; j++ {
				if c := strings.TrimSpace(lines[j]); strings.HasPrefix(c, "//") && syncEdgeWords.MatchString(c) {
					ok = true
					break
				}
			}
			if !ok {
				t.Errorf("%s:%d: %s: no comment names the ordering edge of this atomic operation", f, i+1, code)
			}
		}
	}
}

// TestSTDSYNC_TestBlocks: mk test runs the Mk test blocks of
// std.atomic (atomic/tests_atomic.mk) and std.sync (sync/tests_shared.mk)
// in child processes with leak detection, with 1 and 8 carriers. The panicking
// cases stay Go tests (AtomicAddMatchesBigInt, SharedAcrossTasks).
func TestSTDSYNC_TestBlocks(t *testing.T) {
	root := t.TempDir()
	stdDir := copyStd(t, root, nil)
	if alt := os.Getenv("MK_STDSYNC_SRC"); alt != "" {
		stdDir = filepath.Join(root, "alt", "std")
		if err := os.CopyFS(stdDir, os.DirFS(alt)); err != nil {
			t.Fatal(err)
		}
		copyLaneFiles(t, stdDir)
	}
	t.Setenv("MK_CACHE", filepath.Join(root, "cache"))
	for _, mod := range []string{"std.atomic", "std.sync"} {
		for _, n := range syncCarriers {
			t.Setenv("MK_CARRIERS", fmt.Sprint(n))
			out, status := mkCmd(t, root, stdDir, "test", "--std="+mod)
			if strings.Contains(out, "no usable LLVM") {
				t.Skipf("needs the LLVM toolchain: %s", out)
			}
			if status != 0 || !strings.Contains(out, " 0 failed") || strings.Contains(out, "\n0 passed") {
				t.Errorf("mk test --std=%s, MK_CARRIERS=%d: status %d\n%s", mod, n, status, out)
			}
			t.Logf("mk test --std=%s, MK_CARRIERS=%d:\n%s", mod, n, out)
		}
	}
}
