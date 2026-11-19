// Why: the compressed block is most of a Zstandard decoder, and the part a
// hostile input attacks; it lives apart from the frame and stream logic.
//
// What: ZBlock, which decodes one compressed block of RFC 8878 (section
// 3.1.1.3) for ZstdDecoder (zstd.mk), with ZLiterals, ZSequences, ZHuff,
// ZNorm, ZBuild and ZBits, the backward bit reader.
//
// How: the literals section (raw, RLE, or Huffman coded in one or four
// streams, with a new or the previous table), the sequences section
// (literal length, offset and match length codes under predefined, RLE,
// FSE-described or repeated tables), and the execution of the sequences into
// a window that may have wrapped once. The shape is the zstd reference
// decoder's (lib/decompress: zstd_decompress_block.c, huf_decompress.c,
// fse_decompress.c, bitstream.h): a 64-bit container read backward from the
// end of each stream, decoding tables built per block, the three repeat
// offsets kept across blocks of a frame. Every read is checked against the
// block's bytes and every write against the block's output limit, so a
// hostile block costs O(its size + 128 KiB) and no more memory than the
// decoder took at construction. Part of the zstd decoder (zstd*.mk).

import std.raw

// ZBits reads a stream backward (RFC 8878 section 4.1: the stream's last
// byte holds a 1 bit above its last bit, and bits are read from there
// down). c holds the 8 bytes src[p..p + 8] little-endian, of which the top
// used bits are consumed; s is the stream's first byte. The stream is
// read exactly when p == s and used == 64; used above 64 is an overrun.
struct ZBits {
    c: u64
    used: u64
    p: int
    s: int
}

// ZBitsInit starts b on the stream src[s..e]: false when it is empty or
// its last byte holds no marker bit.
ZBitsInit(b: mut ZBits, src: []u8, s: int, e: int): bool {
    if e <= s {
        return false
    }
    last := src[e - 1]
    if last == 0 {
        return false
    }
    hb := Trunc[u64](63 - raw.Clz64(u64(last)))
    b.s = s
    if e - s >= 8 {
        b.p = e - 8
        b.c = Le64(src, e - 8)
        b.used = 8 - hb
        return true
    }
    var c: u64 = 0
    for j in 0..e - s {
        c = c | (u64(src[s + j]) << Trunc[u64](8 * j))
    }
    b.p = s
    b.c = c
    b.used = Trunc[u64](8 - (e - s)) * 8 + 8 - hb
    return true
}

// ZLook is the next n bits (0 to 56) without consuming them: the bits
// past the stream's first bit read as 0, and after an overrun the value
// is garbage of n bits, which ZReload and ZDone then refuse.
ZLook(b: &ZBits, n: u64): u64 {
    return ((b.c << (b.used & 63)) >> 1) >> ((63 - n) & 63)
}

// ZRead consumes and returns the next n bits (0 to 56).
ZRead(b: mut ZBits, n: u64): u64 {
    v := ZLook(&*b, n)
    b.used += n
    return v
}

// ZReload refills c so that at least 57 bits are held, or every bit left
// when fewer remain; false after an overrun.
ZReload(b: mut ZBits, src: []u8): bool {
    if b.used > 64 {
        return false
    }
    if b.p >= b.s + 8 {
        b.p -= Trunc[int](b.used >> 3)
        b.used = b.used & 7
        b.c = Le64(src, b.p)
        return true
    }
    if b.p == b.s {
        return true
    }
    var nb := Trunc[int](b.used >> 3)
    if b.p - nb < b.s {
        nb = b.p - b.s
    }
    b.p -= nb
    b.used -= Trunc[u64](nb) * 8
    b.c = Le64(src, b.p)
    return true
}

// ZLeft is the number of bits not yet consumed (used at most 64).
ZLeft(b: &ZBits): int {
    return (b.p - b.s) * 8 + 64 - Trunc[int](b.used)
}

// ZDone is whether the stream was read exactly to its first bit.
ZDone(b: &ZBits): bool {
    return b.p == b.s && b.used == 64
}

// ZFwd is the k bits (0 to 32) of src[0..end] that start at bit pos,
// least significant first, the bits at and past end reading as 0.
ZFwd(src: []u8, end: int, pos: int, k: int): int {
    i := pos >> 3
    var v: u64 = 0
    var j := 0
    for j < 6 && i + j < end {
        v = v | (u64(src[i + j]) << Trunc[u64](8 * j))
        j += 1
    }
    v = v >> Trunc[u64](pos & 7)
    return Trunc[int](v & ((u64(1) << Trunc[u64](k)) - 1))
}

