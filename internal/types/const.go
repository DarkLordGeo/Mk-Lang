// Why: compile-time values are compared, hashed and cached across passes and
// processes, so one value must have exactly one representation.
//
// What: ConstValue (plain bytes plus relocations to other consts and
// functions), Reloc and FnRef, ConstTable (Intern, Get), Canonicalize, and
// the little-endian helpers LoadUint and StoreUint.
//
// How: a value is laid out as the target lays out its type, in a canonical
// form (rules C1 to C7 on ConstValue): padding zero, one NaN, bools 0 or 1,
// pointer words zero with a Reloc at their offset. ConstTable interns
// canonical values, so equal values share one ConstID.

package types

import (
	"encoding/binary"
	"fmt"
	"slices"
	"sync"

	"mk/internal/source"
)

// ConstID names one interned const value of a ConstTable; equal values intern
// once. The zero ConstID is none.
type ConstID uint32

// NoConst is the zero ConstID.
const NoConst ConstID = 0

// RelocKind tags a Reloc.
type RelocKind uint8

const (
	// RelocData: the pointer word points at another const value (the pointee
	// of a reference, slice or str).
	RelocData RelocKind = iota + 1
	// RelocFn: the pointer word is a function: a named function with its
	// generic arguments, or a fn literal item.
	RelocFn
)

// FnRef names a function: an item and its generic arguments.
type FnRef struct {
	Item ItemID
	Args []GenericArg
}

// Reloc is one pointer word inside a const value's bytes.
type Reloc struct {
	Offset uint32
	Kind   RelocKind
	Data   ConstID
	Fn     FnRef
}

// Canonical NaN bit patterns (C2): every NaN a const holds is one of these.
const (
	CanonicalNaN32 uint32 = 0x7fc00000
	CanonicalNaN64 uint64 = 0x7ff8000000000000
)

// ConstValue is a compile-time value in canonical form, laid out exactly as
// the target lays out Type, little-endian on every supported target:
//
//	C1 len(Bytes) is the size of Type, padding bytes zero;
//	C2 every float NaN is the canonical NaN;
//	C3 every bool byte is 0 or 1;
//	C4 every pointer word is zero in Bytes and has a Reloc at its offset;
//	C5 none of a niche optional (NicheOptional: ?&T, ?mut T, ?fn) is all
//	   zero with no reloc; every other ?T has a some byte, 0 for none;
//	C6 Relocs are sorted by Offset, at distinct, non-overlapping offsets;
//	C7 the pointee of a str or []T has type [N]u8 or [N]T, N the length;
//	   an empty one too: its pointer word relocates to a [0]u8 or [0]T
//	   const, never a zero word without a reloc.
//
// The evaluator, which knows the layout, establishes C1, C2, C3, C5 and C7;
// Canonicalize establishes C4 and C6. A ConstValue is immutable once interned.
type ConstValue struct {
	Type   TypeID
	Align  uint32
	Bytes  []byte
	Relocs []Reloc
}

// Canonicalize sorts the relocations (C6), zeroes the pointer word under
// each (C4), and reports a reloc outside the bytes, two at overlapping
// offsets, or a reloc of no kind as an error, which the caller turns into an
// ICE. ptrBytes is the target's pointer size.
func (v *ConstValue) Canonicalize(ptrBytes uint8) error {
	slices.SortFunc(v.Relocs, func(a, b Reloc) int { return int(a.Offset) - int(b.Offset) })
	for i, r := range v.Relocs {
		if r.Kind != RelocData && r.Kind != RelocFn {
			return fmt.Errorf("const: reloc at %d has no kind", r.Offset)
		}
		end := uint64(r.Offset) + uint64(ptrBytes)
		if end > uint64(len(v.Bytes)) {
			return fmt.Errorf("const: reloc at %d past %d bytes", r.Offset, len(v.Bytes))
		}
		if i > 0 && uint64(v.Relocs[i-1].Offset)+uint64(ptrBytes) > uint64(r.Offset) {
			return fmt.Errorf("const: relocs at %d and %d overlap", v.Relocs[i-1].Offset, r.Offset)
		}
		clear(v.Bytes[r.Offset:end])
	}
	return nil
}

