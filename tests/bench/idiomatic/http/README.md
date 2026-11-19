# http (idiomatic, concurrency)

**Task.** Serve `GET /` with `200 OK`, `Content-Type: text/plain` and the 14-byte body `Hello, World!\n` (with `Content-Length`) over HTTP/1.1 keep-alive, with the language's standard HTTP server (hello world under a load generator at 1,000 connections, below the 10,000-socket cap). `http <port>` listens on `127.0.0.1:<port>` and serves until it is killed. The spec is `http.json`, not `bench.json`: the row has its own measurement, `tools/bench http` (`tools/bench/httpbench.go`), because a server is not a program hyperfine can time.

**Load.** tools/bench's own generator: 1,000 keep-alive connections, one request outstanding on each, 5 s of warm-up, then 30 s measured; req/s, p50 and p99 of the measured requests. Every response's status line, `Content-Length` and body are checked, and a server that closes the connection fails the cell. So does a server that stops answering: when the measured window ends, every connection gets a 5 s deadline to finish its last request, and a connection still blocked then fails the cell, which names it. Without that, one stalled connection would hang the generator, and with it the machine lock that `http -publish` holds. Three sweeps in rotated column order, each with a fresh server process and fresh connections; req/s is the median of the sweeps. The generator is a raw HTTP/1.1 client on Go's `net.Conn`, one goroutine per connection, instead of `net/http.Client`, so that it spends little per request; it is still only Go's std, and nothing is installed. Connections close with a reset (linger 0), so no TIME_WAIT sockets pile up between sweeps; at most 2,000 sockets are open on the machine at once.

**Machine.** `tools/bench http -publish` holds the machine lock, sets its own affinity to `0xff` (CPUs 0 to 7) before it starts a server, which inherits it, then to `0xff00` (CPUs 8 to 15) for the generator. The Go server and the generator run with GOMAXPROCS 8 and the Mk server with `MK_CARRIERS=8`.

**Latency resolution.** On Windows Go's clock advances in steps of the interrupt time, about 0.5 ms on the measuring machine (measured on 2026-11-07, and by every publish: `clock_step` in results.json), so each latency is known to that step. RESULTS.md also prints the mean latency by Little's law (connections / req/s), which is exact.

**Cells.**

| Column | Server | Model |
|---|---|---|
| go | `net/http` `ListenAndServe` with one `HandleFunc` | goroutines, GOMAXPROCS 8 |
| zig-fast, zig-safe | `std.http.Server` over `std.Io.net` with `std.Io.Threaded`, one `Io.Group.concurrent` task per connection (each on its own OS thread, `concurrent_limit` unlimited) | OS threads |
| c | none: no std HTTP (C's std has no sockets) | |
| rust | none: no std HTTP (`std::net` is TCP only; hyper and axum are crates, and a hand-written server would be the stand-in the contract forbids) | |
| mk | `std.http.Serve` with one `Dispatch` route (`mk/http.mk`, a copy of `tests/bench-lanes/std-http/hello`): an 8 KiB read and a 4 KiB write buffer on each connection task's stack, one arena per connection reset after each response | tasks, `MK_CARRIERS=8` |

**Checks.** `tools/bench http` builds every column and runs 50 connections for 2 s unpinned (provisional); `http -full` runs the 1,000 connections for 5 s unpinned. Both passed in every column (2026-11-07); those are not published numbers.

The Mk column: `http` and `http -full` passed in every column, Mk included, with an mk built from the tree at 17:01 on 2026-11-07, unpinned: not numbers.
