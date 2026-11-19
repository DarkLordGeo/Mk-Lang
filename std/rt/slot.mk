// Why: Mk has no thread-local storage, yet runtime code must find the task
// it runs in from anywhere, cheaply.
//
// What: SlotHeader (the 128 bytes below a slot's top), the region table, and
// the lookups SlotTop, SlotOf, CurrentSlot, SlotBase and RegionWordsOf.
//
// How: every task stack is a slot of one of the reserved regions, so the
// running task is found from the stack pointer: a compare and a shift per
// region give the slot's top, and the header sits below it.

import std.core
import std.raw

/// A task slot's header, the 128 bytes below the slot's top. Its task owns
/// it while it runs; the region's free stacks own it while it is free.
export struct SlotHeader {
    /// The index of the carrier running the task, or NoCarrier; picks the
    /// heap magazine.
    export carrier: u16
    /// ClassDefault or ClassLarge.
    export class: u8
    /// 1 free, 2 in use by a task.
    export state: u8
    /// 1 while the task runs an extern call on its carrier's system stack.
    export in_c: u8
    // unready is 1 on a cold Linux slot whose read-write step failed after
    // the trim (TrimOne): TakeSlot makes it read-write before handing it
    // out. Under the region lock while cold.
    unready: u8
    /// The pages committed at the slot's top (Windows), which a trim gives
    /// back down to one.
    export committed_pages: u16
    /// The task's id (NextTaskId); 1 is Main.
    export id: u64
    /// The rows of the error-return trace in use, at most 12 (trace.mk).
    export trace_n: u32
    /// The trace rows that did not fit.
    export trace_dropped: u32
    /// The trace's sites, oldest first.
    export trace: [12]*Site
    /// The next slot on the region's free or cold stack.
    export next_free: *SlotHeader
}

/// NoCarrier is SlotHeader.carrier while no carrier runs the task.
export const NoCarrier: u16 = 0xFFFF
/// HeaderBytes is the size of a SlotHeader at a slot's top.
export const HeaderBytes: int = 128

// The region bounds, written once by RegionsInit before any task exists;
// atomics so a fault handler on any thread reads them without a lock.
struct RegionWords {
    base: raw.Atomic[u64]
    size: raw.Atomic[u64]
    shift: raw.Atomic[u64]
    // next is the class of the next StackSize.Bytes region in the list
    // RegionTable.lazy heads, 0 at its end.
    next: raw.Atomic[u64]
}

struct RegionTable {
    r: [16]RegionWords
    // lazy is the class of the newest StackSize.Bytes region, the head of
    // their list (each region's next), 0 while none has one (EnsureRegion).
    lazy: raw.Atomic[u32]
    // top_fn and base_fn hold SlotTopLazy's and SlotBaseLazy's fn values
    // once lazy is set (EnsureRegion), 0 before: SlotTop and SlotBase call
    // them through these words, so no caller inlines the list walk (hello's
    // size budget: SlotTop is inlined into the fault and heap paths).
    top_fn: raw.Atomic[u64]
    base_fn: raw.Atomic[u64]
}

