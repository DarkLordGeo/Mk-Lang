// Why: an embedder's own allocator functions must see every request a
// program makes through mem.Custom.
// What: tests of mem.Custom, Alloc.Bytes and FreeBytes over a counting
// allocator table of the test's own.
// How: run by `mk test --std=std.mem -run "Custom "`.

import std.core
import std.raw

// CountCtx is a program's own allocator state: it forwards to the process
// heap and counts the live blocks.
struct CountCtx {
    live: Cell[int]
    heap: &Alloc
}

CountAlloc(ctx: *u8, n: int, align: int): *u8 {
    unsafe "ctx is the test's CountCtx, live for the whole test" {
        c := &*raw.PtrCast[u8, CountCtx](ctx)
        p := c.heap.Bytes(n, align)
        if !raw.IsNull(p) {
            c.live.Store(c.live.Load() + 1)
        }
        return p
    }
}

CountResize(ctx: *u8, p: *u8, old: int, new: int, align: int): bool {
    return false
}

CountFree(ctx: *u8, p: *u8, n: int, align: int) {
    unsafe "ctx is the test's CountCtx; p came from CountAlloc with n and align" {
        c := &*raw.PtrCast[u8, CountCtx](ctx)
        c.heap.FreeBytes(p, n, align)
        c.live.Store(c.live.Load() - 1)
    }
}

const CountTable = core.AllocTable(alloc: CountAlloc, resize: CountResize, free: CountFree)

test "Custom an Alloc over a program's own table serves a HeapList and counts its blocks" (sys: &System) {
    ctx := CountCtx(live: Cell[int].New(0), heap: sys.heap.Alloc())
    unsafe "ctx outlives the Alloc and every block: all are freed below" {
        a := Custom(raw.PtrCast[CountCtx, u8](raw.Of(&ctx)), &CountTable)
        p := a.Bytes(64, 16)
        Assert(!raw.IsNull(p))
        Assert(raw.Addr(p) % 16 == 0)
        Assert(ctx.live.Load() == 1)
        raw.Fill(p, 0xAB, 64)
        a.FreeBytes(p, 64, 16)
        Assert(ctx.live.Load() == 0)
        var l := HeapList[int].New(&a)
        for i in 0..100 {
            l.Push(&a, i) else {
                Assert(false)
            }
        }
        Assert(l.Len() == 100 && l[99] == 99)
        Assert(ctx.live.Load() >= 1)
        l.Free(&a)
        Assert(ctx.live.Load() == 0)
    }
}
