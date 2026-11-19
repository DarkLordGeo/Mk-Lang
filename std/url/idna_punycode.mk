// Why: UTS 46's ToASCII and ToUnicode convert labels to and from Punycode,
// and a host is untrusted input, so the RFC's quadratic loops are not safe
// to copy.
//
// What: PunycodeDecode and PunycodeEncode (RFC 3492, Bootstring with the
// parameters of its section 5), with the bias and digit helpers, a Fenwick
// tree (FenwickInit, FenwickAdd, FenwickSum, FenwickKth) and SortKeys.
//
// How: both directions run in O(L log L) for a label of L code points,
// where the RFC's loops are O(L^2): the decoder records each insertion's
// position and places them all at the end through a Fenwick tree of free
// slots (the last insertion first: its position is final, and each earlier
// one takes the k-th slot the later ones left free); the encoder takes the
// code points in order of value and position, and counts the code points
// below the current one between two positions in a Fenwick tree over
// positions. Arithmetic is checked against the RFC's maxint, 2^32 - 1
// (section 6.4).

const PunyBase: u64 = 36
const PunyTMin: u64 = 1
const PunyTMax: u64 = 26
const PunySkew: u64 = 38
const PunyDamp: u64 = 700
const PunyInitialBias: u64 = 72
const PunyInitialN: u64 = 128
const PunyMaxInt: u64 = 0xFFFF_FFFF

// PunyAdapt is section 6.1's bias adaptation.
PunyAdapt(delta: u64, points: u64, first: bool): u64 {
    var d := if first { delta / PunyDamp } else { delta / 2 }
    d += d / points
    var k: u64 = 0
    for d > ((PunyBase - PunyTMin) * PunyTMax) / 2 {
        d /= PunyBase - PunyTMin
        k += PunyBase
    }
    return k + (PunyBase - PunyTMin + 1) * d / (d + PunySkew)
}

// PunyThreshold is section 6.2's t for k and bias.
PunyThreshold(k: u64, bias: u64): u64 {
    if k <= bias {
        return PunyTMin
    }
    if k >= bias + PunyTMax {
        return PunyTMax
    }
    return k - bias
}

// PunyDigit is the value of basic code point c as a digit (letters in
// either case 0 to 25, digits 26 to 35), or 36 for none.
PunyDigit(c: u8): u64 {
    l := c | 0x20
    if l >= b'a' && l <= b'z' {
        return u64(l - b'a')
    }
    if c >= b'0' && c <= b'9' {
        return u64(c - b'0') + 26
    }
    return 36
}

// PunyChar is the lower-case basic code point of digit d.
PunyChar(d: u64): u8 {
    if d < 26 {
        return b'a' + Trunc[u8](d)
    }
    return b'0' + Trunc[u8](d - 26)
}

// FenwickInit makes t (one more than its n slots) count one in every slot.
FenwickInit(t: mut []u32) {
    for i in 1..t.Len() {
        t[i] = Trunc[u32](i & (0 - i))
    }
}

// FenwickAdd adds d to slot i (from 0) of t.
FenwickAdd(t: mut []u32, i: int, d: u32) {
    var j := i + 1
    for j < t.Len() {
        t[j] = t[j] +% d
        j += j & (0 - j)
    }
}

// FenwickSum is the count of slots 0..i (exclusive) of t.
FenwickSum(t: []u32, i: int): u64 {
    var s: u64 = 0
    var j := i
    for j > 0 {
        s += u64(t[j])
        j -= j & (0 - j)
    }
    return s
}

// FenwickKth is the slot (from 0) holding the k-th one (from 0) of t.
FenwickKth(t: []u32, k: u64): int {
    n := t.Len() - 1
    var step := 1
    for step * 2 <= n {
        step *= 2
    }
    var pos := 0
    var rest := k + 1
    for step > 0 {
        if pos + step <= n && u64(t[pos + step]) < rest {
            pos += step
            rest -= u64(t[pos])
        }
        step /= 2
    }
    return pos
}

