// Why: the curve and RSA code is held to Go's crypto packages through one
// long-running Mk process, separate from the AEAD driver.
// What: the EC driver cr2EcDriver, its fixture cr2EcFixture exporting the
// curve internals, cr2EcAsk and cr2EcCheck.
// How: one command per stdin line, built dev and release over a std copy with
// the fixture; the command table is above cr2EcDriver.

package std

import (
	"strings"
	"sync"
	"testing"
)

// cr2EcFixture exports the curves' internals to the driver.
const cr2EcFixture = `// Fixture of the std-crypto2 tests: the curve internals, exported.

FxPt256(b: []u8): P256Pt {
    if b.Len() == 0 {
        return P256Identity
    }
    return P256PtFromBytes(b) else { P256Identity }
}

FxPt384(b: []u8): P384Pt {
    if b.Len() == 0 {
        return P384Identity
    }
    return P384PtFromBytes(b) else { P384Identity }
}

// FxEcInv writes 1 / a in field f (a below the modulus) to out.
export FxEcInv(f: int, a: []u8, out: mut []u8) {
    if f == 0 {
        x := P256Decode(a) else { return }
        r := P256Invert(&x)
        P256Encode(&r, mut out[..])
    } else if f == 1 {
        x := P256nDecode(a) else { return }
        r := P256nInvert(&x)
        P256nEncode(&r, mut out[..])
    } else if f == 2 {
        x := P384Decode(a) else { return }
        r := P384Invert(&x)
        P384Encode(&r, mut out[..])
    } else {
        x := P384nDecode(a) else { return }
        r := P384nInvert(&x)
        P384nEncode(&r, mut out[..])
    }
}

// FxEcOp is op (0 add, 1 double, 2 constant-time k p, 3 public u1 G + u2 q
// by verification's VtMulAdd, q not the identity) on curve c, its
// uncompressed encoding written to out; 0 for the identity.
export FxEcOp(c: int, op: int, p: []u8, q: []u8, k: []u8, u: []u8, out: mut []u8): int {
    if op == 3 {
        return FxVtMulAdd(c, k, q, u, out)
    }
    if c == 0 {
        a := FxPt256(p)
        b := FxPt256(q)
        r := match op {
            0 { P256PtAdd(&a, &b) }
            1 { P256PtDouble(&a) }
            _ { P256PtMul(&a, k) }
        }
        if !P256PtBytes(&r, mut out[..]) {
            return 0
        }
        return 65
    }
    a := FxPt384(p)
    b := FxPt384(q)
    r := match op {
        0 { P384PtAdd(&a, &b) }
        1 { P384PtDouble(&a) }
        _ { P384PtMul(&a, k) }
    }
    if !P384PtBytes(&r, mut out[..]) {
        return 0
    }
    return 97
}

// FxP256Base writes k G by the comb (P256PtBaseMul) to out and returns
// 65, or 0 for the identity.
export FxP256Base(k: []u8, out: mut []u8): int {
    r := P256PtBaseMul(k)
    if !P256PtBytes(&r, mut out[..]) {
        return 0
    }
    return 65
}

// FxRsaE is the key's exponent; FxRsaN its modulus.
export FxRsaE(k: &RsaPublic): u64 {
    return k.e
}

export FxRsaN(k: &RsaPublic): []u8 from k.n {
    return k.n
}

// FxRsaOp is RsaPublicOp: s^e mod n into out (the modulus's length), or
// false when s is not that long or not below n.
export FxRsaOp(k: &RsaPublic, s: []u8, out: mut []u8): bool {
    return RsaPublicOp(k, s, out)
}

// FxEcScalar is the secret's scalar bytes.
export FxEcScalar(s: &EcSecret): [32]u8 {
    return s.d
}
`

