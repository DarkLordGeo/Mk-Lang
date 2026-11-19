// Why: the stable hasher keys the cache, so its encoding must never change by
// accident.
// What: TestHasherEncodingPinned, TestHasherLengthPrefixPreventsCollisions and
// TestHashRendering.
// How: pins the byte encoding of every field kind, and checks inputs that
// would collide without length prefixes.

package source

import (
	"crypto/sha256"
	"fmt"
	"testing"
)

// TestHasherEncodingPinned pins the byte encoding: domain as a length-prefixed
// string, uvarint, zigzag varint, bool, length-prefixed string and bytes, raw
// nested hash. Changing any of it changes every cache key, so it must be a
// deliberate edit of this test.
func TestHasherEncodingPinned(t *testing.T) {
	t.Parallel()
	nested := HashString("d", "x")
	hs := NewHasher("mk.t.v1")
	hs.Uvarint(300)
	hs.Varint(-2)
	hs.Bool(true)
	hs.String("ab")
	hs.Bytes([]byte{0xff})
	hs.Hash(nested)
	var want []byte
	want = append(want, 7)
	want = append(want, "mk.t.v1"...)
	want = append(want, 0xac, 0x02) // 300
	want = append(want, 3)          // zigzag(-2)
	want = append(want, 1)          // true
	want = append(want, 2, 'a', 'b')
	want = append(want, 1, 0xff)
	want = append(want, nested[:]...)
	if got := hs.Sum(); got != Hash(sha256.Sum256(want)) {
		t.Fatalf("encoding changed: %v", got)
	}
}

func TestHasherLengthPrefixPreventsCollisions(t *testing.T) {
	t.Parallel()
	a := NewHasher("d")
	a.String("ab")
	a.String("c")
	b := NewHasher("d")
	b.String("a")
	b.String("bc")
	if a.Sum() == b.Sum() {
		t.Fatal("concatenation collision")
	}
	if HashString("d1", "x") == HashString("d2", "x") {
		t.Fatal("domains must separate")
	}
	if HashString("d", "x") != HashBytes("d", []byte("x")) {
		t.Fatal("String and Bytes encode alike")
	}
}

func TestHashRendering(t *testing.T) {
	t.Parallel()
	h := HashString("d", "x")
	s := h.String()
	if len(s) != 64 || h.IsZero() || !(Hash{}).IsZero() {
		t.Fatalf("String %q", s)
	}
	if h.Trunc128().String() != s[:32] {
		t.Fatal("Trunc128 is not the prefix")
	}
	if fmt.Sprintf("%016x", h.Uint64()) != s[:16] {
		t.Fatal("Uint64 is not the 16-hex prefix")
	}
	g := HashString("d", "y")
	if h.Compare(g) != -g.Compare(h) || h.Compare(h) != 0 || h.Trunc128().Compare(h.Trunc128()) != 0 {
		t.Fatal("Compare")
	}
	hs := NewHasher("d")
	hs.String("x")
	first := hs.Sum()
	if hs.Sum() != first || first != h {
		t.Fatal("Sum must not consume the state")
	}
}
