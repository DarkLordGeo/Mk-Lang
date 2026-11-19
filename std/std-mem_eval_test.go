// Why: std.mem's allocators can be reached from compile-time evaluation,
// where only some of them make sense.
// What: a Counting over EvalArena() evaluates and agrees with the same
// function run natively over sys.arena; a Fixed is refused (E0901).
// How: one program run both ways; Fixed's New reads the buffer's address.

package std

import (
	"strings"
	"testing"
)

// memEvalStats returns Peak, the Bytes and Live of a three-element list, and
// the Bytes and Live after everything is freed, packed in one int.
const memEvalStats = `import std.mem

Stats(a: &Arena): int {
    c := mem.Counting.New(a.Alloc())
    var xs := HeapList[int].New(c.Alloc())
    for i in 0..10 {
        xs.Push(c.Alloc(), i) else {
            break
        }
    }
    xs.Free(c.Alloc())
    var ys := HeapList[int].New(c.Alloc())
    for i in 0..3 {
        ys.Push(c.Alloc(), i) else {
            break
        }
    }
    mid := c.Bytes() * 100 + c.Live()
    ys.Free(c.Alloc())
    return c.Peak() * 1000000 + mid * 10 + c.Bytes() + c.Live()
}

const S = Stats(EvalArena())

export Main(sys: &System): WriteError!void {
    var out := sys.Stdout()
    r := Stats(sys.arena)
    try Print(mut out, "{S} {r}\n", (S, r))
}
`

// TestSTDMEM_CountingAtCompileTime: the const and the native call print the
// same counts, and they are the hand-computed ones: lists grow 4, 8, 16
// elements of 8 bytes, so the peak is 128 bytes; three elements live in one
// allocation of 4 (32 bytes, Live 1); after the frees both are 0.
func TestSTDMEM_CountingAtCompileTime(t *testing.T) {
	t.Parallel()
	want := "128032010 128032010\n"
	for _, flags := range [][]string{nil, {"--release"}} {
		if out := collExec(t, memBuild(t, memEvalStats, flags...)); out != want {
			t.Errorf("%v: got %q, want %q", flags, out, want)
		}
	}
}

// TestSTDMEM_FixedRefusedAtCompileTime: a Fixed in a const's evaluation is
// E0901 with the pointer-bytes refusal, traced into std/mem's New.
func TestSTDMEM_FixedRefusedAtCompileTime(t *testing.T) {
	t.Parallel()
	src := `import std.mem

Use(a: &Arena): int {
    var buf: [64]u8 = [0; 64]
    var f := mem.Fixed.New(mut buf[..])
    f.Reset()
    return 1
}

const F = Use(EvalArena())

export Main(sys: &System): WriteError!void {
    var out := sys.Stdout()
    try Print(mut out, "{F}\n", (F))
}
`
	out, status := memCheck(t, src)
	if status == 0 || !strings.Contains(out, "E0901") || !strings.Contains(out, "pointer-bytes") || !strings.Contains(out, "std/mem/mem.mk") {
		t.Errorf("want E0901 pointer-bytes traced into std/mem/mem.mk, got exit %d:\n%s", status, out)
	}
}
