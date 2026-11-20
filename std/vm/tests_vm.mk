// Why: std.vm hands out raw address space; reserve, commit, protect and the
// code regions must work as documented on every target.
// What: tests of Region and CodeRegion, run by `mk test --std=std.vm`.
// How: in std.vm's own module the unsafe-only rule does not apply
// (internal/check's unsafeDoors); the unsafe blocks below are for the raw
// reads and writes. A read of a Protect(None) page faults; that test is
// std.runtime's (tests_fault.mk, with runtime.AddFaultFilter).

import std.core
import std.raw

test "VM reserve 32 GiB, commit 32 KiB at each end, write and read them" {
    size := 32 << 30
    r := Reserve(size, 1 << 20) else {
        Assert(false)
        return
    }
    Assert(r.Size() == size)
    Assert(r.Base() % (1 << 20) == 0)
    block := 32 << 10
    r.Commit(0, block) else {
        Assert(false)
    }
    r.Commit(size - block, block) else {
        Assert(false)
    }
    unsafe "both blocks were just committed read-write" {
        lo := raw.FromAddr[u64](r.Base())
        hi := raw.FromAddr[u64](r.Base() + Trunc[uint](size - 8))
        Assert(raw.Read(lo) == 0 && raw.Read(hi) == 0)
        raw.Write(lo, 0x1122334455667788)
        raw.Write(hi, 0x8877665544332211)
        Assert(raw.Read(lo) == 0x1122334455667788)
        Assert(raw.Read(hi) == 0x8877665544332211)
    }
    r.Decommit(0, block)
    r.Commit(0, block) else {
        Assert(false)
    }
    unsafe "recommitted: a fresh page reads zero" {
        Assert(raw.Read(raw.FromAddr[u64](r.Base())) == 0)
    }
    r.Release()
}

test "VM misuse is Misaligned" {
    ps := PageSize()
    Assert(ps >= 4096 && ps % 4096 == 0)
    Assert(ReserveMisaligned(0, ps))
    Assert(ReserveMisaligned(ps + 1, ps))
    Assert(ReserveMisaligned(ps, 3))
    r := Reserve(4 * ps, ps) else {
        Assert(false)
        return
    }
    r.Commit(1, ps) else e {
        Assert(IsMisaligned(e))
    }
    r.Commit(0, ps + 1) else e {
        Assert(IsMisaligned(e))
    }
    r.Commit(3 * ps, 2 * ps) else e {
        Assert(IsMisaligned(e))
    }
    r.Commit(-ps, ps) else e {
        Assert(IsMisaligned(e))
    }
    r.Protect(0, 5 * ps, Prot.Read) else e {
        Assert(IsMisaligned(e))
    }
    r.Release()
}

test "VM Protect Read keeps a committed page readable, ReadWrite writable again" {
    ps := PageSize()
    r := Reserve(2 * ps, ps) else {
        Assert(false)
        return
    }
    r.Commit(0, 2 * ps) else {
        Assert(false)
    }
    p := r.Base()
    unsafe "the page is committed read-write" {
        raw.Write(raw.FromAddr[u32](p), 7)
    }
    r.Protect(0, ps, Prot.Read) else {
        Assert(false)
    }
    unsafe "the page is committed and readable" {
        Assert(raw.Read(raw.FromAddr[u32](p)) == 7)
    }
    r.Protect(0, ps, Prot.ReadWrite) else {
        Assert(false)
    }
    unsafe "the page is read-write again" {
        raw.Write(raw.FromAddr[u32](p), 9)
        Assert(raw.Read(raw.FromAddr[u32](p)) == 9)
    }
    r.Protect(ps, ps, Prot.None) else {
        Assert(false)
    }
    r.Release()
}

// IsMisaligned is whether e is Misaligned.
IsMisaligned(e: Error): bool {
    return match e {
        Misaligned { true }
        _ { false }
    }
}

// ReserveMisaligned is whether Reserve(size, align) fails with Misaligned.
ReserveMisaligned(size: int, align: int): bool {
    r := Reserve(size, align) else e {
        return IsMisaligned(e)
    }
    r.Release()
    return false
}

// RetArm64 is the aarch64 code `movz w0, #0; ret`, whose imm16 is bits 5
// to 20 of the first word; WriteRet writes it, or x86_64's
// `mov eax, v; ret`, with the value it returns.
const RetArm64: [8]u8 = [
    0x00, 0x00, 0x80, 0x52, // movz w0, #0
    0xC0, 0x03, 0x5F, 0xD6 // ret
]

// RetLen is the length of the target's `return v` code.
RetLen(): int {
    return if Target.arch == Arch.Arm64 { 8 } else { 6 }
}

