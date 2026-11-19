// Why: std.core's byte paths (UTF-8 validation, byte search, equality,
// CopyFrom) run on std.simd; each must answer exactly as the SWAR word form
// it replaced.
// What: test blocks that compare str.From, IndexByteIn, str.Find,
// Slice.Index, BytesEqual, StartsWith and EndsWith with the old SWAR forms
// kept here as oracles (TbyValidWords, TbyIndexWords, TbyEqualWords); CopyFrom
// as one raw.Move; and TbyFields's compile-time fuel guard.
// How: lengths and offsets on both sides of the vector widths (16, 32, 64),
// the dispatch lengths (64, 256) and std.core's own thresholds (ValidUtf8's
// 256 bytes, BytesEqual's 16, IndexByteIn's first 32 bytes past from, where
// the SWAR forms stay, as on x86_64-freestanding), plus seeded random text.
// TestCore_BytePathsMatchScalar runs the same paths in release against
// Go's own, FuzzCore_BytePaths fuzzes them, and the other fuel guards are
// std/core_guards_test.go's harness program.

import std.raw

// TbyValidWords is the SWAR UTF-8 check std.core ran before std.simd.
TbyValidWords(b: []u8): bool {
    n := b.Len()
    var i := 0
    for i < n {
        if i + 8 <= n {
            w := LoadWord(b, i) & 0x8080808080808080
            if w == 0 {
                i += 8
                continue
            }
            i += raw.Ctz64(w) / 8
        }
        c := b[i]
        if c < 0x80 {
            i += 1
            continue
        }
        var need := 0
        var lo: u8 = 0x80
        var hi: u8 = 0xBF
        if c >= 0xC2 && c <= 0xDF {
            need = 1
        } else if c == 0xE0 {
            need = 2
            lo = 0xA0
        } else if c >= 0xE1 && c <= 0xEC || c == 0xEE || c == 0xEF {
            need = 2
        } else if c == 0xED {
            need = 2
            hi = 0x9F
        } else if c == 0xF0 {
            need = 3
            lo = 0x90
        } else if c >= 0xF1 && c <= 0xF3 {
            need = 3
        } else if c == 0xF4 {
            need = 3
            hi = 0x8F
        } else {
            return false
        }
        if i + need >= n {
            return false
        }
        d := b[i + 1]
        if d < lo || d > hi {
            return false
        }
        for k in 2..need + 1 {
            e := b[i + k]
            if e < 0x80 || e > 0xBF {
                return false
            }
        }
        i += need + 1
    }
    return true
}

