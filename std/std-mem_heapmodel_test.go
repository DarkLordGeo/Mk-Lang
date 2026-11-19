// Why: FixedHeap, Limit, Failing and Fallback are reached only through
// std.core's AllocBytes, ResizeBytes and FreeBytes, which user code cannot
// call, and a FixedHeap's placement is too intricate to judge by eye.
// What: the fixture module memnew, written into a copy of std; heapModel,
// the Go model of mem.FixedHeap, with heapOracle; the models of Limit,
// Failing and Fallback over it; and the op generator the tests share.
// How: memnew drives an allocator with a tape of ops and prints one line
// per op. heapModel is fixedheap.mk's algorithm over a word array, so its
// offsets are the run's; heapOracle judges the model by first principles
// (blocks aligned, in bounds and disjoint, the invariants H1 to H4, the
// fit the allocator promises), so a mistake made twice is still seen.

package std

import (
	"fmt"
	"math/bits"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// memNewModule is std/memnew/memnew.mk in the copy. A tape of ops is a list
// of ints: 0 size align allocates, 1 i new resizes block i, 2 i frees it, 3
// resets, 4 i size align frees it with other numbers than its own, 5 i old
// new resizes it with another old size, 7 i d frees and resizes the address
// d bytes into it, 8 i frees it twice, 9 frees and resizes null. i is taken
// modulo the live blocks. Every block is filled with a byte of its own and
// read back before it is resized or freed ("corrupt" on a mismatch).
const memNewModule = `// Fixture of the std.mem tests of FixedHeap, Limit, Failing and Fallback
// (std/std-mem_heapmodel_test.go).

import std.core
import std.mem
import std.raw

struct Blk {
    p: *u8
    size: int
    align: int
    fill: u8
}

const MaxBlk = 512

// Tape is a run's live blocks and the buffers a block may lie in: a block
// in the first prints its offset there, any other "i" and its offset in
// the second.
struct Tape {
    blks: [512]Blk
    n: int
    step: int
    lo: uint
    len: uint
    lo2: uint
}

NewTape(lo: uint, len: int, lo2: uint): Tape {
    return Tape(blks: [Blk(p: raw.Null[u8](), size: 0, align: 1, fill: 0); 512], n: 0, step: 0, lo: lo, len: Trunc[uint](len), lo2: lo2)
}

Where(t: &Tape, w: mut Writer, p: *u8): WriteError!void {
    d := raw.Addr(p) -% t.lo
    if d < t.len {
        try Print(w, "{d}", (d: Trunc[int](d)))
    } else {
        try Print(w, "i{d}", (d: Trunc[int](raw.Addr(p) -% t.lo2)))
    }
}

Fill(p: *u8, n: int, fill: u8) {
    unsafe "p is a live block of at least n bytes" {
        raw.Fill(p, fill, n)
    }
}

Filled(p: *u8, n: int, fill: u8): bool {
    unsafe "p is a live block of at least n bytes" {
        for i in 0..n {
            if raw.Read[u8](raw.Offset(p, i)) != fill {
                return false
            }
        }
    }
    return true
}

// Width is how many ints the op k takes.
Width(k: int): int {
    if k == 3 || k == 9 {
        return 1
    }
    if k == 2 || k == 8 {
        return 2
    }
    if k == 4 || k == 5 {
        return 4
    }
    return 3
}

// Step runs the op at ops[i] through a, prints its line without the
// newline and returns the index of the next op. A reset (3) is the
// caller's: here it prints "z" and does nothing.
Step(t: mut Tape, w: mut Writer, a: &Alloc, ops: []int, i: int): WriteError!int {
    k := ops[i]
    t.step += 1
    if k == 3 {
        try Print(w, "z")
        return i + 1
    }
    if k == 0 {
        size := ops[i + 1]
        align := ops[i + 2]
        p := core.AllocBytes(a, size, align)
        if raw.IsNull(p) {
            try Print(w, "a null")
            return i + 3
        }
        try Print(w, "a ")
        try Where(&*t, w, p)
        fill := Trunc[u8](t.step % 251 + 1)
        Fill(p, size, fill)
        if t.n < MaxBlk {
            t.blks[t.n] = Blk(p: p, size: size, align: align, fill: fill)
            t.n += 1
        } else {
            core.FreeBytes(a, p, size, align)
        }
        return i + 3
    }
    if k == 9 {
        core.FreeBytes(a, raw.Null[u8](), 8, 8)
        ok := core.ResizeBytes(a, raw.Null[u8](), 8, 16, 8)
        try Print(w, "n {ok}", (ok: ok))
        return i + 1
    }
    if t.n == 0 {
        try Print(w, "- {k}", (k: k))
        return i + Width(k)
    }
    j := ops[i + 1] % t.n
    b := t.blks[j]
    if !Filled(b.p, b.size, b.fill) {
        try Print(w, "corrupt ")
    }
    if k == 1 || k == 5 {
        var old := b.size
        var new := ops[i + 2]
        if k == 5 {
            old = ops[i + 2]
            new = ops[i + 3]
        }
        ok := core.ResizeBytes(a, b.p, old, new, b.align)
        if ok {
            var kept := b.size
            if new < kept {
                kept = new
            }
            if !Filled(b.p, kept, b.fill) {
                try Print(w, "corrupt ")
            }
            Fill(b.p, new, b.fill)
            t.blks[j] = Blk(p: b.p, size: new, align: b.align, fill: b.fill)
        }
        try Print(w, "r ")
        try Where(&*t, w, b.p)
        try Print(w, " {ok}", (ok: ok))
        return i + Width(k)
    }
    if k == 7 {
        var ok := true
        unsafe "an address inside block j, only passed on: no block starts there" {
            q := raw.Offset(b.p, ops[i + 2])
            core.FreeBytes(a, q, b.size, b.align)
            ok = core.ResizeBytes(a, q, b.size, b.size, b.align)
        }
        try Print(w, "w ")
        try Where(&*t, w, b.p)
        try Print(w, " {ok}", (ok: ok))
        return i + 3
    }
    var size := b.size
    var align := b.align
    if k == 4 {
        size = ops[i + 2]
        align = ops[i + 3]
    }
    core.FreeBytes(a, b.p, size, align)
    if k == 8 {
        core.FreeBytes(a, b.p, size, align)
    }
    t.blks[j] = t.blks[t.n - 1]
    t.n -= 1
    try Print(w, "f ")
    try Where(&*t, w, b.p)
    return i + Width(k)
}

// Finish reads back and frees every live block, the last first.
Finish(t: mut Tape, w: mut Writer, a: &Alloc): WriteError!void {
    for t.n > 0 {
        t.n -= 1
        b := t.blks[t.n]
        if !Filled(b.p, b.size, b.fill) {
            try Print(w, "corrupt\n")
        }
        core.FreeBytes(a, b.p, b.size, b.align)
    }
}

// RunHeap runs ops over a FixedHeap on buf. At the end it frees every
// block and prints the largest request that then succeeds, and where.
export RunHeap(w: mut Writer, buf: mut []u8, ops: []int): WriteError!void {
    lo := raw.Addr(raw.DataMut(buf))
    top := buf.Len() & -8
    try Print(w, "skew {s}\n", (s: Trunc[int](lo & 4095)))
    var h := mem.FixedHeap.New(buf)
    var t := NewTape(lo, 1 << 62, lo)
    var i := 0
    for i < ops.Len() {
        if ops[i] == 3 {
            h.Reset()
            t.n = 0
            t.step += 1
            try Print(w, "z\n")
            i += 1
            continue
        }
        i = try Step(mut t, w, h.Alloc(), ops, i)
        try Print(w, "\n")
    }
    try Finish(mut t, w, h.Alloc())
    var size := top
    for size >= 0 {
        p := core.AllocBytes(h.Alloc(), size, 1)
        if !raw.IsNull(p) {
            try Print(w, "whole {size} ", (size))
            try Where(&t, w, p)
            try Print(w, "\n")
            core.FreeBytes(h.Alloc(), p, size, 1)
            break
        }
        size -= 8
    }
    try Print(w, "end\n")
}

// RunLimit runs ops through a Limit of max bytes over a FixedHeap on buf,
// and prints Bytes and Left after each.
export RunLimit(w: mut Writer, buf: mut []u8, max: int, ops: []int): WriteError!void {
    lo := raw.Addr(raw.DataMut(buf))
    try Print(w, "skew {s}\n", (s: Trunc[int](lo & 4095)))
    h := mem.FixedHeap.New(buf)
    l := mem.Limit.New(h.Alloc(), max)
    var t := NewTape(lo, 1 << 62, lo)
    var i := 0
    for i < ops.Len() {
        i = try Step(mut t, w, l.Alloc(), ops, i)
        try Print(w, " b={b} l={l}\n", (b: l.Bytes(), l: l.Left()))
    }
    try Finish(mut t, w, l.Alloc())
    try Print(w, "end b={b} l={l}\n", (b: l.Bytes(), l: l.Left()))
}

// RunFailing runs ops through a Failing over a FixedHeap on buf, made by
// New(at) or, when from is set, NewFrom(at), and prints Requests and
// Failed after each.
export RunFailing(w: mut Writer, buf: mut []u8, at: int, from: bool, ops: []int): WriteError!void {
    lo := raw.Addr(raw.DataMut(buf))
    try Print(w, "skew {s}\n", (s: Trunc[int](lo & 4095)))
    h := mem.FixedHeap.New(buf)
    f := if from { mem.Failing.NewFrom(h.Alloc(), at) } else { mem.Failing.New(h.Alloc(), at) }
    var t := NewTape(lo, 1 << 62, lo)
    var i := 0
    for i < ops.Len() {
        i = try Step(mut t, w, f.Alloc(), ops, i)
        try Print(w, " q={q} x={x}\n", (q: f.Requests(), x: f.Failed()))
    }
    try Finish(mut t, w, f.Alloc())
    try Print(w, "end q={q} x={x}\n", (q: f.Requests(), x: f.Failed()))
}

// RunFallback runs ops through a Fallback over buf, then a FixedHeap on
// inbuf. A reset frees every live block first, as Reset's caller must.
export RunFallback(w: mut Writer, buf: mut []u8, inbuf: mut []u8, ops: []int): WriteError!void {
    lo := raw.Addr(raw.DataMut(buf))
    lo2 := raw.Addr(raw.DataMut(inbuf))
    n := buf.Len()
    try Print(w, "skew {s} {s2}\n", (s: Trunc[int](lo & 4095), s2: Trunc[int](lo2 & 4095)))
    h := mem.FixedHeap.New(inbuf)
    var fb := mem.Fallback.New(h.Alloc(), buf)
    var t := NewTape(lo, n, lo2)
    var i := 0
    for i < ops.Len() {
        if ops[i] == 3 {
            try Finish(mut t, w, fb.Alloc())
            fb.Reset()
            t.step += 1
            try Print(w, "z\n")
            i += 1
            continue
        }
        i = try Step(mut t, w, fb.Alloc(), ops, i)
        try Print(w, "\n")
    }
    try Finish(mut t, w, fb.Alloc())
    try Print(w, "end\n")
}

// SameOwners reports whether two calls of Alloc on one FixedHeap, Limit,
// Failing and Fallback return one owner each, and four different ones.
export SameOwners(buf: mut []u8, buf2: mut []u8, inner: &Alloc): bool {
    h := mem.FixedHeap.New(buf)
    l := mem.Limit.New(inner, 64)
    f := mem.Failing.New(inner, -1)
    b := mem.Fallback.New(inner, buf2)
    same := raw.Same(raw.Of(h.Alloc()), raw.Of(h.Alloc())) && raw.Same(raw.Of(l.Alloc()), raw.Of(l.Alloc())) && raw.Same(raw.Of(f.Alloc()), raw.Of(f.Alloc())) && raw.Same(raw.Of(b.Alloc()), raw.Of(b.Alloc()))
    apart := !raw.Same(raw.Of(h.Alloc()), raw.Of(l.Alloc())) && !raw.Same(raw.Of(l.Alloc()), raw.Of(f.Alloc())) && !raw.Same(raw.Of(f.Alloc()), raw.Of(b.Alloc())) && !raw.Same(raw.Of(b.Alloc()), raw.Of(inner))
    return same && apart
}
`

// memNewCopyStd is memCopyStd with the memnew fixture beside memfix, and
// with extra files written over the copy (a planted bug in std/mem).
func memNewCopyStd(t *testing.T, root string, extra map[string]string) string {
	t.Helper()
	fixtures := map[string]string{"memfix/memfix.mk": memFixModule, "memnew/memnew.mk": memNewModule}
	for p, s := range extra {
		fixtures[p] = s
	}
	stdDir := copyStd(t, root, fixtures)
	ents, err := os.ReadDir(stdDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range ents {
		if e.IsDir() && !memStdKeep[e.Name()] && e.Name() != "memnew" {
			if err := os.RemoveAll(filepath.Join(stdDir, e.Name())); err != nil {
				t.Fatal(err)
			}
		}
	}
	manifest := filepath.Join(stdDir, "pkg.mk")
	b, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatal(err)
	}
	m := unsafeList.FindSubmatchIndex(b)
	if m == nil {
		t.Fatal("std/pkg.mk has no unsafe_modules list")
	}
	keep := []string{`"memnew"`}
	for _, ent := range strings.Split(string(b[m[2]:m[3]]), ",") {
		ent = strings.TrimSpace(ent)
		dir := strings.SplitN(strings.Trim(ent, `"`), "/", 2)[0]
		if ent != "" && memStdKeep[dir] {
			keep = append(keep, ent)
		}
	}
	out := string(b[:m[2]]) + strings.Join(keep, ", ") + string(b[m[3]:])
	if err := os.WriteFile(manifest, []byte(out), 0o644); err != nil {
		t.Fatal(err)
	}
	return stdDir
}

// memNewBuildIn builds src as main.mk over the std copy at stdDir, in root,
// and returns the executable; a missing toolchain skips. ok is false when
// the build fails and mayFail allows it (a planted bug the compiler sees).
func memNewBuildIn(t *testing.T, root, stdDir, src string, mayFail bool, flags ...string) (exe string, ok bool) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, "main.mk"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	exe = filepath.Join(root, "main")
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	args := append([]string{"build"}, flags...)
	args = append(args, "-o", exe, "main.mk")
	out, status := mkCmd(t, root, stdDir, args...)
	if status != 0 {
		if strings.Contains(out, "no usable LLVM") {
			t.Skipf("needs the LLVM toolchain: %s", out)
		}
		if mayFail {
			return "", false
		}
		t.Fatalf("mk build: exit %d\n%s", status, out)
	}
	return exe, true
}

