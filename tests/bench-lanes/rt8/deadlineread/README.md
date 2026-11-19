# deadlineread (idiomatic table)

Task: `deadlineread <n> <deadline>` makes `n` round trips of one byte over a loopback TCP connection. An echo task, goroutine or thread reads each byte and writes it back; the main one writes `i % 251`, then reads the echo. `sum = 31 sum + byte` (wrapping). It prints `n`, `sum` and the failed calls (0).

- Variant `bare` (`deadline` 0): every read is plain.
- Variant `deadline` (`deadline` 1): every read is bounded by the library's one-operation timeout of one second, which never fires.

The row records Mk's `deadline` cell against its `bare` cell, with its cause: a deadline scope costs one arm and one synchronous disarm on std.task's timer heap, and no system call.

Each column uses its idiomatic form. None of them makes a system call per read for its timeout, so the comparison measures the read and the library's timeout bookkeeping, not an extra kernel crossing in one column:

| Column | Timeout form | Cost per read |
|---|---|---|
| Mk | a scope per read with `Scope.CancelAfter(1 s)` | one timer-heap insert and remove |
| Go | `conn.SetReadDeadline(time.Now() + 1 s)` per read | one runtime timer update |
| Rust | `TcpStream::set_read_timeout(Some(1 s))` once, before the loop: the timeout persists and bounds every later read | none |
| C | `setsockopt(SO_RCVTIMEO)` once, before the loop (a DWORD of milliseconds on Windows, a `timeval` on Linux) | none |
| Zig | not written (bench.json's `skip`) | |

Rust's and C's forms set an OS socket option that bounds each later receive, so setting it once is their idiom. A `poll` before each `recv` (two system calls per read) or a `set_read_timeout` per read (one `setsockopt` per read) would add a kernel crossing to their cells that Mk's and Go's cells do not pay.

Checksums come from a Go model: `sum = 31 sum + i % 251` over `0..n`, wrapping.

| Size | Arguments | Checksum |
|---|---|---|
| quick | `1000 d` | `1000 13725477746376704254 0` |
| publish | `100000 d` | `100000 15942106405504090451 0` |
