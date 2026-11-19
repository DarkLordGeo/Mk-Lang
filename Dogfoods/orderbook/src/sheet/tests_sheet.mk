// Why: the book and fills files must read back as they were written.
// What: round-trip tests of both files, and saves under simulated crashes
// and faults.
// How: SampleOrders and SaveSample drive std.sim's disk.

import std.csv
import std.fs
import std.sim
import src.book

/// Three live orders, one of each kind that rests.
SampleOrders(): [3]book.Order {
    return [
        book.Order(id: book.ClientId(account: 7, serial: 1), seq: 0, side: book.Side.Buy, kind: book.Kind.Limit, price: 10125, stop: 0, qty: 50, left: 40, ts: 3),
        book.Order(id: book.ClientId(account: 8, serial: 2), seq: 1, side: book.Side.Sell, kind: book.Kind.Stop, price: 0, stop: 9950, qty: 15, left: 15, ts: 4),
        book.Order(id: book.ClientId(account: 9, serial: 3), seq: 2, side: book.Side.Sell, kind: book.Kind.StopLimit, price: 10030, stop: 10040, qty: 10, left: 10, ts: 5),
    ]
}

/// The book of SampleOrders, with tags that need quoting, saved to `path`.
SaveSample(io: Io, a: &Arena, path: str, opening: &Opening): fs.Error|OutOfMemory|WriteError!void {
    orders := SampleOrders()
    o := csv.DefaultOptions
    try Save(io, a, path, fn(w: mut Writer): OutOfMemory|WriteError!void {
        try WriteBook(w, a, orders[..], opening, &o)
    })
}

/// An opening whose orders 7/1 and 9/3 carry tags with a delimiter, a line
/// break and a quote.
TaggedOpening(a: &Arena): OutOfMemory!Opening from a {
    var opening := Opening.Empty(a)
    try opening.tags.Set(book.ClientId(account: 7, serial: 1), "desk 4, hedge")
    try opening.tags.Set(book.ClientId(account: 9, serial: 3), "line one\nsays \"urgent\"")
    return opening
}

test "a written book reads back to the same orders and tags" (sys: &System) {
    var s := try sim.Sim.New(sys.arena, 1)
    defer s.Free()
    tagged := try TaggedOpening(sys.arena)
    try SaveSample(s.Io(), sys.arena, "book.csv", &tagged)
    back := LoadBook(sys.arena, s.Io(), "book.csv") else {
        Assert(false)
        return
    }
    Assert(back.commands.Len() == 3)
    for i, order in SampleOrders() {
        want := book.Command.Add(ts: order.ts, id: order.id, side: order.side, kind: order.kind, qty: order.left, price: order.price, stop: order.stop)
        Assert(back.commands[i].Eq(&want))
        Assert(back.Tag(&order.id) == tagged.Tag(&order.id))
    }
}

test "a crash while saving the book leaves the old file or the new one" (sys: &System) {
    plain := Opening.Empty(sys.arena)
    tagged := try TaggedOpening(sys.arena)
    id := book.ClientId(account: 7, serial: 1)
    for seed in 0..100 {
        var s := try sim.Sim.New(sys.arena, Cast[u64](seed))
        defer s.Free()
        SaveSample(s.Io(), sys.arena, "book.csv", &plain) else {
            Assert(false)
        }
        s.CrashAfterRandomOps()
        SaveSample(s.Io(), sys.arena, "book.csv", &tagged) else {}
        io := s.Restart()
        back := LoadBook(sys.arena, io, "book.csv") else {
            Assert(false)
            return
        }
        Assert(back.commands.Len() == 3)
        tag := back.Tag(&id)
        Assert(tag == "" || tag == "desk 4, hedge")
    }
}

test "a full disk fails the save and keeps the old book" (sys: &System) {
    var s := try sim.Sim.New(sys.arena, 7)
    defer s.Free()
    plain := Opening.Empty(sys.arena)
    tagged := try TaggedOpening(sys.arena)
    try SaveSample(s.Io(), sys.arena, "book.csv", &plain)
    s.FailNext(sim.Op.Write, sim.Fault.NoSpace)
    var failed := false
    SaveSample(s.Io(), sys.arena, "book.csv", &tagged) else {
        failed = true
    }
    Assert(failed)
    back := LoadBook(sys.arena, s.Io(), "book.csv") else {
        Assert(false)
        return
    }
    Assert(back.Tag(&book.ClientId(account: 7, serial: 1)) == "")
}

test "fills are written one record each, tags quoted" (sys: &System) {
    tagged := try TaggedOpening(sys.arena)
    fills: [1]book.Fill = [
        book.Fill(taker: book.ClientId(account: 1, serial: 1), maker: book.ClientId(account: 7, serial: 1), taker_seq: 3, maker_seq: 0, side: book.Side.Sell, price: 10125, qty: 5, ts: 9),
    ]
    var text := Text.New(sys.arena)
    var w := text.Writer()
    o := csv.DefaultOptions
    try WriteFills(mut w, sys.arena, fills[..], &tagged, &o)
    Assert(text.Str() == "ts,taker,maker,side,price,qty,taker_tag,maker_tag\r\n9,1/1,7/1,S,101.25,5,,\"desk 4, hedge\"\r\n")
}
