// Why: the published SipHash vectors use one key; hash.Sip13 must also agree
// with an independent implementation under another key.
// What: TestSTDHASHMOD_Sip13MatchesZig.
// How: compares the Mk output and the Go model with values Zig printed.

// The cross-check of the public hash.Sip13
// against an independent implementation:
// Zig 0.17.0's std.crypto.auth.siphash.SipHash64(1, 3) under a key other
// than the published vectors' 00..0f, and the Go model.

package std

import (
	"fmt"
	"strings"
	"testing"
)

// hmSip13Zig are the values Zig 0.17.0 printed on a development machine
// (2026-11-07, `zig build-exe -O ReleaseSafe`):
// SipHash64(1, 3).toInt(msg[0..n], key) with key = LE64(0x0123456789abcdef)
// || LE64(0xfedcba9876543210) and msg[i] = (7*i + 3) mod 256.
var hmSip13Zig = []struct {
	n   int
	hex string
}{
	{0, "f4b3fee397802911"}, {1, "fc6ef6cdabdc7499"}, {7, "1bad6e850ed54988"},
	{8, "0fe5e1dd9738ae51"}, {9, "56f58730002ad094"}, {15, "2073ed27c9e08b60"},
	{16, "9886a4a63a297d7d"}, {63, "b02a23503445b232"}, {64, "0b24d98cc5a7e501"},
	{65, "48b46bef2a63508c"}, {255, "4cd6a68849b05aa7"}, {256, "a4c306381415b1b9"},
	{300, "c33ed7b89b0c9d43"},
}

const hashModSipZig = `import std.hash

const Lens: [13]int = [0, 1, 7, 8, 9, 15, 16, 63, 64, 65, 255, 256, 300]

export Main(sys: &System): WriteError|OutOfMemory!void {
    var out := sys.Stdout()
    msg := try sys.arena.Slice[u8](300)
    for i in 0..300 {
        msg[i] = Trunc[u8](i * 7 + 3)
    }
    key := hash.TestKey(0x0123456789abcdef, 0xfedcba9876543210)
    for k in 0..13 {
        n := Lens[k]
        h := hash.Sip13(msg[0..n], key)
        try Print(mut out, "{n} {h:x}\n", (n: n, h: h))
    }
}
`

// TestSTDHASHMOD_Sip13MatchesZig runs the public hash.Sip13 (release build)
// under a key with all 128 bits chosen, against Zig's SipHash64(1, 3) and the
// Go model.
func TestSTDHASHMOD_Sip13MatchesZig(t *testing.T) {
	msg := make([]byte, 300)
	for i := range msg {
		msg[i] = byte(i*7 + 3)
	}
	var want strings.Builder
	for _, v := range hmSip13Zig {
		if m := fmt.Sprintf("%016x", hmSip13Keyed(msg[:v.n], 0x0123456789abcdef, 0xfedcba9876543210)); m != v.hex {
			t.Errorf("Go model Sip13(len %d) = %s, Zig %s", v.n, m, v.hex)
		}
		fmt.Fprintf(&want, "%d %s\n", v.n, strings.TrimLeft(v.hex, "0"))
	}
	exe := hashModBuild(t, hashModSipZig, "-release")
	got := strings.ReplaceAll(hashModExec(t, exe), "\r\n", "\n")
	if got != want.String() {
		t.Fatalf("Mk Sip13 under the chosen key:\ngot\n%swant\n%s", got, want.String())
	}
}
