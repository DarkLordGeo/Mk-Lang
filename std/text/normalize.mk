// Why: one text can be spelled with different scalar sequences (U+00E9 as one
// scalar or as "e" and a combining accent), and comparing, hashing or
// storing it needs one canonical spelling.
//
// What: text.Normalize, the four normalization forms of UAX 15 (Unicode
// section 3.11): Nfc, Nfd, Nfkc and Nfkd. Layer 1 of std.
//
// How: ICU Normalizer2's structure. A span that passes the form's quick
// check (UAX 15 section 9: every scalar quick-check Yes, canonical classes in
// order) is copied unchanged. At the first scalar that fails it, the work
// backs up to the last boundary, a scalar of class 0 that is quick-check Yes
// (no decomposition starts with a non-starter there, and nothing composes
// across it), and normalizes the chunk up to the next boundary: full
// decomposition into a scalar buffer (precomputed, Hangul by arithmetic),
// canonical ordering by a stable sort on the class, and for Nfc and Nfkc
// canonical composition. A string that passes the quick check whole is one
// copy. Otherwise a first pass (Plan) walks the same spans and chunks and
// sizes the two allocations: the output, the spans' own bytes plus the
// decomposed bytes of the chunks' scalars (no composite is longer than its two
// parts, which the generator checks), so the slack is the decomposition growth
// of the chunks only; and, when the largest chunk decomposes to more than 32
// scalars, one buffer of twice that many u32 for every chunk, whose second
// half is the counting sort's room. A run of non-starters is put in order by
// insertion up to 32 scalars, else by a stable counting sort on the class. O(n)
// for every input. Span takes ASCII 16 bytes a vector (std.simd at the 16-lane
// baseline, not compiled on x86_64-freestanding), then 8 a word.

import std.raw
import std.simd

/// A Unicode normalization form (UAX 15). Nfd decomposes canonically and
/// Nfc then composes again; Nfkd and Nfkc do the same with compatibility
/// decomposition, which also folds variants such as ligatures and
/// full-width letters.
export enum Form {
    Nfc
    Nfd
    Nfkc
    Nfkd
}

// NormMeta's flag bits (tables.mk), shifted down by 8.
const NfcNo: u32 = 1
const NfcMaybe: u32 = 2
const NfkcNo: u32 = 4
const NfkcMaybe: u32 = 8
const NfdNo: u32 = 16
const NfkdNo: u32 = 32
const HangulLvt: u32 = 64

// Hangul syllable arithmetic, Unicode section 3.12.
const SBase: u32 = 0xAC00
const LBase: u32 = 0x1100
const VBase: u32 = 0x1161
const TBase: u32 = 0x11A7
const LCount: u32 = 19
const VCount: u32 = 21
const TCount: u32 = 28
const NCount: u32 = 588
const SCount: u32 = 11172

// The decomposed scalars a chunk holds in the frame before it needs a
// buffer from the arena. At most InsertRun, so a chunk in the frame never
// needs the counting sort's room.
const LocalChunk: int = 32

// The longest run of non-starters Reorder sorts by insertion (at most
// 496 moves); a longer one takes the counting sort.
const InsertRun: int = 32

// The flags that fail form's quick check.
QcMask(form: Form): u32 {
    return match form {
        .Nfc { NfcNo | NfcMaybe }
        .Nfd { NfdNo }
        .Nfkc { NfkcNo | NfkcMaybe }
        .Nfkd { NfkdNo }
    }
}

