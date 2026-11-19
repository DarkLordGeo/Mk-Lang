// Why: a Reset loop whose requests are all over next/2 must reach a steady
// state with no heap traffic, so BumpSlow's own-chunk path grows next too.
// What: the growth property on the Go model (old and new policy), the arena's
// code at compile time over the counting heap, and a release benchmark.
// How: the benchmark compares 511- and 513-int Slice-plus-Reset kernels.

package std

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
)

// amCycles runs cycles of k requests of size bytes (align 8) then a Reset
// on the Go model, and returns the heap allocations of each cycle.
func amCycles(size, k, cycles int, old bool) []int64 {
	m := &amModel{heapCap: 1 << 50, oldOwnChunk: old}
	if !m.heapAlloc(amFirst) {
		panic("model heap")
	}
	m.cur = m.newChunk(amFirst, true)
	m.size, m.next = amFirst-amHdrRoom, 2*amFirst
	out := make([]int64, cycles)
	for c := 0; c < cycles; c++ {
		before := m.allocs
		for i := 0; i < k; i++ {
			if _, ok := m.alloc(size, 8); !ok {
				panic("model heap full")
			}
		}
		out[c] = m.allocs - before
		m.reset()
	}
	return out
}

// steadyFrom is the first cycle from which every cycle allocated nothing,
// or -1.
func steadyFrom(counts []int64) int {
	from := -1
	for i, n := range counts {
		if n != 0 {
			from = -1
		} else if from < 0 {
			from = i
		}
	}
	return from
}

// TestSTDMEM_ArenaGrowthPolicyModel: on the Go model of the arena's policy
// (std-mem_arenamodel_test.go, which TestStdMem_ArenaModel holds to the Mk
// code), every fixed per-cycle workload whose requests are at most 512 KiB
// and whose cycle fits one 1 MiB chunk reaches a cycle with no heap
// allocation, and stays there, within 12 cycles. Over 512 KiB every request
// keeps its own chunk (one allocation each, every cycle: the documented
// limit). The older policy (an own chunk left next unchanged) fails the
// property for 5 x 4,104 bytes and 5 x 8,000 bytes (5 allocations every
// cycle), so the property sees the bug.
func TestSTDMEM_ArenaGrowthPolicyModel(t *testing.T) {
	t.Parallel()
	sizes := []int{1, 8, 1000, 2040, 2048, 4088, 4096, 4104, 4112, 8000, 8192, 8200, 16384, 40000, 100000, 262144, 262152, 400000, 524288}
	for _, size := range sizes {
		for _, k := range []int{1, 2, 3, 5, 17, 100} {
			if (size+7)/8*8*k > amMaxChunk-amRoom {
				continue
			}
			counts := amCycles(size, k, 16, false)
			if f := steadyFrom(counts); f < 0 || f > 12 {
				t.Errorf("%d x %d bytes per cycle: allocations per cycle %v, no steady state by cycle 12", k, size, counts)
			}
		}
	}
	for _, k := range []int{1, 3} {
		counts := amCycles(524296, k, 8, false)
		for c, n := range counts {
			if n != int64(k) {
				t.Errorf("%d x 524,296 bytes: cycle %d made %d allocations, want %d (own chunks)", k, c, n, k)
			}
		}
	}
	for _, size := range []int{4104, 8000} {
		counts := amCycles(size, 5, 16, true)
		if steadyFrom(counts) >= 0 || counts[15] != 5 {
			t.Errorf("old policy, 5 x %d bytes: %v; want 5 allocations every cycle (the finding)", size, counts)
		}
	}
}

// growFixture is TestArenaLifecycleCounted's counting heap with room for the larger
// cycles, and SliceCycles(m): six cycles of five a.Slice[i64](m), each
// checked zero-filled and written, then a.Reset(); it returns the number of
// failed checks plus 1,000 per cycle from cycle 3 on that allocated (so a
// const can name the count). The heap never reuses a freed block, so 262,144
// bytes bounds the sum of every allocation of one call.
var growFixture = strings.Replace(countingHeap, "const CountBytes = 65536", "const CountBytes = 262144", 1) + `
SliceCycles(m: int): int {
    var mem: [262144]u8 = [0; 262144]
    unsafe "fixture" {
        ctx := raw.DataMut(mut mem[..])
        heap := MakeHeap(ctx, raw.Of(&CountTable))
        var a := Arena.New(&heap) else { return 90 }
        var bad := 0
        var counts: [6]i64 = [0; 6]
        for k in 0..6 {
            before := CountGet(ctx, 1)
            for i in 0..5 {
                xs := a.Slice[i64](m) else {
                    a.Free()
                    return 91
                }
                if xs.Len() != m || xs[0] != 0 || xs[m - 1] != 0 {
                    bad += 1
                }
                xs[0] = i64(i + 1)
                xs[m - 1] = i64(k + 1)
                if xs[0] != i64(i + 1) {
                    bad += 1
                }
            }
            counts[k] = CountGet(ctx, 1) - before
            a.Reset()
        }
        for k in 3..6 {
            if counts[k] != 0 {
                bad += 1000
            }
        }
        if counts[0] == 0 {
            bad += 1 // the workload outgrows the first chunk
        }
        a.Free()
        if CountGet(ctx, 2) != CountGet(ctx, 1) || CountGet(ctx, 3) != 0 {
            bad += 1 // every allocation freed, every byte returned
        }
        return bad
    }
}

const Grow511 = [0; 1][SliceCycles(511)]
const Grow513 = [0; 1][SliceCycles(513)]
const Grow1000 = [0; 1][SliceCycles(1000)]
const Grow2000 = [0; 1][SliceCycles(2000)]
`

