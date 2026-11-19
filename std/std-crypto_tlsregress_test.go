// Why: regression tests for bugs found in std.crypto, each pinned by a test
// named after it.
// What: wiped keys fail closed, Prk.FromBytes matches Go's hkdf.Expand,
// zero-padded P-256 scalars read as Go reads them, and a bounded AES-GCM stack.
// How: Mk programs run against Go's crypto; the stack bound comes from
// mk query stack, which must report no recursion.

package std

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/x509/pkix"
	"encoding/asn1"
	"fmt"
	"hash"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

const cr2ReviewProgram = cryptoPrelude + `

Take(sp: mut Split): str {
    return sp.Next() else { "" }
}

Bytes(a: &Arena, s: str): OutOfMemory![]u8 from a {
    out := try a.Slice[u8](s.Len() / 2)
    Unhex(s, mut out[..])
    return out[..]
}

Filled(a: &Arena, n: int): OutOfMemory!mut []u8 from a {
    out := try a.Slice[u8](n)
    for i in 0..n {
        out[i] = 165
    }
    return out
}

Num(s: str): int {
    return Cast[int](s.ParseInt() else { 0 })
}

Name(e: crypto.Error): str {
    return match e {
        crypto.BadTag { "bad-tag" }
        crypto.BadLength { "bad-length" }
        crypto.BadKey { "bad-key" }
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

Kind(bits: int): crypto.HashKind {
    return if bits == 256 { crypto.HashKind.Sha256 } else if bits == 384 { crypto.HashKind.Sha384 } else { crypto.HashKind.Sha512 }
}

// Code is 0 for success, else 1 + the error's place in Codes.
Code(e: crypto.Error): int {
    return match e {
        crypto.BadTag { 1 }
        crypto.BadLength { 2 }
        crypto.BadKey { 3 }
        _ { 4 }
    }
}

Codes(c: int): str {
    return if c == 0 { "ok" } else if c == 1 { "bad-tag" } else if c == 2 { "bad-length" } else if c == 3 { "bad-key" } else { "other" }
}

SealCode(g: &crypto.AesGcm, nonce: &[12]u8, aad: []u8, plain: []u8, out: mut []u8): int {
    _ = g.Seal(nonce, aad, plain, out) else e {
        return Code(e)
    }
    return 0
}

OpenCode(g: &crypto.AesGcm, nonce: &[12]u8, aad: []u8, sealed: []u8, out: mut []u8): int {
    _ = g.Open(nonce, aad, sealed, out) else e {
        return Code(e)
    }
    return 0
}

// DoWipe: key; seals a message, copies the key object, wipes it, then
// tries Seal, Open of the message sealed before, and Open of what the
// wiped object sealed with its first ciphertext byte flipped (a forgery
// that only an all-zero schedule accepts); every out stays 0xa5; the copy
// made before Wipe still opens the message.
DoWipe(w: mut Writer, a: &Arena, sp: mut Split): WriteError|OutOfMemory!void {
    key := try Bytes(a, Take(sp))
    var nonce: [12]u8 = [7; 12]
    var aad: [5]u8 = [9; 5]
    var plain: [40]u8 = [3; 40]
    var g := crypto.AesGcm.New(key) else e {
        try Print(w, "W {e}\n", (e: Name(e)))
        return
    }
    var sealed: [56]u8 = [0; 56]
    _ = g.Seal(&nonce, aad[..], plain[..], mut sealed[..]) else e {
        try Print(w, "W seal {e}\n", (e: Name(e)))
        return
    }
    c := g
    g.Wipe()
    out1 := try Filled(a, 64)
    s := Codes(SealCode(&g, &nonce, aad[..], plain[..], mut out1[..]))
    out2 := try Filled(a, 64)
    o := Codes(OpenCode(&g, &nonce, aad[..], sealed[..], mut out2[..]))
    var forged: [56]u8 = [0; 56]
    _ = g.Seal(&nonce, aad[..], plain[..], mut forged[..]) else {
        0
    }
    forged[0] = forged[0] ^ 1
    out3 := try Filled(a, 64)
    f := Codes(OpenCode(&g, &nonce, aad[..], forged[..], mut out3[..]))
    clean := All(out1[..], 165) && All(out2[..], 165) && All(out3[..], 165)
    var back: [40]u8 = [0; 40]
    k := OpenCode(&c, &nonce, aad[..], sealed[..], mut back[..])
    copy := k == 0 && All(back[..], 3)
    try Print(w, "W {s} {o} {f} {clean} {copy}\n", (s: s, o: o, f: f, clean: clean, copy: copy))
}

// DoPrk: bits salt ikm info n; expands before and after Prk.Wipe.
DoPrk(w: mut Writer, a: &Arena, sp: mut Split): WriteError|OutOfMemory!void {
    h := Kind(Num(Take(sp)))
    salt := try Bytes(a, Take(sp))
    ikm := try Bytes(a, Take(sp))
    info := try Bytes(a, Take(sp))
    n := Num(Take(sp))
    var prk := crypto.HkdfExtract(salt, ikm, h)
    before := try Filled(a, n)
    crypto.HkdfExpand(&prk, info, mut before[..]) else e {
        try Print(w, "P before {e}\n", (e: Name(e)))
        return
    }
    prk.Wipe()
    after := try Filled(a, n)
    crypto.HkdfExpand(&prk, info, mut after[..]) else e {
        clean := All(after[..], 165)
        try Print(w, "P ")
        try Hex(w, before[..])
        try Print(w, " {e} {clean}\n", (e: Name(e), clean: clean))
        return
    }
    try Print(w, "P ")
    try Hex(w, before[..])
    try Print(w, " ok ")
    try Hex(w, after[..])
    try Print(w, "\n")
}

// DoFrom: bits prk info n; Prk.FromBytes, then HkdfExpand.
DoFrom(w: mut Writer, a: &Arena, sp: mut Split): WriteError|OutOfMemory!void {
    h := Kind(Num(Take(sp)))
    key := try Bytes(a, Take(sp))
    info := try Bytes(a, Take(sp))
    out := try Filled(a, Num(Take(sp)))
    prk := crypto.Prk.FromBytes(h, key) else e {
        try Print(w, "F {e}\n", (e: Name(e)))
        return
    }
    crypto.HkdfExpand(&prk, info, mut out[..]) else e {
        try Print(w, "F expand {e}\n", (e: Name(e)))
        return
    }
    try Print(w, "F ")
    try Hex(w, out[..])
    try Print(w, "\n")
}

// DoChain: bits salt ikm label1 label2 n; TLS 1.3's shape: Extract, then
// a HashLen Expand (Derive-Secret), FromBytes of that secret, then an
// n-byte Expand under label2 (a traffic key or IV).
DoChain(w: mut Writer, a: &Arena, sp: mut Split): WriteError|OutOfMemory!void {
    bits := Num(Take(sp))
    h := Kind(bits)
    salt := try Bytes(a, Take(sp))
    ikm := try Bytes(a, Take(sp))
    l1 := try Bytes(a, Take(sp))
    l2 := try Bytes(a, Take(sp))
    out := try Filled(a, Num(Take(sp)))
    prk := crypto.HkdfExtract(salt, ikm, h)
    secret := try Filled(a, bits / 8)
    crypto.HkdfExpand(&prk, l1, mut secret[..]) else e {
        try Print(w, "C {e}\n", (e: Name(e)))
        return
    }
    var p2 := crypto.Prk.FromBytes(h, secret[..]) else e {
        try Print(w, "C {e}\n", (e: Name(e)))
        return
    }
    crypto.HkdfExpand(&p2, l2, mut out[..]) else e {
        try Print(w, "C {e}\n", (e: Name(e)))
        return
    }
    p2.Wipe()
    var again: [1]u8 = [165]
    var wiped := "ok"
    crypto.HkdfExpand(&p2, l2, mut again[..]) else e {
        wiped = Name(e)
    }
    try Print(w, "C ")
    try Hex(w, out[..])
    try Print(w, " {wiped}\n", (wiped: wiped))
}

Do(w: mut Writer, a: &Arena, line: str): WriteError|OutOfMemory!void {
    var sp := line.Split(" ")
    c := Take(mut sp)
    if c == "W" {
        try DoWipe(w, a, mut sp)
    } else if c == "P" {
        try DoPrk(w, a, mut sp)
    } else if c == "F" {
        try DoFrom(w, a, mut sp)
    } else if c == "C" {
        try DoChain(w, a, mut sp)
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
        try Do(mut w, &ar, line)
        try w.Flush()
        ar.Reset()
    }
}
`

