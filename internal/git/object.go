// Why: an object's id is the only proof that its bytes are the ones the tag
// names, so every id is recomputed, never trusted.
//
// What: OID (ParseOID, String, IsZero), ObjectType, HashObject and Object.
//
// How: an object's id is the SHA-1 of "<type> <size>\0<content>",
// recomputed for every object of a pack, delta results included. Only SHA-1
// repositories are read.

package git

import (
	"crypto/sha1"
	"encoding/hex"
	"strconv"
)

// OID is a SHA-1 object id.
type OID [20]byte

// ParseOID reads 40 lowercase or uppercase hex digits.
func ParseOID(s string) (OID, bool) {
	var o OID
	if len(s) != 40 {
		return o, false
	}
	if _, err := hex.Decode(o[:], []byte(s)); err != nil {
		return o, false
	}
	return o, true
}

// String is 40 lowercase hex digits.
func (o OID) String() string { return hex.EncodeToString(o[:]) }

// IsZero reports whether o is all zeros (no object).
func (o OID) IsZero() bool { return o == OID{} }

// ObjectType is a git object type as a pack numbers it.
type ObjectType uint8

const (
	ObjCommit   ObjectType = 1
	ObjTree     ObjectType = 2
	ObjBlob     ObjectType = 3
	ObjTag      ObjectType = 4
	objOfsDelta ObjectType = 6
	objRefDelta ObjectType = 7
)

func (t ObjectType) String() string {
	switch t {
	case ObjCommit:
		return "commit"
	case ObjTree:
		return "tree"
	case ObjBlob:
		return "blob"
	case ObjTag:
		return "tag"
	case objOfsDelta:
		return "ofs-delta"
	case objRefDelta:
		return "ref-delta"
	}
	return "type " + strconv.Itoa(int(t))
}

// HashObject is the id of an object of type t and content data.
func HashObject(t ObjectType, data []byte) OID {
	h := sha1.New()
	h.Write([]byte(t.String()))
	h.Write([]byte{' '})
	h.Write([]byte(strconv.Itoa(len(data))))
	h.Write([]byte{0})
	h.Write(data)
	var o OID
	h.Sum(o[:0])
	return o
}

// Object is one object of a parsed pack.
type Object struct {
	Type ObjectType
	Data []byte
}