// NoCodeRegions is whether the target makes no code region, where every
// block that needs one returns at once: macOS. Apple silicon runs
// generated code only from a MAP_JIT mapping, which is one view whose
// writability is switched per thread, never the second, writable view a
// CodeRegion has, and macOS's layer makes none on Intel either.
NoCodeRegions(): bool {
    return Target.os == core.Os.Macos
}

// CallAt calls the code at addr, which takes nothing and returns a u32 in
// eax or w0, from inline assembly (raw.FnFromAddr is tested in std.mem).
CallAt(addr: uint): u32 {
    var r: u32 = 0
    if Target.arch == Arch.X64 {
        unsafe "addr is a code region's executable entry holding WriteRet's code, which touches no memory and preserves every callee-saved register" {
            // The call pushes below rsp: step over System V's 128-byte red
            // zone first, so it never overwrites a leaf frame's locals.
            asm("sub rsp, 128\ncall {a}\nadd rsp, 128", a: in(reg) addr, out("eax") r, clobber_abi("C"))
        }
    }
    if Target.arch == Arch.Arm64 {
        unsafe "addr is a code region's executable entry holding WriteRet's code, which touches no memory and preserves every callee-saved register" {
            // blr writes only x30, and AAPCS64 has no red zone below sp.
            asm("blr {a}", a: in(reg) addr, out("w0") r, clobber_abi("C"))
        }
    }
    return r
}

test "VM a code region runs code written through its writable view" {
    if NoCodeRegions() {
        return
    }
    c := CodeRegion.New(100) else {
        Assert(false)
        return
    }
    Assert(c.Size() == PageSize())
    var w := c.Writable()
    WriteRet(w, 0, 42)
    n := RetLen()
    c.Flush(0, n)
    e := c.Entry(0)
    first: u8 = if Target.arch == Arch.Arm64 { 0x40 } else { 0xB8 }
    last: u8 = if Target.arch == Arch.Arm64 { 0xD6 } else { 0xC3 }
    unsafe "the executable view is readable: it holds the bytes the writable view wrote" {
        Assert(raw.Read(raw.FromAddr[u8](e)) == first)
        Assert(raw.Read(raw.FromAddr[u8](e + Trunc[uint](n - 1))) == last)
    }
    Assert(e != raw.Addr(raw.DataMut(w)))
    Assert(CallAt(e) == 42)
    // A second function, written later at another offset.
    WriteRet(w, 64, 7)
    c.Flush(64, n)
    Assert(CallAt(c.Entry(64)) == 7)
    Assert(CallAt(e) == 42)
    c.Release()
}

test "VM a code region of a size that is not positive is Misaligned" {
    c := CodeRegion.New(0) else e {
        Assert(IsMisaligned(e))
        return
    }
    c.Release()
    Assert(false)
}

test "VM Protect of an uncommitted page is Os(487) on every target" {
    ps := PageSize()
    r := Reserve(4 * ps, ps) else {
        Assert(false)
        return
    }
    r.Commit(0, ps) else {
        Assert(false)
    }
    var code: i64 = 0
    r.Protect(0, 2 * ps, Prot.Read) else e {
        code = match e {
            Os(code: c) { c }
            _ { -1 }
        }
    }
    Assert(code == 487)
    r.Protect(0, ps, Prot.Read) else {
        Assert(false)
    }
    r.Decommit(0, ps)
    code = 0
    r.Protect(0, ps, Prot.ReadWrite) else e {
        code = match e {
            Os(code: c) { c }
            _ { -1 }
        }
    }
    Assert(code == 487)
    r.Release()
}

test "VM Decommit of a range that is not whole pages panics [exit 101: Region.Decommit: not whole pages inside the region]" {
    ps := PageSize()
    r := Reserve(2 * ps, ps) else {
        return
    }
    r.Decommit(1, ps)
    r.Release()
}

test "VM CodeRegion.Entry outside the region panics [exit 101: CodeRegion.Entry: offset outside the region]" (sys: &System) {
    if NoCodeRegions() {
        sys.Print("skip: macOS gives std no code regions\n", ()) else {
        }
        return
    }
    c := CodeRegion.New(64) else {
        return
    }
    _ = c.Entry(c.Size())
    c.Release()
}

