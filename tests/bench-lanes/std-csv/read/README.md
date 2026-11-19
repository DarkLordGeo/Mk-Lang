# csv-read (idiomatic table)

Workload: read the 50 MB log file of std-regex (`tools/bench gen` kind `log`, seed 5; lines of seven space-separated fields, no quote) as CSV records with the delimiter space, through each standard library's CSV reader over a buffered file reader, `r` times, and count the records, the fields and the field bytes.

| Column | Reader | Buffer |
|---|---|---|
| mk | `csv.Records` over `fs.File.Reader` with no inline buffer, `Record.Field` per field (a `str`, so each field's UTF-8 is checked) | one 64 KiB `buf`: the bytes read and the field index |
| go | `encoding/csv` with `Comma ' '`, `ReuseRecord`, `FieldsPerRecord -1` over `bufio.Reader` (64 KiB) over `os.File` | one string per record (encoding/csv's) |
| c | skipped: no std CSV reader; the hand-written reader is a stand-in, kept in `../standin/` for a labelled table | |
| rust | skipped: Rust std has no CSV reader; the csv crate is a stand-in here and needs cargo, which tools/bench's single-file Rust cells do not run | |
| zig | skipped: no std CSV reader | |

The checksum is the Go column's run: the three counts, summed over `r`.

The ratio that counts is Mk against Go's `encoding/csv`, the only other standard library with a CSV reader.
