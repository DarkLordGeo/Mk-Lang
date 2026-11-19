// Why: compressing a whole buffer in memory needs no window that slides,
// which makes a faster and simpler encoder than the streaming one.
//
// What: compress.Deflate and compress.Gzip, with the matcher (Scan), the
// block writer (FlushBlock, WriteStored, WriteSyms, WriteHeader) and
// DeflateBound.
//
// How: one level, the matcher of Go 1.27's compress/flate level 6
// (klauspost's fastEncL6, the encoder Go's DefaultCompression runs): no hash
// chains, but a table of 4-byte hashes holding the last position and a table
// of 7-byte hashes holding the last two, a repeat-distance check, a look one
// position ahead, a look for a longer match ending where this one ends,
// backward extension, and literal runs scanned with a growing stride. The
// tables hold absolute positions as u32 offsets from a base (Rebase). Every
// block of BlockSyms symbols is sent as the smallest of stored, fixed and
// dynamic, counted in exact bits, so the output is never larger than the
// stored form (DeflateBound).

import std.hash
import std.raw

const WSize = 32768
const TSize = 32768
const Margin = 11
const SkipLog = 7
const MaxLong = 1048576
const BlockSyms = 16384

// The tables hold positions as u32, pos - base + WSize, so an empty entry
// (0) decodes to a position a window or more before any real one and is
// refused by the distance check without a test of its own. When nextS -
// base reaches RebaseAt, every entry drops by RebaseStep (entries older
// than that become empty: they are far past the window), and base rises
// by it, so any input length works with 4-byte entries. A step of the
// scanner moves at most MaxLong + 258 past the last check, so entries
// stay below 2^32.
const RebaseAt = 2147483648
const RebaseStep = 1073741824

// Enc is the bit writer (acc holds cnt bits not yet stored, q the next
// output byte) and the current block: its symbol frequencies, its symbol
// count and its first input byte.
struct Enc {
    acc: u64
    cnt: u64
    q: int
    litf: [286]int
    distf: [30]int
    nsym: int
    bstart: int
}

// DeflateBound is the most Deflate writes for n bytes: every block is at
// most its stored form, 42 bits of header per 65,535 bytes or part, and
// a block holds at least 16,383 input bytes unless it is the last, so the
// headers fit in n / 2048 + 32 bytes; 32 more cover the bit writer's
// 4-byte stores past the end.
DeflateBound(n: int): int {
    return n + (n >> 11) + 64
}

// Bits appends the low k bits of v (k at most 32).
Bits(e: mut Enc, out: mut []u8, v: u64, k: u64) {
    e.acc = e.acc | (v << e.cnt)
    e.cnt += k
    if e.cnt >= 32 {
        PutLe32(out, e.q, Trunc[u32](e.acc))
        e.q += 4
        e.acc = e.acc >> 32
        e.cnt -= 32
    }
}

// ByteAlign pads the bit writer to a byte boundary and stores every held
// byte, so e.q is where the next byte goes.
ByteAlign(e: mut Enc, out: mut []u8) {
    e.cnt = (e.cnt + 7) & 0xF8
    for e.cnt > 0 {
        out[e.q] = Trunc[u8](e.acc)
        e.q += 1
        e.acc = e.acc >> 8
        e.cnt -= 8
    }
    e.acc = 0
}

// PutLe32 stores x little-endian at out[i..i + 4].
PutLe32(out: mut []u8, i: int, x: u32) {
    t := mut out[i..out.Len()]
    w := mut t[0..4]
    w[0] = Trunc[u8](x)
    w[1] = Trunc[u8](x >> 8)
    w[2] = Trunc[u8](x >> 16)
    w[3] = Trunc[u8](x >> 24)
}

// HashS hashes the low 4 bytes of cv into 15 bits, HashL its low 7 bytes:
// Go's hashLen with prime4bytes and prime7bytes (multiplicative hashes,
// the top bits of the product).
HashS(cv: u64): int {
    return int((Trunc[u32](cv) *% 0x9E3779B1) >> 17)
}

HashL(cv: u64): int {
    return Trunc[int](((cv << 8) *% 58295818150454627) >> 49)
}