// memNewBuild copies std with the fixtures and builds src with flags.
func memNewBuild(t *testing.T, src string, flags ...string) string {
	t.Helper()
	root := t.TempDir()
	exe, _ := memNewBuildIn(t, root, memNewCopyStd(t, root, nil), src, false, flags...)
	return exe
}

// The words of a header's size, as fixedheap.mk's FhFree and FhSize.
const (
	hmFree = 0x80000000
	hmSize = 0x7FFFFFFF
)

// heapModel is the Go model of mem.FixedHeap: the buffer from its first
// 8-byte boundary as u32 words (w[k] is the word at byte 4 k), laid out and
// changed exactly as fixedheap.mk's header says. lead is where that
// boundary lies in the buffer and skew the boundary's address modulo 4096,
// which is all an alignment up to 4096 depends on.
type heapModel struct {
	lead, skew                    int
	w                             []uint32
	fls, heads, first, last, max_ int
}

// hmList and hmFit are FhList and FhFit.
func hmList(g int) int {
	if g < 32 {
		return g
	}
	b := bits.Len(uint(g)) - 1
	return (b-3)<<4 + (g>>(b-4))&15
}

func hmFit(g int) int {
	if g < 32 {
		return g
	}
	b := bits.Len(uint(g)) - 1
	return hmList(g + 1<<(b-4) - 1)
}