/// s in the normalization form, in a: one spelling for text that Unicode
/// treats as equal, such as U+00E9 and "e" followed by U+0301. Use it
/// before comparing, hashing or storing text. Text already in the form is
/// one copy. Fails only with OutOfMemory. O(n) for every input; allocates
/// the result and, when a long run of combining marks decomposes to more
/// than 32 scalars, one scratch buffer in a.
export Normalize(a: &Arena, s: str, form: Form): OutOfMemory!str from a {
    mask := QcMask(form)
    compat := form == Form.Nfkc || form == Form.Nfkd
    compose := form == Form.Nfc || form == Form.Nfkc
    b := s.AsBytes()
    n := b.Len()
    first := Span(b, 0, mask)
    if first.stop >= n {
        return try a.Dup(s)
    }
    plan := Plan(b, first.boundary, mask, compat)
    out := try a.Slice[u8](plan.bytes)
    need := if plan.scalars > LocalChunk { 2 * plan.scalars } else { 0 }
    big := try a.Slice[u32](need)
    var local: [32]u32 = [0; 32]
    var o := CopyInto(out, 0, b[0..first.boundary])
    var pos := first.boundary
    for {
        end := ChunkEnd(b, pos, mask)
        if need == 0 {
            o = NormChunk(b[pos..end], mut local[..], out, o, compat, compose)
        } else {
            o = NormChunk(b[pos..end], big, out, o, compat, compose)
        }
        sp := Span(b, end, mask)
        o = CopyInto(out, o, b[end..sp.boundary])
        if sp.stop >= n {
            break
        }
        pos = sp.boundary
    }
    // out holds runs of s copied between scalar boundaries and normalized
    // scalars encoded whole, so it is UTF-8.
    return str.From(out[0..o]).Must()
}

// Plan walks b from start, the first span's boundary, through the chunks
// and spans Normalize will take, and returns the output bound (start, the
// spans' bytes, and the decomposed bytes of every chunk) and the most
// scalars a chunk decomposes to.
Plan(b: []u8, start: int, mask: u32, compat: bool): (bytes: int, scalars: int) {
    n := b.Len()
    var bytes := start
    var most := 0
    var pos := start
    for {
        end := ChunkEnd(b, pos, mask)
        c := ChunkSize(b, pos, end, compat)
        bytes += c.bytes
        if c.scalars > most {
            most = c.scalars
        }
        sp := Span(b, end, mask)
        bytes += sp.boundary - end
        if sp.stop >= n {
            break
        }
        pos = sp.boundary
    }
    return (bytes: bytes, scalars: most)
}

// CopyInto copies src to out[o..] and returns the offset after it.
CopyInto(out: mut []u8, o: int, src: []u8): int {
    m := src.Len()
    var dst := mut out[o..o + m]
    dst.CopyFrom(src)
    return o + m
}

// Span checks b from start, a boundary (or 0), against the quick check
// of mask. stop is the first scalar that fails it and boundary the last
// boundary at or before it; both are n when nothing fails.
Span(b: []u8, start: int, mask: u32): (stop: int, boundary: int) {
    n := b.Len()
    meta := &NormMeta
    var i := start
    var bnd := start
    var last: u32 = 0
    for i < n {
        c := b[i]
        if c < 0x80 {
            // ASCII bytes are boundaries: the ASCII lanes of a vector up to
            // the first other one (at least b[i]), else eight a word,
            // probed only from an ASCII byte.
            if Target.os != Os.Freestanding && i + 16 <= n {
                // The all-ASCII step adds a constant, so the next load does
                // not wait on this one's mask; a data-dependent step is
                // slower than the word loop.
                hi := (simd.Vec[u8, 16].FromSlice(b, i) >= simd.Vec[u8, 16].Splat(0x80)).Bits()
                if hi == 0 {
                    bnd = i + 15
                    last = 0
                    i += 16
                    continue
                }
                k := raw.Ctz64(hi)
                bnd = i + k - 1
                last = 0
                i += k
                continue
            }
            if i + 8 <= n && Word(b, i) & HighBits == 0 {
                bnd = i + 7
                last = 0
                i += 8
                continue
            }
            bnd = i
            last = 0
            i += 1
            continue
        }
        r := Decode(b, i)
        id := NormId(r.c)
        if id == 0 {
            bnd = i
            last = 0
            i += r.n
            continue
        }
        m := meta[id]
        ccc := m & 0xFF
        if (m >> 8) & mask != 0 || (ccc != 0 && ccc < last) {
            return (stop: i, boundary: bnd)
        }
        if ccc == 0 {
            bnd = i
        }
        last = ccc
        i += r.n
    }
    return (stop: n, boundary: n)
}

