# std.csv

`std.csv` reads and writes CSV, RFC 4180's comma-separated records. `Records` reads records from any `Reader` into a buffer you give it, with no allocation per record. `WriteRecord` writes one record to any `Writer`. `gen csv.Codec(T)` reads and writes a struct as one record. Reach for it for tables exchanged with spreadsheets and databases; for nested data use [`std.json`](json.md).

Read the [library guide](README.md) first.

```mk
// verdict: parse
import std.csv
```

## Contents

- [The main pieces](#the-main-pieces)
- [Reading records](#reading-records)
- [The buffer](#the-buffer)
- [Writing records](#writing-records)
- [Options](#options)
- [Typed records with the codec](#typed-records-with-the-codec)
- [Errors](#errors)
- [Memory, cost and cancellation](#memory-cost-and-cancellation)
- [Examples](#examples): [read records](#read-records), [write records](#write-records), [read and write a struct](#read-and-write-a-struct)
- [API](#api)

## The main pieces

| Task | Use |
|---|---|
| Read records | `Records.New`, then `Records.Next` until `none` |
| Read a record's fields | `Record.Len`, `Record.Field`, `Record.Line` |
| Write a record | `WriteRecord` |
| Change the delimiter, the quote or the limits | `Options`, starting from `DefaultOptions` |
| Read and write your own struct | `gen csv.Codec(T)`, then `T.FromCsv`, `T.WriteCsv`, `T.WriteCsvHeader` |

## Reading records

`Records.New(r, buf, o)` makes a reader over the `Reader` `r`, keeping the bytes it reads in `buf`. Each `Next` returns the next `Record`, or `none` at the end of input. A record's fields are views of `buf`, valid until the next `Next`: copy a field you want to keep.

How the input splits into records:

- CR LF or a lone LF ends a record. The last record may lack its line break.
- A blank line, empty or a lone CR, is skipped.
- A CR right before the end of input is dropped. A CR anywhere else is data.
- Inside a quoted field, a line break is data, CR LF included, so what `WriteRecord` wrote reads back exactly.
- A doubled quote inside a quoted field is one quote.
- Three things are `Quote` errors. They are a quote inside an unquoted field, a closing quote followed by anything but the delimiter or a line break, and input that ends inside a quoted field.

`Next` checks no field count across records: each record has as many fields as its line holds. `Record.Field(i)` is `none` for an `i` past the last field, and for a field whose bytes are not UTF-8; tell the two apart with `Record.Len`. `Record.Line` is the line the record starts on, counting from 1.

## The buffer

`buf` holds both the bytes of the record being read and an index of its fields. The index takes the last part of `buf`: 8 bytes for each of `min(record_fields, buf.Len() / 32)` field slots, at most a quarter of `buf`. So:

- a record has at most `buf.Len() / 32` fields;
- a record and its line break must fit in the rest of `buf`;
- a `buf` of at least 32 × N bytes and 4 × B / 3 bytes holds every record of N fields and B bytes.

A record within the options that does not fit `buf` is `TooLarge`, as a record past the options is.

## Writing records

`WriteRecord(w, values, o)` writes the fields as one record and ends it with CR LF.

- A field is quoted only where RFC 4180 needs it: when it holds the delimiter, the quote, CR or LF. A quote inside is doubled.
- A record of one empty field is written as `""`, since an empty line would read back as no record.

## Options

`Options` says how a reader splits and a writer joins fields:

| Field | Meaning | `DefaultOptions` |
|---|---|---|
| `delimiter` | the byte between fields | `,` |
| `quote_byte` | the byte that opens and closes a quoted field | `"` |
| `field_bytes` | the most bytes one field may hold after unquoting | 1 MiB |
| `record_fields` | the most fields one record may hold | 4,096 |

`Options` is a plain record of settings. Start from `DefaultOptions` and assign the fields you change. `Options` has one unexported field, so a literal of it does not compile outside `std.csv`, and a field added later breaks no program.

`Records.New` and `WriteRecord` panic on options no reader can use: a delimiter or quote that is CR or LF, the two equal, a negative `field_bytes`, or `record_fields` below 1. Such options come from the program, not from its input.

## Typed records with the codec

`gen csv.Codec(T)`, for a struct `T`, writes three functions. Each exported field of `T` is one field of the record, in declaration order.

- `T.FromCsv(a: &Arena, r: &csv.Record): ParseError|Utf8Error|OutOfMemory!T from a` reads the record `r` into a `T`. It fails with `ParseError` when the record has another number of fields or a field does not read as its type, and with `Utf8Error` when a field is not UTF-8. It copies every string into `a`, so its result borrows `a` alone and the reader may move on.
- `T.WriteCsv(&self, w: mut Writer, o: &csv.Options): WriteError!void` writes a `T` as one record, quoting as `WriteRecord` does.
- `T.WriteCsvHeader(w: mut Writer, o: &csv.Options): WriteError!void` writes the field names as one record.

| Field type | Written as | Read |
|---|---|---|
| `str` | itself | as it is |
| `bool` | `true` or `false` | the same |
| the signed integers | decimal | decimal, with an optional sign |
| the unsigned integers | decimal | decimal digits with no sign |
| `f32`, `f64` | as `Format` writes them, `inf` and `nan` included | correctly rounded to the type |
| `?T` of one of these | `T`, or an empty field for `none` | an empty field is `none` |

An empty field reads as `none`, so `some("")` of a `?str` reads back as `none`. `WriteCsv` and `WriteCsvHeader` panic on the options `WriteRecord` panics on. When `T` has a number field, they also panic on a delimiter or quote that a number's text can hold (digits, `+ - . e E n a i f`). Any other field type, an unexported field, or a struct with no field is error E0903.

## Errors

`csv.Error` is every way `Records.Next` fails:

| Member | Meaning |
|---|---|
| `Quote(line, column)` | A quote where RFC 4180 allows none (see above). `line` and `column` count from 1, `column` in bytes; CR LF counts as one line break |
| `TooLarge(line)` | A field longer than `field_bytes`, a record with more fields than `record_fields`, or a record that does not fit `buf`. `line` is the record's first line |
| `ReadError` | The `Reader` failed |

After `Quote` or `TooLarge`, every later `Next` fails with the same error. A `ReadError` leaves the reader where it was, so calling `Next` again resumes. `WriteRecord` fails only with the `Writer`'s `WriteError`. `csv.Error` is open, so a `match` on it outside std keeps a `_` arm.

## Memory, cost and cancellation

`Records` allocates nothing: every bound comes from `buf` and the options. It never panics on its input, and its memory and stack are bounded, so it is safe on untrusted files. `Next` costs O(n) in the record's bytes, amortized over refills; `Record.Field` is O(1) when the record is UTF-8 as a whole, which `Next` checks once. `WriteRecord` and `WriteCsv` allocate nothing and cost O(n) in the bytes written. Only `FromCsv` allocates, for its strings.

A cancel reaches `std.csv` through its `Reader` or `Writer`. `Next` reports it as `ReadError`. The reader keeps a partly read record in its own state, so over a `Reader` that can be read again, `Next` can be called again. `WriteRecord` and the written `WriteCsv` report it, or any other write failure, as `WriteError`. The writer may then hold part of the record: close the stream rather than write on.

## Examples

Each program below runs as written with `mk run`. They read from `Reader.Of`, a `Reader` over bytes in memory; a file's `Reader` works the same way.

### Read records

```mk
// verdict: ok
import std.csv

export Main(sys: &System): csv.Error|OutOfMemory|WriteError!void {
    data := "name,city\r\nAda,London\n\n\"Hopper, Grace\",\"say \"\"hi\"\"\"\n"
    var r := Reader.Of(data.AsBytes()) // any Reader: a file, a socket, stdin
    buf := try sys.arena.Slice[u8](4096) // holds one record and its field index
    o := csv.DefaultOptions
    var rs := csv.Records.New(mut r, mut buf[..], &o)
    for rec := try rs.Next() { // none at the end of input
        try sys.Print("line {line}, {n} fields:", (line: rec.Line(), n: rec.Len()))
        for i in 0..rec.Len() {
            field := rec.Field(i) else { "?" } // a view of buf until the next Next
            try sys.Print(" [{field}]", (field))
        }
        try sys.Print("\n", ())
    }
}
```

```text
$ mk run read.mk
line 1, 2 fields: [name] [city]
line 2, 2 fields: [Ada] [London]
line 4, 2 fields: [Hopper, Grace] [say "hi"]
```

- The blank line 3 is skipped, and `Line` still counts it.
- Quoted fields come back unquoted, with `""` folded to `"`, in place in `buf`.

### Write records

```mk
// verdict: ok
import std.csv

export Main(sys: &System): WriteError!void {
    var out := sys.Stdout()
    o := csv.DefaultOptions
    try csv.WriteRecord(mut out, ["id", "comment"][..], &o)
    try csv.WriteRecord(mut out, ["1", "plain"][..], &o)
    try csv.WriteRecord(mut out, ["2", "has, a comma"][..], &o)
    try csv.WriteRecord(mut out, ["3", "say \"hi\""][..], &o)
    try csv.WriteRecord(mut out, [""][..], &o) // a lone empty field is quoted

    // Other options: start from DefaultOptions and change fields.
    var tsv := csv.DefaultOptions
    tsv.delimiter = b'\t'
    try csv.WriteRecord(mut out, ["a", "b,c"][..], &tsv)
}
```

```text
$ mk run write.mk
id,comment
1,plain
2,"has, a comma"
3,"say ""hi"""
""
a	b,c
```

- Every line ends in CR LF, which the listing above does not show.
- With a tab as the delimiter, `b,c` needs no quotes.

### Read and write a struct

```mk
// verdict: ok
import std.csv

struct Sale {
    export item: str
    export qty: u32
    export price: f64
    export note: ?str
}

gen csv.Codec(Sale)

export Main(sys: &System): csv.Error|ParseError|Utf8Error|OutOfMemory|WriteError!void {
    o := csv.DefaultOptions
    var out := sys.Stdout()
    try Sale.WriteCsvHeader(mut out, &o)
    s := Sale(item: "tea, green", qty: 2, price: 3.5, note: none)
    try s.WriteCsv(mut out, &o)

    // Read records back into Sales.
    data := "pens,10,1.25,\nink,x,2,\n"
    var r := Reader.Of(data.AsBytes())
    buf := try sys.arena.Slice[u8](1024)
    var rs := csv.Records.New(mut r, mut buf[..], &o)
    for rec := try rs.Next() {
        sale := Sale.FromCsv(sys.arena, &rec) else e {
            try sys.Print("line {line}: {e}\n", (line: rec.Line(), e))
            continue
        }
        try sys.Print("{qty} x {item} at {price}\n", (qty: sale.qty, item: sale.item, price: sale.price))
    }
}
```

```text
$ mk run sale.mk
item,qty,price,note
"tea, green",2,3.5,
10 x pens at 1.25
line 2: ParseError
```

- The two lines `WriteCsv` and `WriteCsvHeader` write end in CR LF.
- `note` is `none`, so its field is empty; the empty fourth field reads back as `none`.
- `x` does not read as a `u32`, so the second record fails with `ParseError`, and the loop goes on to the next.

To read a file that starts with a header, call `Next` once before the loop.

## API

<!-- api:begin -->
Stability: Stable.

### `Codec`

```
export Codec(g: mut Gen, t: Type)
```

Codec writes, for the struct t, `T.FromCsv(a, r)`, which reads one
Record into a T, and `T.WriteCsv(w, o)` and `T.WriteCsvHeader(w, o)`,
which write a T, or the field names, as one record. Use it as
`gen csv.Codec(T)`. Each exported field is one field of the record, in
declaration order.

str is written as it is, bool as true or false, the integers in decimal,
f32 and f64 as their Format writes them (inf and nan included), and ?T
as T or an empty field for none; an empty field reads back as none, so
some("") of a ?str reads back as none. FromCsv fails with ParseError
when the record has another field count or a field does not read as its
type, and with Utf8Error when a field is not UTF-8. It copies every str
into a, so its result borrows a alone and the reader may move on.
WriteCsv quotes a field only where RFC 4180 needs it and ends the record
with CR LF. WriteCsv and WriteCsvHeader panic on options a reader
refuses (see Records.New), and, when T has a number field, on a
delimiter or quote that a number's text can hold (digits, + - . e E n a
i f).

E0903 at a field that is not exported or of another type (char, a
struct, a list), and at a struct with no field. Runs at compile time;
the emitted FromCsv allocates its strings in a, WriteCsv nothing.

### `DefaultOptions`

```
export const DefaultOptions: Options
```

The RFC 4180 defaults: comma, double quote, fields up to 1 MiB, records
up to 4,096 fields.

### `Error`

```
export open type Error = Quote | TooLarge | ReadError
```

Every way Records.Next fails.

### `Options`

```
export struct Options
```

How a reader splits and a writer joins fields. Start from
DefaultOptions and assign the fields to change. A literal of Options
does not compile outside std.csv, so a field added later breaks no
program.

Fields:

- `delimiter: u8`: The byte between fields; not CR, LF or quote_byte.
- `quote_byte: u8`: The byte that opens and closes a quoted field; not CR or LF.
- `field_bytes: int`: The most bytes one field may hold after unquoting; more is TooLarge.
- `record_fields: int`: The most fields one record may hold; more is TooLarge. Records also
  caps it at buf.Len() / 32, the room its field index takes.

### `Quote`

```
export error Quote(line: i64, column: int)
```

A quote where RFC 4180 allows none: inside a field that did not start
with one, after a closing quote other than before a delimiter or the end
of the line, or a quoted field the input ends inside. line and column
are 1-based and count bytes; a CR LF counts as one line break.

### `Record`

```
export view struct Record
```

One record: its fields are views of the buffer Records reads into, valid
until the next Next.

### `Record.Field`

```
export Record.Field(&self, i: int): ?str from self
```

- Flows: The result borrows from self.

Field i, unquoted, as a view of the reader's buffer; none when i is not
below Len() or the field's bytes are not UTF-8 (tell the two apart by
i < Len()). O(1) when the record's bytes are UTF-8 as a whole, which
Next checked once; else O(field length) for the field's own check;
allocates nothing.

### `Record.Len`

```
export Record.Len(&self): int
```

The number of fields. O(1); allocates nothing.

### `Record.Line`

```
export Record.Line(&self): i64
```

The line the record starts on, from 1, counting LF bytes. O(1);
allocates nothing.

### `Records`

```
export view struct Records
```

A reader of RFC 4180 records from r, using buf for the bytes read and
the field index. Make it with New and call Next until it returns none.

### `Records.New`

```
export Records.New(r: mut Reader, buf: mut []u8, o: &Options): Records from r, buf
```

- Flows: The result borrows from r and buf.

A reader of the records in r, with buf for the bytes it reads and its
field index. Panics on options a reader cannot use (a delimiter or
quote that is CR or LF, the two equal, a negative field_bytes or
record_fields below 1): they come from the program. O(1); allocates
nothing.

The index takes 8 bytes per field slot at the end of buf, with
min(record_fields, buf.Len() / 32) slots; a record fits when it has at
most that many fields and its bytes, its line break included, fit in
the rest. So a buf of at least 32 * N and 4 * B / 3 bytes holds every
record of N fields and B bytes, and a record within the options that
does not fit buf is TooLarge too.

### `Records.Next`

```
export Records.Next(mut self): Error!?Record from self
```

- Flows: The result borrows from self.
- Errors: Error (ReadError, Quote(line: i64, column: int), TooLarge(line: i64))

The next record, or none at the end of input. After Quote or TooLarge,
every later call fails with the same error. ReadError is r's; the reader
keeps its place, and calling again resumes. O(record bytes) amortized,
the bytes searched 16 at a time; allocates nothing.

### `TooLarge`

```
export error TooLarge(line: i64)
```

A field longer than field_bytes, a record with more fields than
record_fields, or a record that does not fit the buffer; line is the
record's first line.

### `WriteRecord`

```
export WriteRecord(w: mut Writer, values: []str, o: &Options): WriteError!void
```

- Errors: WriteError

Writes values as one record to w, then CR LF, quoting a field only
where RFC 4180 needs it. WriteError is w's, and after it w holds an
unknown prefix of the record. Panics on options a reader refuses too
(see Records.New). O(total bytes), each field searched once; allocates
nothing.

<!-- api:end -->
