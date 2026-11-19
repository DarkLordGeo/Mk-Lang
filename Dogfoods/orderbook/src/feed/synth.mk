// Why: load tests and very large replays need input that is not kept as files.
// What: Synth, an iterator of commands from a seed, and WriteLine.
// How: a seeded generator remembers recent client ids for cancels and
// amends; the same seed gives the same stream on every target and profile.

import src.book

/// How many recent client ids the generator remembers for cancels and
/// amends.
const Recent: int = 256

/// How many accounts send orders.
const Accounts: u32 = 64

/// The generator. An iterator: `for cmd in feed.Synth.New(n, seed)`.
export struct Synth {
    state: u64
    left: int
    ts: i64
    mid: i64
    serials: [64]u32
    recent: [256]book.ClientId
    seen: int
}

/// A generator of `n` commands from `seed`. O(1).
export Synth.New(n: int, seed: u64): Synth {
    return Synth(
        state: seed ^ 0x9e3779b97f4a7c15,
        left: n,
        ts: 34_200_000,
        mid: 10_000,
        serials: [0; 64],
        recent: [book.ClientId(account: 0, serial: 0); 256],
        seen: 0,
    )
}

/// The next pseudo-random word: xorshift64*, which never yields the same
/// state twice before 2^64 - 1 steps when the state is not zero.
Synth.Word(mut self): u64 {
    if self.state == 0 {
        self.state = 0x2545f4914f6cdd1d
    }
    var x := self.state
    x = x ^ (x >> 12)
    x = x ^ (x << 25)
    x = x ^ (x >> 27)
    self.state = x
    return x *% 0x2545f4914f6cdd1d
}

/// A number from 0 to n - 1; n is above 0.
Synth.Below(mut self, n: int): int {
    return Cast[int](self.Word() % Cast[u64](n))
}

/// A fresh client id from a random account.
Synth.Fresh(mut self): book.ClientId {
    account := Cast[u32](self.Below(Cast[int](Accounts)))
    self.serials[account] += 1
    id := book.ClientId(account: account + 1, serial: self.serials[account])
    self.recent[self.seen % Recent] = id
    self.seen += 1
    return id
}

/// An id issued earlier, possibly long since filled or cancelled.
Synth.Old(mut self): book.ClientId {
    if self.seen == 0 {
        return book.ClientId(account: 1, serial: 1)
    }
    n := if self.seen < Recent { self.seen } else { Recent }
    return self.recent[self.Below(n)]
}

/// A side, evenly.
Synth.Side(mut self): book.Side {
    return if self.Below(2) == 0 { book.Side.Buy } else { book.Side.Sell }
}

/// A quantity: mostly small, sometimes a block.
Synth.Qty(mut self): i64 {
    q := Cast[i64](1 + self.Below(20)) * 10
    return if self.Below(50) == 0 { q * 25 } else { q }
}

