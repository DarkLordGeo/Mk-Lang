// Why: replays read market data from files a person can write and read.
// What: ParseRecord and ParseFields, the field parsers (Id, Price) and the
// errors BadRecord, BadField and FieldCount.
// How: records come from std.csv; each is checked field by field and turned
// into a book.Command, as the format below lays out.

// The market-data file format: RFC 4180 CSV, one command a record, a record
// whose first field starts with `#` being a comment. Fields are trimmed of
// spaces, and prices have at most two decimals.
//
//   N,ts,acct/serial,B|S,qty,price            limit order
//   M,ts,acct/serial,B|S,qty                  market order
//   S,ts,acct/serial,B|S,qty,stop             stop order
//   T,ts,acct/serial,B|S,qty,stop,limit       stop-limit order
//   C,ts,acct/serial                          cancel
//   A,ts,acct/serial,qty,price                amend: new size and price
//   H,ts,acct                                 halt an account
//   R,ts,acct                                 resume an account
//   X,ts                                      bust the last trade
import std.csv
import src.book

/// A record type letter the format does not have.
export error BadRecord(line: int)
/// A field that does not parse as its column requires; fields count from 1.
export error BadField(line: int, field: int)
/// A record with the wrong number of fields.
export error FieldCount(line: int, want: int, got: int)

export type Error = BadRecord | BadField | FieldCount

/// The most fields any record has.
const MaxFields: int = 7

/// The CSV dialect of a feed: commas and double quotes, with limits no
/// honest feed reaches, so a corrupt file fails at its first bad record
/// instead of filling memory.
export FeedOptions(): csv.Options {
    var o := csv.DefaultOptions
    o.field_bytes = 256
    o.record_fields = 64
    return o
}

/// The command in the CSV record `r`: none for a blank or comment record.
/// Fails with the record's first line and the field at fault, a field that
/// is not UTF-8 included. O(record bytes); allocates nothing.
export ParseRecord(r: &csv.Record): Error!?book.Command {
    n := Cast[int](r.Line())
    count := r.Len()
    var cols: [7]str = [""; 7]
    for i in 0..count {
        f := r.Field(i) else {
            fail BadField(line: n, field: i + 1)
        }
        if i == 0 && f.Trim().StartsWith("#") {
            return none
        }
        if i < MaxFields {
            cols[i] = f.Trim()
        }
    }
    held := if count < MaxFields { count } else { MaxFields }
    return try ParseFields(cols[..held], count, n)
}

/// The command in the trimmed fields `cols` of line `n`, which had `count`
/// fields in all (more than `cols` holds when the record is too long): none
/// for a blank or comment line. O(fields); allocates nothing.
export ParseFields(cols: []str, count: int, n: int): Error!?book.Command {
    if cols.Len() == 0 || count == 1 && cols[0].Len() == 0 || cols[0].StartsWith("#") {
        return none
    }
    kind := cols[0]
    want := match kind {
        "N" { 6 }
        "M" { 5 }
        "S" { 6 }
        "T" { 7 }
        "C" { 3 }
        "A" { 5 }
        "H" { 3 }
        "R" { 3 }
        "X" { 2 }
        _ { fail BadRecord(line: n) }
    }
    if count != want {
        fail FieldCount(line: n, want: want, got: count)
    }
    ts := try Int(cols[1], n, 2)
    if kind == "X" {
        return some(book.Command.Bust(ts: ts))
    }
    if kind == "H" || kind == "R" {
        account := try Account(cols[2], n, 3)
        return some(if kind == "H" { book.Command.Halt(ts: ts, account: account) } else { book.Command.Resume(ts: ts, account: account) })
    }
    id := try Id(cols[2], n, 3)
    if kind == "C" {
        return some(book.Command.Cancel(ts: ts, id: id))
    }
    if kind == "A" {
        qty := try Int(cols[3], n, 4)
        price := try Price(cols[4], n, 5)
        return some(book.Command.Amend(ts: ts, id: id, qty: qty, price: price))
    }
    side := match cols[3] {
        "B" { book.Side.Buy }
        "S" { book.Side.Sell }
        _ { fail BadField(line: n, field: 4) }
    }
    qty := try Int(cols[4], n, 5)
    cmd := match kind {
        "N" {
            book.Command.Add(ts: ts, id: id, side: side, kind: book.Kind.Limit, qty: qty, price: try Price(cols[5], n, 6), stop: 0)
        }
        "M" {
            book.Command.Add(ts: ts, id: id, side: side, kind: book.Kind.Market, qty: qty, price: 0, stop: 0)
        }
        "S" {
            book.Command.Add(ts: ts, id: id, side: side, kind: book.Kind.Stop, qty: qty, price: 0, stop: try Price(cols[5], n, 6))
        }
        _ {
            stop := try Price(cols[5], n, 6)
            book.Command.Add(ts: ts, id: id, side: side, kind: book.Kind.StopLimit, qty: qty, price: try Price(cols[6], n, 7), stop: stop)
        }
    }
    return some(cmd)
}