// ZNorm reads an FSE table description (RFC 8878 section 4.1.1) from
// src[off..end] for the symbols 0 to max_sym with an accuracy log of at
// most max_log: the normalized counts into norm (-1 for "less than 1"),
// the log into lg. Returns the bytes it took, or -1 when the description
// is corrupt. O(max_sym + bytes); allocates nothing. As the reference's
// FSE_readNCount and Go's readFSE.
ZNorm(src: []u8, off: int, end: int, max_sym: int, max_log: int, norm: mut [256]int, lg: mut int): int {
    limit := end * 8
    var pos := off * 8
    if pos + 4 > limit {
        return -1
    }
    log := ZFwd(src, end, pos, 4) + 5
    pos += 4
    if log > max_log {
        return -1
    }
    var remaining := (1 << log) + 1
    var threshold := 1 << log
    var nbits := log + 1
    var sym := 0
    var prev0 := false
    for remaining > 1 && sym <= max_sym {
        if prev0 {
            var z := sym
            for {
                if pos + 2 > limit {
                    return -1
                }
                r := ZFwd(src, end, pos, 2)
                pos += 2
                z += r
                if r != 3 {
                    break
                }
            }
            if z > max_sym {
                return -1
            }
            for sym < z {
                norm[sym] = 0
                sym += 1
            }
            prev0 = false
            continue
        }
        max := 2 * threshold - 1 - remaining
        v := ZFwd(src, end, pos, nbits)
        var count := 0
        if v & (threshold - 1) < max {
            count = v & (threshold - 1)
            pos += nbits - 1
        } else {
            count = v & (2 * threshold - 1)
            if count >= threshold {
                count -= max
            }
            pos += nbits
        }
        if pos > limit {
            return -1
        }
        count -= 1
        remaining -= if count >= 0 { count } else { 1 }
        norm[sym] = count
        sym += 1
        prev0 = count == 0
        if remaining < 1 {
            return -1
        }
        for remaining < threshold {
            nbits -= 1
            threshold = threshold >> 1
        }
    }
    if remaining != 1 {
        return -1
    }
    for sym <= max_sym {
        norm[sym] = 0
        sym += 1
    }
    *lg = log
    return (pos + 7) / 8 - off
}

// ZBuild builds the FSE decoding table of the normalized counts
// norm[0..n] with log lg into t[0..1 << lg]: each entry is its symbol, its
// bit count << 8 and its next state's base << 16 (RFC 8878 section
// 4.1.1, the reference's FSE_buildDTable). False when the spread does not
// come back to 0, which counts that sum to the table size always do.
ZBuild(norm: &[256]int, n: int, lg: int, t: mut []u32): bool {
    size := 1 << lg
    var high := size - 1
    var next: [256]int = [0; 256]
    for s in 0..n {
        if norm[s] == -1 {
            t[high] = Trunc[u32](s)
            high -= 1
            next[s] = 1
        } else {
            next[s] = norm[s]
        }
    }
    step := (size >> 1) + (size >> 3) + 3
    mask := size - 1
    var pos := 0
    for s in 0..n {
        var c := norm[s]
        for c > 0 {
            c -= 1
            t[pos] = Trunc[u32](s)
            pos = (pos + step) & mask
            for pos > high {
                pos = (pos + step) & mask
            }
        }
    }
    if pos != 0 {
        return false
    }
    for u in 0..size {
        s := Trunc[int](t[u] & 0xFF)
        ns := next[s]
        next[s] = ns + 1
        if ns < 1 {
            return false
        }
        nb := lg - (63 - raw.Clz64(Trunc[u64](ns)))
        base := (ns << nb) - size
        t[u] = Trunc[u32](s | (nb << 8) | (base << 16))
    }
    return true
}

// Literal length codes 16 to 35 and match length codes 32 to 52: their
// baselines and extra bits (RFC 8878 section 3.1.1.3.2.1.1).
const ZLlBase: [20]u64 = [16, 18, 20, 22, 24, 28, 32, 40, 48, 64, 128, 256, 512, 1024, 2048, 4096, 8192, 16384, 32768, 65536]
const ZLlBits: [20]u64 = [1, 1, 1, 1, 2, 2, 3, 3, 4, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16]
const ZMlBase: [21]u64 = [35, 37, 39, 41, 43, 47, 51, 59, 67, 83, 99, 131, 259, 515, 1027, 2051, 4099, 8195, 16387, 32771, 65539]
const ZMlBits: [21]u64 = [1, 1, 1, 1, 2, 2, 3, 3, 4, 4, 5, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16]

