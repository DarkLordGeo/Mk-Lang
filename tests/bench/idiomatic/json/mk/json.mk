// json, Mk, idiomatic std: the task of ../README.md with std.json. Each
// round: json.Parse of the document into a sub-arena (numbers kept as
// text, strings views of the text unless escaped), Value.Format of the
// tree into a Text through its Writer (compact JSON), json.Parse of that
// text, and the walk of the second tree. Usage: json <file> <rounds>.
// Prints the checksum.

import std.fs
import std.json

// Walk is the order-independent checksum of README.md, wrapping.
Walk(v: &json.Value): i64 {
    match v {
        .Null {
            return 7
        }
        .Bool(b) {
            if *b {
                return 3
            }
            return 5
        }
        .Number(text) {
            match text.ParseFloat() {
                ok(f) {
                    return 1 +% Cast[i64](f)
                }
                err(_) {
                    return 0
                }
            }
        }
        .Text(s) {
            return 1 +% Cast[i64](s.Len())
        }
        .Array(items) {
            var n: i64 = 1
            xs := items.Items()
            for k in 0..xs.Len() {
                n = n +% Walk(&xs[k])
            }
            return n
        }
        .Object(members) {
            var n: i64 = 1
            ms := members.Items()
            for k in 0..ms.Len() {
                n = n +% 1 +% Cast[i64](ms[k].key.Len()) +% Walk(&ms[k].value)
            }
            return n
        }
    }
}

// Serialise writes v as compact JSON into t.
Serialise(t: mut Text, v: &json.Value): WriteError!void {
    t.Clear()
    var w := t.Writer()
    try v.Format(mut w)
}

Round(h: &Arena, text: str, t: mut Text): json.Error|WriteError!i64 {
    var a := try h.Sub()
    defer a.Free()
    v := try json.Parse(&a, text)
    try Serialise(t, &v)
    out := t.Str()
    w := try json.Parse(&a, out)
    return Walk(&w)
}

export Main(sys: &System): fs.Error|json.Error|WriteError|ParseError!void {
    text := try fs.ReadText(sys.arena, sys.io, sys.args[1])
    rounds := Cast[int](try sys.args[2].ParseInt())
    var t := Text.New(sys.arena)
    var total: i64 = 0
    for _ in 0..rounds {
        total = total +% try Round(sys.arena, text, mut t)
    }
    var out := sys.Stdout()
    try Print(mut out, "{total}\n", (total))
}
