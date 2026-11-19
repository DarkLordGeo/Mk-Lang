// json-codec, Mk: decode a 1 MB document (tools/bench gen kind json) into
// structs, reps times: std.json.Parse into a Value, then the FromJson that
// gen json.Codec emits for each record, pushed into a List, all in one arena
// that is Reset after each rep; then a sum over the records (README.md).
// Usage: json-codec <file> <reps>. Prints the checksum.
//
// The records nest through "next" at most three deep, and a Mk struct
// cannot hold itself, so the levels are four types; R3 has no next field,
// and its "next" (always null) is an unknown member, ignored.

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

// RecSum is one record's share of the checksum: 1, its id, its name's
// length, its score truncated, its tags' count and lengths, 1 if ok.
RecSum(id: i64, name: str, score: f64, tags: &List[str], ok: bool): i64 {
    var n: i64 = 1 +% id +% Cast[i64](name.Len()) +% Cast[i64](score)
    ts := tags.Items()
    n = n +% Cast[i64](ts.Len())
    for k in 0..ts.Len() {
        n = n +% Cast[i64](ts[k].Len())
    }
    if ok {
        n = n +% 1
    }
    return n
}

Sum3(r: &R3): i64 {
    return RecSum(r.id, r.name, r.score, &r.tags, r.ok)
}

Sum2(r: &R2): i64 {
    var n := RecSum(r.id, r.name, r.score, &r.tags, r.ok)
    match &r.next {
        some(x) {
            n = n +% Sum3(x)
        }
        none {}
    }
    return n
}

Sum1(r: &R1): i64 {
    var n := RecSum(r.id, r.name, r.score, &r.tags, r.ok)
    match &r.next {
        some(x) {
            n = n +% Sum2(x)
        }
        none {}
    }
    return n
}

Sum0(r: &R0): i64 {
    var n := RecSum(r.id, r.name, r.score, &r.tags, r.ok)
    match &r.next {
        some(x) {
            n = n +% Sum1(x)
        }
        none {}
    }
    return n
}

// Rep parses text, decodes every record into a List of R0, and sums them.
Rep(a: &Arena, text: str into a): json.Error!i64 {
    v := try json.Parse(a, text)
    var l := List[R0].New(a)
    match &v {
        .Array(items) {
            xs := items.Items()
            for k in 0..xs.Len() {
                r := try R0.FromJson(a, &xs[k])
                try l.Push(r)
            }
        }
        _ {
            fail json.Mismatch(offset: -1)
        }
    }
    var n: i64 = 0
    rs := l.Items()
    for k in 0..rs.Len() {
        n = n +% Sum0(&rs[k])
    }
    return n
}

export Main(sys: &System): fs.Error|OutOfMemory|ParseError|WriteError|Utf8Error|json.Error!void {
    // The bytes are read once; each rep turns them into a str (one UTF-8
    // pass, str.From), since Parse takes a str and the Go and Zig decoders
    // check UTF-8 on every parse too.
    bytes := try fs.ReadBytes(sys.arena, sys.io, sys.args[1])
    reps := Cast[int](try sys.args[2].ParseInt())
    var a := try Arena.New(sys.heap)
    var total: i64 = 0
    for _ in 0..reps {
        text := str.From(bytes) else {
            a.Free()
            fail Utf8Error
        }
        match Rep(&a, text) {
            ok(n) {
                total = total +% n
            }
            err(e) {
                a.Free()
                fail e
            }
        }
        a.Reset()
    }
    a.Free()
    var out := sys.Stdout()
    try Print(mut out, "{total}\n", (total: total))
}
