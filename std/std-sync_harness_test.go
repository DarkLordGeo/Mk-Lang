// Why: atomics, Shared and the sync primitives need tests that run real
// programs on the scheduler, with a time limit so a lost wakeup fails.
// What: std.atomic and Shared tests, and the build-and-run harness the other
// std-sync_*_test.go files share (buildSync, wantSync, syncStressEnv).
// How: each test writes a Mk program, builds it over a copy of std and runs it
// under syncTimeout, on one carrier or on several with seeded jitter.

package std

import (
	"context"
	"fmt"
	"math"
	"math/big"
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

// syncTimeout bounds one run of a test program. Every program here finishes
// in well under a second; the bound only turns a
// hang into a failure.
const syncTimeout = 60 * time.Second

// syncOne is the environment of a run whose schedule the test controls
// with Yield: one carrier, no jitter, so the interleaving is the program's
// own order (the runtime's MK_CARRIERS; unset, the runtime starts one carrier
// per CPU).
var syncOne = []string{"MK_CARRIERS=1"}

// syncCarriers are the carrier counts every stress test runs with
// (1 and 8).
var syncCarriers = []int{1, 8}

// syncSeeds is the number of seeds a stress test runs per configuration:
// MK_STDSYNC_SEEDS, default 8.
func syncSeeds() int {
	if n, err := strconv.Atoi(os.Getenv("MK_STDSYNC_SEEDS")); err == nil && n > 0 {
		return n
	}
	return 8
}

// syncStressEnv is the environment of a stress run: n carriers, and the
// runtime's jitter points on with seed (MK_TEST_SEED, std/task/jitter.mk).
func syncStressEnv(n, seed int) []string {
	return []string{fmt.Sprintf("MK_CARRIERS=%d", n), fmt.Sprintf("MK_TEST_SEED=%d", seed)}
}

// runSync runs exe with args under limit on one carrier; timedOut reports
// a kill.
func runSync(t *testing.T, exe string, limit time.Duration, args ...string) (r nativeRun, timedOut bool) {
	t.Helper()
	return runSyncEnv(t, exe, limit, syncOne, args...)
}

// runSyncEnv is runSync with env added to the process environment.
func runSyncEnv(t *testing.T, exe string, limit time.Duration, env []string, args ...string) (r nativeRun, timedOut bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, args...)
	cmd.Dir = filepath.Dir(exe)
	cmd.Env = append(os.Environ(), env...)
	var out, errb strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &errb
	start := time.Now()
	err := cmd.Run()
	r = nativeRun{stdout: out.String(), stderr: errb.String(), took: time.Since(start)}
	if ctx.Err() == context.DeadlineExceeded {
		return r, true
	}
	if ee, ok := err.(*exec.ExitError); ok {
		r.code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return r, false
}

// buildSync builds src as main.mk over std with buildNative. MK_STDSYNC_SRC
// names another std tree to copy instead of this directory (a snapshot, when
// the live tree is mid-edit); the sync files (copyLaneFiles) are always taken
// from this directory.
func buildSync(t *testing.T, src string, flags ...string) string {
	t.Helper()
	var out string
	exe := buildSyncQuiet(t, src, &out, flags...)
	if exe == "" {
		t.Fatalf("mk build failed\n%s", out)
	}
	return exe
}

// buildSyncQuiet builds like buildSync but returns "" and the output on a
// failed build instead of failing.
func buildSyncQuiet(t *testing.T, src string, out *string, flags ...string) string {
	t.Helper()
	return buildSyncPatchedQuiet(t, src, nil, out, flags...)
}

// buildSyncPatched is buildSync with patch run over the copied std before
// the build: an instrumented scratch copy of a sync file, for a coverage
// check (nothing in the tree changes).
func buildSyncPatched(t *testing.T, src string, patch func(stdDir string), flags ...string) string {
	t.Helper()
	var out string
	exe := buildSyncPatchedQuiet(t, src, patch, &out, flags...)
	if exe == "" {
		t.Fatalf("mk build failed\n%s", out)
	}
	return exe
}

func buildSyncPatchedQuiet(t *testing.T, src string, patch func(stdDir string), out *string, flags ...string) string {
	t.Helper()
	root := t.TempDir()
	alt := os.Getenv("MK_STDSYNC_SRC")
	var stdDir string
	if alt == "" {
		stdDir = copyStd(t, root, nil)
	} else {
		stdDir = filepath.Join(root, "std")
		if err := os.CopyFS(stdDir, os.DirFS(alt)); err != nil {
			t.Fatal(err)
		}
		if os.Getenv("MK_STDSYNC_BASELINE") == "" { // set: measure the snapshot's own sync files, for a before and after
			copyLaneFiles(t, stdDir)
		}
	}
	if patch != nil {
		patch(stdDir)
	}
	if err := os.WriteFile(filepath.Join(root, "main.mk"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(root, "main")
	if runtime.GOOS == "windows" && !slices.Contains(flags, "x86_64-linux") {
		exe += ".exe"
	}
	args := append([]string{"build"}, flags...)
	args = append(args, "-o", exe, "main.mk")
	o, status := mkCmd(t, root, stdDir, args...)
	*out = o
	if status != 0 {
		if strings.Contains(o, "no usable LLVM") {
			t.Skipf("needs the LLVM toolchain: %s", o)
		}
		return ""
	}
	return exe
}

// copyLaneFiles writes the sync files (core/sync_*.mk, core/mem_chan.mk,
// atomic, sync) from this directory over stdDir.
func copyLaneFiles(t *testing.T, stdDir string) {
	t.Helper()
	for _, glob := range []string{"core/sync_*.mk", "core/mem_chan.mk", "atomic/*.mk", "sync/*.mk"} {
		files, _ := filepath.Glob(filepath.FromSlash(glob))
		for _, f := range files {
			b, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			dst := filepath.Join(stdDir, f)
			_ = os.MkdirAll(filepath.Dir(dst), 0o755)
			if err := os.WriteFile(dst, b, 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
}

// wantSync runs exe on one carrier and wants exit code and stdout.
func wantSync(t *testing.T, exe string, code int, stdout string, args ...string) nativeRun {
	t.Helper()
	return wantSyncEnv(t, exe, syncOne, code, stdout, args...)
}

// wantSyncEnv is wantSync with env added to the process environment.
func wantSyncEnv(t *testing.T, exe string, env []string, code int, stdout string, args ...string) nativeRun {
	t.Helper()
	r, hung := runSyncEnv(t, exe, syncTimeout, env, args...)
	if hung {
		t.Fatalf("%v %v: no exit within %v (a lost wakeup?)\nstdout:\n%s", env, args, syncTimeout, r.stdout)
	}
	if r.code != code || r.stdout != stdout {
		t.Errorf("%v %v: exit %d, want %d\nstdout:\n%s\nwant:\n%s\nstderr:\n%s", env, args, r.code, code, r.stdout, stdout, r.stderr)
	}
	return r
}

// wantPanic runs exe and wants exit 101 with msg in the report.
func wantPanic(t *testing.T, exe, msg string, args ...string) {
	t.Helper()
	r, hung := runSync(t, exe, syncTimeout, args...)
	if hung || r.code != 101 || !strings.Contains(r.stderr, msg) {
		t.Errorf("%v: exit %d (hung %v), want 101 and %q\nstdout:\n%s\nstderr:\n%s", args, r.code, hung, msg, r.stdout, r.stderr)
	}
}

// TestSTDSYNC_AtomicBasics: every operation of std.atomic on one task, and a
// `var` global initialized with AtomicInt.New: Add returns
// the new value, Swap the old one.
func TestSTDSYNC_AtomicBasics(t *testing.T) {
	t.Parallel()
	exe := buildSync(t, `import std.atomic

var Total: atomic.AtomicInt = atomic.AtomicInt.New(40)

export Main(sys: &System): WriteError!void {
    var out := sys.Stdout()
    a := Total.Add(2)
    b := Total.Add(-50)
    Total.Store(7)
    c := Total.Load()
    f := atomic.AtomicBool.New(false)
    s1 := f.Swap(true)
    s2 := f.Swap(false)
    l := f.Load()
    f.Store(true)
    try Print(mut out, "{a} {b} {c} {s1} {s2} {l} {l2}\n", (a, b, c, s1, s2, l, l2: f.Load()))
}
`)
	wantSync(t, exe, 0, "42 -8 7 false true false true\n")
}

// TestSTDSYNC_AtomicAddMatchesBigInt: AtomicInt.Add against a Go model in
// math/big, on every pair of the i64 edge values and seeded random ones:
// the sum when it fits i64, else a panic with code 1 naming AtomicInt.Add
// raised before the word changes.
func TestSTDSYNC_AtomicAddMatchesBigInt(t *testing.T) {
	t.Parallel()
	exe := buildSync(t, `import std.atomic

export Main(sys: &System): ParseError|WriteError!void {
    var out := sys.Stdout()
    old := try sys.args[1].ParseInt()
    n := try sys.args[2].ParseInt()
    a := atomic.AtomicInt.New(old)
    r := a.Add(n)
    try Print(mut out, "{r} {l}\n", (r, l: a.Load()))
}
`)
	edges := []int64{math.MaxInt64, math.MaxInt64 - 1, 1, 0, -1, math.MinInt64 + 1, math.MinInt64}
	x := uint64(0x9E3779B97F4A7C15)
	for range 4 {
		x ^= x << 13
		x ^= x >> 7
		x ^= x << 17
		edges = append(edges, int64(x))
	}
	for _, old := range edges {
		for _, n := range edges {
			sum := new(big.Int).Add(big.NewInt(old), big.NewInt(n))
			args := []string{fmt.Sprint(old), fmt.Sprint(n)}
			if sum.IsInt64() {
				wantSync(t, exe, 0, fmt.Sprintf("%d %d\n", sum.Int64(), sum.Int64()), args...)
			} else {
				wantPanic(t, exe, "AtomicInt.Add", args...)
			}
		}
	}
}

// syncAtomicContendedSrc is TestSTDSYNC_AtomicContended's program.
const syncAtomicContendedSrc = `import std.atomic

Adder(total: &atomic.AtomicInt, io: Io, k: int) {
    var x: u64 = Cast[u64](k * 7919 + 1)
    for i in 1..2001 {
        _ = total.Add(i64(i))
        x ^= x << 13
        x ^= x >> 7
        x ^= x << 17
        if x % 3 == 0 {
            io.Yield() else {}
        }
    }
}

Reader(total: &atomic.AtomicInt, bad: &atomic.AtomicBool, io: Io) {
    var last: i64 = 0
    for _ in 0..2000 {
        v := total.Load()
        if v < last || v > 32016000 {
            bad.Store(true)
        }
        last = v
        io.Yield() else {}
    }
}

export Main(sys: &System): WriteError!void {
    total := atomic.AtomicInt.New(0)
    bad := atomic.AtomicBool.New(false)
    scope s := sys.io.Scope() {
        for k in 0..16 {
            s.go Adder(&total, sys.io, k)
            s.go Reader(&total, &bad, sys.io)
        }
    }
    var out := sys.Stdout()
    try Print(mut out, "{t} {b}\n", (t: total.Load(), b: bad.Load()))
}
`

// TestSTDSYNC_AtomicContended: 16 tasks each add 1 to 1..2,000 and yield
// at seeded points while 16 more read; every Add is counted once, and no
// reader sees a value outside the possible range (a torn or lost update),
// with 1 and 8 carriers and the runtime's jitter on.
func TestSTDSYNC_AtomicContended(t *testing.T) {
	t.Parallel()
	exe := buildSync(t, syncAtomicContendedSrc)
	for _, n := range syncCarriers {
		for seed := 1; seed <= syncSeeds(); seed++ {
			wantSyncEnv(t, exe, syncStressEnv(n, seed), 0, "32016000 false\n")
		}
	}
}

// TestSTDSYNC_SharedAcrossTasks: 50 rounds of 64 tasks that each receive a
// Clone of one Shared[int], yield, read it and Free their handle, while Main
// frees its own handle inside the scope, so the last Free (the one that
// frees the box) runs in whichever task ends last; on 8 carriers the
// decrements race. 1 and 8 carriers, jitter on, dev and release.
// The last Free of a Shared over a linear T panics with code 8.
func TestSTDSYNC_SharedAcrossTasks(t *testing.T) {
	t.Parallel()
	lin := buildSync(t, `import std.sync

linear struct Tok {
    n: int
}

export Main(sys: &System): OutOfMemory!void {
    t := Tok(n: 1)
    p := sync.Shared[Tok].New(sys.heap, t) else {
        Tok(n: _) := t
        return
    }
    p.Free(sys.heap)
}
`)
	wantPanic(t, lin, "Shared.Free of the last handle")
	for _, flags := range [][]string{nil, {"--release"}} {
		exe := buildSync(t, syncSharedSrc, flags...)
		for _, n := range syncCarriers {
			for seed := 1; seed <= syncSeeds(); seed++ {
				wantSyncEnv(t, exe, syncStressEnv(n, seed), 0, "16000\n")
			}
		}
	}
}

// syncSharedSrc is TestSTDSYNC_SharedAcrossTasks's program.
const syncSharedSrc = `import std.atomic
import std.sync

Reader(p: sync.Shared[int], h: &Heap, n: &atomic.AtomicInt, io: Io) {
    io.Yield() else {}
    _ = n.Add(i64(*p.Ref()))
    p.Free(h)
}

export Main(sys: &System): OutOfMemory|WriteError!void {
    n := atomic.AtomicInt.New(0)
    for _ in 0..50 {
        p := try sync.Shared[int].New(sys.heap, 5)
        scope s := sys.io.Scope() {
            for _ in 0..64 {
                s.go Reader(p.Clone(), sys.heap, &n, sys.io)
            }
            p.Free(sys.heap)
        }
    }
    var out := sys.Stdout()
    try Print(mut out, "{n}\n", (n: n.Load()))
}
`
