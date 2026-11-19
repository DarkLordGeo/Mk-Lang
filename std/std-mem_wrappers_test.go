// Why: Limit, Failing and Fallback each decide, per request, whether the
// allocator beneath them is asked at all; a count or a number off by one
// leaks a budget, skips a failure or frees a block to the wrong side.
// What: the three against their Go models (std-mem_heapmodel_test.go) over
// seeded tapes, and planted bugs the comparison must see.
// How: each wraps a FixedHeap, whose model predicts every address, so a
// run's lines are predicted whole: where each block lies, and the counts
// after every op. A Fallback's blocks print as an offset in its buffer or
// "i" and an offset in the FixedHeap's.

package std

import (
	"fmt"
	"strings"
	"testing"
)

// wrapCase is one run of a wrapper over a FixedHeap of inner bytes at an
// address shift, with its tape.
type wrapCase struct {
	name         string
	inner, shift int
	seed         uint64
	ops, maxSize int
	alloc        int
	// Limit: the limit in bytes.
	max int
	// Failing: the request number, and whether NewFrom makes it.
	at   int
	from bool
	// Fallback: the buffer's length and address shift, and whether the
	// tape resets.
	buf, bufShift int
	resets        bool
}

// limitCases: a limit that refuses everything, limits that bind long
// before the heap does, one the heap binds before, and one that never
// binds.
var limitCases = []wrapCase{
	{name: "limit-negative", inner: 4096, shift: 1, seed: 31, ops: 200, maxSize: 64, alloc: 50, max: -5},
	{name: "limit-zero", inner: 4096, shift: 0, seed: 32, ops: 200, maxSize: 64, alloc: 50, max: 0},
	{name: "limit-one", inner: 4096, shift: 3, seed: 33, ops: 300, maxSize: 3, alloc: 50, max: 1},
	{name: "limit-tight", inner: 16384, shift: 2, seed: 34, ops: 1500, maxSize: 60, alloc: 50, max: 100},
	{name: "limit-k1", inner: 16384, shift: 5, seed: 35, ops: 2000, maxSize: 200, alloc: 45, max: 1000},
	{name: "limit-k4", inner: 16384, shift: 7, seed: 36, ops: 2000, maxSize: 300, alloc: 45, max: 4000},
	{name: "limit-heap-first", inner: 1024, shift: 4, seed: 37, ops: 1500, maxSize: 200, alloc: 50, max: 4000},
	{name: "limit-loose", inner: 16384, shift: 6, seed: 38, ops: 1500, maxSize: 300, alloc: 45, max: 1 << 40},
}

// failingCases: no failure, the first request, requests in the middle of a
// run, one past its end, and the same numbers failing from there on.
var failingCases = []wrapCase{
	{name: "failing-none", inner: 16384, shift: 1, seed: 41, ops: 600, maxSize: 200, alloc: 45, at: -1},
	{name: "failing-first", inner: 16384, shift: 0, seed: 42, ops: 600, maxSize: 200, alloc: 45, at: 0},
	{name: "failing-second", inner: 16384, shift: 3, seed: 43, ops: 600, maxSize: 200, alloc: 45, at: 1},
	{name: "failing-mid", inner: 16384, shift: 2, seed: 44, ops: 1200, maxSize: 200, alloc: 45, at: 250},
	{name: "failing-past", inner: 16384, shift: 5, seed: 45, ops: 600, maxSize: 200, alloc: 45, at: 100000},
	{name: "failing-from-none", inner: 16384, shift: 7, seed: 46, ops: 600, maxSize: 200, alloc: 45, at: -3, from: true},
	{name: "failing-from-first", inner: 16384, shift: 4, seed: 47, ops: 300, maxSize: 200, alloc: 45, at: 0, from: true},
	{name: "failing-from-mid", inner: 16384, shift: 6, seed: 48, ops: 1200, maxSize: 200, alloc: 45, at: 200, from: true},
}

