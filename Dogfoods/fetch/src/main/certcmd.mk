// Why: a user debugging https wants to see and verify a certificate chain
// without making a request.
// What: `fetch cert`: CertCommand and Show1.
// How: parses the PEM chain with std.x509, prints each certificate and
// verifies the chain for --host at --at against --cacert or the system's
// roots.

import std.time
import std.x509

// CertCommand is `fetch cert`: it prints each certificate of a PEM chain
// and verifies the chain for --host at --at (an HTTP date, the wall clock
// by default) against --cacert, or the system's roots without it.
CertCommand(sys: &System, args: []str): int {
    a := sys.arena
    var chain_path: ?str = none
    var roots_path: ?str = none
    var host := ""
    var at: ?str = none
    var i := 0
    for i < args.Len() {
        arg := args[i]
        i += 1
        if arg == "--cacert" || arg == "--host" || arg == "--at" {
            if i == args.Len() {
                return Fatal(sys, 2, Quote(a, "a value is needed after", arg))
            }
            value := args[i]
            i += 1
            match arg {
                "--cacert" { roots_path = some(value) }
                "--host" { host = value }
                _ { at = some(value) }
            }
            continue
        }
        if arg.StartsWith("-") || chain_path.IsSome() {
            return Fatal(sys, 2, Quote(a, "unexpected argument", arg))
        }
        chain_path = some(arg)
    }
    path := chain_path else {
        return Fatal(sys, 2, "fetch cert needs a PEM file")
    }
    chain := LoadCerts(a, sys.io, path) else e {
        return Fatal(sys, 26, a.Fmt("cannot read the chain {path}: {e}", (path, e)) else { "bad chain" })
    }
    for c in chain {
        Show1(sys, &c) else {
            return 1
        }
    }
    var now := time.Wall(sys.io)
    if date := at {
        now = time.ParseHttpDate(date) else {
            return Fatal(sys, 2, Quote(a, "--at needs an HTTP date, not", date))
        }
    }
    var pool := x509.Pool.New(NoCerts)
    if rp := roots_path {
        roots := LoadCerts(a, sys.io, rp) else e {
            return Fatal(sys, 77, a.Fmt("cannot use the roots in {rp}: {e}", (rp, e)) else { "bad --cacert" })
        }
        pool = x509.Pool.New(roots)
    } else {
        pool = x509.SystemRoots(a, sys.io) else e {
            return Fatal(sys, 77, a.Fmt("cannot read the system's roots: {e}", (e)) else { "no system roots" })
        }
    }
    x509.Verify(a, sys.io, chain, &pool, now, host) else e {
        return Fatal(sys, 60, a.Fmt("{path}: does not verify: {e}", (path, e)) else { "does not verify" })
    }
    sys.Print("{path}: verified for '{host}'\n", (path, host)) else {}
    return 0
}

// Show1 prints one certificate: its subject, issuer, names and validity.
Show1(sys: &System, c: &x509.Certificate): WriteError|OutOfMemory!void *c into sys {
    a := sys.arena
    subject := try c.Subject(a)
    issuer := try c.Issuer(a)
    try sys.Print("subject   {subject}\nissuer    {issuer}\n", (subject, issuer))
    names := try c.Names(a)
    for name in names.Items() {
        try sys.Print("name      {name}\n", (name))
    }
    from := c.NotBefore()
    until := c.NotAfter()
    bytes := c.Der().Len()
    try sys.Print("valid     {from} to {until}\nder       {bytes} bytes\n", (from, until, bytes))
}