// Common is the length of the common prefix of data at a and at b (a <
// b), at most max, where b + max <= data.Len(): 8 bytes a step, the
// first difference found by trailing zeros.
Common(data: []u8, a: int, b: int, max: int): int {
    var k := 0
    for k + 8 <= max {
        x := Le64(data, a + k) ^ Le64(data, b + k)
        if x != 0 {
            return k + raw.Ctz64(x) / 8
        }
        k += 8
    }
    for k < max && data[a + k] == data[b + k] {
        k += 1
    }
    return k
}

// MatchLim is the match length at s against t < s, at most 254 (a match
// found 4 bytes in, so 258 in all): Go's matchLenLimited.
MatchLim(data: []u8, s: int, t: int): int {
    max := if data.Len() - s > 254 { 254 } else { data.Len() - s }
    return Common(data, t, s, max)
}

// MatchLong is the match length at s against t < s, at most MaxLong: Go's
// matchLenLong, bounded so that the tables' entries stay below 2^32.
MatchLong(data: []u8, s: int, t: int): int {
    max := if data.Len() - s > MaxLong { MaxLong } else { data.Len() - s }
    return Common(data, t, s, max)
}

// Lits adds the literals data[from..to], sending the block when it fills.
Lits(e: mut Enc, out: mut []u8, data: []u8, syms: mut []u32, from: int, to: int) {
    var p := from
    for p < to {
        c := data[p]
        e.litf[int(c)] += 1
        syms[e.nsym] = u32(c)
        e.nsym += 1
        p += 1
        if e.nsym == BlockSyms {
            FlushBlock(e, out, data, syms[..], p, false)
        }
    }
}

// Match adds a match of l >= 4 bytes at s, distance dist, as matches of
// at most 258 bytes, none shorter than 3 (Go's AddMatchLong), sending the
// block when it fills.
Match(e: mut Enc, out: mut []u8, data: []u8, syms: mut []u32, s: int, l: int, dist: int) {
    dc := DistCode(dist)
    dw := Cast[u32](dist) << 8
    var p := s
    var left := l
    for left > 0 {
        // 259 to 261 bytes go as 255 and a tail of at least 4, never a
        // tail of 1 or 2, which no symbol codes.
        k := if left <= 258 { left } else if left > 261 { 258 } else { 255 }
        left -= k
        syms[e.nsym] = dw | Cast[u32](k - 3)
        e.nsym += 1
        e.litf[257 + int(LenSym[k - 3])] += 1
        e.distf[dc] += 1
        p += k
        if e.nsym == BlockSyms {
            FlushBlock(e, out, data, syms[..], p, false)
        }
    }
}

