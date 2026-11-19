// Why: `--handshake` checks a server's TLS setup without the HTTP client in
// the way.
// What: Handshake, Checks and CheckCode.
// How: makes a TLS connection with tls.Config directly, sends a HEAD
// request, and prints the protocol ALPN chose and the status line.

import std.time
import std.x509
import src.origin
import std.net
import std.tls
import std.url

// the largest first line of a reply the handshake check reads
const LineBytes = 4096

error NotHttps
error NoReply

/// Why a handshake check failed.
type CheckError = NotHttps | NoReply | tls.Error | url.Error | OutOfMemory

// Handshake is `--handshake`: for an https URL it makes a TLS connection
// with cfg directly, without the HTTP client, sends a HEAD request for the
// URL's path, and reports the protocol ALPN chose and the reply's status
// line.
Handshake(sys: &System, a: &Arena, io: Io, cfg: &tls.Config, link: str into a, shown: str): CheckError!str from a {
    u := try url.Parse(a, link, none)
    if u.Scheme() != "https" {
        fail NotHttps
    }
    host := u.Hostname()
    port := u.Port() else { 443 }
    path := u.Path()
    request := try a.Fmt("HEAD {path} HTTP/1.1\r\nHost: {host}\r\nConnection: close\r\n\r\n", (path, host))
    stream := try net.Dial(io, try a.Fmt("{host}:{port}", (host, port)))
    plain := stream.Conn()
    c := tls.Client(sys.heap, io, plain, cfg, host) else e {
        plain.Abandon()
        fail e
    }
    c.WriteAll(io, request.AsBytes()) else e {
        c.Abandon()
        fail e
    }
    var buf: [LineBytes]u8 = [0; LineBytes]
    var got := 0
    for got < buf.Len() {
        n := c.Read(io, mut buf[got..]) else e {
            c.Abandon()
            fail e
        }
        if n == 0 {
            break
        }
        got += n
        if buf[..got].Has(&b'\n') {
            break
        }
    }
    alpn := c.Alpn() else { "none" }
    proto := a.Dup(alpn) else {
        c.Abandon()
        fail OutOfMemory
    }
    c.Close(io) else {
        c.Abandon()
    }
    head := str.From(buf[..got]) else {
        fail NoReply
    }
    line := head.Lines().Next() else {
        fail NoReply
    }
    return try a.Fmt("{shown}: TLS 1.3, ALPN {proto}, {line}\n", (shown, proto, line))
}

// Checks runs one --handshake check under --max-time, prints its line or
// its failure, and returns the run's status so far.
Checks(sys: &System, io: Io, o: &Options, cfg: &tls.Config, link: str, addrs: &origin.Addrs, code: int): int {
    a := sys.arena
    expanded := origin.Expand(a, link, addrs) else {
        return Fatal(sys, 1, "out of memory")
    }
    var result := code
    scope s := io.Scope() {
        if d := o.max_time {
            s.CancelAfter(d)
        }
        line := Handshake(sys, a, io, cfg, expanded, link) else e {
            failed := if s.Expired() { 28 } else { CheckCode(e) }
            why := match e {
                NotHttps { "--handshake needs an https URL" }
                NoReply { "no HTTP reply" }
                _ { if failed == 28 { "timed out" } else { a.Fmt("{e}", (e)) else { "failed" } } }
            }
            text := a.Fmt("{link}: {why}", (link, why)) else { link }
            _ = Fatal(sys, failed, text)
            if result == 0 {
                result = failed
            }
            return result
        }
        sys.Print("{line}", (line)) else {}
    }
    return result
}

// CheckCode is the exit status of a failed handshake check, as Describe
// gives it for a transfer.
CheckCode(e: CheckError): int {
    return match e {
        NotHttps | url.BadUrl | url.BadHost | url.IdnaUnsupported { 3 }
        net.Refused | net.Unreachable { 7 }
        x509.UnknownAuthority | x509.Expired | x509.NameMismatch | x509.BadSignature | x509.BadUsage { 60 }
        tls.Alert | tls.Protocol | tls.Unsupported { 35 }
        time.TimedOut { 28 }
        _ { 1 }
    }
}
