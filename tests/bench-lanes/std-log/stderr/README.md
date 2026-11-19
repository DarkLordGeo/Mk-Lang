# log-stderr (idiomatic table)

Workload: write 200,000 access-log records of about 80 bytes (`request id=... path=/api/v1/items/... status=200 bytes=... took_us=...`, five integer fields) through each standard library's task-safe standard-error logger from one task, then print the record count and the sum of the bytes fields. Each record is one write system call on both sides. Where standard error goes is the runner's choice, the same for both columns.

| Column | Logger | Write |
|---|---|---|
| mk | `log.Stderr(Level.Info)`, `Sink.Info` per record | formatted into a 4 KiB stack buffer (`core.Capped`), then `core.IoStdio`: standard error's lock, the record copied into the stream's 8 KiB buffer behind any buffered bytes, one write |
| go | the standard logger (`log.Default()`, flags 0, prefix `INFO `), `log.Printf` per record | formatted into a pooled buffer, one `os.Stderr.Write` under the logger's mutex |
| c, rust, zig | no std logger that writes a record per write (`bench.json`'s `skip`) | |

The file row `std-log/sink` measures `log.ToFile`; this row measures the stream path, which has a different cost (rt8's `StreamPut`), so the file row does not stand in for it.

Not published: `tools/bench publish` writes the numbers.