// cr2EcDriver is the EC oracle driver. Commands, fields separated by one
// space, byte fields lower-case hex (possibly empty), numbers decimal; c is
// 0 for P-256 and 1 for P-384, a point is its uncompressed SEC 1 encoding or
// empty for the identity:
//
//	I f a                -> I <1/a>        (f: 0 p256, 1 p256 n, 2 p384, 3 p384 n)
//	A c p q              -> A <p + q>
//	D c p                -> D <2p>
//	X c p k              -> X <k p>        (constant-time window)
//	Y c u1 q u2          -> Y <u1 G + u2 q> (public double multiplication)
//	B k                  -> B <k G>        (P-256's comb, k 32 bytes)
//	P d                  -> P <public> | P <error>
//	H d peer n           -> H <shared> | H <error> <clean>   (out n bytes)
//	S d digest f n       -> S <sig> <tail> | S <error> <clean> (f: der, fixed)
//	V c public digest f sig -> V true | V false
//	G                    -> G <d> <public>  (EcSecret.Generate)
//	R                    -> R <generate P384> <from-bytes P384> <ecdh P384> <wiped sign> <wiped ecdh>
//	K n e                -> K ok | K <error>     (RsaPublic.New, e decimal)
//	W kind h n e digest sig -> W true | W false | W <error>  (kind p PKCS#1 v1.5, s PSS; h 256, 384, 512)
//	E n e s              -> E <s^e mod n> | E false | E <error>   (RsaPublicOp, the modulus's length)
//	U der                -> U ec <curve> <point> | U rsa <n> <e> | U ed <key> | U <error>   (ParsePublicKey)
//	Z der                -> Z ec <d> | Z ed <seed> | Z <error>                              (ParsePrivateKey)
//	J der d              -> J <shared> | J <error>   (ParsePublicKey, then ECDH with d)
//	M pem                -> M <answer>; ... one per block, a U or Z answer by label (PEM text hex-encoded)
const cr2EcDriver = "import std.encoding\n" + cryptoPrelude + `

Take(sp: mut Split): str {
    return sp.Next() else { "" }
}

Bytes(a: &Arena, s: str): OutOfMemory![]u8 from a {
    out := try a.Slice[u8](s.Len() / 2)
    Unhex(s, mut out[..])
    return out[..]
}

Num(s: str): int {
    return Cast[int](s.ParseInt() else { 0 })
}

Name(e: crypto.Error): str {
    return match e {
        crypto.BadLength { "bad-length" }
        crypto.BadKey { "bad-key" }
        crypto.BadEncoding { "bad-encoding" }
        crypto.Encrypted { "encrypted" }
        _ { "other" }
    }
}

All(b: []u8, x: u8): bool {
    var d: u8 = 0
    for i in 0..b.Len() {
        d = d | (b[i] ^ x)
    }
    return d == 0
}

Filled(a: &Arena, n: int): OutOfMemory!mut []u8 from a {
    out := try a.Slice[u8](n)
    for i in 0..n {
        out[i] = 165
    }
    return out
}

Fmt(s: str): crypto.SigFormat {
    if s == "der" {
        return crypto.SigFormat.Der
    }
    return crypto.SigFormat.Fixed
}

CurveOf(c: int): crypto.Curve {
    if c == 0 {
        return crypto.Curve.P256
    }
    return crypto.Curve.P384
}

DoOp(w: mut Writer, a: &Arena, op: int, sp: mut Split): WriteError|OutOfMemory!void {
    c := Num(Take(sp))
    f1 := try Bytes(a, Take(sp))
    f2 := try Bytes(a, Take(sp))
    f3 := try Bytes(a, Take(sp))
    var out: [97]u8 = [0; 97]
    n := if op == 0 {
        crypto.FxEcOp(c, 0, f1, f2, f3, f3, mut out[..])
    } else if op == 1 {
        crypto.FxEcOp(c, 1, f1, f2, f3, f3, mut out[..])
    } else if op == 2 {
        crypto.FxEcOp(c, 2, f1, f3, f2, f3, mut out[..])
    } else {
        crypto.FxEcOp(c, 3, f3, f2, f1, f3, mut out[..])
    }
    cmd := match op {
        0 { "A" }
        1 { "D" }
        2 { "X" }
        _ { "Y" }
    }
    try Print(w, "{cmd} ", (cmd: cmd))
    try Hex(w, out[0..n])
    try Print(w, "\n")
}

DoBase(w: mut Writer, a: &Arena, sp: mut Split): WriteError|OutOfMemory!void {
    k := try Bytes(a, Take(sp))
    var out: [65]u8 = [0; 65]
    n := crypto.FxP256Base(k, mut out[..])
    try Print(w, "B ")
    try Hex(w, out[0..n])
    try Print(w, "\n")
}

DoInv(w: mut Writer, a: &Arena, sp: mut Split): WriteError|OutOfMemory!void {
    f := Num(Take(sp))
    x := try Bytes(a, Take(sp))
    var out: [48]u8 = [0; 48]
    crypto.FxEcInv(f, x, mut out[0..x.Len()])
    try Print(w, "I ")
    try Hex(w, out[0..x.Len()])
    try Print(w, "\n")
}

DoPublic(w: mut Writer, a: &Arena, sp: mut Split): WriteError|OutOfMemory!void {
    d := try Bytes(a, Take(sp))
    s := crypto.EcSecret.FromBytes(crypto.Curve.P256, d) else e {
        try Print(w, "P {e}\n", (e: Name(e)))
        return
    }
    var out: [70]u8 = [0; 70]
    n := s.Public(mut out[..]) else e {
        try Print(w, "P {e}\n", (e: Name(e)))
        return
    }
    try Print(w, "P ")
    try Hex(w, out[0..n])
    try Print(w, "\n")
}

DoEcdh(w: mut Writer, a: &Arena, sp: mut Split): WriteError|OutOfMemory!void {
    d := try Bytes(a, Take(sp))
    peer := try Bytes(a, Take(sp))
    out := try Filled(a, Num(Take(sp)))
    s := crypto.EcSecret.FromBytes(crypto.Curve.P256, d) else e {
        try Print(w, "H {e} true\n", (e: Name(e)))
        return
    }
    crypto.EcdhShared(crypto.Curve.P256, &s, peer, mut out[..]) else e {
        clean := All(out[..], 165)
        try Print(w, "H {e} {clean}\n", (e: Name(e), clean: clean))
        return
    }
    try Print(w, "H ")
    try Hex(w, out[..])
    try Print(w, "\n")
}

DoSign(w: mut Writer, a: &Arena, sp: mut Split): WriteError|OutOfMemory!void {
    d := try Bytes(a, Take(sp))
    digest := try Bytes(a, Take(sp))
    f := Fmt(Take(sp))
    out := try Filled(a, Num(Take(sp)))
    s := crypto.EcSecret.FromBytes(crypto.Curve.P256, d) else e {
        try Print(w, "S {e} true\n", (e: Name(e)))
        return
    }
    n := crypto.EcdsaSign(&s, digest, f, mut out[..]) else e {
        clean := All(out[..], 165)
        try Print(w, "S {e} {clean}\n", (e: Name(e), clean: clean))
        return
    }
    try Print(w, "S ")
    try Hex(w, out[0..n])
    tail := All(out[n..out.Len()], 165)
    try Print(w, " {tail}\n", (tail: tail))
}

DoVerify(w: mut Writer, a: &Arena, sp: mut Split): WriteError|OutOfMemory!void {
    c := Num(Take(sp))
    public := try Bytes(a, Take(sp))
    digest := try Bytes(a, Take(sp))
    f := Fmt(Take(sp))
    sig := try Bytes(a, Take(sp))
    ok := crypto.EcdsaVerify(public, CurveOf(c), digest, f, sig)
    try Print(w, "V {ok}\n", (ok: ok))
}

// DoOldVerify is DoVerify through the projective verification with
// complete formulas (the fixture's FxOldEcdsaVerify).
DoOldVerify(w: mut Writer, a: &Arena, sp: mut Split): WriteError|OutOfMemory!void {
    c := Num(Take(sp))
    public := try Bytes(a, Take(sp))
    digest := try Bytes(a, Take(sp))
    f := Fmt(Take(sp))
    sig := try Bytes(a, Take(sp))
    ok := crypto.FxOldEcdsaVerify(public, c, digest, f, sig)
    try Print(w, "O {ok}\n", (ok: ok))
}

// DoVt runs one Vt formula (FxVtOp) on curve c.
DoVt(w: mut Writer, a: &Arena, sp: mut Split): WriteError|OutOfMemory!void {
    c := Num(Take(sp))
    op := Num(Take(sp))
    p := try Bytes(a, Take(sp))
    q := try Bytes(a, Take(sp))
    var out: [97]u8 = [0; 97]
    n := crypto.FxVtOp(c, op, p, q, mut out[..])
    try Print(w, "T ")
    try Hex(w, out[0..n])
    try Print(w, "\n")
}

DoGenerate(w: mut Writer, io: Io): WriteError!void {
    s := crypto.EcSecret.Generate(io, crypto.Curve.P256) else {
        try Print(w, "G error\n")
        return
    }
    d := crypto.FxEcScalar(&s)
    var out: [65]u8 = [0; 65]
    _ = s.Public(mut out[..]) else {
        try Print(w, "G error\n")
        return
    }
    try Print(w, "G ")
    try Hex(w, d[..])
    try Print(w, " ")
    try Hex(w, out[..])
    try Print(w, "\n")
}

// DoRefusals: a P-384 secret, from the OS or bytes, and a P-384 ECDH, are
// BadKey; a wiped secret signs and exchanges BadKey.
DoRefusals(w: mut Writer, io: Io): WriteError!void {
    var g := "ok"
    _ = crypto.EcSecret.Generate(io, crypto.Curve.P384) else e {
        g = match e {
            crypto.BadKey { "bad-key" }
            _ { "other" }
        }
    }
    var d: [32]u8 = [1; 32]
    var f := "ok"
    _ = crypto.EcSecret.FromBytes(crypto.Curve.P384, d[..]) else e {
        f = Name(e)
    }
    var s := crypto.EcSecret.FromBytes(crypto.Curve.P256, d[..]) else {
        try Print(w, "R error\n")
        return
    }
    var pub: [65]u8 = [0; 65]
    _ = s.Public(mut pub[..]) else {
        try Print(w, "R error\n")
        return
    }
    var out: [32]u8 = [0; 32]
    var x := "ok"
    crypto.EcdhShared(crypto.Curve.P384, &s, pub[..], mut out[..]) else e {
        x = Name(e)
    }
    s.Wipe()
    var sig: [72]u8 = [0; 72]
    var ws := "ok"
    _ = crypto.EcdsaSign(&s, d[..], crypto.SigFormat.Der, mut sig[..]) else e {
        ws = Name(e)
    }
    var wx := "ok"
    crypto.EcdhShared(crypto.Curve.P256, &s, pub[..], mut out[..]) else e {
        wx = Name(e)
    }
    try Print(w, "R {g} {f} {x} {ws} {wx}\n", (g: g, f: f, x: x, ws: ws, wx: wx))
}

DoRsaKey(w: mut Writer, a: &Arena, sp: mut Split): WriteError|OutOfMemory!void {
    n := try Bytes(a, Take(sp))
    ex := Cast[u64](Take(sp).ParseInt() else { 0 })
    _ = crypto.RsaPublic.New(n, ex) else e {
        try Print(w, "K {e}\n", (e: Name(e)))
        return
    }
    try Print(w, "K ok\n")
}

DoRsaVerify(w: mut Writer, a: &Arena, sp: mut Split): WriteError|OutOfMemory!void {
    pss := Take(sp) == "s"
    bits := Num(Take(sp))
    h := if bits == 256 { crypto.HashKind.Sha256 } else if bits == 384 { crypto.HashKind.Sha384 } else { crypto.HashKind.Sha512 }
    n := try Bytes(a, Take(sp))
    ex := Cast[u64](Take(sp).ParseInt() else { 0 })
    digest := try Bytes(a, Take(sp))
    sig := try Bytes(a, Take(sp))
    k := crypto.RsaPublic.New(n, ex) else e {
        try Print(w, "W {e}\n", (e: Name(e)))
        return
    }
    ok := if pss { crypto.RsaVerifyPss(&k, h, digest, sig) } else { crypto.RsaVerifyPkcs1(&k, h, digest, sig) }
    try Print(w, "W {ok}\n", (ok: ok))
}

DoRsaOp(w: mut Writer, a: &Arena, sp: mut Split): WriteError|OutOfMemory!void {
    n := try Bytes(a, Take(sp))
    ex := Cast[u64](Take(sp).ParseInt() else { 0 })
    s := try Bytes(a, Take(sp))
    k := crypto.RsaPublic.New(n, ex) else e {
        try Print(w, "E {e}\n", (e: Name(e)))
        return
    }
    out := try a.Slice[u8](crypto.FxRsaN(&k).Len())
    if !crypto.FxRsaOp(&k, s, mut out[..]) {
        try Print(w, "E false\n")
        return
    }
    try Print(w, "E ")
    try Hex(w, out[..])
    try Print(w, "\n")
}

PrintPublic(w: mut Writer, der: []u8): WriteError!void {
    k := crypto.ParsePublicKey(der) else e {
        try Print(w, "{e}", (e: Name(e)))
        return
    }
    match k {
        .Ec(curve, point) {
            c := match curve {
                .P256 { 0 }
                .P384 { 1 }
                _ {
                    Panic("crypto.Curve gained a member this match does not handle")
                }
            }
            try Print(w, "ec {c} ", (c: c))
            try Hex(w, point)
        }
        .Rsa(key) {
            try Print(w, "rsa ")
            try Hex(w, crypto.FxRsaN(&key))
            x := crypto.FxRsaE(&key)
            try Print(w, " {x}", (x: x))
        }
        .Ed25519(key) {
            try Print(w, "ed ")
            try Hex(w, key[..])
        }
    }
}

PrintPrivate(w: mut Writer, der: []u8): WriteError!void {
    k := crypto.ParsePrivateKey(der) else e {
        try Print(w, "{e}", (e: Name(e)))
        return
    }
    match k {
        .Ec(secret) {
            d := crypto.FxEcScalar(&secret)
            try Print(w, "ec ")
            try Hex(w, d[..])
        }
        .Ed25519(seed) {
            try Print(w, "ed ")
            try Hex(w, seed[..])
        }
    }
}

DoParse(w: mut Writer, a: &Arena, private: bool, sp: mut Split): WriteError|OutOfMemory!void {
    der := try Bytes(a, Take(sp))
    if private {
        try Print(w, "Z ")
        try PrintPrivate(w, der)
    } else {
        try Print(w, "U ")
        try PrintPublic(w, der)
    }
    try Print(w, "\n")
}

DoParseEcdh(w: mut Writer, a: &Arena, sp: mut Split): WriteError|OutOfMemory!void {
    der := try Bytes(a, Take(sp))
    d := try Bytes(a, Take(sp))
    k := crypto.ParsePublicKey(der) else e {
        try Print(w, "J {e}\n", (e: Name(e)))
        return
    }
    point := match k {
        .Ec(curve: _, point: point) { point }
        _ {
            try Print(w, "J not-ec\n")
            return
        }
    }
    s := crypto.EcSecret.FromBytes(crypto.Curve.P256, d) else e {
        try Print(w, "J {e}\n", (e: Name(e)))
        return
    }
    var out: [32]u8 = [0; 32]
    crypto.EcdhShared(crypto.Curve.P256, &s, point, mut out[..]) else e {
        try Print(w, "J {e}\n", (e: Name(e)))
        return
    }
    try Print(w, "J ")
    try Hex(w, out[..])
    try Print(w, "\n")
}

DoPem(w: mut Writer, a: &Arena into a, sp: mut Split): WriteError|OutOfMemory!void {
    raw := try Bytes(a, Take(sp))
    text := str.From(raw) else {
        try Print(w, "M not-utf8\n")
        return
    }
    blocks := encoding.ParsePem(a, text) else {
        try Print(w, "M bad-pem\n")
        return
    }
    try Print(w, "M")
    for b in blocks.Items() {
        if b.label == "PRIVATE KEY" || b.label == "EC PRIVATE KEY" {
            try Print(w, " Z ")
            try PrintPrivate(w, b.der)
        } else {
            try Print(w, " U ")
            try PrintPublic(w, b.der)
        }
        try Print(w, ";")
    }
    try Print(w, "\n")
}

Do(w: mut Writer, a: &Arena into a, io: Io, line: str): WriteError|OutOfMemory!void {
    var sp := line.Split(" ")
    c := Take(mut sp)
    op := if c == "A" { 0 } else if c == "D" { 1 } else if c == "X" { 2 } else if c == "Y" { 3 } else { -1 }
    if op >= 0 {
        try DoOp(w, a, op, mut sp)
    } else if c == "B" {
        try DoBase(w, a, mut sp)
    } else if c == "I" {
        try DoInv(w, a, mut sp)
    } else if c == "P" {
        try DoPublic(w, a, mut sp)
    } else if c == "H" {
        try DoEcdh(w, a, mut sp)
    } else if c == "S" {
        try DoSign(w, a, mut sp)
    } else if c == "V" {
        try DoVerify(w, a, mut sp)
    } else if c == "O" {
        try DoOldVerify(w, a, mut sp)
    } else if c == "T" {
        try DoVt(w, a, mut sp)
    } else if c == "G" {
        try DoGenerate(w, io)
    } else if c == "R" {
        try DoRefusals(w, io)
    } else if c == "K" {
        try DoRsaKey(w, a, mut sp)
    } else if c == "W" {
        try DoRsaVerify(w, a, mut sp)
    } else if c == "E" {
        try DoRsaOp(w, a, mut sp)
    } else if c == "U" || c == "Z" {
        try DoParse(w, a, c == "Z", mut sp)
    } else if c == "J" {
        try DoParseEcdh(w, a, mut sp)
    } else if c == "M" {
        try DoPem(w, a, mut sp)
    } else {
        try Print(w, "? {c}\n", (c: c))
    }
}

export Main(sys: &System): ReadError|WriteError|OutOfMemory!void {
    var w := sys.Stdout()
    var inp := sys.Stdin()
    var ar := try Arena.New(sys.heap)
    defer ar.Free()
    for true {
        lo := try inp.Line(&ar)
        line := lo else {
            break
        }
        try Do(mut w, &ar, sys.io, line)
        try w.Flush()
        ar.Reset()
    }
}
`