// The predefined distributions (RFC 8878 section 3.1.1.3.2.2).
const ZLlNorm: [36]int = [4, 3, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 1, 1, 1, 2, 2, 2, 2, 2, 2, 2, 2, 2, 3, 2, 1, 1, 1, 1, 1, -1, -1, -1, -1]
const ZMlNorm: [53]int = [1, 4, 3, 2, 2, 2, 2, 2, 2, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, -1, -1, -1, -1, -1, -1, -1]
const ZOfNorm: [29]int = [1, 1, 1, 1, 1, 1, 2, 2, 2, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, -1, -1, -1, -1, -1]

// Sequence code kinds, and each one's largest symbol and accuracy log.
const ZKLl = 0
const ZKOf = 1
const ZKMl = 2

// ZSeqEntry is a sequence decoding table entry: the code's baseline in
// bits 0 to 31, its extra bits in 32 to 39, the state's bit count in 40
// to 47 and the next state's base in 48 to 63.
ZSeqEntry(kind: int, sym: int, nb: int, next: int): u64 {
    var base: u64 = 0
    var add: u64 = 0
    s := Trunc[u64](sym)
    if kind == ZKLl {
        if sym < 16 {
            base = s
        } else {
            base = ZLlBase[sym - 16]
            add = ZLlBits[sym - 16]
        }
    } else if kind == ZKMl {
        if sym < 32 {
            base = s + 3
        } else {
            base = ZMlBase[sym - 32]
            add = ZMlBits[sym - 32]
        }
    } else {
        base = u64(1) << s
        add = s
    }
    return base | (add << 32) | (Trunc[u64](nb) << 40) | (Trunc[u64](next) << 48)
}

// ZSeqTable turns the FSE table t[0..1 << lg] of sequence codes of kind
// into the sequence decoding table out.
ZSeqTable(kind: int, t: []u32, lg: int, out: mut [512]u64) {
    for u in 0..1 << lg {
        e := t[u]
        out[u] = ZSeqEntry(kind, Trunc[int](e & 0xFF), Trunc[int]((e >> 8) & 0xFF), Trunc[int](e >> 16))
    }
}

// ZDefSeq is the decoding table of kind's predefined distribution,
// computed at compile time.
ZDefSeq(kind: int): [512]u64 {
    var norm: [256]int = [0; 256]
    var n := 0
    var lg := 0
    if kind == ZKLl {
        n = 36
        lg = 6
        for i in 0..n {
            norm[i] = ZLlNorm[i]
        }
    } else if kind == ZKMl {
        n = 53
        lg = 6
        for i in 0..n {
            norm[i] = ZMlNorm[i]
        }
    } else {
        n = 29
        lg = 5
        for i in 0..n {
            norm[i] = ZOfNorm[i]
        }
    }
    var t: [512]u32 = [0; 512]
    _ = ZBuild(&norm, n, lg, mut t[..])
    var out: [512]u64 = [0; 512]
    ZSeqTable(kind, t[..], lg, mut out)
    return out
}

// ZDef64 and ZDef32 keep the used part of a predefined table (2^6 or
// 2^5 entries), so the binary holds 1,280 bytes of them, not 12 KiB.
ZDef64(kind: int): [64]u64 {
    full := ZDefSeq(kind)
    var o: [64]u64 = [0; 64]
    for u in 0..64 {
        o[u] = full[u]
    }
    return o
}

ZDef32(kind: int): [32]u64 {
    full := ZDefSeq(kind)
    var o: [32]u64 = [0; 32]
    for u in 0..32 {
        o[u] = full[u]
    }
    return o
}

const ZLlDef: [64]u64 = ZDef64(ZKLl)
const ZOfDef: [32]u64 = ZDef32(ZKOf)
const ZMlDef: [64]u64 = ZDef64(ZKMl)

// ZTabs is what a frame's blocks share: the Huffman table (hlog 0 before
// the frame's first one), the three sequence tables with their logs (-1
// before the first, which a repeat mode needs), and the repeat offsets.
// About 16 KiB, inline.
struct ZTabs {
    ht: [2048]u16
    hlog: int
    ll: [512]u64
    of: [512]u64
    ml: [512]u64
    lll: int
    ofl: int
    mll: int
    rep1: int
    rep2: int
    rep3: int
}

