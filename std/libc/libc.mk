// Why: a package's c_sources need the C library functions they call, and Mk
// links against no C runtime; std.libc supplies them, written in Mk.
//
// What: the C functions (strlen, memchr, strchr, strcmp, strncmp and the
// rest), exported with export extern under their C names. It has no Mk
// names a program calls: the functions are for C. memcpy, memmove, memset
// and memcmp are not here because codegen's support pack defines them in
// every executable. The allocator (malloc, calloc, realloc, free,
// malloc_usable_size) and the raw reads this file uses are in mem_alloc.mk.
//
// How: each function's C name is its symbol. The driver generates the C
// headers a C source includes (stdlib.h, string.h and the rest) from these
// signatures, so no C text is in the repository. The searches sqlite leans
// on (strlen, memchr, strchr) are vectorised with std.simd, see below.
// strcmp and strncmp stay byte loops, as musl's.

import std.raw
import std.simd

// The searches (strlen, memchr, strchr and strcspn's one-byte case) read
// 16 bytes at a time with std.simd, as glibc's SSE2 versions do: compare
// the block with the byte, turn the comparison into a 16-bit mask, and
// take the index of its lowest set bit. Blocks are read only at 16-aligned
// addresses, the first one being the block that holds the first byte with
// the bits of the bytes before it shifted out, so a read never crosses a
// page (VecAt). Past the first 64-aligned address four blocks are read
// per step, a 64-byte line at a time, glibc's unrolling.

// Bits16 is the mask of the bytes of the block at the 16-aligned address p
// equal to v's.
Bits16(p: uint, v: simd.Vec[u8, 16]): u64 {
    return (VecAt(p) == v).Bits()
}

// Bits64 is the mask of the bytes of the 64-byte line at the 64-aligned
// address p equal to v's, byte i at bit i.
Bits64(p: uint, v: simd.Vec[u8, 16]): u64 {
    return Bits16(p, v) | (Bits16(p + 16, v) << 16) | (Bits16(p + 32, v) << 32) | (Bits16(p + 48, v) << 48)
}

// It reads only blocks whose first byte is among those n and before a
// match, and a block never crosses a page, so a call whose n runs past the
// object but whose object holds the byte is safe, as C17 7.24.5.1 requires.
/// C's memchr: the first byte equal to the low byte of c among the n bytes
/// at s, or null. An n that runs past the object is safe when the object
/// holds the byte. O(n); allocates nothing.
export extern memchr(s: *u8, c: i32, n: uint): *u8 {
    if n == 0 {
        return raw.Null[u8]()
    }
    k := simd.Vec[u8, 16].Splat(Trunc[u8](c))
    a := raw.Addr(s)
    head := a & 15
    var p := a - head
    // The first block: the bytes before s shifted out, and only the first
    // n of the rest counted.
    var m := Bits16(p, k) >> Trunc[int](head)
    var left := n
    if left < 16 - head {
        m = m & ((u64(1) << Trunc[int](left)) - 1)
        if m == 0 {
            return raw.Null[u8]()
        }
    }
    if m != 0 {
        return PtrAt(a + Trunc[uint](raw.Ctz64(m)))
    }
    left -= 16 - head
    p += 16
    for left >= 16 && (p & 63) != 0 {
        m = Bits16(p, k)
        if m != 0 {
            return PtrAt(p + Trunc[uint](raw.Ctz64(m)))
        }
        p += 16
        left -= 16
    }
    for left >= 64 {
        m = Bits64(p, k)
        if m != 0 {
            return PtrAt(p + Trunc[uint](raw.Ctz64(m)))
        }
        p += 64
        left -= 64
    }
    for left >= 16 {
        m = Bits16(p, k)
        if m != 0 {
            return PtrAt(p + Trunc[uint](raw.Ctz64(m)))
        }
        p += 16
        left -= 16
    }
    if left > 0 {
        m = Bits16(p, k) & ((u64(1) << Trunc[int](left)) - 1)
        if m != 0 {
            return PtrAt(p + Trunc[uint](raw.Ctz64(m)))
        }
    }
    return raw.Null[u8]()
}

// ChrNul is musl's __strchrnul: the address of the first byte of the
// NUL-terminated s equal to b, or of its NUL; with b = 0 it is strlen's
// end. A block's mask is the union of the NUL and the b masks, so its
// lowest bit is the first byte that is either.
ChrNul(s: *u8, b: u8): uint {
    z := simd.Vec[u8, 16].Zero()
    k := simd.Vec[u8, 16].Splat(b)
    a := raw.Addr(s)
    head := a & 15
    var p := a - head
    m := (Bits16(p, z) | Bits16(p, k)) >> Trunc[int](head)
    if m != 0 {
        return a + Trunc[uint](raw.Ctz64(m))
    }
    p += 16
    for (p & 63) != 0 {
        q := Bits16(p, z) | Bits16(p, k)
        if q != 0 {
            return p + Trunc[uint](raw.Ctz64(q))
        }
        p += 16
    }
    for {
        q := Bits64(p, z) | Bits64(p, k)
        if q != 0 {
            return p + Trunc[uint](raw.Ctz64(q))
        }
        p += 64
    }
}

