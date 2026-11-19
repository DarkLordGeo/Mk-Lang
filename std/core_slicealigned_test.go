// Why: Arena.SliceAligned zero-fills and takes a caller's alignment, so it
// needs the zero-valid check and must refuse bad alignments.
// What: E0701 at the call for types that are not zero-valid, panics on a bad
// alignment or a negative count, and E0901 in a compile-time evaluation.
// How: small programs checked, built and run; the alignment and zero-fill of
// the result are std/core/tests_arena.mk's SliceAligned blocks.

package std

import (
	"fmt"
	"strings"
	"testing"
)

const core11SliceAlignedBounds = `struct Pt {
    x: int
    y: i32
}

export Main(sys: &System): OutOfMemory!void {
    a := sys.arena
    _ = try a.SliceAligned[&int](1, 64)
    _ = try a.SliceAligned[str](1, 64)
    _ = try a.SliceAligned[Box[int]](1, 64)
    _ = try a.SliceAligned[u8](1, 64)
    _ = try a.SliceAligned[f32](1, 64)
    _ = try a.SliceAligned[Pt](1, 64)
    g: fn(&Arena, int, int): OutOfMemory!mut []&int = Arena.SliceAligned[&int]
    _ = try g(a, 1, 64)
    _ = try Ap(Arena.SliceAligned[&int], a)
    h: fn(&Arena, int, int): OutOfMemory!mut []Pt = Arena.SliceAligned[Pt]
    _ = try h(a, 1, 64)
}

Ap(f: fn(&Arena, int, int): OutOfMemory!mut []&int, a: &Arena): OutOfMemory!int {
    s := try f(a, 1, 64)
    return *s[0]
}
`

// TestCore_SliceAlignedZeroValid: a reference, a str and a Box are E0701
// at the call, as for Arena.Slice (TestArenaSliceZeroValid); so is
// a reference taken as a fn value, declared or passed as an argument, as
// for Arena.Slice (TestArenaSliceZeroValid; without the
// check a fn value could hand SliceAligned a null reference); u8, f32 and a struct of integers are
// accepted, at a call and as a fn value, and std draws nothing.
func TestCore_SliceAlignedZeroValid(t *testing.T) {
	t.Parallel()
	out, _ := checkProgram(t, nil, core11SliceAlignedBounds)
	if strings.Contains(out, "std/") {
		t.Errorf("std drew a diagnostic:\n%s", out)
	}
	want := []string{"8 E0701", "9 E0701", "10 E0701", "14 E0701", "16 E0701"}
	if got := diagCodes(out); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("want %q, got %q\n%s", want, got, out)
	}
	if !strings.Contains(out, "`Arena.SliceAligned` zero-fills its elements") {
		t.Errorf("the E0701 does not name Arena.SliceAligned:\n%s", out)
	}
}

// TestCore_SliceAlignedPanics: each bad argument, as a run-time value,
// panics with SliceAligned's message, at dev and release; the good
// alignments around them run.
func TestCore_SliceAlignedPanics(t *testing.T) {
	t.Parallel()
	src := `export Main(sys: &System): OutOfMemory|ParseError!void {
    n := Cast[int](try sys.args[1].ParseInt())
    al := Cast[int](try sys.args[2].ParseInt())
    s := try sys.arena.SliceAligned[u8](n, al)
    var w := sys.Stdout()
    Print(mut w, "ok {n}\n", (n: s.Len())) else {
        return
    }
}
`
	for _, flags := range [][]string{nil, {"--release"}} {
		exe, out, status := h2Try(t, nil, src, flags...)
		if status != 0 {
			if strings.Contains(out, "no usable LLVM") {
				t.Skipf("needs the LLVM toolchain: %s", out)
			}
			t.Fatalf("mk build %v: exit %d\n%s", flags, status, out)
		}
		for _, c := range []struct {
			n, align int
			msg      string
		}{
			{3, 1, ""}, {3, 2, ""}, {3, 65536, ""}, {0, 4096, ""},
			{3, 0, "alignment"}, {3, -64, "alignment"}, {3, 3, "alignment"},
			{3, 96, "alignment"}, {3, 131072, "alignment"}, {-1, 64, "negative count"},
		} {
			r := runNative(t, exe, "", fmt.Sprint(c.n), fmt.Sprint(c.align))
			if c.msg == "" {
				if r.code != 0 || strings.TrimSpace(r.stdout) != fmt.Sprintf("ok %d", c.n) {
					t.Errorf("%v n %d align %d: exit %d, stdout %q, stderr %q", flags, c.n, c.align, r.code, r.stdout, r.stderr)
				}
				continue
			}
			if r.code == 0 || !strings.Contains(r.stderr, "Arena.SliceAligned: "+c.msg) {
				t.Errorf("%v n %d align %d: exit %d, want the panic %q; stderr %q", flags, c.n, c.align, r.code, c.msg, r.stderr)
			}
		}
	}
}