// ZNewTabs is the state at a frame's start (RFC 8878 section 3.1.2.5: the
// repeat offsets start at 1, 4 and 8).
ZNewTabs(): ZTabs {
    return ZTabs(ht: [0; 2048], hlog: 0, ll: [0; 512], of: [0; 512], ml: [0; 512], lll: -1, ofl: -1, mll: -1,
        rep1: 1, rep2: 4, rep3: 8)
}

// ZResetTabs puts z back to a frame's start without rebuilding the arrays.
ZResetTabs(z: mut ZTabs) {
    z.hlog = 0
    z.lll = -1
    z.ofl = -1
    z.mll = -1
    z.rep1 = 1
    z.rep2 = 4
    z.rep3 = 8
}

// ZHuff reads a Huffman tree description (RFC 8878 section 4.2.1) at
// src[off..end] into z.ht and z.hlog: the weights, direct or FSE coded
// with two interleaved states, then the implied last weight. Returns the
// bytes it took, or -1 when the description is corrupt. As the reference's
// HUF_readStats and Go's readHuff.
ZHuff(z: mut ZTabs, src: []u8, off: int, end: int): int {
    if off >= end {
        return -1
    }
    hdr := int(src[off])
    var i := off + 1
    var w: [256]u8 = [0; 256]
    var count := 0
    if hdr < 128 {
        if i + hdr > end {
            return -1
        }
        var norm: [256]int = [0; 256]
        var lg := 0
        k := ZNorm(src, i, i + hdr, 255, 6, mut norm, mut lg)
        if k < 0 {
            return -1
        }
        var t: [64]u32 = [0; 64]
        if !ZBuild(&norm, 256, lg, mut t[..]) {
            return -1
        }
        var b := ZBits(c: 0, used: 0, p: 0, s: 0)
        if !ZBitsInit(mut b, src, i + k, i + hdr) {
            return -1
        }
        l := Trunc[u64](lg)
        if ZLeft(&b) < 2 * lg {
            return -1
        }
        var s1 := Trunc[int](ZRead(mut b, l))
        var s2 := Trunc[int](ZRead(mut b, l))
        for {
            if !ZReload(mut b, src) {
                return -1
            }
            e1 := t[s1]
            n1 := Trunc[int]((e1 >> 8) & 0xFF)
            if ZLeft(&b) < n1 {
                if count >= 254 {
                    return -1
                }
                w[count] = Trunc[u8](e1)
                w[count + 1] = Trunc[u8](t[s2])
                count += 2
                break
            }
            s1 = Trunc[int](e1 >> 16) + Trunc[int](ZRead(mut b, Trunc[u64](n1)))
            if count >= 255 {
                return -1
            }
            w[count] = Trunc[u8](e1)
            count += 1
            e2 := t[s2]
            n2 := Trunc[int]((e2 >> 8) & 0xFF)
            if ZLeft(&b) < n2 {
                if count >= 254 {
                    return -1
                }
                w[count] = Trunc[u8](e2)
                w[count + 1] = Trunc[u8](t[s1])
                count += 2
                break
            }
            s2 = Trunc[int](e2 >> 16) + Trunc[int](ZRead(mut b, Trunc[u64](n2)))
            if count >= 255 {
                return -1
            }
            w[count] = Trunc[u8](e2)
            count += 1
        }
        i += hdr
    } else {
        count = hdr - 127
        nbytes := (count + 1) / 2
        if i + nbytes > end {
            return -1
        }
        for j in 0..nbytes {
            x := src[i + j]
            w[2 * j] = x >> 4
            w[2 * j + 1] = x & 15
        }
        i += nbytes
    }
    var rank: [13]int = [0; 13]
    var total := 0
    for j in 0..count {
        wj := int(w[j])
        if wj > 12 {
            return -1
        }
        rank[wj] += 1
        if wj > 0 {
            total += 1 << (wj - 1)
        }
    }
    if total == 0 {
        return -1
    }
    tb := 64 - raw.Clz64(Trunc[u64](total))
    if tb > 11 {
        return -1
    }
    left := (1 << tb) - total
    if left == 0 {
        return -1
    }
    hb := 63 - raw.Clz64(Trunc[u64](left))
    if 1 << hb != left || count >= 256 {
        return -1
    }
    w[count] = Trunc[u8](hb + 1)
    count += 1
    rank[hb + 1] += 1
    if rank[1] < 2 || rank[1] & 1 != 0 {
        return -1
    }
    var next := 0
    for k in 0..tb {
        cur := next
        next += rank[k + 1] << k
        rank[k + 1] = cur
    }
    for j in 0..count {
        wj := int(w[j])
        if wj == 0 {
            continue
        }
        n := 1 << (wj - 1)
        v := Trunc[u16]((j << 8) | (tb + 1 - wj))
        st := rank[wj]
        (mut z.ht[st..st + n]).Fill(v)
        rank[wj] = st + n
    }
    z.hlog = tb
    return i - off
}

