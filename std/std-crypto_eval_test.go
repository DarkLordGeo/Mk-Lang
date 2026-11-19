// Why: crypto code also runs at compile time, on the portable path, and its
// wipes must clear every byte of key material.
// What: compile time against run time and Go, Wipe clearing keys, and Wipe
// refused at compile time (E0901).
// How: compile-time HMAC, HKDF and AES-GCM against run time; the key bytes
// are read through a fixture that exports AesGcm's fields.

package std

import (
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const cr2EvalProgram = `import std.crypto

const Key = "a key of some length for the compile-time path"
const Msg = "the message, long enough to take two blocks of SHA-384 and SHA-512 when the pads are counted, and a partial one"

Seal(): [61]u8 {
    var key: [32]u8 = [0; 32]
    for i in 0..32 {
        key[i] = Trunc[u8](i * 7 + 1)
    }
    g := crypto.AesGcm.New(key[..]) else {
        Panic("New failed")
    }
    var nonce: [12]u8 = [0; 12]
    nonce[3] = 9
    var out: [61]u8 = [0; 61]
    _ = g.Seal(&nonce, Key.AsBytes()[0..5], Msg.AsBytes()[0..45], mut out[..]) else {
        Panic("Seal failed")
    }
    return out
}

Okm(): [77]u8 {
    p := crypto.HkdfExtract(Key.AsBytes()[0..9], Msg.AsBytes(), crypto.HashKind.Sha384)
    var out: [77]u8 = [0; 77]
    crypto.HkdfExpand(&p, Key.AsBytes(), mut out[..]) else {
        Panic("Expand failed")
    }
    return out
}

const H3: [48]u8 = crypto.Hmac384(Key.AsBytes(), Msg.AsBytes())
const H5: [64]u8 = crypto.Hmac512(Key.AsBytes(), Msg.AsBytes())
const S: [61]u8 = Seal()
const K: [77]u8 = Okm()

export Main(sys: &System): WriteError!void {
    var out := sys.Stdout()
    try Hex(mut out, H3[..])
    try Print(mut out, "\n")
    try Hex(mut out, H5[..])
    try Print(mut out, "\n")
    try Hex(mut out, S[..])
    try Print(mut out, "\n")
    try Hex(mut out, K[..])
    try Print(mut out, "\n")
    h3 := crypto.Hmac384(Key.AsBytes(), Msg.AsBytes())
    try Hex(mut out, h3[..])
    try Print(mut out, "\n")
    h5 := crypto.Hmac512(Key.AsBytes(), Msg.AsBytes())
    try Hex(mut out, h5[..])
    try Print(mut out, "\n")
    s := Seal()
    try Hex(mut out, s[..])
    try Print(mut out, "\n")
    k := Okm()
    try Hex(mut out, k[..])
    try Print(mut out, "\n")
}
`

// TestSTDCRYPTO_CompileTimeMatchesRunTime: each value computed by the
// evaluator equals the run-time one and Go's, in dev and release, on the
// instruction and the portable paths.
func TestSTDCRYPTO_CompileTimeMatchesRunTime(t *testing.T) {
	t.Parallel()
	const key = "a key of some length for the compile-time path"
	const msg = "the message, long enough to take two blocks of SHA-384 and SHA-512 when the pads are counted, and a partial one"
	m3 := hmac.New(sha512.New384, []byte(key))
	m3.Write([]byte(msg))
	m5 := hmac.New(sha512.New, []byte(key))
	m5.Write([]byte(msg))
	k := make([]byte, 32)
	for i := range k {
		k[i] = byte(i*7 + 1)
	}
	nonce := make([]byte, 12)
	nonce[3] = 9
	sealed := cr2Seal(k, nonce, []byte(key)[:5], []byte(msg)[:45])
	prk, _ := hkdf.Extract(sha512.New384, []byte(msg), []byte(key)[:9])
	okm, _ := hkdf.Expand(sha512.New384, prk, key, 77)
	vals := []string{hex.EncodeToString(m3.Sum(nil)), hex.EncodeToString(m5.Sum(nil)), hex.EncodeToString(sealed), hex.EncodeToString(okm)}
	want := strings.Join(append(vals, vals...), "\n") + "\n"
	src := cryptoPrelude + strings.TrimPrefix(cr2EvalProgram, "import std.crypto\n")
	for _, v := range cr2Variants {
		dir, err := packageTempDir("mkcrypto2eval")
		if err != nil {
			t.Fatal(err)
		}
		exe := cr2BuildIn(t, dir, v, src, nil, nil)
		r := runNative(t, exe, "")
		if r.code != 0 {
			t.Fatalf("%s: exit %d\n%s", v.name, r.code, r.stderr)
		}
		cryptoCompare(t, v.name, strings.ReplaceAll(r.stdout, "\r\n", "\n"), want)
	}
}

// cr2WipeFixture exports AesGcm's key material to the test program.
const cr2WipeFixture = `// Fixture of the std-crypto2 tests: AesGcm's fields, exported.

export FxAesGcmBytes(g: &AesGcm): [560]u8 {
    var out: [560]u8 = [0; 560]
    (mut out[0..240]).CopyFrom(g.rk[..])
    (mut out[240..480]).CopyFrom(g.sk[..])
    (mut out[480..496]).CopyFrom(g.h[..])
    (mut out[496..560]).CopyFrom(g.powers[..])
    return out
}

// FxPrkBytes is a Prk's key bytes.
export FxPrkBytes(p: &Prk): [64]u8 {
    return p.key
}

// FxGcmMax is Seal's and Open's length limit.
export FxGcmMax(): int {
    return GcmMax
}
`

// TestSTDCRYPTO_WipeClearsKeyMaterial: after AesGcm.Wipe and Prk.Wipe every byte of
// the round keys, H and its table is zero (and was not before), for both
// key sizes, and that GcmMax is SP 800-38D's limit; crypto.Wipe clears every length from 0 to 70 at every offset
// from 0 to 9 and nothing outside its slice. Dev and release.
func TestSTDCRYPTO_WipeClearsKeyMaterial(t *testing.T) {
	t.Parallel()
	src := `import std.crypto

Count(b: []u8): int {
    var n := 0
    for i in 0..b.Len() {
        if b[i] != 0 {
            n += 1
        }
    }
    return n
}

export Main(sys: &System): WriteError|crypto.Error!void {
    var out := sys.Stdout()
    var key: [32]u8 = [0; 32]
    for i in 0..32 {
        key[i] = Trunc[u8](i + 1)
    }
    for k in 1..3 {
        kl := 16 * k
        var g := try crypto.AesGcm.New(key[0..kl])
        before := crypto.FxAesGcmBytes(&g)
        g.Wipe()
        after := crypto.FxAesGcmBytes(&g)
        b := Count(before[0..496])
        a := Count(after[..])
        try Print(mut out, "{kl} {b} {a}\n", (kl: kl, b: b > 300, a: a))
    }
    var bad := 0
    for n in 0..71 {
        for off in 0..10 {
            var buf: [96]u8 = [0xEE; 96]
            crypto.Wipe(mut buf[off..off + n])
            for i in 0..96 {
                inside := i >= off && i < off + n
                if (inside && buf[i] != 0) || (!inside && buf[i] != 0xEE) {
                    bad += 1
                }
            }
        }
    }
    try Print(mut out, "bad {bad}\n", (bad: bad))
    try Print(mut out, "max {m}\n", (m: crypto.FxGcmMax()))
    var prk := crypto.HkdfExtract(key[0..7], key[..], crypto.HashKind.Sha512)
    pb := crypto.FxPrkBytes(&prk)
    prk.Wipe()
    pa := crypto.FxPrkBytes(&prk)
    try Print(mut out, "prk {b} {a}\n", (b: Count(pb[..]) > 50, a: Count(pa[..])))
}
`
	// GcmMax: 2^32 - 2 counter blocks of 16 bytes (SP 800-38D 5.2.1.1).
	want := fmt.Sprintf("16 true 0\n32 true 0\nbad 0\nmax %d\nprk true 0\n", (1<<32-2)*16)
	for _, v := range cr2Variants[:2] {
		dir, err := packageTempDir("mkcrypto2wipe")
		if err != nil {
			t.Fatal(err)
		}
		exe := cr2BuildIn(t, dir, v, src, map[string]string{"crypto/tests_crypto2fx.mk": cr2WipeFixture}, nil)
		r := runNative(t, exe, "")
		if r.code != 0 {
			t.Fatalf("%s: exit %d\n%s", v.name, r.code, r.stderr)
		}
		cryptoCompare(t, v.name, strings.ReplaceAll(r.stdout, "\r\n", "\n"), want)
	}
}

// TestSTDCRYPTO_WipeRefusedAtCompileTime: a constant that reaches
// crypto.Wipe or AesGcm.Wipe is refused (E0901), never folded to bytes
// that were not cleared.
func TestSTDCRYPTO_WipeRefusedAtCompileTime(t *testing.T) {
	t.Parallel()
	for i, body := range []string{
		"var b: [8]u8 = [1; 8]\n    crypto.Wipe(mut b[..])\n    return b[0]",
		"var key: [16]u8 = [1; 16]\n    var g := crypto.AesGcm.New(key[..]) else { return 1 }\n    g.Wipe()\n    return 0",
		"var key: [16]u8 = [1; 16]\n    var p := crypto.HkdfExtract(key[..], key[..], crypto.HashKind.Sha256)\n    p.Wipe()\n    return 0",
	} {
		src := fmt.Sprintf("import std.crypto\n\nF(): u8 {\n    %s\n}\n\nconst X: u8 = F()\n\nexport Main(sys: &System): WriteError!void {\n    var out := sys.Stdout()\n    try Print(mut out, \"{x}\\n\", (x: X))\n}\n", body)
		root := t.TempDir()
		stdDir := cryptoStdDir(t, root, nil)
		if err := os.WriteFile(filepath.Join(root, "main.mk"), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
		out, status := mkCmd(t, root, stdDir, "check", "main.mk")
		if status == 0 || !strings.Contains(out, "E0901") {
			t.Errorf("case %d: mk check exit %d, want E0901:\n%s", i, status, out)
		}
	}
}