// ChunkEnd is the next boundary after the scalar at pos, or n.
ChunkEnd(b: []u8, pos: int, mask: u32): int {
    n := b.Len()
    meta := &NormMeta
    var i := pos + Decode(b, pos).n
    for i < n {
        if b[i] < 0x80 {
            return i
        }
        r := Decode(b, i)
        id := NormId(r.c)
        if id == 0 {
            return i
        }
        m := meta[id]
        if m & 0xFF == 0 && (m >> 8) & mask == 0 {
            return i
        }
        i += r.n
    }
    return n
}

// ChunkSize is what b[pos..end] decomposes to: its bytes and its scalars.
// Every chunk's output is at most its bytes: the decomposition of each
// scalar, in canonical order, then composed, which never lengthens it.
ChunkSize(b: []u8, pos: int, end: int, compat: bool): (bytes: int, scalars: int) {
    meta := &NormMeta
    offs := &NormOff
    var bytes := 0
    var count := 0
    var i := pos
    for i < end {
        r := Decode(b, i)
        i += r.n
        id := NormId(r.c)
        if id == 0 {
            bytes += r.n
            count += 1
            continue
        }
        m := meta[id]
        dlen := int((m >> 15) & 31)
        klen := int((m >> 20) & 31)
        if compat && klen > 0 {
            bytes += Trunc[int]((offs[id] >> 56) & 0xFF)
            count += klen
        } else if dlen > 0 {
            bytes += Trunc[int]((offs[id] >> 48) & 0xFF)
            count += dlen
        } else if (m >> 8) & HangulLvt != 0 {
            if (r.c - SBase) % TCount != 0 {
                bytes += 9
                count += 3
            } else {
                bytes += 6
                count += 2
            }
        } else {
            bytes += r.n
            count += 1
        }
    }
    return (bytes: bytes, scalars: count)
}

// NormChunk normalizes the chunk src into out[o..] through buf, which
// holds at least twice the chunk's decomposed scalars when one of its runs
// of non-starters is longer than InsertRun (Reorder), and returns the
// offset after it.
NormChunk(src: []u8, buf: mut []u32, out: mut []u8, o: int, compat: bool, compose: bool): int {
    var m := 0
    var i := 0
    for i < src.Len() {
        r := Decode(src, i)
        m = DecompInto(buf, m, r.c, compat)
        i += r.n
    }
    Reorder(buf, m)
    len := if compose { Compose(mut buf[0..m]) } else { m }
    var w := o
    for k in 0..len {
        w = Encode(out, w, buf[k] & 0xFFFFFF)
    }
    return w
}

// DecompInto writes scalar c's full decomposition at buf[m..], each
// entry ccc << 24 | scalar, and returns the index after it.
DecompInto(buf: mut []u32, m: int, c: u32, compat: bool): int {
    id := NormId(c)
    if id == 0 {
        buf[m] = c
        return m + 1
    }
    meta := NormMeta[id]
    offs := NormOff[id]
    dlen := int((meta >> 15) & 31)
    klen := int((meta >> 20) & 31)
    var len := 0
    var off := 0
    if compat && klen > 0 {
        len = klen
        off = Trunc[int]((offs >> 16) & 0xFFFF)
    } else if dlen > 0 {
        len = dlen
        off = Trunc[int](offs & 0xFFFF)
    }
    if len > 0 {
        dec := &Decomp
        for k in 0..len {
            buf[m + k] = dec[off + k]
        }
        return m + len
    }
    if (meta >> 8) & HangulLvt != 0 {
        s := c - SBase
        buf[m] = LBase + s / NCount
        buf[m + 1] = VBase + (s % NCount) / TCount
        t := s % TCount
        if t != 0 {
            buf[m + 2] = TBase + t
            return m + 3
        }
        return m + 2
    }
    buf[m] = ((meta & 0xFF) << 24) | c
    return m + 1
}