// ZHuffStream decodes the Huffman stream src[s..e] into all of out with
// the table ht of log hlog. False unless the stream holds exactly
// out.Len() symbols.
ZHuffStream(ht: &[2048]u16, hlog: int, src: []u8, s: int, e: int, out: mut []u8): bool {
    var b := ZBits(c: 0, used: 0, p: 0, s: 0)
    if !ZBitsInit(mut b, src, s, e) {
        return false
    }
    return ZHuffRun(mut b, ht, Trunc[u64](hlog), src, out, 0)
}

// ZHuffRun decodes out[i..] from the stream b with the table ht of log lg:
// four symbols per refill while four remain, then one per refill. False
// unless the stream then ends exactly at its first bit.
ZHuffRun(b: mut ZBits, ht: &[2048]u16, lg: u64, src: []u8, out: mut []u8, i0: int): bool {
    n := out.Len()
    var i := i0
    for n - i >= 4 {
        if !ZReload(b, src) {
            return false
        }
        v0 := ht[Trunc[int](ZLook(&*b, lg))]
        b.used += u64(v0 & 0xFF)
        v1 := ht[Trunc[int](ZLook(&*b, lg))]
        b.used += u64(v1 & 0xFF)
        v2 := ht[Trunc[int](ZLook(&*b, lg))]
        b.used += u64(v2 & 0xFF)
        v3 := ht[Trunc[int](ZLook(&*b, lg))]
        b.used += u64(v3 & 0xFF)
        o := mut out[i..i + 4]
        o[0] = Trunc[u8](v0 >> 8)
        o[1] = Trunc[u8](v1 >> 8)
        o[2] = Trunc[u8](v2 >> 8)
        o[3] = Trunc[u8](v3 >> 8)
        i += 4
    }
    for i < n {
        if !ZReload(b, src) {
            return false
        }
        v := ht[Trunc[int](ZLook(&*b, lg))]
        b.used += u64(v & 0xFF)
        out[i] = Trunc[u8](v >> 8)
        i += 1
    }
    return ZReload(b, src) && ZDone(&*b)
}

// ZHuff4Step decodes the next four symbols of b into o (refilled first);
// false after an overrun.
ZHuff4Step(b: mut ZBits, ht: &[2048]u16, lg: u64, src: []u8, o: mut []u8): bool {
    if !ZReload(b, src) {
        return false
    }
    v0 := ht[Trunc[int](ZLook(&*b, lg))]
    b.used += u64(v0 & 0xFF)
    v1 := ht[Trunc[int](ZLook(&*b, lg))]
    b.used += u64(v1 & 0xFF)
    v2 := ht[Trunc[int](ZLook(&*b, lg))]
    b.used += u64(v2 & 0xFF)
    v3 := ht[Trunc[int](ZLook(&*b, lg))]
    b.used += u64(v3 & 0xFF)
    o[0] = Trunc[u8](v0 >> 8)
    o[1] = Trunc[u8](v1 >> 8)
    o[2] = Trunc[u8](v2 >> 8)
    o[3] = Trunc[u8](v3 >> 8)
    return true
}