const core11SliceAlignedEval = `align(64) struct L64 {
    a: u64
}

U8At(a: &Arena, al: int): int {
    s := a.SliceAligned[u8](5, al) else {
        return 0
    }
    return s.Len()
}

U64At(a: &Arena, al: int): int {
    s := a.SliceAligned[u64](3, al) else {
        return 0
    }
    return s.Len()
}

LAt(a: &Arena, al: int): int {
    s := a.SliceAligned[L64](2, al) else {
        return 0
    }
    return s.Len()
}

ViaSub(a: &Arena, al: int): int {
    var b := a.Sub() else {
        return 0
    }
    defer b.Free()
    return U8At(&b, al)
}

const A = U8At(EvalArena(), 64)
const B = U8At(EvalArena(), 2)
const C = U64At(EvalArena(), 16)
const D = ViaSub(EvalArena(), 4096)
const E = U8At(EvalArena(), 1)
const F = U64At(EvalArena(), 8)
const G = LAt(EvalArena(), 64)
const H = LAt(EvalArena(), 16)
const I = ViaSub(EvalArena(), 1)

export Main(sys: &System) {
    Assert(A + B + C + D + E + F + G + H + I > 0)
}
`

// TestCore_SliceAlignedEvalPanics: a slice made in a compile-time evaluation becomes
// static data aligned to its element type only, so SliceAligned with an
// align over the type's is E0901 there, with its message, through the
// evaluation's arena and through a Sub of it (u8 at 64 and at 2, u64 at
// 16, u8 at 4096 in a Sub); an align at most the type's evaluates (u8 at
// 1, u64 at 8, an align(64) type at 64 and at 16, u8 at 1 in a Sub). The
// run-time side, where an align over the type's is honoured, is
// TestCore_SliceAlignedPanics's good rows and tests_arena.mk's
// SliceAligned blocks.
func TestCore_SliceAlignedEvalPanics(t *testing.T) {
	t.Parallel()
	out, _ := checkProgram(t, nil, core11SliceAlignedEval)
	want := []string{"34 E0901", "35 E0901", "36 E0901", "37 E0901"}
	if got := diagCodes(out); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("want %q, got %q\n%s", want, got, out)
	}
	if n := strings.Count(out, "Arena.SliceAligned: an alignment over the element type's at compile time"); n != 2*len(want) {
		t.Errorf("want the compile-time alignment panic %d times (heading and message), got %d:\n%s", 2*len(want), n, out)
	}
}

const core11SliceAlignedEvalSources = `import std.mem

NewInAt(ea: &Arena, al: int): int {
    var a := Arena.NewIn(ea.Alloc()) else {
        return 0
    }
    defer a.Free()
    s := a.SliceAligned[u8](5, al) else {
        return 0
    }
    return s.Len()
}

CountingAt(ea: &Arena, al: int): int {
    pc := ea.Put(mem.Counting.New(ea.Alloc())) else {
        return 0
    }
    var a := Arena.NewIn(pc.Alloc()) else {
        return 0
    }
    defer a.Free()
    s := a.SliceAligned[u8](5, al) else {
        return 0
    }
    return s.Len()
}

Leak(ea: &Arena, al: int): []u8 from ea {
    a := Arena.NewIn(ea.Alloc()) else {
        return "".AsBytes()
    }
    pa := ea.Put(a) else {
        a.Free()
        return "".AsBytes()
    }
    s := pa.SliceAligned[u8](64, al) else {
        return "".AsBytes()
    }
    return s[..]
}

const A = NewInAt(EvalArena(), 64)
const B = CountingAt(EvalArena(), 64)
const C = Leak(EvalArena(), 64)
const D = NewInAt(EvalArena(), 1)
const E = CountingAt(EvalArena(), 1)
const F = Leak(EvalArena(), 1)

export Main(sys: &System) {
    Assert(A + B + C.Len() + D + E + F.Len() > 0)
}
`

// TestCore_SliceAlignedEvalPanicsAnySource: SliceAligned asks whether an
// evaluation runs, not which arena it serves, so an align over the type's
// is E0901 at compile
// time whatever the arena's chunk source: an Arena.NewIn over the
// evaluation arena's Alloc, one over a mem.Counting kept in the evaluation
// arena, and one moved into the evaluation arena with Put whose slice is
// the const itself (each of these once checked clean and printed a slice
// off its alignment when the check asked IsEvalArena).
// The same three at align 1 evaluate.
func TestCore_SliceAlignedEvalPanicsAnySource(t *testing.T) {
	t.Parallel()
	out, _ := checkProgram(t, nil, core11SliceAlignedEvalSources)
	want := []string{"42 E0901", "43 E0901", "44 E0901"}
	if got := diagCodes(out); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("want %q, got %q\n%s", want, got, out)
	}
	if n := strings.Count(out, "Arena.SliceAligned: an alignment over the element type's at compile time"); n != 2*len(want) {
		t.Errorf("want the compile-time alignment panic %d times (heading and message), got %d:\n%s", 2*len(want), n, out)
	}
}
