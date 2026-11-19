// Why: the closing book and the fills leave as CSV files, replaced whole.
// What: WriteBook, WriteFills, Save and the Describe* error texts.
// How: each row's texts go into a scratch arena freed before the next row;
// Save writes a temporary file and renames it over the old one.

import std.csv
import std.fs
import src.book

/// The fills file's columns. `side` is the taker's.
const FillHeader: [8]str = ["ts", "taker", "maker", "side", "price", "qty", "taker_tag", "maker_tag"]

/// Writes the book file of the live orders `orders`: the header, then one
/// row each, in the order given, with each order's tag from `opening`.
/// WriteError is w's. O(orders); allocates each row's texts in a scratch
/// arena of `a` that it frees before the next.
export WriteBook(w: mut Writer, a: &Arena, orders: []book.Order, opening: &Opening, o: &csv.Options): OutOfMemory|WriteError!void {
    try OrderRow.WriteCsvHeader(w, o)
    for order in orders {
        var scratch := try a.Sub()
        defer scratch.Free()
        row := try RowOf(&scratch, &order, opening.Tag(&order.id))
        try row.WriteCsv(w, o)
    }
}

/// Writes the fills file: the header, then one record a fill, oldest
/// first, each order's tag from `opening`. WriteError is w's. O(fills);
/// allocates each record's texts in a scratch arena of `a` that it frees
/// before the next.
export WriteFills(w: mut Writer, a: &Arena, fills: []book.Fill, opening: &Opening, o: &csv.Options): OutOfMemory|WriteError!void {
    try csv.WriteRecord(w, FillHeader[..], o)
    for f in fills {
        var scratch := try a.Sub()
        defer scratch.Free()
        var values: [8]str = [
            try scratch.Fmt("{ts}", (ts: f.ts)),
            try scratch.Fmt("{id}", (id: f.taker)),
            try scratch.Fmt("{id}", (id: f.maker)),
            SideLetter(f.side),
            try book.PriceText(&scratch, f.price),
            try scratch.Fmt("{qty}", (qty: f.qty)),
            opening.Tag(&f.taker),
            opening.Tag(&f.maker),
        ]
        try csv.WriteRecord(w, values[..], o)
    }
}

/// Writes the file `path` whole through `write`: the bytes go to
/// `path.tmp`, which is synced, closed and renamed over `path`, so a crash
/// leaves the old file or the new one. O(bytes written); allocates the
/// temporary name and a 64 KiB buffer in `a`.
export Save(io: Io, a: &Arena, path: str, const write: fn(mut Writer): OutOfMemory|WriteError!void): fs.Error|OutOfMemory|WriteError!void {
    tmp := try a.Fmt("{path}.tmp", (path))
    buf := try a.Slice[u8](64 * 1024)
    var f := try fs.Create(io, tmp)
    onerr f.Abandon()
    var w := f.Writer(io, buf)
    try write(mut w)
    try w.Flush()
    try f.Sync(io)
    try f.Close(io)
    try fs.Rename(io, tmp, path)
}

/// Writes a sentence for the CSV error `e`, met reading `path` under the
/// options `o`, without a newline.
export DescribeCsv(w: mut Writer, path: str, e: csv.Error, o: &csv.Options): WriteError!void {
    match e {
        csv.Quote(line, column) {
            try Print(w, "{path}:{line}:{column}: malformed quoted field", (path, line, column))
        }
        csv.TooLarge(line) {
            try Print(w, "{path}:{line}: record too large: over {n} fields, or a field over {b} bytes", (path, line, n: o.record_fields, b: o.field_bytes))
        }
        ReadError {
            try Print(w, "cannot read {path}: {e}", (path, e))
        }
        _ {
            Panic("csv.Error gained a member this match does not handle")
        }
    }
}

/// Writes a sentence for the book error `e`, met reading `path`, without
/// a newline.
export DescribeBook(w: mut Writer, path: str, e: BookError): WriteError!void {
    match e {
        BadHeader(line) {
            try Print(w, "{path}:{line}: expected the header id,side,kind,qty,price,stop,ts,tag", (path, line))
        }
        RowLength(line, got) {
            try Print(w, "{path}:{line}: expected {want} fields, found {got}", (path, line, want: Columns, got))
        }
        BadNumber(line) {
            try Print(w, "{path}:{line}: qty and ts must be integers", (path, line))
        }
        BadText(line) {
            try Print(w, "{path}:{line}: a field is not valid UTF-8", (path, line))
        }
        BadColumn(line, column) {
            try Print(w, "{path}:{line}: column {column} ({name}) is not valid", (path, line, column, name: ColumnName(column)))
        }
    }
}

/// Everything LoadBook can fail with.
export type LoadError = fs.Error | csv.Error | BookError | OutOfMemory

/// Writes a sentence for `e`, met loading the book `path`, without a
/// newline, and returns the exit status the command line gives it: 3 for a
/// file that is not a book file, 4 for one that cannot be read, 5 for out
/// of memory.
export DescribeLoad(w: mut Writer, path: str, e: LoadError): WriteError!int {
    match e {
        OutOfMemory {
            try w.Str("error: out of memory reading the book")
            return 5
        }
        ReadError {
            try Print(w, "error: cannot read {path}: {e}", (path, e))
            return 4
        }
        csv.Quote(line, column) {
            try DescribeCsv(w, path, csv.Quote(line: line, column: column), &BookOptions())
        }
        csv.TooLarge(line) {
            try DescribeCsv(w, path, csv.TooLarge(line: line), &BookOptions())
        }
        BadHeader(line) {
            try DescribeBook(w, path, BadHeader(line: line))
        }
        RowLength(line, got) {
            try DescribeBook(w, path, RowLength(line: line, got: got))
        }
        BadNumber(line) {
            try DescribeBook(w, path, BadNumber(line: line))
        }
        BadText(line) {
            try DescribeBook(w, path, BadText(line: line))
        }
        BadColumn(line, column) {
            try DescribeBook(w, path, BadColumn(line: line, column: column))
        }
        _ {
            try Print(w, "error: cannot read {path}: {e}", (path, e))
            return 4
        }
    }
    return 3
}