// Scan is the matcher (Go's fastEncL6.encode, over data[start..], which is
// at least 13 bytes, with data[0..start] as history): it adds the symbols
// for data[start..r] and returns r, leaving the rest for literals. st is
// the 4-byte table, lc and lp the last and the one-before-last positions
// of the 7-byte table, each entry a position p as p - tbase + WSize (all
// zeros at the start of a stream); tbase is updated by Rebase. No entry
// names a position at or past start but one Scan wrote, and an entry for a
// position before 0 (history slid out, Encoder) fails the distance check.
Scan(e: mut Enc, out: mut []u8, data: []u8, start: int, tbase: mut int, st: mut []u32, lc: mut []u32, lp: mut []u32, syms: mut []u32): int {
    n := data.Len()
    slimit := n - Margin
    var base := *tbase
    var s := start
    var emit := start
    var repeat := 1
    var cv := Le64(data, start)
    outer: for {
        var nexts := s
        var l := 0
        var t := 0
        for {
            var hs := HashS(cv)
            var hl := HashL(cv)
            s = nexts
            nexts = s + 1 + ((s - emit) >> SkipLog)
            if nexts > slimit {
                break outer
            }
            if nexts - base >= RebaseAt {
                Rebase(st, lc, lp)
                base += RebaseStep
            }
            sc := st[hs]
            lcur := lc[hl]
            lprev := lp[hl]
            next := Le64(data, nexts)
            ent := Trunc[u32](s - base + WSize)
            st[hs] = ent
            lp[hl] = lcur
            lc[hl] = ent
            hs = HashS(next)
            hl = HashL(next)
            nent := Trunc[u32](nexts - base + WSize)
            c4 := Trunc[u32](cv)
            t = base + int(lcur) - WSize
            if s - t < WSize {
                if c4 == Le32(data, t) {
                    // The long candidate matches 4 bytes; try the one
                    // before it too.
                    st[hs] = nent
                    lp[hl] = lc[hl]
                    lc[hl] = nent
                    t2 := base + int(lprev) - WSize
                    if s - t2 < WSize && c4 == Le32(data, t2) {
                        l = MatchLim(data, s + 4, t + 4) + 4
                        l1 := MatchLim(data, s + 4, t2 + 4) + 4
                        if l1 > l {
                            t = t2
                            l = l1
                        }
                    }
                    break
                }
                t = base + int(lprev) - WSize
                if s - t < WSize && c4 == Le32(data, t) {
                    st[hs] = nent
                    lp[hl] = lc[hl]
                    lc[hl] = nent
                    break
                }
            }
            t = base + int(sc) - WSize
            if s - t < WSize && c4 == Le32(data, t) {
                // A 4-byte match from the short table: try the last
                // distance one byte on, then the long candidates of nexts.
                l = MatchLim(data, s + 4, t + 4) + 4
                ncur := lc[hl]
                nprev := lp[hl]
                st[hs] = nent
                lp[hl] = ncur
                lc[hl] = nent
                t2 := s - repeat + 1
                if Le32(data, t2) == Trunc[u32](cv >> 8) {
                    ml := MatchLim(data, s + 5, t2 + 4) + 4
                    if ml > l {
                        t = t2
                        l = ml
                        s += 1
                        break
                    }
                }
                t3 := base + int(ncur) - WSize
                if nexts - t3 < WSize {
                    if Le32(data, t3) == Trunc[u32](next) {
                        ml := MatchLim(data, nexts + 4, t3 + 4) + 4
                        if ml > l {
                            t = t3
                            s = nexts
                            l = ml
                        }
                    }
                    t4 := base + int(nprev) - WSize
                    if nexts - t4 < WSize && Le32(data, t4) == Trunc[u32](next) {
                        ml := MatchLim(data, nexts + 4, t4 + 4) + 4
                        if ml > l {
                            t = t4
                            s = nexts
                            l = ml
                            break
                        }
                    }
                }
                break
            }
            cv = next
        }
        if l == 0 {
            l = MatchLong(data, s + 4, t + 4) + 4
        } else if l == 258 {
            l += MatchLong(data, s + l, t + l)
        }
        // A longer match may end where this one ends: look up the 7-byte
        // hash at the end, allowing the first 2 bytes to differ (the
        // backward extension takes them back when they match).
        at := s + l
        if at < slimit {
            hh := HashL(Le64(data, at))
            s2 := s + 2
            var t2 := base + int(lc[hh]) - WSize - l + 2
            if s2 - t2 < WSize {
                if s2 - t2 > 0 && t2 >= 0 {
                    l2 := MatchLong(data, s2, t2)
                    if l2 > l {
                        t = t2
                        l = l2
                        s = s2
                    }
                }
                t2 = base + int(lp[hh]) - WSize - l + 2
                if s2 - t2 > 0 && s2 - t2 < WSize && t2 >= 0 {
                    l2 := MatchLong(data, s2, t2)
                    if l2 > l {
                        t = t2
                        l = l2
                        s = s2
                    }
                }
            }
        }
        for t > 0 && s > emit && data[t - 1] == data[s - 1] {
            s -= 1
            t -= 1
            l += 1
        }
        Lits(e, out, data, syms, emit, s)
        Match(e, out, data, syms, s, l, s - t)
        repeat = s - t
        s += l
        emit = s
        if nexts >= s {
            s = nexts + 1
        }
        if s >= slimit {
            break
        }
        // Every long hash in the match and every second short one.
        var i := nexts + 1
        for i < s - 1 {
            x := Le64(data, i)
            v := Trunc[u32](i - base + WSize)
            h1 := HashL(x)
            h2 := HashL(x >> 8)
            st[HashS(x)] = v
            lp[h1] = lc[h1]
            lc[h1] = v
            lp[h2] = lc[h2]
            lc[h2] = v + 1
            i += 2
        }
        cv = Le64(data, s)
    }
    *tbase = base
    return emit
}

