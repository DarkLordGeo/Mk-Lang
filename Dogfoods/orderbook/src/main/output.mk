// Why: a replay's results go to a terminal, a JSON file and CSV sheets.
// What: Output, which writes the text report and builds the JSON one, and
// SaveJson and the sheet writers.
// How: the JSON report leaves out the snapshot and memory figures, which the
// engine's release fills in.

import std.csv
import std.fs
import std.mem
import src.book
import src.report
import src.sheet
import src.stats

/// Labels for the reject counts, in Reject.Code order.
const RejectNames: [7]str = ["duplicate id", "account halted", "unknown id", "already done", "no liquidity", "bad quantity", "nothing to bust"]

/// Writes the text report of `engine` unless the options ask for quiet,
/// and returns the JSON report without its snapshot and memory, which the
/// engine's release decides.
Output(sys: &System, o: &Options, arena: &Arena, engine: &book.Engine, counting: &mem.Counting, out: mut Writer): Exit!report.ReportJson from arena {
    return Write(sys, o, arena, engine, out) else e {
        match e {
            OutOfMemory {
                fail Fatal(sys, "out of memory writing the report", 5)
            }
            WriteError {
                fail Exit(code: 4)
            }
            NoTrades {
                fail Exit(code: 1)
            }
        }
    }
}

/// An `--account` that did not trade; reported, not a failure of the run.
error NoTrades

