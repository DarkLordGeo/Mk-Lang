// Why: std.coll's containers all move values in and out of raw buffers; one
// mem_ file holds that unsafe code so the container files hold none.
//
// What: slot moves and references (SlotRead, SlotWrite, SlotShift, SlotCopy,
// SlotRef, SlotMut), buffer management (AllocSlots, ResizeSlots, FreeSlots,
// GrowSlots, NextCap), Dangling, Drop and OwnerCheck.
//
// How: each helper states the obligation its caller meets. std.core's copy
// (core/mem_slots.mk) is not exported, so this module carries the same
// helpers. Growth is max(4, 2 cap, need), in place when the allocator can
// resize. Nothing here allocates except through the &Alloc it is given.

import std.core
import std.raw

// SlotRead moves the value out of slot i; the slot is then uninitialized.
SlotRead[T](p: *T, i: int): T {
    unsafe "the caller vouches slot i holds an initialized value it gives up" {
        return raw.Read(raw.Offset(p, i))
    }
}

// SlotWrite moves x into slot i, which held no value.
SlotWrite[T](p: *T, i: int, x: T) {
    unsafe "the caller vouches slot i is allocated and holds no value" {
        raw.Write(raw.Offset(p, i), x)
    }
}

// SlotShift moves n values from slot src to slot dst of p; the ranges may
// overlap.
SlotShift[T](p: *T, dst: int, src: int, n: int) {
    if n <= 0 {
        return
    }
    unsafe "the caller keeps both ranges within the allocation of p" {
        raw.Move(raw.Offset(p, dst), raw.Offset(p, src), n)
    }
}

// SlotCopy copies n values from slot si of src to slot di of dst; the
// ranges do not overlap.
SlotCopy[T](dst: *T, di: int, src: *T, si: int, n: int) {
    if n <= 0 {
        return
    }
    unsafe "the caller vouches both ranges are allocated and do not overlap" {
        raw.Copy(raw.Offset(dst, di), raw.Offset(src, si), n)
    }
}

// SlotRef and SlotMut are slot i as a reference.
SlotRef[T](p: *T, i: int): &T {
    unsafe "the caller vouches slot i holds an initialized value that outlives the reference" {
        return &*raw.Offset(p, i)
    }
}

SlotMut[T](p: *T, i: int): mut T {
    unsafe "the caller vouches slot i holds an initialized value that outlives the reference, with no other loan" {
        return mut *raw.Offset(p, i)
    }
}

// Dangling is the data pointer of an empty buffer: never null, aligned for
// T, and never dereferenced.
Dangling[T](): *T {
    unsafe "an address no allocation holds, used only for empty buffers" {
        return raw.FromAddr[T](Trunc[uint](AlignOf[T]()))
    }
}

// Drop gives up a value that is not linear: its caller checked
// raw.IsLinear, or check refuses a linear one at the caller's call site
// (E0701, the container's release bounds).
Drop[T](x: T) {
    unsafe "T is not linear, by the caller's raw.IsLinear check or its E0701 bound" {
        raw.Forget(x)
    }
}

// OwnerCheck panics with code 12 unless a is the allocator recorded at New
// New: one compare, on grow and free only.
OwnerCheck(a: &Alloc, owner: *Alloc, op: str) {
    if !raw.Same(raw.Of(a), owner) {
        raw.Fail(12, op)
    }
}

// MaxBytes is the largest byte count a request may name.
const MaxBytes: int = 0x7FFFFFFFFFFFFFFF

// AllocSlots allocates c slots of T from a; null when a is out of memory or
// c slots would pass MaxBytes. A zero-size T, or c = 0, needs no memory and
// gets the dangling pointer.
AllocSlots[T](a: &Alloc, c: int): *T {
    size := SizeOf[T]()
    if size == 0 || c == 0 {
        return Dangling[T]()
    }
    if c > MaxBytes / size {
        return raw.Null[T]()
    }
    p := core.AllocBytes(a, c * size, AlignOf[T]())
    if raw.IsNull(p) {
        return raw.Null[T]()
    }
    unsafe "a pointer cast; a allocated p for c slots of T" {
        return raw.PtrCast[u8, T](p)
    }
}

// ResizeSlots grows or shrinks the buffer of cap slots at p to c slots in
// place; false when the allocator cannot, and then nothing changed. A
// zero-size T always can.
ResizeSlots[T](a: &Alloc, p: *T, cap: int, c: int): bool {
    size := SizeOf[T]()
    if size == 0 {
        return true
    }
    if cap == 0 || c > MaxBytes / size {
        return false
    }
    var q := raw.Null[u8]()
    unsafe "a pointer cast; nothing is dereferenced" {
        q = raw.PtrCast[T, u8](p)
    }
    return core.ResizeBytes(a, q, cap * size, c * size, AlignOf[T]())
}

// FreeSlots returns a buffer of cap slots of T to a.
FreeSlots[T](a: &Alloc, p: *T, cap: int) {
    size := SizeOf[T]()
    if size == 0 || cap == 0 {
        return
    }
    var q := raw.Null[u8]()
    unsafe "a pointer cast; nothing is dereferenced" {
        q = raw.PtrCast[T, u8](p)
    }
    core.FreeBytes(a, q, cap * size, AlignOf[T]())
}

// NextCap is the growth rule: max(4, 2 cap, need). Doubling
// past MaxBytes gives need, so the allocator, not an overflow panic,
// reports the failure.
NextCap(cap: int, need: int): int {
    var c := need
    if cap <= MaxBytes / 2 {
        c = cap * 2
    }
    if c < 4 {
        c = 4
    }
    if c < need {
        c = need
    }
    return c
}

// GrowSlots makes room for c slots in a buffer of cap slots whose first n
// hold values: in place when a can resize, else a new buffer with the n
// values copied, the old one freed when free is set (heap forms) or left
// to the arena. Null when a is out of memory, and then the old
// buffer is untouched.
GrowSlots[T](a: &Alloc, p: *T, n: int, cap: int, c: int, free: bool): *T {
    if cap > 0 && ResizeSlots(a, p, cap, c) {
        return p
    }
    q := AllocSlots[T](a, c)
    if raw.IsNull(q) {
        return q
    }
    SlotCopy(q, 0, p, 0, n)
    if free {
        FreeSlots(a, p, cap)
    }
    return q
}