// Compress writes data as a complete deflate stream into out from q0 and
// returns the offset after it. st, lc and lp are TSize zeros; syms holds
// BlockSyms symbols.
Compress(data: []u8, out: mut []u8, q0: int, st: mut []u32, lc: mut []u32, lp: mut []u32, syms: mut []u32): int {
    n := data.Len()
    var e := Enc(acc: 0, cnt: 0, q: q0, litf: [0; 286], distf: [0; 30], nsym: 0, bstart: 0)
    var base := 0
    r := if n >= 13 { Scan(mut e, out, data, 0, mut base, st, lc, lp, syms) } else { 0 }
    Lits(mut e, out, data, syms, r, n)
    FlushBlock(mut e, out, data, syms[..], n, true)
    ByteAlign(mut e, out)
    return e.q
}

// FlushBlock sends the block of e's symbols, which covers data[e.bstart
// ..end], as the smallest of its three forms, and starts the next block.
FlushBlock(e: mut Enc, out: mut []u8, data: []u8, syms: []u32, end: int, final: bool) {
    e.litf[256] = 1
    var llen: [286]u8 = [0; 286]
    var dlen: [30]u8 = [0; 30]
    Lengths(e.litf[..], mut llen[..], 15)
    Lengths(e.distf[..], mut dlen[..], 15)
    var xbits := 0
    var dyn := 0
    var fixed := 0
    for s in 0..286 {
        f := e.litf[s]
        dyn += f * int(llen[s])
        fixed += f * int(FixedLens[s])
    }
    for i in 0..29 {
        xbits += e.litf[257 + i] * int(LenExtra[i])
    }
    for i in 0..30 {
        f := e.distf[i]
        xbits += f * int(DistExtra[i])
        dyn += f * int(dlen[i])
        fixed += f * 5
    }
    var h := HeaderPlan(llen: llen, dlen: dlen, nlit: 0, ndist: 0, rsym: [0; 316], rext: [0; 316], nr: 0,
        plen: [0; 19], pcode: [0; 19], ncl: 0, bits: 0)
    Plan(mut h)
    dyn += 3 + h.bits + xbits
    fixed += 3 + xbits
    raw := end - e.bstart
    stored := 42 * (raw / 65535 + 1) + 8 * raw
    fin: u64 = if final { 1 } else { 0 }
    if stored <= dyn && stored <= fixed {
        WriteStored(e, out, data[e.bstart..end], final)
    } else if fixed <= dyn {
        Bits(e, out, fin | (1 << 1), 3)
        WriteSyms(e, out, syms[0..e.nsym], FixedCodes[..], FixedLens[..], FixedDistCodes[..], FixedDistLens[..])
    } else {
        Bits(e, out, fin | (2 << 1), 3)
        WriteHeader(e, out, &h)
        var lc: [286]u16 = [0; 286]
        var dc: [30]u16 = [0; 30]
        Codes(llen[..], mut lc[..])
        Codes(dlen[..], mut dc[..])
        WriteSyms(e, out, syms[0..e.nsym], lc[..], llen[..], dc[..], dlen[..])
    }
    (mut e.litf[..]).Fill(0)
    (mut e.distf[..]).Fill(0)
    e.nsym = 0
    e.bstart = end
}

// WriteStored sends b as stored blocks of at most 65,535 bytes (one, empty,
// when b is), the last one final when final is set.
WriteStored(e: mut Enc, out: mut []u8, b: []u8, final: bool) {
    n := b.Len()
    var p := 0
    for {
        k := if n - p > 65535 { 65535 } else { n - p }
        head: u64 = if final && p + k == n { 1 } else { 0 }
        Bits(e, out, head, 3)
        ByteAlign(e, out)
        q := e.q
        out[q] = Trunc[u8](k)
        out[q + 1] = Trunc[u8](k >> 8)
        out[q + 2] = Trunc[u8](k) ^ 0xFF
        out[q + 3] = Trunc[u8](k >> 8) ^ 0xFF
        (mut out[q + 4..q + 4 + k]).CopyFrom(b[p..p + k])
        e.q = q + 4 + k
        p += k
        if p == n {
            return
        }
    }
}

