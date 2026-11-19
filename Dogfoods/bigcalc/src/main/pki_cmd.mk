// Why: the certificate, key and signature commands share key loading and
// hash and format options.
//
// What: Cert (show, verify, der), KeyCmd (show, public), Sign and Verify.
//
// How: certificates go through std.x509, keys through pki.Load, and
// signatures through pki.Check and pki.Sign.

import std.crypto
import std.encoding
import std.fs
import std.hash
import std.time
import std.x509
import src.pki
import src.value

const CertValued: []str = ["--roots", "--host", "--at"]
const SigValued: []str = ["--key", "--key-scalar", "--hash", "--format", "--out", "--sig-encoding"]
const SigSwitches: []str = ["--pss"]
const NoOptions: []str = []

/// Cert runs `cert show`, `cert verify` or `cert der`.
Cert(sys: &System, argv: []str): int *argv into sys {
    if argv.Len() == 0 {
        sys.PrintErr("bigcalc: cert needs show, verify or der\n") else {}
        return ExitUsage
    }
    args := ParseArgs(sys.arena, argv[1..], CertValued, NoOptions) else e {
        return Usage(sys, argv[1..], e)
    }
    if args.positional.Len() == 0 {
        sys.PrintErr("bigcalc: cert {sub} needs a file\n", (sub: argv[0])) else {}
        return ExitUsage
    }
    return match argv[0] {
        "show" { CertShow(sys, &args) }
        "verify" { CertVerify(sys, &args) }
        "der" { CertDer(sys, &args) }
        _ {
            sys.PrintErr("bigcalc: cert needs show, verify or der\n") else {}
            ExitUsage
        }
    }
}

// Reads every certificate of path into list: each CERTIFICATE block of a
// PEM file, or one DER certificate.
Certificates(a: &Arena into list, io: Io, path: str, list: mut List[x509.Certificate]): x509.Error|fs.Error!void {
    data := try fs.ReadBytes(a, io, path)
    if text := value.Pem(data) {
        for cert in try x509.ParsePem(a, text) {
            try list.Push(cert)
        }
        return
    }
    try list.Push(try x509.Parse(a, data))
}

// cert show: the fields of every certificate of every file.
CertShow(sys: &System, args: &Args): int {
    var out := sys.Stdout()
    var code := 0
    for path in args.positional {
        var certs := List[x509.Certificate].New(sys.arena)
        Certificates(sys.arena, sys.io, path, mut certs) else e {
            sys.PrintErr("bigcalc: {path}: {e}\n", (path, e)) else {}
            code = ExitError
            continue
        }
        for i, cert in certs {
            ShowOne(sys.arena, mut out, &cert, path, i + 1) else e {
                sys.PrintErr("bigcalc: {path}: {e}\n", (path, e)) else {}
                code = ExitError
            }
        }
    }
    return code
}

// One certificate's fields.
ShowOne(a: &Arena, out: mut Writer, cert: &x509.Certificate, path: str, n: int): pki.KeyError|WriteError!void *cert into a {
    der := cert.Der()
    found := try pki.ReadCert(a, der)
    serial := try pki.Signed(a, found.serial)
    subject := try cert.Subject(a)
    issuer := try cert.Issuer(a)
    from := cert.NotBefore()
    until := cert.NotAfter()
    try Print(out, "{path} #{n}\n", (path, n))
    try Print(out, "  subject:    {subject}\n  issuer:     {issuer}\n", (subject, issuer))
    hex := try serial.Text(a, 16)
    try Print(out, "  serial:     {hex}\n  not before: {from}\n  not after:  {until}\n", (hex, from, until))
    var names := Text.New(a)
    for i, name in try cert.Names(a) {
        if i > 0 {
            try names.Push(", ")
        }
        try names.Push(name)
    }
    all := if names.Str().Len() == 0 { "(none)" } else { names.Str() }
    try Print(out, "  names:      {all}\n  key:        ", (all))
    key := try cert.PublicKey()
    try KeyLine(a, out, &key, found.spki)
    ca := if b := found.ca { b } else { false }
    if found.ca.IsNone() {
        try Print(out, "  ca:         (no basic constraints)\n")
    } else if !ca {
        try Print(out, "  ca:         no\n")
    } else if len := found.path_len {
        try Print(out, "  ca:         yes, path length {len}\n", (len))
    } else {
        try Print(out, "  ca:         yes\n")
    }
    sha1 := try encoding.HexEncode(a, hash.Sha1(der)[..])
    sha256 := try encoding.HexEncode(a, hash.Sha256(der)[..])
    try Print(out, "  sha-1:      {sha1}\n  sha-256:    {sha256}\n", (sha1, sha256))
}