// hmHeads and hmShape are FhHeads and FhShape: where the list heads start
// for a number of first levels, and the number of first levels that leaves
// the largest block, with that block's granules (0 levels: no block fits).
func hmHeads(fls int) int { return (4 + 4*fls + 7) &^ 7 }

func hmShape(g int) (best, span int) {
	span = 1
	for fls := 1; fls <= hmList(g)>>4+1; fls++ {
		room := g - 1 - (hmHeads(fls)+64*fls)>>3
		if room < 2 {
			break
		}
		if n := min(room, 8<<fls-1); n > span {
			best, span = fls, n
		}
	}
	return best, span
}

// newHeapModel is the model of FixedHeap.New over n bytes whose address is
// skew modulo 4096.
func newHeapModel(skew, n int) *heapModel {
	m := &heapModel{lead: (-skew) & 7}
	g := 0
	if m.lead <= n {
		g = (n - m.lead) >> 3
	}
	m.skew = (skew + m.lead) & 4095
	if fls, span := hmShape(g); fls != 0 {
		m.fls = fls
		m.heads = hmHeads(fls)
		m.first = (m.heads + 64*fls) >> 3
		m.last = m.first + span
		m.max_ = 8*span - 8
	}
	m.w = make([]uint32, 2*g)
	m.format()
	return m
}

