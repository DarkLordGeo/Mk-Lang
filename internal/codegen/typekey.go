// Why: std.dyn's type-erased references check the type at run time, so a
// Ref.Of in one pack and a Ref.Get in another must agree on a key for T.
//
// What: std.dyn.erased.Key[T]() (typeKeyIntrinsic): typeKey and its
// canonical encoder typeKeyEnc, and typeKeys, the program's table that
// catches collisions.
//
// How: T's type key is a compile-time constant, distinct for distinct
// types of one program and the same in every build and every object pack:
// the first 8 bytes of a SHA-256 hash of T's canonical encoding.

package codegen

import (
	"encoding/binary"
	"sort"
	"strings"
	"sync"

	"mk/internal/mir"
	"mk/internal/source"
	"mk/internal/types"
)

// typeKeyIntrinsic is the std item whose calls fold to typeKey.
const typeKeyIntrinsic = "std.dyn.erased.Key"

// typeKey is the type key of t, an instance type (no generic parameter,
// Self or inference variable at any depth): the first 8 bytes, read
// little-endian, of the SHA-256 hash (source.Hasher, domain "mk.typekey.v1")
// of t's canonical encoding. The encoding writes each type as its kind
// (types.TypeKind as a uvarint) and then:
//
//   - KInt, KIntPtr, KFloat: Bits (uvarint), Signed (bool);
//   - KBool, KChar, KVoid, KNever, KStr: nothing;
//   - KRef, KMut, KSlice, KMutSlice, KRawPtr, KOptional: the element;
//   - KArray: the element, then the length as a const argument;
//   - KErrorUnion: the error set, then the ok type; KErrorSet: the set;
//   - KRecord: the field count, then each field's name (string) and type;
//   - KFn: the parameter count, each parameter's type and Into (uvarint),
//     the result, the error set, the sources (count; each Param, Behind,
//     and its path: count, each step's Field and Type), the stores (count;
//     each From, Into) and the ends (count; each);
//   - KNamed: the item's stable key (types.Key.String, module-qualified),
//     then the generic argument count and each argument.
//
// An error set is its member count and each member's item key; a generic
// argument is IsConst (bool) and then its type or its const argument; a
// const argument is its kind and then, for CAValue, the value (its type,
// Align, Bytes, and its relocations: count; each Offset, Kind, then the
// target value for RelocData or the function's item key and generic
// arguments for RelocFn), for CASizeOf the type. Names and keys are written
// as strings, never as IDs, so the key does not depend on table order.
// Distinct types encode differently, so two types share a key only through
// a SHA-256 collision in 64 bits, which typeKeys checks for per program.
// internal/eval must compute the same value.
func typeKey(tt *types.Table, t types.TypeID) uint64 {
	h := source.NewHasher("mk.typekey.v1")
	(&typeKeyEnc{tt: tt, h: h}).typ(t)
	s := h.Sum()
	return binary.LittleEndian.Uint64(s[:8])
}

// typeKeyEnc writes the canonical encoding of typeKey.
type typeKeyEnc struct {
	tt *types.Table
	h  *source.Hasher
}

func (e *typeKeyEnc) typ(id types.TypeID) {
	t := e.tt.Get(id)
	e.h.Uvarint(uint64(t.Kind))
	switch t.Kind {
	case types.KInt, types.KIntPtr, types.KFloat:
		e.h.Uvarint(uint64(t.Bits))
		e.h.Bool(t.Signed)
	case types.KBool, types.KChar, types.KVoid, types.KNever, types.KStr:
	case types.KRef, types.KMut, types.KSlice, types.KMutSlice, types.KRawPtr, types.KOptional:
		e.typ(t.Elem)
	case types.KArray:
		e.typ(t.Elem)
		e.constArg(t.Len)
	case types.KErrorUnion:
		e.errors(t.Errors)
		e.typ(t.Elem)
	case types.KErrorSet:
		e.errors(t.Errors)
	case types.KRecord:
		e.h.Uvarint(uint64(len(t.Fields)))
		for _, f := range t.Fields {
			e.h.String(e.tt.Interner().Str(f.Name))
			e.typ(f.Type)
		}
	case types.KFn:
		e.fn(t.Fn)
	case types.KNamed:
		e.h.String(e.tt.Items().Get(t.Item).Key.String())
		e.args(t.Args)
	default:
		source.ICEf(source.Span{}, "codegen: type key of %s, which is not an instance type", e.tt.TypeString(id, nil))
	}
}

func (e *typeKeyEnc) fn(f *types.FnType) {
	e.h.Uvarint(uint64(len(f.Params)))
	for _, p := range f.Params {
		e.typ(p.Type)
		e.h.Uvarint(uint64(p.Into))
	}
	e.typ(f.Result)
	e.errors(f.Errors)
	e.h.Uvarint(uint64(len(f.Sources)))
	for _, s := range f.Sources {
		e.h.Uvarint(uint64(s.Param))
		e.h.Bool(s.Behind)
		e.h.Uvarint(uint64(len(s.Path)))
		for _, st := range s.Path {
			e.h.Uvarint(uint64(st.Field))
			e.typ(st.Type)
		}
	}
	e.h.Uvarint(uint64(len(f.Stores)))
	for _, s := range f.Stores {
		e.h.Uvarint(uint64(s.From))
		e.h.Uvarint(uint64(s.Into))
	}
	e.h.Uvarint(uint64(len(f.Ends)))
	for _, p := range f.Ends {
		e.h.Uvarint(uint64(p))
	}
}

