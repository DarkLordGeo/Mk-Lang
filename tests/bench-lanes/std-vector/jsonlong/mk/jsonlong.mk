// jsonlong, Mk: std.json on a document of
// long strings, where the string scan's width shows. Usage: jsonlong
// <mode> <strings> <reps> <seed>; mode parse parses the document reps
// times (an arena Reset after each) and sums the decoded string bytes, mode
// write parses it once and formats it reps times into a Text and sums the
// lengths. The document is one array of <strings> strings built from
// splitmix64(seed): each 100 to 1,099 bytes of printable ASCII other than
// '"' and '\', with an escape (\n or \") about every 256 bytes (README.md).

import std.json

struct Mix {
    x: u64
}

Next(m: mut Mix): u64 {
    m.x = m.x +% 0x9E3779B97F4A7C15
    var z := m.x
    z = (z ^ (z >> 30)) *% 0xBF58476D1CE4E5B9
    z = (z ^ (z >> 27)) *% 0x94D049BB133111EB
    return z ^ (z >> 31)
}

Doc(a: &Arena, strings: int, seed: u64): OutOfMemory|Utf8Error!str from a {
    var m := Mix(x: seed)
    var t := Text.New(a)
    var w := t.Writer()
    w.Str("[") else {
        fail OutOfMemory
    }
    var buf: [1100]u8 = [0; 1100]
    for k in 0..strings {
        if k > 0 {
            w.Str(",") else {
                fail OutOfMemory
            }
        }
        n := 100 + Cast[int](Next(mut m) % 1000)
        var i := 0
        buf[i] = 0x22
        i += 1
        for i < n {
            r := Next(mut m)
            if r % 256 == 0 && i + 2 < n {
                buf[i] = 0x5C
                if (r >> 8) % 2 == 0 {
                    buf[i + 1] = 0x6E
                } else {
                    buf[i + 1] = 0x22
                }
                i += 2
            } else {
                var c := Trunc[u8](0x20 + (r >> 16) % 95)
                if c == 0x22 || c == 0x5C {
                    c = 0x61
                }
                buf[i] = c
                i += 1
            }
        }
        buf[n] = 0x22
        w.Str(str.From(buf[0..n + 1]).Must()) else {
            fail OutOfMemory
        }
    }
    w.Str("]") else {
        fail OutOfMemory
    }
    return try a.Dup(t.Str())
}

export Main(sys: &System): OutOfMemory|ParseError|WriteError|Utf8Error|json.Error!void {
    mode := sys.args[1]
    strings := Cast[int](try sys.args[2].ParseInt())
    reps := Cast[int](try sys.args[3].ParseInt())
    seed := Cast[u64](try sys.args[4].ParseInt())
    text := try Doc(sys.arena, strings, seed)
    var total: u64 = 0
    if mode == "parse" {
        var a := try Arena.New(sys.heap)
        for _ in 0..reps {
            match json.Parse(&a, text) {
                ok(v) {
                    match v {
                        .Array(items) {
                            xs := items.Items()
                            for k in 0..xs.Len() {
                                if s := xs[k].Str() {
                                    total = total +% Cast[u64](s.Len())
                                }
                            }
                        }
                        _ {}
                    }
                }
                err(e) {
                    a.Free()
                    fail e
                }
            }
            a.Reset()
        }
        a.Free()
    } else {
        v := try json.Parse(sys.arena, text)
        var t := Text.New(sys.arena)
        for _ in 0..reps {
            t.Clear()
            var w := t.Writer()
            try v.Format(mut w)
            total = total +% Cast[u64](t.Str().Len())
        }
    }
    var out := sys.Stdout()
    try Print(mut out, "{total}\n", (total: total))
}