// cr2ReviewAsk builds the regression program for v and runs lines through it.
func cr2ReviewAsk(t *testing.T, v cr2Variant, lines []string) []string {
	t.Helper()
	dir, err := packageTempDir("mkcrypto2rv")
	if err != nil {
		t.Fatal(err)
	}
	exe := cr2BuildIn(t, dir, v, cr2ReviewProgram, nil, nil)
	r := runNative(t, exe, strings.Join(lines, "\n")+"\n")
	if r.code != 0 {
		t.Fatalf("%s: exit %d\nstderr: %s\nstdout: %s", v.name, r.code, r.stderr, cryptoTail(r.stdout, 2000))
	}
	got := strings.Split(strings.TrimRight(strings.ReplaceAll(r.stdout, "\r\n", "\n"), "\n"), "\n")
	if len(got) != len(lines) {
		t.Fatalf("%s: %d answers to %d commands\n%s", v.name, len(got), len(lines), cryptoTail(r.stdout, 2000))
	}
	return got
}

func cr2HashOf(bits int) func() hash.Hash {
	switch bits {
	case 256:
		return sha256.New
	case 384:
		return sha512.New384
	}
	return sha512.New
}

// cr2HkdfLabel is RFC 8446 section 7.1's HkdfLabel: length, "tls13 " ||
// label, context.
func cr2HkdfLabel(label string, context []byte, n int) []byte {
	l := "tls13 " + label
	b := []byte{byte(n >> 8), byte(n), byte(len(l))}
	b = append(b, l...)
	b = append(b, byte(len(context)))
	return append(b, context...)
}

