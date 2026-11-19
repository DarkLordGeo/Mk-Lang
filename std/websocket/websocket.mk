// Why: programs talk to browsers and servers over WebSocket (RFC 6455), and
// the module's shared vocabulary (its errors, message kinds and options)
// belongs in one place that the codec, the connection and the handshake
// files all read.
//
// What: the errors Protocol, TooLarge, PeerClosed and Handshake and the open
// Error set; Kind and Message; Options and DefaultOptions.
//
// How: client and server, with no extension: permessage-deflate is never
// offered, and an offer is declined by not answering it. No unsafe: the
// connection's state is built from Mutex, a Semaphore and std.core's Box on
// the &Heap given at creation, and the masking uses std.simd's public
// vectors. The frame codec, the connection, the opening handshake, Dial and
// Accept are files of their own.

import std.http
import std.net
import std.time

/// The peer broke RFC 6455, or a value given to Send, Ping or Close is one
/// RFC 6455 forbids. code is the close status the connection sent, or
/// would send, for it: 1002 for a malformed or unexpected frame, 1007 for
/// text that is not UTF-8, 1008 for a peer that pings faster than its
/// pongs can be sent.
export error Protocol(code: u16)

/// A frame or message over the connection's limit (the peer is sent close
/// status 1009), a handshake head over 16 KiB, or a ping payload or close
/// reason over what a control frame holds.
export error TooLarge

/// The peer closed: code is the status of its Close frame, 1005 when it
/// sent none, and 1006 when the connection ended without a Close frame.
/// The connection answered with its own Close frame when it could.
export error PeerClosed(code: u16)

/// The opening handshake failed: status is the HTTP status the server
/// answered (Dial), or the one Accept answered with (400, 403 or 426), and
/// 0 when the request was never sent or the reply was not an HTTP/1.1
/// head. A 101 reply that breaks RFC 6455's handshake rules is status
/// 101.
export error Handshake(status: int)

/// Every failure of std.websocket: its own four, the connection's
/// net.Error (with time.TimedOut for a Close past its limit),
/// http.NoSecureTransport for a wss URL with no secure connector, and
/// OutOfMemory. It is open: a match on it outside std keeps a `_` arm.
export open type Error = Protocol | TooLarge | PeerClosed | Handshake | net.Error | http.NoSecureTransport | OutOfMemory

/// What a data message holds: UTF-8 text (checked on both sides) or bytes.
export enum Kind {
    /// UTF-8 text, checked on both sides.
    Text
    /// Bytes.
    Binary
}

/// One data message, its fragments joined; data is in the arena Recv was
/// given.
export struct Message {
    /// Whether the message is text or bytes.
    export kind: Kind
    /// The message's bytes, its fragments joined, in the arena Recv was given.
    export data: []u8
}

/// What Dial sends and what a connection allows. Plain: references and
/// slices into the caller's memory, never kept by the connection, which
/// copies the limits. Start from DefaultOptions and assign the fields you
/// change: a literal of it does not compile outside std. Dial fails with
/// Handshake(0), before any I/O, for a malformed or reserved field.
export struct Options {
    /// Fields Dial adds to its request (an Origin, a cookie, a token).
    /// None may be one the handshake sets: Host, Upgrade, Connection or a
    /// Sec-WebSocket- field.
    export headers: []http.Header
    /// The subprotocols Dial offers, in preference order; empty offers
    /// none.
    export protocols: []str
    /// The connector Dial uses for a wss URL; none fails a wss URL with
    /// http.NoSecureTransport.
    export secure: ?net.Secure
    /// The longest frame payload Recv takes; a longer one is TooLarge.
    export frame_bytes: int
    /// The longest message Recv takes, fragments joined; more is TooLarge.
    export message_bytes: int
    /// How many pongs may be owed while another task holds the write lock;
    /// one ping more fails the connection with Protocol(1008).
    export pending_pongs: int
    /// The longest a Close, or a reply Recv writes, may wait for the peer
    /// before it fails with time.TimedOut, also under a NoCancel Io.
    export close: time.Duration
    sealed: u8
}

/// Frames and messages up to 16 MiB, 16 owed pongs, a 5-second close
/// limit, no subprotocol, no extra field and no secure connector. Accept
/// uses these limits.
export const DefaultOptions: Options = Options(headers: [], protocols: [], secure: none, frame_bytes: 1 << 24,
    message_bytes: 1 << 24, pending_pongs: 16, close: time.Seconds(5), sealed: 0)
