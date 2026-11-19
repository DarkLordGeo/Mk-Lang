// Why: a cached entry is reused only while every read it recorded still
// holds in the current session.
//
// What: World and LayoutWorld (the questions validation asks), Valid and
// Hasher.ItemPart.
//
// How: a positive read holds while its part's hash is unchanged, a negative
// read while the thing is still absent; validation stops at the first
// failing read and returns it.

package iface

import (
	"mk/internal/source"
	"mk/internal/types"
)

// World answers "what is the current hash of this part", over the session
// the driver has built so far (modules loaded from the cache and modules
// compiled in this run). The driver implements it; SessionWorld is the
// implementation over Env plus callbacks.
type World interface {
	// Part returns the current hash for a read's (Kind, Key, Name), and
	// false when the thing does not exist (an absent impl, item, file,
	// field). For the negative kinds ReadNoItem and ReadNoImpl, ok reports
	// whether the thing now exists and the hash is the part the read keeps
	// (ReadNoItem's folded-name neighbourhood, zero for ReadNoImpl). A World
	// is scoped to the reading module: parts that depend on what the reader
	// can see (a type's member list) are computed over the reader's module
	// and its transitive import closure.
	Part(r types.Read) (source.Hash, bool)
}

// LayoutWorld is a World that answers the layout parts InstanceContentHash
// reads by identity instead of by key: the part of the named type t, or, with
// t NoType, of the error item e. The driver's pack key world implements it, so
// a type that exists only as an instance of this session needs no index by
// spelling.
type LayoutWorld interface {
	World
	LayoutPart(t types.TypeID, e types.ItemID) (source.Hash, bool)
}

// Valid reports whether every read still holds: a positive read's part
// exists with an equal hash; a negative read's thing is still absent and,
// for ReadNoItem, its folded-name neighbourhood hash is unchanged; ReadFile
// with a zero hash holds while the file is absent. The first failing read is
// returned for --timings explanations. Validation stops at it.
func Valid(w World, reads []types.Read) (ok bool, failed types.Read) {
	for _, r := range reads {
		h, exists := w.Part(r)
		switch {
		case r.Kind.IsNegative():
			if exists || h != r.Hash {
				return false, r
			}
		case r.Kind == types.ReadFile && r.Hash.IsZero():
			if exists {
				return false, r
			}
		default:
			if !exists || h != r.Hash {
				return false, r
			}
		}
	}
	return true, types.Read{}
}

// ItemPart is the hash of one part of an item, the value a Read records
// (the driver fills the zero hashes resolve and check record
// with it). It is w's answer for (kind, k, name).
func (h *Hasher) ItemPart(w World, kind types.ReadKind, k types.Key, name string) (source.Hash, bool) {
	return w.Part(types.Read{Kind: kind, Key: k, Name: name})
}
