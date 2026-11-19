// json-codec-write, Mk: decode a 1 MB document (tools/bench gen kind json)
// into a List of R0 once (std.json.Parse, then the FromJson that gen
// json.Codec emits per record), then write the records back as one compact
// JSON array, reps times, into one Text through its Writer, cleared before
// each: "[", the FormatJson that gen json.Codec emits per record with ","
// between, "]". Usage: json-codec-write <file> <reps>. Prints the checksum:
// reps times the count of the bytes { } [ ] : , and " in the last output,
// which every language writes alike whatever its float digits (README.md).
//
// The records nest through "next" at most three deep, and a Mk struct
// cannot hold itself, so the levels are four types; R3 has no next field,
// and its "next" (always null) is an unknown member, ignored on the read.

import std.fs
import std.json

struct R3 {
    export id: i64
    export name: str
    export score: f64
    export tags: List[str]
    export ok: bool
}

struct R2 {
    export id: i64
    export name: str
    export score: f64
    export tags: List[str]
    export ok: bool
    export next: ?R3
}

struct R1 {
    export id: i64
    export name: str
    export score: f64
    export tags: List[str]
    export ok: bool
    export next: ?R2
}

struct R0 {
    export id: i64
    export name: str
    export score: f64
    export tags: List[str]
    export ok: bool
    export next: ?R1
}

gen json.Codec(R3)
gen json.Codec(R2)
gen json.Codec(R1)
gen json.Codec(R0)

// Structural counts the bytes of s that are { } [ ] : , or ".
Structural(s: str): u64 {
    b := s.AsBytes()
    var n: u64 = 0
    for k in 0..b.Len() {
        c := b[k]
        if c == 0x7B || c == 0x7D || c == 0x5B || c == 0x5D || c == 0x3A || c == 0x2C || c == 0x22 {
            n += 1
        }
    }
    return n
}

export Main(sys: &System): fs.Error|OutOfMemory|ParseError|WriteError|json.Error!void {
    text := try fs.ReadText(sys.arena, sys.io, sys.args[1])
    reps := Cast[int](try sys.args[2].ParseInt())
    v := try json.Parse(sys.arena, text)
    var l := List[R0].New(sys.arena)
    match &v {
        .Array(items) {
            xs := items.Items()
            for k in 0..xs.Len() {
                r := try R0.FromJson(sys.arena, &xs[k])
                try l.Push(r)
            }
        }
        _ {
            fail json.Mismatch(offset: -1)
        }
    }
    rs := l.Items()
    var t := Text.New(sys.arena)
    for _ in 0..reps {
        t.Clear()
        var w := t.Writer()
        try w.Str("[")
        for k in 0..rs.Len() {
            if k > 0 {
                try w.Str(",")
            }
            try rs[k].FormatJson(mut w)
        }
        try w.Str("]")
    }
    total := Structural(t.Str()) *% Cast[u64](reps)
    var out := sys.Stdout()
    try Print(mut out, "{total}\n", (total: total))
}
