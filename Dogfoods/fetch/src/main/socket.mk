// Why: a ws:// or wss:// URL is a WebSocket session, not a transfer.
// What: Socket, Show2, Refusal, SocketCode and Secure2.
// How: dials with websocket.Dial, sends each --ws-send, --ws-binary and
// --ws-ping in order, prints the replies and closes with status 1000; each
// failure maps to a curl-style exit status.

import std.net
import std.tls
import std.url
import std.websocket
import src.origin

// Socket is the WebSocket mode, for a ws or wss URL: it connects with
// websocket.Dial, then for each --ws-send, --ws-binary and --ws-ping in
// order sends the message (and prints the reply) or the ping, and closes
// with status 1000. It returns the exit status.
Socket(sys: &System, io: Io, o: &Options, cfg: &tls.Config, link: str, addrs: &origin.Addrs): int {
    a := sys.arena
    expanded := origin.Expand(a, link, addrs) else {
        return Fatal(sys, 1, "out of memory")
    }
    u := url.Parse(a, expanded, none) else e {
        return Fatal(sys, 3, a.Fmt("{link}: bad URL: {e}", (link, e)) else { "bad URL" })
    }
    var opts := websocket.DefaultOptions
    opts.secure = some(tls.Connector(cfg))
    opts.protocols = o.ws_protocols.Items()
    if o.ws_max > 0 {
        opts.frame_bytes = o.ws_max
        opts.message_bytes = o.ws_max
    }
    var code := 0
    scope s := io.Scope() {
        if d := o.max_time {
            s.CancelAfter(d)
        }
        conn := websocket.Dial(sys.heap, a, io, &u, &opts) else e {
            code = Refusal(sys, link, if s.Expired() { 28 } else { SocketCode(e) }, e)
            return code
        }
        if chosen := conn.Protocol() {
            sys.Print("subprotocol {chosen}\n", (chosen)) else {}
        }
        for i in 0..o.ws_kinds.Len() {
            step := o.ws_kinds.Items()[i]
            what := o.ws_texts.Items()[i]
            if step == b'p' {
                conn.Ping(io, what.AsBytes()) else e {
                    conn.Abandon()
                    return Refusal(sys, link, SocketCode(e), e)
                }
                sys.Print("> ping {what}\n", (what)) else {}
                continue
            }
            kind := if step == b'b' { websocket.Kind.Binary } else { websocket.Kind.Text }
            conn.Send(io, kind, what.AsBytes()) else e {
                conn.Abandon()
                return Refusal(sys, link, SocketCode(e), e)
            }
            sys.Print("> {what}\n", (what)) else {}
            m := conn.Recv(a, io) else e {
                match e {
                    websocket.PeerClosed(code: status) {
                        sys.Print("< closed by the server with status {status}\n", (status)) else {}
                        conn.Close(io, 0, "") else {
                            conn.Abandon()
                        }
                        return 0
                    }
                    _ {
                        conn.Abandon()
                        return Refusal(sys, link, if s.Expired() { 28 } else { SocketCode(e) }, e)
                    }
                }
            }
            Show2(sys, &m)
        }
        conn.Close(io, 1000, "done") else e {
            conn.Abandon()
            code = Refusal(sys, link, SocketCode(e), e)
        }
    }
    return code
}

// Show2 prints a received message: text as it is, binary by its size.
Show2(sys: &System, m: &websocket.Message) {
    match m.kind {
        .Text {
            text := str.From(m.data) else { "(not UTF-8)" }
            n := text.Len()
            if n > 80 {
                sys.Print("< text of {n} bytes\n", (n)) else {}
                return
            }
            sys.Print("< {text}\n", (text)) else {}
        }
        .Binary {
            n := m.data.Len()
            sys.Print("< binary of {n} bytes\n", (n)) else {}
        }
    }
}

// Refusal reports a failed WebSocket step and returns its status.
Refusal(sys: &System, link: str, code: int, e: websocket.Error): int {
    a := sys.arena
    text := if code == 28 { a.Fmt("{link}: timed out", (link)) else { link } } else { a.Fmt("{link}: {e}", (link, e)) else { link } }
    return Fatal(sys, code, text)
}

// SocketCode is the exit status of a WebSocket failure: as a transfer's
// for the connection, 22 for a refused handshake, 56 for a protocol
// fault, 63 for a message past --ws-max.
SocketCode(e: websocket.Error): int {
    return match e {
        websocket.Handshake { 22 }
        websocket.Protocol { 56 }
        websocket.TooLarge { 63 }
        websocket.PeerClosed { 52 }
        net.InvalidAddress { 3 }
        net.Refused | net.Unreachable { 7 }
        net.SecureFailed(why) { Secure2(why) }
        Closed | net.Reset { 56 }
        _ { 1 }
    }
}

// Secure2 is the exit status of a failed TLS connection.
Secure2(why: net.SecureFailure): int {
    return match why {
        .Handshake | .Protocol { 35 }
        _ { 60 }
    }
}
