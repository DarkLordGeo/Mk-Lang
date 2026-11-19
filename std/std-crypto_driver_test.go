// Why: AES-GCM, HMAC and HKDF are held to Go's crypto packages on every build
// and path, through one long-running Mk process rather than a build per case.
// What: the oracle driver cr2Driver, its four variants cr2Variants, cr2Ask to
// run commands and cr2Want, Go's answer to each command.
// How: the driver reads commands on stdin and answers each on a line; the
// masked variants force the portable path with s8AesOff (simd_aes_test.go).

package std

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"hash"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// cr2Driver is the oracle driver, built over a copy of std (MK_CRYPTO_BASE
// as the std-crypto harness takes it). Commands, fields separated by one
// space, byte fields lower-case hex (possibly empty), numbers decimal:
//
//	G key nonce aad plain     -> G <sealed> <tail> | G <error>
//	O key nonce aad sealed    -> O ok <plain> <tail> | O <error> <clean>
//	M bits key data           -> M <mac>                (bits 256, 384, 512)
//	K bits salt ikm info n    -> K <okm> | K <error> <clean>
//	S key nonce aad plain sealed -> S <seal error> <clean> <open error> <clean>
//	                            (out one byte short for each; plain at least 1 byte)
//
// Seal's out is plain + 16 + 3 bytes of 0xa5 and <tail> says whether the 3
// bytes past the count Seal returned are still 0xa5; Open's out is
// sealed - 16 + 2 bytes (or 2) of 0xa5, and <clean> says whether all of
// it is still 0xa5 after a failure. HKDF's out is n bytes of 0xa5.
const cr2Driver = cryptoPrelude + `

// Take is the next field of sp, or "" past the last.
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

Filled(a: &Arena, n: int): OutOfMemory!mut []u8 from a {
    out := try a.Slice[u8](n)
    for i in 0..n {
        out[i] = 165
    }
    return out
}

DoSeal(w: mut Writer, a: &Arena, sp: mut Split): WriteError|OutOfMemory!void {
    key := try Bytes(a, Take(sp))
    nonce := Arr12(try Bytes(a, Take(sp)))
    aad := try Bytes(a, Take(sp))
    plain := try Bytes(a, Take(sp))
    g := crypto.AesGcm.New(key) else e {
        try Print(w, "G {e}\n", (e: Name(e)))
        return
    }
    out := try Filled(a, plain.Len() + 19)
    n := g.Seal(&nonce, aad, plain, mut out[..]) else e {
        try Print(w, "G {e}\n", (e: Name(e)))
        return
    }
    try Print(w, "G ")
    try Hex(w, out[0..n])
    tail := All(out[n..out.Len()], 165)
    try Print(w, " {tail}\n", (tail: tail))
}

DoOpen(w: mut Writer, a: &Arena, sp: mut Split): WriteError|OutOfMemory!void {
    key := try Bytes(a, Take(sp))
    nonce := Arr12(try Bytes(a, Take(sp)))
    aad := try Bytes(a, Take(sp))
    sealed := try Bytes(a, Take(sp))
    g := crypto.AesGcm.New(key) else e {
        try Print(w, "O {e} true\n", (e: Name(e)))
        return
    }
    var m := sealed.Len() - 16
    if m < 0 {
        m = 0
    }
    out := try Filled(a, m + 2)
    n := g.Open(&nonce, aad, sealed, mut out[..]) else e {
        clean := All(out[..], 165)
        try Print(w, "O {e} {clean}\n", (e: Name(e), clean: clean))
        return
    }
    try Print(w, "O ok ")
    try Hex(w, out[0..n])
    tail := All(out[n..out.Len()], 165)
    try Print(w, " {tail}\n", (tail: tail))
}

DoMac(w: mut Writer, a: &Arena, sp: mut Split): WriteError|OutOfMemory!void {
    bits := Num(Take(sp))
    key := try Bytes(a, Take(sp))
    data := try Bytes(a, Take(sp))
    try Print(w, "M ")
    if bits == 256 {
        m := crypto.Hmac256(key, data)
        try Hex(w, m[..])
    } else if bits == 384 {
        m := crypto.Hmac384(key, data)
        try Hex(w, m[..])
    } else {
        m := crypto.Hmac512(key, data)
        try Hex(w, m[..])
    }
    try Print(w, "\n")
}

DoHkdf(w: mut Writer, a: &Arena, sp: mut Split): WriteError|OutOfMemory!void {
    bits := Num(Take(sp))
    h := if bits == 256 { crypto.HashKind.Sha256 } else if bits == 384 { crypto.HashKind.Sha384 } else { crypto.HashKind.Sha512 }
    salt := try Bytes(a, Take(sp))
    ikm := try Bytes(a, Take(sp))
    info := try Bytes(a, Take(sp))
    out := try Filled(a, Num(Take(sp)))
    prk := crypto.HkdfExtract(salt, ikm, h)
    crypto.HkdfExpand(&prk, info, mut out[..]) else e {
        clean := All(out[..], 165)
        try Print(w, "K {e} {clean}\n", (e: Name(e), clean: clean))
        return
    }
    try Print(w, "K ")
    try Hex(w, out[..])
    try Print(w, "\n")
}

// DoShort seals plain into an out one byte short, then opens Go's sealed
// message (the last field) into an out one byte short, when it has a byte.
DoShort(w: mut Writer, a: &Arena, sp: mut Split): WriteError|OutOfMemory!void {
    key := try Bytes(a, Take(sp))
    nonce := Arr12(try Bytes(a, Take(sp)))
    aad := try Bytes(a, Take(sp))
    plain := try Bytes(a, Take(sp))
    sealed := try Bytes(a, Take(sp))
    g := crypto.AesGcm.New(key) else e {
        try Print(w, "S {e}\n", (e: Name(e)))
        return
    }
    out := try Filled(a, plain.Len() + 15)
    _ = g.Seal(&nonce, aad, plain, mut out[..]) else e {
        clean := All(out[..], 165)
        back := try Filled(a, plain.Len() - 1)
        _ = g.Open(&nonce, aad, sealed, mut back[..]) else e2 {
            clean2 := All(back[..], 165)
            try Print(w, "S {e} {clean} {e2} {clean2}\n", (e: Name(e), clean: clean, e2: Name(e2), clean2: clean2))
            return
        }
        try Print(w, "S {e} {clean} ok\n", (e: Name(e), clean: clean))
        return
    }
    try Print(w, "S sealed\n")
}

Do(w: mut Writer, a: &Arena, line: str): WriteError|OutOfMemory!void {
    var sp := line.Split(" ")
    c := Take(mut sp)
    if c == "G" {
        try DoSeal(w, a, mut sp)
    } else if c == "O" {
        try DoOpen(w, a, mut sp)
    } else if c == "M" {
        try DoMac(w, a, mut sp)
    } else if c == "K" {
        try DoHkdf(w, a, mut sp)
    } else if c == "S" {
        try DoShort(w, a, mut sp)
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

// cr2Variant is one build of the driver: its mk build flags and whether
// the AES and GHASH kernels are masked.
type cr2Variant struct {
	name   string
	flags  []string
	masked bool
}

var cr2Variants = []cr2Variant{
	{"dev", nil, false},
	{"release", []string{"--release"}, false},
	{"dev-portable", nil, true},
	{"release-portable", []string{"--release"}, true},
}

// cr2Exes holds each variant's driver, built once per test binary.
var cr2Exes struct {
	sync.Mutex
	m map[string]string
}

// cr2Fixtures is the std copy's overlay for v: s8AesOff's mask when v masks.
func cr2Fixtures(t *testing.T, v cr2Variant, extra map[string]string) map[string]string {
	t.Helper()
	fx := map[string]string{}
	if v.masked {
		maps.Copy(fx, sdFixtures(t, s8AesOff))
	}
	maps.Copy(fx, extra)
	return fx
}

// cr2Exe builds the driver for v (once).
func cr2Exe(t *testing.T, v cr2Variant) string {
	t.Helper()
	cr2Exes.Lock()
	defer cr2Exes.Unlock()
	if exe, ok := cr2Exes.m[v.name]; ok {
		return exe
	}
	dir, err := packageTempDir("mkcrypto2")
	if err != nil {
		t.Fatal(err)
	}
	return cr2BuildIn(t, dir, v, cr2Driver, nil, &cr2Exes.m)
}

// cr2BuildIn builds src for v in dir and records it in cache when cache
// is not nil.
func cr2BuildIn(t *testing.T, dir string, v cr2Variant, src string, extra map[string]string, cache *map[string]string) string {
	t.Helper()
	stdDir := cryptoStdDir(t, dir, cr2Fixtures(t, v, extra))
	if err := os.WriteFile(filepath.Join(dir, "main.mk"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(dir, "main")
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	args := append([]string{"build"}, v.flags...)
	args = append(args, "-o", exe, "main.mk")
	out, status := mkCmd(t, dir, stdDir, args...)
	if status != 0 {
		if strings.Contains(out, "no usable LLVM") {
			t.Skipf("needs the LLVM toolchain: %s", out)
		}
		t.Fatalf("mk build %s: exit %d\n%s", v.name, status, out)
	}
	if cache != nil {
		if *cache == nil {
			*cache = map[string]string{}
		}
		(*cache)[v.name] = exe
	}
	return exe
}

// cr2Ask runs v's driver over lines and returns its answers, one a line.
func cr2Ask(t *testing.T, v cr2Variant, lines []string) []string {
	t.Helper()
	exe := cr2Exe(t, v)
	r := runNative(t, exe, strings.Join(lines, "\n")+"\n")
	if r.code != 0 {
		t.Fatalf("%s driver exit %d\nstderr: %s\nstdout (tail): %s", v.name, r.code, r.stderr, cryptoTail(r.stdout, 2000))
	}
	got := strings.Split(strings.TrimRight(strings.ReplaceAll(r.stdout, "\r\n", "\n"), "\n"), "\n")
	if len(got) != len(lines) {
		t.Fatalf("%s driver: %d answers to %d commands\nstderr: %s\nstdout (tail): %s", v.name, len(got), len(lines), r.stderr, cryptoTail(r.stdout, 2000))
	}
	return got
}

// cr2Check runs lines on every variant and compares each answer with
// want's, reporting at most 10 differences per variant.
func cr2Check(t *testing.T, what string, lines []string) {
	t.Helper()
	want := make([]string, len(lines))
	for i, l := range lines {
		want[i] = cr2Want(l)
	}
	for _, v := range cr2Variants {
		got := cr2Ask(t, v, lines)
		bad := 0
		for i := range lines {
			if got[i] != want[i] {
				t.Errorf("%s %s: %s\n got  %s\n want %s", what, v.name, cryptoTail(lines[i], 300), cryptoTail(got[i], 300), cryptoTail(want[i], 300))
				bad++
				if bad == 10 {
					t.Fatalf("%s %s: stopping after 10 differences", what, v.name)
				}
			}
		}
	}
}

func cr2X(b []byte) string { return hex.EncodeToString(b) }

func cr2Hash(bits int) func() hash.Hash {
	switch bits {
	case 256:
		return sha256.New
	case 384:
		return sha512.New384
	}
	return sha512.New
}

// cr2Want is Go's answer to one driver command.
func cr2Want(line string) string {
	f := strings.Split(line, " ")
	b := func(i int) []byte {
		x, err := hex.DecodeString(f[i])
		if err != nil {
			panic("cr2Want: bad hex in " + line)
		}
		return x
	}
	switch f[0] {
	case "G":
		key, nonce, aad, plain := b(1), b(2), b(3), b(4)
		if len(key) != 16 && len(key) != 32 {
			return "G bad-length"
		}
		c, _ := aes.NewCipher(key)
		g, _ := cipher.NewGCM(c)
		return "G " + cr2X(g.Seal(nil, nonce, plain, aad)) + " true"
	case "O":
		key, nonce, aad, sealed := b(1), b(2), b(3), b(4)
		if len(key) != 16 && len(key) != 32 {
			return "O bad-length true"
		}
		if len(sealed) < 16 {
			return "O bad-length true"
		}
		c, _ := aes.NewCipher(key)
		g, _ := cipher.NewGCM(c)
		p, err := g.Open(nil, nonce, sealed, aad)
		if err != nil {
			return "O bad-tag true"
		}
		return "O ok " + cr2X(p) + " true"
	case "S":
		if len(b(1)) != 16 && len(b(1)) != 32 {
			return "S bad-length"
		}
		return "S bad-length true bad-length true"
	case "M":
		bits, _ := strconv.Atoi(f[1])
		m := hmac.New(cr2Hash(bits), b(2))
		m.Write(b(3))
		return "M " + cr2X(m.Sum(nil))
	case "K":
		bits, _ := strconv.Atoi(f[1])
		n, _ := strconv.Atoi(f[5])
		h := cr2Hash(bits)
		if n > 255*h().Size() {
			return "K bad-length true"
		}
		prk, err := hkdf.Extract(h, b(3), b(2))
		if err != nil {
			panic(err)
		}
		if n == 0 {
			return "K "
		}
		okm, err := hkdf.Expand(h, prk, string(b(4)), n)
		if err != nil {
			panic(err)
		}
		return "K " + cr2X(okm)
	}
	return "? " + f[0]
}
