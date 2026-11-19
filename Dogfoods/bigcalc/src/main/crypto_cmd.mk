// Why: the encryption, tag, digest and encoding commands share key specs and
// output encodings.
//
// What: Enc (seal and open, with ECDH), Mac, Hash, Encode and Decode.
//
// How: keys come from specs or files; `seal --ecdh-peer` makes a one-use
// P-256 key and writes its public half first, as ECIES does.

import std.crypto
import std.encoding
import std.fs
import src.digest
import src.pki
import src.seal
import src.value

const EncValued: []str = ["--key-from", "--prk", "--ecdh-key", "--ecdh-peer", "--salt", "--info", "--nonce", "--aad", "--bits"]
const EncSwitches: []str = ["--armor"]
const MacValued: []str = ["--key", "--hash", "--expect"]
const HashValued: []str = ["--alg"]
const HashSwitches: []str = ["--base64"]

/// Enc runs `enc seal` or `enc open`.
Enc(sys: &System, argv: []str): int *argv into sys {
    if argv.Len() == 0 || (argv[0] != "seal" && argv[0] != "open") {
        sys.PrintErr("bigcalc: enc needs seal or open\n") else {}
        return ExitUsage
    }
    args := ParseArgs(sys.arena, argv[1..], EncValued, EncSwitches) else e {
        return Usage(sys, argv[1..], e)
    }
    if args.positional.Len() != 2 {
        sys.PrintErr("bigcalc: enc {mode} takes an input and an output file\n", (mode: argv[0])) else {}
        return ExitUsage
    }
    bits := args.Int("--bits", 256, 128, 256) else { 0 }
    if bits != 128 && bits != 256 {
        sys.PrintErr("bigcalc: --bits takes 128 or 256\n") else {}
        return ExitUsage
    }
    code := if argv[0] == "seal" { EncSeal(sys, &args, bits) } else { EncOpen(sys, &args, bits) }
    return code
}

/// The length of the ephemeral P-256 public key a sealed file starts with
/// when it was sealed with --ecdh-peer alone: an uncompressed point.
const EphemeralLen: int = 65

// Whether the command seals to a peer with a key made for this file alone
// (--ecdh-peer without --ecdh-key), or opens such a file (--ecdh-key alone).
Ephemeral(args: &Args): bool {
    return args.Has("--ecdh-key") != args.Has("--ecdh-peer")
}

// Reports a key that cannot be made and returns the exit code.
KeyFailed(sys: &System, e: pki.KeyError | seal.Error | Canceled): int {
    sys.PrintErr("bigcalc: enc key: {e}\n", (e)) else {}
    return match e {
        Canceled { ExitTime }
        _ { ExitError }
    }
}

// The key object from --key-from, --prk, or a P-256 ECDH secret: of
// --ecdh-key and --ecdh-peer; of a new key and --ecdh-peer, whose public
// half is written to point; or of --ecdh-key and the public half a sealed
// file starts with, sent.
EncCipher(sys: &System, args: &Args, bits: int, point: mut []u8, sent: []u8): pki.KeyError|seal.Error|Canceled!crypto.AesGcm {
    info := (args.Value("--info") else { "" }).AsBytes()
    salt := if spec := args.Value("--salt") { try value.Spec(sys.arena, sys.io, spec) } else { "".AsBytes() }
    if spec := args.Value("--prk") {
        prk := try value.Spec(sys.arena, sys.io, spec)
        return try seal.Cipher(seal.Source.Prk(prk: prk), info, bits)
    }
    if spec := args.Value("--key-from") {
        ikm := try value.Spec(sys.arena, sys.io, spec)
        return try seal.Cipher(seal.Source.Secret(ikm: ikm, salt: salt), info, bits)
    }
    var shared: [32]u8 = [0; 32]
    defer crypto.Wipe(mut shared[..])
    if mine := args.Value("--ecdh-key") {
        if peer := args.Value("--ecdh-peer") {
            try EcdhSecret(sys, mine, peer, mut shared[..])
        } else {
            try EcdhSent(sys, mine, sent, mut shared[..])
        }
    } else {
        peer := args.Value("--ecdh-peer") else {
            fail pki.NoKey
        }
        try EcdhEphemeral(sys, peer, point, mut shared[..])
    }
    return try seal.Cipher(seal.Source.Secret(ikm: shared[..], salt: salt), info, bits)
}

