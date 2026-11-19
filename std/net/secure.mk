// Why: https and wss need TLS, but std.http and std.websocket must not
// import std.tls; net.Secure is the connector they call instead.
// What: Secure, Secure.Of and Secure.Connect.
// How: a function and its state, erased by std.core so callers never name
// the state's type. Only std makes one: Secure.Of takes a core.ErasedCall,
// which only a std module can build (std.core is std-internal), and
// tls.Connector and tls.Acceptor are the two that ship.

import std.core

/// A plain value that makes a connection secure, made only by std:
/// tls.Connector gives one for a client, tls.Acceptor for a server. Pass it
/// to std.http and std.websocket, which import no TLS of their own. Copy
/// it freely; it borrows the configuration it was made from. call is
/// std's; a program has no use for it.
export struct Secure {
    /// The call std makes the connection secure through; a program has no use
    /// for it.
    export call: core.ErasedCall
    thunk: fn(&Secure, h: &Heap into conn, io: Io, conn: Conn, host: str): Error!Conn from h, conn
}

/// The connector over c, which std made with core.ErasedCall.Of[T, F] from
/// a state of type T and its function, F being
/// `fn(&T, h: &Heap into conn, io: Io, conn: net.Conn, host: str): net.Error!net.Conn from h, conn`:
/// given its state, a heap for the new connection's state, the plain
/// connection and the host name (empty on a server), the secure
/// connection. Only std calls this: no program can build a
/// core.ErasedCall. A c made from another pair of types panics (code 8)
/// at Connect, before the function runs. O(1); allocates nothing.
export Secure.Of[T: Share](c: core.ErasedCall): Secure from c {
    return Secure(call: c, thunk: SecureCall[T])
}

// SecureCall recovers the state and the function as the types Of was
// given; ErasedCall checks them (a mismatch panics with code 8).
SecureCall[T: Share](s: &Secure, h: &Heap into conn, io: Io, conn: Conn, host: str): Error!Conn from h, conn {
    f := s.call.Fn[T, fn(&T, h: &Heap into conn, io: Io, conn: Conn, host: str): Error!Conn from h, conn]()
    st := s.call.State[T, fn(&T, h: &Heap into conn, io: Io, conn: Conn, host: str): Error!Conn from h, conn]()
    return try f(st, h, io, conn, host)
}

/// Makes conn secure for host (empty on a server) by running the
/// connector's handshake, through one indirect call (O(1) here plus the
/// handshake's cost). On success conn is inside the result. On failure it
/// stays the caller's, mid-handshake, to Close or Abandon. Errors: the
/// connector's; a failed handshake is SecureFailed(why), its time limit
/// time.TimedOut, a cancel Canceled, and after a cancel part of the
/// handshake may have been sent. It allocates on h what the connector's
/// state needs. The connection is boxed on h and holds conn, so h's loan
/// may reach what conn reaches (`h: &Heap into conn`).
export Secure.Connect(&self, h: &Heap into conn, io: Io, conn: Conn, host: str): Error!Conn from h, conn {
    thunk := self.thunk
    return try thunk(self, h, io, conn, host)
}
