// jsonl, Mk, std: the task of ../README.md. The file is read through
// fs.File's Reader over a 64 KiB buffer, one line at a time with
// Reader.Line into a line arena that is reset after every line. Each line
// is parsed by json.Parse and decoded into an Event by the FromJson that
// gen json.Codec writes. Kept events are grouped by user in a
// Map[str, Group] made by Map.New; a group's key and user are one copy of
// the name in the program's arena. The groups are sorted with
// List.Sort, and the Report is written by its FormatJson (compact JSON,
// members in declaration order) to stdout, then a line feed.
// Usage: jsonl <file> [<lines>].

import std.fs
import std.json

struct Event {
    export id: i64
    export user: str
    export event: str
    export status: i64
    export bytes: i64
    export ms: i64
    export delta: i64
    export ok: bool
    export tags: []str
    export msg: str
}

gen json.Codec(Event)

struct Group {
    export user: str
    export count: i64
    export first: i64
    export bytes: i64
    export ms_min: i64
    export ms_max: i64
    export delta: i64
    export tags: i64
    export msg: i64
    export buys: i64
    export fails: i64
}

gen json.Codec(Group)

struct Report {
    export lines: i64
    export kept: i64
    export dropped: i64
    export groups: List[Group]
}

gen json.Codec(Report)

// Add counts e into g.
Add(g: mut Group, e: &Event) {
    g.count += 1
    g.bytes += e.bytes
    if e.ms < g.ms_min {
        g.ms_min = e.ms
    }
    if e.ms > g.ms_max {
        g.ms_max = e.ms
    }
    g.delta += e.delta
    g.tags += Cast[i64](e.tags.Len())
    g.msg += Cast[i64](e.msg.Len())
    if e.event == "buy" {
        g.buys += 1
    }
    if e.status >= 400 {
        g.fails += 1
    }
}

// Before is the report's order: more events first, then more bytes, then
// the user name in byte order.
Before(a: &Group, b: &Group): bool {
    if a.count != b.count {
        return a.count > b.count
    }
    if a.bytes != b.bytes {
        return a.bytes > b.bytes
    }
    return a.user < b.user
}

export Main(sys: &System): fs.Error|json.Error|ReadError|ParseError|WriteError|OutOfMemory!void {
    var limit: i64 = -1
    if sys.args.Len() > 2 {
        limit = try sys.args[2].ParseInt()
    }
    var lines := try Arena.New(sys.heap)
    defer lines.Free()
    buf := try sys.arena.Slice[u8](65536)
    var f := try fs.Open(sys.io, sys.args[1])
    onerr f.Abandon()
    var r := f.Reader(sys.io, buf)
    var groups := Map[str, Group].New(sys.arena)
    var n: i64 = 0
    var kept: i64 = 0
    for n != limit {
        line := try r.Line(&lines) else {
            break
        }
        n += 1
        v := try json.Parse(&lines, line)
        e := try Event.FromJson(&lines, &v)
        if !e.ok {
            lines.Reset()
            continue
        }
        kept += 1
        if g := groups.GetMut(&e.user) {
            Add(g, &e)
        } else {
            user := try sys.arena.Dup(e.user)
            var g := Group(user: user, count: 0, first: e.id, bytes: 0, ms_min: e.ms, ms_max: e.ms, delta: 0, tags: 0, msg: 0, buys: 0, fails: 0)
            Add(mut g, &e)
            try groups.Set(user, g)
        }
        lines.Reset()
    }
    try f.Close(sys.io)
    var gs := List[Group].New(sys.arena)
    var it := groups.Iter()
    for x := it.Next() {
        try gs.Push(*x.value)
    }
    gs.Sort(Before)
    rep := Report(lines: n, kept: kept, dropped: n - kept, groups: gs)
    var out := sys.Stdout()
    try rep.FormatJson(mut out)
    try out.Str("\n")
}
