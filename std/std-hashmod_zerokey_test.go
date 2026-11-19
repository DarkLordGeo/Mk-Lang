// Why: hash.Sip13 must agree with a second, unrelated SipHash-1-3, and Rust's
// DefaultHasher is one under the key (0, 0).
// What: TestSTDHASHMOD_ZeroKeyMatchesRust and
// TestSTDHASHMOD_ZeroFilledKeyMatchesRust.
// How: compares the Mk output with values Rust printed, stored in the file.

// The cross-check of hash.Sip13 against Rust's
// std::hash::DefaultHasher: DefaultHasher::new()
// is SipHash-1-3 under the key words (0, 0), so hash.Sip13 under the key
// (0, 0) must print what Rust printed. TestSTDHASHMOD_ZeroKeyMatchesRust takes
// the key from the test fixture hash.TestKey (std-hashmod_mk_test.go);
// TestSTDHASHMOD_ZeroFilledKeyMatchesRust uses public names only and
// zero-fills a HashKey, which is the key (0, 0). The Mk columns of
// the rows hash-sip13 and hash-sip13-short rest on the second.

package std

import (
	"fmt"
	"strings"
	"testing"
)

// hmSip13RustZero are the values Rust 1.99.1 printed on a development machine
// (2026-11-07, `rustc --edition 2024 -O`):
// DefaultHasher::new(), write(&msg[..n]), finish(), msg[i] = (7*i + 3) mod 256.
var hmSip13RustZero = []struct {
	n   int
	hex string
}{
	{0, "d1fba762150c532c"}, {1, "486b06067755d7c9"}, {7, "8a715eaaf55549aa"},
	{8, "36c186f0aa4cdbeb"}, {9, "5176191e3d24cbff"}, {15, "19ed3f1b38f7e4e2"},
	{16, "d1c94d62751d7b7b"}, {63, "42ed5c087e0bfe5d"}, {64, "65134a36595a21fb"},
	{65, "bbb33b0725b183dd"}, {255, "621951539b5066e6"}, {256, "9e352535fdf272ea"},
	{300, "37ba751da8b682e3"},
}

const hashModSipZeroKey = `import std.hash

const Lens: [13]int = [0, 1, 7, 8, 9, 15, 16, 63, 64, 65, 255, 256, 300]

export Main(sys: &System): WriteError|OutOfMemory!void {
    var out := sys.Stdout()
    msg := try sys.arena.Slice[u8](300)
    for i in 0..300 {
        msg[i] = Trunc[u8](i * 7 + 3)
    }
    key := hash.TestKey(0, 0)
    for k in 0..13 {
        n := Lens[k]
        h := hash.Sip13(msg[0..n], key)
        try Print(mut out, "{n} {h:x}\n", (n: n, h: h))
    }
}
`

// TestSTDHASHMOD_ZeroKeyMatchesRust runs hash.Sip13 (release build) under
// the key (0, 0), against Rust's DefaultHasher and the Go model.
func TestSTDHASHMOD_ZeroKeyMatchesRust(t *testing.T) {
	msg := make([]byte, 300)
	for i := range msg {
		msg[i] = byte(i*7 + 3)
	}
	var want strings.Builder
	for _, v := range hmSip13RustZero {
		if m := fmt.Sprintf("%016x", hmSip13Keyed(msg[:v.n], 0, 0)); m != v.hex {
			t.Errorf("Go model Sip13(len %d) under (0, 0) = %s, Rust %s", v.n, m, v.hex)
		}
		fmt.Fprintf(&want, "%d %s\n", v.n, strings.TrimLeft(v.hex, "0"))
	}
	exe := hashModBuild(t, hashModSipZeroKey, "-release")
	got := strings.ReplaceAll(hashModExec(t, exe), "\r\n", "\n")
	if got != want.String() {
		t.Fatalf("Mk Sip13 under the key (0, 0):\ngot\n%swant\n%s", got, want.String())
	}
}

// hashModSipZeroFilled is hashModSipZeroKey with public names only: the key
// is a zero-filled HashKey, as the benchmark rows make it.
var hashModSipZeroFilled = strings.Replace(hashModSipZeroKey,
	"    key := hash.TestKey(0, 0)\n",
	"    keys := try sys.arena.Slice[HashKey](1)\n    key := keys[0]\n", 1)

// TestSTDHASHMOD_ZeroFilledKeyMatchesRust runs hash.Sip13 (dev and
// release builds) under a zero-filled HashKey, which is the key
// (0, 0), against Rust's DefaultHasher::new().
func TestSTDHASHMOD_ZeroFilledKeyMatchesRust(t *testing.T) {
	if !strings.Contains(hashModSipZeroFilled, "Slice[HashKey](1)") || strings.Contains(hashModSipZeroFilled, "TestKey") {
		t.Fatal("the program still uses the fixture key")
	}
	var want strings.Builder
	for _, v := range hmSip13RustZero {
		fmt.Fprintf(&want, "%d %s\n", v.n, strings.TrimLeft(v.hex, "0"))
	}
	for _, flags := range [][]string{nil, {"-release"}} {
		exe := hashModBuild(t, hashModSipZeroFilled, flags...)
		got := strings.ReplaceAll(hashModExec(t, exe), "\r\n", "\n")
		if got != want.String() {
			t.Fatalf("Mk Sip13 under a zero-filled HashKey %v:\ngot\n%swant\n%s", flags, got, want.String())
		}
	}
}
