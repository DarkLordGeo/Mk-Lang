// Why: Bump's fast path uses wrapping operators (AlignUp, size -% off,
// off +% size) to drop two overflow branches, so its boundaries need proof.
// What: bumpEdgeFixture (core/tests_bumpedge.mk) and the tests that run it.
// How: the same function runs in the evaluator over a small bump heap,
// natively over that heap and over the process heap, at dev and --release.

package std

import (
	"strings"
	"testing"
)

// bumpEdgeFixture is std.core code (core/tests_bumpedge.mk). BumpEdges
// returns a mask of failed checks, 0 when every boundary holds:
//
//	1     the first chunk has 4,016 free bytes, none used
//	2     align-1 requests fill it to size - 3, contiguously
//	4     a zero-size request at align 8 lands exactly on the chunk's end
//	      (off = size), uses nothing, starts no chunk
//	8     the full chunk: one byte at align 8 starts the next chunk (8,176)
//	16    an 8-byte request at align 8 from size - 9 fits exactly at size - 8
//	32    then Put[u64] starts the third chunk (16,368)
//	64    Dup of 3 bytes from size - 3 fits exactly at the chunk's end
//	128   the next Dup: OutOfMemory with the arena unchanged on the small heap
//	      (no fourth chunk fits), a fourth chunk (32,752) on the process heap
//	256   AllocBytes of MaxBytes, MaxBytes at align 8, MaxBytes - 15 and
//	      MaxBytes - 16 at align 8 (the one that reaches the heap, which
//	      refuses MaxBytes bytes) are null
//	512   Slice[u64] of MaxBytes / 8 (BumpSlow's guard) and MaxBytes / 8 + 1
//	      (Slice's overflow check) are OutOfMemory
//	1024  and none of those requests moved used or started a chunk
const bumpEdgeFixture = `import std.raw

const BeBytes = 32768
const BeTable = AllocTable(alloc: BeAlloc, resize: BeResize, free: BeFree)

// A bump heap over the caller's array: its first 8 bytes are the offset;
// blocks follow from byte 16, 16-aligned; resize and free do nothing. A
// request past the array, of any size up to MaxBytes, is null with no
// arithmetic that could overflow.
BeWord(ctx: *u8): *i64 {
    unsafe "ctx is the fixture's array; its first 8 bytes are the offset" {
        return raw.PtrCast[u8, i64](ctx)
    }
}

BeAlloc(ctx: *u8, size: int, align: int): *u8 {
    unsafe "inside the fixture's array" {
        off := int(raw.Read(BeWord(ctx)))
        if size < 0 || size > BeBytes - 16 - off {
            return raw.Null[u8]()
        }
        raw.Write(BeWord(ctx), i64(off + (size + 15) / 16 * 16))
        return raw.Offset(ctx, 16 + off)
    }
}

BeResize(ctx: *u8, p: *u8, old: int, new: int, align: int): bool {
    return false
}

BeFree(ctx: *u8, p: *u8, size: int, align: int) {
}

BeDupFails(a: &Arena, s: str): bool {
    _ = a.Dup(s) else { return true }
    return false
}

BeSliceFails(a: &Arena, n: int): bool {
    _ = a.Slice[u64](n) else { return true }
    return false
}

BumpEdges(h: &Heap, bounded: bool): int {
    var a := Arena.New(h) else { return 1 << 20 }
    defer a.Free()
    var bad := 0
    unsafe "a's header is live until the deferred a.Free()" {
        hd := a.hdr
        al := a.Alloc()
        if (*hd).size != 4016 || (*hd).used != 0 {
            bad = bad | 1
        }
        c1 := (*hd).chunks
        p := AllocBytes(al, 1, 1)
        q := AllocBytes(al, (*hd).size - 4, 1)
        if raw.IsNull(p) || raw.IsNull(q) || !raw.Same(q, raw.Offset(p, 1)) || (*hd).used != (*hd).size - 3 {
            bad = bad | 2
        }
        e := AllocBytes(al, 0, 8)
        if !raw.Same(e, raw.Offset((*hd).base, (*hd).size)) || (*hd).used != (*hd).size || !raw.Same((*hd).chunks, c1) {
            bad = bad | 4
        }
        r := AllocBytes(al, 1, 8)
        if raw.IsNull(r) || raw.Same((*hd).chunks, c1) || !raw.Same(r, (*hd).base) || (*hd).used != 1 || (*hd).size != 8176 {
            bad = bad | 8
        }
        c2 := (*hd).chunks
        s := AllocBytes(al, (*hd).size - 10, 1)
        x := AllocBytes(al, 8, 8)
        if raw.IsNull(s) || !raw.Same(x, raw.Offset((*hd).base, (*hd).size - 8)) || (*hd).used != (*hd).size || !raw.Same((*hd).chunks, c2) {
            bad = bad | 16
        }
        u := a.Put[u64](7) else { return bad | 32 }
        if *u != 7 || raw.Same((*hd).chunks, c2) || (*hd).used != 8 || (*hd).size != 16368 {
            bad = bad | 32
        }
        c3 := (*hd).chunks
        _ = AllocBytes(al, (*hd).size - 11, 1)
        d := a.Dup("abc") else { return bad | 64 }
        if d != "abc" || !raw.Same(raw.StrData(d), raw.Offset((*hd).base, (*hd).size - 3)) || (*hd).used != (*hd).size || !raw.Same((*hd).chunks, c3) {
            bad = bad | 64
        }
        if bounded {
            if !BeDupFails(&a, "z") || (*hd).used != (*hd).size || !raw.Same((*hd).chunks, c3) {
                bad = bad | 128
            }
        } else {
            z := a.Dup("z") else { return bad | 128 }
            if z != "z" || raw.Same((*hd).chunks, c3) || (*hd).size != 32752 || (*hd).used != 1 {
                bad = bad | 128
            }
        }
        u0 := (*hd).used
        c4 := (*hd).chunks
        if !raw.IsNull(AllocBytes(al, MaxBytes, 1)) || !raw.IsNull(AllocBytes(al, MaxBytes, 8)) {
            bad = bad | 256
        }
        if !raw.IsNull(AllocBytes(al, MaxBytes - 15, 1)) || !raw.IsNull(AllocBytes(al, MaxBytes - 16, 8)) {
            bad = bad | 256
        }
        if !BeSliceFails(&a, MaxBytes / 8) || !BeSliceFails(&a, MaxBytes / 8 + 1) {
            bad = bad | 512
        }
        if (*hd).used != u0 || !raw.Same((*hd).chunks, c4) {
            bad = bad | 1024
        }
    }
    return bad
}

export BumpEdgesFixture(): int {
    var mem: [4096]u64 = [0; 4096]
    unsafe "mem is 32 KiB of this frame, 8-aligned" {
        var heap := MakeHeap(raw.PtrCast[u64, u8](raw.DataMut(mut mem[..])), raw.Of(&BeTable))
        return BumpEdges(&heap, true)
    }
}

const BumpEdgesC = BumpEdgesFixture()

export BumpEdgesConst(): int {
    return BumpEdgesC
}

export BumpEdgesHeap(h: &Heap): int {
    return BumpEdges(h, false)
}
`

