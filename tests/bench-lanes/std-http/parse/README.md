# http-parse (idiomatic table)

Workload: build `n` HTTP/1.1 requests, then `reps` times send them over a loopback TCP stream and parse each one back with the language's standard request parser.

The requests come from splitmix64 seeded with 1 (`next` adds `0x9E3779B97F4A7C15`, then the usual two multiply-xorshift rounds). For each request, `r = next()`:
- method `POST` when `r % 4 == 0`, else `GET`; target `/api/v1/items/<(r >> 8) % 1000000>`, with `?page=<(r >> 41) % 100>&sort=asc` when bit 40 of `r` is set;
- the fields `Host: bench.example.com`, a Chrome `User-Agent`, `Accept`, `Accept-Language: en-US,en;q=0.9`, `Accept-Encoding: gzip, deflate, br`, `Cookie: session=<next() in decimal>`, `Connection: keep-alive`;
- for a POST, `Content-Type: application/json`, `Content-Length: L` with `L = 16 + (r >> 20) % 240`, and a body of `L` letters `a` to `z` repeating.

Each rep writes the requests in batches of at most 32,768 bytes (a batch always fits the loopback socket's buffer, so the one task or goroutine writes and then reads with no second thread), and parses every request of the batch before the next is written.

Checksum: over every request, the length of the method, the path, the query (without `?`), the body and every field's name and value, plus the number of fields, summed with wrapping arithmetic. In Go, `Host` is taken out of `Header` into `Request.Host`, so the program adds it back.

| Column | Parser | Reader | Memory |
|---|---|---|---|
| mk | `std.http.ReadRequest` (strict RFC 9112: CRLF only, the smuggling rules, offsets in errors) | `net.Stream.Reader`, 4,096-byte inline buffer | one `Arena`, `Reset` after each request |
| go | `net/http.ReadRequest`, the body with `io.ReadFull` | `bufio.NewReaderSize(conn, 4096)` | the garbage collector |
| c, rust | no std HTTP | | |
| zig | not written yet | | |

The Mk reader has no peek, so `ReadRequest` takes the head one `Reader.Read` call per byte to leave a pipelined next request in the reader. Go's `bufio.Reader` scans its buffer for each line.