var Regions: RegionTable = RegionTable(r: [
    RegionWords(base: raw.Atomic[u64].New(0), size: raw.Atomic[u64].New(0), shift: raw.Atomic[u64].New(0), next: raw.Atomic[u64].New(0)),
    RegionWords(base: raw.Atomic[u64].New(0), size: raw.Atomic[u64].New(0), shift: raw.Atomic[u64].New(0), next: raw.Atomic[u64].New(0)),
    RegionWords(base: raw.Atomic[u64].New(0), size: raw.Atomic[u64].New(0), shift: raw.Atomic[u64].New(0), next: raw.Atomic[u64].New(0)),
    RegionWords(base: raw.Atomic[u64].New(0), size: raw.Atomic[u64].New(0), shift: raw.Atomic[u64].New(0), next: raw.Atomic[u64].New(0)),
    RegionWords(base: raw.Atomic[u64].New(0), size: raw.Atomic[u64].New(0), shift: raw.Atomic[u64].New(0), next: raw.Atomic[u64].New(0)),
    RegionWords(base: raw.Atomic[u64].New(0), size: raw.Atomic[u64].New(0), shift: raw.Atomic[u64].New(0), next: raw.Atomic[u64].New(0)),
    RegionWords(base: raw.Atomic[u64].New(0), size: raw.Atomic[u64].New(0), shift: raw.Atomic[u64].New(0), next: raw.Atomic[u64].New(0)),
    RegionWords(base: raw.Atomic[u64].New(0), size: raw.Atomic[u64].New(0), shift: raw.Atomic[u64].New(0), next: raw.Atomic[u64].New(0)),
    RegionWords(base: raw.Atomic[u64].New(0), size: raw.Atomic[u64].New(0), shift: raw.Atomic[u64].New(0), next: raw.Atomic[u64].New(0)),
    RegionWords(base: raw.Atomic[u64].New(0), size: raw.Atomic[u64].New(0), shift: raw.Atomic[u64].New(0), next: raw.Atomic[u64].New(0)),
    RegionWords(base: raw.Atomic[u64].New(0), size: raw.Atomic[u64].New(0), shift: raw.Atomic[u64].New(0), next: raw.Atomic[u64].New(0)),
    RegionWords(base: raw.Atomic[u64].New(0), size: raw.Atomic[u64].New(0), shift: raw.Atomic[u64].New(0), next: raw.Atomic[u64].New(0)),
    RegionWords(base: raw.Atomic[u64].New(0), size: raw.Atomic[u64].New(0), shift: raw.Atomic[u64].New(0), next: raw.Atomic[u64].New(0)),
    RegionWords(base: raw.Atomic[u64].New(0), size: raw.Atomic[u64].New(0), shift: raw.Atomic[u64].New(0), next: raw.Atomic[u64].New(0)),
    RegionWords(base: raw.Atomic[u64].New(0), size: raw.Atomic[u64].New(0), shift: raw.Atomic[u64].New(0), next: raw.Atomic[u64].New(0)),
    RegionWords(base: raw.Atomic[u64].New(0), size: raw.Atomic[u64].New(0), shift: raw.Atomic[u64].New(0), next: raw.Atomic[u64].New(0))], lazy: raw.Atomic[u32].New(0), top_fn: raw.Atomic[u64].New(0),
    base_fn: raw.Atomic[u64].New(0))

/// SlotTop is the top of the slot holding a, or 0 when a is in no slot. Two
/// unsigned compares and a shift per region.
/// Allocates nothing.
export SlotTop(a: uint): uint {
    for i in 0..2 {
        // Edge: RegionReserve stores shift and size before base; base 0 means no
        // region, so a nonzero base comes with its size and shift.
        base := Trunc[uint](Regions.r[i].base.Load())
        size := Trunc[uint](Regions.r[i].size.Load())
        off := a -% base
        if base != 0 && off < size {
            shift := Trunc[uint](Regions.r[i].shift.Load())
            return base + (((off >> shift) + 1) << shift)
        }
    }
    // Edge: EnsureRegion stores the word after the list it walks.
    var w := Regions.top_fn.Load()
    if w != 0 {
        unsafe "the word holds SlotTopLazy's fn value (EnsureRegion)" {
            f := raw.Read(raw.PtrCast[u64, fn(uint): uint](raw.OfMut(mut w)))
            return f(a)
        }
    }
    return 0
}

// SlotTopLazy is SlotTop over StackSize.Bytes's classes, by their list
// (EnsureRegion pushes each class it reserves): a chain, not a loop over
// every class, so no compiler unrolls it into every program's code
// (hello's size budget).
SlotTopLazy(a: uint): uint {
    // Edge: EnsureRegion stores a class's words and next before the head
    // that names it, so a class read from the list comes with its words.
    var i := Trunc[int](Regions.lazy.Load())
    for i != 0 {
        base := Trunc[uint](Regions.r[i].base.Load())
        size := Trunc[uint](Regions.r[i].size.Load())
        off := a -% base
        if off < size {
            shift := Trunc[uint](Regions.r[i].shift.Load())
            return base + (((off >> shift) + 1) << shift)
        }
        // Edge: as the loads above; EnsureRegion stores next before the head.
        i = Trunc[int](Regions.r[i].next.Load())
    }
    return 0
}