// The P-256 ECDH shared secret of a private key file and a peer's public key
// file (a key or a certificate).
EcdhSecret(sys: &System, mine: str, peer: str, out: mut []u8): pki.KeyError!void {
    var k := try pki.Load(sys.arena, sys.io, mine)
    defer pki.Wipe(mut k)
    p := try pki.Load(sys.arena, sys.io, peer)
    match &k {
        .Private(key) {
            match key {
                .Ec(secret) {
                    match &p {
                        .Public(key: public) {
                            match public {
                                .Ec(curve, point) {
                                    try crypto.EcdhShared(*curve, secret, *point, out)
                                    return
                                }
                                _ {}
                            }
                        }
                        _ {}
                    }
                }
                _ {}
            }
        }
        _ {}
    }
    fail crypto.BadKey
}

// The ECDH secret of a P-256 key made for this one file and the peer's
// public key file; the new key's public half goes to point, and the key
// itself is wiped on return, so only the peer can open the file.
EcdhEphemeral(sys: &System, peer: str, point: mut []u8, out: mut []u8): pki.KeyError|Canceled!void {
    p := try pki.Load(sys.arena, sys.io, peer)
    match &p {
        .Public(key: public) {
            match public {
                .Ec(curve, point: theirs) {
                    if *curve != crypto.Curve.P256 {
                        fail crypto.BadKey
                    }
                    var mine := try crypto.EcSecret.Generate(sys.io, *curve)
                    defer mine.Wipe()
                    if try mine.Public(point) != EphemeralLen {
                        fail crypto.BadLength
                    }
                    try crypto.EcdhShared(*curve, &mine, *theirs, out)
                    return
                }
                _ {}
            }
        }
        _ {}
    }
    fail crypto.BadKey
}

// The ECDH secret of a private key file and the public key a sealed file
// starts with.
EcdhSent(sys: &System, mine: str, sent: []u8, out: mut []u8): pki.KeyError!void {
    var k := try pki.Load(sys.arena, sys.io, mine)
    defer pki.Wipe(mut k)
    match &k {
        .Private(key) {
            match key {
                .Ec(secret) {
                    try crypto.EcdhShared(crypto.Curve.P256, secret, sent, out)
                    return
                }
                _ {}
            }
        }
        _ {}
    }
    fail crypto.BadKey
}

// The output file, or standard output for `-`.
WriteOut(sys: &System, path: str, data: []u8): fs.Error|OutOfMemory|WriteError!void {
    if path == "-" {
        var out := sys.Stdout()
        try out.Bytes(data)
        return
    }
    try value.Save(sys.arena, sys.io, path, data)
}

// enc seal: the nonce from --nonce, or 12 random bytes.
EncSeal(sys: &System, args: &Args, bits: int): int {
    var stdin := sys.Stdin()
    source := args.positional[0]
    plain := value.Input(sys.arena, sys.io, mut stdin, source) else e {
        sys.PrintErr("bigcalc: {source}: {e}\n", (source, e)) else {}
        return ExitError
    }
    var nonce: [12]u8 = [0; 12]
    if hex := args.Value("--nonce") {
        given := encoding.HexDecode(sys.arena, hex) else {
            sys.PrintErr("bigcalc: --nonce takes 24 hex digits\n") else {}
            return ExitUsage
        }
        if given.Len() != 12 {
            sys.PrintErr("bigcalc: --nonce takes 24 hex digits\n") else {}
            return ExitUsage
        }
        (mut nonce[..]).CopyFrom(given)
    } else {
        crypto.Random(sys.io, mut nonce[..]) else {
            return Stop(sys, "canceled while reading random bytes", ExitTime)
        }
    }
    var point: [65]u8 = [0; 65]
    var g := EncCipher(sys, args, bits, mut point[..], "".AsBytes()) else e {
        return KeyFailed(sys, e)
    }
    defer g.Wipe()
    aad := (args.Value("--aad") else { "" }).AsBytes()
    var sealed := seal.Seal(sys.arena, &g, &nonce, aad, plain) else e {
        sys.PrintErr("bigcalc: enc: {e}\n", (e)) else {}
        return ExitError
    }
    if Ephemeral(args) {
        // the file starts with the new key's public half
        var both := sys.arena.Slice[u8](EphemeralLen + sealed.Len()) else {
            return Stop(sys, "out of memory", ExitMemory)
        }
        (mut both[..EphemeralLen]).CopyFrom(point[..])
        (mut both[EphemeralLen..]).CopyFrom(sealed)
        sealed = both[..]
    }
    var data := sealed
    if args.Has("--armor") {
        text := encoding.Base64Encode(sys.arena, sealed, encoding.Base64) else {
            return Stop(sys, "out of memory", ExitMemory)
        }
        armored := sys.arena.Fmt("{text}\n", (text)) else {
            return Stop(sys, "out of memory", ExitMemory)
        }
        data = armored.AsBytes()
    }
    out := args.positional[1]
    WriteOut(sys, out, data) else e {
        sys.PrintErr("bigcalc: {out}: {e}\n", (out, e)) else {}
        return ExitError
    }
    return 0
}

