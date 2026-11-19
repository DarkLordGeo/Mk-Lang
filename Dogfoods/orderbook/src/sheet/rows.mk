// Why: the book file is how a desk keeps its book between sessions.
// What: OrderRow, the header check, RowOf, CommandOf and the BookError set.
// How: one resting order a CSV row, read and written through std.csv, in
// the format below.

// The book file: one resting order a CSV row under a header, as a desk
// keeps the book between sessions. Prices are decimals with two places, an
// empty price or stop being one the order kind does not have, and `tag` is
// the client's free text, quoted where RFC 4180 needs it.
//
//   id,side,kind,qty,price,stop,ts,tag
//   7/1,B,limit,40,101.25,,1003,"hedge, desk 4"
//   9/2,S,stop-limit,10,100.30,100.40,1006,
import std.csv
import src.book
import src.feed

/// One resting order as a row of the book file. `qty` is what is left of
/// it; `kind` is `limit`, `stop` or `stop-limit`.
export struct OrderRow {
    export id: str
    export side: str
    export kind: str
    export qty: i64
    export price: str
    export stop: str
    export ts: i64
    export tag: str
}

gen csv.Codec(OrderRow)

/// The number of columns of a book row.
export const Columns: int = 8

/// The header WriteCsvHeader writes, field by field.
const Header: [8]str = ["id", "side", "kind", "qty", "price", "stop", "ts", "tag"]

/// The name of column `n` of the book file, counted from 1.
export ColumnName(n: int): str {
    if n < 1 || n > Columns {
        return ""
    }
    return Header[n - 1]
}

/// The book's first record is not the header.
export error BadHeader(line: i64)
/// A row whose number of fields is not the header's.
export error RowLength(line: i64, got: int)
/// A row whose `qty` or `ts` is not an integer.
export error BadNumber(line: i64)
/// A row with a field that is not UTF-8.
export error BadText(line: i64)
/// A column, counted from 1, whose value no resting order has.
export error BadColumn(line: i64, column: int)

export type BookError = BadHeader | RowLength | BadNumber | BadText | BadColumn

/// The CSV dialect of the book file: commas and double quotes, a tag of at
/// most 4 KiB and no more than 16 fields to a record.
export BookOptions(): csv.Options {
    var o := csv.DefaultOptions
    o.field_bytes = 4096
    o.record_fields = 16
    return o
}

/// Fails with BadHeader unless `r` holds exactly the header's names.
/// O(header bytes); allocates nothing.
export CheckHeader(r: &csv.Record): BadHeader!void {
    if r.Len() != Columns {
        fail BadHeader(line: r.Line())
    }
    for i, name in Header {
        field := r.Field(i) else {
            fail BadHeader(line: r.Line())
        }
        if field.Trim() != name {
            fail BadHeader(line: r.Line())
        }
    }
}

/// The book row of the live order `o`, with its tag, its texts in `a`.
/// O(1); allocates the id and the prices.
export RowOf(a: &Arena, o: &book.Order, tag: str): OutOfMemory!OrderRow from a, tag {
    kind := match o.kind {
        .Limit { "limit" }
        .Market { "market" }
        .Stop { "stop" }
        .StopLimit { "stop-limit" }
    }
    priced := o.kind == book.Kind.Limit || o.kind == book.Kind.StopLimit
    stopped := o.kind == book.Kind.Stop || o.kind == book.Kind.StopLimit
    return OrderRow(
        id: try a.Fmt("{id}", (id: o.id)),
        side: SideLetter(o.side),
        kind: kind,
        qty: o.left,
        price: if priced { try book.PriceText(a, o.price) } else { "" },
        stop: if stopped { try book.PriceText(a, o.stop) } else { "" },
        ts: o.ts,
        tag: tag,
    )
}

/// `B` for a buy, `S` for a sell, as the feed writes sides.
export SideLetter(s: book.Side): str {
    return if s == book.Side.Buy { "B" } else { "S" }
}

/// The client id in `row`, read from line `line`. O(id bytes); allocates
/// nothing.
export IdOf(row: &OrderRow, line: i64): BadColumn!book.ClientId {
    return feed.Id(row.id.Trim(), 0, 0) else {
        fail BadColumn(line: line, column: 1)
    }
}

/// The command that puts the order `id` of `row`, read from line `line`,
/// back on the book. Fails with the first column after the id, left to
/// right, that holds a value no resting order has. O(row bytes); allocates
/// nothing.
export CommandOf(row: &OrderRow, id: book.ClientId, line: i64): BadColumn!book.Command {
    side := match row.side.Trim() {
        "B" { book.Side.Buy }
        "S" { book.Side.Sell }
        _ { fail BadColumn(line: line, column: 2) }
    }
    kind := match row.kind.Trim() {
        "limit" { book.Kind.Limit }
        "stop" { book.Kind.Stop }
        "stop-limit" { book.Kind.StopLimit }
        _ { fail BadColumn(line: line, column: 3) }
    }
    if row.qty < 1 {
        fail BadColumn(line: line, column: 4)
    }
    price := try Ticks(row.price, kind != book.Kind.Stop, line, 5)
    stop := try Ticks(row.stop, kind != book.Kind.Limit, line, 6)
    return book.Command.Add(ts: row.ts, id: id, side: side, kind: kind, qty: row.qty, price: price, stop: stop)
}

/// The price in `text` as ticks when the order kind has one (`wanted`),
/// else 0, the text then being empty.
Ticks(text: str, wanted: bool, line: i64, column: int): BadColumn!i64 {
    t := text.Trim()
    if !wanted {
        if t.Len() > 0 {
            fail BadColumn(line: line, column: column)
        }
        return 0
    }
    return feed.Price(t, 0, 0) else {
        fail BadColumn(line: line, column: column)
    }
}

test "a row names its first bad column" {
    row := OrderRow(id: "7/1", side: "B", kind: "stop", qty: 10, price: "101.00", stop: "100.00", ts: 1, tag: "")
    match CommandOf(&row, book.ClientId(account: 7, serial: 1), 3) {
        err(e) { Assert(e.line == 3 && e.column == 5) }
        ok(c) {
            _ = c
            Assert(false)
        }
    }
}

test "a stop-limit row becomes its order" {
    row := OrderRow(id: " 9/2 ", side: "S", kind: "stop-limit", qty: 10, price: "100.30", stop: "100.40", ts: 6, tag: "x")
    want := book.Command.Add(ts: 6, id: book.ClientId(account: 9, serial: 2), side: book.Side.Sell, kind: book.Kind.StopLimit, qty: 10, price: 10030, stop: 10040)
    Assert(CommandOf(&row, IdOf(&row, 1).Must(), 1).Must().Eq(&want))
}
