// Why: std.dyn.erased.Key[T]() names a type at run time; a compile-time
// evaluation of Ref.Of or Ref.Get must agree with the native program on it.
//
// What: typeKey and its encoder typeKeyEnc, and keyOf, the intrinsic.
//
// How: T's key is the same constant codegen folds the call to
// (internal/codegen/typekey.go): the first 8 bytes of a SHA-256 hash of the
// type's canonical encoding, with names and keys written as strings.

package eval

import (
	"encoding/binary"

	"mk/internal/mir"
	"mk/internal/source"
	"mk/internal/types"
)

// typeKeyIntrinsic is the std item whose calls return typeKey.
const typeKeyIntrinsic = "std.dyn.erased.Key"

// typeKey is the type key of t, an instance type: the first 8 bytes, read
// little-endian, of the SHA-256 hash (source.Hasher, domain "mk.typekey.v1")
// of t's canonical encoding. It is codegen's typeKey, copied because eval
// may not import codegen (the package DAG forbids it); the encoding
// is written out in codegen's doc comment, and TestTypeKey_MatchesCodegen
// pins the same three values as codegen's TestTypeKeyDistinctAndStable, so
// a change to either copy is seen. Names and keys are written as strings,
// never as IDs, so the key does not depend on table order.
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
		source.ICEf(source.Span{}, "eval: type key of %s, which is not an instance type", e.tt.TypeString(id, nil))
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
			source.ICEf(source.Span{}, "eval: type key of an error set with a generic member")
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
		source.ICEf(source.Span{}, "eval: type key of a const argument of kind %d", c.Kind)
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

// keyOf runs Key[T](): the type key of the call's one type argument, which
// mono substituted (an instance body), as a u64.
func keyOf(m *Machine, f *frame, t *mir.Terminator, fn types.FnRef) {
	m.targ(f, fn, 0)
	m.store(m.dest(f, t), typeKey(m.tab(), fn.Args[0].Type))
}
