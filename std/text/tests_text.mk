// Why: Normalize's private sizing pass (Plan), its one scratch buffer and
// its counting sort (Reorder, CountSort) are reachable only from inside
// the module.
// What: test blocks of std.text, run by `mk test --std=std.text`
// (TestSTDTEXT_TestBlocks, dev and release). Helper names start with Tt.
// How: texts built to hit each path (Korean and Latin-1 after a mark, a
// run of 40 marks) check that Plan's bounds are exact, that the scratch
// buffer fits the largest chunk, and that CountSort is a stable sort.

// TtRep is head then n copies of unit.
TtRep(a: &Arena, head: str, unit: str, n: int): OutOfMemory!str from a {
    var t := Text.New(a)
    try t.Push(head)
    for _ in 0..n {
        try t.Push(unit)
    }
    return try a.Dup(t.Str())
}

// TtPlan is what Normalize allocates for s in form: the output bound and
// the most scalars a chunk decomposes to (0 when the quick check passes
// whole and Normalize copies s).
TtPlan(s: str, form: Form): (bytes: int, scalars: int) {
    b := s.AsBytes()
    mask := QcMask(form)
    first := Span(b, 0, mask)
    if first.stop >= b.Len() {
        return (bytes: b.Len(), scalars: 0)
    }
    return Plan(b, first.boundary, mask, form == Form.Nfkc || form == Form.Nfkd)
}

TtMix(x: u64): u64 {
    var z := x
    z = (z ^ (z >> 30)) *% 0xbf58476d1ce4e5b9
    z = (z ^ (z >> 27)) *% 0x94d049bb133111eb
    return z ^ (z >> 31)
}

test "Korean text after one mark: the Nfc bound is the result's length" (sys: &System) {
    var ar := try Arena.New(sys.heap)
    defer ar.Free()
    // U+0301 fails Nfc's quick check (Maybe) at byte 0; every U+AC01 after
    // it is a boundary that the spans copy, three bytes each.
    s := try TtRep(&ar, "\u{301}", "\u{AC01}", 100000)
    t := try Normalize(&ar, s, Form.Nfc)
    Assert(t == s)
    p := TtPlan(s, Form.Nfc)
    Assert(p.bytes == 2 + 3 * 100000)
    Assert(p.bytes == t.Len())
    Assert(p.scalars == 1)
}

test "Latin-1 text after one mark: the Nfc bound is the result's length" (sys: &System) {
    var ar := try Arena.New(sys.heap)
    defer ar.Free()
    s := try TtRep(&ar, "\u{301}", "\u{E9}x", 100000)
    t := try Normalize(&ar, s, Form.Nfc)
    Assert(t == s)
    Assert(TtPlan(s, Form.Nfc).bytes == t.Len())
    Assert(TtPlan(s, Form.Nfkc).bytes == t.Len())
}

test "the Nfd and Nfkd bounds are exact" (sys: &System) {
    var ar := try Arena.New(sys.heap)
    defer ar.Free()
    s := try TtRep(&ar, "a", "\u{E9}\u{AC01}\u{1E0B}\u{323}\u{FB01}x", 1000)
    for form in [Form.Nfd, Form.Nfkd] {
        t := try Normalize(&ar, s, form)
        Assert(TtPlan(s, form).bytes == t.Len())
    }
    for form in [Form.Nfc, Form.Nfkc] {
        t := try Normalize(&ar, s, form)
        Assert(TtPlan(s, form).bytes >= t.Len())
    }
}

test "Normalize sizes its one scratch buffer by the largest chunk" (sys: &System) {
    var ar := try Arena.New(sys.heap)
    defer ar.Free()
    // U+FDFA is NFKC_QC No and decomposes to 18 scalars; each chunk is
    // x U+FDFA U+FDFA, 37 scalars, and the ASCII x after it ends it.
    s := try TtRep(&ar, "", "x\u{FDFA}\u{FDFA}", 1000)
    p := TtPlan(s, Form.Nfkc)
    Assert(p.scalars == 37)
    t := try Normalize(&ar, s, Form.Nfkc)
    Assert(p.bytes == t.Len())
    u := try TtRep(&ar, "", "x\u{FDFA}\u{FDFA}", 1)
    Assert(t == try TtRep(&ar, "", try Normalize(&ar, u, Form.Nfkc), 1000))
}

test "CountSort gives the order of a stable insertion sort" (sys: &System) {
    var ar := try Arena.New(sys.heap)
    defer ar.Free()
    var st: u64 = 7
    for len in [33, 34, 100, 1000, 5000] {
        for classes in [2, 5, 40, 255] {
            buf := try ar.Slice[u32](2 * len)
            want := try ar.Slice[u32](len)
            for k in 0..len {
                st = st +% 0x9e3779b97f4a7c15
                c := Trunc[u32](TtMix(st) % Cast[u64](classes)) + 1
                x := (c << 24) | Trunc[u32](k)
                buf[k] = x
                want[k] = x
            }
            for k in 1..len {
                x := want[k]
                var h := k
                for h > 0 && want[h - 1] >> 24 > x >> 24 {
                    want[h] = want[h - 1]
                    h -= 1
                }
                want[h] = x
            }
            Reorder(buf, len)
            for k in 0..len {
                Assert(buf[k] == want[k])
            }
        }
    }
}

test "Nfd orders a run of 40 marks, longer than the frame's chunk, by class" (sys: &System) {
    var ar := try Arena.New(sys.heap)
    defer ar.Free()
    // 40 marks, classes 230 then 220 alternating: Nfd puts the 20 of 220
    // first, each class in its own order.
    s := try TtRep(&ar, "a", "\u{301}\u{323}", 20)
    t := try Normalize(&ar, s, Form.Nfd)
    w := try TtRep(&ar, try TtRep(&ar, "a", "\u{323}", 20), "\u{301}", 20)
    Assert(t == w)
    Assert(TtPlan(s, Form.Nfd).scalars == 41)
}