// A public key's kind on one line; der is its SubjectPublicKeyInfo, which
// holds an RSA key's size and exponent.
KeyLine(a: &Arena, out: mut Writer, key: &crypto.PublicKey, der: []u8): pki.KeyError|WriteError!void {
    match key {
        .Ec(curve) {
            name := pki.CurveName(*curve)
            try Print(out, "{name}\n", (name))
        }
        .Rsa {
            _ = try pki.DescribeRsa(a, out, der)
        }
        .Ed25519 {
            try Print(out, "Ed25519\n")
        }
    }
}

// cert verify: a chain, leaf first, against the roots at a time.
CertVerify(sys: &System, args: &Args): int {
    var roots := List[x509.Certificate].New(sys.arena)
    var i := 0
    for path := args.Nth("--roots", i) {
        Certificates(sys.arena, sys.io, path, mut roots) else e {
            sys.PrintErr("bigcalc: {path}: {e}\n", (path, e)) else {}
            return ExitError
        }
        i += 1
    }
    if roots.Len() == 0 {
        sys.PrintErr("bigcalc: cert verify needs --roots with at least one certificate\n") else {}
        return ExitUsage
    }
    path := args.positional[0]
    var chain := List[x509.Certificate].New(sys.arena)
    Certificates(sys.arena, sys.io, path, mut chain) else e {
        sys.PrintErr("bigcalc: {path}: {e}\n", (path, e)) else {}
        return ExitError
    }
    if chain.Len() == 0 {
        sys.PrintErr("bigcalc: {path}: no certificate\n", (path)) else {}
        return ExitError
    }
    now := if at := args.Value("--at") {
        time.ParseRfc3339(at) else {
            sys.PrintErr("bigcalc: --at takes an RFC 3339 time such as 2026-01-02T03:04:05Z\n") else {}
            return ExitUsage
        }
    } else {
        time.Wall(sys.io)
    }
    host := args.Value("--host") else { "" }
    pool := x509.Pool.New(roots[..])
    x509.Verify(sys.arena, sys.io, chain[..], &pool, now, host) else e {
        sys.PrintErr("bigcalc: {path}: {e}\n", (path, e)) else {}
        return match e {
            x509.UnknownAuthority | x509.Expired | x509.NameMismatch | x509.BadSignature | x509.BadUsage | x509.TooComplex { ExitInvalid }
            x509.Malformed | x509.Unsupported { ExitError }
            Canceled { ExitTime }
            _ { ExitMemory }
        }
    }
    subject := chain[0].Subject(sys.arena) else {
        return Stop(sys, "out of memory", ExitMemory)
    }
    sys.Print("ok: {subject}\n", (subject)) else {}
    return 0
}

// cert der: the DER tree of every block of a file.
CertDer(sys: &System, args: &Args): int {
    var out := sys.Stdout()
    path := args.positional[0]
    blocks := value.Blocks(sys.arena, sys.io, path) else e {
        sys.PrintErr("bigcalc: {path}: {e}\n", (path, e)) else {}
        return ExitError
    }
    for b in blocks {
        depth := if b.label == "" { 0 } else { 1 }
        if b.label != "" {
            n := b.der.Len()
            Print(mut out, "{b.label} ({n} bytes)\n", (b, n)) else {}
        }
        pki.Dump(sys.arena, mut out, b.der, depth) else e {
            match e {
                encoding.TooLarge {
                    sys.PrintErr("bigcalc: {path}: DER nested deeper than 32 levels\n", (path)) else {}
                }
                _ {
                    sys.PrintErr("bigcalc: {path}: {e}\n", (path, e)) else {}
                }
            }
            return ExitError
        }
    }
    return 0
}

