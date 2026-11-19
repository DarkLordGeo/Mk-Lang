# conn-churn (idiomatic table)

The row for a connection's set-up and tear-down, where the peer's address comes from the accept itself, so Mk pays no `getpeername` per accepted connection. n loopback TCP connections, one after another, of one thread: each round dials the listener, accepts the connection and reads the peer's address from the accept, writes two bytes (the round's low byte twice) at the accepted end, reads one at the dialled end, and closes the dialled end, then the accepted one.

Every cell takes the peer's address from the accept itself: Mk's `Listener.Accept` (the runtime's accept op keeps it in the handle's record: AcceptEx's buffer through GetAcceptExSockaddrs on Windows, accept4's sockaddr under epoll and io_uring), C's `accept(l, &sa, &len)`, Rust's `TcpListener::accept`, Zig 0.17's `Server.accept` (its `Stream.socket.address`) and Go's `Listener.Accept` (`RemoteAddr`). A round whose peer is not 127.0.0.1 with a port counts as failed. Mk's `Dial` still reads its peer with one `getpeername`, as Go's dial does; no other cell does.

Checksum: sum = 31 sum + each byte read, wrapping; byte i is i mod 256, so the expected value is tcp-pingpong's, from a Go model over i = 0..n-1 (1,000: `2447299257384679924`; 100: `6955345978518086706`). The second byte of a round is never read, so it does not enter the sum.

Sockets: the teardown is a reset, not a graceful close. The dialled end closes with the second byte unread, so its TCP resets the connection (RFC 1122 4.2.2.13) and neither end enters TIME_WAIT; that one extra byte per round is the price. No linger option is needed, so every cell does the same with its language's plain close. A run therefore leaves no TIME_WAIT entries behind, and the row stays under a 10,000-socket cap however many runs a publishing run makes. Observed on Windows (bench.json's notes); on Linux, close(2) with unread data sends a reset too (tcp_close), not yet run for this row.

Numbers come only from `go run ./tools/bench verify` or `publish`. None is recorded yet.
