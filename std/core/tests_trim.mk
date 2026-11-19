// Why: str.Trim returns a text whose end bytes are both ASCII and not
// White_Space whole, with no call of SpaceAt or SpaceBefore; that fast path
// must trim as the general loop does.
// What: test blocks over every text of 0 to 4 scalars of TtrAlpha and 20,000
// random texts of 5 to 40, against TtrOldTrim, the general loop, as oracle.
// How: TtrAlpha holds ASCII letters and punctuation, ASCII White_Space,
// U+0085, U+00A0, U+2028 and U+3000 (White_Space of 2 and 3 bytes) and U+00E9
// and U+200B (not White_Space). Each text is also searched with str.Find for
// every one-byte sub of the alphabet and a few longer ones, against a naive
// search.

const TtrAlpha: [13]str = ["a", ",", " ", "\t", "\n", "\r", "\u{85}", "\u{A0}", "\u{2028}", "\u{3000}", "\u{E9}", "\u{200B}", "/"]
const TtrSubs: [12]str = ["a", ",", " ", "\t", "\n", "/", ".", "\u{E9}", "a,", " a", "\u{A0}", "a\u{3000}"]

// TtrOldTrim is Trim's general loop: the start and end of the trimmed view.
TtrOldTrim(s: str): (start: int, end: int) {
    var start := 0
    var end := s.Len()
    for start < end {
        n := SpaceAt(s, start)
        if n == 0 {
            break
        }
        start += n
    }
    for end > start {
        n := SpaceBefore(s, end)
        if n == 0 {
            break
        }
        end -= n
    }
    return (start: start, end: end)
}

// TtrNaive is the first offset of sub in s, trying every start, or -1.
TtrNaive(s: str, sub: str): int {
    b := s.AsBytes()
    p := sub.AsBytes()
    var i := 0
    for i + p.Len() <= b.Len() {
        var j := 0
        for j < p.Len() && b[i + j] == p[j] {
            j += 1
        }
        if j == p.Len() {
            return i
        }
        i += 1
    }
    return -1
}

// TtrCheck trims and searches s and returns the answers that differ from
// the oracles'.
TtrCheck(s: str): int {
    var bad := 0
    r := TtrOldTrim(s)
    t := s.Trim()
    if t.Len() != r.end - r.start || t != s[r.start..r.end] {
        bad += 1
    }
    for sub in TtrSubs {
        got := s.Find(sub) else { -1 }
        if got != TtrNaive(s, sub) {
            bad += 1
        }
    }
    return bad
}

// TtrMix is splitmix64's output step.
TtrMix(x: u64): u64 {
    var z := x
    z = (z ^ (z >> 30)) *% 0xbf58476d1ce4e5b9
    z = (z ^ (z >> 27)) *% 0x94d049bb133111eb
    return z ^ (z >> 31)
}

test "Trim's ASCII-ends fast path trims every text of 0 to 4 scalars as the general loop does" {
    var buf: [16]u8 = [0; 16]
    var texts := 0
    var bad := 0
    // code's base-14 digits, low first, are the scalars (0: none); a text
    // is the digits before the first 0, and a nonzero digit after one is
    // a text already counted.
    for code in 0..14 * 14 * 14 * 14 {
        var c := code
        var n := 0
        var ok := true
        var ended := false
        for _ in 0..4 {
            d := c % 14
            c = c / 14
            if d == 0 {
                ended = true
                continue
            }
            if ended {
                ok = false
            }
            a := TtrAlpha[d - 1].AsBytes()
            for k in 0..a.Len() {
                buf[n + k] = a[k]
            }
            n += a.Len()
        }
        if !ok {
            continue
        }
        bad += TtrCheck(str.From(buf[0..n]).Must())
        texts += 1
    }
    Assert(texts == 1 + 13 + 13 * 13 + 13 * 13 * 13 + 13 * 13 * 13 * 13)
    Assert(bad == 0)
}

test "Trim's ASCII-ends fast path trims 20,000 random texts of 5 to 40 scalars as the general loop does" {
    var buf: [128]u8 = [0; 128]
    var bad := 0
    var st: u64 = 5
    for _ in 0..20000 {
        st = st +% 0x9e3779b97f4a7c15
        k := 5 + Cast[int](TtrMix(st) % 36)
        var n := 0
        for _ in 0..k {
            st = st +% 0x9e3779b97f4a7c15
            a := TtrAlpha[Cast[int](TtrMix(st) % 13)].AsBytes()
            for j in 0..a.Len() {
                buf[n + j] = a[j]
            }
            n += a.Len()
        }
        bad += TtrCheck(str.From(buf[0..n]).Must())
    }
    Assert(bad == 0)
}