// fallbackCases: no buffer, buffers too small for any block, and buffers a
// tape fills many times over, each before a FixedHeap of 32 KiB.
var fallbackCases = []wrapCase{
	{name: "fallback-empty", inner: 32768, shift: 1, seed: 51, ops: 600, maxSize: 200, alloc: 42, buf: 0, bufShift: 0, resets: true},
	{name: "fallback-one", inner: 32768, shift: 0, seed: 52, ops: 600, maxSize: 200, alloc: 42, buf: 1, bufShift: 1, resets: true},
	{name: "fallback-seven", inner: 32768, shift: 3, seed: 53, ops: 600, maxSize: 60, alloc: 42, buf: 7, bufShift: 2, resets: true},
	{name: "fallback-64", inner: 32768, shift: 2, seed: 54, ops: 1500, maxSize: 60, alloc: 42, buf: 64, bufShift: 3, resets: true},
	{name: "fallback-256", inner: 32768, shift: 5, seed: 55, ops: 2000, maxSize: 120, alloc: 42, buf: 256, bufShift: 0, resets: true},
	{name: "fallback-k1", inner: 32768, shift: 7, seed: 56, ops: 2500, maxSize: 200, alloc: 42, buf: 1024, bufShift: 5, resets: false},
	{name: "fallback-page", inner: 32768, shift: 4, seed: 57, ops: 2500, maxSize: 300, alloc: 42, buf: 4096, bufShift: 7, resets: true},
}

// wrapOps is the tape of case c: allocations of bad sizes and alignments
// are mixed in, and resets only where the case asks.
func wrapOps(c wrapCase) []int {
	return heapOps(c.seed, c.ops, c.maxSize, c.alloc, c.resets, memWildAllocs)
}

// wrapProgram runs every case of the three lists in one program.
func wrapProgram() string {
	var b strings.Builder
	b.WriteString("import std.memnew\n\nexport Main(sys: &System): WriteError!void {\n    var out := sys.Stdout()\n")
	n := 0
	emit := func(c wrapCase, call func(i int) string) {
		fmt.Fprintf(&b, "    try Print(mut out, \"case %s\\n\")\n", c.name)
		fmt.Fprintf(&b, "    var in%d: [%d]u8 = [0; %d]\n", n, c.inner+c.shift+1, c.inner+c.shift+1)
		fmt.Fprintf(&b, "    ops%d := %s\n", n, mkInts(wrapOps(c)))
		b.WriteString(call(n))
		n++
	}
	for _, c := range limitCases {
		emit(c, func(i int) string {
			return fmt.Sprintf("    try memnew.RunLimit(mut out, mut in%d[%d..%d], %d, ops%d[..])\n", i, c.shift, c.shift+c.inner, c.max, i)
		})
	}
	for _, c := range failingCases {
		emit(c, func(i int) string {
			return fmt.Sprintf("    try memnew.RunFailing(mut out, mut in%d[%d..%d], %d, %t, ops%d[..])\n", i, c.shift, c.shift+c.inner, c.at, c.from, i)
		})
	}
	for _, c := range fallbackCases {
		emit(c, func(i int) string {
			return fmt.Sprintf("    var buf%d: [%d]u8 = [0; %d]\n    try memnew.RunFallback(mut out, mut buf%d[%d..%d], mut in%d[%d..%d], ops%d[..])\n",
				i, c.buf+c.bufShift+1, c.buf+c.bufShift+1, i, c.bufShift, c.bufShift+c.buf, i, c.shift, c.shift+c.inner, i)
		})
	}
	b.WriteString("}\n")
	return b.String()
}

// heapBlocks is the tape's blocks that lie in the heap at base of the
// address space, as offsets from the model's own base, for heapOracle.
func heapBlocks(tape *memTape, m *heapModel, base int) []memBlk {
	var rel []memBlk
	for _, b := range tape.blks {
		if base == 0 || b.off >= base {
			rel = append(rel, memBlk{b.off - base - m.lead, b.size, b.align, -1})
		}
	}
	return rel
}

