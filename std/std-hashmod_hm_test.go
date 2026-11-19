// Why: std.hash had defects in its streaming states, CRC-32, compile-time
// paths and short keys that must not come back.
// What: the TestSTDHASHMOD_ tests: zero-filled states refused, CRC-32
// streaming, long compile-time hashes, seeded XXH64 against Zig, short keys.
// How: Mk programs built over a copy of std and judged by Go models, Go's
// hash/crc32 and Zig's output, plus a scan of std/hash and its benchmark
// rows (hashModLaneFiles) for Print with an empty record.

package std

import (
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"hash/crc32"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSTDHASHMOD_ZeroFilledStatesRejected: Sha256State, Sha512State and
// Blake3State carry an invariant, so they are not zero-valid and
// Arena.Slice of one, which zero-fills (a state whose chaining value is not
// the IV), is E0701 at the call.
func TestSTDHASHMOD_ZeroFilledStatesRejected(t *testing.T) {
	for _, st := range []string{"Sha256State", "Sha512State", "Blake3State"} {
		t.Run(st, func(t *testing.T) {
			src := `import std.hash

export Main(sys: &System): WriteError|OutOfMemory!void {
    s := try sys.arena.Slice[hash.` + st + `](1)
    var t := s[0]
    t.Bytes("abc".AsBytes())
    _ = t.Sum()
}
`
			_, out, status := hashModTry(t, src)
			if strings.Contains(out, "no usable LLVM") {
				t.Skipf("needs the LLVM toolchain: %s", out)
			}
			if status == 0 || !strings.Contains(out, "E0701") || !strings.Contains(out, "not zero-valid") {
				t.Fatalf("Arena.Slice[hash.%s] built (exit %d), want E0701 not zero-valid:\n%s", st, status, out)
			}
		})
	}
}

// hashModCrcStream feeds a 1 MiB message to a Crc32State in seeded random
// pieces (0 to 4,099 bytes, empty pieces included), and to a zero-filled
// Crc32State (the type is zero-valid on purpose: its zero is the CRC of the
// empty message) in other pieces, using public names only.
const hashModCrcStream = `import std.hash

struct Rng {
    s: u64
}

Rng.Next(mut self): u64 {
    self.s = self.s +% 0x9E3779B97F4A7C15
    var z := self.s
    z = (z ^ (z >> 30)) *% 0xBF58476D1CE4E5B9
    z = (z ^ (z >> 27)) *% 0x94D049BB133111EB
    return z ^ (z >> 31)
}

Feed(s: mut hash.Crc32State, r: mut Rng, b: []u8, most: u64) {
    var i := 0
    for i < b.Len() {
        var k := Trunc[int](r.Next() % most)
        if k > b.Len() - i {
            k = b.Len() - i
        }
        s.Bytes(b[i..i + k])
        i += k
    }
}

export Main(sys: &System): WriteError|OutOfMemory!void {
    var out := sys.Stdout()
    n := 1048576
    b := try sys.arena.Slice[u8](n)
    var r := Rng(s: 3)
    for i in 0..n {
        b[i] = Trunc[u8](r.Next())
    }
    var s := hash.Crc32State.New()
    e := s.Sum()
    Feed(mut s, mut r, b[..], 4100)
    zs := try sys.arena.Slice[hash.Crc32State](1)
    var z := zs[0]
    Feed(mut z, mut r, b[..], 9)
    var u := hash.Crc32State.New()
    u.Bytes(b[..])
    one := hash.Crc32(b[..])
    a := s.Sum()
    c := z.Sum()
    d := u.Sum()
    try Print(mut out, "{e:x} {a:x} {c:x} {d:x} {one:x}\n", (e: e, a: a, c: c, d: d, one: one))
}
`

// TestSTDHASHMOD_Crc32StateStreams: hash.Crc32State in any split, and a
// zero-filled one, equal Go's crc32.ChecksumIEEE of the whole message.
func TestSTDHASHMOD_Crc32StateStreams(t *testing.T) {
	r := hmSplitmix{3}
	b := make([]byte, 1048576)
	for i := range b {
		b[i] = byte(r.next())
	}
	c := crc32.ChecksumIEEE(b)
	want := fmt.Sprintf("0 %x %x %x %x", c, c, c, c)
	for _, flags := range [][]string{nil, {"-release"}} {
		exe := hashModBuild(t, hashModCrcStream, flags...)
		if got := strings.TrimSpace(hashModExec(t, exe)); got != want {
			t.Fatalf("mk build %v: got %s, want %s", flags, got, want)
		}
	}
}

// hashModLongConst hashes a 4,097-byte message (built at compile time) as a
// const and at run time: XXH3's stripe loop over four 1,024-byte blocks,
// XXH64's stripes, CRC-32's 16-byte steps, SHA-2's blocks, BLAKE3's four full
// chunks, their parent merges at two levels and a fifth chunk, and SipHash.
const hashModLongConst = `import std.hash

Make(): [4097]u8 {
    var b: [4097]u8 = [0; 4097]
    for i in 0..4097 {
        b[i] = Trunc[u8](i * 7 + 3)
    }
    return b
}

const Big: [4097]u8 = Make()
const CX: u64 = hash.Xx(Big[..])
const CX64: u64 = hash.Xx64(Big[..], 0x9E3779B97F4A7C15)
const CC: u32 = hash.Crc32(Big[..])
const CS: [32]u8 = hash.Sha256(Big[..])
const CS5: [64]u8 = hash.Sha512(Big[..])
const CB: [32]u8 = hash.Blake3(Big[..])
const CP: u64 = hash.Sip13(Big[..], hash.TestKey(0x0706050403020100, 0x0f0e0d0c0b0a0908))
const CP16: u64 = hash.Sip13(Big[0..16], hash.TestKey(0x0706050403020100, 0x0f0e0d0c0b0a0908))

Hex(out: mut Writer, b: []u8): WriteError!void {
    for i in 0..b.Len() {
        x := b[i]
        if x < 16 {
            try Print(out, "0")
        }
        try Print(out, "{x:x}", (x: x))
    }
}

export Main(sys: &System): WriteError|OutOfMemory!void {
    var out := sys.Stdout()
    m := try sys.arena.Slice[u8](4097)
    for i in 0..4097 {
        m[i] = Trunc[u8](i * 7 + 3)
    }
    b := m[..]
    k := hash.TestKey(0x0706050403020100, 0x0f0e0d0c0b0a0908)
    x := hash.Xx(b) == CX
    y := hash.Xx64(b, 0x9E3779B97F4A7C15) == CX64
    c := hash.Crc32(b) == CC
    p := hash.Sip13(b, k) == CP
    p16 := hash.Sip13(b[0..16], k) == CP16
    try Print(mut out, "{x} {y} {c} {p} {p16} {cx:x} {cx64:x} {cc:x} {cp:x} {cp16:x}\n", (x: x, y: y, c: c, p: p, p16: p16, cx: CX, cx64: CX64, cc: CC, cp: CP, cp16: CP16))
    s := hash.Sha256(b)
    try Hex(mut out, s[..])
    try Print(mut out, " ")
    try Hex(mut out, CS[..])
    try Print(mut out, "\n")
    s5 := hash.Sha512(b)
    try Hex(mut out, s5[..])
    try Print(mut out, " ")
    try Hex(mut out, CS5[..])
    try Print(mut out, "\n")
    e := hash.Blake3(b)
    try Hex(mut out, e[..])
    try Print(mut out, " ")
    try Hex(mut out, CB[..])
    try Print(mut out, "\n")
}
`

// TestSTDHASHMOD_LongConstEqualsRunTime: every public one-shot hash
// evaluated as a const (the evaluator, mk check) over a message long enough
// to reach XxLong, BLAKE3's EndChunk and its parent merges, and Sip13 under
// a const key, equals the native result and the Go model.
func TestSTDHASHMOD_LongConstEqualsRunTime(t *testing.T) {
	b := make([]byte, 4097)
	for i := range b {
		b[i] = byte(i*7 + 3)
	}
	k0, k1 := uint64(0x0706050403020100), uint64(0x0f0e0d0c0b0a0908)
	s := sha256.Sum256(b)
	s5 := sha512.Sum512(b)
	e := hmBlake3(b)
	want := fmt.Sprintf("true true true true true %x %x %x %x %x\n%s %s\n%s %s\n%s %s\n",
		hmXxh3(b), hmXxh64(b, 0x9E3779B97F4A7C15), crc32.ChecksumIEEE(b), hmSip13Keyed(b, k0, k1), hmSip13Keyed(b[:16], k0, k1),
		hex.EncodeToString(s[:]), hex.EncodeToString(s[:]), hex.EncodeToString(s5[:]), hex.EncodeToString(s5[:]),
		hex.EncodeToString(e[:]), hex.EncodeToString(e[:]))
	exe := hashModBuild(t, hashModLongConst)
	got := strings.ReplaceAll(hashModExec(t, exe), "\r\n", "\n")
	if got != want {
		t.Fatalf("got\n%swant\n%s", got, want)
	}
}

// hmXx64Zig are the values Zig 0.17.0 printed on a development machine
// (2026-11-07, `zig build-exe -O ReleaseSafe`, a
// scratch program): std.hash.XxHash64.hash(seed, msg[0..n]) with msg[i] =
// (7i + 3) mod 256. Lengths cover the n < 32 path (seed + P5), the stripe
// path (the four seeded lanes) and each tail class.
var hmXx64Zig = []struct {
	seed uint64
	n    int
	hex  string
}{
	{1, 0, "d5afba1336a3be4b"}, {1, 3, "2d80e074130d0176"}, {1, 4, "5ec9fdd10fcbcb0e"},
	{1, 7, "d8b4a941715fee31"}, {1, 8, "f119f5b26c0020ab"}, {1, 31, "aaca4e41ffcae21c"},
	{1, 32, "7421b79a02b42132"}, {1, 33, "17b3df313de9a40e"}, {1, 100, "8d8957e68f02c7ce"},
	{0x9E3779B97F4A7C15, 0, "c4349fc93c010000"}, {0x9E3779B97F4A7C15, 3, "78efd77575e26575"},
	{0x9E3779B97F4A7C15, 4, "6f0a6c97d68bf353"}, {0x9E3779B97F4A7C15, 7, "d97ede93c9d66a0d"},
	{0x9E3779B97F4A7C15, 8, "a2f1e28437a78a1b"}, {0x9E3779B97F4A7C15, 31, "755437271d1d0a84"},
	{0x9E3779B97F4A7C15, 32, "bf624b932c090428"}, {0x9E3779B97F4A7C15, 33, "7aceaf1e9d34ea35"},
	{0x9E3779B97F4A7C15, 100, "f6d8f65c625abb4f"},
	{^uint64(0), 0, "298f4c84b24f5380"}, {^uint64(0), 3, "e422054a9bc43a06"}, {^uint64(0), 4, "1c1305b583b5e78c"},
	{^uint64(0), 7, "76c17f70933c79ed"}, {^uint64(0), 8, "91981f79e1f7d960"}, {^uint64(0), 31, "931df7ff3b1d1e4a"},
	{^uint64(0), 32, "2cd9b29bf8eed6bf"}, {^uint64(0), 33, "dbbac9766bb3adf7"}, {^uint64(0), 100, "7d5ca39dfcfbeaca"},
}

const hashModXx64Zig = `import std.hash

const Seeds: [3]u64 = [1, 0x9E3779B97F4A7C15, 0xFFFFFFFFFFFFFFFF]
const Lens: [9]int = [0, 3, 4, 7, 8, 31, 32, 33, 100]

export Main(sys: &System): WriteError|OutOfMemory!void {
    var out := sys.Stdout()
    msg := try sys.arena.Slice[u8](100)
    for i in 0..100 {
        msg[i] = Trunc[u8](i * 7 + 3)
    }
    for s in 0..3 {
        for k in 0..9 {
            n := Lens[k]
            h := hash.Xx64(msg[0..n], Seeds[s])
            try Print(mut out, "{h:x}\n", (h: h))
        }
    }
}
`

// TestSTDHASHMOD_Xx64SeededMatchesZig: seeded hash.Xx64 (release
// build) and the Go model against an independent implementation, Zig's
// std.hash.XxHash64, under three non-zero seeds.
func TestSTDHASHMOD_Xx64SeededMatchesZig(t *testing.T) {
	msg := make([]byte, 100)
	for i := range msg {
		msg[i] = byte(i*7 + 3)
	}
	var want strings.Builder
	for _, v := range hmXx64Zig {
		if m := fmt.Sprintf("%016x", hmXxh64(msg[:v.n], v.seed)); m != v.hex {
			t.Errorf("Go model Xx64(len %d, seed %x) = %s, Zig %s", v.n, v.seed, m, v.hex)
		}
		fmt.Fprintf(&want, "%s\n", strings.TrimLeft(v.hex, "0"))
	}
	exe := hashModBuild(t, hashModXx64Zig, "-release")
	got := strings.ReplaceAll(hashModExec(t, exe), "\r\n", "\n")
	if got != want.String() {
		t.Fatalf("Mk Xx64 under seeds:\ngot\n%swant\n%s", got, want.String())
	}
}

// hashModShortKeys hashes, for every length 0 to 33, a key that is its own
// allocation (so a load past its end is a bounds panic, not a read of the
// neighbour), with Xx, Sip13 under two keys, Crc32 and Xx64.
const hashModShortKeys = `import std.hash

export Main(sys: &System): WriteError|OutOfMemory!void {
    var out := sys.Stdout()
    var st: u64 = 5
    for n in 0..34 {
        b := try sys.arena.Slice[u8](n)
        for i in 0..n {
            st = st +% 0x9E3779B97F4A7C15
            b[i] = Trunc[u8](st >> 29)
        }
        x := hash.Xx(b[..])
        s := hash.Sip13(b[..], hash.TestKey(0x0706050403020100, 0x0f0e0d0c0b0a0908))
        s2 := hash.Sip13(b[..], hash.TestKey(0, 0))
        c := hash.Crc32(b[..])
        y := hash.Xx64(b[..], 7)
        try Print(mut out, "{n} {x:x} {s:x} {s2:x} {c:x} {y:x}\n", (n: n, x: x, s: s, s2: s2, c: c, y: y))
    }
}
`

// TestSTDHASHMOD_ShortKeysAtAllocationEnd: the short-input entries of Xx
// and Sip13 (no loop, overlapping loads) read nothing outside the key, at
// every length to 33, in dev and release builds, against the Go models.
func TestSTDHASHMOD_ShortKeysAtAllocationEnd(t *testing.T) {
	var want strings.Builder
	st := uint64(5)
	for n := 0; n < 34; n++ {
		b := make([]byte, n)
		for i := range b {
			st += 0x9E3779B97F4A7C15
			b[i] = byte(st >> 29)
		}
		fmt.Fprintf(&want, "%d %x %x %x %x %x\n", n, hmXxh3(b), hmSip13Keyed(b, 0x0706050403020100, 0x0f0e0d0c0b0a0908),
			hmSip13Keyed(b, 0, 0), crc32.ChecksumIEEE(b), hmXxh64(b, 7))
	}
	for _, flags := range [][]string{nil, {"-release"}} {
		exe := hashModBuild(t, hashModShortKeys, flags...)
		got := strings.ReplaceAll(hashModExec(t, exe), "\r\n", "\n")
		if got != want.String() {
			t.Fatalf("mk build %v:\ngot\n%swant\n%s", flags, got, want.String())
		}
	}
}

// hashModLaneFiles walks std/hash's Mk sources, its benchmark rows and the
// Go test files of std.hash.
func hashModLaneFiles(t *testing.T, visit func(path, text string)) {
	t.Helper()
	var files []string
	for _, dir := range []string{"hash", filepath.Join("..", "tests", "bench-lanes", "std-hashmod")} {
		err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() && strings.HasSuffix(p, ".mk") {
				files = append(files, p)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	gofiles, _ := filepath.Glob("std-hashmod_*_test.go")
	files = append(files, gofiles...)
	if len(files) < 10 {
		t.Fatalf("only %d hash files found", len(files))
	}
	for _, p := range files {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		visit(p, string(b))
	}
}

// TestSTDHASHMOD_NoEmptyRecordPrint: no Mk source of std.hash, and no
// Mk text embedded in its Go tests, calls Print with an empty record (the
// short form is the house style).
func TestSTDHASHMOD_NoEmptyRecordPrint(t *testing.T) {
	bad := `", ()` + `)`
	hashModLaneFiles(t, func(p, text string) {
		for i, line := range strings.Split(text, "\n") {
			if strings.Contains(line, "Print(") && strings.Contains(line, bad) {
				t.Errorf("%s:%d: Print with an empty record: %s", p, i+1, strings.TrimSpace(line))
			}
		}
	})
}
