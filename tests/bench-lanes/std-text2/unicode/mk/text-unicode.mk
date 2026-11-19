// text-unicode, Mk only: one std.text function, <reps> passes over the
// generated text of ../utf16/README.md (splitmix64 from <seed>, at least
// <mib> MiB). Modes: graphemes (adds the clusters and the bytes of the
// last one), width (adds text.Width), nfc, nfd, nfkc, nfkd and fold (add
// the result's length and its byte at r % length), and marks (Nfd, as
// nfd, of Marks: "a" then mib * 1048576 combining marks, the worst case of
// canonical ordering). Usage: text-unicode <mode> <mib> <reps> <seed>.

import std.text

Mix(x: u64): u64 {
    var z := x
    z = (z ^ (z >> 30)) *% 0xbf58476d1ce4e5b9
    z = (z ^ (z >> 27)) *% 0x94d049bb133111eb
    return z ^ (z >> 31)
}

Gen(a: &Arena, n: int, seed: u64): OutOfMemory!str from a {
    var t := Text.New(a)
    var st := seed
    var len := 0
    for len < n {
        st = st +% 0x9e3779b97f4a7c15
        x := Mix(st)
        k := x % 16
        y := x >> 8
        var c: u32 = 0
        if k < 10 {
            c = 0x61 + Trunc[u32](y % 26)
            if x % 7 == 0 {
                c = 0x41 + Trunc[u32](y % 26)
            }
        } else if k == 10 {
            c = 0x20
        } else if k == 11 {
            c = 0xC0 + Trunc[u32](y % 64)
        } else if k == 12 {
            c = 0x430 + Trunc[u32](y % 32)
        } else if k == 13 {
            c = 0x4E00 + Trunc[u32](y % 0x5000)
        } else if k == 14 {
            c = 0x1F600 + Trunc[u32](y % 80)
        } else {
            c = 0x300 + Trunc[u32](y % 0x70)
        }
        try t.PushChar(Cast[char](c))
        len += Utf8Len(c)
    }
    return try a.Dup(t.Str())
}

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

// Out is what a mode that returns text adds: its length and one byte.
Out(t: str, r: int): u64 {
    b := t.AsBytes()
    if b.Len() == 0 {
        return 0
    }
    return Cast[u64](b.Len()) +% u64(b[r % b.Len()])
}

// Marks is the adversarial input of mode marks: "a" then n combining
// marks whose classes cycle 240, 230, 220, 202 (U+0345, U+0301, U+0323,
// U+0327), one run of non-starters that canonical ordering must sort.
Marks(a: &Arena, n: int): OutOfMemory!str from a {
    var t := Text.New(a)
    try t.Push("a")
    for i in 0..n {
        k := i % 4
        if k == 0 {
            try t.Push("\u{345}")
        } else if k == 1 {
            try t.Push("\u{301}")
        } else if k == 2 {
            try t.Push("\u{323}")
        } else {
            try t.Push("\u{327}")
        }
    }
    return try a.Dup(t.Str())
}

// Input is mode's text: Marks of mib * 1048576 marks for marks, else Gen.
Input(a: &Arena, mode: str, mib: int, seed: u64): OutOfMemory!str from a {
    if mode == "marks" {
        return try Marks(a, mib * 1048576)
    }
    return try Gen(a, mib * 1048576, seed)
}

export Main(sys: &System): OutOfMemory|ParseError|WriteError!void {
    mode := sys.args[1]
    mib := Cast[int](try sys.args[2].ParseInt())
    reps := Cast[int](try sys.args[3].ParseInt())
    seed := Cast[u64](try sys.args[4].ParseInt())
    s := try Input(sys.arena, mode, mib, seed)
    var ar := try Arena.New(sys.heap)
    defer ar.Free()
    var sum: u64 = 0
    for r in 0..reps {
        if mode == "graphemes" {
            var g := text.Graphemes(s)
            var n: u64 = 0
            var last := 0
            for {
                if c := g.Next() {
                    n += 1
                    last = c.Len()
                } else {
                    break
                }
            }
            sum = sum +% n +% Cast[u64](last)
        } else if mode == "width" {
            sum = sum +% Cast[u64](text.Width(s))
        } else if mode == "nfc" {
            sum = sum +% Out(try text.Normalize(&ar, s, text.Form.Nfc), r)
        } else if mode == "nfd" || mode == "marks" {
            sum = sum +% Out(try text.Normalize(&ar, s, text.Form.Nfd), r)
        } else if mode == "nfkc" {
            sum = sum +% Out(try text.Normalize(&ar, s, text.Form.Nfkc), r)
        } else if mode == "nfkd" {
            sum = sum +% Out(try text.Normalize(&ar, s, text.Form.Nfkd), r)
        } else {
            sum = sum +% Out(try text.FoldCase(&ar, s), r)
        }
        ar.Reset()
    }
    var out := sys.Stdout()
    try Print(mut out, "{sum}\n", (sum))
}
