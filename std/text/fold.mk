// Why: caseless matching ("Stra\u{DF}e" against "STRASSE") needs Unicode's full
// case folding; a one-to-one lower-case mapping misses the cases where one
// scalar folds to several.
//
// What: text.FoldCase, full default case folding (Unicode section 3.13,
// CaseFolding.txt statuses C and F, not T). It is not str.ToLower, the
// simple one-to-one lower mapping: "Stra\u{DF}e" folds to "strasse", and
// U+0130 to "i\u{307}". Layer 1 of std.
//
// How: two passes: the first sizes the result exactly and notes whether
// anything changes (if nothing does, the result is one copy of s); the second
// writes it. ASCII by arithmetic, every other scalar by one two-stage lookup
// into FoldPool. ASCII runs go 16 bytes a vector (std.simd at the 16-lane
// baseline, not compiled on x86_64-freestanding), then 8 bytes a word.

import std.raw
import std.simd

/// s with Unicode's full default case folding, in a: the form to compare
/// text without regard to case. One scalar may fold to several, so
/// "Stra\u{DF}e" folds to "strasse" and U+0130 to "i\u{307}"; that is the
/// difference from str.ToLower, which maps one scalar to one. Turkic
/// dotless-i folding is not applied. Fails only with OutOfMemory. O(n), two
/// passes; allocates the result, exactly its length, in a (one copy of s
/// when nothing folds).
export FoldCase(a: &Arena, s: str): OutOfMemory!str from a {
    b := s.AsBytes()
    n := b.Len()
    var size := 0
    var changed := false
    var i := 0
    for i < n {
        c := b[i]
        if c < 0x80 {
            // The probes run only from an ASCII byte, so mixed text pays
            // them once per ASCII run (as ToUtf16's).
            if Target.os != Os.Freestanding && i + 16 <= n {
                // The ASCII lanes up to the first other one (at least b[i]);
                // the all-ASCII step adds a constant, so the next load does
                // not wait on this one's mask.
                v := simd.Vec[u8, 16].FromSlice(b, i)
                hi := (v >= simd.Vec[u8, 16].Splat(0x80)).Bits()
                up := ((v >= simd.Vec[u8, 16].Splat(0x41)) & (v <= simd.Vec[u8, 16].Splat(0x5A))).Bits()
                if hi == 0 {
                    if up != 0 {
                        changed = true
                    }
                    size += 16
                    i += 16
                    continue
                }
                k := raw.Ctz64(hi)
                if up & ((Cast[u64](1) << k) - 1) != 0 {
                    changed = true
                }
                size += k
                i += k
                continue
            }
            if i + 8 <= n {
                w := Word(b, i)
                if w & HighBits == 0 {
                    if Upper8(w) != 0 {
                        changed = true
                    }
                    size += 8
                    i += 8
                    continue
                }
            }
            if c >= 0x41 && c <= 0x5A {
                changed = true
            }
            size += 1
            i += 1
            continue
        }
        r := Decode(b, i)
        f := FoldOf(r.c)
        if f == 0 {
            size += r.n
        } else {
            changed = true
            size += FoldBytes(f)
        }
        i += r.n
    }
    if !changed {
        return try a.Dup(s)
    }
    out := try a.Slice[u8](size)
    pool := &FoldPool
    var o := 0
    i = 0
    for i < n {
        c := b[i]
        if c < 0x80 {
            if Target.os != Os.Freestanding && i + 16 <= n && o + 16 <= size {
                // Bit 5 set in each upper-case lane: its lower case. All 16
                // lanes are stored; those past the ASCII ones are written
                // again by what follows, since out is written in order to
                // its end.
                v := simd.Vec[u8, 16].FromSlice(b, i)
                up := (v >= simd.Vec[u8, 16].Splat(0x41)) & (v <= simd.Vec[u8, 16].Splat(0x5A))
                (v | up.Select(simd.Vec[u8, 16].Splat(0x20), simd.Vec[u8, 16].Zero())).CopyTo(out, o)
                hi := (v >= simd.Vec[u8, 16].Splat(0x80)).Bits()
                if hi == 0 {
                    o += 16
                    i += 16
                    continue
                }
                k := raw.Ctz64(hi)
                o += k
                i += k
                continue
            }
            if i + 8 <= n {
                w := Word(b, i)
                if w & HighBits == 0 {
                    // Bit 5 of each upper-case byte set: its lower case.
                    x := w | (Upper8(w) >> 2)
                    var d := mut out[o..o + 8]
                    for k in 0..8 {
                        d[k] = Trunc[u8](x >> (8 * k))
                    }
                    o += 8
                    i += 8
                    continue
                }
            }
            out[o] = if c >= 0x41 && c <= 0x5A { c + 0x20 } else { c }
            o += 1
            i += 1
            continue
        }
        r := Decode(b, i)
        f := FoldOf(r.c)
        if f == 0 {
            for k in 0..r.n {
                out[o + k] = b[i + k]
            }
            o += r.n
        } else {
            off := f >> 2
            for k in 0..f & 3 {
                o = Encode(out, o, pool[off + k])
            }
        }
        i += r.n
    }
    // out holds s's scalars, each kept or replaced by its folding's scalars
    // encoded whole, so it is UTF-8.
    return str.From(out[..]).Must()
}

// Upper8 has bit 7 of each byte of w that is A to Z; every byte of w is
// ASCII, so neither sum carries out of its byte: b + 0x3F reaches bit 7
// from 'A' up, b + 0x25 from 'Z' + 1 up.
Upper8(w: u64): u64 {
    return (w +% 0x3F3F3F3F3F3F3F3F) & ~(w +% 0x2525252525252525) & HighBits
}

// FoldBytes is the UTF-8 length of the folding f names.
FoldBytes(f: u32): int {
    pool := &FoldPool
    off := f >> 2
    var size := 0
    for k in 0..f & 3 {
        size += Utf8Len(pool[off + k])
    }
    return size
}
