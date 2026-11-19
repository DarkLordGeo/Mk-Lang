# log-sink (idiomatic table)

Workload: write 200,000 access-log records of about 80 bytes (`request id=... path=/api/v1/items/... status=200 bytes=... took_us=...`, five integer fields) through each standard library's task-safe file logger into a new file from one task, then print the record count and the file's size (the checksum: every logger wrote the same bytes). Each record is one write system call on both sides.

| Column | Logger | Write |
|---|---|---|
| mk | `log.ToFile(&m, Level.Info)`, `Sink.Info` per record | formatted into a 4 KiB stack buffer (`core.Capped`), one `File.WriteAll` under the file's `Mutex`, taken through `io.NoCancel()` |
| go | `log.New(f, "", 0)`, `Logger.Printf` per record | formatted into a pooled buffer, one `File.Write` under the logger's mutex |
| c, rust, zig | no std file logger (`bench.json`'s `skip`) | |

Not published: `tools/bench publish` writes the numbers.