// enc open: a tag that does not match exits with ExitInvalid.
EncOpen(sys: &System, args: &Args, bits: int): int {
    var stdin := sys.Stdin()
    source := args.positional[0]
    var sealed := value.Input(sys.arena, sys.io, mut stdin, source) else e {
        sys.PrintErr("bigcalc: {source}: {e}\n", (source, e)) else {}
        return ExitError
    }
    if args.Has("--armor") {
        text := str.From(sealed) else {
            sys.PrintErr("bigcalc: {source}: not base64 text\n", (source)) else {}
            return ExitError
        }
        sealed = encoding.Base64Decode(sys.arena, text.Trim(), encoding.Base64) else e {
            sys.PrintErr("bigcalc: {source}: {e}\n", (source, e)) else {}
            return ExitError
        }
    }
    var sent := "".AsBytes()
    if Ephemeral(args) {
        if sealed.Len() < EphemeralLen {
            sys.PrintErr("bigcalc: {source}: too short for a file sealed to a key\n", (source)) else {}
            return ExitInvalid
        }
        sent = sealed[..EphemeralLen]
        sealed = sealed[EphemeralLen..]
    }
    var point: [65]u8 = [0; 65]
    var g := EncCipher(sys, args, bits, mut point[..], sent) else e {
        return KeyFailed(sys, e)
    }
    defer g.Wipe()
    aad := (args.Value("--aad") else { "" }).AsBytes()
    plain := seal.Open(sys.arena, &g, aad, sealed) else e {
        sys.PrintErr("bigcalc: enc: {e}\n", (e)) else {}
        return match e {
            crypto.BadTag { ExitInvalid }
            _ { ExitError }
        }
    }
    out := args.positional[1]
    WriteOut(sys, out, plain) else e {
        sys.PrintErr("bigcalc: {out}: {e}\n", (out, e)) else {}
        return ExitError
    }
    return 0
}

/// Mac prints the HMAC of a file, or checks it against --expect.
Mac(sys: &System, argv: []str): int *argv into sys {
    args := ParseArgs(sys.arena, argv, MacValued, NoOptions) else e {
        return Usage(sys, argv, e)
    }
    if args.positional.Len() != 1 {
        sys.PrintErr("bigcalc: mac takes one file\n") else {}
        return ExitUsage
    }
    spec := args.Value("--key") else {
        sys.PrintErr("bigcalc: mac needs --key\n") else {}
        return ExitUsage
    }
    key := value.Spec(sys.arena, sys.io, spec) else e {
        sys.PrintErr("bigcalc: --key: {e}\n", (e)) else {}
        return ExitError
    }
    var stdin := sys.Stdin()
    path := args.positional[0]
    data := value.Input(sys.arena, sys.io, mut stdin, path) else e {
        sys.PrintErr("bigcalc: {path}: {e}\n", (path, e)) else {}
        return ExitError
    }
    var tag: [64]u8 = [0; 64]
    var n := 0
    var name := ""
    match args.Value("--hash") else { "sha256" } {
        "sha256" {
            (mut tag[..32]).CopyFrom(crypto.Hmac256(key, data)[..])
            n = 32
            name = "HMAC-SHA256"
        }
        "sha384" {
            (mut tag[..48]).CopyFrom(crypto.Hmac384(key, data)[..])
            n = 48
            name = "HMAC-SHA384"
        }
        "sha512" {
            (mut tag[..64]).CopyFrom(crypto.Hmac512(key, data)[..])
            n = 64
            name = "HMAC-SHA512"
        }
        _ {
            sys.PrintErr("bigcalc: --hash takes sha256, sha384 or sha512\n") else {}
            return ExitUsage
        }
    }
    if expect := args.Value("--expect") {
        want := encoding.HexDecode(sys.arena, expect) else {
            sys.PrintErr("bigcalc: --expect takes hex\n") else {}
            return ExitUsage
        }
        if !crypto.Equal(tag[..n], want) {
            sys.PrintErr("bigcalc: tag does not match\n") else {}
            return ExitInvalid
        }
        sys.Print("tag ok\n") else {}
        return 0
    }
    hex := encoding.HexEncode(sys.arena, tag[..n]) else {
        return Stop(sys, "out of memory", ExitMemory)
    }
    sys.Print("{name} ({path}) = {hex}\n", (name, path, hex)) else {}
    return 0
}

