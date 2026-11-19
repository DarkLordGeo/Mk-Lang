// verdict: reject E0503
// note: the callee half in std: x509.ParsePem's Certificate.data views DER decoded into the arena: E0503 at ca.Free.
// If accepted, a run prints result 346 (freed memory). Mutant
// M-D791-keep accepts it
// x509.ParsePem(a: &Arena, pem: str): Error!List[Certificate]
// from a decodes each certificate's DER into a, and Certificate.data is a
// view of it. One certificate's data is read out of l.Done() before the
// arena is freed (unlike a program that keeps the Pool, and so the list's
// loan, live past the free); the arena is freed, refilled with 'X', and the
// DER read.
import std.x509

Run(sys: &System): int {
    h := sys.heap
    var ca := Arena.New(h) else {
        return -1
    }
    l := x509.ParsePem(&ca, "-----BEGIN CERTIFICATE-----\nMIIBVjCCAQigAwIBAgICA+8wBQYDK2VwMDExEDAOBgNVBAoTB01rIHRlc3QxHTAb\nBgNVBAMTFE1rIFRlc3QgRWQyNTUxOSBSb290MCAXDTAwMDEwMTAwMDAwMFoYDzIy\nMDAwMTAxMDAwMDAwWjAxMRAwDgYDVQQKEwdNayB0ZXN0MR0wGwYDVQQDExRNayBU\nZXN0IEVkMjU1MTkgUm9vdDAqMAUGAytlcAMhAIfLhpvy8aeqWOrFL+2fMjovo1yx\nEhJY+u1+VkWeF03ro0IwQDAOBgNVHQ8BAf8EBAMCAQYwDwYDVR0TAQH/BAUwAwEB\n/zAdBgNVHQ4EFgQUE5zasbHoZgTtFEi8P8Pa74aH0WQwBQYDK2VwA0EAsHOt4A21\nWyQBHQet1Sd89tdHXqkJ9Gpbj4SNN9D3/GYv1Y/WOz7Jvy0mbh6S0FJVV6LlAEF/\nt41s0/75axH6AQ==\n-----END CERTIFICATE-----\n") else {
        ca.Free()
        return -2
    }
    certs := l.Done()
    if certs.Len() == 0 {
        ca.Free()
        return -3
    }
    d := certs[0].data
    ca.Free() // <- E0503
    return Refill(h, d)
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