// SortKeys puts keys in increasing order: heapsort (Williams 1964, with
// Floyd's bottom-up construction), O(n log n) for every input, in place
// and without recursion, so the encoder's stack is bounded. The
// keys are distinct (each holds its position), so stability is moot.
SortKeys(keys: mut []u64) {
    n := keys.Len()
    var start := n / 2
    for start > 0 {
        start -= 1
        SiftDown(keys, start, n)
    }
    var end := n
    for end > 1 {
        end -= 1
        top := keys[0]
        keys[0] = keys[end]
        keys[end] = top
        SiftDown(keys, 0, end)
    }
}

// SiftDown moves keys[root] down the max-heap keys[0..end] to its place.
SiftDown(keys: mut []u64, root: int, end: int) {
    var at := root
    for {
        var child := 2 * at + 1
        if child >= end {
            return
        }
        if child + 1 < end && keys[child + 1] > keys[child] {
            child += 1
        }
        if keys[at] >= keys[child] {
            return
        }
        swap := keys[at]
        keys[at] = keys[child]
        keys[child] = swap
        at = child
    }
}

// Utf8Len is the length of code point c in UTF-8.
Utf8Len(c: u32): int {
    if c < 0x80 {
        return 1
    }
    if c < 0x800 {
        return 2
    }
    if c < 0x10000 {
        return 3
    }
    return 4
}

// PutUtf8 writes c in UTF-8 at out[o..] and returns the offset after it.
PutUtf8(out: mut []u8, o: int, c: u32): int {
    if c < 0x80 {
        out[o] = Trunc[u8](c)
        return o + 1
    }
    if c < 0x800 {
        out[o] = 0xC0 | Trunc[u8](c >> 6)
        out[o + 1] = 0x80 | Trunc[u8](c & 0x3F)
        return o + 2
    }
    if c < 0x10000 {
        out[o] = 0xE0 | Trunc[u8](c >> 12)
        out[o + 1] = 0x80 | Trunc[u8]((c >> 6) & 0x3F)
        out[o + 2] = 0x80 | Trunc[u8](c & 0x3F)
        return o + 3
    }
    out[o] = 0xF0 | Trunc[u8](c >> 18)
    out[o + 1] = 0x80 | Trunc[u8]((c >> 12) & 0x3F)
    out[o + 2] = 0x80 | Trunc[u8]((c >> 6) & 0x3F)
    out[o + 3] = 0x80 | Trunc[u8](c & 0x3F)
    return o + 4
}

// PunycodeDecode is section 6.2's decoder on s, a label without its
// "xn--": the text it encodes, in a, or none when s is not Punycode or
// encodes a surrogate or a value past U+10FFFF. O(L log L); allocates
// O(L) in a.
PunycodeDecode(a: &Arena, s: []u8): OutOfMemory!?str from a {
    // The basic code points are those before the last delimiter.
    var b := 0
    for i, c in s {
        if c == b'-' {
            b = i
        }
    }
    for c in s[0..b] {
        if c >= 0x80 {
            return none
        }
    }
    var input := if b > 0 { b + 1 } else { 0 }
    inserted := try a.Slice[u32](s.Len() - input)
    positions := try a.Slice[u32](s.Len() - input)
    var count := 0
    var n := PunyInitialN
    var i: u64 = 0
    var bias := PunyInitialBias
    var length := Cast[u64](b)
    for input < s.Len() {
        old := i
        var w: u64 = 1
        var k := PunyBase
        for {
            if input >= s.Len() {
                return none
            }
            digit := PunyDigit(s[input])
            input += 1
            if digit >= PunyBase || digit > (PunyMaxInt - i) / w {
                return none
            }
            i += digit * w
            t := PunyThreshold(k, bias)
            if digit < t {
                break
            }
            if w > PunyMaxInt / (PunyBase - t) {
                return none
            }
            w *= PunyBase - t
            k += PunyBase
        }
        bias = PunyAdapt(i - old, length + 1, old == 0)
        if i / (length + 1) > PunyMaxInt - n {
            return none
        }
        n += i / (length + 1)
        i %= length + 1
        if n > 0x10FFFF || n >= 0xD800 && n <= 0xDFFF {
            return none
        }
        inserted[count] = Trunc[u32](n)
        positions[count] = Trunc[u32](i)
        count += 1
        i += 1
        length += 1
    }
    // Place the insertions, the last first, each in the k-th slot the
    // later ones left free; the basic code points fill the rest in order.
    total := b + count
    slots := try a.Slice[u32](total)
    slots.Fill(0xFFFF_FFFF)
    tree := try a.Slice[u32](total + 1)
    FenwickInit(tree)
    var j := count
    for j > 0 {
        j -= 1
        at := FenwickKth(tree[..], u64(positions[j]))
        slots[at] = inserted[j]
        FenwickAdd(tree, at, 0xFFFF_FFFF)
    }
    var bytes := 0
    var next := 0
    for at in 0..total {
        if slots[at] == 0xFFFF_FFFF {
            slots[at] = u32(s[next])
            next += 1
        }
        bytes += Utf8Len(slots[at])
    }
    out := try a.Slice[u8](bytes)
    var o := 0
    for c in slots {
        o = PutUtf8(out, o, c)
    }
    // Must: every value written is a scalar value, checked above.
    return some(str.From(out[..]).Must())
}

