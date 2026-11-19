// Why: std's maps, sort and text scans need timings next to Go's for the same
// work.
// What: TestStdColl_Bench: Map lookups and inserts, str-key lookups, Sort, and
// the Lines and Split scans.
// How: built with mk build --release and timed inside through IoTable.now
// (std.collfix's Now); numbers go to the test log. Skips unless MK_BENCH=1.

package std

import (
	"bytes"
	"fmt"
	"os"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

const benchProgram = `import std.collfix

Less(a: &int, b: &int): bool {
    return *a < *b
}

MapGet(sys: &System, out: mut Writer, n: int): WriteError|OutOfMemory!void {
    var a := try sys.arena.Sub()
    defer a.Free()
    var m := Map[int, int].New(&a)
    var s: u64 = 1
    t0 := collfix.Now(sys.io)
    for i in 0..n {
        s = s *% 6364136223846793005 +% 1442695040888963407
        try m.Set(Trunc[int](s >> 1), i)
    }
    t1 := collfix.Now(sys.io)
    reps := 4000000 / n
    var sum := 0
    for _ in 0..reps {
        s = 1
        for _ in 0..n {
            s = s *% 6364136223846793005 +% 1442695040888963407
            k := Trunc[int](s >> 1)
            if v := m.Get(&k) {
                sum += *v
            }
        }
    }
    t2 := collfix.Now(sys.io)
    set := (t1 - t0) / Trunc[i64](n)
    get := (t2 - t1) / Trunc[i64](reps * n)
    try Print(out, "map-int n {n} set_ns {set} get_ns {get} {sum}\n", (n, set, get, sum))
}

MapStr(sys: &System, out: mut Writer, n: int, pad: str): WriteError|OutOfMemory!void {
    var a := try sys.arena.Sub()
    defer a.Free()
    var kl := List[str].New(&a)
    for i in 0..n {
        try kl.Push(try a.Fmt("{pad}key{i}", (pad, i)))
    }
    keys := kl.Done()
    var m := Map[str, int].New(&a)
    for i in 0..n {
        try m.Set(keys[i], i)
    }
    reps := 2000000 / n
    t1 := collfix.Now(sys.io)
    var sum := 0
    for _ in 0..reps {
        for i in 0..n {
            if v := m.Get(&keys[i]) {
                sum += *v
            }
        }
    }
    t2 := collfix.Now(sys.io)
    get := (t2 - t1) / Trunc[i64](reps * n)
    l := keys[0].Len()
    try Print(out, "map-str n {n} len {l} get_ns {get} {sum}\n", (n, l, get, sum))
}

Sorting(sys: &System, out: mut Writer, n: int): WriteError|OutOfMemory!void {
    var a := try sys.arena.Sub()
    defer a.Free()
    var xs := try a.Slice[int](n)
    var best: i64 = 9223372036854775807
    for _ in 0..5 {
        var s: u64 = 1
        for i in 0..n {
            s = s *% 6364136223846793005 +% 1442695040888963407
            xs[i] = Trunc[int](s >> 1)
        }
        t0 := collfix.Now(sys.io)
        xs.Sort(Less)
        t1 := collfix.Now(sys.io)
        if t1 - t0 < best {
            best = t1 - t0
        }
    }
    try Print(out, "sort n {n} ns {best}\n", (n, best))
}

Scan(sys: &System, out: mut Writer, width: int): WriteError|OutOfMemory!void {
    var a := try sys.arena.Sub()
    defer a.Free()
    var t := Text.New(&a)
    var line := Text.New(&a)
    for i in 0..width {
        try line.PushChar(Cast[char](97 + i % 26))
    }
    for _ in 0..(8000000 / (width + 1)) {
        try t.Push(line.Str())
        try t.Push("\n")
    }
    s := t.Str()
    t1 := collfix.Now(sys.io)
    var count := 0
    for _ in 0..10 {
        var it := s.Lines()
        for l := it.Next() {
            count += 1 + l.Len()
        }
    }
    t2 := collfix.Now(sys.io)
    total := Trunc[i64](s.Len() * 10)
    ps := (t2 - t1) * 1000 / total
    try Print(out, "lines width {width} ps_per_byte {ps} {count}\n", (width, ps, count))
}

SliceWork(sys: &System, out: mut Writer): WriteError|OutOfMemory!void {
    var a := try sys.arena.Sub()
    defer a.Free()
    n := 1 << 20
    var b := try a.Slice[u8](n)
    var c := try a.Slice[u8](n)
    reps := 200
    var best: [4]i64 = [9223372036854775807; 4]
    var sink: u64 = 0
    for _ in 0..5 {
        t0 := collfix.Now(sys.io)
        for r in 0..reps {
            b.Fill(Trunc[u8](r))
        }
        t1 := collfix.Now(sys.io)
        for r in 0..reps {
            c.CopyFrom(b[..])
            c[r] = 1
        }
        t2 := collfix.Now(sys.io)
        c[n - 1] = 0xEE
        for _ in 0..reps {
            if i := c.Index(&0xEE) {
                sink +%= Trunc[u64](i)
            }
        }
        t3 := collfix.Now(sys.io)
        for _ in 0..reps {
            sink +%= collfix.HashBytes(b[..])
        }
        t4 := collfix.Now(sys.io)
        ts := [t1 - t0, t2 - t1, t3 - t2, t4 - t3]
        for k in 0..4 {
            if ts[k] < best[k] {
                best[k] = ts[k]
            }
        }
    }
    total := Trunc[i64](reps * n)
    fill := best[0] * 1000 / total
    copy := best[1] * 1000 / total
    index := best[2] * 1000 / total
    hash := best[3] * 1000 / total
    try Print(out, "slice ps_per_byte fill {fill} copy {copy} index-u8 {index} hasher-bytes {hash} {sink}\n", (fill, copy, index, hash, sink))
}

export Main(sys: &System): WriteError|OutOfMemory!void {
    var out := sys.Stdout()
    for n in [1000, 65536, 1000000] {
        try MapGet(sys, mut out, n)
    }
    try MapStr(sys, mut out, 1000, "")
    try MapStr(sys, mut out, 1000, "abcdefghijklmnopqrstuvwxyz012345")
    try Sorting(sys, mut out, 1000000)
    for w in [10, 80, 1000] {
        try Scan(sys, mut out, w)
    }
    try SliceWork(sys, mut out)
}
`

// goBench does benchProgram's work in Go and returns the same lines.
func goBench() string {
	var b strings.Builder
	for _, n := range []int{1000, 65536, 1000000} {
		// Inserts: whole maps built until 50 ms have passed, so the time is
		// far above the clock's resolution (Windows' time.Now ticks are
		// coarse: one map of 1,000 inserts read 0 ns).
		var m map[int]int
		var s uint64
		built := 0
		t0 := time.Now()
		for time.Since(t0) < 50*time.Millisecond {
			m = map[int]int{}
			s = 1
			for i := 0; i < n; i++ {
				s = s*6364136223846793005 + 1442695040888963407
				m[int(s>>1)] = i
			}
			built++
		}
		set := time.Since(t0).Nanoseconds() / int64(built*n)
		t1 := time.Now()
		reps := 4000000 / n
		sum := 0
		for r := 0; r < reps; r++ {
			s = 1
			for i := 0; i < n; i++ {
				s = s*6364136223846793005 + 1442695040888963407
				if v, ok := m[int(s>>1)]; ok {
					sum += v
				}
			}
		}
		t2 := time.Now()
		fmt.Fprintf(&b, "map-int n %d set_ns %d get_ns %d %d\n", n, set, t2.Sub(t1).Nanoseconds()/int64(reps*n), sum)
	}
	for _, pad := range []string{"", "abcdefghijklmnopqrstuvwxyz012345"} {
		n := 1000
		keys := make([]string, n)
		m := map[string]int{}
		for i := range keys {
			keys[i] = fmt.Sprintf("%skey%d", pad, i)
			m[keys[i]] = i
		}
		reps := 2000000 / n
		sum := 0
		t1 := time.Now()
		for r := 0; r < reps; r++ {
			for i := range keys {
				if v, ok := m[keys[i]]; ok {
					sum += v
				}
			}
		}
		fmt.Fprintf(&b, "map-str n %d len %d get_ns %d %d\n", n, len(keys[0]), time.Since(t1).Nanoseconds()/int64(reps*n), sum)
	}
	{
		n := 1000000
		xs := make([]int, n)
		best := time.Duration(1 << 62)
		for r := 0; r < 5; r++ {
			var s uint64 = 1
			for i := range xs {
				s = s*6364136223846793005 + 1442695040888963407
				xs[i] = int(s >> 1)
			}
			t0 := time.Now()
			slices.SortFunc(xs, func(a, b int) int {
				if a < b {
					return -1
				}
				if a > b {
					return 1
				}
				return 0
			})
			best = min(best, time.Since(t0))
		}
		fmt.Fprintf(&b, "sort n %d ns %d (slices.SortFunc)\n", n, best.Nanoseconds())
	}
	for _, w := range []int{10, 80, 1000} {
		line := make([]byte, w)
		for i := range line {
			line[i] = byte('a' + i%26)
		}
		var t strings.Builder
		for i := 0; i < 8000000/(w+1); i++ {
			t.Write(line)
			t.WriteByte('\n')
		}
		s := t.String()
		t1 := time.Now()
		count, scans := 0, 0
		for time.Since(t1) < 50*time.Millisecond || scans < 10 {
			rest := s
			for len(rest) > 0 {
				i := strings.IndexByte(rest, '\n')
				if i < 0 {
					count += 1 + len(rest)
					break
				}
				count += 1 + i
				rest = rest[i+1:]
			}
			scans++
		}
		fmt.Fprintf(&b, "lines width %d ps_per_byte %d (%d scans) %d\n", w, time.Since(t1).Nanoseconds()*1000/int64(len(s)*scans), scans, count)
	}
	{
		// Each pass repeats until 50 ms have passed (Windows' time.Now is
		// coarse), best of 3.
		n := 1 << 20
		bb := make([]byte, n)
		cc := make([]byte, n)
		var sink uint64
		rate := func(f func(r int)) int64 {
			best := int64(1 << 62)
			for k := 0; k < 3; k++ {
				t0 := time.Now()
				passes := 0
				for time.Since(t0) < 50*time.Millisecond || passes < 10 {
					f(passes)
					passes++
				}
				best = min(best, time.Since(t0).Nanoseconds()*1000/int64(passes*n))
			}
			return best
		}
		fill := rate(func(r int) {
			for i := range bb {
				bb[i] = byte(r)
			}
		})
		cp := rate(func(r int) {
			copy(cc, bb)
			cc[r%n] = 1
		})
		cc[n-1] = 0xEE
		for i := range cc[:n-1] {
			if cc[i] == 0xEE {
				cc[i] = 0
			}
		}
		index := rate(func(int) { sink += uint64(bytes.IndexByte(cc, 0xEE)) })
		hash := rate(func(int) { sink += sip13(bb) })
		fmt.Fprintf(&b, "slice ps_per_byte fill %d (a byte loop) copy %d (copy) index-u8 %d (bytes.IndexByte) hasher-bytes %d (Go sip13 of the tests) %d\n", fill, cp, index, hash, sink)
	}
	return b.String()
}

// TestStdColl_Bench logs std's numbers and Go's for the same work.
func TestStdColl_Bench(t *testing.T) {
	if os.Getenv("MK_BENCH") != "1" {
		t.Skip("set MK_BENCH=1 to run the std-coll benchmarks")
	}
	mk := collRun(t, benchProgram, "--release")
	t.Logf("machine: %s/%s, %d CPUs, %s", runtime.GOOS, runtime.GOARCH, runtime.NumCPU(), runtime.Version())
	t.Logf("mk build --release:\n%s", mk)
	t.Logf("Go:\n%s", goBench())
}
