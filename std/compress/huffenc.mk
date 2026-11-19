// Why: Deflate and Gzip must send length-limited Huffman codes that are
// optimal for the block's symbol counts and always complete.
//
// What: MinRedundancy, Lengths and Codes, plus the fixed-block and symbol
// tables (FixedLensTable, FixedCodesTable, FixedDistCodesTable, LenSymTable,
// DistSymTable) and DistCode.
//
// How: code lengths by Moffat and Katajainen's in-place algorithm ("In-Place
// Calculation of Minimum-Redundancy Codes", 1995) over the frequencies
// sorted ascending, limited to the format's maximum by miniz's Kraft-sum
// repair (tdefl_huffman_enforce_max_code_size); at least two symbols get a
// code, as zlib forces, so every code sent is complete. Canonical codes by
// RFC 1951 section 3.2.2, bit-reversed for the LSB-first bit writer.

// MinRedundancy turns w, weights in ascending order (at least two), into
// the code lengths of an optimal prefix code, in place: w[i] becomes the
// length of the i-th lightest symbol. O(n), no allocation.
MinRedundancy(w: mut []int) {
    n := w.Len()
    w[0] += w[1]
    var root := 0
    var leaf := 2
    for next in 1..n - 1 {
        if leaf >= n || w[root] < w[leaf] {
            w[next] = w[root]
            w[root] = next
            root += 1
        } else {
            w[next] = w[leaf]
            leaf += 1
        }
        if leaf >= n || (root < next && w[root] < w[leaf]) {
            w[next] += w[root]
            w[root] = next
            root += 1
        } else {
            w[next] += w[leaf]
            leaf += 1
        }
    }
    w[n - 2] = 0
    var j := n - 3
    for j >= 0 {
        w[j] = w[w[j]] + 1
        j -= 1
    }
    var avbl := 1
    var used := 0
    var depth := 0
    var r := n - 2
    var nx := n - 1
    for avbl > 0 {
        for r >= 0 && w[r] == depth {
            used += 1
            r -= 1
        }
        for avbl > used {
            w[nx] = depth
            nx -= 1
            avbl -= 1
        }
        avbl = 2 * used
        depth += 1
        used = 0
    }
}

// Lengths sets lens to code lengths of at most limit bits for freq (both
// of one length, at most 288): zero for an unused symbol, except that
// when fewer than two symbols are used the first unused ones get a code
// too. O(n log n) for the sort; no allocation.
Lengths(freq: []int, lens: mut []u8, limit: int) {
    n := freq.Len()
    var keys: [288]u64 = [0; 288]
    var m := 0
    for s in 0..n {
        lens[s] = 0
        if freq[s] > 0 {
            keys[m] = (Cast[u64](freq[s]) << 16) | Cast[u64](s)
            m += 1
        }
    }
    var s := 0
    for m < 2 && s < n {
        if freq[s] == 0 {
            keys[m] = (u64(1) << 16) | Cast[u64](s)
            m += 1
        }
        s += 1
    }
    (mut keys[0..m]).Sort(fn(a: &u64, b: &u64): bool {
        return *a < *b
    })
    var w: [288]int = [0; 288]
    for i in 0..m {
        w[i] = Trunc[int](keys[i] >> 16)
    }
    MinRedundancy(mut w[0..m])
    // Lengths can exceed limit (at most m - 1); count them, then repair.
    var cnt: [289]int = [0; 289]
    for i in 0..m {
        cnt[w[i]] += 1
    }
    for l in limit + 1..289 {
        cnt[limit] += cnt[l]
        cnt[l] = 0
    }
    var total := 0
    for l in 1..limit + 1 {
        total += cnt[l] << (limit - l)
    }
    for total != 1 << limit {
        cnt[limit] -= 1
        var l := limit - 1
        for l > 0 {
            if cnt[l] != 0 {
                cnt[l] -= 1
                cnt[l + 1] += 2
                break
            }
            l -= 1
        }
        total -= 1
    }
    // The lightest symbols take the longest codes.
    var i := 0
    var l := limit
    for l >= 1 {
        for _ in 0..cnt[l] {
            lens[Trunc[int](keys[i] & 0xFFFF)] = Trunc[u8](l)
            i += 1
        }
        l -= 1
    }
}

// Codes sets codes to the canonical code of lens (section 3.2.2), each
// bit-reversed so that it is sent LSB first.
Codes(lens: []u8, codes: mut []u16) {
    var cnt: [16]int = [0; 16]
    for s in 0..lens.Len() {
        cnt[int(lens[s])] += 1
    }
    cnt[0] = 0
    var next: [16]int = [0; 16]
    var code := 0
    for l in 1..16 {
        code = (code + cnt[l - 1]) << 1
        next[l] = code
    }
    for s in 0..lens.Len() {
        l := int(lens[s])
        if l != 0 {
            codes[s] = Trunc[u16](Rev(next[l], l))
            next[l] += 1
        } else {
            codes[s] = 0
        }
    }
}

// FixedLensTable is the litlen lengths of a fixed-code block (section
// 3.2.6).
FixedLensTable(): [286]u8 {
    var lens: [286]u8 = [8; 286]
    (mut lens[144..256]).Fill(9)
    (mut lens[256..280]).Fill(7)
    return lens
}

FixedCodesTable(): [286]u16 {
    // Symbols 286 and 287 take part in the code (two more 8-bit codes,
    // before every 9-bit one), though no block sends them.
    short := FixedLensTable()
    var lens: [288]u8 = [8; 288]
    (mut lens[0..286]).CopyFrom(short[..])
    var all: [288]u16 = [0; 288]
    Codes(lens[..], mut all[..])
    var c: [286]u16 = [0; 286]
    (mut c[..]).CopyFrom(all[0..286])
    return c
}

FixedDistCodesTable(): [30]u16 {
    lens: [30]u8 = [5; 30]
    var c: [30]u16 = [0; 30]
    Codes(lens[..], mut c[..])
    return c
}

const FixedLens: [286]u8 = FixedLensTable()
const FixedCodes: [286]u16 = FixedCodesTable()
const FixedDistLens: [30]u8 = [5; 30]
const FixedDistCodes: [30]u16 = FixedDistCodesTable()

// LenSymTable maps a match length minus 3 to its symbol minus 257 (zlib's
// _length_code): 258 is symbol 285, not 284 with 31 extra.
LenSymTable(): [256]u8 {
    var t: [256]u8 = [0; 256]
    for c in 0..28 {
        b := int(LenBase[c]) - 3
        (mut t[b..b + (1 << int(LenExtra[c]))]).Fill(Trunc[u8](c))
    }
    t[255] = 28
    return t
}

// DistSymTable maps a distance minus 1 to its code (zlib's _dist_code):
// entries 0 to 255 for d < 256, then entry 256 + (d >> 7) for larger d,
// since every code from 16 on spans a multiple of 128 distances.
DistSymTable(): [512]u8 {
    var t: [512]u8 = [0; 512]
    for c in 0..30 {
        b := int(DistBase[c]) - 1
        span := 1 << int(DistExtra[c])
        if b < 256 {
            (mut t[b..b + span]).Fill(Trunc[u8](c))
        } else {
            var d := b
            for d < b + span {
                t[256 + (d >> 7)] = Trunc[u8](c)
                d += 128
            }
        }
    }
    return t
}

const LenSym: [256]u8 = LenSymTable()
const DistSym: [512]u8 = DistSymTable()

// DistCode is the distance code of dist (1 to 32768).
DistCode(dist: int): int {
    d := dist - 1
    if d < 256 {
        return int(DistSym[d])
    }
    return int(DistSym[256 + (d >> 7)])
}
