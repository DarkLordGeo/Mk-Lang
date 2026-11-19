// Why: each side of the book must find its best price and the oldest order
// at it in a few steps, however deep the book is.
// What: Ladder, one side of the book; Level, Depth and Expired.
// How: price levels live in a B-tree, each a FIFO of order handles, so the
// first key is the best price and the front of its queue the oldest order.
// Dead handles stay queued until compaction; Level counts live ones only.

import std.coll
import std.mem

/// The orders resting at one price, oldest first, with their total
/// remaining quantity and their number. A queue may still hold handles of
/// dead orders; `qty` and `live` count live ones only.
export struct Level {
    export qty: i64
    export live: int
    export orders: coll.HeapDeque[mem.Handle]
}

impl Free for Level {
    Free(self, a: &Alloc) {
        Level(orders, ..) := self
        orders.Free(a)
    }
}

/// A side of the book. Keys are prices times `sign`, 1 for asks and -1 for
/// bids, so that ascending key order is best price first on both sides.
export view linear struct Ladder {
    levels: coll.HeapBTree[i64, Level]
    sign: i64
}

/// One price level as the depth table shows it.
export struct Depth {
    export price: i64
    export qty: i64
    export orders: int
}

/// An empty side for `side`'s resting orders, on `a`. O(1).
export Ladder.New(a: &Alloc, side: Side): Ladder {
    sign: i64 = if side == Side.Buy { -1 } else { 1 }
    return Ladder(levels: coll.HeapBTree[i64, Level].New(a), sign: sign)
}

impl Free for Ladder {
    Free(self, a: &Alloc) {
        Ladder(levels, ..) := self
        levels.Free(a)
    }
}

/// The best price on this side, if any order rests.
export Ladder.Best(&self): ?i64 {
    var it := self.levels.Iter()
    first := it.Next() else {
        return none
    }
    return some(*first.key * self.sign)
}

/// Whether an incoming order at `limit` from the other side trades against
/// a level at `price` on this one.
export Ladder.Crosses(&self, price: i64, limit: i64): bool {
    return if self.sign > 0 { price <= limit } else { price >= limit }
}

/// The number of price levels.
export Ladder.LevelCount(&self): int {
    return self.levels.Len()
}

/// Appends the order `h` at `price` with `qty` remaining, behind every
/// order already there. Fails with OutOfMemory when `a` cannot hold it.
export Ladder.Rest(mut self, a: &Alloc into self, price: i64, qty: i64, h: mem.Handle): OutOfMemory!void {
    key := price * self.sign
    if lvl := self.levels.GetMut(&key) {
        try lvl.orders.Push(a, h)
        lvl.qty += qty
        lvl.live += 1
        return
    }
    var orders := coll.HeapDeque[mem.Handle].New(a)
    onerr orders.Free(a)
    try orders.Push(a, h)
    lvl := Level(qty: qty, live: 1, orders: orders)
    onerr lvl.Free(a)
    try self.levels.Set(a, key, lvl)
}

/// Takes `qty` off the level at `price` for an order that left the book
/// without trading, `h`; when `h` is the newest order there, its handle
/// goes at once. An emptied level is removed.
export Ladder.Withdraw(mut self, a: &Alloc, price: i64, qty: i64, h: mem.Handle) {
    key := price * self.sign
    lvl := self.levels.GetMut(&key) else {
        return
    }
    lvl.qty -= qty
    lvl.live -= 1
    if last := lvl.orders.Get(lvl.orders.Len() - 1) {
        if last.Eq(&h) {
            _ = lvl.orders.Pop()
        }
    }
    if lvl.qty == 0 {
        self.Drop(a, key)
    }
}

/// Rebuilds the queue at `price` without the handles of dead orders once
/// they outnumber the live ones by more than CompactSlack, so that a level
/// where most orders are cancelled does not grow without bound. Fails with
/// OutOfMemory when the new queue cannot be allocated; the old one is kept.
export Ladder.Compact(mut self, a: &Alloc into self, price: i64, book: &Orders): OutOfMemory!void {
    key := price * self.sign
    if !self.levels.Has(&key) {
        return
    }
    old := self.levels.Get(&key) else {
        return
    }
    if old.orders.Len() <= 2 * old.live + CompactSlack {
        return
    }
    var orders := coll.HeapDeque[mem.Handle].New(a)
    onerr orders.Free(a)
    for i in 0..old.orders.Len() {
        h := *(old.orders.Get(i) else {
            break
        })
        if book.Live(h).IsSome() {
            try orders.Push(a, h)
        }
    }
    fresh := Level(qty: old.qty, live: old.live, orders: orders)
    onerr fresh.Free(a)
    if replaced := try self.levels.Replace(a, key, fresh) {
        replaced.Free(a)
    }
}

/// How many dead handles a queue may hold beyond twice its live orders.
const CompactSlack: int = 8