// cr2EcExes holds the EC driver per variant, built once per test binary.
var cr2EcExes struct {
	sync.Mutex
	m map[string]string
}

// cr2EcVariants are the EC driver's builds: dev and release (the curves
// have no instruction path).
var cr2EcVariants = cr2Variants[:2]

// cr2EcAsk runs v's EC driver over lines and returns its answers.
func cr2EcAsk(t *testing.T, v cr2Variant, lines []string) []string {
	t.Helper()
	exe := func() string {
		cr2EcExes.Lock()
		defer cr2EcExes.Unlock()
		if exe, ok := cr2EcExes.m[v.name]; ok {
			return exe
		}
		dir, err := packageTempDir("mkcrypto2ec")
		if err != nil {
			t.Fatal(err)
		}
		return cr2BuildIn(t, dir, v, cr2EcDriver, map[string]string{"crypto/tests_crypto2ec.mk": cr2EcFixtureAll}, &cr2EcExes.m)
	}()
	r := runNative(t, exe, strings.Join(lines, "\n")+"\n")
	if r.code != 0 {
		t.Fatalf("%s EC driver exit %d\nstderr: %s\nstdout (tail): %s", v.name, r.code, r.stderr, cryptoTail(r.stdout, 2000))
	}
	got := strings.Split(strings.TrimRight(strings.ReplaceAll(r.stdout, "\r\n", "\n"), "\n"), "\n")
	if len(got) != len(lines) {
		t.Fatalf("%s EC driver: %d answers to %d commands\nstderr: %s\nstdout (tail): %s", v.name, len(got), len(lines), r.stderr, cryptoTail(r.stdout, 2000))
	}
	return got
}

// cr2EcCheck runs lines on both variants and compares each answer with
// want, reporting at most 10 differences per variant.
func cr2EcCheck(t *testing.T, what string, lines, want []string) {
	t.Helper()
	for _, v := range cr2EcVariants {
		got := cr2EcAsk(t, v, lines)
		bad := 0
		for i := range lines {
			if got[i] != want[i] {
				t.Errorf("%s %s: %s\n got  %s\n want %s", what, v.name, cryptoTail(lines[i], 300), cryptoTail(got[i], 300), cryptoTail(want[i], 300))
				if bad++; bad == 10 {
					t.Fatalf("%s %s: stopping after 10 differences", what, v.name)
				}
			}
		}
	}
}
