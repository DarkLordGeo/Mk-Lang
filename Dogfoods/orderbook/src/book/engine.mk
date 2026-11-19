// Why: the matching engine is the heart of the program: every feed command
// passes through it, and it decides every fill.
// What: Engine (price-time priority over two ladders), StopEntry, Snapshot
// and Closed.
// How: stop orders wait in priority queues keyed by trigger price; a work
// queue runs the stops a trade triggers before the next feed command, and
// the top of the book is kept as a boxed snapshot.

import std.coll
import std.mem

/// A stop order waiting for its trigger. `key` orders the queue: the stop
/// price for buy stops, which trigger as the price rises to it, and minus
/// the stop price for sell stops, which trigger as it falls to it.
struct StopEntry {
    key: i64
    seq: int
    handle: mem.Handle
}

impl Ord for StopEntry {
    Less(&self, other: &StopEntry): bool {
        if self.key != other.key {
            return self.key < other.key
        }
        return self.seq < other.seq
    }
}

/// The top of the book at one moment of the replay.
export struct Snapshot {
    export event: int
    export best_bid: ?i64
    export best_ask: ?i64
    export last: ?i64
    export live: int
    export fills: int
}

/// How often, in feed commands, dead orders are swept from the pool.
const SweepEvery: int = 4096

/// The engine. Its ladders, trade log and snapshot are on the allocator it
/// is given; the order store, the stop queues and the work queue live in
/// the replay's arena.
export view linear struct Engine {
    bids: Ladder
    asks: Ladder
    orders: Orders
    log: Trades
    buy_stops: coll.PriorityQueue[StopEntry]
    sell_stops: coll.PriorityQueue[StopEntry]
    work: coll.Deque[Command]
    /// The stops one trade fired, kept between commands so its storage is
    /// reused; empty except inside Triggers.
    fired: HeapList[mem.Handle]
    halted: Set[u32]
    rejects: [7]int
    snapshot: Box[Snapshot]
    every: int
    now: i64
    events: int
    cancels: int
    swept: int
}

/// An empty engine for at most `capacity` orders that takes a snapshot every
/// `every` feed commands (never when 0). Fails with OutOfMemory when `a` or
/// `arena` cannot hold its first allocations.
export Engine.New(a: &Alloc, arena: &Arena, capacity: int, every: int): OutOfMemory!Engine from a, arena {
    orders := try Orders.New(arena, capacity)
    first := Snapshot(event: 0, best_bid: none, best_ask: none, last: none, live: 0, fills: 0)
    snapshot := try Box[Snapshot].New(a, first)
    return Engine(
        bids: Ladder.New(a, Side.Buy),
        asks: Ladder.New(a, Side.Sell),
        orders: orders,
        log: Trades.New(a, arena),
        buy_stops: coll.PriorityQueue[StopEntry].New(arena),
        sell_stops: coll.PriorityQueue[StopEntry].New(arena),
        work: coll.Deque[Command].New(arena),
        fired: HeapList[mem.Handle].New(a),
        halted: Set[u32].NewKeyed(arena, HashKey.Of(0, 0)),
        rejects: [0; 7],
        snapshot: snapshot,
        every: every,
        now: 0,
        events: 0,
        cancels: 0,
        swept: 0,
    )
}

impl Free for Engine {
    Free(self, a: &Alloc) {
        Engine(bids, asks, orders, log, buy_stops, sell_stops, work, fired, halted, snapshot, ..) := self
        // The arena owns the store and the queues; they need no release.
        _ = (orders, buy_stops, sell_stops, work, halted)
        fired.FreeEmpty(a)
        bids.Free(a)
        asks.Free(a)
        log.Free(a)
        snapshot.Free(a)
    }
}

/// What is left when a session closes.
export struct Closed {
    export snapshot: Snapshot
    export bids: Expired
    export asks: Expired
}

