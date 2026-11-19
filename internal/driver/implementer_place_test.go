// Why: the check, mono's const folding and the support pack's ErrorName
// table can each look right in IR and still print the wrong thing.
// What: regression tests of payload error names, const aggregate str
// arguments, and the order of index and slice-bound evaluation before an
// implementer's borrow.
// How: buildRunText builds each program for the host and runs it, so each
// is judged by what it prints.

package driver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mk/internal/types"
)

// buildRunText builds src as a one-file package for the host in the dev
// profile and runs it with args: stdout, stderr and the exit code.
func buildRunText(t *testing.T, src string, args ...string) (string, string, int) {
	t.Helper()
	target := HostTarget()
	dir := t.TempDir()
	file := filepath.Join(dir, "main.mk")
	if err := os.WriteFile(file, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := Config{Root: file, Target: target, Profile: types.Dev, CacheDir: t.TempDir()}
	cfg.Tools = needTools(t)
	s, err := NewSession(cfg)
	if err != nil {
		t.Fatal(err)
	}
	r, paths, err := s.Build(filepath.Join(dir, "main"+exeSuffix(target)))
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Report.Diags) > 0 || len(paths) != 1 {
		t.Fatalf("build:\n%s", r.Report.Text(r.Sources))
	}
	return runExe(t, target, paths[0], "", args...)
}

// TestPanicNamesPayloadError: an error that exists only in another
// error's payload inside a const keeps its ErrorName row (the pruning
// walks a const's type into each member's fields), so `.Must()` on it
// prints its name, not "unknown error".
func TestPanicNamesPayloadError(t *testing.T) {
	t.Parallel()
	_, errOut, code := buildRunText(t, `error Inner
error Other
error Outer(code: int, inner: Inner|Other)

const Boom: [2]Outer = [Outer(code: 1, inner: Inner), Outer(code: 2, inner: Other)]

Pick(xs: &[2]Outer, i: int): Inner|Other {
    return xs[i].inner
}

Get(i: int): Inner|Other!int {
    fail Pick(&Boom, i)
}

export Main(sys: &System): OutOfMemory|WriteError!void {
    x := Get(sys.args.Len() - 1).Must()
    try sys.Print("{x}\n", (x: x))
}
`)
	if code == 0 || !strings.Contains(errOut, "panic: must: main.Inner") {
		t.Errorf("exit %d, stderr %q; want a panic naming main.Inner", code, errOut)
	}
}

// TestConstAggregateStrArgRuns: an array or struct literal of `str`
// parts passed for a const parameter builds and runs, as the same value
// named by a const does; it was E0000.
func TestConstAggregateStrArgRuns(t *testing.T) {
	t.Parallel()
	out, errOut, code := buildRunText(t, `struct Two {
    a: str
    b: str
}

PickS(const p: [2]str): int {
    return p[0].Len() + p[1].Len()
}

PickT(const p: Two): int {
    return p.a.Len() + p.b.Len()
}

const Names: [2]str = ["ab", "cde"]

export Main(sys: &System): OutOfMemory|WriteError!void {
    try sys.Print("{a} {b} {c} {d}\n", (a: PickS(["ab", "cde"]), b: PickS(Names), c: PickT(Two(a: "x", b: "yz")), d: PickS(["", "q"])))
}
`)
	if code != 0 || out != "5 5 3 1\n" {
		t.Errorf("prints %q (exit %d, stderr %q), want %q", out, code, errOut, "5 5 3 1\n")
	}
}