Write(sys: &System, o: &Options, arena: &Arena, engine: &book.Engine, out: mut Writer): OutOfMemory|WriteError|NoTrades!report.ReportJson from arena {
    t := engine.Totals()
    trades := engine.Trades()
    summary := try stats.Summarize(arena, sys.io, trades.All(), o.bars)
    bids := try engine.BidDepth(arena, o.depth)
    asks := try engine.AskDepth(arena, o.depth)
    var rejects := List[report.RejectJson].New(arena)
    for i in 0..book.RejectKinds {
        n := engine.Refused(book.RejectOf(i))
        if n > 0 {
            try rejects.Push(report.RejectJson(reason: RejectNames[i], count: n))
        }
    }
    sum := try arena.Fmt("{c:016x}", (c: engine.Checksum()))
    if o.quiet {
        try Print(out, "commands {n} fills {f} checksum {sum}\n", (n: t.events, f: trades.Count(), sum))
    } else {
        try Print(out, "{l:<18}{v:>12}\n", (l: "commands", v: t.events))
        try Print(out, "{l:<18}{v:>12}\n", (l: "orders", v: t.orders))
        try Print(out, "{l:<18}{v:>12}\n", (l: "fills", v: trades.Count()))
        try Print(out, "{l:<18}{v:>12}\n", (l: "cancels", v: t.cancels))
        try Print(out, "{l:<18}{v:>12}\n", (l: "busts", v: trades.Busts()))
        try Print(out, "{l:<18}{v:>12}\n", (l: "live orders", v: t.live))
        try Print(out, "{l:<18}{v:>12}\n", (l: "stop queue entries", v: t.stops))
        for r in rejects {
            try Print(out, "rejected: {r.reason:<14}{r.count:>12}\n", (r))
        }
        try Print(out, "{l:<18}{v:>12}\n", (l: "volume", v: trades.Quantity()))
        try report.PriceLine(out, arena, "vwap", summary.vwap)
        try report.PriceLine(out, arena, "vwap, buyers", summary.buy_vwap)
        try report.PriceLine(out, arena, "vwap, sellers", summary.sell_vwap)
        try report.PriceLine(out, arena, "median fill", summary.p50)
        try report.PriceLine(out, arena, "90th percentile", summary.p90)
        try report.PriceLine(out, arena, "99th percentile", summary.p99)
        try Print(out, "{l:<18}{v:>12}\n", (l: "fills <= vwap", v: summary.at_or_below_vwap))
        try report.PriceLine(out, arena, "last", trades.Last())
        try Print(out, "{l:<18}{v:>12}\n", (l: "traded prices", v: trades.Prices()))
        try out.Str("\n")
        try report.Depth(out, arena, bids[..], asks[..])
        try out.Str("\n")
        try report.Fills(out, arena, "largest fills", trades.Largest())
        recent := try trades.Recent(arena)
        try report.Fills(out, arena, "recent fills, newest first", recent[..])
        busiest := try trades.Busiest(arena, 3)
        try out.Str("most traded prices\n")
        for d in busiest {
            p := try book.PriceText(arena, d.price)
            try Print(out, "  {p:>12} {q:>10}\n", (p, q: d.qty))
        }
        resting := try engine.Resting(arena)
        try Print(out, "oldest resting orders ({n} live)\n", (n: resting.Len()))
        for i, r in resting {
            if i == 3 {
                break
            }
            p := try book.PriceText(arena, if r.kind == book.Kind.Stop { r.stop } else { r.price })
            try Print(out, "  {id:<9} {side:<4} {kind:<9} {left:>7} @ {p:>10}\n", (id: r.id, side: r.side, kind: r.kind, left: r.left, p))
        }
        if summary.bars.Len() > 0 {
            try report.Bars(out, arena, summary.bars[..], 10)
        }
        try report.Sizes(out, summary.sizes)
        try report.Accounts(out, summary.accounts[..], 10)
        if band := o.band {
            try Band(out, arena, engine, band.lo, band.hi)
        }
        try Print(out, "checksum {sum}\n", (sum))
    }
    if acct := o.account {
        rank := try stats.Rank(arena, summary.accounts[..], acct)
        if r := rank {
            try Print(out, "account {acct} ranks {r} of {n} by volume\n", (acct, r, n: summary.accounts.Len()))
        } else {
            try out.Flush()
            sys.PrintErr("account {acct} did not trade\n", (acct)) else {}
            fail NoTrades
        }
    }
    s := engine.LastSnapshot()
    return report.ReportJson(
        source: try SourceName(arena, o),
        events: t.events,
        orders: t.orders,
        fills: trades.Count(),
        cancels: t.cancels,
        busts: trades.Busts(),
        volume: trades.Quantity(),
        vwap: try report.Opt(arena, summary.vwap),
        buy_vwap: try report.Opt(arena, summary.buy_vwap),
        sell_vwap: try report.Opt(arena, summary.sell_vwap),
        p50: try report.Opt(arena, summary.p50),
        p99: try report.Opt(arena, summary.p99),
        last: try report.Opt(arena, trades.Last()),
        best_bid: try report.Opt(arena, engine.BestBid()),
        best_ask: try report.Opt(arena, engine.BestAsk()),
        checksum: sum,
        snapshot: report.SnapshotJson(event: s.event, best_bid: try report.Opt(arena, s.best_bid), best_ask: try report.Opt(arena, s.best_ask), live: s.live),
        memory: report.MemoryJson(peak_bytes: 0, live_bytes: 0, live_allocations: 0),
        rejects: rejects,
        bids: try report.LevelsJson(arena, bids[..]),
        asks: try report.LevelsJson(arena, asks[..]),
        largest: try report.FillsJson(arena, trades.Largest()),
        bars: try report.BarsJson(arena, summary.bars[..]),
        accounts: try report.AccountsJson(arena, summary.accounts[..], 10),
    )
}

/// The feed's file name without its directory, which differs from machine
/// to machine, or the synthetic feed's parameters.
SourceName(a: &Arena, o: &Options): OutOfMemory!str from a {
    if path := o.path {
        var start := 0
        for i in 0..path.Len() {
            b := path.Byte(i)
            if b == b'/' || b == b'\\' {
                start = i + 1
            }
        }
        return try a.Dup(path[start..])
    }
    n := o.synth else { 0 }
    return try a.Fmt("synth:{n}:{seed}", (n, seed: o.seed))
}

/// The book and the traded volume between two prices.
Band(out: mut Writer, arena: &Arena, engine: &book.Engine, lo: i64, hi: i64): OutOfMemory|WriteError!void {
    l := try book.PriceText(arena, lo)
    h := try book.PriceText(arena, hi)
    try Print(out, "\nband {l} to {h}\n", (l, h))
    (bids, asks) := try engine.Band(arena, lo, hi)
    try report.Depth(out, arena, bids[..], asks[..])
    traded := try engine.Trades().VolumeBand(arena, lo, hi)
    for d in traded {
        p := try book.PriceText(arena, d.price)
        try Print(out, "  traded {p:>12} {q:>10}\n", (p, q: d.qty))
    }
}