/// Closes the session: every resting order expires, the engine is
/// released, and its last snapshot comes back with what expired.
export Engine.Finish(self, a: &Alloc): Closed {
    Engine(bids, asks, orders, log, buy_stops, sell_stops, work, fired, halted, snapshot, ..) := self
    // The arena owns the store and the queues; they need no release.
    _ = (buy_stops, sell_stops, work, halted)
    fired.FreeEmpty(a)
    b := bids.Close(a, &orders)
    s := asks.Close(a, &orders)
    log.Free(a)
    return Closed(snapshot: snapshot.Take(a), bids: b, asks: s)
}

/// Runs one feed command, then every stop it triggers, in trigger order.
/// Fails with OutOfMemory when the allocator or the arena is exhausted; the
/// engine is then still whole and may be released, but not used further.
export Engine.Submit(mut self, a: &Alloc into self, cmd: Command): OutOfMemory!void {
    try self.work.Push(cmd)
    for c := self.work.PopFront() {
        try self.Apply(a, c)
    }
    self.events += 1
    if self.events % SweepEvery == 0 {
        self.swept += self.orders.Sweep()
    }
    if self.every > 0 && self.events % self.every == 0 {
        self.Snap()
    }
}

/// Overwrites the boxed snapshot with the book as it is now.
Engine.Snap(mut self) {
    s := self.snapshot.Mut()
    s.event = self.events
    s.best_bid = self.bids.Best()
    s.best_ask = self.asks.Best()
    s.last = self.log.Last()
    s.live = self.orders.LiveCount()
    s.fills = self.log.Count()
}

/// The last snapshot taken.
export Engine.LastSnapshot(&self): &Snapshot {
    return self.snapshot.Ref()
}

Engine.Apply(mut self, a: &Alloc into self, cmd: Command): OutOfMemory!void {
    match cmd {
        .Add(ts, id, side, kind, qty, price, stop) {
            self.now = ts
            o := Order(id: id, seq: 0, side: side, kind: kind, price: price, stop: stop, qty: qty, left: qty, ts: ts)
            try self.Add(a, o)
        }
        .Cancel(ts, id) {
            self.now = ts
            try self.Cancel(a, id)
        }
        .Amend(ts, id, qty, price) {
            self.now = ts
            try self.Amend(a, ts, id, qty, price)
        }
        .Halt(ts, account) {
            self.now = ts
            _ = try self.halted.Add(account)
        }
        .Resume(ts, account) {
            self.now = ts
            _ = self.halted.Remove(&account)
        }
        .Bust(ts) {
            self.now = ts
            if !self.log.Bust() {
                self.Refuse(Reject.NothingToBust)
            }
        }
        .Trigger(handle) {
            try self.Fire(a, handle)
        }
    }
}

/// Counts a refusal and folds it into the checksum.
Engine.Refuse(mut self, r: Reject) {
    code := r.Code()
    self.rejects[code] += 1
    self.log.Note(u64(0x7e00) + Cast[u64](code))
}

Engine.Add(mut self, a: &Alloc into self, o: Order): OutOfMemory!void {
    if o.qty <= 0 || (o.kind != Kind.Market && o.kind != Kind.Stop && o.price <= 0) {
        self.Refuse(Reject.BadQuantity)
        return
    }
    if self.halted.Has(&o.id.account) {
        self.Refuse(Reject.Halted)
        return
    }
    if self.orders.Has(&o.id) {
        self.Refuse(Reject.Duplicate)
        return
    }
    h := try self.orders.Add(o)
    if o.kind == Kind.Stop || o.kind == Kind.StopLimit {
        try self.Arm(a, h)
        return
    }
    try self.Execute(a, h)
}

/// Queues the stop order `h` for its trigger, or fires it at once when the
/// last trade has already reached its stop price.
Engine.Arm(mut self, a: &Alloc into self, h: mem.Handle): OutOfMemory!void {
    o := *(self.orders.Live(h) else {
        return
    })
    if last := self.log.Last() {
        reached := if o.side == Side.Buy { last >= o.stop } else { last <= o.stop }
        if reached {
            try self.Fire(a, h)
            return
        }
    }
    if o.side == Side.Buy {
        try self.buy_stops.Push(StopEntry(key: o.stop, seq: o.seq, handle: h))
    } else {
        try self.sell_stops.Push(StopEntry(key: -o.stop, seq: o.seq, handle: h))
    }
}