// TestSTDMEM_ArenaSliceResetSteady runs the arena's code at compile time:
// five Slice[i64](m) per cycle then Reset, for m = 511 (4,088 bytes, the
// control under next/2), 513 (4,104 bytes, the failing case), 1,000 and
// 2,000; from cycle 3 on no cycle allocates. Before the fix, 513 and 1,000
// made 5 allocations in every cycle, so the consts
// failed (index 5000 of a one-element array).
func TestSTDMEM_ArenaSliceResetSteady(t *testing.T) {
	t.Parallel()
	expectClean(t, "mem_grow", growFixture)
}

// TestSTDMEM_ArenaSliceResetSteadySeesOldPolicy: the same fixture over
// mem_arena.mk with the own-chunk growth taken out fails, at the 513 and
// 1,000 consts and not at 511's.
func TestSTDMEM_ArenaSliceResetSteadySeesOldPolicy(t *testing.T) {
	t.Parallel()
	src := readText(t, "core/mem_arena.mk")
	old := strings.Replace(src, "            (*h).next = n\n            return raw.Offset", "            return raw.Offset", 1)
	if old == src {
		t.Fatal("core/mem_arena.mk: the own-chunk growth line was not found")
	}
	out, code := mkCheckWith(t, map[string]string{"core/mem_arena.mk": old, "core/tests_mem_grow.mk": growFixture})
	if code == 0 {
		t.Fatalf("the old policy checked clean:\n%s", out)
	}
	at := func(c string) string {
		i := strings.Index(growFixture, "\nconst "+c+" ")
		return "std/core/tests_mem_grow.mk:" + strconv.Itoa(strings.Count(growFixture[:i+1], "\n")+1) + ":"
	}
	for _, c := range []string{"Grow513", "Grow1000"} {
		if !strings.Contains(out, at(c)) {
			t.Errorf("no diagnostic at %s (%s):\n%s", c, at(c), out)
		}
	}
	if strings.Contains(out, at("Grow511")) {
		t.Errorf("511, under next/2, failed under the old policy too:\n%s", out)
	}
}

// sliceResetProgram is the kernel that showed the bug: n Slice[int](m), each read at
// an index from a generator (so release keeps the fill), a Reset every 5.
func sliceResetProgram(m, n int) string {
	return `export Main(sys: &System): WriteError|OutOfMemory!void {
    var out := sys.Stdout()
    var ar := try Arena.New(sys.heap)
    var s: u64 = 0
    var g: u64 = 1
    for i in 0..` + strconv.Itoa(n) + ` {
        xs := ar.Slice[int](` + strconv.Itoa(m) + `) else {
            ar.Free()
            return
        }
        g = g *% 6364136223846793005 +% 1442695040888963407
        s = s +% Trunc[u64](i + xs[Trunc[int](g >> 55) % ` + strconv.Itoa(m) + `])
        if i % 5 == 4 {
            ar.Reset()
        }
    }
    ar.Free()
    try Print(mut out, "{s}\n", (s))
}
`
}

// TestSTDMEM_SliceResetKernelRuns runs the kernel at a small count, dev
// and release, so the benchmark's program is checked in every run.
func TestSTDMEM_SliceResetKernelRuns(t *testing.T) {
	const n = 3000
	want := fmt.Sprintf("%d\n", uint64(n)*(n-1)/2)
	for _, m := range []int{511, 513} {
		for _, flags := range [][]string{nil, {"--release"}} {
			t.Run(fmt.Sprintf("%d%v", m, flags), func(t *testing.T) {
				wantRun(t, runNative(t, buildNative(t, nil, sliceResetProgram(m, n), flags...), ""), 0, want)
			})
		}
	}
}

// TestSTDMEM_SliceResetBench times the kernel at release, 5,000,000
// slices (1,000,000 Reset cycles), best of 5, for 511 and 513 ints, over
// the live mem_arena.mk and over the old policy (own chunks leave next).
// MK_BENCH=1 only; numbers are logged, to be recorded with the machine.
func TestSTDMEM_SliceResetBench(t *testing.T) {
	if os.Getenv("MK_BENCH") != "1" {
		t.Skip("set MK_BENCH=1 to time the Slice-plus-Reset kernel")
	}
	const n = 5_000_000
	src := readText(t, "core/mem_arena.mk")
	old := strings.Replace(src, "            (*h).next = n\n            return raw.Offset", "            return raw.Offset", 1)
	if old == src {
		t.Fatal("core/mem_arena.mk: the own-chunk growth line was not found")
	}
	want := fmt.Sprintf("%d\n", uint64(n)*(n-1)/2)
	for _, policy := range []string{"new", "old"} {
		var fix map[string]string
		if policy == "old" {
			fix = map[string]string{"core/mem_arena.mk": old}
		}
		for _, m := range []int{511, 513} {
			exe := buildNative(t, fix, sliceResetProgram(m, n), "--release")
			b := -1.0
			for i := 0; i < 5; i++ {
				r := runNative(t, exe, "")
				wantRun(t, r, 0, want)
				if s := r.took.Seconds(); b < 0 || s < b {
					b = s
				}
			}
			t.Logf("%s policy, Slice[int](%d) x %d, Reset every 5: best of 5 %.3f s, %.2f ns per slice", policy, m, n, b, b*1e9/n)
		}
	}
}