// TestNestedImplementerIndex: with nested implementers,
// `self.rows[self.Pick()][self.Pick()] += 1` runs both index expressions
// before the first ItemsMut borrow, so it checks and runs as the [2][2]int
// form and an array of List do.
func TestNestedImplementerIndex(t *testing.T) {
	t.Parallel()
	out, errOut, code := buildRunText(t, `struct Grid {
    rows: List[List[int]]
    arr: [2][2]int
    mixed: [2]List[int]
    flat: List[[2]int]
    slot: int
}

Grid.Pick(&self): int {
    return self.slot
}

Grid.Bump(mut self) {
    self.arr[self.Pick()][self.Pick()] += 1
    self.rows[self.Pick()][self.Pick()] += 1
    self.mixed[0][self.Pick()] += 1
    self.flat[self.Pick()][self.Pick() + 1] += 1
    x := self.rows[self.Pick()][self.Pick()]
    self.rows[self.Pick()][self.Pick()] = x + 1
}

export Main(sys: &System): OutOfMemory|WriteError!void {
    var g := Grid(rows: List[List[int]].New(sys.arena), arr: [[0, 0], [0, 0]], mixed: [List[int].New(sys.arena), List[int].New(sys.arena)], flat: List[[2]int].New(sys.arena), slot: 0)
    var r := List[int].New(sys.arena)
    try r.Push(0)
    try g.rows.Push(r)
    try g.mixed[0].Push(0)
    try g.flat.Push([0, 0])
    g.Bump()
    try sys.Print("{a} {b} {c} {d}\n", (a: g.arr[0][0], b: g.rows[0][0], c: g.mixed[0][0], d: g.flat[0][1]))
}
`)
	if code != 0 || out != "1 2 1 1\n" {
		t.Errorf("prints %q (exit %d, stderr %q), want %q", out, code, errOut, "1 2 1 1\n")
	}
}

// TestImplementerSliceBoundsBeforeBorrow: a slice of a List
// field runs its bounds before Items or ItemsMut borrows the field, so
// `Zero(mut self.slots[self.Pick()..])` checks and runs as over a [4]int
// field.
func TestImplementerSliceBoundsBeforeBorrow(t *testing.T) {
	t.Parallel()
	out, errOut, code := buildRunText(t, `struct Counts {
    slots: List[int]
    fixed: [4]int
    slot: int
}

Counts.Pick(&self): int {
    return self.slot
}

Zero(xs: mut []int) {
    for i in 0..xs.Len() {
        xs[i] = 0
    }
}

Sum(xs: []int): int {
    var n := 0
    for x in xs {
        n += x
    }
    return n
}

Counts.Run(mut self): int {
    a := Sum(self.slots[self.Pick()..self.Pick() + 1])
    Zero(mut self.fixed[self.Pick()..])
    Zero(mut self.slots[self.Pick()..])
    return a
}

export Main(sys: &System): OutOfMemory|WriteError!void {
    var c := Counts(slots: List[int].New(sys.arena), fixed: [1, 2, 3, 4], slot: 1)
    try c.slots.Push(5)
    try c.slots.Push(6)
    a := c.Run()
    try sys.Print("{a} {b} {c} {d} {e}\n", (a: a, b: c.fixed[0], c: c.fixed[1], d: c.slots[0], e: c.slots[1]))
}
`)
	if code != 0 || out != "6 1 0 5 0\n" {
		t.Errorf("prints %q (exit %d, stderr %q), want %q", out, code, errOut, "6 1 0 5 0\n")
	}
}

