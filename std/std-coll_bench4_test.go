// Why: HeapMap.Free of an emptied map must be O(1), its two frees, with no
// walk of the table's cap/8 control groups.
// What: TestStdColl_BenchEmptiedFree for capacities 2^10 to 2^21, and
// TextMap.Free of an emptied map.
// How: a --release program times Free, the old walk (TableNext over every
// group) and TableFreeArrays alone. Skips unless MK_BENCH=1.

package std

import (
	"os"
	"runtime"
	"testing"
)

// collBench4Core is written to std/core/tests_collbench4.mk in the copy.
const collBench4Core = `// Fixture of the COLL-R3 benchmark: the old HeapMap.Free walk and the
// frees alone, on a map the benchmark built.

// CollBench4Twin consumes m (emptied, its owner a) and returns the time of
// the walk the old HeapMap.Free made over it and of TableFreeArrays, in ns;
// seen is the number of full slots the walk met (0 for an emptied map),
// returned so the walk is not dead code.
export CollBench4Twin(io: Io, m: HeapMap[int, int], a: &Alloc): (walk: i64, arrays: i64, seen: int) {
    HeapMap(t, owner: _, mk, mv) := m
    Discard(mk)
    Discard(mv)
    t0 := IoNow(io).value
    var seen := 0
    var i := TableNext(&t, 0)
    for i >= 0 {
        seen += 1
        i = TableNext(&t, i + 1)
    }
    t1 := IoNow(io).value
    TableFreeArrays(&t, a)
    t2 := IoNow(io).value
    return (walk: t1 - t0, arrays: t2 - t1, seen: seen)
}

export CollBench4Cap(m: &HeapMap[int, int]): int {
    return m.t.cap
}
`

// collBench4Module is written to std/collbench4/collbench4.mk in the copy.
const collBench4Module = `// Fixture of the COLL-R3 benchmark: std.core's calls for user code.

import std.core

export Twin(io: Io, m: HeapMap[int, int], a: &Alloc): (walk: i64, arrays: i64, seen: int) {
    return core.CollBench4Twin(io, m, a)
}

export Cap(m: &HeapMap[int, int]): int {
    return core.CollBench4Cap(m)
}
`

const bench4Program = `import std.collfix
import std.collbench4

// Emptied is a HeapMap[int, int] that held n keys, all removed.
Emptied(h: &Alloc, n: int): OutOfMemory!HeapMap[int, int] {
    var m := HeapMap[int, int].New(h)
    for i in 0..n {
        m.Set(h, i, i) else {
            m.Free(h)
            fail OutOfMemory
        }
    }
    for i in 0..n {
        _ = m.Remove(&i)
    }
    return m
}

// TextFree is the best of 5 times of TextMap.Free of an emptied map that
// held n keys: the n distinct 8-byte windows of a pseudo-random text.
TextFree(sys: &System, h: &Alloc, n: int): OutOfMemory!i64 {
    var src := HeapText.New(h)
    defer src.Free(h)
    var x: u64 = 7
    for _ in 0..n + 8 {
        x = x *% 6364136223846793005 +% 1442695040888963407
        c := Trunc[int](x >> 59)
        try src.Push(h, Letters[c..c + 1])
    }
    var best: i64 = 1 << 62
    for _ in 0..5 {
        var tm := TextMap[int].New(h)
        for i in 0..n {
            _ = tm.Ensure(h, src.Str()[i..i + 8], i) else {
                tm.Free(h)
                fail OutOfMemory
            }
        }
        for i in 0..n {
            _ = tm.Remove(h, src.Str()[i..i + 8])
        }
        t0 := collfix.Now(sys.io)
        tm.Free(h)
        t1 := collfix.Now(sys.io)
        best = Min(best, t1 - t0)
    }
    return best
}

const Letters: str = "abcdefghijklmnopqrstuvwxyzABCDEF"

Min(a: i64, b: i64): i64 {
    if a < b {
        return a
    }
    return b
}

export Main(sys: &System): WriteError|OutOfMemory!void {
    var out := sys.Stdout()
    h := sys.heap.Alloc()
    for e in 10..22 {
        cap := 1 << e
        n := cap / 2
        var free: i64 = 1 << 62
        var walk: i64 = 1 << 62
        var arrays: i64 = 1 << 62
        var text: i64 = 1 << 62
        var seen := 0
        var got := 0
        for _ in 0..5 {
            var m := try Emptied(h, n)
            got = collbench4.Cap(&m)
            t0 := collfix.Now(sys.io)
            m.Free(h)
            t1 := collfix.Now(sys.io)
            free = Min(free, t1 - t0)
            twin := try Emptied(h, n)
            r := collbench4.Twin(sys.io, twin, h)
            walk = Min(walk, r.walk)
            arrays = Min(arrays, r.arrays)
            seen += r.seen
        }
        text = try TextFree(sys, h, n)
        try Print(mut out, "cap {got} free_ns {free} arrays_ns {arrays} old_walk_ns {walk} seen {seen} textmap_free_ns {text}\n", (got, free, arrays, walk, seen, text))
    }
}
`

// TestStdColl_BenchEmptiedFree logs the numbers and asserts no
// timing, because the machine's load moves frees of this size by tens of
// percent.
func TestStdColl_BenchEmptiedFree(t *testing.T) {
	if os.Getenv("MK_BENCH") != "1" {
		t.Skip("set MK_BENCH=1 to run the std-coll benchmarks")
	}
	extra := map[string]string{"core/tests_collbench4.mk": collBench4Core, "collbench4/collbench4.mk": collBench4Module}
	mk := collExec(t, collBuildWith(t, extra, bench4Program, "--release"))
	t.Logf("machine: %s/%s, %d CPUs, %s", runtime.GOOS, runtime.GOARCH, runtime.NumCPU(), runtime.Version())
	t.Logf("mk build --release, best of 5 per row:\n%s", mk)
}
