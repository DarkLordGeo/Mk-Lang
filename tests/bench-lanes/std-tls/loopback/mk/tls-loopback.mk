// tls-loopback, Mk, std.tls: a TLS 1.3 server and client
// in one process over loopback TCP, a task each. Mode bulk: one
// handshake, then the client sends n MiB with WriteAll in 16 KiB pieces
// (byte i of each piece is (7 i + 1) mod 256) and the server reads them
// all with 16 KiB Reads, adding each byte. Mode handshake: n connections
// one after another, each a full handshake (X25519,
// TLS_AES_128_GCM_SHA256, the server's P-256 leaf under a P-256 root,
// checked by the client), one byte each way, and close_notify both ways.
// The certificates and key are constants, valid 2000 to 2200, the same
// bytes in every column. Prints the bytes or round trips the client
// counted, the server's sum and the failures. Mode system: as
// handshake, but the client has roots none, the system's pool, which does
// not hold the bench root, so each handshake is refused at the chain check
// (on Windows by the platform's check, on Linux by the bundle's pool) and
// counted, and the server counts its failed handshake. Usage: tls-loopback
// <n> bulk|handshake|system.

import std.crypto
import std.encoding
import std.net
import std.tls
import std.x509

const LeafPem: str = "-----BEGIN CERTIFICATE-----\nMIIBfDCCASGgAwIBAgIBAjAKBggqhkjOPQQDAjAYMRYwFAYDVQQDEw1NayBiZW5j\naCByb290MCAXDTAwMDEwMTAwMDAwMFoYDzIyMDAwMTAxMDAwMDAwWjAUMRIwEAYD\nVQQDEwlsb2NhbGhvc3QwWTATBgcqhkjOPQIBBggqhkjOPQMBBwNCAARYWAiT0sIB\nPFPoIQColXkYt09CRdmx6LjiHDj75/IQjS3AYB3CWa+ymGXqR2ucu2cguSiCoiNK\nqIH+y9bVqzPco14wXDAOBgNVHQ8BAf8EBAMCB4AwEwYDVR0lBAwwCgYIKwYBBQUH\nAwEwHwYDVR0jBBgwFoAUJFRJG8q4njDrib1e2WaLjryUxnkwFAYDVR0RBA0wC4IJ\nbG9jYWxob3N0MAoGCCqGSM49BAMCA0kAMEYCIQD4NROoBKH8KfhkQ/zOMONC60gR\ncRjTYRvpycsxggV3OgIhAKjdsY7ozxVLBHMGzKcJDmUfhRo7ejfd4qjWiC3mdn+n\n-----END CERTIFICATE-----\n"
const KeyPem: str = "-----BEGIN PRIVATE KEY-----\nMIGHAgEAMBMGByqGSM49AgEGCCqGSM49AwEHBG0wawIBAQQgQ3K5SvjaOAx5Gj4e\nrlkGTawIlLQ2SZBirD7hp5nMGbChRANCAARYWAiT0sIBPFPoIQColXkYt09CRdmx\n6LjiHDj75/IQjS3AYB3CWa+ymGXqR2ucu2cguSiCoiNKqIH+y9bVqzPc\n-----END PRIVATE KEY-----\n"
const RootPem: str = "-----BEGIN CERTIFICATE-----\nMIIBYzCCAQmgAwIBAgIBATAKBggqhkjOPQQDAjAYMRYwFAYDVQQDEw1NayBiZW5j\naCByb290MCAXDTAwMDEwMTAwMDAwMFoYDzIyMDAwMTAxMDAwMDAwWjAYMRYwFAYD\nVQQDEw1NayBiZW5jaCByb290MFkwEwYHKoZIzj0CAQYIKoZIzj0DAQcDQgAEyTx+\nYlmJmAU6O4dZ0UQ3l5NkWBp3YM0DnNEBOBQ4TupVoFHlQRki7kyY6p+VX9Qrr6Hn\nz8thWyiM/gt642/2dqNCMEAwDgYDVR0PAQH/BAQDAgIEMA8GA1UdEwEB/wQFMAMB\nAf8wHQYDVR0OBBYEFCRUSRvKuJ4w64m9Xtlmi468lMZ5MAoGCCqGSM49BAMCA0gA\nMEUCIQCGfzbV5b0fQ7vvXGWBDlwaBxou4hsIMFapz+zbFYiqbQIgIH6Cu3lYtLDM\n5NakQ34Pul2rlQOLEFZvP0CmTMKU1NE=\n-----END CERTIFICATE-----\n"

