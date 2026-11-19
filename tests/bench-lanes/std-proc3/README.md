# std-proc3 benchmark rows (idiomatic standard library)

Rows for the idiomatic table of tests/bench/README.md. Each baseline uses its standard library's idiomatic form and prints one checksum line. Only `tools/bench` publishes a number from these rows.

| Row | Work | Mk | Baselines |
|---|---|---|---|
| `spawnpipe` | `n` runs of the program itself with the argument `child`, which copies standard input to standard output; 64 bytes written to a stdin pipe, which is then closed, stdout read to the end from a pipe, stderr the null device, each exit waited for; prints `n bytes fails` | `proc.Spawn`, `PipeWriter.WriteAll`, `PipeReader.Read`, `Child.Wait` | Go `os/exec` (`StdinPipe`, `StdoutPipe`, `Start`, `Wait`), Rust `std::process::Command::spawn` with `Stdio::piped()`, Zig `std.process.spawn` with `.pipe` streams; C has no std facility (skipped in `bench.json`) |
| `pipethru` | the program runs itself with `child n`; the child writes `n` bytes (one 64 KiB splitmix64 chunk, filled once, written again and again) to its stdout; the parent reads the pipe into a 64 KiB buffer and sums the bytes; prints `bytes sum code` | `proc.Spawn` with `Stdio.Pipe`, `PipeReader.Read` on the end Spawn returns, `Child.Wait` | Go `cmd.StdoutPipe()`, Rust `ChildStdout::read`, Zig `std.process.spawn` with `.pipe` and `readStreaming`; C skipped |
| `pipepair` | one process, one anonymous pipe: a task writes `n` bytes (the same chunk) in 64 KiB writes and closes it; the main task reads into a 64 KiB buffer and sums; prints `bytes sum` | `proc.Pipe`, `PipeWriter.WriteAll` in a task, `PipeReader.Read` (the blocking-pool ends) | Go `os.Pipe` with a goroutine, Rust `std::io::pipe` with a thread; Zig (no anonymous pipe on Windows in its std) and C skipped |

std-proc2's `run` row (`proc.Run`) measures `Run`, which is built over `Spawn`.