// TestSTDCRYPTO_WipedKeysFailClosed: after AesGcm.Wipe, Seal, Open
// of a genuine message and Open of a forgery are BadKey with out
// untouched, on the instruction and the portable path, dev and release;
// a copy made before Wipe still opens. After Prk.Wipe, HkdfExpand is
// BadKey with out untouched, for each hash. Before the fix the wiped
// AesGcm sealed under the all-zero schedule and opened the forgery (Ok),
// and the wiped Prk expanded as the all-zero key.
func TestSTDCRYPTO_WipedKeysFailClosed(t *testing.T) {
	t.Parallel()
	r := cryptoSplitmix{s: 0xC2_1}
	var lines, want []string
	for _, kl := range []int{16, 32} {
		k := make([]byte, kl)
		r.fill(k)
		lines = append(lines, fmt.Sprintf("W %x", k))
		want = append(want, "W bad-key bad-key bad-key true true")
	}
	for _, bits := range []int{256, 384, 512} {
		salt, ikm, info := make([]byte, 13), make([]byte, 40), make([]byte, 9)
		r.fill(salt)
		r.fill(ikm)
		r.fill(info)
		okm, err := hkdf.Key(cr2HashOf(bits), ikm, salt, string(info), 42)
		if err != nil {
			t.Fatal(err)
		}
		lines = append(lines, fmt.Sprintf("P %d %x %x %x 42", bits, salt, ikm, info))
		want = append(want, fmt.Sprintf("P %x bad-key true", okm))
	}
	for _, v := range cr2Variants {
		got := cr2ReviewAsk(t, v, lines)
		for i := range lines {
			if got[i] != want[i] {
				t.Errorf("%s: %s\n got  %s\n want %s", v.name, lines[i], got[i], want[i])
			}
		}
	}
}

