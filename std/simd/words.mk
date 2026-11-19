// Why: a freestanding bin may hold no vector value (E1108), and nothing tells
// a function which bins reach it, so std.simd's byte routines need a form in
// general registers alone.
//
// What: the SWAR word forms of the byte routines that every public byte
// routine takes on x86_64-freestanding, and the private alias Os.
//
// How: on freestanding (built `general-regs-only` by default) the target's
// Target.os, a compile-time constant, picks the word form whatever the bin's
// cpu, so no std.simd function there runs an SSE or AVX instruction or a
// `cpu` function (which would not save the FPU state), and the vector path
// is not instantiated there and draws no E1108. The forms take eight bytes
// per u64, as musl's and glibc's portable string routines (and std.core's
// IndexByteIn): x = w ^ (b * 0x01..01) has a zero byte exactly where w
// holds b. A word is built from eight byte reads of one checked eight-byte
// slice, which LLVM combines into one load, and which the evaluator runs too
// (a raw pointer read would be refused at compile time, E0901). Each is O(n)
// with no allocation, and gives the bits of the vector forms
// (TestSTDSIMD_WordForms runs the differential programs over them).
//
// std.core imports std.simd, so std.simd sits below the prelude and sees none
// of its names. TargetInfo, Arch and Os are declared in std.target, the
// lowest std module, which every std module may import; the private alias Os
// below keeps each fork's text `Target.os == Os.Freestanding`, the universe
// constant against std.target's enum.

import std.raw
import std.target

// Os is std.target's Os, the prelude's own (std.core aliases the same enum).
type Os = target.Os

const Ones8: u64 = 0x0101010101010101
const Highs8: u64 = 0x8080808080808080
const Lows7: u64 = 0x7F7F7F7F7F7F7F7F

// Word is the eight bytes of s at i as a little-endian u64: byte i + k is
// bits 8k to 8k + 7, so the lowest marked byte of a word is its first.
Word(s: []u8, i: int): u64 {
    w := s[i..i + 8]
    return Cast[u64](w[0]) | (Cast[u64](w[1]) << 8) | (Cast[u64](w[2]) << 16) | (Cast[u64](w[3]) << 24) |
        (Cast[u64](w[4]) << 32) | (Cast[u64](w[5]) << 40) | (Cast[u64](w[6]) << 48) | (Cast[u64](w[7]) << 56)
}

// FirstZero marks with bit 7 the zero bytes of x; the lowest mark is exact
// (a borrow starts only at a zero byte and moves up, so a false mark can
// only sit above a true one), the ones above it are not.
FirstZero(x: u64): u64 {
    return (x -% Ones8) & ~x & Highs8
}

// Zeros marks with bit 7 exactly the zero bytes of x: adding 0x7F to the
// low seven bits carries into bit 7 for every nonzero low part, and x's own
// bit 7 covers the rest; no carry crosses a byte.
Zeros(x: u64): u64 {
    return ~(((x & Lows7) +% Lows7) | x | Lows7)
}

// IndexByteWords is IndexByte in words: two words per step, one test.
IndexByteWords(s: []u8, b: u8): ?int {
    n := s.Len()
    pat := Cast[u64](b) *% Ones8
    var i := 0
    for i + 16 <= n {
        m0 := FirstZero(Word(s, i) ^ pat)
        m1 := FirstZero(Word(s, i + 8) ^ pat)
        if (m0 | m1) != 0 {
            if m0 != 0 {
                return some(i + raw.Ctz64(m0) / 8)
            }
            return some(i + 8 + raw.Ctz64(m1) / 8)
        }
        i += 16
    }
    for i < n {
        if s[i] == b {
            return some(i)
        }
        i += 1
    }
    return none
}

// IndexAnyWords is IndexAny in words: none for an empty set, IndexByteWords
// for one byte, else a 256-bit membership table (musl's strcspn shape),
// built in O(set.Len()), one lookup per byte.
IndexAnyWords(s: []u8, set: []u8): ?int {
    k := set.Len()
    if k == 0 {
        return none
    }
    if k == 1 {
        return IndexByteWords(s, set[0])
    }
    var bm: [4]u64 = [0; 4]
    for x in set {
        bm[x >> 6] |= Cast[u64](1) << Cast[int](x & 63)
    }
    for i in 0..s.Len() {
        x := s[i]
        if (bm[x >> 6] >> Cast[int](x & 63)) & 1 != 0 {
            return some(i)
        }
    }
    return none
}

