# http-fetch (idiomatic table)

Workload: `n` GET requests, one after another, through the language's standard HTTP client with keep-alive, to a responder in the same program over a loopback TCP connection. The row measures the client: building and writing the request, reading the reply's head and body, and the keep-alive pool.

The targets come from splitmix64 seeded with 1 (`next` adds `0x9E3779B97F4A7C15`, then the usual two multiply-xorshift rounds): request `i` is `GET http://127.0.0.1:<port>/items/<next() % 1000000>`.

The responder is the same code in every column, the same byte loops (a scalar scan for CRLF CRLF, a loop to the target's `/`; no `bytes.Index` in Go), and uses no HTTP library. It accepts one connection, reads each request's head to its blank line, takes the decimal id after `/items/`, and writes `HTTP/1.1 200 OK`, `Content-Type: text/plain`, `Content-Length: L` and a body of `L = 16 + id % 1024` letters `a` to `z` repeating, in one write.

Checksum: over every reply, its status, its body's length and the body's first and last bytes, summed with wrapping arithmetic.

| Column | Client | Per request | Memory |
|---|---|---|---|
| mk | `std.http.Client.Fetch`, `DefaultClientLimits` (no decoders) | `url.Parse` of the target, the reply's fields and body copied into the arena | one `Arena`, `Reset` after each request |
| go | `net/http.Client`, a `Transport` with one idle connection per host and compression off | `http.Get` (parses the URL), `io.ReadAll` of the body, `Body.Close` | the garbage collector |
| c, rust | no std HTTP client | | |
| zig | `std.http.Client` (0.17), its connection pool with keep-alive; the responder a task of `Io.Group.concurrent` | `std.Uri.parse` of the target, `request`, `sendBodiless`, `receiveHead`, `Reader.allocRemaining` of the body | an `ArenaAllocator`, reset after each request |

`Fetch` runs `Do` in a sub-arena and copies the fields and body out, then frees the sub-arena; Go's client hands back the body's reader and the program reads it whole.