// ConstTable interns const values. Intern and Get are safe for concurrent use;
// Get takes no lock. The zero ConstTable is not usable; call NewConstTable.
type ConstTable struct {
	mu    sync.Mutex
	vals  source.AppendList[*ConstValue]
	index map[string]ConstID
}

// NewConstTable returns an empty table with slot 0 reserved.
func NewConstTable() *ConstTable {
	t := &ConstTable{index: make(map[string]ConstID)}
	t.vals.Append(nil)
	return t
}

// Intern returns the ID of v, adding a copy when no equal value exists. v
// must be canonical (call Canonicalize first); a reloc to an unknown const is
// an ICE. Equality is by type, alignment, bytes and relocations with their
// targets' IDs, which is deep because pointees intern first.
func (t *ConstTable) Intern(v ConstValue) ConstID {
	for _, r := range v.Relocs {
		if r.Kind == RelocData {
			if _, ok := t.vals.Get(uint32(r.Data)); !ok || r.Data == NoConst {
				source.ICEf(source.Span{}, "ConstTable.Intern: reloc to unknown const %d", r.Data)
			}
		}
	}
	key := encodeConst(nil, &v)
	t.mu.Lock()
	defer t.mu.Unlock()
	if id, ok := t.index[string(key)]; ok {
		return id
	}
	c := v
	c.Bytes = slices.Clone(v.Bytes)
	c.Relocs = slices.Clone(v.Relocs)
	for i := range c.Relocs {
		c.Relocs[i].Fn.Args = slices.Clone(c.Relocs[i].Fn.Args)
	}
	id := ConstID(t.vals.Append(&c))
	t.index[string(key)] = id
	return id
}

// Get returns an interned value; shared, do not modify. An unknown ID is an ICE.
func (t *ConstTable) Get(id ConstID) *ConstValue {
	v, ok := t.vals.Get(uint32(id))
	if !ok || id == NoConst {
		source.ICEf(source.Span{}, "ConstTable.Get: unknown const %d", id)
	}
	return v
}

// Len is the number of slots in use, slot 0 included.
func (t *ConstTable) Len() int { return int(t.vals.Len()) }

// encodeConst is the in-memory interning key; the stable encoding is iface's.
func encodeConst(b []byte, v *ConstValue) []byte {
	u := func(x uint64) { b = binary.AppendUvarint(b, x) }
	u(uint64(v.Type))
	u(uint64(v.Align))
	u(uint64(len(v.Bytes)))
	b = append(b, v.Bytes...)
	u(uint64(len(v.Relocs)))
	for _, r := range v.Relocs {
		u(uint64(r.Offset))
		u(uint64(r.Kind))
		u(uint64(r.Data))
		u(uint64(r.Fn.Item))
		u(uint64(len(r.Fn.Args)))
		for _, a := range r.Fn.Args {
			if a.IsConst {
				u(1)
			} else {
				u(0)
			}
			u(uint64(a.Type))
			u(uint64(a.Const.Kind))
			u(uint64(a.Const.Param))
			u(uint64(a.Const.Value))
			u(uint64(a.Const.Type))
		}
	}
	return b
}

// LoadUint returns the first n bytes (1, 2, 4 or 8) of b as a little-endian
// unsigned integer.
func LoadUint(b []byte, n int) uint64 {
	switch n {
	case 1:
		return uint64(b[0])
	case 2:
		return uint64(binary.LittleEndian.Uint16(b))
	case 4:
		return uint64(binary.LittleEndian.Uint32(b))
	case 8:
		return binary.LittleEndian.Uint64(b)
	}
	source.ICEf(source.Span{}, "types.LoadUint: width %d", n)
	return 0
}

// StoreUint writes the low n bytes (1, 2, 4 or 8) of x little-endian into b.
func StoreUint(b []byte, n int, x uint64) {
	switch n {
	case 1:
		b[0] = byte(x)
	case 2:
		binary.LittleEndian.PutUint16(b, uint16(x))
	case 4:
		binary.LittleEndian.PutUint32(b, uint32(x))
	case 8:
		binary.LittleEndian.PutUint64(b, x)
	default:
		source.ICEf(source.Span{}, "types.StoreUint: width %d", n)
	}
}