func (e *typeKeyEnc) errors(set types.ErrorSetID) {
	ms := e.tt.Members(set)
	e.h.Uvarint(uint64(len(ms)))
	for _, m := range ms {
		if m.IsParam {
			source.ICEf(source.Span{}, "codegen: type key of an error set with a generic member")
		}
		e.h.String(e.tt.Items().Get(m.Item).Key.String())
	}
}

func (e *typeKeyEnc) args(as []types.GenericArg) {
	e.h.Uvarint(uint64(len(as)))
	for _, a := range as {
		e.h.Bool(a.IsConst)
		if a.IsConst {
			e.constArg(a.Const)
		} else {
			e.typ(a.Type)
		}
	}
}

func (e *typeKeyEnc) constArg(c types.ConstArg) {
	e.h.Uvarint(uint64(c.Kind))
	switch c.Kind {
	case types.CAValue:
		e.constValue(c.Value)
	case types.CASizeOf:
		e.typ(c.Type)
	default:
		source.ICEf(source.Span{}, "codegen: type key of a const argument of kind %d", c.Kind)
	}
}

func (e *typeKeyEnc) constValue(id types.ConstID) {
	v := e.tt.Consts().Get(id)
	e.typ(v.Type)
	e.h.Uvarint(uint64(v.Align))
	e.h.Bytes(v.Bytes)
	e.h.Uvarint(uint64(len(v.Relocs)))
	for _, r := range v.Relocs {
		e.h.Uvarint(uint64(r.Offset))
		e.h.Uvarint(uint64(r.Kind))
		if r.Kind == types.RelocFn {
			e.h.String(e.tt.Items().Get(r.Fn.Item).Key.String())
			e.args(r.Fn.Args)
		} else {
			e.constValue(r.Data)
		}
	}
}

// typeKeys is the program's table of type keys, built on the first Key call
// any pack emits from every body of the unit (so it covers packs that came
// from the cache too) and shared by every pack. Two types of one program
// with one key are an ICE naming both, as iface's check of error
// identities is.
type typeKeys struct {
	once sync.Once
	keys map[types.TypeID]uint64
	ice  string
}

// typeKeysOf returns u's type key table, building it on first use.
func (u *Unit) typeKeysOf(tt *types.Table) *typeKeys {
	u.idxMu.Lock()
	if u.tkeys == nil {
		u.tkeys = &typeKeys{}
	}
	k := u.tkeys
	u.idxMu.Unlock()
	k.once.Do(func() { k.build(u, tt) })
	return k
}

// build collects the type argument of every Key call in u's bodies.
func (k *typeKeys) build(u *Unit, tt *types.Table) {
	k.keys = map[types.TypeID]uint64{}
	for i := range u.Funcs {
		b := u.Funcs[i].Body
		if b == nil {
			continue
		}
		for bi := range b.Blocks {
			t := &b.Blocks[bi].Term
			if !isCall(t) || t.Callee.Kind != mir.CallDirect || t.Callee.Fn.Args.Len == 0 {
				continue
			}
			if it := tt.Items().Get(t.Callee.Fn.Item); it == nil || it.Key.String() != typeKeyIntrinsic {
				continue
			}
			if t.Kind == mir.TermTailCall {
				source.ICEf(source.Span{}, "codegen: a tail call of the TypeKey intrinsic (check's E0314)")
			}
			ty := b.GenericArgs[t.Callee.Fn.Args.Start].Type
			if _, ok := k.keys[ty]; !ok {
				k.keys[ty] = typeKey(tt, ty)
			}
		}
	}
	k.ice = keyClashes(tt, k.keys)
}

// keyClashes names every set of types of keys that share a key, sorted,
// or is "" when the keys are distinct.
func keyClashes(tt *types.Table, keys map[types.TypeID]uint64) string {
	by := map[uint64][]string{}
	for ty, v := range keys {
		by[v] = append(by[v], tt.TypeString(ty, nil))
	}
	var clash []string
	for v, names := range by {
		if len(names) > 1 {
			sort.Strings(names)
			clash = append(clash, strings.Join(names, " and ")+" share the type key "+hex16(v))
		}
	}
	sort.Strings(clash)
	return strings.Join(clash, "; ")
}

// key is ty's type key, an ICE when the program has a key collision.
func (k *typeKeys) key(tt *types.Table, ty types.TypeID) uint64 {
	if k.ice != "" {
		source.ICEf(source.Span{}, "codegen: %s (std.dyn.erased.Key)", k.ice)
	}
	if v, ok := k.keys[ty]; ok {
		return v
	}
	return typeKey(tt, ty)
}

// hex16 spells v as 16 hex digits.
func hex16(v uint64) string {
	const digits = "0123456789abcdef"
	var b [16]byte
	for i := 15; i >= 0; i-- {
		b[i] = digits[v&15]
		v >>= 4
	}
	return string(b[:])
}