/// RegionWordsOf is region i's (0 or 1) base, size and slot shift, as SlotTop
/// reads them (base 0: no region), for std.core's chain check
/// (core.ChainInit). O(1); allocates nothing.
export RegionWordsOf(i: int): core.ChainRegion {
    // Edge: as SlotTop's loads; called after RegionsInit, before any task.
    return core.ChainRegion(base: Regions.r[i].base.Load(), size: Regions.r[i].size.Load(),
        shift: Regions.r[i].shift.Load())
}

/// SlotOf is the header of the slot holding p, or null.
/// O(1), as SlotTop; allocates nothing.
export SlotOf(p: *u8): *SlotHeader {
    top := SlotTop(raw.Addr(p))
    unsafe "a header address inside a reserved region, or null" {
        if top == 0 {
            return raw.Null[SlotHeader]()
        }
        return raw.FromAddr[SlotHeader](top - Trunc[uint](HeaderBytes))
    }
}

/// CurrentSlot is the running task's header, or null off a task stack (a
/// carrier's system stack, the main thread before Start, a foreign thread).
/// O(1): SlotTop of the stack pointer; allocates nothing.
export CurrentSlot(): *SlotHeader {
    sp := StackPointer()
    top := SlotTop(sp)
    unsafe "a header address inside a reserved region, or null" {
        if top == 0 {
            return raw.Null[SlotHeader]()
        }
        return raw.FromAddr[SlotHeader](top - Trunc[uint](HeaderBytes))
    }
}

/// SlotBase is the lowest address of the slot whose top is top.
/// O(1); allocates nothing.
export SlotBase(top: uint): uint {
    for i in 0..2 {
        // Edge: as SlotTop's loads.
        base := Trunc[uint](Regions.r[i].base.Load())
        size := Trunc[uint](Regions.r[i].size.Load())
        if base != 0 && top -% base <= size && top > base {
            return top - (Trunc[uint](1) << Trunc[uint](Regions.r[i].shift.Load()))
        }
    }
    // Edge: as SlotTop's word.
    var w := Regions.base_fn.Load()
    if w != 0 {
        unsafe "the word holds SlotBaseLazy's fn value (EnsureRegion)" {
            f := raw.Read(raw.PtrCast[u64, fn(uint): uint](raw.OfMut(mut w)))
            return f(top)
        }
    }
    return 0
}

// SlotBaseLazy is SlotBase over StackSize.Bytes's classes, by their list.
SlotBaseLazy(top: uint): uint {
    // Edge: as SlotTopLazy's.
    var i := Trunc[int](Regions.lazy.Load())
    for i != 0 {
        base := Trunc[uint](Regions.r[i].base.Load())
        size := Trunc[uint](Regions.r[i].size.Load())
        if top -% base <= size && top > base {
            return top - (Trunc[uint](1) << Trunc[uint](Regions.r[i].shift.Load()))
        }
        i = Trunc[int](Regions.r[i].next.Load())
    }
    return 0
}

/// LazyLookups stores SlotTopLazy's and SlotBaseLazy's fn values in the
/// words SlotTop and SlotBase call them through; EnsureRegion calls it,
/// under its lock, after the first Bytes region is on the list.
/// O(1); allocates nothing.
export LazyLookups() {
    var t: fn(uint): uint = SlotTopLazy
    var b: fn(uint): uint = SlotBaseLazy
    unsafe "t and b are fn values; the words hold their bytes" {
        // Edge: after the list's head (EnsureRegion), so a call through a
        // word walks a list whose words are stored.
        Regions.base_fn.Store(raw.Read(raw.PtrCast[fn(uint): uint, u64](raw.Of(&b))))
        Regions.top_fn.Store(raw.Read(raw.PtrCast[fn(uint): uint, u64](raw.Of(&t))))
    }
}