// Commit over committed pages keeps
// their contents on every target; it commits only the runs of uncommitted
// pages (on Linux a map-again would zero them: 0 on bd81ffa2's Linux run).
test "VM Commit over committed pages keeps their contents and commits only the rest" {
    ps := PageSize()
    r := Reserve(8 * ps, ps) else {
        Assert(false)
        return
    }
    r.Commit(ps, ps) else {
        Assert(false)
    }
    r.Commit(3 * ps, 2 * ps) else {
        Assert(false)
    }
    unsafe "pages 1, 3 and 4 were just committed read-write" {
        raw.Write(raw.FromAddr[u64](r.Base() + Trunc[uint](ps)), 77)
        raw.Write(raw.FromAddr[u64](r.Base() + Trunc[uint](3 * ps)), 78)
        raw.Write(raw.FromAddr[u64](r.Base() + Trunc[uint](4 * ps + 8)), 79)
    }
    r.Commit(0, 8 * ps) else {
        Assert(false)
    }
    unsafe "every page is committed now" {
        Assert(raw.Read(raw.FromAddr[u64](r.Base() + Trunc[uint](ps))) == 77)
        Assert(raw.Read(raw.FromAddr[u64](r.Base() + Trunc[uint](3 * ps))) == 78)
        Assert(raw.Read(raw.FromAddr[u64](r.Base() + Trunc[uint](4 * ps + 8))) == 79)
        for k in [0, 2, 5, 6, 7] {
            a := raw.FromAddr[u64](r.Base() + Trunc[uint](k * ps))
            Assert(raw.Read(a) == 0)
            raw.Write(a, 1)
        }
    }
    r.Protect(0, 8 * ps, Prot.Read) else {
        Assert(false)
    }
    r.Release()
}

// The commit bitmap is kept a word (64 pages) at a time; ranges that
// start and end inside words and cross them are marked and tested exactly.
test "VM the commit bitmap is exact across its 64-page words" {
    ps := PageSize()
    r := Reserve(200 * ps, ps) else {
        Assert(false)
        return
    }
    r.Commit(60 * ps, 75 * ps) else {
        Assert(false)
    }
    r.Protect(60 * ps, 75 * ps, Prot.Read) else {
        Assert(false)
    }
    var cut := 0
    r.Protect(59 * ps, 2 * ps, Prot.Read) else {
        cut += 1
    }
    r.Protect(134 * ps, 2 * ps, Prot.Read) else {
        cut += 1
    }
    Assert(cut == 2)
    r.Decommit(64 * ps, 64 * ps)
    r.Protect(60 * ps, 4 * ps, Prot.ReadWrite) else {
        Assert(false)
    }
    r.Protect(128 * ps, 7 * ps, Prot.ReadWrite) else {
        Assert(false)
    }
    r.Protect(127 * ps, ps, Prot.ReadWrite) else {
        cut += 1
    }
    r.Protect(64 * ps, ps, Prot.ReadWrite) else {
        cut += 1
    }
    Assert(cut == 4)
    r.Release()
}

// WriteRet writes the target's code `return v` at offset o of w: x86_64's
// `mov eax, v; ret`, or RetArm64 with v in its immediate.
WriteRet(w: mut []u8, o: int, v: u8) {
    if Target.arch == Arch.Arm64 {
        for i in 0..8 {
            w[o + i] = RetArm64[i]
        }
        SetImm(w, o, v)
        return
    }
    w[o] = 0xB8
    w[o + 1] = v
    w[o + 2] = 0
    w[o + 3] = 0
    w[o + 4] = 0
    w[o + 5] = 0xC3
}

// SetImm writes v into the immediate of the code WriteRet wrote at offset
// o of w: x86_64's imm32 byte 1; aarch64's imm16, bits 5 to 12 of v's
// value, which lie in bytes 0 and 1.
SetImm(w: mut []u8, o: int, v: u8) {
    if Target.arch == Arch.Arm64 {
        imm := u32(v) << 5
        w[o] = RetArm64[0] | Trunc[u8](imm)
        w[o + 1] = RetArm64[1] | Trunc[u8](imm >> 8)
        return
    }
    w[o + 1] = v
}

// ImmOf is the value SetImm wrote at offset o of w.
ImmOf(w: []u8, o: int): u8 {
    if Target.arch == Arch.Arm64 {
        return Trunc[u8]((u32(w[o]) >> 5) | (u32(w[o + 1]) << 3))
    }
    return w[o + 1]
}

