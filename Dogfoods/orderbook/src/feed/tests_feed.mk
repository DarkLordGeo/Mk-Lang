// Why: the feed parser decides what a malformed market-data file reports.
// What: tests of single lines and of whole files read through std.csv.
// How: writes files to a simulated disk (std.sim) and parses them back.

import std.csv
import std.fs
import std.sim
import src.book

/// Parses one line of comma-separated fields without quotes, as a feed
/// line with no quoted field reads through std.csv.
ParseText(line: str, n: int): Error!?book.Command {
    var cols: [7]str = [""; 7]
    var count := 0
    for f in line.Split(",") {
        if count < MaxFields {
            cols[count] = f.Trim()
        }
        count += 1
    }
    held := if count < MaxFields { count } else { MaxFields }
    return try ParseFields(cols[..held], count, n)
}

/// Writes `text` to the file `path` of `io`.
WriteFile(a: &Arena, io: Io, path: str, text: str): fs.Error|OutOfMemory|WriteError!void {
    buf := try a.Slice[u8](4096)
    var f := try fs.Create(io, path)
    onerr f.Abandon()
    var w := f.Writer(io, buf)
    try w.Str(text)
    try w.Flush()
    try f.Close(io)
}

/// Every command of the feed in the file `path` of `io`, in order, and the
/// first error, if any, as text.
ReadFeed(a: &Arena, io: Io, path: str): fs.Error|OutOfMemory!(cmds: List[book.Command], failure: str) from a {
    var cmds := List[book.Command].New(a)
    rbuf := try a.Slice[u8](512)
    cbuf := try a.Slice[u8](4096)
    o := FeedOptions()
    var f := try fs.Open(io, path)
    onerr f.Abandon()
    var r := f.Reader(io, rbuf)
    var records := csv.Records.New(mut r, cbuf, &o)
    var failure := ""
    for {
        next := records.Next() else e {
            failure = try a.Fmt("{e}", (e))
            break
        }
        rec := next else {
            break
        }
        parsed := ParseRecord(&rec) else e {
            failure = try a.Fmt("{e}", (e))
            break
        }
        if cmd := parsed {
            try cmds.Push(cmd)
        }
    }
    try f.Close(io)
    return (cmds: cmds, failure: failure)
}

test "a limit order parses with its price in ticks" {
    cmd := ParseText("N,5,7/1,B,10,101.5", 1).Must().Must()
    match cmd {
        .Add(ts, id, side, kind, qty, price, stop) {
            Assert(ts == 5 && id.account == 7 && id.serial == 1)
            Assert(side == book.Side.Buy && kind == book.Kind.Limit)
            Assert(qty == 10 && price == 10150 && stop == 0)
        }
        _ { Assert(false) }
    }
}

test "comments and blank lines are skipped" {
    Assert(ParseText("# header", 1).Must().IsNone())
    Assert(ParseText("   ", 2).Must().IsNone())
}

test "a bad side names its field" {
    match ParseText("N,5,7/1,Q,10,101.5", 9) {
        err(e) {
            match e {
                BadField(line, field) { Assert(line == 9 && field == 4) }
                _ { Assert(false) }
            }
        }
        ok(v) {
            _ = v
            Assert(false)
        }
    }
}

test "a record longer than any command counts all its fields" {
    match ParseText("N,5,7/1,B,10,101.5,1,2,3", 4) {
        err(e) {
            match e {
                FieldCount(line, want, got) { Assert(line == 4 && want == 6 && got == 9) }
                _ { Assert(false) }
            }
        }
        ok(v) {
            _ = v
            Assert(false)
        }
    }
}

test "quoted fields, CR LF and comments with commas read as the plain feed" (sys: &System) {
    var s := try sim.Sim.New(sys.arena, 1)
    defer s.Free()
    text := "# a comment, with commas\r\n\"N\",1,\"7/1\",B,10, 101.50 \r\n\r\nC,2,7/1\r\n"
    try WriteFile(sys.arena, s.Io(), "feed.csv", text)
    got := try ReadFeed(sys.arena, s.Io(), "feed.csv")
    Assert(got.failure == "")
    Assert(got.cmds.Len() == 2)
    want := book.Command.Add(ts: 1, id: book.ClientId(account: 7, serial: 1), side: book.Side.Buy, kind: book.Kind.Limit, qty: 10, price: 10150, stop: 0)
    Assert(got.cmds[0].Eq(&want))
}

test "a malformed quote and an oversized field stop the read" (sys: &System) {
    var s := try sim.Sim.New(sys.arena, 2)
    defer s.Free()
    try WriteFile(sys.arena, s.Io(), "quote.csv", "N,1,\"7/1\"x,B,10,1.00\n")
    q := try ReadFeed(sys.arena, s.Io(), "quote.csv")
    Assert(q.failure.StartsWith("Quote(line: 1"))
    var long := Text.New(sys.arena)
    try long.Push("N,1,7/1,B,")
    for _ in 0..300 {
        try long.Push("1")
    }
    try WriteFile(sys.arena, s.Io(), "long.csv", long.Str())
    l := try ReadFeed(sys.arena, s.Io(), "long.csv")
    Assert(l.failure == "TooLarge(line: 1)")
}

test "a read fault reaches the caller as a ReadError" (sys: &System) {
    var s := try sim.Sim.New(sys.arena, 3)
    defer s.Free()
    try WriteFile(sys.arena, s.Io(), "feed.csv", "N,1,7/1,B,10,1.00\n")
    s.FailNext(sim.Op.Read, sim.Fault.Denied)
    got := try ReadFeed(sys.arena, s.Io(), "feed.csv")
    Assert(got.failure == "ReadError")
}
