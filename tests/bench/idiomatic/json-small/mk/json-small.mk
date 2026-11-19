// json-small, Mk, idiomatic std: the task of ../README.md with std.json on
// small documents: each line of the file (the generator's array, one
// record per line, its "[", "," or "]" cut off) is one document. Per
// document: json.Parse into a sub-arena made per round, Value.Format of the
// tree into a Text through its Writer (compact JSON), json.Parse of that
// text, the walk of the second tree, then Arena.Reset. Usage:
// json-small <file> <rounds>. Prints the checksum.

import std.fs
import std.json

// Walk is the order-independent checksum of ../README.md (json's), wrapping.
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

// Doc cuts the array's punctuation off one line: a leading "[" and a
// trailing "," or "]".
Doc(line: str): str {
    var d := line
    if d.StartsWith("[") {
        d = d[1..]
    }
    if d.EndsWith(",") || d.EndsWith("]") {
        d = d[..d.Len() - 1]
    }
    return d
}

// Round runs the documents of text once, in a sub-arena of h (as json's
// Round). Per document: json.Parse into a, Value.Format of the tree into t
// through its Writer, json.Parse of t's text into a, the walk, then
// a.Reset(), which ends every loan the trees held.
Round(h: &Arena, text: str, t: mut Text): json.Error|WriteError!i64 {
    var a := try h.Sub()
    defer a.Free()
    var total: i64 = 0
    var lines := text.Lines()
    for line := lines.Next() {
        d := Doc(line)
        if d.Len() == 0 {
            continue
        }
        v := try json.Parse(&a, d)
        t.Clear()
        var w := t.Writer()
        try v.Format(mut w)
        u := try json.Parse(&a, t.Str())
        total = total +% Walk(&u)
        a.Reset()
    }
    return total
}

export Main(sys: &System): fs.Error|json.Error|WriteError|ParseError!void {
    text := try fs.ReadText(sys.arena, sys.io, sys.args[1])
    rounds := Cast[int](try sys.args[2].ParseInt())
    var t := Text.New(sys.arena)
    var total: i64 = 0
    for _ in 0..rounds {
        total = total +% try Round(sys.arena, text, mut t)
    }
    try sys.Print("{total}\n", (total))
}