// Seal and Unseal change the writable view only; Entry's code runs
// before, while and after. The write that faults while sealed is
// std.runtime's test (tests_fault.mk), with a filter that sees it.
test "VM a sealed code region runs at Entry, and Unseal lets a patch through" {
    if NoCodeRegions() {
        return
    }
    c := CodeRegion.New(2 * PageSize()) else {
        Assert(false)
        return
    }
    var w := c.Writable()
    WriteRet(w, 0, 42)
    c.Flush(0, RetLen())
    e := c.Entry(0)
    Assert(CallAt(e) == 42)
    c.Seal() else {
        Assert(false)
    }
    Assert(CallAt(e) == 42)
    Assert(ImmOf(w[..], 0) == 42)
    c.Seal() else {
        Assert(false)
    }
    Assert(CallAt(e) == 42)
    c.Unseal() else {
        Assert(false)
    }
    WriteRet(w, 0, 7)
    WriteRet(w, PageSize(), 9)
    c.Flush(0, 2 * PageSize())
    Assert(CallAt(e) == 7)
    c.Seal() else {
        Assert(false)
    }
    Assert(CallAt(e) == 7)
    Assert(CallAt(c.Entry(PageSize())) == 9)
    c.Release()
}

test "VM a reserved code region runs what Commit took, and keeps it over a second Commit" {
    if NoCodeRegions() {
        return
    }
    ps := PageSize()
    c := CodeRegion.Reserve(8 * ps) else {
        Assert(false)
        return
    }
    Assert(c.Size() == 8 * ps)
    c.Commit(ps, ps) else {
        Assert(false)
    }
    c.Commit(4 * ps, 2 * ps) else {
        Assert(false)
    }
    var w := c.Writable()
    Assert(w[ps] == 0 && w[5 * ps + 8] == 0)
    WriteRet(w, ps, 42)
    WriteRet(w, 5 * ps, 5)
    // Only committed pages are flushed: aarch64's cache maintenance (dc
    // cvau) faults on a page of the executable view not committed.
    c.Flush(ps, RetLen())
    c.Flush(5 * ps, RetLen())
    Assert(CallAt(c.Entry(ps)) == 42)
    Assert(CallAt(c.Entry(5 * ps)) == 5)
    // Pages 1, 4 and 5 committed, the others not: Seal and Unseal go by
    // runs of committed pages (Windows refuses one call over a gap, 487).
    c.Seal() else {
        Assert(false)
    }
    Assert(CallAt(c.Entry(ps)) == 42)
    c.Unseal() else {
        Assert(false)
    }
    c.Commit(0, 8 * ps) else {
        Assert(false)
    }
    Assert(CallAt(c.Entry(ps)) == 42)
    Assert(CallAt(c.Entry(5 * ps)) == 5)
    WriteRet(w, 7 * ps, 3)
    c.Flush(7 * ps, RetLen())
    Assert(CallAt(c.Entry(7 * ps)) == 3)
    c.Release()
}

test "VM CodeRegion.Commit outside whole pages of the region is Misaligned" {
    if NoCodeRegions() {
        return
    }
    ps := PageSize()
    c := CodeRegion.Reserve(4 * ps) else {
        Assert(false)
        return
    }
    var bad := 0
    c.Commit(1, ps) else e {
        if IsMisaligned(e) {
            bad += 1
        }
    }
    c.Commit(0, 0) else e {
        if IsMisaligned(e) {
            bad += 1
        }
    }
    c.Commit(3 * ps, 2 * ps) else e {
        if IsMisaligned(e) {
            bad += 1
        }
    }
    c.Commit(-ps, ps) else e {
        if IsMisaligned(e) {
            bad += 1
        }
    }
    Assert(bad == 4)
    c.Release()
    n := CodeRegion.New(ps) else {
        Assert(false)
        return
    }
    n.Commit(0, ps) else {
        Assert(false)
    }
    n.Commit(0, 2 * ps) else e {
        Assert(IsMisaligned(e))
    }
    n.Release()
}

test "VM a write to a page a reserved code region has not committed faults [exit 101: panic: invalid memory access at]" (sys: &System) {
    if NoCodeRegions() {
        sys.Print("skip: macOS gives std no code regions\n", ()) else {
        }
        return
    }
    c := CodeRegion.Reserve(2 * PageSize()) else {
        return
    }
    c.Commit(0, PageSize()) else {}
    var w := c.Writable()
    w[PageSize() + 8] = 1
    c.Release()
}

// The executable view of a page not committed is inaccessible too: a read
// of it faults (a committed page would read 0, so this tells the two apart,
// where a call into a zero page faults in either case).
test "VM a read through the executable view of a page a reserved code region has not committed faults [exit 101: panic: invalid memory access at]" (sys: &System) {
    if NoCodeRegions() {
        sys.Print("skip: macOS gives std no code regions\n", ()) else {
        }
        return
    }
    c := CodeRegion.Reserve(2 * PageSize()) else {
        return
    }
    c.Commit(0, PageSize()) else {}
    unsafe "the page is reserved, not committed: the read is meant to fault" {
        Assert(raw.Read(raw.FromAddr[u8](c.Entry(PageSize()))) == 0)
    }
    c.Release()
}