func (m *heapModel) r(off int) int     { return int(m.w[off>>2]) }
func (m *heapModel) wr(off int, v int) { m.w[off>>2] = uint32(v) }

func (m *heapModel) format() {
	if m.fls == 0 {
		return
	}
	for i := 0; i < 2*m.first; i++ {
		m.w[i] = 0
	}
	m.wr(m.first<<3, 0)
	m.wr(m.last<<3+4, 1)
	m.put(m.first, m.last-m.first)
}

func (m *heapModel) put(g, n int) {
	at := g << 3
	m.wr(at+4, n|hmFree)
	m.wr((g+n)<<3, n)
	i := hmList(n)
	hp := m.heads + i<<2
	head := m.r(hp)
	m.wr(at+8, head)
	m.wr(at+12, 0)
	if head != 0 {
		m.wr(head<<3+12, g)
	}
	m.wr(hp, g)
	f := i >> 4
	m.wr(4+f<<2, m.r(4+f<<2)|1<<(i&15))
	m.wr(0, m.r(0)|1<<f)
}

func (m *heapModel) empty(i int) {
	f := i >> 4
	v := m.r(4+f<<2) &^ (1 << (i & 15))
	m.wr(4+f<<2, v)
	if v == 0 {
		m.wr(0, m.r(0)&^(1<<f))
	}
}

func (m *heapModel) unlink(g, n int) {
	at := g << 3
	nx, bk := m.r(at+8), m.r(at+12)
	if nx != 0 {
		m.wr(nx<<3+12, bk)
	}
	if bk != 0 {
		m.wr(bk<<3+8, nx)
		return
	}
	i := hmList(n)
	m.wr(m.heads+i<<2, nx)
	if nx == 0 {
		m.empty(i)
	}
}

func (m *heapModel) take(i int) int {
	f := i >> 4
	if f >= m.fls {
		return 0
	}
	sm := m.r(4+f<<2) & (-1 << (i & 15))
	if sm == 0 {
		fm := m.r(0) & (-1 << (f + 1))
		if fm == 0 {
			return 0
		}
		f = bits.TrailingZeros(uint(fm))
		sm = m.r(4 + f<<2)
	}
	j := f<<4 + bits.TrailingZeros(uint(sm))
	hp := m.heads + j<<2
	g := m.r(hp)
	nx := m.r(g<<3 + 8)
	m.wr(hp, nx)
	if nx != 0 {
		m.wr(nx<<3+12, 0)
	} else {
		m.empty(j)
	}
	return g
}