/// KeyCmd runs `key show` or `key public`.
KeyCmd(sys: &System, argv: []str): int {
    if argv.Len() != 2 || (argv[0] != "show" && argv[0] != "public") {
        sys.PrintErr("bigcalc: key needs show or public, and a file\n") else {}
        return ExitUsage
    }
    path := argv[1]
    var out := sys.Stdout()
    var k := pki.Load(sys.arena, sys.io, path) else e {
        match e {
            crypto.BadKey {
                // describe a key std.crypto refuses, such as RSA outside its bounds
                RefusedRsa(sys, path)
            }
            crypto.Encrypted {
                sys.PrintErr("bigcalc: {path}: the key is encrypted; decrypt it first, as with openssl pkcs8 -in {path}\n", (path)) else {}
            }
            crypto.BadEncoding {
                sys.PrintErr("bigcalc: {path}: not a key bigcalc reads; it signs with Ed25519, P-256 and P-384 keys in PKCS#8 or SEC 1\n", (path)) else {}
            }
            _ {}
        }
        sys.PrintErr("bigcalc: {path}: {e}\n", (path, e)) else {}
        return ExitError
    }
    defer pki.Wipe(mut k)
    if argv[0] == "show" {
        pki.Describe(sys.arena, mut out, &k) else e {
            sys.PrintErr("bigcalc: {path}: {e}\n", (path, e)) else {}
            return ExitError
        }
        return 0
    }
    pki.PublicPem(sys.arena, mut out, &k) else e {
        sys.PrintErr("bigcalc: {path}: {e}\n", (path, e)) else {}
        return ExitError
    }
    return 0
}

// Prints what an RSA key std.crypto refused holds, when it is one.
RefusedRsa(sys: &System, path: str) {
    blocks := value.Blocks(sys.arena, sys.io, path) else {
        return
    }
    if blocks.Len() == 0 {
        return
    }
    var out := sys.Stdout()
    Print(mut out, "public key: ") else {}
    _ = pki.DescribeRsa(sys.arena, mut out, blocks[0].der) else { false }
}

// The hash, format and encoding options of sign and verify.
HashOption(args: &Args, fallback: crypto.HashKind): ?crypto.HashKind {
    name := args.Value("--hash") else {
        return some(fallback)
    }
    return match name {
        "sha256" { some(crypto.HashKind.Sha256) }
        "sha384" { some(crypto.HashKind.Sha384) }
        "sha512" { some(crypto.HashKind.Sha512) }
        _ { none }
    }
}

FormatOption(args: &Args): ?crypto.SigFormat {
    name := args.Value("--format") else { "der" }
    return match name {
        "der" { some(crypto.SigFormat.Der) }
        "fixed" { some(crypto.SigFormat.Fixed) }
        _ { none }
    }
}

// The key to sign with: --key FILE, or --key-scalar SPEC for a raw P-256
// scalar.
SigningKey(sys: &System, args: &Args): pki.KeyError!pki.Key from sys.arena {
    if spec := args.Value("--key-scalar") {
        scalar := try value.Spec(sys.arena, sys.io, spec)
        secret := try crypto.EcSecret.FromBytes(crypto.Curve.P256, scalar)
        return pki.Key.Private(key: crypto.PrivateKey.Ec(secret: secret))
    }
    path := args.Value("--key") else {
        fail pki.NoKey
    }
    return try pki.Load(sys.arena, sys.io, path)
}

