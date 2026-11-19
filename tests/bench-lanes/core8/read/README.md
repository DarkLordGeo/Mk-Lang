# read (idiomatic table)

Task: read the 50 MB log (`regex-50mb.log`, std-regex's input: kind `log`, seed 5, 50,000,038 bytes) through each standard library's buffered file reader with a 64 KiB buffer, by read calls of `want` bytes each, `r` times. `read <file> <want> <r>` prints the bytes read and the wrapping (u64) sum of each call's first byte.

- `want` 64: the buffered path. Each call copies 64 bytes out of the reader's buffer; one file read refills it every 1,024 calls.
- `want` 131072: the direct path. A request at least as large as the buffer is read straight into the caller's bytes (Go bufio's rule, which Mk's `Reader.Read` and Rust's `BufReader` follow; C's `fread` and Zig's `readSliceShort` fill the request their own way).

Each column uses its idiomatic form: Mk `fs.File.Reader` and `Reader.Read`; Go `bufio.NewReaderSize(f, 65536)` and `Read`; Rust `BufReader::with_capacity(65536, f)` and `Read::read`; Zig `std.Io.File.Reader` over a 64 KiB buffer and `std.Io.Reader.readSliceShort`; C `fopen`, `setvbuf(f, NULL, _IOFBF, 65536)` and `fread`.

Checksums (from a Go model over the data file, the sum of the bytes at offsets 0, `want`, 2 `want` and so on, times `r`, and the file's length times `r`; and every column's `tools/bench verify`):

| Variant | Arguments | Checksum |
|---|---|---|
| small, quick | `{data} 64 1` | `50000038 57480414` |
| small, publish | `{data} 64 40` | `2000001520 2299216560` |
| direct, quick | `{data} 131072 1` | `50000038 28474` |
| direct, publish | `{data} 131072 120` | `6000004560 3416880` |

The publish sizes are a guess until `tools/bench publish` times the row on a quiet machine (the contract's floor is 1 s for the fastest cell).

Zig's cell fills its buffer first when `want` is below the buffer's size: `std.Io.File.Reader` of Zig 0.17 reads only a vector's first slice on Windows, so a 64-byte `readSliceShort` from an empty buffer was one file read per call (1.243 s for the quick size, against 33 to 83 ms for the other cells, in the first verify run).