// TbyIndexWords is the SWAR IndexByteIn std.core ran before std.simd.
TbyIndexWords(b: []u8, c: u8, from: int): ?int {
    n := b.Len()
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
    }
    for n - i >= 8 {
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

// TbyEqualWords is the SWAR BytesEqual std.core ran before std.simd.
TbyEqualWords(b: []u8, at: int, p: []u8): bool {
    m := p.Len()
    var j := 0
    for j + 8 <= m {
        if LoadWord(b, at + j) != LoadWord(p, j) {
            return false
        }
        j += 8
    }
    for j < m {
        if b[at + j] != p[j] {
            return false
        }
        j += 1
    }
    return true
}

// TbyFindNaive is the first match of p in b at or after from, by the
// O(n * m) scan, or none; p is not empty.
TbyFindNaive(b: []u8, p: []u8, from: int): ?int {
    var i := from
    for i + p.Len() <= b.Len() {
        if TbyEqualWords(b, i, p) {
            return some(i)
        }
        i += 1
    }
    return none
}

// TbySame reports whether two optional offsets are equal.
TbySame(x: ?int, y: ?int): bool {
    if a := x {
        if b := y {
            return a == b
        }
        return false
    }
    return y.IsNone()
}

// TbyRng is xorshift64 (Marsaglia, 2003): a fixed sequence, so a failure
// repeats.
struct TbyRng {
    s: u64
}

TbyRng.Next(mut self): u64 {
    var x := self.s
    x = x ^ (x << 13)
    x = x ^ (x >> 7)
    x = x ^ (x << 17)
    self.s = x
    return x
}

TbyRng.Below(mut self, n: int): int {
    return Cast[int](self.Next() % Cast[u64](n))
}

// TbyUtf8Byte is a byte drawn so that valid and nearly valid UTF-8 are
// common: ASCII, continuation bytes, every lead, and the bytes no UTF-8
// holds (C0, C1, F5 to FF).
TbyUtf8Byte(r: mut TbyRng): u8 {
    k := r.Below(8)
    if k < 3 {
        return Trunc[u8](0x20 + r.Below(0x5F))
    }
    if k < 6 {
        return Trunc[u8](0x80 + r.Below(0x40))
    }
    return Trunc[u8](0xC0 + r.Below(0x40))
}

// TbyPut writes the UTF-8 of a random scalar (every length, surrogates
// excluded) at b[i..] when it fits, and returns its length, or 0.
TbyPut(r: mut TbyRng, b: mut []u8, i: int): int {
    k := r.Below(4)
    var v: u32 = 0
    if k == 0 {
        v = Trunc[u32](0x80 + r.Below(0x780))
    } else if k == 1 {
        v = Trunc[u32](0x800 + r.Below(0xF800))
        if v >= 0xD800 && v <= 0xDFFF {
            v = 0xE000
        }
    } else if k == 2 {
        v = Trunc[u32](0x10000 + r.Below(0x100000))
    } else {
        v = Trunc[u32](r.Below(0x80))
    }
    if v < 0x80 {
        if i + 1 > b.Len() {
            return 0
        }
        b[i] = Trunc[u8](v)
        return 1
    }
    if v < 0x800 {
        if i + 2 > b.Len() {
            return 0
        }
        b[i] = Trunc[u8](0xC0 | (v >> 6))
        b[i + 1] = Trunc[u8](0x80 | (v & 0x3F))
        return 2
    }
    if v < 0x10000 {
        if i + 3 > b.Len() {
            return 0
        }
        b[i] = Trunc[u8](0xE0 | (v >> 12))
        b[i + 1] = Trunc[u8](0x80 | ((v >> 6) & 0x3F))
        b[i + 2] = Trunc[u8](0x80 | (v & 0x3F))
        return 3
    }
    if i + 4 > b.Len() {
        return 0
    }
    b[i] = Trunc[u8](0xF0 | (v >> 18))
    b[i + 1] = Trunc[u8](0x80 | ((v >> 12) & 0x3F))
    b[i + 2] = Trunc[u8](0x80 | ((v >> 6) & 0x3F))
    b[i + 3] = Trunc[u8](0x80 | (v & 0x3F))
    return 4
}

// TbyFromAgrees reports whether str.From and the SWAR oracle agree on b.
TbyFromAgrees(b: []u8): bool {
    var ok := true
    _ = str.From(b) else {
        ok = false
    }
    return ok == TbyValidWords(b)
}

test "str.From accepts exactly what the SWAR form accepted, two and three bytes everywhere" {
    // Every byte pair, and every lead with every second byte and a
    // boundary third, at offsets on both sides of 8, 16, 32 and 64 bytes,
    // in ASCII text of 300 bytes (so the 256-byte dispatch runs), in its
    // prefix that ends after the pair (63 and 64 bytes long at 61 and 62),
    // and in its first 200 bytes, below ValidUtf8's 256, on the SWAR form.
    var buf: [300]u8 = [0x61; 300]
    var bad := 0
    for at in [0, 7, 15, 16, 31, 61, 62, 63, 64, 255, 297] {
        for x in 0..256 {
            for y in 0..256 {
                buf[at] = Trunc[u8](x)
                buf[at + 1] = Trunc[u8](y)
                if !TbyFromAgrees(buf[0..]) || !TbyFromAgrees(buf[0..at + 2]) {
                    bad += 1
                }
                if at + 2 <= 200 && !TbyFromAgrees(buf[0..200]) {
                    bad += 1
                }
                if x >= 0xE0 {
                    for z in [0x7F, 0x80, 0xBF, 0xC0] {
                        buf[at + 2] = Trunc[u8](z)
                        if !TbyFromAgrees(buf[0..]) || !TbyFromAgrees(buf[at..at + 3]) {
                            bad += 1
                        }
                    }
                    buf[at + 2] = 0x61
                }
            }
        }
        buf[at] = 0x61
        buf[at + 1] = 0x61
    }
    Assert(bad == 0)
}

test "str.From accepts exactly what the SWAR form accepted, random text" {
    var r := TbyRng(s: 0x9E3779B97F4A7C15)
    var buf: [600]u8 = [0; 600]
    var valid := 0
    var bad := 0
    for round in 0..6000 {
        n := r.Below(600)
        var i := 0
        for i < n {
            if r.Below(4) == 0 {
                buf[i] = TbyUtf8Byte(mut r)
                i += 1
            } else {
                k := TbyPut(mut r, mut buf[0..n], i)
                if k == 0 {
                    buf[i] = 0x41
                    i += 1
                } else {
                    i += k
                }
            }
        }
        // Mostly valid: one in three rounds has no stray byte at all.
        if round % 3 == 0 {
            var j := 0
            for j < n {
                k := TbyPut(mut r, mut buf[0..n], j)
                if k == 0 {
                    buf[j] = 0x42
                    j += 1
                } else {
                    j += k
                }
            }
        }
        from := r.Below(n + 1)
        s := buf[from..n]
        if !TbyFromAgrees(s) {
            bad += 1
        }
        if TbyValidWords(s) {
            valid += 1
        }
    }
    Assert(bad == 0)
    // Both answers were exercised.
    Assert(valid > 1000)
    Assert(valid < 5900)
}

test "str.From over runs of two-byte scalars below 256 bytes, with the SWAR form's answers" {
    // Below 256 bytes ValidUtf8 is the SWAR form at every density (an
    // earlier density gate gave std.simd the rest after a dense run). The
    // block keeps the gate's shapes, so a
    // hand-off at a scalar boundary below 256 bytes stays checked: runs of
    // 7, 8 and 9 two-byte scalars, packed or three ASCII bytes apart, after
    // 0 or 9 ASCII bytes, then ASCII or two-byte text to the end, in texts
    // of 64 to 255 bytes; in each, every lead byte with eleven second
    // bytes at offsets from the run's end on.
    var buf: [255]u8 = [0x61; 255]
    var bad := 0
    var cases := 0
    for n in [64, 65, 100, 127, 128, 200, 255] {
        for start in [0, 9] {
            for k in [7, 8, 9] {
                for sep in [0, 3] {
                    for rest in 0..2 {
                        for i in 0..n {
                            buf[i] = 0x61
                        }
                        var p := start
                        for _ in 0..k {
                            if p + 2 <= n {
                                buf[p] = 0xC3
                                buf[p + 1] = 0xA9
                            }
                            p += 2 + sep
                        }
                        end := p - sep
                        if rest == 1 {
                            var q := end
                            for q + 2 <= n {
                                buf[q] = 0xD0
                                buf[q + 1] = 0xB0
                                q += 2
                            }
                        }
                        if !TbyFromAgrees(buf[0..n]) {
                            bad += 1
                        }
                        for at in [end, end + 1, end + 8, n - 17, n - 2] {
                            if at < end || at + 2 > n {
                                continue
                            }
                            sx := buf[at]
                            sy := buf[at + 1]
                            for x in 0..256 {
                                for y in [0x00, 0x41, 0x7F, 0x80, 0x8F, 0x90, 0x9F, 0xA0, 0xBF, 0xC0, 0xFF] {
                                    buf[at] = Trunc[u8](x)
                                    buf[at + 1] = Trunc[u8](y)
                                    cases += 1
                                    if !TbyFromAgrees(buf[0..n]) {
                                        bad += 1
                                    }
                                }
                            }
                            buf[at] = sx
                            buf[at + 1] = sy
                        }
                    }
                }
            }
        }
    }
    Assert(bad == 0)
    Assert(cases > 1_000_000)
}

test "IndexByteIn, str.Find of one byte and Slice.Index find what the SWAR form found" {
    var r := TbyRng(s: 0xD1B54A32D192ED03)
    var buf: [400]u8 = [0; 400]
    var bad := 0
    var hits := 0
    for round in 0..3000 {
        n := r.Below(400)
        // A few bytes, so a byte is often there; sometimes none of c.
        alpha := 2 + r.Below(24)
        for i in 0..n {
            buf[i] = Trunc[u8](0x61 + r.Below(alpha))
        }
        c := Trunc[u8](0x61 + r.Below(alpha + 2))
        b := buf[0..n]
        for from in [0, 1, 7, 8, 15, 16, 17, 31, 32, 33, 48, 63, 64, 65, 255, 256, n - 1, n] {
            if from < 0 || from > n {
                continue
            }
            want := TbyIndexWords(b, c, from)
            if want.IsSome() {
                hits += 1
            }
            if !TbySame(IndexByteIn(b, c, from), want) {
                bad += 1
            }
            s := str.From(b).Must()
            var one: [1]u8 = [c]
            if !TbySame(FindFrom(s, str.From(one[0..]).Must(), from), want) {
                bad += 1
            }
        }
        if !TbySame(b.Index(&c), TbyIndexWords(b, c, 0)) {
            bad += 1
        }
        _ = round
    }
    Assert(bad == 0)
    Assert(hits > 10000)
    // Past a from beyond the bytes there is nothing; a match in the last
    // byte of a vector, of the tail and of a short slice is found.
    // A match d bytes past from, every d from 1 to 49: both sides of the
    // first 32 bytes' four SWAR words (each of 17 to 32 in the third and
    // fourth) and of std.simd's widths after them.
    var z: [100]u8 = [0x30; 100]
    for at in [0, 15, 16, 31, 32, 33, 47, 48, 63, 64, 99] {
        z[at] = 0x31
        Assert(TbySame(IndexByteIn(z[0..], 0x31, 0), some(at)))
        Assert(TbySame(IndexByteIn(z[0..at + 1], 0x31, at), some(at)))
        Assert(IndexByteIn(z[0..], 0x31, at + 1).IsNone())
        for d in 1..50 {
            if at >= d {
                Assert(TbySame(IndexByteIn(z[0..], 0x31, at - d), some(at)))
                Assert(TbySame(IndexByteIn(z[0..at + 1], 0x31, at - d), some(at)))
            }
        }
        z[at] = 0x30
    }
    Assert(IndexByteIn(z[0..], 0x30, 100).IsNone())
    Assert(IndexByteIn(z[0..], 0x30, 101).IsNone())
}

test "BytesEqual, StartsWith, EndsWith and Find agree with the SWAR form" {
    var r := TbyRng(s: 0x2545F4914F6CDD1D)
    var a: [300]u8 = [0; 300]
    var p: [300]u8 = [0; 300]
    var bad := 0
    var equal := 0
    for round in 0..4000 {
        n := r.Below(300)
        for i in 0..n {
            a[i] = Trunc[u8](0x61 + r.Below(3))
        }
        m := r.Below(n + 1)
        at := r.Below(n - m + 1)
        for j in 0..m {
            p[j] = a[at + j]
        }
        // Half the rounds flip one byte of the copy.
        if m > 0 && round % 2 == 0 {
            k := r.Below(m)
            p[k] = p[k] ^ Trunc[u8](1 + r.Below(255))
        }
        b := a[0..n]
        q := p[0..m]
        want := TbyEqualWords(b, at, q)
        if want {
            equal += 1
        }
        if BytesEqual(b, at, q) != want {
            bad += 1
        }
        // ASCII both, so both are text.
        if round % 2 != 0 {
            s := str.From(b).Must()
            t := str.From(q).Must()
            if s.StartsWith(t) != TbyEqualWords(b, 0, q) {
                bad += 1
            }
            if s.EndsWith(t) != TbyEqualWords(b, n - m, q) {
                bad += 1
            }
            if m > 0 {
                from := r.Below(n + 1)
                if !TbySame(FindFrom(s, t, from), TbyFindNaive(b, q, from)) {
                    bad += 1
                }
            }
        }
    }
    Assert(bad == 0)
    Assert(equal > 1500)
    // Every length on both sides of BytesEqual's 16 (two words up to it,
    // std.simd above) and a word, each byte flipped in turn.
    var c: [40]u8 = [0x5A; 40]
    var d: [40]u8 = [0x5A; 40]
    for m in [1, 7, 8, 9, 15, 16, 17, 23, 24, 33] {
        Assert(BytesEqual(c[0..], 40 - m, d[0..m]))
        for k in 0..m {
            d[k] = 0x5B
            Assert(!BytesEqual(c[0..], 40 - m, d[0..m]))
            Assert(!BytesEqual(c[0..], 0, d[0..m]))
            d[k] = 0x5A
        }
    }
    // Periodic needles, where Two-Way's period test (BytesEqual) decides.
    h := "abababababababababababababababababababababababababababababababababababc"
    Assert(TbySame(h.Find("ababc"), some(h.Len() - 5)))
    Assert(TbySame(h.Find("abababababababababababababababababababc"), some(h.Len() - 39)))
    Assert(h.Find("abababababababababababababababababababababababababababababababababababab").IsNone())
    Assert(TbySame("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaab".Find("aaaaaaaaaaaaaaaaaaab"), some(24)))
}

// TbyCopySum fills one array, copies it into another with CopyFrom and sums
// the copy, at compile time below.
TbyCopySum(): int {
    var src: [40]i64 = [0; 40]
    for i in 0..40 {
        src[i] = Cast[i64](i * 3)
    }
    var dst: [40]i64 = [0; 40]
    dst.CopyFrom(src[0..])
    var t: i64 = 0
    for x in dst {
        t += x
    }
    return Cast[int](t)
}

const TbyCopyAtCompileTime: int = TbyCopySum()

const TbyFromAtCompileTime: bool = TbyFromAgrees("h\u{E9}llo w\u{F6}rld, a longer line of text to pass sixteen bytes \u{1F600}".AsBytes())

const TbyFindAtCompileTime: ?int = "key=value; with a longer tail past sixteen bytes".Find(";")

// TbyFields splits a table of short fields and runs, per field, StartsWith,
// EndsWith, a one-byte Find and str.From: the shape of a table parsed at
// compile time, where std.simd's forms cost several times the steps of
// std.core's word forms.
TbyFields(t: str): int {
    var n := 0
    var it := t.Split(",")
    for p := it.Next() {
        if p.StartsWith("k0") {
            n += 1
        }
        if p.EndsWith("1") {
            n += 1
        }
        if k := p.Find("=") {
            n += k
        }
        _ = str.From(p.AsBytes()) else {
            return -1
        }
    }
    return n
}

// The fuel is 1.3 times what v1.2.0's std needed on the 0.5% grid, 49,333
// steps (measured by bisecting the fuel, 2026-11-16); on that grid the first
// std.simd form needed 109,384, and the form with the short paths 49,577.
// Exact needs, measured the same way: v1.2.0 49,280, this std 50,339, on
// x86_64-freestanding 49,280.
const TbyFieldsAtCompileTime: int = TbyFields("k001=vx01,k002=vx02,k003=vx03,k004=vx04,k005=vx05,k006=vx06,k007=vx07,k008=vx08,k009=vx09,k010=vx10,k011=vx11,k012=vx12,k013=vx13,k014=vx14,k015=vx15,k016=vx16,k017=vx17,k018=vx18,k019=vx19,k020=vx20,k021=vx21,k022=vx22,k023=vx23,k024=vx24,k025=vx25,k026=vx26,k027=vx27,k028=vx28,k029=vx29,k030=vx30,k031=vx31,k032=vx32,k033=vx33,k034=vx34,k035=vx35,k036=vx36,k037=vx37,k038=vx38,k039=vx39,k040=vx40,") fuel 64_000

// The other fuel guards (Split, str.From at the 256-byte hand-off,
// StartsWith, and the 4 KiB paths) are a harness program,
// std/core_guards_test.go's TestCore_FuelGuards: as consts here they
// read std.simd's layouts at compile time, and the front cache looks
// std.core, in the prelude's closure, up before any layout is answered, so
// std.core would miss it on every warm check.

struct TbyRec {
    a: u8
    b: i64
    c: u16
}

test "CopyFrom is one raw.Move of every element, at run time and compile time" {
    Assert(TbyCopyAtCompileTime == 3 * 39 * 40 / 2)
    Assert(TbyFromAtCompileTime)
    Assert(TbySame(TbyFindAtCompileTime, some(9)))
    Assert(TbyFieldsAtCompileTime == 40 + 4 + 40 * 4)
    var r := TbyRng(s: 0xA0761D6478BD642F)
    for n in 0..70 {
        var s8: [70]u8 = [0; 70]
        var d8: [70]u8 = [0xEE; 70]
        var s64: [70]u64 = [0; 70]
        var d64: [70]u64 = [7; 70]
        var sr: [70]TbyRec = [TbyRec(a: 0, b: 0, c: 0); 70]
        var dr: [70]TbyRec = [TbyRec(a: 9, b: 9, c: 9); 70]
        for i in 0..n {
            s8[i] = Trunc[u8](r.Next())
            s64[i] = r.Next()
            sr[i] = TbyRec(a: Trunc[u8](r.Next()), b: Cast[i64](r.Below(1000)), c: Trunc[u16](r.Next()))
        }
        (mut d8[0..n]).CopyFrom(s8[0..n])
        (mut d64[0..n]).CopyFrom(s64[0..n])
        (mut dr[0..n]).CopyFrom(sr[0..n])
        for i in 0..n {
            Assert(d8[i] == s8[i])
            Assert(d64[i] == s64[i])
            Assert(dr[i].a == sr[i].a && dr[i].b == sr[i].b && dr[i].c == sr[i].c)
        }
        // Nothing past n is written.
        for i in n..70 {
            Assert(d8[i] == 0xEE)
            Assert(d64[i] == 7)
            Assert(dr[i].a == 9 && dr[i].b == 9 && dr[i].c == 9)
        }
    }
    // Two views of one array, the later half over the earlier: CopyFrom
    // between distinct sub-slices of one var array.
    var both: [64]u8 = [0; 64]
    for i in 0..64 {
        both[i] = Trunc[u8](i)
    }
    var tmp: [32]u8 = [0; 32]
    tmp.CopyFrom(both[32..64])
    (mut both[0..32]).CopyFrom(tmp[0..])
    for i in 0..32 {
        Assert(both[i] == Trunc[u8](i + 32))
    }
}
