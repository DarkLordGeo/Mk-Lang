// Page tables built in Mk: align(4096)
// tables that map the first GiB with 2 MiB pages and the local APIC page at
// 0xFEE00000 uncached, loaded with mov cr3; then the APIC version register
// read with mmio.Load.

import std.mmio
import std.raw

align(4096) struct PageTable {
    entries: [512]u64
}

var Pml4: PageTable = PageTable(entries: [0; 512])
var Pdpt: PageTable = PageTable(entries: [0; 512])
var PdLow: PageTable = PageTable(entries: [0; 512])
var PdApic: PageTable = PageTable(entries: [0; 512])

const Present: u64 = 0x1
const Writable: u64 = 0x2
const WriteThrough: u64 = 0x8
const NoCache: u64 = 0x10
const Huge: u64 = 0x80
const ApicBase: u64 = 0xFEE00000

// Entries is the address of t's first entry; the loan on t ends with the
// call, so the writes through it below are under no live loan.
Entries(t: &PageTable): *u64 {
    unsafe "a PageTable is exactly its 512 entries" {
        return raw.PtrCast[PageTable, u64](raw.Of(t))
    }
}

PhysOf(t: &PageTable): u64 {
    unsafe "the boot map is the identity, so a table's address is its physical address" {
        return u64(raw.Addr(raw.Of(t)))
    }
}

SetEntry(p: *u64, i: int, v: u64) {
    unsafe "the tables are written at boot, before CR3 names them, under no live loan" {
        raw.Write(raw.Offset(p, i), v)
    }
}

// MapApic builds the tables and loads them.
MapApic() {
    low := Entries(&PdLow)
    for i in 0..512 {
        SetEntry(low, i, (Cast[u64](i) << 21) | Present | Writable | Huge)
    }
    apic := Entries(&PdApic)
    SetEntry(apic, Trunc[int]((ApicBase >> 21) & 511), ApicBase | Present | Writable | WriteThrough | NoCache | Huge)
    pdpt := Entries(&Pdpt)
    SetEntry(pdpt, 0, PhysOf(&PdLow) | Present | Writable)
    SetEntry(pdpt, Trunc[int](ApicBase >> 30), PhysOf(&PdApic) | Present | Writable)
    SetEntry(Entries(&Pml4), 0, PhysOf(&Pdpt) | Present | Writable)
    root := PhysOf(&Pml4)
    unsafe "the new tables map the image, the stack and the APIC page; mov cr3 also flushes the TLB" {
        asm("mov cr3, {r}", r: in(reg) root, options(nostack, preserves_flags))
    }
}

// ApicVersion is the low byte of the local APIC's version register.
ApicVersion(): u32 {
    unsafe "MapApic mapped the local APIC page uncached at 0xFEE00000" {
        return mmio.Load(raw.FromAddr[u32](0xFEE00030)) & 0xFF
    }
}