// bumpEdgeModule reaches the fixture from a user program.
const bumpEdgeModule = `import std.core

export Const(): int {
    return core.BumpEdgesConst()
}

export Fixture(): int {
    return core.BumpEdgesFixture()
}

export OnHeap(h: &Heap): int {
    return core.BumpEdgesHeap(h)
}
`

const bumpEdgeMain = `import std.bumpedge

export Main(sys: &System): WriteError!void {
    var out := sys.Stdout()
    c := bumpedge.Const()
    f := bumpedge.Fixture()
    h := bumpedge.OnHeap(sys.heap)
    try Print(mut out, "const={c} fixture={f} heap={h}\n", (c, f, h))
}
`

func bumpEdgeFixtures(arena string) map[string]string {
	fix := map[string]string{"core/tests_bumpedge.mk": bumpEdgeFixture, "bumpedge/bumpedge.mk": bumpEdgeModule}
	if arena != "" {
		fix["core/mem_arena.mk"] = arena
	}
	return fix
}

// TestStdMem_BumpBoundaries: every boundary above holds in the evaluator and
// natively, at dev and --release (the release build is where the wrapping
// operators change the code).
func TestStdMem_BumpBoundaries(t *testing.T) {
	t.Parallel()
	const want = "const=0 fixture=0 heap=0\n"
	for _, flags := range [][]string{nil, {"--release"}} {
		r := runNative(t, buildNative(t, bumpEdgeFixtures(""), bumpEdgeMain, flags...), "")
		if r.code != 0 || r.stdout != want {
			t.Errorf("%v: exit %d, stdout %q, want %q\nstderr:\n%s", flags, r.code, r.stdout, want, r.stderr)
		}
	}
}

// TestStdMem_BumpBoundariesSeePlantedBugs: a fast path that admits one byte
// past the chunk, and one that ignores the alignment, each fail a check in
// all three legs, so the boundaries test checks what it claims to.
func TestStdMem_BumpBoundariesSeePlantedBugs(t *testing.T) {
	t.Parallel()
	src := readText(t, "core/mem_arena.mk")
	for _, p := range []struct{ name, old, new string }{
		{"one byte past the chunk", "if size <= (*h).size -% off {", "if size <= (*h).size -% off +% 1 {"},
		{"alignment ignored", "off := AlignUp((*h).used, align)", "off := (*h).used"},
	} {
		planted := strings.Replace(src, p.old, p.new, 1)
		if planted == src {
			t.Fatalf("%s: line %q not found in core/mem_arena.mk", p.name, p.old)
		}
		t.Run(p.name, func(t *testing.T) {
			t.Parallel()
			r := runNative(t, buildNative(t, bumpEdgeFixtures(planted), bumpEdgeMain, "--release"), "")
			if r.code != 0 {
				t.Fatalf("exit %d\nstdout:\n%s\nstderr:\n%s", r.code, r.stdout, r.stderr)
			}
			for _, leg := range []string{"const=", "fixture=", "heap="} {
				if strings.Contains(r.stdout, leg+"0 ") || strings.Contains(r.stdout, leg+"0\n") {
					t.Errorf("leg %s passed every check with the planted change: %q", strings.TrimSuffix(leg, "="), r.stdout)
				}
			}
		})
	}
}