// WriteSyms sends the symbols with the given codes, then end of block.
WriteSyms(e: mut Enc, out: mut []u8, syms: []u32, lc: []u16, ll: []u8, dc: []u16, dl: []u8) {
    // The bit writer in locals: a literal adds at most 15 bits and each
    // half of a match at most 28, so with fewer than 32 bits held before
    // an add the 64-bit buffer never overflows.
    var acc := e.acc
    var cnt := e.cnt
    var q := e.q
    for s in syms {
        if s >> 8 == 0 {
            i := Trunc[int](s)
            acc = acc | (u64(lc[i]) << cnt)
            cnt += u64(ll[i])
        } else {
            l3 := Trunc[int](s & 0xFF)
            dist := Trunc[int](s >> 8)
            c := int(LenSym[l3])
            acc = acc | (u64(lc[257 + c]) << cnt)
            cnt += u64(ll[257 + c])
            acc = acc | (Cast[u64](l3 + 3 - int(LenBase[c])) << cnt)
            cnt += u64(LenExtra[c])
            if cnt >= 32 {
                PutLe32(out, q, Trunc[u32](acc))
                q += 4
                acc = acc >> 32
                cnt -= 32
            }
            d := DistCode(dist)
            acc = acc | (u64(dc[d]) << cnt)
            cnt += u64(dl[d])
            acc = acc | (Cast[u64](dist - int(DistBase[d])) << cnt)
            cnt += u64(DistExtra[d])
        }
        if cnt >= 32 {
            PutLe32(out, q, Trunc[u32](acc))
            q += 4
            acc = acc >> 32
            cnt -= 32
        }
    }
    e.acc = acc
    e.cnt = cnt
    e.q = q
    Bits(e, out, u64(lc[256]), u64(ll[256]))
}

// HeaderPlan is a dynamic block's header (section 3.2.7): the counts, the
// code lengths run-length coded into code-length symbols rsym with extra
// values rext, the code-length code, and the header's size in bits.
struct HeaderPlan {
    llen: [286]u8
    dlen: [30]u8
    nlit: int
    ndist: int
    rsym: [316]u8
    rext: [316]u8
    nr: int
    plen: [19]u8
    pcode: [19]u16
    ncl: int
    bits: int
}

// Plan fills h from h.llen and h.dlen: zlib's scan_tree and send_tree
// run-length coding (16 repeats the previous length 3 to 6 times, 17 and
// 18 give 3 to 10 and 11 to 138 zeros), over the litlen and distance
// lengths as one sequence, which section 3.2.7 allows.
Plan(h: mut HeaderPlan) {
    var nlit := 286
    for nlit > 257 && h.llen[nlit - 1] == 0 {
        nlit -= 1
    }
    var ndist := 30
    for ndist > 1 && h.dlen[ndist - 1] == 0 {
        ndist -= 1
    }
    h.nlit = nlit
    h.ndist = ndist
    total := nlit + ndist
    var seq: [316]u8 = [0; 316]
    (mut seq[0..nlit]).CopyFrom(h.llen[0..nlit])
    (mut seq[nlit..total]).CopyFrom(h.dlen[0..ndist])
    var nr := 0
    var i := 0
    for i < total {
        v := seq[i]
        var r := 1
        for i + r < total && seq[i + r] == v {
            r += 1
        }
        i += r
        if v == 0 {
            for r >= 11 {
                k := if r > 138 { 138 } else { r }
                h.rsym[nr] = 18
                h.rext[nr] = Trunc[u8](k - 11)
                nr += 1
                r -= k
            }
            if r >= 3 {
                h.rsym[nr] = 17
                h.rext[nr] = Trunc[u8](r - 3)
                nr += 1
                r = 0
            }
        } else {
            h.rsym[nr] = v
            h.rext[nr] = 0
            nr += 1
            r -= 1
            for r >= 3 {
                k := if r > 6 { 6 } else { r }
                h.rsym[nr] = 16
                h.rext[nr] = Trunc[u8](k - 3)
                nr += 1
                r -= k
            }
        }
        for r > 0 {
            h.rsym[nr] = v
            h.rext[nr] = 0
            nr += 1
            r -= 1
        }
    }
    h.nr = nr
    var pf: [19]int = [0; 19]
    for j in 0..nr {
        pf[int(h.rsym[j])] += 1
    }
    Lengths(pf[..], mut h.plen[..], 7)
    Codes(h.plen[..], mut h.pcode[..])
    var ncl := 19
    for ncl > 4 && h.plen[PreOrder[ncl - 1]] == 0 {
        ncl -= 1
    }
    h.ncl = ncl
    var bits := 14 + 3 * ncl
    for j in 0..nr {
        sym := int(h.rsym[j])
        bits += int(h.plen[sym]) + int(RepeatExtra(sym))
    }
    h.bits = bits
}