// Past the first 64-aligned address it tests a whole line with one
// comparison, glibc's: the lane-wise minimum of the four blocks has a zero
// byte where any of them has one.
/// C's strlen: the number of bytes before the first NUL. O(n); allocates
/// nothing.
export extern strlen(s: *u8): uint {
    z := simd.Vec[u8, 16].Zero()
    a := raw.Addr(s)
    head := a & 15
    var p := a - head
    m := Bits16(p, z) >> Trunc[int](head)
    if m != 0 {
        return Trunc[uint](raw.Ctz64(m))
    }
    p += 16
    for (p & 63) != 0 {
        q := Bits16(p, z)
        if q != 0 {
            return p - a + Trunc[uint](raw.Ctz64(q))
        }
        p += 16
    }
    for {
        v := VecAt(p).Min(VecAt(p + 16)).Min(VecAt(p + 32).Min(VecAt(p + 48)))
        if (v == z).Any() {
            return p - a + Trunc[uint](raw.Ctz64(Bits64(p, z)))
        }
        p += 64
    }
}

/// C's strchr: the first byte equal to the low byte of c, the NUL itself
/// when c is 0, or null. O(n); allocates nothing.
export extern strchr(s: *u8, c: i32): *u8 {
    b := Trunc[u8](c)
    q := ChrNul(s, b)
    if At(q) == b {
        return PtrAt(q)
    }
    return raw.Null[u8]()
}

// musl's: the length first (word at a time), then back from the NUL.
/// C's strrchr: the last byte equal to the low byte of c, the NUL itself
/// when c is 0, or null. O(n); allocates nothing.
export extern strrchr(s: *u8, c: i32): *u8 {
    b := Trunc[u8](c)
    a := raw.Addr(s)
    var p := a + strlen(s)
    for {
        if At(p) == b {
            return PtrAt(p)
        }
        if p == a {
            return raw.Null[u8]()
        }
        p -= 1
    }
}

// A byte loop, as musl's: the first difference is usually within a few
// bytes, where a word loop's alignment prologue costs more than it saves.
/// C's strcmp: compares two NUL-terminated strings byte by byte, as
/// unsigned char, as C requires. O(n); allocates nothing.
export extern strcmp(a: *u8, b: *u8): i32 {
    var x := raw.Addr(a)
    var y := raw.Addr(b)
    for {
        u := At(x)
        v := At(y)
        if u != v {
            return i32(u) - i32(v)
        }
        if u == 0 {
            return 0
        }
        x += 1
        y += 1
    }
}

/// C's strncmp: compares at most n bytes, stopping at a NUL. O(n);
/// allocates nothing.
export extern strncmp(a: *u8, b: *u8, n: uint): i32 {
    x := raw.Addr(a)
    y := raw.Addr(b)
    var i: uint = 0
    for i < n {
        u := At(x + i)
        v := At(y + i)
        if u != v {
            return i32(u) - i32(v)
        }
        if u == 0 {
            return 0
        }
        i += 1
    }
    return 0
}

// ByteSet is a 256-bit set of bytes, musl's strspn table.
struct ByteSet {
    w: [4]u64
}

// SetOf builds the set of the bytes of the NUL-terminated set at a.
SetOf(a: uint): ByteSet {
    var s := ByteSet(w: [0; 4])
    var p := a
    for {
        x := At(p)
        if x == 0 {
            return s
        }
        s.w[int(x >> 6)] = s.w[int(x >> 6)] | (u64(1) << int(x & 63))
        p += 1
    }
}

// Has reports whether x is in s.
ByteSet.Has(&self, x: u8): bool {
    return (self.w[int(x >> 6)] >> int(x & 63)) & 1 != 0
}

/// C's strspn: the length of the prefix of s made of bytes in set.
/// O(len(s) + len(set)); allocates nothing.
export extern strspn(s: *u8, set: *u8): uint {
    a := raw.Addr(s)
    t := SetOf(raw.Addr(set))
    var p := a
    for {
        x := At(p)
        if x == 0 || !t.Has(x) {
            return p - a
        }
        p += 1
    }
}

// A set of at most one byte is ChrNul, word at a time, as musl's.
/// C's strcspn: the length of the prefix of s with no byte of set.
/// O(len(s) + len(set)); allocates nothing.
export extern strcspn(s: *u8, set: *u8): uint {
    a := raw.Addr(s)
    first := At(raw.Addr(set))
    if first == 0 || At(raw.Addr(set) + 1) == 0 {
        return ChrNul(s, first) - a
    }
    t := SetOf(raw.Addr(set))
    var p := a
    for {
        x := At(p)
        if x == 0 || t.Has(x) {
            return p - a
        }
        p += 1
    }
}

/// C's fabs: x with its sign bit cleared, so -0 and -NaN lose their sign
/// too. O(1); allocates nothing.
export extern fabs(x: f64): f64 {
    return f64.FromBits(x.Bits() & 0x7FFF_FFFF_FFFF_FFFF)
}
