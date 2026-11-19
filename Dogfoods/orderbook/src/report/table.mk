// Why: a person reading a replay wants the book and the trades at a glance.
// What: Depth, Fills, Bars, Accounts, Sizes and PriceLine.
// How: each writes an aligned text table to any Writer.

import src.book
import src.stats

/// The depth table: bids on the left, asks on the right, best prices on
/// the first row.
export Depth(w: mut Writer, a: &Arena, bids: []book.Depth, asks: []book.Depth): OutOfMemory|WriteError!void {
    try Print(w, "{bq:>10} {bo:>6} {bp:>12} | {ap:<12} {aq:>10} {ao:>6}\n", (bq: "bid qty", bo: "orders", bp: "bid", ap: "ask", aq: "ask qty", ao: "orders"))
    rows := if bids.Len() > asks.Len() { bids.Len() } else { asks.Len() }
    if rows == 0 {
        try Print(w, "{e:^60}\n", (e: "(the book is empty)"))
    }
    for i in 0..rows {
        if b := bids.Get(i) {
            bp := try book.PriceText(a, b.price)
            try Print(w, "{q:>10} {o:>6} {bp:>12}", (q: b.qty, o: b.orders, bp))
        } else {
            try Print(w, "{blank:30}", (blank: ""))
        }
        try w.Str(" | ")
        if s := asks.Get(i) {
            ap := try book.PriceText(a, s.price)
            try Print(w, "{ap:<12} {q:>10} {o:>6}", (ap, q: s.qty, o: s.orders))
        }
        try w.Str("\n")
    }
}

/// A list of fills, one a line, with a title.
export Fills(w: mut Writer, a: &Arena, title: str, fills: []book.Fill): OutOfMemory|WriteError!void {
    try Print(w, "{title}\n", (title))
    if fills.Len() == 0 {
        try w.Str("  (none)\n")
        return
    }
    for f in fills {
        price := try book.PriceText(a, f.price)
        taker := try a.Fmt("{id}", (id: f.taker))
        maker := try a.Fmt("{id}", (id: f.maker))
        try Print(w, "  {ts:>10}  {side:<4} {qty:>7} @ {price:>10}  taker {taker:<9} maker {maker}\n", (ts: f.ts, side: f.side, qty: f.qty, price, taker, maker))
    }
}

/// The bars, newest first, at most `limit` of them.
export Bars(w: mut Writer, a: &Arena, bars: []stats.Bar, limit: int): OutOfMemory|WriteError!void {
    try Print(w, "bars, newest first\n{n:>5} {o:>10} {h:>10} {l:>10} {c:>10} {v:>10}\n", (n: "#", o: "open", h: "high", l: "low", c: "close", v: "volume"))
    newest := try stats.Newest(a, bars)
    for i, b in newest {
        if i == limit {
            break
        }
        o := try book.PriceText(a, b.open)
        h := try book.PriceText(a, b.high)
        l := try book.PriceText(a, b.low)
        c := try book.PriceText(a, b.close)
        try Print(w, "{n:>5} {o:>10} {h:>10} {l:>10} {c:>10} {v:>10}\n", (n: bars.Len() - i, o, h, l, c, v: b.volume))
    }
}

/// The accounts, largest volume first, at most `limit` of them.
export Accounts(w: mut Writer, rows: []stats.AccountRow, limit: int): WriteError!void {
    try Print(w, "accounts by volume\n{r:>4} {acct:>8} {b:>10} {s:>10} {f:>7}\n", (r: "rank", acct: "account", b: "bought", s: "sold", f: "fills"))
    for i, row in rows {
        if i == limit {
            break
        }
        try Print(w, "{r:>4} {row.account:>8} {row.bought:>10} {row.sold:>10} {row.fills:>7}\n", (r: i + 1, row))
    }
}

/// The fill-size histogram.
export Sizes(w: mut Writer, counts: []int): WriteError!void {
    labels: [7]str = ["<=10", "<=50", "<=100", "<=500", "<=1000", "<=5000", ">5000"]
    try w.Str("fill sizes\n")
    for i, c in counts {
        try Print(w, "  {l:<7}{c:>9}\n", (l: labels[i], c))
    }
}

/// One price line: a label, then the price or a dash.
export PriceLine(w: mut Writer, a: &Arena, label: str, p: ?i64): OutOfMemory|WriteError!void {
    text := if v := p { try book.PriceText(a, v) } else { "-" }
    try Print(w, "{label:<18}{text:>12}\n", (label, text))
}