// limitExpect is the lines RunLimit must print for case c over a heap at
// skew: each op's line, then Bytes and Left.
func limitExpect(t *testing.T, c wrapCase, skew int) string {
	t.Helper()
	m := newHeapModel(skew, c.inner)
	l := &limitModel{inner: heapAt{m, 0}, max: max(c.max, 0)}
	tape := &memTape{a: l}
	tape.check = func([]int) {
		heapOracle(t, m, heapBlocks(tape, m, 0), nil)
		// The limit bounds what is live through it, and counts it exactly.
		live := 0
		for _, b := range tape.blks {
			live += b.size
		}
		if l.used != live || l.used > l.max {
			t.Fatalf("model limit: %d bytes counted, %d live, limit %d", l.used, live, l.max)
		}
	}
	fmt.Fprintf(&tape.out, "skew %d\n", skew)
	ops := wrapOps(c)
	for i := 0; i < len(ops); {
		i = tape.step(ops, i)
		fmt.Fprintf(&tape.out, " b=%d l=%d\n", l.used, l.max-l.used)
	}
	tape.finish()
	fmt.Fprintf(&tape.out, "end b=%d l=%d\n", l.used, l.max-l.used)
	return tape.out.String()
}

// failingExpect is the lines RunFailing must print for case c: each op's
// line, then Requests and Failed.
func failingExpect(t *testing.T, c wrapCase, skew int) string {
	t.Helper()
	m := newHeapModel(skew, c.inner)
	f := &failingModel{inner: heapAt{m, 0}, at: c.at, last: c.at}
	if c.from {
		f.last = 1 << 62
	}
	if c.at < 0 {
		f.at, f.last = 0, -1
	}
	tape := &memTape{a: f}
	tape.check = func([]int) { heapOracle(t, m, heapBlocks(tape, m, 0), nil) }
	fmt.Fprintf(&tape.out, "skew %d\n", skew)
	ops := wrapOps(c)
	for i := 0; i < len(ops); {
		i = tape.step(ops, i)
		fmt.Fprintf(&tape.out, " q=%d x=%d\n", f.reqs, f.fails)
	}
	tape.finish()
	fmt.Fprintf(&tape.out, "end q=%d x=%d\n", f.reqs, f.fails)
	return tape.out.String()
}

// fallbackExpect is the lines RunFallback must print for case c, with its
// buffer at skew and its heap at skew2. A reset frees every live block,
// then makes the buffer free.
func fallbackExpect(t *testing.T, c wrapCase, skew, skew2 int) string {
	t.Helper()
	fx := newFixedModel(skew, c.buf)
	m := newHeapModel(skew2, c.inner)
	fb := &fallbackModel{fixed: fixedAt{fx}, inner: heapAt{m, memSecond}}
	tape := &memTape{a: fb}
	tape.check = func([]int) {
		heapOracle(t, m, heapBlocks(tape, m, memSecond), nil)
		// The buffer's blocks are aligned, inside it and share no byte.
		var in []memBlk
		for _, b := range tape.blks {
			if b.off < memSecond {
				in = append(in, memBlk{b.off - fx.lead, b.size, b.align, -1})
			}
		}
		for j := range in {
			fixedOracle(t, fx.skew, fx.size, in, j)
		}
	}
	fmt.Fprintf(&tape.out, "skew %d %d\n", skew, skew2)
	ops := wrapOps(c)
	for i := 0; i < len(ops); {
		if ops[i] == 3 {
			tape.finish()
			fx.used = 0
			tape.out.WriteString("z\n")
			i++
			continue
		}
		i = tape.step(ops, i)
		tape.out.WriteString("\n")
	}
	tape.finish()
	if spans := m.walk(t); len(spans) != 1 || !spans[0].free {
		t.Fatalf("model fallback: the heap holds %d blocks after every block was freed", len(spans))
	}
	tape.out.WriteString("end\n")
	return tape.out.String()
}

