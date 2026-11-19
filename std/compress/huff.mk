// Why: inflating is mostly Huffman decoding, so the table layout decides
// the decoder's speed: one table load must give a whole decode step.
//
// What: Build (the decode tables of RFC 1951 section 3.2 for a code-length
// list), SymEntry, Rev, FixedLitTable and FixedDistTable, used by Inflate,
// Gunzip and Inflater.
//
// How: laid out as libdeflate's (Eric Biggers, deflate_decompress.c) and
// zlib's inflate_table: a main table indexed by the next root bits of the
// stream, and for codes longer than root a subtable per root-bit prefix,
// sized by zlib's rule so that the litlen table never needs more than 2342
// entries (zlib's `enough 288 11 15`) and the distance table 402
// (`enough 32 8 15`). One u32 entry is the whole decode step:
//   bits 0 to 3   the code length L to consume (for a subtable pointer: root)
//   bits 4 to 7   the extra-bit count of a length or distance (for a
//                 subtable pointer: the subtable's index bits)
//   bits 8 to 10  the kind (KLit, KLen, KEnd, KSub, KBad)
//   bits 16 to 31 the value: the literal byte, the base length or distance,
//                 the code-length symbol, or the subtable's first index

// The kinds of an entry (bits 8 to 10).
const KLit: u32 = 0
const KLen: u32 = 1
const KEnd: u32 = 2
const KSub: u32 = 3
const KBad: u32 = 4

// The table sizes: main tables of 2^11 (litlen), 2^8 (distance) and 2^7
// (code-length code, whose codes are at most 7 bits, so it has no
// subtables), each with room for its largest subtable set.
const LitBits = 11
const DistBits = 8
const PreBits = 7
const LitSize = 2342
const DistSize = 402
const PreSize = 128

// Which code a table decodes: the entry a symbol gets depends on it.
const WLit = 0
const WDist = 1
const WPre = 2

// RFC 1951 section 3.2.5: base lengths and extra bits of symbols 257 to
// 285, and base distances and extra bits of distance codes 0 to 29.
const LenBase: [29]u32 = [3, 4, 5, 6, 7, 8, 9, 10, 11, 13, 15, 17, 19, 23, 27, 31, 35, 43, 51, 59, 67, 83, 99, 115, 131, 163, 195, 227, 258]
const LenExtra: [29]u32 = [0, 0, 0, 0, 0, 0, 0, 0, 1, 1, 1, 1, 2, 2, 2, 2, 3, 3, 3, 3, 4, 4, 4, 4, 5, 5, 5, 5, 0]
const DistBase: [30]u32 = [1, 2, 3, 4, 5, 7, 9, 13, 17, 25, 33, 49, 65, 97, 129, 193, 257, 385, 513, 769, 1025, 1537, 2049, 3073, 4097, 6145, 8193, 12289, 16385, 24577]
const DistExtra: [30]u32 = [0, 0, 0, 0, 1, 1, 2, 2, 3, 3, 4, 4, 5, 5, 6, 6, 7, 7, 8, 8, 9, 9, 10, 10, 11, 11, 12, 12, 13, 13]

// PreOrder is the order of the code-length code's lengths in a dynamic
// block header (section 3.2.7).
const PreOrder: [19]int = [16, 17, 18, 0, 8, 7, 9, 6, 10, 5, 11, 4, 12, 3, 13, 2, 14, 1, 15]

// SymEntry is the entry of symbol s, with code length l, in a table of
// kind which. Symbols 286 and 287 and distance codes 30 and 31 take part
// in the code but are invalid when decoded (section 3.2.6), as in Go's
// compress/flate.
SymEntry(s: int, l: int, which: int): u32 {
    ul := u32(Trunc[u8](l))
    if which == WPre {
        return (u32(Trunc[u16](s)) << 16) | (KLit << 8) | ul
    }
    if which == WDist {
        if s < 30 {
            return (DistBase[s] << 16) | (DistExtra[s] << 4) | (KLen << 8) | ul
        }
        return (KBad << 8) | ul
    }
    if s < 256 {
        return (u32(Trunc[u16](s)) << 16) | (KLit << 8) | ul
    }
    if s == 256 {
        return (KEnd << 8) | ul
    }
    if s < 286 {
        return (LenBase[s - 257] << 16) | (LenExtra[s - 257] << 4) | (KLen << 8) | ul
    }
    return (KBad << 8) | ul
}