// TestSTDCRYPTO_PrkFromBytesMatchesGo: a Prk made from HashLen
// bytes expands as crypto/hkdf.Expand does over the same PRK, for each
// hash, with TLS 1.3 HkdfLabel infos ("key" 16 and 32 bytes, "iv" 12,
// "finished" HashLen) and plain ones; any other PRK length is BadLength;
// and the TLS key schedule's chain (Extract, a HashLen Derive-Secret
// Expand, FromBytes of that secret, the traffic key's Expand) equals Go's,
// the re-made Prk failing closed after its Wipe.
func TestSTDCRYPTO_PrkFromBytesMatchesGo(t *testing.T) {
	t.Parallel()
	r := cryptoSplitmix{s: 0xC2_2}
	var lines, want []string
	for _, bits := range []int{256, 384, 512} {
		hl := bits / 8
		for _, n := range []int{0, hl - 1, hl + 1, 64 + 1, 1} {
			if n == hl {
				continue
			}
			k := make([]byte, n)
			r.fill(k)
			lines = append(lines, fmt.Sprintf("F %d %x 00 4", bits, k))
			want = append(want, "F bad-length")
		}
		for _, c := range []struct {
			label string
			n     int
		}{{"key", 16}, {"key", 32}, {"iv", 12}, {"finished", hl}, {"", 1}, {"", 255 * hl}} {
			prk := make([]byte, hl)
			r.fill(prk)
			info := cr2HkdfLabel(c.label, nil, c.n)
			if c.label == "" {
				info = []byte{}
			}
			okm, err := hkdf.Expand(cr2HashOf(bits), prk, string(info), c.n)
			if err != nil {
				t.Fatal(err)
			}
			lines = append(lines, fmt.Sprintf("F %d %x %x %d", bits, prk, info, c.n))
			want = append(want, fmt.Sprintf("F %x", okm))
		}
		prk := make([]byte, hl)
		r.fill(prk)
		lines = append(lines, fmt.Sprintf("F %d %x 00 %d", bits, prk, 255*hl+1))
		want = append(want, "F expand bad-length")
		// The chain: client_handshake_traffic_secret's shape, then its key.
		salt, ikm, th := make([]byte, hl), make([]byte, 32), make([]byte, hl)
		r.fill(salt)
		r.fill(ikm)
		r.fill(th)
		l1 := cr2HkdfLabel("c hs traffic", th, hl)
		l2 := cr2HkdfLabel("key", nil, 16)
		p1, err := hkdf.Extract(cr2HashOf(bits), ikm, salt)
		if err != nil {
			t.Fatal(err)
		}
		secret, err := hkdf.Expand(cr2HashOf(bits), p1, string(l1), hl)
		if err != nil {
			t.Fatal(err)
		}
		key, err := hkdf.Expand(cr2HashOf(bits), secret, string(l2), 16)
		if err != nil {
			t.Fatal(err)
		}
		lines = append(lines, fmt.Sprintf("C %d %x %x %x %x 16", bits, salt, ikm, l1, l2))
		want = append(want, fmt.Sprintf("C %x bad-key", key))
	}
	for _, v := range cr2Variants[:2] {
		got := cr2ReviewAsk(t, v, lines)
		for i := range lines {
			if got[i] != want[i] {
				t.Errorf("%s: %s\n got  %s\n want %s", v.name, cryptoTail(lines[i], 200), cryptoTail(got[i], 200), cryptoTail(want[i], 200))
			}
		}
	}
}