/// Turns the triggered stop `h` into a market or limit order and runs it.
Engine.Fire(mut self, a: &Alloc into self, h: mem.Handle): OutOfMemory!void {
    if self.orders.Live(h).IsNone() {
        return
    }
    o := self.orders.Mut(h) else {
        return
    }
    o.kind = if o.kind == Kind.StopLimit { Kind.Limit } else { Kind.Market }
    try self.Execute(a, h)
}

/// Matches the live order `h` against the other side, then rests what is
/// left of a limit order and retires the rest; then fires the stops the
/// trades reached.
Engine.Execute(mut self, a: &Alloc into self, h: mem.Handle): OutOfMemory!void {
    before := self.log.Count()
    side := (self.orders.Live(h) else {
        return
    }).side
    if side == Side.Buy {
        try self.asks.Match(a, mut self.orders, h, self.now, mut self.log)
    } else {
        try self.bids.Match(a, mut self.orders, h, self.now, mut self.log)
    }
    o := *(self.orders.Live(h) else {
        return
    })
    if o.left == 0 {
        try self.orders.Retire(h)
    } else if o.kind == Kind.Limit {
        if side == Side.Buy {
            try self.bids.Rest(a, o.price, o.left, h)
        } else {
            try self.asks.Rest(a, o.price, o.left, h)
        }
    } else {
        // The rest of a market order is cancelled, as on most venues.
        try self.orders.Retire(h)
        if self.log.Count() == before {
            self.orders.Discard(h)
            self.Refuse(Reject.NoLiquidity)
        }
    }
    if self.log.Count() > before {
        try self.Triggers(a)
    }
}

/// Moves every stop the last trade reached from its queue to the front of
/// the work queue, the earliest triggered first.
Engine.Triggers(mut self, a: &Alloc): OutOfMemory!void {
    last := self.log.Last() else {
        return
    }
    // A failure leaves no handle behind, so the list is empty at release.
    onerr self.fired.Clear()
    for {
        top := self.buy_stops.Peek() else {
            break
        }
        if top.key > last {
            break
        }
        e := self.buy_stops.Pop().Must() // Peek just saw it
        if self.orders.Live(e.handle).IsSome() {
            try self.fired.Push(a, e.handle)
        }
    }
    for {
        top := self.sell_stops.Peek() else {
            break
        }
        if top.key > -last {
            break
        }
        e := self.sell_stops.Pop().Must() // Peek just saw it
        if self.orders.Live(e.handle).IsSome() {
            try self.fired.Push(a, e.handle)
        }
    }
    // Pushing to the front from the back keeps the work queue in trigger
    // order ahead of anything queued before.
    n := self.fired.Len()
    for i in 0..n {
        try self.work.PushFront(Command.Trigger(handle: self.fired[n - 1 - i]))
    }
    self.fired.Clear()
}

Engine.Cancel(mut self, a: &Alloc into self, id: ClientId): OutOfMemory!void {
    h := self.orders.Find(&id) else {
        self.Refuse(if self.orders.WasDone(&id) { Reject.AlreadyDone } else { Reject.Unknown })
        return
    }
    try self.Withdraw(a, h)
    self.cancels += 1
}

/// Takes the live order `h` out of the book: out of its ladder when it
/// rests, and out of the store. A waiting stop leaves its queue lazily.
Engine.Withdraw(mut self, a: &Alloc into self, h: mem.Handle): OutOfMemory!void {
    o := *(self.orders.Live(h) else {
        return
    })
    try self.orders.Retire(h)
    if o.kind == Kind.Limit {
        if o.side == Side.Buy {
            self.bids.Withdraw(a, o.price, o.left, h)
            try self.bids.Compact(a, o.price, &self.orders)
        } else {
            self.asks.Withdraw(a, o.price, o.left, h)
            try self.asks.Compact(a, o.price, &self.orders)
        }
    }
}