// wrapExpect is case c's expected output, from the skews its own output
// starts with.
func wrapExpect(t *testing.T, c wrapCase, g string) string {
	t.Helper()
	skews, _ := memSkews(t, c.name, g)
	switch {
	case strings.HasPrefix(c.name, "limit-"):
		return limitExpect(t, c, skews[0])
	case strings.HasPrefix(c.name, "failing-"):
		return failingExpect(t, c, skews[0])
	}
	if len(skews) != 2 {
		t.Fatalf("case %s: want two skews, have %v", c.name, skews)
	}
	return fallbackExpect(t, c, skews[0], skews[1])
}

// wrapCases is every case of the three lists.
func wrapCases() []wrapCase {
	return append(append(append([]wrapCase{}, limitCases...), failingCases...), fallbackCases...)
}

// TestSTDMEM_WrappersMatchModels: seeded tapes through a Limit, a Failing
// and a Fallback, each over a FixedHeap, print exactly what the models
// predict, dev and release: where every block lies, a Limit's Bytes and
// Left and a Failing's Requests and Failed after every op, and zero bytes
// and an empty heap at the end. Each kind of line is reached often: a
// Limit refuses and grants, a Failing fails resizes as well as
// allocations, and a Fallback's blocks land on both sides.
func TestSTDMEM_WrappersMatchModels(t *testing.T) {
	t.Parallel()
	src := wrapProgram()
	for _, flags := range [][]string{nil, {"--release"}} {
		out := collExec(t, memNewBuild(t, src, flags...))
		if strings.Contains(out, "corrupt") {
			t.Errorf("%v: a block's contents changed under another request:\n%.2000s", flags, out)
		}
		got := splitCases(out)
		reach := map[string]int{}
		for _, c := range wrapCases() {
			g, ok := got[c.name]
			if !ok {
				t.Fatalf("%v: no output for case %s\n%.2000s", flags, c.name, out)
			}
			if want := wrapExpect(t, c, g); g != want {
				t.Errorf("%v case %s:", flags, c.name)
				diffLines(t, g, want)
			}
			kind, _, _ := strings.Cut(c.name, "-")
			for _, l := range strings.Split(g, "\n") {
				switch {
				case strings.HasPrefix(l, "a null"):
					reach[kind+" refused"]++
				case strings.HasPrefix(l, "a i"):
					reach[kind+" granted outside"]++
				case strings.HasPrefix(l, "a "):
					reach[kind+" granted"]++
				case strings.HasPrefix(l, "r ") && strings.Contains(l, " true"):
					reach[kind+" resized"]++
				case strings.HasPrefix(l, "r "):
					reach[kind+" resize refused"]++
				}
			}
		}
		t.Logf("%v: %v", flags, reach)
		for _, k := range []string{
			"limit refused", "limit granted", "limit resized", "limit resize refused",
			"failing refused", "failing granted", "failing resized", "failing resize refused",
			"fallback granted", "fallback granted outside", "fallback resized", "fallback resize refused",
		} {
			if reach[k] < 200 {
				t.Errorf("%v: the tapes reach %q only %d times, want at least 200", flags, k, reach[k])
			}
		}
	}
}