/// Hash prints the digests of files, hashing each file in a task of its
/// own and printing in the order given.
Hash(sys: &System, argv: []str into sys): int {
    args := ParseArgs(sys.arena, argv, HashValued, HashSwitches) else e {
        return Usage(sys, argv, e)
    }
    if args.positional.Len() == 0 {
        sys.PrintErr("bigcalc: hash takes at least one file\n") else {}
        return ExitUsage
    }
    alg := args.Value("--alg") else { "sha256" }
    algs := digest.Algs(sha1: alg == "sha1" || alg == "all", sha256: alg == "sha256" || alg == "all", sha384: alg == "sha384" || alg == "all", sha512: alg == "sha512" || alg == "all")
    if !algs.sha1 && !algs.sha256 && !algs.sha384 && !algs.sha512 {
        sys.PrintErr("bigcalc: --alg takes sha1, sha256, sha384, sha512 or all\n") else {}
        return ExitUsage
    }
    var results := List[digest.Sums].New(sys.arena)
    for path in args.positional {
        if path == "-" {
            var stdin := sys.Stdin()
            data := value.Input(sys.arena, sys.io, mut stdin, path) else {
                return Stop(sys, "cannot read standard input", ExitError)
            }
            results.Push(digest.Bytes(path, data)) else {
                return Stop(sys, "out of memory", ExitMemory)
            }
            continue
        }
        results.Push(digest.Empty(path)) else {
            return Stop(sys, "out of memory", ExitMemory)
        }
    }
    scope s := sys.io.Scope() {
        for r in mut results {
            if r.path != "-" {
                s.go digest.File(sys.io, algs, r)
            }
        }
    }
    var code := 0
    for r in &results {
        if e := r.failed {
            sys.PrintErr("bigcalc: {r.path}: {e}\n", (r, e)) else {}
            code = ExitError
            continue
        }
        PrintSums(sys, r, algs, args.Has("--base64")) else {
            return Stop(sys, "cannot write standard output", ExitError)
        }
    }
    return code
}

// One file's lines, one per digest.
PrintSums(sys: &System, r: &digest.Sums, algs: digest.Algs, base64: bool): OutOfMemory|WriteError!void {
    if algs.sha1 {
        try SumLine(sys, "SHA1", r.path, r.sha1[..], base64)
    }
    if algs.sha256 {
        try SumLine(sys, "SHA256", r.path, r.sha256[..], base64)
    }
    if algs.sha384 {
        try SumLine(sys, "SHA384", r.path, r.sha384[..], base64)
    }
    if algs.sha512 {
        try SumLine(sys, "SHA512", r.path, r.sha512[..], base64)
    }
}

SumLine(sys: &System, name: str, path: str, sum: []u8, base64: bool): OutOfMemory|WriteError!void {
    text := try value.Text(sys.arena, sum, if base64 { "base64" } else { "hex" })
    try sys.Print("{name} ({path}) = {text}\n", (name, path, text))
}

// The alphabet a command line names.
Alphabet(name: str): ?encoding.Alphabet {
    return match name {
        "base64" { some(encoding.Base64) }
        "base64url" { some(encoding.Base64Url) }
        _ { none }
    }
}

