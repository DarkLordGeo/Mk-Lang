// Why: the CSV reader and writer share one set of errors and one options
// struct, and options a reader cannot use must be caught before any input.
//
// What: the errors Quote and TooLarge, the open type Error (with
// ReadError), Options, DefaultOptions and CheckOptions.
//
// How: Options has an unexported field, so a program builds it from
// DefaultOptions and a field added later breaks no literal. No unsafe, so
// std.csv is not in std/pkg.mk's unsafe_modules.

/// A quote where RFC 4180 allows none: inside a field that did not start
/// with one, after a closing quote other than before a delimiter or the end
/// of the line, or a quoted field the input ends inside. line and column
/// are 1-based and count bytes; a CR LF counts as one line break.
export error Quote(line: i64, column: int)

/// A field longer than field_bytes, a record with more fields than
/// record_fields, or a record that does not fit the buffer; line is the
/// record's first line.
export error TooLarge(line: i64)

/// Every way Records.Next fails.
export open type Error = Quote | TooLarge | ReadError

/// How a reader splits and a writer joins fields. Start from
/// DefaultOptions and assign the fields to change. A literal of Options
/// does not compile outside std.csv, so a field added later breaks no
/// program.
export struct Options {
    /// The byte between fields; not CR, LF or quote_byte.
    export delimiter: u8
    /// The byte that opens and closes a quoted field; not CR or LF.
    export quote_byte: u8
    /// The most bytes one field may hold after unquoting; more is TooLarge.
    export field_bytes: int
    /// The most fields one record may hold; more is TooLarge. Records also
    /// caps it at buf.Len() / 32, the room its field index takes.
    export record_fields: int
    sealed: u8
}

/// The RFC 4180 defaults: comma, double quote, fields up to 1 MiB, records
/// up to 4,096 fields.
export const DefaultOptions: Options = Options(delimiter: 0x2C, quote_byte: 0x22, field_bytes: 1 << 20, record_fields: 4096, sealed: 0)

// CheckOptions panics on options no reader or writer can use: a delimiter
// or quote that is CR or LF, the two equal, or a limit below its least
// value. They come from the program, not its input.
CheckOptions(o: &Options) {
    if o.delimiter == 0x0A || o.delimiter == 0x0D || o.quote_byte == 0x0A || o.quote_byte == 0x0D {
        Panic("csv: the delimiter and the quote cannot be CR or LF")
    }
    if o.delimiter == o.quote_byte {
        Panic("csv: the delimiter and the quote must differ")
    }
    if o.field_bytes < 0 || o.record_fields < 1 {
        Panic("csv: field_bytes must be at least 0 and record_fields at least 1")
    }
}