Engine.Amend(mut self, a: &Alloc into self, ts: i64, id: ClientId, qty: i64, price: i64): OutOfMemory!void {
    h := self.orders.Find(&id) else {
        self.Refuse(if self.orders.WasDone(&id) { Reject.AlreadyDone } else { Reject.Unknown })
        return
    }
    if qty <= 0 || price <= 0 {
        self.Refuse(Reject.BadQuantity)
        return
    }
    old := *(self.orders.Live(h) else {
        return
    })
    if old.kind == Kind.Limit {
        if old.side == Side.Buy {
            self.bids.Withdraw(a, old.price, old.left, h)
        } else {
            self.asks.Withdraw(a, old.price, old.left, h)
        }
    }
    // An amended order loses its time priority: it is a new arrival.
    // The new price is a stop order's trigger and any other order's limit.
    var o := old
    o.qty = qty
    o.left = qty
    if o.kind == Kind.Stop {
        o.stop = price
    } else {
        o.price = price
    }
    o.ts = ts
    h2 := try self.orders.Reissue(o)
    if o.kind == Kind.Stop || o.kind == Kind.StopLimit {
        try self.Arm(a, h2)
        return
    }
    try self.Execute(a, h2)
}

/// The best bid, if any.
export Engine.BestBid(&self): ?i64 {
    return self.bids.Best()
}

/// The best ask, if any.
export Engine.BestAsk(&self): ?i64 {
    return self.asks.Best()
}

/// The best `n` bid levels, best first.
export Engine.BidDepth(&self, a: &Arena, n: int): OutOfMemory!List[Depth] from a {
    return try self.bids.Top(a, n)
}

/// The best `n` ask levels, best first.
export Engine.AskDepth(&self, a: &Arena, n: int): OutOfMemory!List[Depth] from a {
    return try self.asks.Top(a, n)
}

/// Bid and ask levels priced from `lo` to `hi`.
export Engine.Band(&self, a: &Arena, lo: i64, hi: i64): OutOfMemory!(bids: List[Depth], asks: List[Depth]) from a {
    bids := try self.bids.Band(a, lo, hi)
    asks := try self.asks.Band(a, lo, hi)
    return (bids, asks)
}

/// The trades so far.
export Engine.Trades(&self): &Trades {
    return &self.log
}

/// The live orders, in arrival order.
export Engine.Resting(&self, a: &Arena): OutOfMemory!List[Order] from a {
    return try self.orders.Resting(a)
}

/// Counts of what the engine did, for the report.
export struct Totals {
    export events: int
    export orders: int
    export live: int
    export held: int
    export cancels: int
    export swept: int
    export bid_levels: int
    export ask_levels: int
    export bid_qty: i64
    export ask_qty: i64
    export stops: int
    export capacity: int
}

/// The engine's counts as they are now.
export Engine.Totals(&self): Totals {
    return Totals(
        events: self.events,
        orders: self.orders.seq,
        live: self.orders.LiveCount(),
        held: self.orders.Held(),
        cancels: self.cancels,
        swept: self.swept,
        bid_levels: self.bids.LevelCount(),
        ask_levels: self.asks.LevelCount(),
        bid_qty: self.bids.Quantity(),
        ask_qty: self.asks.Quantity(),
        stops: self.buy_stops.Len() + self.sell_stops.Len(),
        capacity: self.orders.Capacity(),
    )
}

/// How many commands were refused with `r`.
export Engine.Refused(&self, r: Reject): int {
    return self.rejects[r.Code()]
}

/// The checksum over every fill, bust and refusal, and the final book.
export Engine.Checksum(&self): u64 {
    var h := self.log.Sum()
    h = MixInt(h, self.bids.Best() else { 0 })
    h = MixInt(h, self.asks.Best() else { 0 })
    h = MixInt(h, self.bids.Quantity())
    h = MixInt(h, self.asks.Quantity())
    return Mix(h, Cast[u64](self.orders.LiveCount()))
}