func (m *heapModel) find(need int) int {
	g := m.take(hmFit(need))
	if g != 0 || need < 32 {
		return g
	}
	i := hmList(need)
	if i>>4 >= m.fls {
		return 0
	}
	own := m.r(m.heads + i<<2)
	if own == 0 {
		return 0
	}
	n := m.r(own<<3+4) & hmSize
	if n < need {
		return 0
	}
	m.unlink(own, n)
	return own
}

func (m *heapModel) carve(g, have, need int) {
	rest := have - need
	if rest < 2 {
		m.wr(g<<3+4, have)
		m.wr((g+have)<<3, have)
		return
	}
	m.wr(g<<3+4, need)
	m.put(g+need, rest)
	m.wr((g+need)<<3, need)
}

func hmNeed(size int) int { return max(2, (size+15)>>3) }

// alloc returns the payload's byte offset from the model's base.
func (m *heapModel) alloc(size, align int) (int, bool) {
	if size < 0 || size > m.max_ || align < 1 || align > 1<<30 || align&(align-1) != 0 {
		return 0, false
	}
	need := hmNeed(size)
	if align <= 8 {
		g := m.find(need)
		if g == 0 {
			return 0, false
		}
		m.carve(g, m.r(g<<3+4)&hmSize, need)
		return g<<3 + 8, true
	}
	ag := align >> 3
	want := need + ag + 1
	if want > m.last-m.first {
		return 0, false
	}
	found := m.find(want)
	if found == 0 {
		return 0, false
	}
	g, have := found, m.r(found<<3+4)&hmSize
	at := m.skew>>3 + g + 1
	gap := (-at) & (ag - 1)
	if gap == 1 {
		gap += ag
	}
	if gap != 0 {
		g += gap
		have -= gap
		m.put(found, gap)
	}
	m.carve(g, have, need)
	return g<<3 + 8, true
}

// block is FhBlock: the granule of the used block whose payload is at
// byte off of the base, or 0.
func (m *heapModel) block(off int) int {
	g := off>>3 - 1
	if off&7 != 0 || uint(g-m.first) >= uint(m.last-m.first) {
		return 0
	}
	if m.r(g<<3+4)&hmFree != 0 {
		return 0
	}
	return g
}

func (m *heapModel) resize(off, new int) bool {
	g := m.block(off)
	if g == 0 || new < 0 || new > m.max_ {
		return false
	}
	need := hmNeed(new)
	cur := m.r(g<<3 + 4)
	have := cur
	if need != cur {
		nx := g + cur
		ns := m.r(nx<<3 + 4)
		if ns&hmFree != 0 {
			if cur+ns&hmSize < need {
				return false
			}
			m.unlink(nx, ns&hmSize)
			have = cur + ns&hmSize
		} else if need > cur {
			return false
		}
	}
	m.carve(g, have, need)
	return true
}

func (m *heapModel) free(off int) {
	g := m.block(off)
	if g == 0 {
		return
	}
	start, n := g, m.r(g<<3+4)
	nx := g + n
	if ns := m.r(nx<<3 + 4); ns&hmFree != 0 {
		m.unlink(nx, ns&hmSize)
		n += ns & hmSize
	}
	if pv := m.r(g << 3); pv != 0 && m.r((g-pv)<<3+4)&hmFree != 0 {
		start = g - pv
		m.unlink(start, pv)
		n += pv
		m.wr(g<<3+4, hmFree)
	}
	m.put(start, n)
}

// hmSpan is one block as the walk sees it: its header's granule, its size
// in granules and whether it is free.
type hmSpan struct {
	g, n int
	free bool
}

// walk checks H1 to H4 over the model's words, as ThWalk of
// tests_fixedheap.mk does over a real buffer, and returns the blocks in
// address order.
func (m *heapModel) walk(t *testing.T) []hmSpan {
	t.Helper()
	if m.fls == 0 {
		return nil
	}
	var spans []hmSpan
	prev, prevFree := 0, false
	free, freeG := 0, 0
	g := m.first
	for g < m.last {
		s := m.r(g<<3 + 4)
		n, isFree := s&hmSize, s&hmFree != 0
		if m.r(g<<3) != prev {
			t.Fatalf("model H1: block %d has prev %d, the block before it is %d granules", g, m.r(g<<3), prev)
		}
		if n < 2 || g+n > m.last {
			t.Fatalf("model H4: block %d has %d granules (last %d)", g, n, m.last)
		}
		if isFree && prevFree {
			t.Fatalf("model H2: block %d and the block before it are both free", g)
		}
		if isFree {
			free++
			freeG += n
		}
		spans = append(spans, hmSpan{g, n, isFree})
		prev, prevFree = n, isFree
		g += n
	}
	if g != m.last || m.r(m.last<<3) != prev || m.r(m.last<<3+4) != 1 {
		t.Fatalf("model H1: the blocks end at %d, the end marker is at %d with prev %d size %d", g, m.last, m.r(m.last<<3), m.r(m.last<<3+4))
	}
	listed, listedG, flmap := 0, 0, 0
	for f := 0; f < m.fls; f++ {
		slmap := 0
		for s := 0; s < 16; s++ {
			i := f<<4 + s
			back := 0
			x := m.r(m.heads + i<<2)
			if x != 0 {
				slmap |= 1 << s
			}
			for x != 0 {
				if x < m.first || x >= m.last {
					t.Fatalf("model H3: list %d holds granule %d, outside the blocks", i, x)
				}
				sz := m.r(x<<3 + 4)
				if sz&hmFree == 0 || hmList(sz&hmSize) != i {
					t.Fatalf("model H3: list %d holds block %d of size word %#x", i, x, sz)
				}
				if m.r(x<<3+12) != back {
					t.Fatalf("model H3: block %d on list %d links back to %d, not %d", x, i, m.r(x<<3+12), back)
				}
				listed++
				listedG += sz & hmSize
				if listed > free {
					t.Fatalf("model H3: the lists hold more blocks than the %d free ones", free)
				}
				back, x = x, m.r(x<<3+8)
			}
		}
		if m.r(4+f<<2) != slmap {
			t.Fatalf("model H3: second-level map %d is %#x, its heads say %#x", f, m.r(4+f<<2), slmap)
		}
		if slmap != 0 {
			flmap |= 1 << f
		}
	}
	if m.r(0) != flmap || listed != free || listedG != freeG {
		t.Fatalf("model H3: first-level map %#x (heads say %#x), %d blocks of %d granules listed, %d of %d free", m.r(0), flmap, listed, listedG, free, freeG)
	}
	return spans
}