// RepeatExtra is the number of extra bits after code-length symbol sym
// (RFC 1951 3.2.7): 2 after 16, 3 after 17, 7 after 18, none after a length.
RepeatExtra(sym: int): u8 {
    return match sym {
        16 { 2 }
        17 { 3 }
        18 { 7 }
        _ { 0 }
    }
}

// WriteHeader sends the header h describes (after the 3 block-type bits).
WriteHeader(e: mut Enc, out: mut []u8, h: &HeaderPlan) {
    Bits(e, out, Cast[u64](h.nlit - 257), 5)
    Bits(e, out, Cast[u64](h.ndist - 1), 5)
    Bits(e, out, Cast[u64](h.ncl - 4), 4)
    for i in 0..h.ncl {
        Bits(e, out, u64(h.plen[PreOrder[i]]), 3)
    }
    for j in 0..h.nr {
        sym := int(h.rsym[j])
        Bits(e, out, u64(h.pcode[sym]), u64(h.plen[sym]))
        if sym >= 16 {
            Bits(e, out, u64(h.rext[j]), u64(RepeatExtra(sym)))
        }
    }
}

// Rebase lowers every entry of the three tables by RebaseStep.
Rebase(st: mut []u32, lc: mut []u32, lp: mut []u32) {
    for i in 0..st.Len() {
        st[i] = Rebased(st[i])
    }
    for i in 0..lc.Len() {
        lc[i] = Rebased(lc[i])
    }
    for i in 0..lp.Len() {
        lp[i] = Rebased(lp[i])
    }
}

// Rebased is table entry x lowered by RebaseStep, or 0 (empty) when it
// falls out of the window.
Rebased(x: u32): u32 {
    step := Trunc[u32](RebaseStep)
    return if x > step { x - step } else { 0 }
}

// DeflateInto compresses data into out from q, with its scratch (three
// tables and the symbols: 448 KiB) from a sub-arena of a that it frees
// before it returns, so a keeps only the output.
DeflateInto(a: &Arena, data: []u8, out: mut []u8, q: int): OutOfMemory!int {
    var s := try a.Sub()
    defer s.Free()
    st := try s.Slice[u32](TSize)
    lc := try s.Slice[u32](TSize)
    lp := try s.Slice[u32](TSize)
    syms := try s.Slice[u32](BlockSyms)
    return Compress(data, out, q, st, lc, lp, syms)
}

/// Compresses data as one raw DEFLATE stream (RFC 1951). There is one
/// compression level. No block is larger than its stored form, so the
/// output is at most n + n / 2048 + 64 bytes for n bytes of input.
///
/// Fails only with OutOfMemory. O(n); allocates that bound in a for the
/// output, and 448 KiB of scratch in a sub-arena of a, freed before it
/// returns.
export Deflate(a: &Arena, data: []u8): OutOfMemory![]u8 from a {
    out := try a.Slice[u8](DeflateBound(data.Len()))
    q := try DeflateInto(a, data, out, 0)
    return out[0..q]
}

/// Compresses data as one gzip member (RFC 1952): a 10-byte header with
/// mtime 0, XFL 0 and OS 255 (unknown), Deflate's stream, then the CRC-32
/// and the length mod 2^32.
///
/// Fails only with OutOfMemory. O(n); allocates as Deflate does, plus 18
/// bytes.
export Gzip(a: &Arena, data: []u8): OutOfMemory![]u8 from a {
    out := try a.Slice[u8](DeflateBound(data.Len()) + 18)
    out[0] = 0x1F
    out[1] = 0x8B
    out[2] = 8
    out[9] = 0xFF
    q := try DeflateInto(a, data, out, 10)
    PutLe32(out, q, hash.Crc32(data))
    PutLe32(out, q + 4, Trunc[u32](data.Len()))
    return out[0..q + 8]
}
