// text-bounds, Mk only: text.WordBounds or text.SentenceBounds walked to
// the end, <reps> passes over a generated text of at least <mib> MiB.
// Modes: words and sentences over the mixed text of
// ../../std-text2/utf16/README.md (splitmix64 from <seed>, 5 of 16
// scalars outside ASCII); prosewords and prosesentences over ASCII prose
// (sentences of lower-case words of 1 to 10 letters, the first
// capitalised, some joined by ", ", a number like 3.14 one word in 16,
// each sentence ended by ". ", "? " or "! "); closes, sentences over the
// look-ahead's worst shape: "a." then n closing brackets, a space, n
// digits and "x", n = mib * 1048576 / 2 (one sentence). Each pass adds the
// number of boundaries and every boundary (wrapping). Usage: text-bounds
// <mode> <mib> <reps> <seed>.

import std.text

Mix(x: u64): u64 {
    var z := x
    z = (z ^ (z >> 30)) *% 0xbf58476d1ce4e5b9
    z = (z ^ (z >> 27)) *% 0x94d049bb133111eb
    return z ^ (z >> 31)
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

// Mixed is text-unicode's generated text.
Mixed(a: &Arena, n: int, seed: u64): OutOfMemory!str from a {
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

// Prose is the ASCII prose of the file comment.
Prose(a: &Arena, n: int, seed: u64): OutOfMemory!str from a {
    var t := Text.New(a)
    var st := seed
    var start := true
    var len := 0
    for len < n {
        st = st +% 0x9e3779b97f4a7c15
        x := Mix(st)
        if x % 16 == 0 {
            // A number, d.dd: four ASCII bytes.
            try t.PushChar(Cast[char](0x31 + Trunc[u32](x % 9)))
            try t.Push(".")
            try t.PushChar(Cast[char](0x30 + Trunc[u32]((x >> 8) % 10)))
            try t.PushChar(Cast[char](0x30 + Trunc[u32]((x >> 12) % 10)))
            len += 4
        } else {
            k := Cast[int]((x >> 4) % 10) + 1
            var y := x >> 8
            for i in 0..k {
                var c := 0x61 + Trunc[u32](y % 26)
                if i == 0 && start {
                    c -= 0x20
                }
                try t.PushChar(Cast[char](c))
                y = y / 26
            }
            len += k
        }
        start = false
        p := (x >> 40) % 12
        if p == 0 {
            try t.Push(". ")
            start = true
            len += 2
        } else if p == 1 && (x >> 48) % 4 == 0 {
            try t.Push("? ")
            start = true
            len += 2
        } else if p == 1 && (x >> 48) % 4 == 1 {
            try t.Push("! ")
            start = true
            len += 2
        } else if p == 2 {
            try t.Push(", ")
            len += 2
        } else {
            try t.Push(" ")
            len += 1
        }
    }
    return try a.Dup(t.Str())
}

// Closes is mode closes' text.
Closes(a: &Arena, n: int): OutOfMemory!str from a {
    var t := Text.New(a)
    try t.Push("a.")
    for _ in 0..n {
        try t.Push(")")
    }
    try t.Push(" ")
    for _ in 0..n {
        try t.Push("1")
    }
    try t.Push("x")
    return try a.Dup(t.Str())
}

Input(a: &Arena, mode: str, mib: int, seed: u64): OutOfMemory!str from a {
    if mode == "closes" {
        return try Closes(a, mib * 1048576 / 2)
    }
    if mode == "prosewords" || mode == "prosesentences" {
        return try Prose(a, mib * 1048576, seed)
    }
    return try Mixed(a, mib * 1048576, seed)
}

export Main(sys: &System): OutOfMemory|ParseError|WriteError!void {
    mode := sys.args[1]
    mib := Cast[int](try sys.args[2].ParseInt())
    reps := Cast[int](try sys.args[3].ParseInt())
    seed := Cast[u64](try sys.args[4].ParseInt())
    s := try Input(sys.arena, mode, mib, seed)
    words := mode == "words" || mode == "prosewords"
    var sum: u64 = 0
    for _ in 0..reps {
        var n: u64 = 0
        var at: u64 = 0
        if words {
            for b in text.WordBounds(s) {
                n += 1
                at = at +% Cast[u64](b)
            }
        } else {
            for b in text.SentenceBounds(s) {
                n += 1
                at = at +% Cast[u64](b)
            }
        }
        sum = sum +% n +% at
    }
    var out := sys.Stdout()
    try Print(mut out, "{sum}\n", (sum))
}