/// Sign prints the signature of a message file under a private key.
Sign(sys: &System, argv: []str): int *argv into sys {
    args := ParseArgs(sys.arena, argv, SigValued, SigSwitches) else e {
        return Usage(sys, argv, e)
    }
    if args.positional.Len() != 1 {
        sys.PrintErr("bigcalc: sign takes one message file\n") else {}
        return ExitUsage
    }
    h := HashOption(&args, crypto.HashKind.Sha256) else {
        sys.PrintErr("bigcalc: --hash takes sha256, sha384 or sha512\n") else {}
        return ExitUsage
    }
    format := FormatOption(&args) else {
        sys.PrintErr("bigcalc: --format takes der or fixed\n") else {}
        return ExitUsage
    }
    var k := SigningKey(sys, &args) else e {
        sys.PrintErr("bigcalc: signing key: {e}\n", (e)) else {}
        return ExitError
    }
    defer pki.Wipe(mut k)
    path := args.positional[0]
    var stdin := sys.Stdin()
    msg := value.Input(sys.arena, sys.io, mut stdin, path) else e {
        sys.PrintErr("bigcalc: {path}: {e}\n", (path, e)) else {}
        return ExitError
    }
    var sig: [72]u8 = [0; 72]
    n := pki.Sign(sys.arena, &k, h, format, msg, mut sig[..]) else e {
        sys.PrintErr("bigcalc: sign: {e}\n", (e)) else {}
        return ExitError
    }
    text := value.Text(sys.arena, sig[..n], args.Value("--out") else { "hex" }) else {
        return Stop(sys, "out of memory", ExitMemory)
    }
    sys.Print("{text}\n", (text)) else {}
    return 0
}

/// Verify checks a signature file over a message file.
Verify(sys: &System, argv: []str): int *argv into sys {
    args := ParseArgs(sys.arena, argv, SigValued, SigSwitches) else e {
        return Usage(sys, argv, e)
    }
    if args.positional.Len() != 2 {
        sys.PrintErr("bigcalc: verify takes a message file and a signature file\n") else {}
        return ExitUsage
    }
    key_path := args.Value("--key") else {
        sys.PrintErr("bigcalc: verify needs --key\n") else {}
        return ExitUsage
    }
    k := pki.Load(sys.arena, sys.io, key_path) else e {
        sys.PrintErr("bigcalc: {key_path}: {e}\n", (key_path, e)) else {}
        return ExitError
    }
    h := HashOption(&args, pki.DefaultHash(&k)) else {
        sys.PrintErr("bigcalc: --hash takes sha256, sha384 or sha512\n") else {}
        return ExitUsage
    }
    format := FormatOption(&args) else {
        sys.PrintErr("bigcalc: --format takes der or fixed\n") else {}
        return ExitUsage
    }
    var stdin := sys.Stdin()
    msg_path := args.positional[0]
    msg := value.Input(sys.arena, sys.io, mut stdin, msg_path) else e {
        sys.PrintErr("bigcalc: {msg_path}: {e}\n", (msg_path, e)) else {}
        return ExitError
    }
    sig_path := args.positional[1]
    sig := SignatureFile(sys, sig_path, args.Value("--sig-encoding") else { "raw" }) else e {
        sys.PrintErr("bigcalc: {sig_path}: {e}\n", (sig_path, e)) else {}
        return ExitError
    }
    scheme := pki.Scheme(h: h, pss: args.Has("--pss"), format: format)
    valid := pki.Check(sys.arena, &k, scheme, msg, sig) else {
        return Stop(sys, "out of memory", ExitMemory)
    }
    if !valid {
        sys.PrintErr("bigcalc: signature does not verify\n") else {}
        return ExitInvalid
    }
    sys.Print("signature ok\n") else {}
    return 0
}

// A signature file: raw bytes, or hex or base64 text with whitespace around.
SignatureFile(sys: &System, path: str, encoding_name: str): value.Error![]u8 from sys.arena {
    raw := try value.Spec(sys.arena, sys.io, try sys.arena.Fmt("file:{path}", (path)))
    if encoding_name == "raw" {
        return raw
    }
    text := (try str.From(raw)).Trim()
    if encoding_name == "base64" {
        return try encoding.Base64Decode(sys.arena, text, encoding.Base64)
    }
    return try encoding.HexDecode(sys.arena, text)
}
