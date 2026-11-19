// Why: the engine, the feed, the reports and the sheets share one vocabulary.
// What: Side, Kind, ClientId, Order, Fill, Command and Reject, with
// RejectOf, WritePrice and PriceText.
// How: prices are integer ticks of a hundredth, so no fill rounds; derived
// equality and hashing come from std.derive.

import std.derive
import std.mem

/// Which side of the book an order rests on.
export enum Side {
    Buy
    Sell
}

gen derive.Eq(Side)
gen derive.Format(Side)

/// How an order executes. A stop rests outside the book until the last trade
/// reaches its trigger, then becomes a market order (Stop) or a limit order
/// (StopLimit).
export enum Kind {
    Limit
    Market
    Stop
    StopLimit
}

gen derive.Eq(Kind)
gen derive.Format(Kind)

/// A client order id: the account that sent the order and the account's own
/// serial number for it. Written `account/serial` in a feed.
export struct ClientId {
    export account: u32
    export serial: u32
}

gen derive.Eq(ClientId)
gen derive.Hash(ClientId)

impl Format for ClientId {
    Format(&self, w: mut Writer): WriteError!void {
        try Print(w, "{a}/{s}", (a: self.account, s: self.serial))
    }
}

/// An order as the engine keeps it. `seq` is its arrival number, unique per
/// replay, and indexes the live flags. Prices are integer ticks of 0.01.
export struct Order {
    export id: ClientId
    export seq: int
    export side: Side
    export kind: Kind
    export price: i64
    export stop: i64
    export qty: i64
    export left: i64
    export ts: i64
}

gen derive.Eq(Order)

/// One execution between an incoming order (the taker) and a resting one
/// (the maker), at the maker's price.
export struct Fill {
    export taker: ClientId
    export maker: ClientId
    export taker_seq: int
    export maker_seq: int
    export side: Side
    export price: i64
    export qty: i64
    export ts: i64
}

gen derive.Eq(Fill)
gen derive.Format(Fill)

/// A command for the engine: what one feed line says, or a stop that the
/// engine itself triggered.
export enum Command {
    Add(ts: i64, id: ClientId, side: Side, kind: Kind, qty: i64, price: i64, stop: i64)
    Cancel(ts: i64, id: ClientId)
    Amend(ts: i64, id: ClientId, qty: i64, price: i64)
    Halt(ts: i64, account: u32)
    Resume(ts: i64, account: u32)
    Bust(ts: i64)
    Trigger(handle: mem.Handle)
}

gen derive.Eq(Command)

/// Why the engine refused a command. A refusal is part of the replay's
/// result, not a failure of the replay.
export enum Reject {
    Duplicate
    Halted
    Unknown
    AlreadyDone
    NoLiquidity
    BadQuantity
    NothingToBust
}

gen derive.Format(Reject)

/// The number of Reject variants, for a table indexed by Reject.Code.
export const RejectKinds: int = 7

/// A dense index for a Reject, in declaration order.
export Reject.Code(self): int {
    return match self {
        .Duplicate { 0 }
        .Halted { 1 }
        .Unknown { 2 }
        .AlreadyDone { 3 }
        .NoLiquidity { 4 }
        .BadQuantity { 5 }
        .NothingToBust { 6 }
    }
}

/// The Reject with dense index `code`; code must be below RejectKinds.
export RejectOf(code: int): Reject {
    return match code {
        0 { Reject.Duplicate }
        1 { Reject.Halted }
        2 { Reject.Unknown }
        3 { Reject.AlreadyDone }
        4 { Reject.NoLiquidity }
        5 { Reject.BadQuantity }
        _ { Reject.NothingToBust }
    }
}

/// Writes ticks as a decimal price with two places: 10125 is 101.25.
export WritePrice(w: mut Writer, ticks: i64): WriteError!void {
    var t := ticks
    if t < 0 {
        try w.Str("-")
        t = -t
    }
    try Print(w, "{whole}.{cents:02}", (whole: t / 100, cents: t % 100))
}

/// A price as text in `a`, as WritePrice writes it. O(1); allocates the text.
export PriceText(a: &Arena, ticks: i64): OutOfMemory!str from a {
    sign := if ticks < 0 { "-" } else { "" }
    t := if ticks < 0 { -ticks } else { ticks }
    return try a.Fmt("{sign}{whole}.{cents:02}", (sign, whole: t / 100, cents: t % 100))
}

/// The side an order of `side` trades against.
export Side.Other(self): Side {
    return if self == Side.Buy { Side.Sell } else { Side.Buy }
}

test "a price prints with two places" (sys: &System) {
    Assert(try PriceText(sys.arena, 10125) == "101.25")
    Assert(try PriceText(sys.arena, 7) == "0.07")
    Assert(try PriceText(sys.arena, -150) == "-1.50")
}

test "every reject code maps back to its reject" {
    for i in 0..RejectKinds {
        Assert(RejectOf(i).Code() == i)
    }
}
