// Why: Text's Writer checks a run of at most 32 bytes for ASCII inline before
// ValidUtf8, and its answer must stay ValidUtf8's for every run.
// What: test blocks for text_text.mk's TextWrite, with the helper TwCase.
// How: each case writes one run into a Text and judges it against str.From of
// the same bytes: an accepted run is appended exactly, and a rejected one fails
// with WriteError and leaves the text as it was.

// TwCase writes b into a Text holding "<" and reports whether the outcome
// agrees with str.From(b): appended whole when it is UTF-8, else
// WriteError with the text unchanged.
TwCase(sys: &System, b: []u8): bool {
    var t := Text.New(sys.arena)
    t.Push("<") else {
        return false
    }
    var w := t.Writer()
    var failed := false
    w.Bytes(b) else {
        failed = true
    }
    valid := ValidUtf8(b)
    if failed {
        return !valid && t.Str() == "<"
    }
    if !valid || t.Len() != b.Len() + 1 {
        return false
    }
    got := t.Str().AsBytes()
    for i in 0..b.Len() {
        if got[i + 1] != b[i] {
            return false
        }
    }
    return true
}

// TwAscii fills buf[..n] with printable ASCII, a different byte at each
// position.
TwAscii(buf: mut []u8, n: int) {
    for i in 0..n {
        buf[i] = Trunc[u8](0x21 + i % 90)
    }
}

test "Text's Writer appends an ASCII run of every length from 0 to 40" (sys: &System) {
    var buf: [48]u8 = [0; 48]
    for n in 0..41 {
        TwAscii(mut buf[..], n)
        Assert(TwCase(sys, buf[..n]))
        // and with 0x7F and 0x00, the ends of ASCII, at the last position
        if n > 0 {
            buf[n - 1] = 0x7F
            Assert(TwCase(sys, buf[..n]))
            buf[n - 1] = 0x00
            Assert(TwCase(sys, buf[..n]))
        }
    }
    var t := Text.New(sys.arena)
    var w := t.Writer()
    TwAscii(mut buf[..], 40)
    w.Bytes(buf[..40]).Must()
    w.Bytes(buf[..0]).Must()
    w.Bytes(buf[..1]).Must()
    Assert(t.Len() == 41)
}

test "Text's Writer judges a byte that is not ASCII at every position of a run of 1 to 40 as ValidUtf8 does" (sys: &System) {
    // lone continuation, overlong leads, bytes no UTF-8 holds, and leads of
    // 2, 3 and 4 bytes with ASCII after them (so not followed by their
    // continuations, or cut at the end of the run)
    odd: [12]u8 = [0x80, 0xBF, 0xC0, 0xC1, 0xF5, 0xF8, 0xFE, 0xFF, 0xC3, 0xE2, 0xED, 0xF0]
    var buf: [48]u8 = [0; 48]
    for n in 1..41 {
        for p in 0..n {
            for x in odd {
                TwAscii(mut buf[..], n)
                buf[p] = x
                Assert(TwCase(sys, buf[..n]))
                Assert(!ValidUtf8(buf[..n]))
            }
        }
    }
}

test "Text's Writer takes a whole sequence at every position and refuses one cut at the end of a run" (sys: &System) {
    // é, €, U+FFFF's neighbour U+FFFD, 😀, and the edges of each length
    seqs := ["é", "€", "\u{FFFD}", "😀", "\u{80}", "\u{7FF}", "\u{800}", "\u{FFFF}", "\u{10000}", "\u{10FFFF}"]
    var buf: [48]u8 = [0; 48]
    for s in seqs {
        q := s.AsBytes()
        for n in 0..37 {
            for p in 0..n + 1 {
                TwAscii(mut buf[..], n)
                // the sequence at p, the ASCII after it shifted on
                var k := n + q.Len()
                for i in 0..n - p {
                    buf[k - 1 - i] = buf[n - 1 - i]
                }
                for i in 0..q.Len() {
                    buf[p + i] = q[i]
                }
                Assert(TwCase(sys, buf[..k]))
                Assert(ValidUtf8(buf[..k]))
                // the same run ending inside the sequence
                if p == n {
                    for cut in 1..q.Len() {
                        Assert(TwCase(sys, buf[..k - cut]))
                        Assert(!ValidUtf8(buf[..k - cut]))
                    }
                }
            }
        }
    }
}

test "Text's Writer refuses surrogates, overlong forms and values past U+10FFFF in short runs" (sys: &System) {
    // ED A0 80 (U+D800), E0 80 80 and C0 80 (overlong), F4 90 80 80 (past
    // U+10FFFF), each alone and after 3 and 9 ASCII bytes
    bad: [4][4]u8 = [[0xED, 0xA0, 0x80, 0x41], [0xE0, 0x80, 0x80, 0x41], [0xC0, 0x80, 0x41, 0x41], [0xF4, 0x90, 0x80, 0x80]]
    var buf: [16]u8 = [0; 16]
    for b in bad {
        for pre in [0, 3, 9] {
            TwAscii(mut buf[..], pre)
            for i in 0..4 {
                buf[pre + i] = b[i]
            }
            Assert(TwCase(sys, buf[..pre + 4]))
            Assert(!ValidUtf8(buf[..pre + 4]))
        }
    }
}

test "A write that ends inside a sequence is refused, and the next write starts clean" (sys: &System) {
    var t := Text.New(sys.arena)
    var w := t.Writer()
    w.Str("ab").Must()
    e := "é".AsBytes()
    var failed := false
    w.Bytes(e[..1]) else {
        failed = true
    }
    Assert(failed)
    // the rest of the sequence alone is a lone continuation: refused too
    failed = false
    w.Bytes(e[1..]) else {
        failed = true
    }
    Assert(failed)
    w.Bytes(e).Must()
    Assert(t.Str() == "abé")
}
