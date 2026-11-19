// Why: List, HeapList, Text, HeapText, Box and the Swiss table keep their
// elements behind a raw pointer; putting every raw slot operation here keeps
// the containers' own bodies free of unsafe blocks.
//
// What: the slot helpers SlotAt, SlotRead, SlotWrite, SlotShift, SlotCopy,
// SlotSwap, SlotView, SlotRef and their kin, growth (GrowSlots, FreeSlots,
// NextCap), and the byte forms (ByteAt, ByteView, CopyBytes, ShiftBytes,
// GrowBytes) that run at compile time.
//
// How: each helper is one obligation the caller states in a comment at the
// call (the slot is allocated, initialized, in range). Growth is max(4,
// 2·cap, need): in place when the allocator can resize, else a new buffer;
// the old one is freed for heap containers or left to the arena.

import std.raw

// SlotAt is the i-th slot of the array at p.
SlotAt[T](p: *T, i: int): *T {
    unsafe "the caller keeps i within the allocation of p" {
        return raw.Offset(p, i)
    }
}

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

// SlotShift moves n values from slot src to slot dst; the ranges may
// overlap.
SlotShift[T](p: *T, dst: int, src: int, n: int) {
    if n <= 0 {
        return
    }
    unsafe "the caller keeps both ranges within the allocation of p" {
        raw.Move(raw.Offset(p, dst), raw.Offset(p, src), n)
    }
}

// SlotCopy copies n values from src to dst, which do not overlap.
SlotCopy[T](dst: *T, src: *T, n: int) {
    if n <= 0 {
        return
    }
    unsafe "the caller vouches dst and src hold n slots each and do not overlap" {
        raw.Copy(dst, src, n)
    }
}

// SlotSwap exchanges slots i and j bitwise.
SlotSwap[T](p: *T, i: int, j: int) {
    unsafe "the caller vouches slots i and j hold initialized values" {
        a := raw.Offset(p, i)
        b := raw.Offset(p, j)
        x := raw.Read(a)
        raw.Write(a, raw.Read(b))
        raw.Write(b, x)
    }
}

// SlotView and SlotViewMut are the first n slots as a slice.
SlotView[T](p: *T, n: int): []T {
    unsafe "the caller vouches p holds n initialized values that outlive the view" {
        return raw.Slice(p, n)
    }
}

SlotViewMut[T](p: *T, n: int): mut []T {
    unsafe "the caller vouches p holds n initialized values that outlive the view" {
        return raw.SliceMut(p, n)
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

// BytesAs and AsBytes cast between a typed slot array and its bytes.
BytesAs[T](p: *u8): *T {
    unsafe "a pointer cast; the caller allocated p for T" {
        return raw.PtrCast[u8, T](p)
    }
}

AsBytes[T](p: *T): *u8 {
    unsafe "a pointer cast; nothing is dereferenced" {
        return raw.PtrCast[T, u8](p)
    }
}

// ZeroBytes writes n zero bytes at p.
ZeroBytes(p: *u8, n: int) {
    if n <= 0 {
        return
    }
    unsafe "the caller vouches p holds n bytes" {
        raw.Fill(p, 0, n)
    }
}

// GrowSlots makes room for c slots of T in a buffer of cap slots holding n
// values, from a: in place when the allocator can resize, else a new
// buffer with the n values copied and the old one freed when free is set
// (heap containers) or left to the arena. Null when a is out
// of memory, and then the old buffer is untouched. A zero-size T needs no
// memory: its buffer stays the dangling pointer. A byte count that would
// overflow int is out of memory, not an overflow panic.
GrowSlots[T](a: &Alloc, p: *T, n: int, cap: int, c: int, free: bool): *T {
    size := SizeOfT[T]()
    if size == 0 {
        return p
    }
    if c > GrowLimit / size {
        return raw.Null[T]()
    }
    align := AlignOfT[T]()
    bytes := c * size
    if cap > 0 && ResizeBytes(a, AsBytes(p), cap * size, bytes, align) {
        return p
    }
    q := AllocBytes(a, bytes, align)
    if raw.IsNull(q) {
        return raw.Null[T]()
    }
    t := BytesAs[T](q)
    SlotCopy(t, p, n)
    if free && cap > 0 {
        FreeBytes(a, AsBytes(p), cap * size, align)
    }
    return t
}

// FreeSlots returns a buffer of cap slots of T to a.
FreeSlots[T](a: &Alloc, p: *T, cap: int) {
    size := SizeOfT[T]()
    if size == 0 || cap == 0 {
        return
    }
    FreeBytes(a, AsBytes(p), cap * size, AlignOfT[T]())
}

// GrowLimit is the largest byte count a growth request can name (the
// largest int; mem_arena.mk has its own MaxBytes for the arena).
const GrowLimit: int = 0x7FFFFFFFFFFFFFFF

// NextCap is the containers' growth: max(4, 2·cap, need). Doubling past GrowLimit
// gives need, so the allocator, not an overflow panic, reports the failure.
NextCap(cap: int, need: int): int {
    var c := if cap <= GrowLimit / 2 { cap * 2 } else { need }
    if c < 4 {
        c = 4
    }
    if c < need {
        c = need
    }
    return c
}

// StrOf is the n bytes at p as a str; the caller vouches they are valid
// UTF-8 and outlive the view.
StrOf(p: *u8, n: int): str {
    unsafe "the caller vouches p holds n bytes of valid UTF-8 that outlive the view" {
        return raw.Str(p, n)
    }
}

// TextCtx is a Text's address as a text writer's ctx (Text.Writer).
TextCtx(t: mut Text): *u8 {
    unsafe "a pointer cast; nothing is dereferenced" {
        return raw.PtrCast[Text, u8](raw.OfMut(t))
    }
}

// The byte forms of the slot helpers, not generic, so Text, HeapText and
// Reader.Line run at compile time before mono gives generic bodies
// instances; each states the same obligation as its
// generic twin.
ByteAt(p: *u8, i: int): *u8 {
    unsafe "the caller keeps i within the allocation of p" {
        return raw.Offset(p, i)
    }
}

ByteView(p: *u8, n: int): []u8 {
    unsafe "the caller vouches p holds n bytes that outlive the view" {
        return raw.Slice(p, n)
    }
}

ByteViewMut(p: *u8, n: int): mut []u8 {
    unsafe "the caller vouches p holds n bytes that outlive the view, with no other loan" {
        return raw.SliceMut(p, n)
    }
}

CopyBytes(dst: *u8, src: *u8, n: int) {
    if n <= 0 {
        return
    }
    unsafe "the caller vouches dst and src hold n bytes each and do not overlap" {
        raw.Copy(dst, src, n)
    }
}

ShiftBytes(p: *u8, dst: int, src: int, n: int) {
    if n <= 0 {
        return
    }
    unsafe "the caller keeps both ranges within the allocation of p" {
        raw.Move(raw.Offset(p, dst), raw.Offset(p, src), n)
    }
}

// GrowBytes is GrowSlots for bytes.
GrowBytes(a: &Alloc, p: *u8, n: int, cap: int, c: int, free: bool): *u8 {
    if cap > 0 && ResizeBytes(a, p, cap, c, 1) {
        return p
    }
    q := AllocBytes(a, c, 1)
    if raw.IsNull(q) {
        return q
    }
    CopyBytes(q, p, n)
    if free && cap > 0 {
        FreeBytes(a, p, cap, 1)
    }
    return q
}