// heapOracle judges the model after an op, independently of how it placed
// anything. Every invariant holds (walk). The live blocks, each with the 8
// bytes of its header, are aligned, inside the blocks' part of the buffer
// and share no byte. refused is a request (its size and alignment) the
// model just refused, or nil: an alignment of 8 or less must not be refused
// while a free block holds what the allocator promises to find, a block of
// the request's size below 32 granules, and one a sixteenth larger above.
func heapOracle(t *testing.T, m *heapModel, blks []memBlk, refused []int) {
	t.Helper()
	spans := m.walk(t)
	lo, hi := m.first<<3+8, m.last<<3
	for j, x := range blks {
		if (m.skew+x.off)%x.align != 0 || x.off&7 != 0 || x.off < lo || x.off+x.size > hi {
			t.Fatalf("model block at %d size %d align %d (skew %d) breaks alignment or bounds [%d, %d)", x.off, x.size, x.align, m.skew, lo, hi)
		}
		for k := j + 1; k < len(blks); k++ {
			o := blks[k]
			if x.off-8 < o.off+max(o.size, 1) && o.off-8 < x.off+max(x.size, 1) {
				t.Fatalf("model block %d+%d overlaps live %d+%d or its header", x.off, x.size, o.off, o.size)
			}
		}
	}
	if refused == nil || refused[0] < 0 || !memAlignOK(refused[1]) || refused[1] > 8 {
		return
	}
	need := hmNeed(refused[0])
	promised := need
	if need >= 32 {
		promised = need + need/16
	}
	for _, s := range spans {
		if s.free && s.n >= promised {
			t.Fatalf("model refused %d bytes (%d granules) while a free block of %d granules exists at %d", refused[0], need, s.n, s.g)
		}
	}
}

// allocModel is an allocator as memnew's Step drives it. An address is a
// byte offset in a model address space: the first buffer starts at 0, the
// second at memSecond.
type allocModel interface {
	alloc(size, align int) (p int, ok bool)
	resize(p, old, new, align int) bool
	free(p, size, align int)
}

// memSecond is where the second buffer of a run starts in the model's
// address space, and memNull the null pointer: far from both buffers.
const (
	memSecond = 1 << 40
	memNull   = -(1 << 50)
)

// heapAt is a heapModel whose buffer starts at base of the address space.
type heapAt struct {
	m    *heapModel
	base int
}

func (h heapAt) alloc(size, align int) (int, bool) {
	off, ok := h.m.alloc(size, align)
	return h.base + h.m.lead + off, ok
}

func (h heapAt) resize(p, old, new, align int) bool { return h.m.resize(p-h.base-h.m.lead, new) }
func (h heapAt) free(p, size, align int)            { h.m.free(p - h.base - h.m.lead) }

// fixedAt is a fixedModel whose buffer starts at 0.
type fixedAt struct{ m *fixedModel }

func (f fixedAt) alloc(size, align int) (int, bool) {
	off, ok := f.m.alloc(size, align)
	return f.m.lead + off, ok
}

func (f fixedAt) resize(p, old, new, align int) bool { return f.m.resize(p-f.m.lead, old, new) }
func (f fixedAt) free(p, size, align int)            { f.m.free(p-f.m.lead, size, align) }

// limitModel is the model of mem.Limit.
type limitModel struct {
	inner     allocModel
	max, used int
}

func (l *limitModel) alloc(size, align int) (int, bool) {
	if size < 0 || size > l.max-l.used {
		return 0, false
	}
	p, ok := l.inner.alloc(size, align)
	if ok {
		l.used += size
	}
	return p, ok
}