// cr2Sec1Padded is a SEC 1 ECPrivateKey of k whose privateKey OCTET
// STRING is pad || d, naming the curve when named.
func cr2Sec1Padded(t testing.TB, k *ecdsa.PrivateKey, pad []byte, named bool) []byte {
	t.Helper()
	d, err := k.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	pub, err := k.PublicKey.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	v := struct {
		Version    int
		PrivateKey []byte
		Curve      asn1.ObjectIdentifier `asn1:"optional,explicit,tag:0"`
		PublicKey  asn1.BitString        `asn1:"optional,explicit,tag:1"`
	}{1, append(append([]byte{}, pad...), d...), nil, asn1.BitString{Bytes: pub, BitLength: 8 * len(pub)}}
	if named {
		v.Curve = asn1.ObjectIdentifier{1, 2, 840, 10045, 3, 1, 7}
	}
	b, err := asn1.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// cr2Pkcs8Of wraps a SEC 1 key (no curve) in a P-256 PKCS#8
// PrivateKeyInfo.
func cr2Pkcs8Of(t testing.TB, sec1 []byte) []byte {
	t.Helper()
	params, err := asn1.Marshal(asn1.ObjectIdentifier{1, 2, 840, 10045, 3, 1, 7})
	if err != nil {
		t.Fatal(err)
	}
	b, err := asn1.Marshal(struct {
		Version    int
		Algo       pkix.AlgorithmIdentifier
		PrivateKey []byte
	}{0, pkix.AlgorithmIdentifier{Algorithm: asn1.ObjectIdentifier{1, 2, 840, 10045, 2, 1}, Parameters: asn1.RawValue{FullBytes: params}}, sec1})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// cr2PaddedKeys are SEC 1 and PKCS#8 encodings of k whose scalar has
// 1 and 2 leading zero bytes (Go reads them), and with a non-zero
// leading byte (Go refuses), each with Go's reading ("" when it refuses).
func cr2PaddedKeys(t testing.TB, k *ecdsa.PrivateKey) (ders [][]byte, goSays []string) {
	t.Helper()
	for _, pad := range [][]byte{{0}, {0, 0}, {1}, {0, 1}} {
		s1 := cr2Sec1Padded(t, k, pad, true)
		p8 := cr2Pkcs8Of(t, cr2Sec1Padded(t, k, pad, false))
		ders = append(ders, s1, p8)
		goSays = append(goSays, cr2FzGoPriv(s1), cr2FzGoPriv(p8))
	}
	return ders, goSays
}

// TestSTDCRYPTO_PaddedScalarMatchesGo: a P-256 private key whose
// SEC 1 privateKey is 0x00 || d or 0x00 0x00 || d, alone or in PKCS#8,
// reads as the same secret Go's ParseECPrivateKey and ParsePKCS8PrivateKey
// read; a non-zero byte in the padding is BadEncoding, and Go refuses it
// too. Before the fix every padded key was BadEncoding.
func TestSTDCRYPTO_PaddedScalarMatchesGo(t *testing.T) {
	t.Parallel()
	var lines, want []string
	for i := 0; i < 4; i++ {
		k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		d, _ := k.Bytes()
		ders, goSays := cr2PaddedKeys(t, k)
		for j, der := range ders {
			w := goSays[j]
			if j < 4 {
				// Zero padding: Go reads d.
				if w != fmt.Sprintf("Z ec %x", d) {
					t.Fatalf("Go reads %x as %q", der, w)
				}
			} else {
				if w != "" {
					t.Fatalf("Go reads non-zero padding %x as %q", der, w)
				}
				w = "Z bad-encoding"
			}
			lines = append(lines, fmt.Sprintf("Z %x", der))
			want = append(want, w)
		}
	}
	cr2EcCheck(t, "CR2-3", lines, want)
}

// TestSTDCRYPTO_AesGcmStackBounded: mk query stack shows
// AesGcm.Seal and AesGcm.Open (Open reads every TLS record) with no
// recursion, a direct chain of at most 12 calls, and no indirect call in
// std.crypto or std.simd.kernel. The one indirect call the query reports
// is std.rt's, not the cipher's: in a dev or test build every `fail`
// calls TraceFail, which calls CurrentSlot, and SlotTop
// (std/rt/slot.mk) calls through Regions.top_fn, the word that holds
// SlotTopLazy once a StackSize.Bytes class is reserved. SlotTopLazy is a
// loop that calls nothing, so the call adds one leaf frame below SlotTop
// and the bound is at most one frame more than the query's chain; a
// release build drops the calls to TraceFail. Every function that can
// fail reports this call (AesGcm.New, HkdfExpand; Hmac256 and Wipe, which
// cannot, are bounded). Exactly one, in that file, is allowed, so that
// any other fails.
func TestSTDCRYPTO_AesGcmStackBounded(t *testing.T) {
	t.Parallel()
	src := `import std.crypto

export Main(sys: &System): WriteError|crypto.Error!void {
    var key: [16]u8 = [1; 16]
    var nonce: [12]u8 = [2; 12]
    var plain: [64]u8 = [3; 64]
    var sealed: [80]u8 = [0; 80]
    var back: [64]u8 = [0; 64]
    var g := try crypto.AesGcm.New(key[..])
    n := try g.Seal(&nonce, plain[0..8], plain[..], mut sealed[..])
    m := try g.Open(&nonce, plain[0..8], sealed[0..n], mut back[..])
    g.Wipe()
    var out := sys.Stdout()
    try Print(mut out, "{m}\n", (m: m))
}
`
	dir, err := packageTempDir("mkcrypto2stack")
	if err != nil {
		t.Fatal(err)
	}
	stdDir := cryptoStdDir(t, dir, nil)
	if err := os.WriteFile(filepath.Join(dir, "main.mk"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	frameRe := regexp.MustCompile(`deepest direct chain: (\d+) frame\(s\)`)
	indirectRe := regexp.MustCompile(`(\d+) indirect call\(s\), first (\S+)`)
	for _, fn := range []string{"std.crypto.AesGcm.Seal", "std.crypto.AesGcm.Open"} {
		out, status := mkCmd(t, dir, stdDir, "query", "stack", fn)
		if status != 0 {
			t.Fatalf("mk query stack %s: exit %d\n%s", fn, status, out)
		}
		t.Logf("%s:\n%s", fn, out)
		if strings.Contains(strings.ReplaceAll(out, "no recursion", ""), "recursion") {
			t.Errorf("%s: recursion reported:\n%s", fn, out)
		}
		m := frameRe.FindStringSubmatch(out)
		if m == nil {
			t.Fatalf("%s: no chain depth in:\n%s", fn, out)
		}
		if n, _ := strconv.Atoi(m[1]); n < 2 || n > 12 {
			t.Errorf("%s: deepest chain %d calls, want 2 to 12", fn, n)
		}
		if strings.Contains(out, "bounded: no recursion and no indirect call") {
			continue
		}
		ind := indirectRe.FindStringSubmatch(out)
		if ind == nil || ind[1] != "1" || !strings.HasPrefix(filepath.ToSlash(ind[2]), "std/rt/slot.mk:") {
			t.Errorf("%s: an indirect call other than std.rt's SlotTop:\n%s", fn, out)
		}
	}
}