/// Encode writes a file as base64, base64url or hex on one line, reading it
/// in pieces.
Encode(sys: &System, argv: []str): int {
    if argv.Len() != 2 {
        sys.PrintErr("bigcalc: encode takes base64, base64url or hex, and a file\n") else {}
        return ExitUsage
    }
    path := argv[1]
    if path == "-" {
        var input := sys.Stdin()
        return EncodeFrom(sys, argv[0], mut input, path)
    }
    var f := fs.Open(sys.io, path) else e {
        sys.PrintErr("bigcalc: {path}: {e}\n", (path, e)) else {}
        return ExitError
    }
    var buf: [4096]u8 = [0; 4096]
    var input := f.Reader(sys.io, mut buf[..])
    code := EncodeFrom(sys, argv[0], mut input, path)
    f.Close(sys.io) else {
        f.Abandon()
    }
    return code
}

// Encodes input in pieces of 48 KiB, a multiple of 3, so only the last
// piece of base64 is padded.
EncodeFrom(sys: &System, kind: str, input: mut Reader, path: str): int {
    alphabet := Alphabet(kind)
    if alphabet.IsNone() && kind != "hex" {
        sys.PrintErr("bigcalc: encode takes base64, base64url or hex\n") else {}
        return ExitUsage
    }
    var piece := sys.arena.Slice[u8](49_152) else {
        return Stop(sys, "out of memory", ExitMemory)
    }
    var text := sys.arena.Slice[u8](65_536) else {
        return Stop(sys, "out of memory", ExitMemory)
    }
    var scratch := Arena.New(sys.heap) else {
        return Stop(sys, "out of memory", ExitMemory)
    }
    defer scratch.Free()
    var out := sys.Stdout()
    for {
        n := input.ReadAll(mut piece[..]) else e {
            sys.PrintErr("bigcalc: {path}: {e}\n", (path, e)) else {}
            return ExitError
        }
        if n == 0 {
            break
        }
        if a := alphabet {
            m := encoding.Base64EncodeTo(mut text[..encoding.Base64Len(n, a)], piece[..n], a)
            out.Bytes(text[..m]) else {
                return Stop(sys, "cannot write standard output", ExitError)
            }
        } else {
            hex := encoding.HexEncode(&scratch, piece[..n]) else {
                return Stop(sys, "out of memory", ExitMemory)
            }
            out.Str(hex) else {
                return Stop(sys, "cannot write standard output", ExitError)
            }
            scratch.Reset()
        }
        if n < piece.Len() {
            break
        }
    }
    out.Str("\n") else {
        return Stop(sys, "cannot write standard output", ExitError)
    }
    return 0
}

/// Decode writes the bytes a base64, base64url or hex file spells.
Decode(sys: &System, argv: []str): int {
    if argv.Len() != 2 {
        sys.PrintErr("bigcalc: decode takes base64, base64url or hex, and a file\n") else {}
        return ExitUsage
    }
    var stdin := sys.Stdin()
    path := argv[1]
    data := value.Input(sys.arena, sys.io, mut stdin, path) else e {
        sys.PrintErr("bigcalc: {path}: {e}\n", (path, e)) else {}
        return ExitError
    }
    text := str.From(data) else {
        sys.PrintErr("bigcalc: {path}: not text\n", (path)) else {}
        return ExitError
    }
    body := text.Trim()
    var bytes := "".AsBytes()
    if a := Alphabet(argv[0]) {
        var dst := sys.arena.Slice[u8](body.Len() / 4 * 3 + 3) else {
            return Stop(sys, "out of memory", ExitMemory)
        }
        n := encoding.Base64DecodeTo(mut dst[..], body, a) else e {
            sys.PrintErr("bigcalc: {path}: {e}\n", (path, e)) else {}
            return ExitError
        }
        bytes = dst[..n]
    } else if argv[0] == "hex" {
        bytes = encoding.HexDecode(sys.arena, body) else e {
            sys.PrintErr("bigcalc: {path}: {e}\n", (path, e)) else {}
            return ExitError
        }
    } else {
        sys.PrintErr("bigcalc: decode takes base64, base64url or hex\n") else {}
        return ExitUsage
    }
    var out := sys.Stdout()
    out.Bytes(bytes) else {
        return Stop(sys, "cannot write standard output", ExitError)
    }
    return 0
}