/// After the engine's release: the final snapshot, the memory it used, and
/// the JSON report when the options ask for one.
Closing(sys: &System, o: &Options, arena: &Arena into r, closed: &book.Closed, counting: &mem.Counting, out: mut Writer, r: mut report.ReportJson): Exit!void {
    snap := &closed.snapshot
    r.memory = report.MemoryJson(peak_bytes: counting.Peak(), live_bytes: counting.Bytes(), live_allocations: counting.Live())
    if !o.quiet {
        Print(out, "last snapshot at command {e}: {live} live orders, {f} fills\n", (e: snap.event, live: snap.live, f: snap.fills)) else {
            fail Exit(code: 4)
        }
        Print(out, "expired at close: {bo} bids ({bq}), {ao} asks ({aq})\n", (bo: closed.bids.orders, bq: closed.bids.qty, ao: closed.asks.orders, aq: closed.asks.qty)) else {
            fail Exit(code: 4)
        }
        Print(out, "engine heap: peak {p} bytes, {b} bytes in {n} allocations left after release\n", (p: counting.Peak(), b: counting.Bytes(), n: counting.Live())) else {
            fail Exit(code: 4)
        }
    }
    path := o.json else {
        out.Flush() else {
            fail Exit(code: 4)
        }
        return
    }
    if path == "-" {
        report.Emit(out, &*r) else {
            fail Exit(code: 4)
        }
        out.Flush() else {
            fail Exit(code: 4)
        }
        return
    }
    out.Flush() else {
        fail Exit(code: 4)
    }
    SaveJson(sys.io, arena, path, &*r) else e {
        sys.PrintErr("error: cannot write {path}: {e}\n", (path, e)) else {}
        fail Exit(code: 4)
    }
}

/// Writes `r` to the file `path`, replacing it whole: the report goes to
/// `path.tmp`, which is synced, closed and then renamed over `path`, so a
/// crash leaves either the old report or the new one.
SaveJson(io: Io, arena: &Arena, path: str, r: &report.ReportJson): fs.Error|OutOfMemory|WriteError!void {
    tmp := try arena.Fmt("{path}.tmp", (path))
    var buf := try arena.Slice[u8](64 * 1024)
    var f := try fs.Create(io, tmp)
    onerr f.Abandon()
    var w := f.Writer(io, buf)
    try report.Emit(mut w, r)
    try w.Flush()
    try f.Sync(io)
    try f.Close(io)
    try fs.Rename(io, tmp, path)
}

/// Writes the CSV files the options ask for, while the engine still holds
/// the book: the book before the close and every fill.
Sheets(sys: &System, o: &Options, arena: &Arena, engine: &book.Engine, opening: &sheet.Opening, out: mut Writer): Exit!void {
    co := csv.DefaultOptions
    if path := o.book_out {
        resting := engine.Resting(arena) else {
            fail Fatal(sys, "out of memory writing the book", 5)
        }
        try SaveSheet(sys, arena, path, out, fn(w: mut Writer): OutOfMemory|WriteError!void {
            try sheet.WriteBook(w, arena, resting[..], opening, &co)
        })
    }
    if path := o.fills {
        fills := engine.Trades().All()
        try SaveSheet(sys, arena, path, out, fn(w: mut Writer): OutOfMemory|WriteError!void {
            try sheet.WriteFills(w, arena, fills, opening, &co)
        })
    }
}

/// Writes a CSV file through `write` to `path`, or to `out` when `path` is
/// `-`. Fails, reported, when the file cannot be written.
SaveSheet(sys: &System, arena: &Arena, path: str, out: mut Writer, const write: fn(mut Writer): OutOfMemory|WriteError!void): Exit!void {
    if path == "-" {
        write(out) else e {
            match e {
                OutOfMemory { fail Fatal(sys, "out of memory writing CSV", 5) }
                WriteError { fail Exit(code: 4) }
            }
        }
        return
    }
    sheet.Save(sys.io, arena, path, write) else e {
        match e {
            OutOfMemory { fail Fatal(sys, "out of memory writing CSV", 5) }
            _ {
                sys.PrintErr("error: cannot write {path}: {e}\n", (path, e)) else {}
                fail Exit(code: 4)
            }
        }
    }
}