/// A decimal integer field.
Int(s: str, line: int, field: int): BadField!i64 {
    return s.ParseInt() else {
        fail BadField(line: line, field: field)
    }
}

/// An account number: a decimal from 0 to 2^32 - 1.
Account(s: str, line: int, field: int): BadField!u32 {
    n := try Int(s, line, field)
    if n < 0 || n > 0xffffffff {
        fail BadField(line: line, field: field)
    }
    return Cast[u32](n)
}

/// A client id, `account/serial`.
export Id(s: str, line: int, field: int): BadField!book.ClientId {
    slash := s.Find("/") else {
        fail BadField(line: line, field: field)
    }
    account := try Account(s[..slash], line, field)
    serial := try Account(s[slash + 1..], line, field)
    return book.ClientId(account: account, serial: serial)
}

/// A price with at most two decimals, as ticks: `101.5` is 10150. Prices are
/// positive and below 10^15 ticks.
export Price(s: str, line: int, field: int): BadField!i64 {
    dot := s.Find(".") else { s.Len() }
    whole := try Digits(s[..dot], line, field)
    var cents: i64 = 0
    if dot < s.Len() {
        frac := s[dot + 1..]
        if frac.Len() == 0 || frac.Len() > 2 {
            fail BadField(line: line, field: field)
        }
        cents = try Digits(frac, line, field)
        if frac.Len() == 1 {
            cents *= 10
        }
    }
    if whole > 9_999_999_999_999 {
        fail BadField(line: line, field: field)
    }
    return whole * 100 + cents
}

/// One or more ASCII digits, no sign, at most 15 of them.
Digits(s: str, line: int, field: int): BadField!i64 {
    if s.Len() == 0 || s.Len() > 15 {
        fail BadField(line: line, field: field)
    }
    var n: i64 = 0
    for b in s.AsBytes() {
        if b < b'0' || b > b'9' {
            fail BadField(line: line, field: field)
        }
        n = n * 10 + i64(b - b'0')
    }
    return n
}

/// What each field of each record type holds, for error messages.
export FieldName(record: str, field: int): str {
    if field == 1 {
        return "record type"
    }
    if field == 2 {
        return "timestamp"
    }
    if field == 3 {
        return if record == "H" || record == "R" { "account" } else { "client id" }
    }
    if record == "A" {
        return if field == 4 { "quantity" } else { "price" }
    }
    return match field {
        4 { "side" }
        5 { "quantity" }
        6 {
            if record == "N" { "price" } else { "stop price" }
        }
        _ { "limit price" }
    }
}

/// Writes a sentence for `e` about `path`, without a newline; `record` is
/// the record type field of the record at fault.
export Describe(w: mut Writer, path: str, e: Error, record: str): WriteError!void {
    match e {
        BadRecord(line) {
            try Print(w, "{path}:{line}: unknown record type \"{record}\"", (path, line, record))
        }
        BadField(line, field) {
            what := FieldName(record, field)
            try Print(w, "{path}:{line}: field {field} ({what}) is not valid", (path, line, field, what))
        }
        FieldCount(line, want, got) {
            try Print(w, "{path}:{line}: expected {want} fields, found {got}", (path, line, want, got))
        }
    }
}

test "prices take at most two decimals" {
    p := Price("3.141", 1, 1) else { -1 }
    Assert(p == -1)
}