const Piece: int = 16384

// Serve accepts count connections and answers each: in bulk mode it reads
// to the end and adds the bytes; in handshake mode it reads one byte, adds
// it and writes it back. sum and failed are the server's report.
Serve(h: &Heap, io: Io, l: &net.Listener, cfg: &tls.Config, bulk: bool, count: int, sum: mut u64, failed: mut int) {
    var buf: [16384]u8 = [0; 16384]
    for _ in 0..count {
        s := l.Accept(io) else {
            *failed += 1
            return
        }
        conn := s.Conn()
        c := tls.Server(h, io, conn, cfg) else {
            conn.Abandon()
            *failed += 1
            continue
        }
        if bulk {
            for {
                k := c.Read(io, mut buf[..]) else {
                    *failed += 1
                    break
                }
                if k == 0 {
                    break
                }
                for i in 0..k {
                    *sum += Cast[u64](buf[i])
                }
            }
        } else {
            k := c.Read(io, mut buf[0..1]) else {
                *failed += 1
                0
            }
            if k == 1 {
                *sum += Cast[u64](buf[0])
                c.WriteAll(io, buf[0..1]) else {
                    *failed += 1
                }
            }
            // The client's close_notify.
            _ = c.Read(io, mut buf[0..1]) else {
                0
            }
        }
        c.Close(io) else {
            c.Abandon()
        }
    }
}

export Main(sys: &System): OutOfMemory|ParseError|WriteError|x509.Error|encoding.Error|crypto.Error|tls.Error!void {
    io := sys.io
    n := Cast[int](try sys.args[1].ParseInt())
    bulk := sys.args[2] == "bulk"
    a := sys.arena
    certs := try x509.ParsePem(a, LeafPem)
    blocks := try encoding.ParsePem(a, KeyPem)
    key := try crypto.ParsePrivateKey(blocks[0].der)
    roots := try x509.ParsePem(a, RootPem)
    chain := certs.Done()
    ids: [1]tls.Identity = [tls.Identity(chain: chain[..], key: &key)]
    var scfg := tls.DefaultConfig
    scfg.identities = ids[..]
    rs := roots.Done()
    pool := x509.Pool.New(rs[..])
    var ccfg := tls.DefaultConfig
    system := sys.args[2] == "system"
    if !system {
        ccfg.roots = some(&pool)
    }
    l := try net.Listen(io, "127.0.0.1:0")
    defer l.Close()
    addr := try a.Fmt("127.0.0.1:{p}", (p: l.Port()))
    count := if bulk { 1 } else { n }
    var sum: u64 = 0
    var failed := 0
    var counted: u64 = 0
    var data: [16384]u8 = [0; 16384]
    for i in 0..Piece {
        data[i] = Trunc[u8](7 * i + 1)
    }
    scope s := io.Scope() {
        s.go Serve(sys.heap, io, &l, &scfg, bulk, count, mut sum, mut failed)
        for i in 0..count {
            stream := try net.Dial(io, addr)
            conn := stream.Conn()
            c := tls.Client(sys.heap, io, conn, &ccfg, "localhost") else e {
                conn.Abandon()
                if system {
                    // The refusal mode's handshake: the system's pool
                    // does not hold the bench root.
                    counted += 1
                    continue
                }
                fail e
            }
            onerr c.Abandon()
            if bulk {
                for _ in 0..n * 64 {
                    try c.WriteAll(io, data[..])
                    counted += Cast[u64](Piece)
                }
            } else {
                one: [1]u8 = [Trunc[u8](i)]
                try c.WriteAll(io, one[..])
                var back: [1]u8 = [0]
                if try c.Read(io, mut back[..]) == 1 {
                    counted += 1
                }
            }
            try c.Close(io)
        }
    }
    try sys.Print("{counted} {sum} {failed}\n", (counted, sum, failed))
}
