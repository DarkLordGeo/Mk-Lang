// verdict: reject E0503
// note: a store the callee drops, reached through std: the DER comes from encoding.ParsePem's
// block in sa, whose store into sa the callee drops, and x509.Parse's certificate views it; sa is freed and
// refilled: E0503 at sa.Free. If accepted, a run prints result 346 (freed memory). Mutant M-D791-keep
// accepts it
// x509.Parse(a: &Arena, der: []u8): Error!
// Certificate from a, der. The DER comes from a PEM block decoded into an
// arena (sa); the certificate is parsed into another (ca), sa is freed and
// refilled, and the certificate's data (a view of the DER) read.
import std.encoding
import std.x509

Run(sys: &System): int {
    h := sys.heap
    var sa := Arena.New(h) else {
        return -1
    }
    l := encoding.ParsePem(&sa, "-----BEGIN CERTIFICATE-----\nMIIBVjCCAQigAwIBAgICA+8wBQYDK2VwMDExEDAOBgNVBAoTB01rIHRlc3QxHTAb\nBgNVBAMTFE1rIFRlc3QgRWQyNTUxOSBSb290MCAXDTAwMDEwMTAwMDAwMFoYDzIy\nMDAwMTAxMDAwMDAwWjAxMRAwDgYDVQQKEwdNayB0ZXN0MR0wGwYDVQQDExRNayBU\nZXN0IEVkMjU1MTkgUm9vdDAqMAUGAytlcAMhAIfLhpvy8aeqWOrFL+2fMjovo1yx\nEhJY+u1+VkWeF03ro0IwQDAOBgNVHQ8BAf8EBAMCAQYwDwYDVR0TAQH/BAUwAwEB\n/zAdBgNVHQ4EFgQUE5zasbHoZgTtFEi8P8Pa74aH0WQwBQYDK2VwA0EAsHOt4A21\nWyQBHQet1Sd89tdHXqkJ9Gpbj4SNN9D3/GYv1Y/WOz7Jvy0mbh6S0FJVV6LlAEF/\nt41s0/75axH6AQ==\n-----END CERTIFICATE-----\n") else {
        sa.Free()
        return -2
    }
    bs := l.Done()
    if bs.Len() == 0 {
        sa.Free()
        return -3
    }
    der := bs[0].der
    var ca := Arena.New(h) else {
        sa.Free()
        return -4
    }
    c := x509.Parse(&ca, der) else {
        ca.Free()
        sa.Free()
        return -5
    }
    sa.Free() // <- E0503
    n := Refill(h, c.data)
    ca.Free()
    return n
}

// ---- helpers shared by the arena witnesses ----

// Clobber overwrites the stack below its caller's frame.
Clobber(v: i32): i32 {
    var buf: [256]i32 = [v; 256]
    var s: i32 = 0
    for i in 0..256 {
        buf[i] = buf[i] +% Trunc[i32](i)
        s = s +% buf[i]
    }
    return s
}

// CountX counts the bytes of b that are 'X' (88).
CountX(b: []u8): int {
    var n := 0
    for i in 0..b.Len() {
        if b[i] == 88 {
            n += 1
        }
    }
    return n
}

// Refill takes a second arena from h and fills 32 blocks of 64 bytes of it with 'X',
// so memory that a freed arena gave back to h is overwritten, and returns
// CountX(e), read while the second arena lives. A program that reads freed
// memory through e answers with the number of 'X' bytes it saw.
Refill(h: &Heap, e: []u8): int {
    var two := Arena.New(h) else {
        return -40
    }
    for _ in 0..32 {
        fill := two.Slice[u8](64) else {
            two.Free()
            return -50
        }
        for i in 0..fill.Len() {
            fill[i] = 88
        }
    }
    n := CountX(e)
    two.Free()
    return n
}

export Main(sys: &System): WriteError!void {
    r := Run(sys)
    try sys.Print("result {r}\n", (r))
}
