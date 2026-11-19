// Why: mem.Custom names &AllocTable, so a package outside std must be able to
// name the table to build its own allocator.
// What: TestRt_MemAllocTable, through `mem.AllocTable` (an alias of std.core's).
// How: builds and runs a user package with its own allocator table; without
// the export the program is E0201 at mem.AllocTable.

package std

import (
	"strings"
	"testing"
)

const rt10bAllocTableMain = `import std.mem
import std.raw

struct Ctx {
    live: Cell[int]
    heap: &Alloc
}

MyAlloc(ctx: *u8, n: int, align: int): *u8 {
    unsafe "ctx is Main's Ctx, live for the whole program" {
        c := &*raw.PtrCast[u8, Ctx](ctx)
        p := c.heap.Bytes(n, align)
        if !raw.IsNull(p) {
            c.live.Store(c.live.Load() + 1)
        }
        return p
    }
}

MyResize(ctx: *u8, p: *u8, old: int, new: int, align: int): bool {
    return false
}

MyFree(ctx: *u8, p: *u8, n: int, align: int) {
    unsafe "ctx is Main's Ctx; p came from MyAlloc with n and align" {
        c := &*raw.PtrCast[u8, Ctx](ctx)
        c.heap.FreeBytes(p, n, align)
        c.live.Store(c.live.Load() - 1)
    }
}

const Table = mem.AllocTable(alloc: MyAlloc, resize: MyResize, free: MyFree)

export Main(sys: &System) {
    ctx := Ctx(live: Cell[int].New(0), heap: sys.heap.Alloc())
    unsafe "ctx and Table outlive the Alloc and its blocks" {
        a := mem.Custom(raw.PtrCast[Ctx, u8](raw.Of(&ctx)), &Table)
        var l := HeapList[int].New(&a)
        for i in 0..100 {
            l.Push(&a, i) else {
                break
            }
        }
        peak := ctx.live.Load()
        var sum := 0
        for i in 0..l.Len() {
            sum += l[i]
        }
        l.Free(&a)
        ok := peak > 0
        left := ctx.live.Load()
        sys.Print("{ok} {sum} {left}\n", (ok, sum, left)) else {}
    }
}
`

// TestRt_MemAllocTable builds and runs a user package whose allocator
// is a mem.AllocTable given to mem.Custom: every block it handed out is
// freed (live 0) and the list it served held its values.
func TestRt_MemAllocTable(t *testing.T) {
	exe := rt10bBuildPkg(t, "custom", rt10bAllocTableMain, "dev")
	out, status := rt10bRun(t, exe, nil)
	if status != 0 || strings.TrimSpace(out) != "true 4950 0" {
		t.Fatalf("status %d, output %q, want 0 and \"true 4950 0\"", status, out)
	}
}
