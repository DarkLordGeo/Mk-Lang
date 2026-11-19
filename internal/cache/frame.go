// Why: a cache entry may be torn by a crash, flipped on disk, or read under
// the wrong key; each must read as a miss, never as data.
//
// What: the frame of an entry: frameMagic, frameVersion, frameFixed,
// encodeFrame, decodeFrame and its frameResult.
//
// How: magic "MKC1", version, kind, key, payload length, payload, then a
// SHA-256 trailer over every byte before it. Integers are little-endian
// fixed width; decodeFrame checks the kind and key it was asked for.

package cache

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"

	"mk/internal/source"
)

// frameMagic opens every framed entry and sidecar.
const frameMagic = "MKC1"

// frameVersion is the frame layout version; a change bumps the v1 directory.
const frameVersion = 1

// frameFixed is the size of a frame without its kind bytes and payload:
// magic 4, version 1, kind length 1, key 32, payload length 8, trailer 32.
const frameFixed = 4 + 1 + 1 + 32 + 8 + sha256.Size

// encodeFrame returns the framed bytes of payload under (kind, key). All
// integers are little-endian fixed width; the trailer is SHA-256 of every byte
// before it, so a torn or flipped file never verifies.
func encodeFrame(kind Kind, key source.Hash, payload []byte) []byte {
	out := make([]byte, 0, frameFixed+len(kind)+len(payload))
	out = append(out, frameMagic...)
	out = append(out, frameVersion, byte(len(kind)))
	out = append(out, kind...)
	out = append(out, key[:]...)
	out = binary.LittleEndian.AppendUint64(out, uint64(len(payload)))
	out = append(out, payload...)
	sum := sha256.Sum256(out)
	return append(out, sum[:]...)
}

// frameResult says what decodeFrame found.
type frameResult int

const (
	frameOK       frameResult = iota // verified, kind and key equal
	frameCorrupt                     // torn, truncated, flipped or not a frame
	frameOtherKey                    // a verified frame for another key (a 128-bit name collision)
)

// decodeFrame verifies b as a frame of kind and key and returns its payload,
// which aliases b. A frame that verifies but names another kind is corrupt
// (an entry never moves between kind directories); one that names another
// key is a collision, reported apart so the entry is kept.
func decodeFrame(b []byte, kind Kind, key source.Hash) ([]byte, frameResult) {
	if len(b) < frameFixed {
		return nil, frameCorrupt
	}
	if string(b[:4]) != frameMagic || b[4] != frameVersion {
		return nil, frameCorrupt
	}
	kl := int(b[5])
	if len(b) < frameFixed+kl {
		return nil, frameCorrupt
	}
	p := 6
	k := b[p : p+kl]
	p += kl
	var fk source.Hash
	copy(fk[:], b[p:p+32])
	p += 32
	n := binary.LittleEndian.Uint64(b[p : p+8])
	p += 8
	if n != uint64(len(b)-p-sha256.Size) {
		return nil, frameCorrupt
	}
	body := len(b) - sha256.Size
	sum := sha256.Sum256(b[:body])
	if !bytes.Equal(sum[:], b[body:]) {
		return nil, frameCorrupt
	}
	if string(k) != string(kind) {
		return nil, frameCorrupt
	}
	if fk != key {
		return nil, frameOtherKey
	}
	return b[p:body], frameOK
}
