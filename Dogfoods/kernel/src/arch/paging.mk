// Why: the boot map is temporary; the kernel needs its own tables, with the
// APIC's page uncached.
//
// What: MapKernel, SetEntry and the entry flags.
//
// How: four levels (Intel SDM vol. 3, 4.5) with 2 MiB pages: the first GiB
// identity-mapped write-back, which holds the image and every page the
// allocator hands out, and the local APIC's page identity-mapped uncached.

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

/// The first address the kernel's map does not cover: everything below it
/// is identity-mapped.
export const MappedEnd: u64 = 0x40000000

// The loan on t ends with the call, so the writes through the result below
// happen under no live loan.
Entries(t: &PageTable): *u64 {
    unsafe "a PageTable is exactly its 512 entries" {
        return raw.PtrCast[PageTable, u64](raw.Of(t))
    }
}

PhysicalAddress(t: &PageTable): u64 {
    unsafe "the boot map is the identity, so a table's address is its physical address" {
        return u64(raw.Addr(raw.Of(t)))
    }
}

SetEntry(table: *u64, i: int, v: u64) {
    unsafe "the tables are written before CR3 names them, under no live loan, and i is below 512" {
        raw.Write(raw.Offset(table, i), v)
    }
}

/// Builds the kernel's tables and loads them into CR3, which also flushes
/// the TLB.
export MapKernel() {
    low := Entries(&PdLow)
    for i in 0..512 {
        SetEntry(low, i, (Cast[u64](i) << 21) | Present | Writable | Huge)
    }
    apic := Entries(&PdApic)
    SetEntry(apic, Cast[int]((ApicBase >> 21) & 511), ApicBase | Present | Writable | WriteThrough | NoCache | Huge)
    pdpt := Entries(&Pdpt)
    SetEntry(pdpt, 0, PhysicalAddress(&PdLow) | Present | Writable)
    SetEntry(pdpt, Cast[int](ApicBase >> 30), PhysicalAddress(&PdApic) | Present | Writable)
    SetEntry(Entries(&Pml4), 0, PhysicalAddress(&Pdpt) | Present | Writable)
    root := PhysicalAddress(&Pml4)
    unsafe "the new tables map the image, the stack, every allocator page and the APIC page" {
        asm("mov cr3, {r}", r: in(reg) root, options(nostack, preserves_flags))
    }
}
