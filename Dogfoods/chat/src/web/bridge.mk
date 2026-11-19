// Why: An HTTP handler cannot reach the hub's channels, so a web client talks
// to its session through a bridge.
//
// What: Packet, Bridge, Arrival, and Link, a net.Transport over a bridge.
//
// How: A bridge is a pair of bounded channels, one each way. The session sees
// it as an ordinary connection through Link.

import std.atomic
import std.net
import src.proto

/// Lines a bridge queues each way before the sender waits.
const BridgeDepth: int = 8

/// One line crossing a bridge, or the end of one direction. Plain, so a web
/// handler can hand it over without lending the server anything.
export struct Packet {
    export done: bool
    export frame: proto.Frame
}

/// A line for the other side.
export Packet.Line(f: proto.Frame): Packet {
    return Packet(done: false, frame: f)
}

/// The end of one direction.
export Packet.End(): Packet {
    return Packet(done: true, frame: proto.Frame.Empty(proto.Kind.Line))
}

/// Carries one web client's lines between its handler, which owns the
/// WebSocket or the event stream, and its session, which runs under the
/// server's scope like any TCP session. A handler reaches the server only
/// through bridges and plain values.
// A workaround: a handler may store nothing into the value it is served
// with, so it cannot reach the hub's channels itself.
export struct Bridge {
    /// Lines from the web client to its session.
    export up: Chan[Packet]
    /// Lines from the session to the web client.
    export down: Chan[Packet]
    /// How many of the two sides still hold the bridge; 0 when it is free.
    users: atomic.Atomic[i64]
}

/// A free bridge. Fails with `OutOfMemory` when a channel cannot be made.
/// O(1); allocates two channels of `BridgeDepth` packets.
export Bridge.New(a: &Alloc): OutOfMemory!Bridge {
    up := try Chan[Packet].New(a, BridgeDepth)
    onerr up.Free(a)
    down := try Chan[Packet].New(a, BridgeDepth)
    return Bridge(up: up, down: down, users: atomic.Atomic[i64].New(0))
}

/// Frees both channels; packets still queued are plain and simply dropped.
export Bridge.Free(self, a: &Alloc) {
    Bridge(up, down, users: _) := self
    up.Free(a)
    down.Free(a)
}

/// Takes the bridge for a new client when it is free, for both sides at
/// once, and empties what an earlier client left queued. Returns false when
/// it is in use. Lock-free: one compare-exchange.
export Bridge.Claim(&self, io: Io): bool {
    if !self.users.CompareExchange(0, 2, atomic.AcqRel, atomic.Acquire).ok {
        return false
    }
    for self.up.Ready() {
        _ = self.up.Recv(io) else {
            break
        }
    }
    for self.down.Ready() {
        _ = self.down.Recv(io) else {
            break
        }
    }
    return true
}

/// One side lets go; the bridge is free once both have.
export Bridge.Let(&self) {
    _ = self.users.FetchSub(1, atomic.AcqRel)
}

/// A new web client: the bridge it holds and its address, which the
/// session's log reports.
export struct Arrival {
    export bridge: int
    export peer: ?net.Addr
}

/// The session's end of a bridge as a `net.Transport`, so a web client's
/// session is the TCP session over `net.NewConn`. `Read` hands over one line
/// at a time, with its newline, keeping what did not fit for the next call;
/// `WriteAll` sends each line as one packet.
struct Link {
    bridge: &Bridge
    peer: ?net.Addr
    /// The part of the last line `Read` could not hand over yet.
    rest: Mutex[Rest]
    /// Set by the first `Shutdown` or `Close` (`End`), after which the Link
    /// no longer touches the bridge.
    closed: atomic.AtomicBool
}

/// What is left of a line `Read` cut.
struct Rest {
    frame: proto.Frame
    at: int
}

/// The session's end of `b`, for a client at `peer`.
Link.Of(b: &Bridge, peer: ?net.Addr): Link from b {
    return Link(bridge: b, peer: peer, rest: Mutex.New(Rest(frame: proto.Frame.Empty(proto.Kind.Line), at: 0)), closed: atomic.AtomicBool.New(false))
}

impl net.Transport for Link {
    Read(&self, io: Io, buf: mut []u8): net.Error!int {
        if self.closed.Load() {
            // the web side stops pumping its client's lines once it has
            // read the session's end, and the bridge may be another's now
            return 0
        }
        var g := try self.rest.Lock(io)
        defer g.Unlock()
        if g.Ref().at == g.Ref().frame.Bytes().Len() {
            p := self.bridge.up.Recv(io) else e {
                match e {
                    Canceled {
                        fail Canceled
                    }
                    Closed {
                        return 0
                    }
                }
            }
            if p.done {
                return 0
            }
            g.Store(Rest(frame: p.frame, at: 0))
        }
        r := g.Mut()
        left := r.frame.Bytes()[r.at..]
        n := if left.Len() < buf.Len() { left.Len() } else { buf.Len() }
        (mut buf[..n]).CopyFrom(left[..n])
        r.at += n
        return n
    }

    WriteAll(&self, io: Io, data: []u8): net.Error!void {
        var start := 0
        for i in 0..data.Len() {
            if data[i] == b'\n' {
                try self.Send(io, data[start..i])
                start = i + 1
            }
        }
        if start < data.Len() {
            try self.Send(io, data[start..])
        }
    }

    Shutdown(&self, io: Io, how: net.Shutdown): net.Error!void {
        // the end of the session's lines ends the web client, so either
        // half ends both
        try self.End(io)
    }

    PeerAddr(&self): ?net.Addr {
        return self.peer
    }

    Close(&self, io: Io): net.Error!void {
        try self.End(io)
    }

    Abandon(self) {
        if !self.closed.Load() {
            self.bridge.Let()
        }
    }
}

/// Sends the end of the session's lines, once, by the first `Shutdown` or
/// `Close`. It lets go of the bridge before it sends the end, so the bridge
/// is free once the web side has read it; the Link touches it no more.
Link.End(&self, io: Io): net.Error!void {
    if self.closed.Swap(true) {
        return
    }
    self.bridge.Let()
    // the web side ends its client when it reads the end
    try self.bridge.down.Send(io, Packet.End())
}

/// Sends one line, without its newline, as a packet. Fails with `Closed`
/// after the end was sent.
Link.Send(&self, io: Io, line: []u8): net.Error!void {
    if self.closed.Load() {
        fail Closed
    }
    text := str.From(line) else {
        // the session writes only text, so this is a defect of the server
        Panic("a session wrote a line that is not UTF-8")
    }
    try self.bridge.down.Send(io, Packet.Line(proto.Frame.Of(proto.Kind.Line, text)))
}