// Rev is the low l bits of code in reverse order: a canonical code is
// defined MSB first and read LSB first (section 3.1.1).
Rev(code: int, l: int): int {
    var r := 0
    var c := code
    for _ in 0..l {
        r = (r << 1) | (c & 1)
        c = c >> 1
    }
    return r
}

// Build fills t with the decode table of the code whose lengths are lens
// (0 for an unused symbol), main table of 2^root entries first. It returns
// false for a code that is over-subscribed, or incomplete other than the
// degenerate code of one symbol of length 1; Go's compress/flate accepts
// exactly the same codes. An empty code (every length 0) is accepted, and
// every lookup in it is KBad, so it fails only when it is used. O(2^root +
// subtables + lens.Len()); no allocation.
Build(t: mut []u32, lens: []u8, root: int, which: int): bool {
    n := lens.Len()
    var count: [16]int = [0; 16]
    var max := 0
    for s in 0..n {
        l := int(lens[s])
        count[l] += 1
        if l > max {
            max = l
        }
    }
    size := 1 << root
    // A bad entry gives the bits that decide it: none for an empty code,
    // whose every lookup is corrupt (as Go's compress/flate decides at
    // once), and one for the unused half of a one-code code, so that with
    // no bit left to read the caller reports a truncated stream.
    bad := (KBad << 8) | 1
    if max == 0 {
        (mut t[0..size]).Fill(KBad << 8)
        return true
    }
    var left := 1
    for l in 1..16 {
        left = (left << 1) - count[l]
        if left < 0 {
            return false
        }
    }
    if left > 0 {
        if !(max == 1 && count[1] == 1) {
            return false
        }
        (mut t[0..size]).Fill(bad)
    }
    // Symbols sorted by (length, symbol): the canonical order.
    var offs: [17]int = [0; 17]
    for l in 1..16 {
        offs[l + 1] = offs[l] + count[l]
    }
    var sorted: [320]u16 = [0; 320]
    for s in 0..n {
        l := int(lens[s])
        if l != 0 {
            sorted[offs[l]] = Trunc[u16](s)
            offs[l] += 1
        }
    }
    // rem[l] is the number of codes of length l not placed yet; zlib's rule
    // sizes a subtable from them.
    var rem := count
    var code := 0
    var next := size
    var prefix := -1
    var sub := 0
    var sub_bits := 0
    var k := 0
    mask := size - 1
    for l in 1..max + 1 {
        for _ in 0..count[l] {
            s := int(sorted[k])
            k += 1
            e := SymEntry(s, l, which)
            r := Rev(code, l)
            if l <= root {
                var j := r
                step := 1 << l
                for j < size {
                    t[j] = e
                    j += step
                }
            } else {
                p := r & mask
                if p != prefix {
                    var cur := l - root
                    var room := 1 << cur
                    for cur + root < max {
                        room -= rem[cur + root]
                        if room <= 0 {
                            break
                        }
                        cur += 1
                        room = room << 1
                    }
                    prefix = p
                    sub = next
                    sub_bits = cur
                    next += 1 << cur
                    t[p] = (u32(Trunc[u16](sub)) << 16) | (KSub << 8) | (u32(Trunc[u8](cur)) << 4) | u32(Trunc[u8](root))
                }
                var j := r >> root
                step := 1 << (l - root)
                sub_size := 1 << sub_bits
                for j < sub_size {
                    t[sub + j] = e
                    j += step
                }
            }
            rem[l] -= 1
            code += 1
        }
        code = code << 1
    }
    return true
}

// FixedLitTable is the litlen table of a fixed-code block (section
// 3.2.6): lengths 8, 9, 7 and 8 for symbols 0 to 143, 144 to 255, 256 to
// 279 and 280 to 287.
FixedLitTable(): [2342]u32 {
    var lens: [288]u8 = [8; 288]
    (mut lens[144..256]).Fill(9)
    (mut lens[256..280]).Fill(7)
    var t: [2342]u32 = [0; 2342]
    _ = Build(mut t[..], lens[..], LitBits, WLit)
    return t
}

// FixedDistTable: 32 distance codes of length 5.
FixedDistTable(): [402]u32 {
    lens: [32]u8 = [5; 32]
    var t: [402]u32 = [0; 402]
    _ = Build(mut t[..], lens[..], DistBits, WDist)
    return t
}

// The fixed tables, computed at compile time.
const FixedLit: [2342]u32 = FixedLitTable()
const FixedDist: [402]u32 = FixedDistTable()
