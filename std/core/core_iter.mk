// Why: splitting text into pieces, lines, words and scalars is the most
// common text loop; these iterators do it with no allocation and fast byte
// searches.
//
// What: the iterators Split, Lines, Words and Scalars with their Next
// methods, and the byte helpers ScalarLen, ScalarAt, WordBytes8,
// IndexByteFrom and IndexByteIn.
//
// How: each iterator holds the text it walks (Split also its separator) and
// returns pieces that borrow from it. Byte searches read 8 bytes at a time
// with SWAR tests and, on longer text, std.simd's vector memchr; Words takes
// an ASCII fast path and falls back to one scalar at a time.

import std.raw
import std.simd

/// An iterator over the pieces of a text between occurrences of a
/// separator, made by str.Split.
export view struct Split {
    text: str
    sep: str
    pos: int
    done: bool
}

/// An iterator over the lines of a text, made by str.Lines.
export view struct Lines {
    text: str
    pos: int
}

/// An iterator over the whitespace-separated words of a text, made by
/// str.Words.
export view struct Words {
    text: str
    pos: int
}

/// An iterator over the Unicode scalars of a text, made by str.Scalars.
export view struct Scalars {
    text: str
    pos: int
}

/// The next piece before the separator, or none after the last. Amortized
/// O(piece length + separator length); allocates nothing.
///
/// Empty pieces are kept, and "" yields one empty piece. A one-byte
/// separator such as "," is found with a byte scan, a longer one as
/// str.Find finds it.
export Split.Next(mut self): ?str from self.text {
    if self.done {
        return none
    }
    start := self.pos
    if self.sep.Len() == 1 {
        if at := IndexByteFrom(self.text, self.sep.Byte(0), start) {
            self.pos = at + 1
            return some(self.text[start..at])
        }
        self.done = true
        return some(self.text[start..self.text.Len()])
    }
    if at := FindFrom(self.text, self.sep, start) {
        self.pos = at + self.sep.Len()
        return some(self.text[start..at])
    }
    self.done = true
    return some(self.text[start..self.text.Len()])
}

/// The next line, or none after the last. O(line length); allocates nothing.
///
/// Up to the next \n, with one \r before it stripped; a final newline ends
/// the last line without starting an empty one; a lone \r is kept.
export Lines.Next(mut self): ?str from self.text {
    n := self.text.Len()
    start := self.pos
    if start >= n {
        return none
    }
    if at := IndexByteFrom(self.text, 0x0A, start) {
        self.pos = at + 1
        if at > start && self.text.Byte(at - 1) == 0x0D {
            return some(self.text[start..at - 1])
        }
        return some(self.text[start..at])
    }
    self.pos = n
    return some(self.text[start..n])
}

/// The next word, or none after the last. O(bytes skipped and taken); allocates
/// nothing.
///
/// Skips White_Space scalars, then takes the longest run of other scalars,
/// so a word is never empty. ASCII text is read 8 bytes at a time; other
/// bytes one scalar at a time.
export Words.Next(mut self): ?str from self.text {
    b := self.text.AsBytes()
    n := b.Len()
    var p := self.pos
    for p < n {
        c := b[p]
        if c < 0x80 {
            if !AsciiSpaceByte(c) {
                break
            }
            p += 1
        } else {
            k := SpaceAt(self.text, p)
            if k == 0 {
                break
            }
            p += k
        }
    }
    if p >= n {
        self.pos = n
        return none
    }
    start := p
    for p < n {
        if n - p >= 8 {
            m := WordStops8(LoadU64(self.text, p))
            if m == 0 {
                p += 8
                continue
            }
            p += raw.Ctz64(m) / 8
        }
        c := b[p]
        if c < 0x80 {
            if AsciiSpaceByte(c) {
                break
            }
            p += 1
        } else {
            if SpaceAt(self.text, p) != 0 {
                break
            }
            p += ScalarLen(c)
        }
    }
    self.pos = p
    return some(self.text[start..p])
}

// AsciiSpaceByte reports whether c, below 0x80, is White_Space: 0x09 to
// 0x0D or 0x20 (core_tables.mk's AsciiSpace mask).
AsciiSpaceByte(c: u8): bool {
    return c < 64 && ((AsciiSpace >> u64(c)) & 1) != 0
}

// WordStops8 marks, with bit 7, each byte of w outside 0x21..0x7F; the
// lowest marked byte is exact (a borrow starts only at a marked byte and
// moves up), so Ctz64 / 8 of a nonzero result is the first byte at which
// a word may end. Zero is WordBytes8.
WordStops8(w: u64): u64 {
    return ((w -% 0x2121212121212121) | w) & 0x8080808080808080
}

/// Whether each of the 8 bytes of w is in 0x21..0x7F: printable ASCII or
/// DEL, no space and no byte of a multibyte scalar. O(1), a few word
/// operations with no branch per byte.
export WordBytes8(w: u64): bool {
    return WordStops8(w) == 0
}

/// The next scalar, or none at the end of the text. O(1); allocates nothing.
export Scalars.Next(mut self): ?char {
    b := self.text.AsBytes()
    p := self.pos
    if p >= b.Len() {
        return none
    }
    c := b[p]
    if c < 0x80 {
        self.pos = p + 1
        return some(char(c))
    }
    r := ScalarAt(b, p)
    self.pos = p + r.len
    return some(r.c)
}

