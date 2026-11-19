# log (idiomatic table)

Workload: write `n` log lines `INFO event <k> took <(k * 7) mod 1000> us`, one per call, through the language's standard logger into one in-memory buffer, emptied before each of `reps` reps. No I/O: the row measures the logger and its formatting, not the console or the disk.

Checksum: the sum over reps of the buffer's byte length.

| Column | Logger | Buffer |
|---|---|---|
| mk | `std.log` `Logger.New(mut w, Level.Info)`, `Info("event {k} took {d} us", (k: k, d: d))` | one `Text`, `Clear` before each rep, written through `Text.Writer` |
| go | `log.New(&buf, "INFO ", 0)`, `Printf("event %d took %d us", k, d)` (log adds the newline) | one `bytes.Buffer`, `Reset` before each rep |
| c | no logger: `memcpy` of `INFO ` then `snprintf` | one `malloc` buffer, doubled when less than 64 bytes are left |
| rust | no std logger (the `log` crate is not std) | |
| zig | `std.log.info("event {d} took {d} us", ...)` with `std_options.logFn` set to a function that prints the level word, the comptime format and a newline into the buffer (std.log's own hook; its `defaultLog` writes to stderr only) | one `std.Io.Writer.Allocating`, `clearRetainingCapacity` before each rep |

Mk's `Info` walks its format at run time until the language lets it forward the format to `Print`; the row's first number is that interim body's.