// PunycodeEncode is section 6.3's encoder on the label text s: its
// Punycode, without "xn--", in a, or none when a delta passes maxint.
// O(L log L); allocates O(L) in a.
PunycodeEncode(a: &Arena, s: str): OutOfMemory!?str from a {
    var length := 0
    for _ in s.Scalars() {
        length += 1
    }
    // Each non-basic code point as (value << 32) | position, sorted.
    var keys := try a.Slice[u64](length)
    var others := 0
    var basic := 0
    var position: u64 = 0
    for c in s.Scalars() {
        v := u64(u32(c))
        if v < 0x80 {
            basic += 1
        } else {
            keys[others] = (v << 32) | position
            others += 1
        }
        position += 1
    }
    SortKeys(mut keys[0..others])
    sorted := keys[0..others]
    // At most 10 digits a delta below 2^32 (t is at most 26, so each digit
    // but the last divides by at least 10).
    out := try a.Slice[u8](basic + 1 + 10 * others)
    var o := 0
    for c in s.Scalars() {
        if u32(c) < 0x80 {
            out[o] = Trunc[u8](u32(c))
            o += 1
        }
    }
    if basic > 0 {
        out[o] = b'-'
        o += 1
    }
    // tree counts the positions holding a code point below the current one.
    tree := try a.Slice[u32](length + 1)
    var at: u64 = 0
    for c in s.Scalars() {
        if u32(c) < 0x80 {
            FenwickAdd(tree, Cast[int](at), 1)
        }
        at += 1
    }
    var n := PunyInitialN
    var delta: u64 = 0
    var bias := PunyInitialBias
    var h := Cast[u64](basic)
    var idx := 0
    for idx < others {
        m := sorted[idx] >> 32
        if (m - n) > (PunyMaxInt - delta) / (h + 1) {
            return none
        }
        delta += (m - n) * (h + 1)
        var prev: int = -1
        var j := idx
        for j < others && sorted[j] >> 32 == m {
            p := Cast[int](sorted[j] & 0xFFFF_FFFF)
            delta += FenwickSum(tree[..], p) - FenwickSum(tree[..], prev + 1)
            if delta > PunyMaxInt {
                return none
            }
            // Section 6.3: q in base 36 with thresholds.
            var q := delta
            var k := PunyBase
            for {
                t := PunyThreshold(k, bias)
                if q < t {
                    break
                }
                out[o] = PunyChar(t + (q - t) % (PunyBase - t))
                o += 1
                q = (q - t) / (PunyBase - t)
                k += PunyBase
            }
            out[o] = PunyChar(q)
            o += 1
            bias = PunyAdapt(delta, h + 1, h == Cast[u64](basic))
            delta = 0
            h += 1
            prev = p
            j += 1
        }
        delta += FenwickSum(tree[..], length) - FenwickSum(tree[..], prev + 1) + 1
        if delta > PunyMaxInt {
            return none
        }
        for r in idx..j {
            FenwickAdd(tree, Cast[int](sorted[r] & 0xFFFF_FFFF), 1)
        }
        n = m + 1
        idx = j
    }
    // Must: every byte written is ASCII.
    return some(str.From(out[0..o]).Must())
}