// ScalarAt decodes the multi-byte scalar that starts at b[p]; b is valid
// UTF-8 and b[p] a lead byte (0xC2 to 0xF4), so its continuation bytes are
// there. Straight-line, one step per length.
ScalarAt(b: []u8, p: int): (c: char, len: int) {
    lead := u32(b[p])
    c1 := u32(b[p + 1]) & 0x3F
    if lead < 0xE0 {
        return (c: Cast[char](((lead & 0x1F) << 6) | c1), len: 2)
    }
    c2 := u32(b[p + 2]) & 0x3F
    if lead < 0xF0 {
        return (c: Cast[char](((lead & 0x0F) << 12) | (c1 << 6) | c2), len: 3)
    }
    c3 := u32(b[p + 3]) & 0x3F
    return (c: Cast[char](((lead & 0x07) << 18) | (c1 << 12) | (c2 << 6) | c3), len: 4)
}

/// The byte length of the UTF-8 sequence that the lead byte lead starts.
/// O(1).
export ScalarLen(lead: u8): int {
    if lead < 0x80 {
        return 1
    }
    if lead < 0xE0 {
        return 2
    }
    if lead < 0xF0 {
        return 3
    }
    return 4
}

/// The offset of the first byte c of s at or after from, or none when there
/// is none (also when from >= s.Len()). A negative from panics (code 2).
/// IndexByteIn over the text's bytes: O(n); allocates nothing.
export IndexByteFrom(s: str, c: u8, from: int): ?int {
    return IndexByteIn(s.AsBytes(), c, from)
}

/// IndexByteFrom over bytes: the offset of the first c in b at or after
/// from, or none (also when from >= b.Len()). A negative from panics
/// (code 2). O(n); allocates nothing.
///
/// The first 32 bytes are tested 8 at a time in general registers, since a
/// short line or field usually ends there and needs no vector setup. Past
/// them, with 16 bytes or more left, the scan uses std.simd's IndexByte:
/// 16-byte vectors, wider where Cpu() reports avx2 or avx512bw. On
/// x86_64-freestanding it uses 8-byte words throughout.
export IndexByteIn(b: []u8, c: u8, from: int): ?int {
    n := b.Len()
    if from < 0 {
        raw.Fail(2, "IndexByteIn: negative start")
    }
    pat := u64(c) * 0x0101010101010101
    var i := from
    if n - i >= 16 {
        x := LoadWord(b, i) ^ pat
        y := LoadWord(b, i + 8) ^ pat
        mx := (x -% 0x0101010101010101) & ~x & 0x8080808080808080
        my := (y -% 0x0101010101010101) & ~y & 0x8080808080808080
        if mx != 0 {
            return some(i + raw.Ctz64(mx) / 8)
        }
        if my != 0 {
            return some(i + 8 + raw.Ctz64(my) / 8)
        }
        i += 16
        if Target.os != Os.Freestanding {
            if n - i >= 16 {
                u := LoadWord(b, i) ^ pat
                v := LoadWord(b, i + 8) ^ pat
                mu := (u -% 0x0101010101010101) & ~u & 0x8080808080808080
                mv := (v -% 0x0101010101010101) & ~v & 0x8080808080808080
                if mu != 0 {
                    return some(i + raw.Ctz64(mu) / 8)
                }
                if mv != 0 {
                    return some(i + 8 + raw.Ctz64(mv) / 8)
                }
                i += 16
            }
            if n - i >= 16 {
                if k := simd.IndexByte(b[i..], c) {
                    return some(i + k)
                }
                return none
            }
        }
    }
    if Target.os == Os.Freestanding {
        // std.core's own form on x86_64-freestanding: four words
        // (32 bytes) per step with one combined test, then the word that
        // holds the match 8 bytes at a time, then bytes. The one word of the
        // hosted form is its else, so neither form's frame holds the other's
        // words (a frame costs compile-time steps by its size).
        for n - i >= 32 {
            w0 := LoadWord(b, i) ^ pat
            w1 := LoadWord(b, i + 8) ^ pat
            w2 := LoadWord(b, i + 16) ^ pat
            w3 := LoadWord(b, i + 24) ^ pat
            m0 := (w0 -% 0x0101010101010101) & ~w0
            m1 := (w1 -% 0x0101010101010101) & ~w1
            m2 := (w2 -% 0x0101010101010101) & ~w2
            m3 := (w3 -% 0x0101010101010101) & ~w3
            if ((m0 | m1 | m2 | m3) & 0x8080808080808080) != 0 {
                break
            }
            i += 32
        }
        for n - i >= 8 {
            w := LoadWord(b, i) ^ pat
            m := (w -% 0x0101010101010101) & ~w & 0x8080808080808080
            if m != 0 {
                return some(i + raw.Ctz64(m) / 8)
            }
            i += 8
        }
    } else if n - i >= 8 {
        x := LoadWord(b, i) ^ pat
        m := (x -% 0x0101010101010101) & ~x & 0x8080808080808080
        if m != 0 {
            return some(i + raw.Ctz64(m) / 8)
        }
        i += 8
    }
    for i < n {
        if b[i] == c {
            return some(i)
        }
        i += 1
    }
    return none
}
