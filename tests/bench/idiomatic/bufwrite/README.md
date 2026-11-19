# bufwrite (idiomatic, formatted buffered file writes)

**Task.** Write n formatted lines to a file through a 64 KiB buffer with each language's standard formatter and buffered file writer, then read the file back. This row prices buffered file writes. `bufwrite <n>`:

```
p = <the temporary directory>/mkbench-bufwrite-<column>.txt
f = create p; w = a buffered writer over f with a 64 KiB buffer
for i in 0 .. n-1:
    h = i * 0x9e3779b97f4a7c15                 (wrapping, u64)
    write to w, through the std formatter: "<i decimal> <h lowercase hex, no leading zeros> <i*7 decimal>\n"
flush w; close f
b = read the whole file p
total = len(b) + FNV-1a 64 of b               (wrapping)
remove p
```

Every column writes the same bytes (decimal, lowercase hex with no padding, `\n` only, binary mode), so the checksum over the bytes read back checks both the formatter and the writer. That departs from the letter of tests/bench/README.md's "Checksums over values" (formatting rows check parsed-back values, never the output bytes) on purpose: bytes and values differ only where a value has more than one right spelling, as a float or a padded field has, and here every field is an integer in decimal or in lowercase hex with no padding, which has exactly one, so equal values mean equal bytes and comparing the bytes is the stricter check. The temporary directory is each language's own (TMP on Windows), as `std-fs2/fs` does, so all columns write to one volume. The file is read back as soon as it is closed, so the row is meant to price the formatter, the buffered writer and the write calls rather than the disk; whether the OS file cache holds it all is not checked here.

**Cells.** Every column formats each line through its general formatter (critic F26: no column bypasses it):

| Column | Formatter | Buffered writer | Read back |
|---|---|---|---|
| mk | `Print(mut w, "{u} {h:x} {k}\n", ...)` | `fs.File.Writer(io, buf)` over a 64 KiB array, `Flush` | `fs.ReadBytes` |
| c | `fprintf(f, "%llu %llx %llu\n", ...)` | `setvbuf(f, NULL, _IOFBF, 65536)` on an `fopen("wb")` stream | `fread` of the whole file |
| rust, rust-ovf | `writeln!(w, "{} {:x} {}", ...)` | `BufWriter::with_capacity(65536, File)` | `std::fs::read` |
| zig-fast, zig-safe | `w.interface.print("{d} {x} {d}\n", ...)` | `File.writer(io, &buf)` over a 64 KiB array, `flush` | `Dir.readFileAlloc` |
| go | `fmt.Fprintf(w, "%d %x %d\n", ...)` | `bufio.NewWriterSize(f, 65536)` | `os.ReadFile` |

Sizes: quick `100000`, publish `3000000` (a line is at most 34 bytes, so the file is at most 102 MB). The publish size is not yet checked against the 1 s floor: `tools/bench verify -publish-size bufwrite` sets it. Overflow class `wraps` (the hex value and the checksum).
