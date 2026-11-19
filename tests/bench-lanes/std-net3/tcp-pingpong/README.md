# tcp-pingpong (std-net3)

The row for `net.Conn`'s `Read` and `WriteAll` over a stream (`Stream.Conn`). One loopback TCP connection of one thread: each of n rounds writes 32 bytes at the dialled end, reads them whole at the accepted end, writes them back and reads them whole at the dialled end. Every cell sets TCP_NODELAY on both ends.

The Mk column takes a mode: `conn` (the published one) goes through `net.Conn`, `stream` through `net.Stream`, so the difference between the two variants is what a Conn adds per call: two uncontended `Mutex` operations and one indirect call. The C, Rust, Zig and Go cells ignore the mode.

Checksum: sum = 31 sum + byte 0 of each answer, wrapping; byte 0 of round i is i mod 256, so the expected value comes from a model in Python, the same as udp-pingpong's (std-net2).

Numbers come only from `go run ./tools/bench verify` or `publish`.