// ZHuff4 decodes four Huffman streams, src[a..e1], src[e1..e2],
// src[e2..e3] and src[e3..end], into lit[0..seg], lit[seg..2 seg],
// lit[2 seg..3 seg] and lit[3 seg..regen] with the table ht of log hlog,
// interleaved as the reference's HUF_decompress4X1 does: one loop takes
// four symbols from each stream per refill while the shortest stream (the
// last, regen - 3 seg symbols) has four left, so the four bit containers
// are independent chains; then each stream's rest as ZHuffStream. Each
// stream takes exactly the steps ZHuffStream would, so the output and the
// verdict are the same; false unless every stream holds exactly its
// symbols.
ZHuff4(ht: &[2048]u16, hlog: int, src: []u8, a: int, e1: int, e2: int, e3: int, end: int, lit: mut []u8, seg: int, regen: int): bool {
    var b0 := ZBits(c: 0, used: 0, p: 0, s: 0)
    var b1 := ZBits(c: 0, used: 0, p: 0, s: 0)
    var b2 := ZBits(c: 0, used: 0, p: 0, s: 0)
    var b3 := ZBits(c: 0, used: 0, p: 0, s: 0)
    if !ZBitsInit(mut b0, src, a, e1) || !ZBitsInit(mut b1, src, e1, e2) || !ZBitsInit(mut b2, src, e2, e3) || !ZBitsInit(mut b3, src, e3, end) {
        return false
    }
    lg := Trunc[u64](hlog)
    n3 := regen - 3 * seg
    s2 := 2 * seg
    s3 := 3 * seg
    var i := 0
    for n3 - i >= 4 {
        if !ZHuff4Step(mut b0, ht, lg, src, mut lit[i..i + 4]) || !ZHuff4Step(mut b1, ht, lg, src, mut lit[seg + i..seg + i + 4]) || !ZHuff4Step(mut b2, ht, lg, src, mut lit[s2 + i..s2 + i + 4]) || !ZHuff4Step(mut b3, ht, lg, src, mut lit[s3 + i..s3 + i + 4]) {
            return false
        }
        i += 4
    }
    return ZHuffRun(mut b0, ht, lg, src, mut lit[0..seg], i) && ZHuffRun(mut b1, ht, lg, src, mut lit[seg..s2], i) && ZHuffRun(mut b2, ht, lg, src, mut lit[s2..s3], i) && ZHuffRun(mut b3, ht, lg, src, mut lit[s3..regen], i)
}

// ZLiterals decodes the literals section at src[i..e] into lit and
// returns the section's end, with the literal count in n; -1 when it is
// corrupt or regenerates more than bmax bytes.
ZLiterals(z: mut ZTabs, src: []u8, i: int, e: int, lit: mut []u8, bmax: int, n: mut int): int {
    if i >= e {
        return -1
    }
    b0 := int(src[i])
    kind := b0 & 3
    sf := (b0 >> 2) & 3
    if kind < 2 {
        var size := 0
        var j := i
        if sf & 1 == 0 {
            size = b0 >> 3
            j = i + 1
        } else if sf == 1 {
            if i + 2 > e {
                return -1
            }
            size = (b0 >> 4) + (int(src[i + 1]) << 4)
            j = i + 2
        } else {
            if i + 3 > e {
                return -1
            }
            size = (b0 >> 4) + (int(src[i + 1]) << 4) + (int(src[i + 2]) << 12)
            j = i + 3
        }
        if size > bmax {
            return -1
        }
        *n = size
        if kind == 0 {
            if j + size > e {
                return -1
            }
            (mut lit[0..size]).CopyFrom(src[j..j + size])
            return j + size
        }
        if j >= e {
            return -1
        }
        (mut lit[0..size]).Fill(src[j])
        return j + 1
    }
    var regen := 0
    var comp := 0
    var j := i
    if sf < 2 {
        if i + 3 > e {
            return -1
        }
        regen = (b0 >> 4) | ((int(src[i + 1]) & 0x3F) << 4)
        comp = (int(src[i + 1]) >> 6) | (int(src[i + 2]) << 2)
        j = i + 3
    } else if sf == 2 {
        if i + 4 > e {
            return -1
        }
        regen = (b0 >> 4) | (int(src[i + 1]) << 4) | ((int(src[i + 2]) & 3) << 12)
        comp = (int(src[i + 2]) >> 2) | (int(src[i + 3]) << 6)
        j = i + 4
    } else {
        if i + 5 > e {
            return -1
        }
        regen = (b0 >> 4) | (int(src[i + 1]) << 4) | ((int(src[i + 2]) & 0x3F) << 12)
        comp = (int(src[i + 2]) >> 6) | (int(src[i + 3]) << 2) | (int(src[i + 4]) << 10)
        j = i + 5
    }
    if regen > bmax || j + comp > e {
        return -1
    }
    end := j + comp
    var s := j
    if kind == 2 {
        k := ZHuff(z, src, j, end)
        if k < 0 {
            return -1
        }
        s = j + k
    } else if z.hlog == 0 {
        // Treeless literals with no table earlier in the frame.
        return -1
    }
    *n = regen
    if sf == 0 {
        if !ZHuffStream(&z.ht, z.hlog, src, s, end, mut lit[0..regen]) {
            return -1
        }
        return end
    }
    // Four streams after a jump table of three 2-byte sizes (RFC 8878
    // section 3.1.1.3.1.6); each regenerates (regen + 3) / 4 bytes, the
    // last what is left.
    if end - s < 6 {
        return -1
    }
    seg := (regen + 3) / 4
    if regen < 3 * seg {
        return -1
    }
    l1 := int(src[s]) | (int(src[s + 1]) << 8)
    l2 := int(src[s + 2]) | (int(src[s + 3]) << 8)
    l3 := int(src[s + 4]) | (int(src[s + 5]) << 8)
    a := s + 6
    if l1 + l2 + l3 > end - a {
        return -1
    }
    if !ZHuff4(&z.ht, z.hlog, src, a, a + l1, a + l1 + l2, a + l1 + l2 + l3, end, lit, seg, regen) {
        return -1
    }
    return end
}

