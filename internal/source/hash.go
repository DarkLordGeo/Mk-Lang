// Why: cache keys and public-fact hashes must be identical across runs,
// machines and goroutine schedules, so they are computed over canonical
// encodings only.
//
// What: Hash and Hash128, Hasher (Uvarint, Varint, Bool, String, Bytes, Hash,
// Sum), HashString and HashBytes.
//
// How: SHA-256 over a domain string, then length-prefixed writes in order;
// truncated to 128 bits where a hash names a cache entry.

package source

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"hash"
	"io"
)

// Hash is a full SHA-256 digest of a canonical encoding: a stable identity that
// is the same on every machine, run and goroutine schedule. The zero Hash means
// "none". Output is ordered by Compare (byte order), never by anything else.
type Hash [32]byte

// IsZero reports whether h is the zero Hash.
func (h Hash) IsZero() bool { return h == Hash{} }

// String renders 64 lowercase hex digits.
func (h Hash) String() string { return hex.EncodeToString(h[:]) }

// Compare orders hashes by bytes: -1, 0 or +1.
func (h Hash) Compare(o Hash) int { return bytes.Compare(h[:], o[:]) }

// Trunc128 keeps the first 16 bytes: the name of a cache entry.
func (h Hash) Trunc128() Hash128 { return Hash128(h[:16]) }

// Uint64 is the first 8 bytes read big-endian, so fmt.Sprintf("%016x",
// h.Uint64()) equals the first 16 hex digits of h.String() (the instance
// symbol suffix).
func (h Hash) Uint64() uint64 { return binary.BigEndian.Uint64(h[:8]) }

// Hash128 is a Hash truncated to its first 16 bytes; it names cache entries.
type Hash128 [16]byte

// IsZero reports whether h is the zero Hash128.
func (h Hash128) IsZero() bool { return h == Hash128{} }

// String renders 32 lowercase hex digits, the prefix of the full Hash's String.
func (h Hash128) String() string { return hex.EncodeToString(h[:]) }

// Compare orders by bytes: -1, 0 or +1.
func (h Hash128) Compare(o Hash128) int { return bytes.Compare(h[:], o[:]) }

// Hasher computes a stable Hash over a sequence of canonical writes. The digest
// is SHA-256 of the domain written as String, followed by every write in order.
// Every write is self-delimiting (a uvarint, or a length-prefixed byte string, or
// a fixed-width nested hash), so two different sequences of writes under one
// schema never encode to the same bytes. Never write a FileID, Span, Symbol or
// any other ID: write the canonical value (path, string, stable key) instead.
// A Hasher is not safe for concurrent use.
type Hasher struct {
	h   hash.Hash
	buf [binary.MaxVarintLen64]byte
}

// NewHasher starts a hash in the given domain, a constant such as "mk.item.v1"
// naming what is hashed and its encoding version, so equal bytes under two
// domains never give equal hashes.
func NewHasher(domain string) *Hasher {
	hs := &Hasher{h: sha256.New()}
	hs.String(domain)
	return hs
}

// Uvarint writes u in unsigned LEB128 (encoding/binary's Uvarint form).
func (hs *Hasher) Uvarint(u uint64) {
	n := binary.PutUvarint(hs.buf[:], u)
	hs.h.Write(hs.buf[:n])
}

// Varint writes i zigzag-encoded as a uvarint (encoding/binary's Varint form).
func (hs *Hasher) Varint(i int64) {
	n := binary.PutVarint(hs.buf[:], i)
	hs.h.Write(hs.buf[:n])
}

// Bool writes 1 or 0 as a uvarint.
func (hs *Hasher) Bool(b bool) {
	if b {
		hs.Uvarint(1)
	} else {
		hs.Uvarint(0)
	}
}

// String writes len(s) as a uvarint, then the bytes of s.
func (hs *Hasher) String(s string) {
	hs.Uvarint(uint64(len(s)))
	io.WriteString(hs.h, s)
}

// Bytes writes len(b) as a uvarint, then b.
func (hs *Hasher) Bytes(b []byte) {
	hs.Uvarint(uint64(len(b)))
	hs.h.Write(b)
}

// Hash writes a nested hash as its 32 raw bytes (fixed width, no prefix).
func (hs *Hasher) Hash(h Hash) { hs.h.Write(h[:]) }

// Sum returns the digest of everything written so far; writing may continue.
func (hs *Hasher) Sum() Hash {
	var out Hash
	hs.h.Sum(out[:0])
	return out
}

// HashString is the Hash of one String write of s in domain.
func HashString(domain, s string) Hash {
	hs := NewHasher(domain)
	hs.String(s)
	return hs.Sum()
}

// HashBytes is the Hash of one Bytes write of b in domain.
func HashBytes(domain string, b []byte) Hash {
	hs := NewHasher(domain)
	hs.Bytes(b)
	return hs.Sum()
}