func (l *limitModel) resize(p, old, new, align int) bool {
	if new < 0 || new-old > l.max-l.used || !l.inner.resize(p, old, new, align) {
		return false
	}
	l.used += new - old
	return true
}

func (l *limitModel) free(p, size, align int) {
	l.inner.free(p, size, align)
	l.used -= size
}

// failingModel is the model of mem.Failing: the requests numbered at to
// last fail.
type failingModel struct {
	inner                 allocModel
	at, last, reqs, fails int
}

func (f *failingModel) picked() bool {
	n := f.reqs
	f.reqs++
	if n < f.at || n > f.last {
		return false
	}
	f.fails++
	return true
}

func (f *failingModel) alloc(size, align int) (int, bool) {
	if f.picked() {
		return 0, false
	}
	return f.inner.alloc(size, align)
}

func (f *failingModel) resize(p, old, new, align int) bool {
	if new > old && f.picked() {
		return false
	}
	return f.inner.resize(p, old, new, align)
}

func (f *failingModel) free(p, size, align int) { f.inner.free(p, size, align) }

// fallbackModel is the model of mem.Fallback: a Fixed over the first
// buffer, whose blocks are the addresses below the buffer's usable end,
// then inner, which is told one byte for a zero-byte block (innerSize).
type fallbackModel struct {
	fixed fixedAt
	inner allocModel
}

func (b *fallbackModel) in(p int) bool {
	off := p - b.fixed.m.lead
	return b.fixed.m.size > 0 && off >= 0 && off < b.fixed.m.size
}

func innerSize(size int) int {
	if size == 0 {
		return 1
	}
	return size
}

func (b *fallbackModel) alloc(size, align int) (int, bool) {
	if size > 0 {
		if p, ok := b.fixed.alloc(size, align); ok {
			return p, true
		}
	}
	return b.inner.alloc(innerSize(size), align)
}

func (b *fallbackModel) resize(p, old, new, align int) bool {
	if b.in(p) {
		return b.fixed.resize(p, old, new, align)
	}
	return b.inner.resize(p, innerSize(old), innerSize(new), align)
}

func (b *fallbackModel) free(p, size, align int) {
	if b.in(p) {
		b.fixed.free(p, size, align)
		return
	}
	b.inner.free(p, innerSize(size), align)
}

// memTape is the Go side of memnew's Tape: the live blocks, as addresses of
// the model's address space, and the lines printed so far.
type memTape struct {
	a    allocModel
	blks []memBlk // off is the address
	out  strings.Builder
	// check, when set, runs after every op with the request just refused,
	// or nil.
	check func(refused []int)
}

func memWidth(k int) int {
	switch k {
	case 3, 9:
		return 1
	case 2, 8:
		return 2
	case 4, 5:
		return 4
	}
	return 3
}

// where is how memnew prints an address: its offset in the first buffer,
// or "i" and its offset in the second.
func memWhere(p int) string {
	if p >= memSecond {
		return fmt.Sprintf("i%d", p-memSecond)
	}
	return fmt.Sprint(p)
}

// step runs the op at ops[i] as memnew's Step does, writes its line without
// the newline and returns the next index.
func (t *memTape) step(ops []int, i int) int {
	k := ops[i]
	var refused []int
	defer func() {
		if t.check != nil {
			t.check(refused)
		}
	}()
	switch {
	case k == 3:
		t.out.WriteString("z")
		return i + 1
	case k == 0:
		p, ok := t.a.alloc(ops[i+1], ops[i+2])
		if !ok {
			refused = []int{ops[i+1], ops[i+2]}
			t.out.WriteString("a null")
			return i + 3
		}
		t.out.WriteString("a " + memWhere(p))
		if len(t.blks) < 512 {
			t.blks = append(t.blks, memBlk{p, ops[i+1], ops[i+2], -1})
		} else {
			t.a.free(p, ops[i+1], ops[i+2])
		}
		return i + 3
	case k == 9:
		t.a.free(memNull, 8, 8)
		fmt.Fprintf(&t.out, "n %t", t.a.resize(memNull, 8, 16, 8))
		return i + 1
	case len(t.blks) == 0:
		fmt.Fprintf(&t.out, "- %d", k)
		return i + memWidth(k)
	}
	j := ops[i+1] % len(t.blks)
	b := t.blks[j]
	switch k {
	case 1, 5:
		old, new := b.size, ops[i+2]
		if k == 5 {
			old, new = ops[i+2], ops[i+3]
		}
		ok := t.a.resize(b.off, old, new, b.align)
		if ok {
			t.blks[j].size = new
		}
		fmt.Fprintf(&t.out, "r %s %t", memWhere(b.off), ok)
	case 7:
		q := b.off + ops[i+2]
		t.a.free(q, b.size, b.align)
		fmt.Fprintf(&t.out, "w %s %t", memWhere(b.off), t.a.resize(q, b.size, b.size, b.align))
	default:
		size, align := b.size, b.align
		if k == 4 {
			size, align = ops[i+2], ops[i+3]
		}
		t.a.free(b.off, size, align)
		if k == 8 {
			t.a.free(b.off, size, align)
		}
		t.blks[j] = t.blks[len(t.blks)-1]
		t.blks = t.blks[:len(t.blks)-1]
		t.out.WriteString("f " + memWhere(b.off))
	}
	return i + memWidth(k)
}

