# http-stream (idiomatic table)

Workload: `n` POST requests, one after another, through the language's standard HTTP client with keep-alive, to a responder in the same program over a loopback TCP connection. Each request streams a 65,536-byte file as its body. The row measures the client's streamed upload: the request head, reading the body from a file and writing it with its framing, then reading the reply. It covers `Client.DoStreaming` in Mk.

At the start, the program writes a temporary file of 65,536 bytes, byte `k` being `'a' + k % 26`. Request `i` (from 0) is `POST http://127.0.0.1:<port>/up/<i>`. Its body is the file, opened anew for each request: chunked when `i` is even, and with `Content-Length: 65536` when `i` is odd.

The responder is the same code in every column, the same byte loops (a line copied a byte at a time, each body piece summed into a local and added once; no `bufio.ReadSlice` in Go), and uses no HTTP library. It accepts one connection and reads each request's head to its blank line, noting `Transfer-Encoding: chunked` or `Content-Length: L`, field names matched without case (Zig's client sends them in lower case). It reads the body by that framing; a chunked body has no extensions or trailers. It answers `HTTP/1.1 200 OK`, `Content-Length` and a body holding, in decimal, the body's byte count times 1,000,003 plus the sum of its bytes.

Checksum: over every reply, its status plus the number in its body, summed with wrapping arithmetic.

| Column | Client | Per request | Memory |
|---|---|---|---|
| mk | `std.http.Client.DoStreaming`, `DefaultClientLimits`, `len` none or `some(65536)` | `url.Parse` of the target, `fs.Open` and `File.Reader` over a 16 KiB buffer, the reply read through `Exchange.Body` | one `Arena`, `Reset` after each request |
| go | `net/http.Client`, a `Transport` with one idle connection per host and compression off | `http.NewRequest` with the `*os.File` as body (`ContentLength` -1 or 65,536), `io.ReadAll` of the reply, `Body.Close` | the garbage collector |
| c, rust | no std HTTP client | | |
| zig | `std.http.Client` (0.17), keep-alive; the body a `File.Reader` streamed into the request's `BodyWriter` (`transfer_encoding` `.chunked` or `.content_length`) | `std.Uri.parse`, `request`, `sendBody`, `streamRemaining`, `end`, `receiveHead`, `Reader.allocRemaining` of the reply | an `ArenaAllocator`, reset after each request |

DoStreaming writes the head through the connection's 4 KiB Writer and the body in pieces of at most 16 KiB, each one chunk when chunked. Go's Transport writes a chunked body through its own buffered writer.