// wrapPlants are wrong wrappers the differential test must tell from the
// real ones.
var wrapPlants = []memPlant{
	{"limit-resize-not-charged", "mem/limit.mk",
		"    l.used.Store(used +% d)\n    return true",
		"    l.used.Store(used +% d -% d)\n    return true"},
	{"limit-free-gives-nothing-back", "mem/limit.mk",
		"    l.used.Store(l.used.Load() -% size)",
		"    l.used.Store(l.used.Load() -% size +% size)"},
	{"limit-refuses-the-last-byte", "mem/limit.mk",
		"    if size < 0 || size > l.max -% used {",
		"    if size < 0 || size >= l.max -% used {"},
	{"limit-charges-a-refused-request", "mem/limit.mk",
		"    p := core.AllocBytes(l.inner, size, align)\n    if !raw.IsNull(p) {\n        l.used.Store(used +% size)\n    }",
		"    p := core.AllocBytes(l.inner, size, align)\n    if !raw.IsNull(p) || size > 0 {\n        l.used.Store(used +% size)\n    }"},
	{"failing-fails-a-resize-to-the-same-size", "mem/failing.mk",
		"    if new > old && Picked(f) {",
		"    if new >= old && Picked(f) {"},
	{"failing-fails-one-request-late", "mem/failing.mk",
		"    if n < f.at || n > f.last {",
		"    if n <= f.at || n > f.last +% 1 {"},
	{"failing-counts-no-failure", "mem/failing.mk",
		"    f.nfails.Store(f.nfails.Load() +% 1)\n    return true",
		"    f.nfails.Store(f.nfails.Load())\n    return true"},
	{"failing-asks-inner-anyway", "mem/failing.mk",
		"    if Picked(f) {\n        return raw.Null[u8]()\n    }\n    return core.AllocBytes(f.inner, size, align)",
		"    p := core.AllocBytes(f.inner, size, align)\n    if Picked(f) {\n        return raw.Null[u8]()\n    }\n    return p"},
	{"fallback-zero-bytes-from-the-buffer", "mem/fallback.mk",
		"    if size > 0 {\n        p := FixedAllocEntry",
		"    if size >= 0 {\n        p := FixedAllocEntry"},
	{"fallback-half-the-buffer-not-its-own", "mem/fallback.mk",
		"    return raw.Addr(p) -% raw.Addr(b.fixed.base) < b.lim",
		"    return raw.Addr(p) -% raw.Addr(b.fixed.base) < b.lim >> 1"},
	{"fallback-resize-always-inner", "mem/fallback.mk",
		"    if InBuffer(b, p) {\n        return FixedResizeEntry(FixedCtx(b), p, old, new, align)\n    }",
		"    if InBuffer(b, p) && new < 0 {\n        return FixedResizeEntry(FixedCtx(b), p, old, new, align)\n    }"},
	{"fallback-never-falls-back", "mem/fallback.mk",
		"    return core.AllocBytes(b.inner, InnerSize(size), align)\n}",
		"    return core.AllocBytes(b.inner, InnerSize(size) -% InnerSize(size) -% 1, align)\n}"},
}

// TestSTDMEM_WrapperPlantsAreSeen: each planted bug, built into a copy of
// std, makes the program of TestSTDMEM_WrappersMatchModels print something
// the models do not, crash or hang; the same program over the unchanged
// std agrees with them.
func TestSTDMEM_WrapperPlantsAreSeen(t *testing.T) {
	t.Parallel()
	src := wrapProgram()
	agrees := func(t *testing.T, out string) bool {
		got := splitCases(out)
		for _, c := range wrapCases() {
			g, ok := got[c.name]
			if !ok || !strings.HasPrefix(g, "skew ") || g != wrapExpect(t, c, g) {
				return false
			}
		}
		return true
	}
	t.Run("unchanged", func(t *testing.T) {
		t.Parallel()
		out, clean := memRunLoose(t, memNewBuild(t, src))
		if !clean || !agrees(t, out) {
			t.Fatalf("the unchanged std does not agree with the models (clean exit %t)", clean)
		}
	})
	for _, p := range wrapPlants {
		t.Run(p.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			stdDir := memNewCopyStd(t, root, map[string]string{p.file: memPlantSource(t, p)})
			exe, built := memNewBuildIn(t, root, stdDir, src, true)
			if !built {
				t.Fatalf("plant %s does not compile, so it tests nothing", p.name)
			}
			out, clean := memRunLoose(t, exe)
			if clean && agrees(t, out) {
				t.Errorf("plant %s: the run agrees with the models line for line", p.name)
			}
		})
	}
}