// finish frees every live block, the last first, as memnew's Finish.
func (t *memTape) finish() {
	for len(t.blks) > 0 {
		b := t.blks[len(t.blks)-1]
		t.blks = t.blks[:len(t.blks)-1]
		t.a.free(b.off, b.size, b.align)
		if t.check != nil {
			t.check(nil)
		}
	}
}

// heapExpect runs ops on the model of a FixedHeap over size bytes at skew
// and returns the lines RunHeap must print, judging the model with
// heapOracle after every op.
func heapExpect(t *testing.T, skew, size int, ops []int) string {
	t.Helper()
	m := newHeapModel(skew, size)
	tape := &memTape{a: heapAt{m, 0}}
	// The oracle takes offsets from the model's base, the tape addresses
	// from the buffer's start.
	tape.check = func(refused []int) {
		rel := make([]memBlk, len(tape.blks))
		for i, b := range tape.blks {
			rel[i] = memBlk{b.off - m.lead, b.size, b.align, -1}
		}
		heapOracle(t, m, rel, refused)
	}
	fmt.Fprintf(&tape.out, "skew %d\n", skew)
	for i := 0; i < len(ops); {
		if ops[i] == 3 {
			m.format()
			tape.blks = tape.blks[:0]
			tape.check(nil)
			tape.out.WriteString("z\n")
			i++
			continue
		}
		i = tape.step(ops, i)
		tape.out.WriteString("\n")
	}
	tape.finish()
	// Everything is free again, so the buffer is one block, and the
	// largest request is all of it.
	if spans := m.walk(t); m.fls != 0 && (len(spans) != 1 || !spans[0].free) {
		t.Fatalf("model: %d blocks after every block was freed, want one free block", len(spans))
	}
	if m.fls != 0 {
		off, ok := m.alloc(m.max_, 1)
		if !ok || off != m.first<<3+8 {
			t.Fatalf("model: the largest request (%d bytes) of an empty heap: offset %d, ok %t", m.max_, off, ok)
		}
		if _, ok := m.alloc(0, 1); ok {
			t.Fatal("model: a request succeeded beside the largest one")
		}
		m.free(off)
		fmt.Fprintf(&tape.out, "whole %d %d\n", m.max_, m.lead+off)
	}
	tape.out.WriteString("end\n")
	return tape.out.String()
}

// The wild requests heapOps mixes in.
const (
	memWildNone = iota
	memWildAllocs
	memWildAll
)

// heapOps is a random tape: alloc percent of the ops are allocations of 0
// to maxSize bytes at alignments 1 to 4096 (mostly 8 or less), a fifth are
// resizes, the rest frees, and resets are rare. wild adds the requests no
// container makes. At memWildAllocs these are allocations of bad sizes and
// alignments, which every allocator refuses. At memWildAll they are also
// frees and resizes with other numbers than the block's own, addresses
// inside a block, a block freed twice and the null pointer, which a
// FixedHeap ignores and an allocator that counts sizes may not be given.
func heapOps(seed uint64, count, maxSize, alloc int, resets bool, wild int) []int {
	s := splitmix(seed)
	aligns := []int{1, 1, 2, 4, 8, 8, 8, 8, 16, 32, 64, 256, 4096}
	bad := []int{-1, 0, 3, 6, 12, -8, 1 << 31, 1 << 40, 1 << 30}
	var ops []int
	for range count {
		switch r := s.below(100); {
		case r < alloc:
			ops = append(ops, 0, s.below(maxSize+1), aligns[s.below(len(aligns))])
		case r < alloc+20:
			ops = append(ops, 1, s.below(1<<20), s.below(maxSize*2+1))
		case r < 99 || !resets:
			ops = append(ops, 2, s.below(1<<20))
		default:
			ops = append(ops, 3)
		}
		if wild == memWildNone {
			continue
		}
		switch r := s.below(100); {
		case r < 5:
			ops = append(ops, 0, s.below(maxSize+1), bad[s.below(len(bad))])
		case r < 7:
			ops = append(ops, 0, -1-s.below(1<<20), 8)
		case r < 9:
			ops = append(ops, 0, 1<<40+s.below(1000), 1<<s.below(4))
		case wild != memWildAll:
		case r < 12:
			ops = append(ops, 5, s.below(1<<20), memWildSize(&s), s.below(maxSize*2+1))
		case r < 15:
			ops = append(ops, 4, s.below(1<<20), memWildSize(&s), 1<<s.below(13))
		case r < 19:
			ops = append(ops, 7, s.below(1<<20), 1+s.below(7))
		case r < 22:
			ops = append(ops, 8, s.below(1<<20))
		case r < 24:
			ops = append(ops, 9)
		case r < 26:
			ops = append(ops, 1, s.below(1<<20), -1-s.below(1000))
		}
	}
	return ops
}
