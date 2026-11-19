// Why: HashKey.Of is the public way to key a hash, so it must work from a
// user program through public names only.
// What: TestStdHash_HashKeyOfFromUserCode.
// How: Sip13 under HashKey.Of(0, 0) against Rust's DefaultHasher values
// (hashOfRustZero), and a NewKeyed Map's order against the Go model.

package std

import (
	"encoding/binary"
	"fmt"
	"strings"
	"testing"
)

// hashOfRustZero are Rust 1.99.1's DefaultHasher::new() values (SipHash-1-3
// under the key (0, 0)) of msg[0..n], msg[i] = (7 i + 3) mod 256, as
// printed by a Rust program (as in std-hashmod_zerokey_test.go). Copied so that
// this test does not depend on another file's fixture.
var hashOfRustZero = []struct {
	n   int
	hex string
}{
	{0, "d1fba762150c532c"}, {1, "486b06067755d7c9"}, {7, "8a715eaaf55549aa"},
	{8, "36c186f0aa4cdbeb"}, {9, "5176191e3d24cbff"}, {15, "19ed3f1b38f7e4e2"},
	{16, "d1c94d62751d7b7b"}, {63, "42ed5c087e0bfe5d"}, {64, "65134a36595a21fb"},
	{65, "bbb33b0725b183dd"}, {255, "621951539b5066e6"}, {256, "9e352535fdf272ea"},
	{300, "37ba751da8b682e3"},
}

const hashOfProgram = `import std.hash

const Lens: [13]int = [0, 1, 7, 8, 9, 15, 16, 63, 64, 65, 255, 256, 300]

export Main(sys: &System): WriteError|OutOfMemory!void {
    var out := sys.Stdout()
    msg := try sys.arena.Slice[u8](300)
    for i in 0..300 {
        msg[i] = Trunc[u8](i * 7 + 3)
    }
    zero := HashKey.Of(0, 0)
    for k in 0..13 {
        n := Lens[k]
        h := hash.Sip13(msg[0..n], zero)
        try Print(mut out, "{n} {h:x}\n", (n: n, h: h))
    }
    var m := Map[int, int].NewKeyed(sys.arena, HashKey.Of(0x0706050403020100, 0x0f0e0d0c0b0a0908))
    for i in 0..400 {
        try m.Set(i * 7 - 1000, i)
        if i % 5 == 3 {
            k := (i - 2) * 7 - 1000
            _ = m.Remove(&k)
        }
    }
    var it := m.Iter()
    for e := it.Next() {
        try Print(mut out, "{v}\n", (v: *e.value))
    }
}
`

// TestStdHash_HashKeyOfFromUserCode builds a user program at dev and
// at release that makes keys only with HashKey.Of: hash.Sip13 under
// HashKey.Of(0, 0) gives Rust's DefaultHasher values, and a NewKeyed Map
// under HashKey.Of(SipHash's reference words) iterates in the order of the
// Go Swiss table model hashing with sip13k under those words.
func TestStdHash_HashKeyOfFromUserCode(t *testing.T) {
	t.Parallel()
	msg := make([]byte, 300)
	for i := range msg {
		msg[i] = byte(i*7 + 3)
	}
	var want strings.Builder
	for _, v := range hashOfRustZero {
		if m := fmt.Sprintf("%016x", sip13k(0, 0, msg[:v.n])); m != v.hex {
			t.Fatalf("Go model sip13k(0, 0, len %d) = %s, Rust %s", v.n, m, v.hex)
		}
		fmt.Fprintf(&want, "%d %s\n", v.n, strings.TrimLeft(v.hex, "0"))
	}

	// The model: ids are the insertion indices i, the key is i*7 - 1000;
	// the stored value is i, which the program prints in iteration order.
	const k0, k1 = 0x0706050403020100, 0x0f0e0d0c0b0a0908
	keyOf := func(i int) int { return i*7 - 1000 }
	hashID := func(i int) uint64 {
		var b [8]byte
		binary.LittleEndian.PutUint64(b[:], uint64(int64(keyOf(i))))
		return sip13k(k0, k1, b[:])
	}
	m := &swissModel{hashFn: hashID}
	for i := 0; i < 400; i++ {
		h := hashID(i)
		at, free := m.lookup(h, i)
		if at >= 0 {
			t.Fatalf("model: key %d already present", keyOf(i))
		}
		j := m.place(h, free)
		m.key[j], m.val[j] = i, i
		if i%5 == 3 {
			if j := m.find(hashID(i-2), i-2); j >= 0 {
				m.erase(j)
			}
		}
	}
	for i := 0; i < m.cap; i++ {
		if m.ctrl[i] < mDeleted {
			fmt.Fprintf(&want, "%d\n", m.val[i])
		}
	}
	for _, flags := range [][]string{nil, {"--release"}} {
		got := strings.ReplaceAll(collExec(t, collBuild(t, hashOfProgram, flags...)), "\r\n", "\n")
		diffLines(t, got, want.String())
	}
}
