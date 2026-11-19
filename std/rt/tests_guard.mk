// Why: on Linux, a guard page per idle task must not cost a mapping each,
// or idle tasks hit vm.max_map_count.
// What: tests that a prepared slot's guard is a light guard where the kernel
// has one (6.13 and later: no mapping per slot), else the PROT_NONE page
// (two mappings per slot); MK_TEST_GUARDS=prot forces the older form in a
// dev build. Windows slots keep PAGE_GUARD and never take a Linux guard
// kind.
// How: prepares 10,000 slots and counts the process's mappings before and
// after.

import std.core
import std.raw
import std.sys

// GMaps is the number of mappings of /proc/self/maps that start inside
// the Default slot region, or -1 off Linux. Only the region's own lines are
// counted, so blocks running in parallel in one process (mk test: heap
// segments, pool-thread stacks) do not move the count. One pass over the
// file: each line starts with its start address in hex, then '-'.
GMaps(): int {
    if Target.os != Os.Linux {
        return -1
    }
    r := RegionOf(ClassDefault)
    var lo: u64 = 0
    var hi: u64 = 0
    unsafe "the region record, written once at start" {
        lo = u64((*r).base)
        hi = lo + Trunc[u64]((*r).slots) * Trunc[u64]((*r).slot_bytes)
    }
    o := sys.OpenFile("/proc/self/maps", 0, false)
    Assert(o.err == core.SysErr.None)
    h := Trunc[uint](o.value)
    var b: [16384]u8 = [0; 16384]
    var lines := 0
    var start: u64 = 0
    var in_addr := true
    for {
        n := sys.ReadSync(h, mut b[..], -1)
        Assert(n.err == core.SysErr.None)
        if n.value == 0 {
            break
        }
        for i in 0..Trunc[int](n.value) {
            c := b[i]
            if c == 10 {
                if start >= lo && start < hi {
                    lines += 1
                }
                start = 0
                in_addr = true
            } else if in_addr {
                if c >= 48 && c <= 57 {
                    start = start * 16 + u64(c - 48)
                } else if c >= 97 && c <= 102 {
                    start = start * 16 + u64(c - 87)
                } else {
                    in_addr = false
                }
            }
        }
    }
    _ = sys.Close(h)
    return lines
}

// GPrepared is how many never-used Default slots the region has prepared.
GPrepared(): int {
    r := RegionOf(ClassDefault)
    unsafe "the region record; next is read under its lock" {
        Lock(&(*r).lock)
        n := (*r).next
        Unlock(&(*r).lock)
        return n
    }
}

// GTakeMany takes n Default slots (fresh ones once the free stack is empty),
// then gives them back; the slots stay prepared. It returns how many
// fresh slots it prepared.
GTakeMany(n: int): int {
    before := GPrepared()
    var hs: [10000]u64 = [0; 10000]
    Assert(n <= 10000)
    for i in 0..n {
        h := TakeSlot(ClassDefault)
        Assert(!raw.IsNull(h))
        hs[i] = u64(raw.Addr(h))
    }
    fresh := GPrepared() - before
    for i in 0..n {
        unsafe "a slot this function took; no task runs in it" {
            FreeSlot(raw.FromAddr[SlotHeader](Trunc[uint](hs[i])))
        }
    }
    return fresh
}

// GCheck prepares n slots and checks the mappings they added against the
// guard kind; force_prot: the block asked for MK_TEST_GUARDS=prot.
GCheck(n: int, force_prot: bool) {
    m0 := GMaps()
    fresh := GTakeMany(n)
    m1 := GMaps()
    kind := GuardKind()
    if Target.os != Os.Linux {
        Assert(kind == GuardUntried && m0 == -1 && m1 == -1)
        return
    }
    Assert(fresh >= n - 64)
    if force_prot && DevBuild() {
        Assert(kind == GuardProt)
    }
    added := m1 - m0
    if kind == GuardLight {
        // The read-write slots merge into one mapping with their
        // neighbours; the guards live in the page tables.
        Assert(added <= 2)
    } else {
        Assert(kind == GuardProt)
        // A PROT_NONE page and a read-write range per slot (the bound on
        // idle tasks), give or take a merge at the ends of the prepared run and a slot
        // another block prepares while this one reads (mk test; next counts it).
        Assert(added >= 2 * fresh - 4 && added <= 2 * fresh + 4)
    }
}

test "RT-Slot-LightGuard 10,000 prepared slots add no mapping each with light guards, two each with PROT_NONE guards" {
    GCheck(10000, false)
}

test "RT-Slot-LightGuard-Prot [env MK_TEST_GUARDS=prot] the PROT_NONE form on demand: two mappings per prepared slot" {
    GCheck(10000, true)
}