// TestImplementerPlaceOrder: the index expressions and slice
// bounds an implementer's borrow waits for each run once, in source order: a
// counter bumped by each shows the order, and the List forms change the same
// cells as the array forms.
func TestImplementerPlaceOrder(t *testing.T) {
	t.Parallel()
	out, errOut, code := buildRunText(t, `Next(n: mut int): int {
    *n += 1
    return *n - 1
}

Zero(xs: mut []int) {
    for i in 0..xs.Len() {
        xs[i] = 0
    }
}

struct Grid {
    rows: List[List[int]]
    arr: [3][3]int
}

Grid.Pick(&self): int {
    return 1
}

Grid.ZeroTail(mut self) {
    Zero(mut self.rows[self.Pick()][self.Pick()..])
    Zero(mut self.arr[self.Pick()][self.Pick()..])
}

Row(a: &Arena): OutOfMemory!List[int] {
    var r := List[int].New(a)
    for i in 0..3 {
        try r.Push(i + 1)
    }
    return r
}

export Main(sys: &System): OutOfMemory|WriteError!void {
    var g := Grid(rows: List[List[int]].New(sys.arena), arr: [[1, 2, 3], [1, 2, 3], [1, 2, 3]])
    for _ in 0..3 {
        try g.rows.Push(try Row(sys.arena))
    }
    var n := 0
    g.rows[Next(mut n)][Next(mut n)] += 10
    var p := 0
    g.arr[Next(mut p)][Next(mut p)] += 10
    m := 0
    var k := 0
    s := Sum(g.rows[m][Next(mut k)..Next(mut k) + 2])
    var j := 0
    t := Sum(g.arr[m][Next(mut j)..Next(mut j) + 2])
    g.ZeroTail()
    r := g.rows[1][0] + g.rows[1][1] + g.rows[1][2]
    q := g.arr[1][0] + g.arr[1][1] + g.arr[1][2]
    try sys.Print("{n} {p} {k} {j} {a} {b} {s} {t} {r} {q}\n", (n: n, p: p, k: k, j: j, a: g.rows[0][1], b: g.arr[0][1], s: s, t: t, r: r, q: q))
}

Sum(xs: []int): int {
    var n := 0
    for x in xs {
        n += x
    }
    return n
}
`)
	// Each counter ran twice, lo before hi and outer before inner: a
	// reversed order would bump rows[1][0] and sum [1..2].
	const want = "2 2 2 2 12 12 16 16 1 1\n"
	if code != 0 || out != want {
		t.Errorf("prints %q (exit %d, stderr %q), want %q", out, code, errOut, want)
	}
}

// TestImplementerSliceBoundPlaceRead: a slice bound that is a
// plain read of the implementer's own place (`b.n`, `self.n`) is read before
// Items or ItemsMut borrows the implementer, as an index is, so `mut
// b[0..b.n]` and `mut self[self.n..]` check clean and zero the cells the
// [4]int field forms zero.
func TestImplementerSliceBoundPlaceRead(t *testing.T) {
	t.Parallel()
	const want = "0 0 3 4 | 1 0 0 0 | 1 2 0 4 | 1 0 0 4 | 10\n"
	out, errOut, code := buildRunText(t, `struct Buf {
    xs: [4]int
    n: int
}

impl Slice[int] for Buf {
    Items(&self): []int {
        return self.xs[0..]
    }
    ItemsMut(mut self): mut []int {
        return mut self.xs[0..]
    }
}

Zero(xs: mut []int) {
    for i in 0..xs.Len() {
        xs[i] = 0
    }
}

Sum(xs: []int): int {
    var n := 0
    for x in xs {
        n += x
    }
    return n
}

Buf.Clear(mut self) {
    Zero(mut self[self.n..])
}

Fresh(n: int): Buf {
    return Buf(xs: [1, 2, 3, 4], n: n)
}

export Main(sys: &System): OutOfMemory|WriteError!void {
    var a := Fresh(2)
    Zero(mut a[0..a.n])
    var b := Fresh(1)
    b.Clear()
    var c := Fresh(2)
    Zero(mut c[c.n..c.n + 1])
    var d := Fresh(1)
    Zero(mut d[d.n..d.n + 2])
    e := Fresh(0)
    s := Sum(e[e.n..])
    try sys.Print("{a0} {a1} {a2} {a3} | {b0} {b1} {b2} {b3} | {c0} {c1} {c2} {c3} | {d0} {d1} {d2} {d3} | {s}\n", (a0: a.xs[0], a1: a.xs[1], a2: a.xs[2], a3: a.xs[3], b0: b.xs[0], b1: b.xs[1], b2: b.xs[2], b3: b.xs[3], c0: c.xs[0], c1: c.xs[1], c2: c.xs[2], c3: c.xs[3], d0: d.xs[0], d1: d.xs[1], d2: d.xs[2], d3: d.xs[3], s: s))
}
`)
	if code != 0 || out != want {
		t.Errorf("prints %q (exit %d, stderr %q), want %q", out, code, errOut, want)
	}
}