// CountWords is Count in words: each word's exact zero marks, shifted to
// bit 0, are added to eight byte counters, folded every 255 words (Muła's
// SWAR byte count) so no counter wraps.
CountWords(s: []u8, b: u8): int {
    n := s.Len()
    pat := Cast[u64](b) *% Ones8
    var total := 0
    var i := 0
    for i + 8 <= n {
        steps := if (n - i) / 8 > 255 { 255 } else { (n - i) / 8 }
        var acc: u64 = 0
        for j in 0..steps {
            acc = acc +% (Zeros(Word(s, i + 8 * j) ^ pat) >> 7)
        }
        i += 8 * steps
        // Pairs of byte counters into four 16-bit sums (each at most 510),
        // then the four into the top 16 bits.
        pairs := (acc & 0x00FF00FF00FF00FF) +% ((acc >> 8) & 0x00FF00FF00FF00FF)
        total += Cast[int]((pairs *% 0x0001000100010001) >> 48)
    }
    for i < n {
        if s[i] == b {
            total += 1
        }
        i += 1
    }
    return total
}

// EqualWords is Equal in words for a.Len() == b.Len(): two words per step,
// then the last eight bytes by one overlapping word, below eight bytes one
// byte at a time.
EqualWords(a: []u8, b: []u8): bool {
    n := a.Len()
    if n < 8 {
        for i in 0..n {
            if a[i] != b[i] {
                return false
            }
        }
        return true
    }
    var i := 0
    for i + 16 <= n {
        if ((Word(a, i) ^ Word(b, i)) | (Word(a, i + 8) ^ Word(b, i + 8))) != 0 {
            return false
        }
        i += 16
    }
    if i + 8 <= n {
        if Word(a, i) != Word(b, i) {
            return false
        }
        i += 8
    }
    return i == n || Word(a, n - 8) == Word(b, n - 8)
}

// CompareWords is Compare in words: the first differing word's lowest set
// bit of a ^ b names the first differing byte; the tail is one overlapping
// word, below eight bytes one byte at a time; an equal prefix orders by
// length.
CompareWords(a: []u8, b: []u8): int {
    m := if b.Len() < a.Len() { b.Len() } else { a.Len() }
    if m < 8 {
        for j in 0..m {
            if a[j] != b[j] {
                return ByteOrder(a, b, j)
            }
        }
        return LenOrder(a.Len(), b.Len())
    }
    var i := 0
    for i + 8 <= m {
        d := Word(a, i) ^ Word(b, i)
        if d != 0 {
            return ByteOrder(a, b, i + raw.Ctz64(d) / 8)
        }
        i += 8
    }
    if i < m {
        d := Word(a, m - 8) ^ Word(b, m - 8)
        if d != 0 {
            return ByteOrder(a, b, m - 8 + raw.Ctz64(d) / 8)
        }
    }
    return LenOrder(a.Len(), b.Len())
}

// ValidUtf8Words is ValidUtf8 in words: ASCII two words per test, a word
// holding a byte from 0x80 jumps to it, and each sequence is judged by
// Utf8SeqLen (table 3-7).
ValidUtf8Words(s: []u8): bool {
    n := s.Len()
    var i := 0
    for i < n {
        if i + 16 <= n {
            w0 := Word(s, i) & Highs8
            w1 := Word(s, i + 8) & Highs8
            if (w0 | w1) == 0 {
                i += 16
                continue
            }
            i += if w0 != 0 { raw.Ctz64(w0) / 8 } else { 8 + raw.Ctz64(w1) / 8 }
        } else if s[i] < 0x80 {
            i += 1
            continue
        }
        k := Utf8SeqLen(s, i)
        if k == 0 {
            return false
        }
        i += k
    }
    return true
}
