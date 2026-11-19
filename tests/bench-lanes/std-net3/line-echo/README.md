# line-echo (std-net3)

The row for `net.Conn`'s `Reader` and `Writer` (`Conn.Reader`, `Conn.Writer`): a buffered line echo, as a line protocol (a chat server, a Redis-style command channel) uses a connection. Each of n rounds writes 16 lines of 32 bytes through the client's 4096-byte writer and flushes, reads each line at the server up to its newline and writes it back through the server's writer, flushes, and reads the 16 lines back at the client.

Two variants, each a row of its own:

- `tcp`: one loopback TCP connection of one thread, TCP_NODELAY at both ends. The Mk column takes `Stream.Conn` at both ends, so the row measures the Conn's Reader and Writer over the poller.
- `inmem`: no sockets. Two in-memory pipes of 4096 bytes, each under its own mutex; one connection per end over a transport boxed by `net.NewConn`, which takes a read or a write lock of its own and makes one indirect call per operation. Every cell builds the same shape in its language (README of inmem-conn), and picks the transport at run time from the mode (`null`: a transport that reads the end of the stream, which no variant runs).

Lines are framed by their newline: byte 0 of the k-th line is 32 + k mod 64, never a newline, and the checksum folds each line's length as well as its byte 0, so a split or merged line fails the row.

Checksum: sum = 31 sum + byte 0 + 256 * length of each line read back at the client, wrapping, from a Python model over k = 0..16n-1.

Numbers come only from `go run ./tools/bench verify` or `publish`.