// Reorder puts every run of non-starters of buf[0..m] in canonical order,
// a stable sort by class: insertion for a run of up to InsertRun, else
// CountSort, through buf[m..], which then holds at least m entries.
Reorder(buf: mut []u32, m: int) {
    var i := 0
    for i < m {
        if buf[i] >> 24 == 0 {
            i += 1
            continue
        }
        var j := i + 1
        for j < m && buf[j] >> 24 != 0 {
            j += 1
        }
        if j - i > InsertRun {
            CountSort(buf, i, j, m)
        } else {
            for k in i + 1..j {
                x := buf[k]
                var h := k
                for h > i && buf[h - 1] >> 24 > x >> 24 {
                    buf[h] = buf[h - 1]
                    h -= 1
                }
                buf[h] = x
            }
        }
        i = j
    }
}

// CountSort sorts buf[i..j] stably by class (the top byte), through
// buf[room..room + j - i]: a counting sort over the 256 classes, O(j - i +
// 256), the same order as any stable sort on the class.
CountSort(buf: mut []u32, i: int, j: int, room: int) {
    var at: [256]int = [0; 256]
    for k in i..j {
        at[int(buf[k] >> 24)] += 1
    }
    var sum := room
    for c in 0..256 {
        t := at[c]
        at[c] = sum
        sum += t
    }
    for k in i..j {
        x := buf[k]
        c := int(x >> 24)
        buf[at[c]] = x
        at[c] += 1
    }
    halves := buf.SplitAt(room)
    (mut halves.left[i..j]).CopyFrom(halves.right[0..j - i])
}

// Compose applies canonical composition to buf, which is decomposed and
// in canonical order, in place, and returns the length left. A scalar
// joins the last starter unless something between them blocks it (a
// scalar of the same or a higher class, or a starter).
Compose(buf: mut []u32): int {
    n := buf.Len()
    if n == 0 {
        return 0
    }
    var starter := 0
    var has := buf[0] >> 24 == 0
    var last: u32 = if has { 0 } else { 256 }
    var w := 1
    for k in 1..n {
        x := buf[k]
        ccc := x >> 24
        if has && (last < ccc || last == 0) {
            if comp := ComposePair(buf[starter] & 0xFFFFFF, x & 0xFFFFFF) {
                buf[starter] = comp
                continue
            }
        }
        if ccc == 0 {
            starter = w
            has = true
        }
        last = ccc
        buf[w] = x
        w += 1
    }
    return w
}

// ComposePair is the primary composite of a and b, if there is one:
// Hangul by arithmetic, else a binary search of b's pairs.
ComposePair(a: u32, b: u32): ?u32 {
    if a >= LBase && a < LBase + LCount && b >= VBase && b < VBase + VCount {
        return some(SBase + ((a - LBase) * VCount + (b - VBase)) * TCount)
    }
    if a >= SBase && a < SBase + SCount && (a - SBase) % TCount == 0 && b > TBase && b < TBase + TCount {
        return some(a + (b - TBase))
    }
    id := NormId(b)
    if id == 0 {
        return none
    }
    clen := int(NormMeta[id] >> 25)
    if clen == 0 {
        return none
    }
    firsts := &CompFirst
    var lo := Trunc[int]((NormOff[id] >> 32) & 0xFFFF)
    var hi := lo + clen
    for lo < hi {
        mid := (lo + hi) / 2
        f := firsts[mid]
        if f == a {
            return some(CompResult[mid])
        }
        if f < a {
            lo = mid + 1
        } else {
            hi = mid
        }
    }
    return none
}
