// Why: a protocol upgrade (WebSocket) takes the connection away from the
// HTTP server, together with any bytes the server already read past the
// request.
//
// What: Taken, a connection a handler took from its server, and
// Taken.Abandon.
//
// How: Link.Takeover (takeover.mk) makes one from a ServeWith handler's
// link; websocket.Accept consumes one (RFC 6455 4.2.2's server side), as
// Go's http.Hijacker hands net/http's connection over. Taken is linear, so
// the connection is always consumed or closed.

import std.net

/// A connection taken from its server, as Link.Takeover returns it: conn,
/// and rest, the bytes the server read past the request, such as a
/// client's first frames sent right behind its handshake. rest comes
/// before anything read from conn. It is linear: websocket.Accept consumes
/// it, or Abandon closes it. It keeps the link's loans, so it lives inside
/// the handler that took it.
export linear struct Taken {
    /// The connection.
    export conn: net.Conn
    /// The bytes the server read past the request, which come before anything
    /// read from conn.
    export rest: []u8
}

/// Closes the taken connection at once and sends nothing: a TLS connection
/// sends no close_notify. Never blocks or fails. O(1); frees the
/// connection's box, if it has one.
export Taken.Abandon(self) {
    Taken(conn, rest: _) := self
    conn.Abandon()
}