// ZSetTable sets the sequence table of kind for mode m (RFC 8878 section
// 3.1.1.3.2.1: 0 predefined, 1 RLE, 2 FSE described at src[i..e], 3 the
// previous block's) into t with its log in lg, and returns the position
// after its description; -1 when it is corrupt.
ZSetTable(kind: int, m: int, src: []u8, i: int, e: int, t: mut [512]u64, lg: mut int): int {
    max_sym := if kind == ZKLl { 35 } else if kind == ZKMl { 52 } else { 31 }
    if m == 0 {
        n := if kind == ZKOf { 32 } else { 64 }
        for u in 0..n {
            t[u] = if kind == ZKLl { ZLlDef[u] } else if kind == ZKMl { ZMlDef[u] } else { ZOfDef[u] }
        }
        *lg = if kind == ZKOf { 5 } else { 6 }
        return i
    }
    if m == 1 {
        if i >= e {
            return -1
        }
        sym := int(src[i])
        if sym > max_sym {
            return -1
        }
        t[0] = ZSeqEntry(kind, sym, 0, 0)
        *lg = 0
        return i + 1
    }
    if m == 2 {
        var norm: [256]int = [0; 256]
        var l := 0
        max_log := if kind == ZKOf { 8 } else { 9 }
        k := ZNorm(src, i, e, max_sym, max_log, mut norm, mut l)
        if k < 0 {
            return -1
        }
        var ft: [512]u32 = [0; 512]
        if !ZBuild(&norm, max_sym + 1, l, mut ft[..]) {
            return -1
        }
        ZSeqTable(kind, ft[..], l, t)
        *lg = l
        return i + k
    }
    if *lg < 0 {
        return -1
    }
    return i
}

// ZCopy copies w[from..from + n] to w[to..to + n] front to back, as a
// match copy must: right when from >= to, or when to - from >= 8 (8 bytes
// a step), and for 0 < to - from < 8 by repeating the period until it is
// at least 8.
ZCopy(w: mut []u8, from: int, to: int, n: int) {
    var i := 0
    var f := from
    if f < to && to - f < 8 {
        d := to - f
        p := d * ((8 + d - 1) / d)
        k := if p > n { n } else { p }
        for i < k {
            w[to + i] = w[f + i]
            i += 1
        }
        f = to - p
    }
    for n - i >= 8 {
        x := Le64(w[..], f + i)
        PutLe64(w, to + i, x)
        i += 8
    }
    for i < n {
        w[to + i] = w[f + i]
        i += 1
    }
}

// ZBlock decodes the compressed block src[s..e] into w at op and returns
// the output's new end, or -1 when the block is corrupt. hist is the
// history before op a match may reach (at most the window), old the end
// of the window's previous segment when it has wrapped (0 when not), bmax
// the block's largest output. lit is scratch of at least bmax bytes.
// O(e - s + output); allocates nothing.
ZBlock(z: mut ZTabs, src: []u8, s: int, e: int, lit: mut []u8, w: mut []u8, op: int, hist: int, old: int, bmax: int): int {
    var nlit := 0
    var i := ZLiterals(z, src, s, e, lit, bmax, mut nlit)
    if i < 0 || i >= e {
        return -1
    }
    b0 := int(src[i])
    i += 1
    var nseq := 0
    if b0 < 128 {
        nseq = b0
    } else if b0 < 255 {
        if i >= e {
            return -1
        }
        nseq = ((b0 - 128) << 8) + int(src[i])
        i += 1
    } else {
        if i + 1 >= e {
            return -1
        }
        nseq = int(src[i]) + (int(src[i + 1]) << 8) + 0x7F00
        i += 2
    }
    if nseq == 0 {
        // No sequences, in any of the three forms: the section ends here.
        if i != e {
            return -1
        }
        (mut w[op..op + nlit]).CopyFrom(lit[0..nlit])
        return op + nlit
    }
    if i >= e {
        return -1
    }
    m := int(src[i])
    i += 1
    if m & 3 != 0 {
        return -1
    }
    i = ZSetTable(ZKLl, m >> 6, src, i, e, mut z.ll, mut z.lll)
    if i < 0 {
        return -1
    }
    i = ZSetTable(ZKOf, (m >> 4) & 3, src, i, e, mut z.of, mut z.ofl)
    if i < 0 {
        return -1
    }
    i = ZSetTable(ZKMl, (m >> 2) & 3, src, i, e, mut z.ml, mut z.mll)
    if i < 0 {
        return -1
    }
    q := ZSequences(z, src, i, e, nseq, lit[0..nlit], w, op, hist, old, bmax)
    return q
}

