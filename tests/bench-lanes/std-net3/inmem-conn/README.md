# inmem-conn (std-net3)

The row for the layer `net.Conn` adds over a transport (`net.NewConn`, doc/reference.md): no sockets, one thread. The transport keeps one 32-byte message under its own mutex, as a `Send + Share` transport must; each round writes 32 bytes through the connection and reads them back.

Each cell is its language's shape of a connection over an erased transport: a lock per direction plus one indirect call (Go's `net.Conn` and `crypto/tls.Conn` pattern, Rust's `Box<dyn Trait>`, a C function-pointer table, a Zig vtable). The Mk column's `direct` mode calls the transport itself, so `conn` minus `direct` is the cost of the Conn's lock, indirect call and box branch per operation. Every cell picks the transport at run time from the mode argument (the loop, or for `null` a transport that reads the end of the stream, which no variant runs), so no optimiser sees one transport type at the loop and no cell needs an opacity device (NET3-R1).

Checksum: sum = 31 sum + byte 0 of each read, wrapping, from a Python model (byte 0 of round i is i mod 256).

Numbers come only from `go run ./tools/bench verify` or `publish`.
