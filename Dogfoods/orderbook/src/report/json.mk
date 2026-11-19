// Why: other programs read a replay's results, so every number must survive
// the trip unrounded.
// What: ReportJson and its parts (LevelJson, FillJson, BarJson, ...), and Emit.
// How: std.json's codec writes the structs; every price is a decimal string.

import std.json
import src.book
import src.stats

/// A price level: its price, resting quantity and live orders.
export struct LevelJson {
    export price: str
    export qty: i64
    export orders: int
}

/// A fill: when, who took and who made it, the taker's side, price and size.
export struct FillJson {
    export ts: i64
    export taker: str
    export maker: str
    export side: str
    export price: str
    export qty: i64
}

/// A bar of trades: open, high, low, close, volume and trade count.
export struct BarJson {
    export open: str
    export high: str
    export low: str
    export close: str
    export volume: i64
    export trades: int
}

/// An account's traded volume.
export struct AccountJson {
    export account: i64
    export bought: i64
    export sold: i64
    export fills: int
}

/// How many commands were refused for one reason.
export struct RejectJson {
    export reason: str
    export count: int
}

/// The engine's heap, as the counting allocator saw it after release.
export struct MemoryJson {
    export peak_bytes: int
    export live_bytes: int
    export live_allocations: int
}

/// The last snapshot of the top of the book.
export struct SnapshotJson {
    export event: int
    export best_bid: ?str
    export best_ask: ?str
    export live: int
}

/// The whole report: one JSON object holding the scalar members, then the
/// lists.
export struct ReportJson {
    export source: str
    export events: int
    export orders: int
    export fills: int
    export cancels: int
    export busts: int
    export volume: i64
    export vwap: ?str
    export buy_vwap: ?str
    export sell_vwap: ?str
    export p50: ?str
    export p99: ?str
    export last: ?str
    export best_bid: ?str
    export best_ask: ?str
    export checksum: str
    export snapshot: SnapshotJson
    export memory: MemoryJson
    export rejects: List[RejectJson]
    export bids: List[LevelJson]
    export asks: List[LevelJson]
    export largest: List[FillJson]
    export bars: List[BarJson]
    export accounts: List[AccountJson]
}

gen json.Codec(LevelJson)
gen json.Codec(FillJson)
gen json.Codec(BarJson)
gen json.Codec(AccountJson)
gen json.Codec(RejectJson)
gen json.Codec(MemoryJson)
gen json.Codec(SnapshotJson)
gen json.Codec(ReportJson)

/// Writes any codec value as one line of JSON.
export Emit[T: json.Json](w: mut Writer, x: &T): WriteError!void {
    try x.FormatJson(w)
    try w.Str("\n")
}

/// A price as text, or none.
export Opt(a: &Arena, p: ?i64): OutOfMemory!?str from a {
    v := p else {
        return none
    }
    return some(try book.PriceText(a, v))
}

/// Levels for the report.
export LevelsJson(a: &Arena, xs: []book.Depth): OutOfMemory!List[LevelJson] from a {
    var out := List[LevelJson].New(a)
    for x in xs {
        try out.Push(LevelJson(price: try book.PriceText(a, x.price), qty: x.qty, orders: x.orders))
    }
    return out
}

/// Fills for the report.
export FillsJson(a: &Arena, xs: []book.Fill): OutOfMemory!List[FillJson] from a {
    var out := List[FillJson].New(a)
    for f in xs {
        side := if f.side == book.Side.Buy { "buy" } else { "sell" }
        try out.Push(FillJson(
            ts: f.ts,
            taker: try a.Fmt("{id}", (id: f.taker)),
            maker: try a.Fmt("{id}", (id: f.maker)),
            side: side,
            price: try book.PriceText(a, f.price),
            qty: f.qty,
        ))
    }
    return out
}

/// Bars for the report, oldest first.
export BarsJson(a: &Arena, xs: []stats.Bar): OutOfMemory!List[BarJson] from a {
    var out := List[BarJson].New(a)
    for b in xs {
        try out.Push(BarJson(
            open: try book.PriceText(a, b.open),
            high: try book.PriceText(a, b.high),
            low: try book.PriceText(a, b.low),
            close: try book.PriceText(a, b.close),
            volume: b.volume,
            trades: b.trades,
        ))
    }
    return out
}

/// Accounts for the report, at most `limit`.
export AccountsJson(a: &Arena, xs: []stats.AccountRow, limit: int): OutOfMemory!List[AccountJson] from a {
    var out := List[AccountJson].New(a)
    for i, r in xs {
        if i == limit {
            break
        }
        try out.Push(AccountJson(account: i64(r.account), bought: r.bought, sold: r.sold, fills: r.fills))
    }
    return out
}