// ZSequences decodes and executes the nseq sequences of the bitstream
// src[i..e] with z's tables, copying literals from lit and matches from w
// (reaching into its old segment, which ends at old, when the window has
// wrapped), and then the literals left; returns the output's end, or -1.
ZSequences(z: mut ZTabs, src: []u8, i: int, e: int, nseq: int, lit: []u8, w: mut []u8, op: int, hist: int, old: int, bmax: int): int {
    var b := ZBits(c: 0, used: 0, p: 0, s: 0)
    if !ZBitsInit(mut b, src, i, e) {
        return -1
    }
    ll := &z.ll
    of := &z.of
    ml := &z.ml
    var sl := Trunc[int](ZRead(mut b, Trunc[u64](z.lll)))
    var so := Trunc[int](ZRead(mut b, Trunc[u64](z.ofl)))
    var sm := Trunc[int](ZRead(mut b, Trunc[u64](z.mll)))
    var r1 := z.rep1
    var r2 := z.rep2
    var r3 := z.rep3
    lend := lit.Len()
    limit := op + bmax
    var lp := 0
    var q := op
    for k in 0..nseq {
        if !ZReload(mut b, src) {
            return -1
        }
        eo := of[so]
        em := ml[sm]
        el := ll[sl]
        ofa := (eo >> 32) & 0xFF
        ofv := (eo & 0xFFFFFFFF) + ZRead(mut b, ofa)
        if ofa > 16 {
            if !ZReload(mut b, src) {
                return -1
            }
        }
        mlv := (em & 0xFFFFFFFF) + ZRead(mut b, (em >> 32) & 0xFF)
        llv := (el & 0xFFFFFFFF) + ZRead(mut b, (el >> 32) & 0xFF)
        var off := 0
        if ofa > 1 {
            off = Trunc[int](ofv) - 3
            r3 = r2
            r2 = r1
            r1 = off
        } else {
            var v := Trunc[int](ofv)
            if llv == 0 {
                v += 1
            }
            if v == 1 {
                off = r1
            } else if v == 2 {
                off = r2
                r2 = r1
                r1 = off
            } else if v == 3 {
                off = r3
                r3 = r2
                r2 = r1
                r1 = off
            } else {
                off = r1 - 1
                r3 = r2
                r2 = r1
                r1 = off
            }
        }
        if k + 1 < nseq {
            if !ZReload(mut b, src) {
                return -1
            }
            sl = Trunc[int](el >> 48) + Trunc[int](ZRead(mut b, (el >> 40) & 0xFF))
            sm = Trunc[int](em >> 48) + Trunc[int](ZRead(mut b, (em >> 40) & 0xFF))
            so = Trunc[int](eo >> 48) + Trunc[int](ZRead(mut b, (eo >> 40) & 0xFF))
        }
        nl := Trunc[int](llv)
        nm := Trunc[int](mlv)
        if nl > lend - lp || nm > limit - q - nl {
            return -1
        }
        (mut w[q..q + nl]).CopyFrom(lit[lp..lp + nl])
        q += nl
        lp += nl
        if off <= 0 || off > hist + (q - op) {
            return -1
        }
        if off <= q {
            ZCopy(w, q - off, q, nm)
        } else {
            // The match starts in the old segment and may run on into this
            // one, which starts at 0 (zstd.mk keeps the old segment's last
            // hist bytes intact while this one is written).
            from := old - (off - q)
            k1 := if nm > off - q { off - q } else { nm }
            ZCopy(w, from, q, k1)
            if nm > k1 {
                ZCopy(w, 0, q + k1, nm - k1)
            }
        }
        q += nm
    }
    rest := lend - lp
    if rest > limit - q {
        return -1
    }
    (mut w[q..q + rest]).CopyFrom(lit[lp..lend])
    q += rest
    if !ZReload(mut b, src) || !ZDone(&b) {
        return -1
    }
    z.rep1 = r1
    z.rep2 = r2
    z.rep3 = r3
    return q
}
