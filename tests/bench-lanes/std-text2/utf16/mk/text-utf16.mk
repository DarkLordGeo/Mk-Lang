// text-utf16, Mk: std.text.ToUtf16 (encode), or std.text.FromUtf16 of one
// unit buffer (decode), <reps> passes over generated text of at least <mib>
// MiB (README.md: splitmix64 from <seed>, one scalar a step, 37.5 percent
// of them outside ASCII), into an arena reset each pass. Each pass adds the
// result's length and its unit (or byte) at r % length to the checksum.
// Usage: text-utf16 <encode|decode> <mib> <reps> <seed>.

import std.text

Mix(x: u64): u64 {
    var z := x
    z = (z ^ (z >> 30)) *% 0xbf58476d1ce4e5b9
    z = (z ^ (z >> 27)) *% 0x94d049bb133111eb
    return z ^ (z >> 31)
}

// Gen is README.md's text: scalars until it holds n bytes.
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

export Main(sys: &System): OutOfMemory|ParseError|Utf8Error|WriteError!void {
    mode := sys.args[1]
    mib := Cast[int](try sys.args[2].ParseInt())
    reps := Cast[int](try sys.args[3].ParseInt())
    seed := Cast[u64](try sys.args[4].ParseInt())
    s := try Gen(sys.arena, mib * 1048576, seed)
    var ar := try Arena.New(sys.heap)
    defer ar.Free()
    var sum: u64 = 0
    if mode == "encode" {
        for r in 0..reps {
            u := try text.ToUtf16(&ar, s)
            sum = sum +% Cast[u64](u.Len()) +% u64(u[r % u.Len()])
            ar.Reset()
        }
    } else if mode == "decode" {
        u := try text.ToUtf16(sys.arena, s)
        for r in 0..reps {
            back := try text.FromUtf16(&ar, u)
            b := back.AsBytes()
            sum = sum +% Cast[u64](b.Len()) +% u64(b[r % b.Len()])
            ar.Reset()
        }
    } else {
        fail ParseError
    }
    var out := sys.Stdout()
    try Print(mut out, "{sum}\n", (sum))
}