/// What a session close expired on one side.
export struct Expired {
    export orders: int
    export qty: i64
}

/// Closes the session on this side: every resting order expires, as day
/// orders do, and the side is released. Returns what expired.
export Ladder.Close(self, a: &Alloc, book: &Orders): Expired {
    Ladder(levels, ..) := self
    if levels.Len() == 0 {
        levels.FreeEmpty(a)
        return Expired(orders: 0, qty: 0)
    }
    // Each level's queue is released handle by handle, counting the live
    // orders it held; the counts go through Cells because a callback reads
    // what it captures and never writes it.
    count := Cell[int].New(0)
    qty := Cell[i64].New(0)
    levels.FreeEach(a, fn(price: i64, lvl: Level) {
        _ = price
        qty.Store(qty.Load() + lvl.qty)
        Level(orders, ..) := lvl
        orders.FreeEach(a, fn(h: mem.Handle) {
            if book.Live(h).IsSome() {
                count.Store(count.Load() + 1)
            }
        })
    })
    return Expired(orders: count.Load(), qty: qty.Load())
}

/// Removes the level under `key` and frees its queue.
Ladder.Drop(mut self, a: &Alloc, key: i64) {
    if old := self.levels.Remove(&key) {
        old.Free(a)
    }
}

/// Matches the live order `taker` against this side, best price first and
/// oldest first within a price, until it is filled, the next level does
/// not cross its limit, or the side is empty. Every fill goes to `out`,
/// stamped `now`.
/// Fails with OutOfMemory when a fill or a queue cannot grow.
export Ladder.Match(mut self, a: &Alloc, book: mut Orders, taker: mem.Handle, now: i64, out: mut Trades): OutOfMemory!void {
    for {
        t := book.Live(taker) else {
            return
        }
        if t.left == 0 {
            return
        }
        limited := t.kind == Kind.Limit || t.kind == Kind.StopLimit
        limit := t.price
        var it := self.levels.Iter()
        first := it.Next() else {
            return
        }
        key := *first.key
        if limited && !self.Crosses(key * self.sign, limit) {
            return
        }
        try self.Sweep(a, key, book, taker, now, out)
    }
}

/// Fills `taker` against the level under `key` until one of them is done,
/// skipping handles of dead orders; removes the level when it empties.
Ladder.Sweep(mut self, a: &Alloc, key: i64, book: mut Orders, taker: mem.Handle, now: i64, out: mut Trades): OutOfMemory!void {
    price := key * self.sign
    lvl := self.levels.GetMut(&key) else {
        return
    }
    for lvl.qty > 0 {
        maker := lvl.orders.PopFront() else {
            break
        }
        if book.Live(maker).IsNone() {
            continue
        }
        pair := book.Pair(taker, maker) else {
            // The taker never rests on this side, so the two handles differ.
            continue
        }
        (a: t, b: m) := pair
        q := if t.left < m.left { t.left } else { m.left }
        t.left -= q
        m.left -= q
        lvl.qty -= q
        fill := Fill(taker: t.id, maker: m.id, taker_seq: t.seq, maker_seq: m.seq, side: t.side, price: price, qty: q, ts: now)
        maker_left := m.left
        taker_left := t.left
        try out.Record(a, fill)
        if maker_left > 0 {
            // A partly filled maker keeps its place at the front.
            try lvl.orders.PushFront(a, maker)
        } else {
            lvl.live -= 1
            try book.Retire(maker)
        }
        if taker_left == 0 {
            break
        }
    }
    if lvl.qty == 0 {
        self.Drop(a, key)
    }
}

/// The best `n` levels, best first, in `a`. O(n log levels).
export Ladder.Top(&self, a: &Arena, n: int): OutOfMemory!List[Depth] from a {
    var out := List[Depth].New(a)
    for (key, value) in self.levels.Iter() {
        if out.Len() == n {
            break
        }
        try out.Push(Depth(price: *key * self.sign, qty: value.qty, orders: value.live))
    }
    return out
}

/// The levels priced from `lo` to `hi`, both included, best first, in `a`.
export Ladder.Band(&self, a: &Arena, lo: i64, hi: i64): OutOfMemory!List[Depth] from a {
    // Keys run from the best price outwards, so the band's two ends swap
    // places on the bid side; Range excludes its upper key, hence the + 1.
    klo := if self.sign > 0 { lo } else { -hi }
    khi := if self.sign > 0 { hi + 1 } else { -lo + 1 }
    var out := List[Depth].New(a)
    for (key, value) in self.levels.Range(&klo, &khi) {
        try out.Push(Depth(price: *key * self.sign, qty: value.qty, orders: value.live))
    }
    return out
}

/// The total quantity resting on this side.
export Ladder.Quantity(&self): i64 {
    var sum: i64 = 0
    for (key, value) in self.levels.Iter() {
        _ = key
        sum += value.qty
    }
    return sum
}
