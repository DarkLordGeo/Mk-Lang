// Why: a fault must be called a stack overflow exactly when it hits a guard
// page, on both sides of every boundary.
// What: tests of FaultKind's two Linux rules (the lowest page of a task
// slot, the guard page below a carrier's system stack), SysStackOf, which
// the Windows fault filter's in_c test uses, and RtState's size.
// How: addresses at and around each boundary are classified directly.

import std.raw
import std.sys

// FkOf is FaultKind of a fault at addr reported as kind (outside the test
// block, whose System parameter hides the sys module).
FkOf(kind: u32, addr: uint): u32 {
    f := sys.FaultInfo(kind: kind, addr: addr, pc: 0, sp: 0)
    return FaultKind(&f)
}

// FkPages is a fresh two-page reservation (no access).
FkPages(): *u8 {
    return sys.Reserve(8192, 4096)
}

test "RT-FaultKind-SysGuard the system-stack guard page, the stack and their edges" {
    // Entry 255 belongs to carrier 255, which no test run starts. lo is in
    // a fresh two-page reservation, so it is in no slot region (a fixed
    // address can land in one: ASLR places the 1 TiB regions anywhere).
    p := FkPages()
    Assert(!raw.IsNull(p))
    lo := raw.Addr(p) + 4096
    Assert(SlotTop(lo) == 0 && SlotTop(lo - 4096) == 0)
    SetSysStack(255, lo, lo + 4096)
    if Target.os == Os.Linux {
        Assert(FkOf(2, lo - 4096) == 1)
        Assert(FkOf(2, lo - 1) == 1)
    } else {
        // Windows reports its own stack overflows; nothing is recorded.
        Assert(FkOf(2, lo - 4096) == 2)
        Assert(FkOf(2, lo - 1) == 2)
    }
    Assert(FkOf(2, lo) == 2)
    Assert(FkOf(2, lo - 4097) == 2)
    Assert(FkOf(1, 16) == 1)
    // Addresses near 0 and near the top never match an empty entry.
    Assert(FkOf(2, 16) == 2)
    Assert(FkOf(2, ~Trunc[uint](0)) == 2)
    // The stack itself, [lo, lo + 4 KiB), is carrier 255's on both targets.
    Assert(SysStackOf(lo) == 255)
    Assert(SysStackOf(lo + 4095) == 255)
    Assert(SysStackOf(lo + 4096) != 255)
    Assert(SysStackOf(lo - 1) != 255)
    unsafe "the RtState page: give entry 255 back" {
        (*R()).sys_lo[255].Store(0)
        (*R()).sys_hi[255].Store(0)
    }
    Assert(FkOf(2, lo - 4096) == 2)
    Assert(SysStackOf(lo) == -1)
}

test "RT-StateFits the RtState record fits its 64 KiB page" {
    Assert(SizeOf[RtState]() <= StateBytes)
}
