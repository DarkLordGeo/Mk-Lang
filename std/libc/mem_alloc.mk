// Why: C code needs malloc and free, and the string functions of libc.mk
// need raw reads of C memory; both are unsafe and live in this mem_ file.
//
// What: the C allocator (malloc, calloc, realloc, free, malloc_usable_size)
// over the process Heap, and the three raw accesses libc.mk is written with
// (At, VecAt, PtrAt), so that file reads C's memory through them alone.
//
// How: allocation is over rt.HeapAlloc, HeapResize and HeapFree. Each block
// starts with a 16-byte header holding the size asked for, so free finds it
// and every pointer is 16-aligned (C's max_align_t). Every failure is a
// null pointer: nothing here panics, since a panic under a C frame aborts.

import std.raw
import std.rt
import std.simd

// Head is the header size and the alignment of every block.
const Head: int = 16

// MaxSize is the largest request served: a size above it, or a header
// added to it, could not be an int.
const MaxSize: uint = 0x7FFF_FFFF_FFFF_0000

// At is the byte at address a.
At(a: uint): u8 {
    unsafe "a is inside a C object its caller was handed (C's contract)" {
        return raw.Read(raw.FromAddr[u8](a))
    }
}

// VecAt is the 16 bytes at the 16-aligned address a, as std.simd's vector.
// The block may run past the object (strlen's last block) or start before
// it (the block that holds a string's first byte), never past its page: an
// aligned 16-byte block lies in one page, and the caller reads it only
// when one of its bytes is inside the object, so the read cannot fault.
VecAt(a: uint): simd.Vec[u8, 16] {
    unsafe "a is 16-aligned and its first byte is inside a C object" {
        return simd.Vec[u8, 16].FromSlice(raw.Slice(raw.FromAddr[u8](a), 16), 0)
    }
}

// PtrAt is a as C's pointer, for a result inside the caller's object.
PtrAt(a: uint): *u8 {
    unsafe "a is inside the object the caller was handed" {
        return raw.FromAddr[u8](a)
    }
}

// Block allocates n bytes plus the header and records n; null on failure.
Block(n: int): *u8 {
    p := rt.HeapAlloc(n + Head, Head)
    unsafe "p is null or a fresh block of n + Head bytes, 16-aligned" {
        if raw.IsNull(p) {
            return p
        }
        raw.Write(raw.PtrCast[u8, int](p), n)
        return raw.Offset(p, Head)
    }
}

// SizeOfBlock reads the size a live block was asked for.
SizeOfBlock(p: *u8): int {
    unsafe "p came from Block, so its header is the 16 bytes before it" {
        return raw.Read(raw.PtrCast[u8, int](raw.Offset(p, -Head)))
    }
}

/// C's malloc: a 16-aligned block of n bytes, or null when the heap has no
/// room. malloc(0) gives a unique block. It never panics; it allocates
/// n + 16 bytes from the process Heap.
export extern malloc(n: uint): *u8 {
    if n > MaxSize {
        return raw.Null[u8]()
    }
    return Block(Trunc[int](n))
}

/// C's calloc: count * size zeroed bytes, or null when the product
/// overflows or the heap has no room. It zeroes the count * size bytes and
/// allocates that many plus 16 from the process Heap.
export extern calloc(count: uint, size: uint): *u8 {
    if size != 0 && count > MaxSize / size {
        return raw.Null[u8]()
    }
    n := Trunc[int](count * size)
    p := Block(n)
    unsafe "p is null or holds n bytes" {
        if !raw.IsNull(p) {
            raw.Fill(p, 0, n)
        }
    }
    return p
}

/// C's free: gives the block back to the process Heap. A null p does
/// nothing.
export extern free(p: *u8) {
    unsafe "p is null or came from Block and is live" {
        if raw.IsNull(p) {
            return
        }
        n := SizeOfBlock(p)
        rt.HeapFree(raw.Offset(p, -Head), n + Head, Head)
    }
}

// sqlite built with -DSQLITE_MALLOCSIZE=malloc_usable_size asks the block
// its size through it instead of keeping an 8-byte size prefix of its own
// in every allocation (sqlite's mem1.c).
/// glibc's malloc_usable_size: the bytes the block at p may hold, 0 for
/// null. Here it is the size asked for. O(1); allocates nothing.
export extern malloc_usable_size(p: *u8): uint {
    if raw.IsNull(p) {
        return 0
    }
    return Trunc[uint](SizeOfBlock(p))
}

// realloc(p, 0) frees nothing and gives a block of 0 bytes, so the caller's
// free of the result stays right (C17 leaves it to the implementation;
// sqlite never asks for 0).
/// C's realloc: with a null p it is malloc. The block grows or shrinks in
/// place when the heap allows it; else the bytes move to a new block and
/// the old one is freed. On failure the old block is untouched and null is
/// returned. It copies the bytes kept when they move, and then allocates
/// n + 16 bytes.
export extern realloc(p: *u8, n: uint): *u8 {
    unsafe "p is null or came from Block and is live" {
        if raw.IsNull(p) {
            return malloc(n)
        }
        if n > MaxSize {
            return raw.Null[u8]()
        }
        want := Trunc[int](n)
        old := SizeOfBlock(p)
        base := raw.Offset(p, -Head)
        if rt.HeapResize(base, old + Head, want + Head, Head) {
            raw.Write(raw.PtrCast[u8, int](base), want)
            return p
        }
        q := Block(want)
        if raw.IsNull(q) {
            return q
        }
        keep := if want < old { want } else { old }
        raw.Copy(q, p, keep)
        rt.HeapFree(base, old + Head, Head)
        return q
    }
}
