// Why: every std.crypto primitive must survive any input and agree with a Go
// reference.
// What: FuzzSTDCRYPTO_Primitives and TestSTDCRYPTO_FuzzOracleMatchesReference.
// How: each input is one command to a long-running dev-build oracle; a panic,
// a hang or a different answer fails the input.

package std

import (
	"bufio"
	"bytes"
	"crypto/ed25519"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

// cryptoFuzzMain reads one command per line, fields separated by one
// space, each field lower-case hex (possibly empty), and prints one
// answer line per command, flushed:
//
//	O key nonce aad sealed      -> O ok plain | O bad-tag clean | O bad-length clean
//	S key nonce aad plain flip  -> S sealed roundtrip verdict clean
//	V public sig msg            -> V bool
//	G seed msg flip             -> G public sig good bad
//	X secret point              -> X out
//	E point                     -> E point | E none
//	H key data                  -> H mac
//
// Open's out starts as 0xa5 bytes, and "clean" says they are all still
// 0xa5 after a failure. Seal's answer is the sealed message, whether Open
// gives the plaintext back, Open's verdict once bit flip (mod the sealed
// length in bits) is flipped, and whether out still holds the plaintext
// after that failure. G signs msg under FromSeed(seed), verifies it
// (good), flips bit flip mod 768 of sig || public, and verifies again
// (bad).
const cryptoFuzzMain = cryptoPrelude + `
// FieldEnd is the index of the first space at or after i in b, or b.Len().
FieldEnd(b: []u8, i: int): int {
    var j := i
    for j < b.Len() && b[j] != 32 {
        j += 1
    }
    return j
}

Field(a: &Arena, s: str): OutOfMemory![]u8 from a {
    out := try a.Slice[u8](s.Len() / 2)
    Unhex(s, mut out[..])
    return out[..]
}

Arr64(b: []u8): [64]u8 {
    var k: [64]u8 = [0; 64]
    for i in 0..64 {
        k[i] = b[i]
    }
    return k
}

Bit(f: []u8): int {
    return int(f[0]) | (int(f[1]) << 8)
}

Flip(b: mut []u8, bit: int) {
    var one: u8 = 1
    b[bit >> 3] = b[bit >> 3] ^ (one << (bit & 7))
}

Verdict(w: mut Writer, e: crypto.Error): WriteError!void {
    match e {
        crypto.BadTag {
            try Print(w, "bad-tag")
        }
        crypto.BadLength {
            try Print(w, "bad-length")
        }
        _ {
            try Print(w, "other")
        }
    }
}

// All reports whether every byte of b is x.
All(b: []u8, x: u8): bool {
    var ok := true
    for i in 0..b.Len() {
        if b[i] != x {
            ok = false
        }
    }
    return ok
}

DoOpen(w: mut Writer, a: &Arena, line: str): WriteError|OutOfMemory!void {
    b := line.AsBytes()
    e1 := FieldEnd(b, 2)
    key := Arr32(try Field(a, line[2..e1]))
    e2 := FieldEnd(b, e1 + 1)
    nonce := Arr12(try Field(a, line[e1 + 1..e2]))
    e3 := FieldEnd(b, e2 + 1)
    aad := try Field(a, line[e2 + 1..e3])
    sealed := try Field(a, line[e3 + 1..b.Len()])
    var m := sealed.Len() - 16
    if m < 0 {
        m = 0
    }
    out := try a.Slice[u8](m)
    for i in 0..m {
        out[i] = 165
    }
    crypto.ChaCha20Poly1305.Open(&key, &nonce, aad, sealed, mut out[..]) else e {
        try Print(w, "O ")
        try Verdict(w, e)
        clean := All(out[..], 165)
        try Print(w, " {clean}\n", (clean: clean))
        return
    }
    try Print(w, "O ok ")
    try Hex(w, out[..])
    try Print(w, "\n")
}

DoSeal(w: mut Writer, a: &Arena, line: str): WriteError|OutOfMemory!void {
    b := line.AsBytes()
    e1 := FieldEnd(b, 2)
    key := Arr32(try Field(a, line[2..e1]))
    e2 := FieldEnd(b, e1 + 1)
    nonce := Arr12(try Field(a, line[e1 + 1..e2]))
    e3 := FieldEnd(b, e2 + 1)
    aad := try Field(a, line[e2 + 1..e3])
    e4 := FieldEnd(b, e3 + 1)
    plain := try Field(a, line[e3 + 1..e4])
    fl := try Field(a, line[e4 + 1..b.Len()])
    sealed := try a.Slice[u8](plain.Len() + 16)
    crypto.ChaCha20Poly1305.Seal(&key, &nonce, aad, plain, mut sealed[..]) else e {
        try Print(w, "S ")
        try Verdict(w, e)
        try Print(w, "\n")
        return
    }
    try Print(w, "S ")
    try Hex(w, sealed[..])
    back := try a.Slice[u8](plain.Len())
    var rt := true
    crypto.ChaCha20Poly1305.Open(&key, &nonce, aad, sealed[..], mut back[..]) else {
        rt = false
    }
    rt = rt && crypto.Equal(back[..], plain)
    Flip(mut sealed[..], Bit(fl) % (8 * sealed.Len()))
    crypto.ChaCha20Poly1305.Open(&key, &nonce, aad, sealed[..], mut back[..]) else e {
        try Print(w, " {rt} ", (rt: rt))
        try Verdict(w, e)
        clean := crypto.Equal(back[..], plain)
        try Print(w, " {clean}\n", (clean: clean))
        return
    }
    try Print(w, " {rt} ok false\n", (rt: rt))
}

DoVerify(w: mut Writer, a: &Arena, line: str): WriteError|OutOfMemory!void {
    b := line.AsBytes()
    e1 := FieldEnd(b, 2)
    pk := Arr32(try Field(a, line[2..e1]))
    e2 := FieldEnd(b, e1 + 1)
    sig := Arr64(try Field(a, line[e1 + 1..e2]))
    msg := try Field(a, line[e2 + 1..b.Len()])
    ok := crypto.Ed25519.Verify(&pk, msg, &sig)
    try Print(w, "V {ok}\n", (ok: ok))
}

DoSign(w: mut Writer, a: &Arena, line: str): WriteError|OutOfMemory!void {
    b := line.AsBytes()
    e1 := FieldEnd(b, 2)
    seed := Arr32(try Field(a, line[2..e1]))
    e2 := FieldEnd(b, e1 + 1)
    msg := try Field(a, line[e1 + 1..e2])
    fl := try Field(a, line[e2 + 1..b.Len()])
    kp := crypto.Ed25519.FromSeed(&seed)
    var sig := crypto.Ed25519.Sign(&kp, msg)
    var pk: [32]u8 = [0; 32]
    for i in 0..32 {
        pk[i] = kp[32 + i]
    }
    try Print(w, "G ")
    try Hex(w, pk[..])
    try Print(w, " ")
    try Hex(w, sig[..])
    good := crypto.Ed25519.Verify(&pk, msg, &sig)
    bit := Bit(fl) % 768
    if bit < 512 {
        Flip(mut sig[..], bit)
    } else {
        Flip(mut pk[..], bit - 512)
    }
    bad := crypto.Ed25519.Verify(&pk, msg, &sig)
    try Print(w, " {good} {bad}\n", (good: good, bad: bad))
}

DoX25519(w: mut Writer, a: &Arena, line: str): WriteError|OutOfMemory!void {
    b := line.AsBytes()
    e1 := FieldEnd(b, 2)
    k := Arr32(try Field(a, line[2..e1]))
    u := Arr32(try Field(a, line[e1 + 1..b.Len()]))
    r := crypto.X25519(&k, &u)
    try Print(w, "X ")
    try Hex(w, r[..])
    try Print(w, "\n")
}

DoDecode(w: mut Writer, a: &Arena, line: str): WriteError|OutOfMemory!void {
    p := Arr32(try Field(a, line[2..line.Len()]))
    v := crypto.FxDecode(&p) else {
        try Print(w, "E none\n")
        return
    }
    try Print(w, "E ")
    try Hex(w, v[..])
    try Print(w, "\n")
}

DoHmac(w: mut Writer, a: &Arena, line: str): WriteError|OutOfMemory!void {
    b := line.AsBytes()
    e1 := FieldEnd(b, 2)
    key := try Field(a, line[2..e1])
    data := try Field(a, line[e1 + 1..b.Len()])
    m := crypto.Hmac256(key, data)
    try Print(w, "H ")
    try Hex(w, m[..])
    try Print(w, "\n")
}

Do(w: mut Writer, a: &Arena, line: str): WriteError|OutOfMemory!void {
    if line.Len() < 2 {
        try Print(w, "?\n")
        return
    }
    c := line.AsBytes()[0]
    if c == 79 {
        try DoOpen(w, a, line)
    } else if c == 83 {
        try DoSeal(w, a, line)
    } else if c == 86 {
        try DoVerify(w, a, line)
    } else if c == 71 {
        try DoSign(w, a, line)
    } else if c == 88 {
        try DoX25519(w, a, line)
    } else if c == 69 {
        try DoDecode(w, a, line)
    } else if c == 72 {
        try DoHmac(w, a, line)
    } else {
        try Print(w, "?\n")
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

// cfExes holds the oracle built once per test binary and mode ("" for a
// dev build, "--release"), outside t.TempDir, since the running process
// outlives the test (or fuzz input) that started it.
var cfExes struct {
	sync.Mutex
	m map[string]string
}

func cfOracleExe(t *testing.T, mode string) string {
	t.Helper()
	cfExes.Lock()
	defer cfExes.Unlock()
	if exe, ok := cfExes.m[mode]; ok {
		return exe
	}
	dir, err := packageTempDir("mkcrypto")
	if err != nil {
		t.Fatal(err)
	}
	stdDir := cryptoStdDir(t, dir, nil)
	if err := os.WriteFile(filepath.Join(dir, "main.mk"), []byte(cryptoFuzzMain), 0o644); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(dir, "main")
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	args := []string{"build"}
	if mode != "" {
		args = append(args, mode)
	}
	args = append(args, "-o", exe, "main.mk")
	out, status := mkCmd(t, dir, stdDir, args...)
	if status != 0 {
		if strings.Contains(out, "no usable LLVM") {
			t.Skipf("needs the LLVM toolchain: %s", out)
		}
		t.Fatalf("mk build %s: exit %d\n%s", mode, status, out)
	}
	if cfExes.m == nil {
		cfExes.m = map[string]string{}
	}
	cfExes.m[mode] = exe
	return exe
}

// cfServer is a running oracle with its pipes.
type cfServer struct {
	cmd  *exec.Cmd
	in   io.WriteCloser
	out  *bufio.Reader
	errb *bytes.Buffer
}

var cfSrv struct {
	sync.Mutex
	m map[string]*cfServer
}

func cfStart(t *testing.T, mode string) *cfServer {
	t.Helper()
	cmd := exec.Command(cfOracleExe(t, mode))
	in, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var errb bytes.Buffer
	cmd.Stderr = &errb
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	return &cfServer{cmd: cmd, in: in, out: bufio.NewReader(out), errb: &errb}
}

// cfAsk sends lines to the mode's running oracle (starting one if needed)
// and returns its answers, one per line. The lines are written from a
// goroutine, so a long batch cannot fill both pipes. A dead oracle fails
// the test with its stderr, and the next call starts a new one.
func cfAsk(t *testing.T, mode string, lines []string) []string {
	t.Helper()
	cfSrv.Lock()
	defer cfSrv.Unlock()
	if cfSrv.m == nil {
		cfSrv.m = map[string]*cfServer{}
	}
	s := cfSrv.m[mode]
	if s == nil {
		s = cfStart(t, mode)
		cfSrv.m[mode] = s
	}
	werr := make(chan error, 1)
	go func() {
		_, err := io.WriteString(s.in, strings.Join(lines, "\n")+"\n")
		werr <- err
	}()
	res := make([]string, 0, len(lines))
	for range lines {
		l, err := s.out.ReadString('\n')
		if err != nil {
			delete(cfSrv.m, mode)
			s.in.Close()
			s.cmd.Wait()
			t.Fatalf("oracle (%q) died after %d answers: %v\nstderr:\n%s\nlast command: %.300s", mode, len(res), err, s.errb.String(), lines[len(res)])
		}
		res = append(res, strings.TrimRight(l, "\r\n"))
	}
	if err := <-werr; err != nil {
		t.Fatalf("oracle (%q): write: %v", mode, err)
	}
	return res
}

// cfTake is a cursor over a fuzz input: fixed fields are padded with
// zeros past the end, so every input decodes.
type cfTake struct{ b []byte }

func (c *cfTake) n(k int) []byte {
	out := make([]byte, k)
	m := copy(out, c.b)
	c.b = c.b[m:]
	return out
}

// v is a field whose length is the next byte (cut to what is left).
func (c *cfTake) v() []byte {
	k := int(c.n(1)[0])
	k = min(k, len(c.b))
	out := append([]byte(nil), c.b[:k]...)
	c.b = c.b[k:]
	return out
}

func (c *cfTake) rest() []byte {
	out := append([]byte(nil), c.b...)
	c.b = nil
	return out
}

// cfCase is one oracle command and the reference's answer.
type cfCase struct{ line, want string }

func cfX(b []byte) string { return hex.EncodeToString(b) }

func cfFlip(b []byte, bit int) {
	b[bit>>3] ^= 1 << (bit & 7)
}

// cfDecode turns a fuzz input into a command: the first byte picks the
// operation (mod 7), the rest are its fields, in the order of
// cryptoFuzzMain's table.
func cfDecode(t *testing.T, data []byte) cfCase {
	if len(data) == 0 {
		data = []byte{0}
	}
	c := &cfTake{b: data[1:]}
	switch data[0] % 7 {
	case 0:
		key, nonce, aad, sealed := c.n(32), c.n(12), c.v(), c.rest()
		return cfCase{fmt.Sprintf("O %s %s %s %s", cfX(key), cfX(nonce), cfX(aad), cfX(sealed)), cfOpenWant(key, nonce, aad, sealed)}
	case 1:
		key, nonce, fl, aad, plain := c.n(32), c.n(12), c.n(2), c.v(), c.rest()
		sealed := aeadModelSeal(key, nonce, aad, plain)
		return cfCase{fmt.Sprintf("S %s %s %s %s %s", cfX(key), cfX(nonce), cfX(aad), cfX(plain), cfX(fl)), "S " + cfX(sealed) + " true bad-tag true"}
	case 2:
		pub, sig, msg := c.n(32), c.n(64), c.rest()
		return cfCase{fmt.Sprintf("V %s %s %s", cfX(pub), cfX(sig), cfX(msg)), fmt.Sprintf("V %t", ed25519.Verify(pub, msg, sig))}
	case 3:
		seed, fl, msg := c.n(32), c.n(2), c.rest()
		k := ed25519.NewKeyFromSeed(seed)
		pub := append([]byte(nil), k[32:]...)
		sig := ed25519.Sign(k, msg)
		want := "G " + cfX(pub) + " " + cfX(sig)
		bit := int(binary.LittleEndian.Uint16(fl)) % 768
		if bit < 512 {
			cfFlip(sig, bit)
		} else {
			cfFlip(pub, bit-512)
		}
		want += fmt.Sprintf(" true %t", ed25519.Verify(pub, msg, sig))
		return cfCase{fmt.Sprintf("G %s %s %s", cfX(seed), cfX(msg), cfX(fl)), want}
	case 4:
		k, u := c.n(32), c.n(32)
		want, ok := goX25519(t, k, u)
		if !ok {
			want = strings.Repeat("00", 32)
		}
		return cfCase{fmt.Sprintf("X %s %s", cfX(k), cfX(u)), "X " + want}
	case 5:
		p := c.n(32)
		want := "E none"
		if pt, ok := edDecode(p); ok {
			want = "E " + cfX(edEncode(pt))
		}
		return cfCase{"E " + cfX(p), want}
	default:
		key, msg := c.v(), c.rest()
		return cfCase{fmt.Sprintf("H %s %s", cfX(key), cfX(msg)), "H " + cfX(goHmac(key, msg))}
	}
}

// cfOpenWant is RFC 8439 2.8's Open by the model: the tag over aad and
// the ciphertext is recomputed by sealing the decrypted text again.
func cfOpenWant(key, nonce, aad, sealed []byte) string {
	if len(sealed) < 16 {
		return "O bad-length true"
	}
	plain := ccModelXor(key, nonce, 1, sealed[:len(sealed)-16])
	if !bytes.Equal(aeadModelSeal(key, nonce, aad, plain), sealed) {
		return "O bad-tag true"
	}
	return "O ok " + cfX(plain)
}

// cfSeeds are fuzz inputs that reach every success path: a real sealed
// message (RFC 8439 2.8.2's), real signatures, RFC 7748's first vector,
// the base point, and RFC 4231 case 2.
func cfSeeds(t testing.TB) [][]byte {
	un := func(s string) []byte {
		b, err := hex.DecodeString(s)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	cat := func(parts ...[]byte) []byte {
		var b []byte
		for _, p := range parts {
			b = append(b, p...)
		}
		return b
	}
	key := un(rfcKey80)
	nonce := un(rfc8439Aead.nonce)
	aad := un(rfc8439Aead.aad)
	sealed := un(rfc8439Aead.want)
	seed := un("9d61b19deffd5a60ba844af492ec2cc44449c5697b326919703bac031cae7f60")
	k := ed25519.NewKeyFromSeed(seed)
	msg := []byte("fuzz me")
	sig := ed25519.Sign(k, msg)
	seeds := [][]byte{
		cat([]byte{0}, key, nonce, []byte{byte(len(aad))}, aad, sealed),
		cat([]byte{0}, key, nonce, []byte{0}),
		cat([]byte{1}, key, nonce, []byte{7, 1, byte(len(aad))}, aad, []byte(sunscreen)),
		cat([]byte{1}, key, nonce, []byte{0, 0, 0}),
		cat([]byte{2}, k[32:], sig, msg),
		cat([]byte{2}, k[32:], sig[:63], []byte{sig[63] ^ 0x10}, msg),
		cat([]byte{3}, seed, []byte{0, 2}, msg),
		cat([]byte{3}, seed, []byte{255, 3}),
		cat([]byte{4}, un(x25519Vectors[0].k), un(x25519Vectors[0].u)),
		cat([]byte{4}, un(x25519Vectors[0].k), un(x25519LowOrder[0])),
		cat([]byte{5}, un("5866666666666666666666666666666666666666666666666666666666666666")),
		cat([]byte{5}, un("0200000000000000000000000000000000000000000000000000000000000000")),
		cat([]byte{6, 4}, []byte("Jefe"), []byte("what do ya want for nothing?")),
		cat([]byte{6, 200}, bytes.Repeat([]byte{0xaa}, 131)),
	}
	return seeds
}

// FuzzSTDCRYPTO_Primitives: see the file comment. Without -fuzz it runs
// the seeds, which also check that the oracle answers every operation:
//
//	go test ./std -run '^$' -fuzz FuzzSTDCRYPTO_Primitives -fuzztime 5m -parallel 2
func FuzzSTDCRYPTO_Primitives(f *testing.F) {
	for _, s := range cfSeeds(f) {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		// A field's hex doubles its length, and the oracle reads each
		// command with Reader.Line through stdin's 64 KiB buffer, which
		// fails with ReadError on a longer line: 16 KiB of input keeps the
		// line under it.
		if len(data) > 16<<10 {
			data = data[:16<<10]
		}
		c := cfDecode(t, data)
		got := cfAsk(t, "", []string{c.line})[0]
		if got != c.want {
			t.Errorf("command %.400s\n got  %.400s\n want %.400s", c.line, got, c.want)
		}
	})
}

// TestSTDCRYPTO_FuzzOracleMatchesReference runs the seeds and 700 seeded
// random inputs (100 per operation, lengths 0 to 299) through the oracle
// in dev and release builds, so the fuzz target's oracle and decoder are
// themselves tested without -fuzz.
func TestSTDCRYPTO_FuzzOracleMatchesReference(t *testing.T) {
	inputs := cfSeeds(t)
	r := &cryptoSplitmix{s: 0x5eed}
	for i := 0; i < 700; i++ {
		b := make([]byte, 1+int(r.next()%300))
		r.fill(b)
		b[0] = byte(i % 7)
		inputs = append(inputs, b)
	}
	var cases []cfCase
	var lines []string
	for _, in := range inputs {
		c := cfDecode(t, in)
		cases = append(cases, c)
		lines = append(lines, c.line)
	}
	for _, mode := range []string{"", "--release"} {
		got := cfAsk(t, mode, lines)
		bad := 0
		for i, c := range cases {
			if got[i] != c.want {
				t.Errorf("%q: command %.300s\n got  %.300s\n want %.300s", mode, c.line, got[i], c.want)
				if bad++; bad == 5 {
					t.FailNow()
				}
			}
		}
	}
}