/// The next command, none after the n-th.
export Synth.Next(mut self): ?book.Command {
    if self.left == 0 {
        return none
    }
    self.left -= 1
    self.ts += Cast[i64](1 + self.Below(3))
    if self.Below(64) == 0 {
        // A slow random walk of the reference price.
        self.mid += Cast[i64](self.Below(7)) - 3
        if self.mid < 1_000 {
            self.mid = 1_000
        }
    }
    ts := self.ts
    roll := self.Below(100)
    if roll < 55 {
        side := self.Side()
        off := Cast[i64](self.Below(12))
        price := if side == book.Side.Buy { self.mid + 2 - off } else { self.mid - 2 + off }
        return some(book.Command.Add(ts: ts, id: self.Fresh(), side: side, kind: book.Kind.Limit, qty: self.Qty(), price: price, stop: 0))
    }
    if roll < 61 {
        return some(book.Command.Add(ts: ts, id: self.Fresh(), side: self.Side(), kind: book.Kind.Market, qty: self.Qty(), price: 0, stop: 0))
    }
    if roll < 80 {
        return some(book.Command.Cancel(ts: ts, id: self.Old()))
    }
    if roll < 85 {
        price := self.mid + Cast[i64](self.Below(9)) - 4
        return some(book.Command.Amend(ts: ts, id: self.Old(), qty: self.Qty(), price: price))
    }
    if roll < 90 {
        side := self.Side()
        gap := Cast[i64](3 + self.Below(8))
        stop := if side == book.Side.Buy { self.mid + gap } else { self.mid - gap }
        return some(book.Command.Add(ts: ts, id: self.Fresh(), side: side, kind: book.Kind.Stop, qty: self.Qty(), price: 0, stop: stop))
    }
    if roll < 93 {
        side := self.Side()
        gap := Cast[i64](3 + self.Below(8))
        stop := if side == book.Side.Buy { self.mid + gap } else { self.mid - gap }
        limit := if side == book.Side.Buy { stop + 2 } else { stop - 2 }
        return some(book.Command.Add(ts: ts, id: self.Fresh(), side: side, kind: book.Kind.StopLimit, qty: self.Qty(), price: limit, stop: stop))
    }
    if roll < 97 {
        side := self.Side()
        off := Cast[i64](20 + self.Below(200))
        price := if side == book.Side.Buy { self.mid - off } else { self.mid + off }
        return some(book.Command.Add(ts: ts, id: self.Fresh(), side: side, kind: book.Kind.Limit, qty: self.Qty(), price: price, stop: 0))
    }
    if roll == 97 {
        return some(book.Command.Bust(ts: ts))
    }
    // Halts are rare and resumes common, so that few accounts are halted
    // at any one time.
    account := Cast[u32](1 + self.Below(Cast[int](Accounts)))
    halt := roll == 98 && self.Below(8) == 0
    return some(if halt { book.Command.Halt(ts: ts, account: account) } else { book.Command.Resume(ts: ts, account: account) })
}

/// Writes `cmd` as one feed line, with its newline. A Trigger, which no
/// feed holds, writes nothing.
export WriteLine(w: mut Writer, cmd: &book.Command): WriteError!void {
    match *cmd {
        .Add(ts, id, side, kind, qty, price, stop) {
            s := if side == book.Side.Buy { "B" } else { "S" }
            match kind {
                .Limit {
                    try Print(w, "N,{ts},{id},{s},{qty},", (ts, id, s, qty))
                    try book.WritePrice(w, price)
                }
                .Market {
                    try Print(w, "M,{ts},{id},{s},{qty}", (ts, id, s, qty))
                }
                .Stop {
                    try Print(w, "S,{ts},{id},{s},{qty},", (ts, id, s, qty))
                    try book.WritePrice(w, stop)
                }
                .StopLimit {
                    try Print(w, "T,{ts},{id},{s},{qty},", (ts, id, s, qty))
                    try book.WritePrice(w, stop)
                    try w.Str(",")
                    try book.WritePrice(w, price)
                }
            }
        }
        .Cancel(ts, id) {
            try Print(w, "C,{ts},{id}", (ts, id))
        }
        .Amend(ts, id, qty, price) {
            try Print(w, "A,{ts},{id},{qty},", (ts, id, qty))
            try book.WritePrice(w, price)
        }
        .Halt(ts, account) {
            try Print(w, "H,{ts},{account}", (ts, account))
        }
        .Resume(ts, account) {
            try Print(w, "R,{ts},{account}", (ts, account))
        }
        .Bust(ts) {
            try Print(w, "X,{ts}", (ts))
        }
        .Trigger(handle) {
            _ = handle
            return
        }
    }
    try w.Str("\n")
}

test "the same seed gives the same stream" {
    var a := Synth.New(500, 42)
    var b := Synth.New(500, 42)
    var n := 0
    for x := a.Next() {
        y := b.Next().Must()
        Assert(x.Eq(&y))
        n += 1
    }
    Assert(n == 500)
    Assert(b.Next().IsNone())
}

test "a written line parses back to its command" (sys: &System) {
    var text := Text.New(sys.arena)
    var w := text.Writer()
    var line := 0
    for cmd in Synth.New(2000, 7) {
        try WriteLine(mut w, &cmd)
    }
    for l in text.Str().Lines() {
        line += 1
        _ = l
    }
    Assert(line == 2000)
    var again := Synth.New(2000, 7)
    var n := 0
    for l in text.Str().Lines() {
        n += 1
        got := ParseText(l, n) else {
            Assert(false)
            return
        }
        want := again.Next().Must()
        Assert(got.Must().Eq(&want))
    }
}
