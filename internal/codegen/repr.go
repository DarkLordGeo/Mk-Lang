// Why: codegen needs one answer, per Mk type, to how it lives in LLVM: its
// memory type, its SSA type, and what a load of it may assume.
//
// What: repr (a type's representation) with reprOf and computeRepr,
// sizeAlign and layout, and loadMD and tagMD (load metadata).
//
// How: every local lives in memory; scalars are loaded and stored with their
// scalar type, aggregates are bytes read through types.Layout. Loads carry
// !range, !nonnull, !noundef and !align, which are sound because writing
// through a raw pointer under a live loan, or making an invalid bool, char
// or enum value, is undefined behaviour. reprOf memoizes per pack.

package codegen

import (
	"mk/internal/llvm"
	"mk/internal/source"
	"mk/internal/types"
)

// reprKind is how a Mk type lives in LLVM.
type reprKind uint8

const (
	// rZero: size 0, no storage (void, never, empty structs).
	rZero reprKind = iota
	// rScalar: one LLVM scalar in memory and in SSA (bool is i8 in memory,
	// i1 in SSA).
	rScalar
	// rAgg: bytes in memory, addressed by offsets; never an SSA value.
	rAgg
)

// scalarClass refines rScalar for loads, stores and operations.
type scalarClass uint8

const (
	sInt scalarClass = iota
	sFloat
	sBool
	sChar
	sTag // a payload-free enum
	sPtr // &T, mut T: never null
	sRaw // *T, fn, ?&T, ?mut T, ?fn: may be null, no pointee facts
)

// repr is the representation of one concrete type.
type repr struct {
	kind   reprKind
	class  scalarClass
	ssa    llvm.Type // the SSA type of a scalar (i1 for bool)
	mem    llvm.Type // the memory type of a scalar (i8 for bool)
	signed bool
	size   uint64
	align  uint32
	// pointee is the type behind &T or mut T (its size and alignment give
	// dereferenceable and align), NoType otherwise.
	pointee types.TypeID
	// discrs are a payload-free enum's discriminant bit patterns.
	discrs []uint64
}

// reprOf is the representation of t, memoized per pack.
func (g *packGen) reprOf(t types.TypeID) *repr {
	if r, ok := g.reprs[t]; ok {
		return r
	}
	r := g.computeRepr(t)
	g.reprs[t] = r
	return r
}

func (g *packGen) computeRepr(t types.TypeID) *repr {
	m := g.m
	ty := g.tt.Get(t)
	sc := func(c scalarClass, ssa, mem llvm.Type, size uint64, signed bool) *repr {
		return &repr{kind: rScalar, class: c, ssa: ssa, mem: mem, size: size, align: uint32(size), signed: signed}
	}
	switch ty.Kind {
	case types.KVoid, types.KNever:
		return &repr{kind: rZero, size: 0, align: 1}
	case types.KBool:
		return sc(sBool, m.I(1), m.I(8), 1, false)
	case types.KChar:
		return sc(sChar, m.I(32), m.I(32), 4, false)
	case types.KInt:
		return sc(sInt, m.I(int(ty.Bits)), m.I(int(ty.Bits)), uint64(ty.Bits)/8, ty.Signed)
	case types.KIntPtr:
		return sc(sInt, m.I(64), m.I(64), 8, ty.Signed)
	case types.KFloat:
		if ty.Bits == 32 {
			return sc(sFloat, m.Float(), m.Float(), 4, true)
		}
		return sc(sFloat, m.Double(), m.Double(), 8, true)
	case types.KRef, types.KMut:
		r := sc(sPtr, m.Ptr(), m.Ptr(), 8, false)
		r.pointee = ty.Elem
		return r
	case types.KRawPtr, types.KFn:
		return sc(sRaw, m.Ptr(), m.Ptr(), 8, false)
	case types.KOptional:
		if types.NicheOptional(g.tt.Kind(ty.Elem)) {
			return sc(sRaw, m.Ptr(), m.Ptr(), 8, false)
		}
	case types.KStr, types.KSlice, types.KMutSlice:
		return &repr{kind: rAgg, size: 16, align: 8}
	case types.KParam, types.KSelf, types.KInfer, types.KPoison, types.KNone:
		source.ICEf(source.Span{}, "codegen: type %s is not concrete", g.tt.TypeString(t, nil))
	}
	l := g.env.Layout(t)
	if l == nil {
		source.ICEf(source.Span{}, "codegen: no layout for %s", g.tt.TypeString(t, nil))
	}
	if l.Shape.Size == 0 {
		return &repr{kind: rZero, size: 0, align: max(l.Shape.Align, 1)}
	}
	if ty.Kind == types.KNamed && l.HasTag && !l.Tag.Niche && l.Tag.Offset == 0 &&
		uint64(l.Tag.Size) == l.Shape.Size && len(l.Fields) == 0 {
		if d, ok := g.items.Get(ty.Item).Decl.(*types.EnumDecl); ok && noPayload(l) {
			// A declared signed tag type (enum E: i8) makes the tag signed:
			// int(e) sign-extends it, and Cast[E] tests the tag type's
			// signed bounds. The
			// discriminants stay the tag's bit patterns.
			signed := d.Tag != types.NoType && g.tt.Get(d.Tag).Signed
			r := sc(sTag, m.I(8*int(l.Tag.Size)), m.I(8*int(l.Tag.Size)), uint64(l.Tag.Size), signed)
			r.align = l.Shape.Align
			for _, v := range l.Variants {
				r.discrs = append(r.discrs, v.Discr)
			}
			return r
		}
	}
	return &repr{kind: rAgg, size: l.Shape.Size, align: max(l.Shape.Align, 1)}
}

func noPayload(l *types.Layout) bool {
	for _, v := range l.Variants {
		if len(v.Fields) > 0 {
			return false
		}
	}
	return true
}

// sizeAlign is t's size and alignment.
func (g *packGen) sizeAlign(t types.TypeID) (uint64, uint32) {
	r := g.reprOf(t)
	return r.size, r.align
}

// layout is env.Layout with an ICE for a missing one.
func (g *packGen) layout(t types.TypeID) *types.Layout {
	l := g.env.Layout(t)
	if l == nil {
		source.ICEf(source.Span{}, "codegen: no layout for %s", g.tt.TypeString(t, nil))
	}
	return l
}

// loadMD is the metadata of a load of a scalar. discr is set for the
// tag load of an RvDiscriminant, which never carries !range.
func (g *packGen) loadMD(r *repr, discr bool) []llvm.Attach {
	md := g.m.Metadata()
	switch r.class {
	case sRaw:
		return nil
	case sPtr:
		out := []llvm.Attach{md.NonNull(), md.NoUndef()}
		if _, a := g.sizeAlign(r.pointee); a > 1 {
			out = append(out, md.Align(uint64(a)))
		}
		return out
	case sBool:
		if discr {
			return []llvm.Attach{md.NoUndef()}
		}
		return []llvm.Attach{llvm.Range(md.Range(r.mem, []int64{0, 2})), md.NoUndef()}
	case sChar:
		if discr {
			return []llvm.Attach{md.NoUndef()}
		}
		return []llvm.Attach{llvm.Range(md.Range(r.mem, []int64{0, 0xD800, 0xE000, 0x110000})), md.NoUndef()}
	case sTag:
		if pairs := llvm.RangePairs(r.mem.Bits(), r.discrs); pairs != nil && !discr {
			return []llvm.Attach{llvm.Range(md.Range(r.mem, pairs)), md.NoUndef()}
		}
	}
	return []llvm.Attach{md.NoUndef()}
}

// tagMD is the metadata of an enum tag load of n bits with the given
// discriminants (an enum with payloads, read for a switch or a copy).
func (g *packGen) tagMD(bits int, discrs []uint64, discr bool) []llvm.Attach {
	md := g.m.Metadata()
	if pairs := llvm.RangePairs(bits, discrs); pairs != nil && !discr {
		return []llvm.Attach{llvm.Range(md.Range(g.m.I(bits), pairs)), md.NoUndef()}
	}
	return []llvm.Attach{md.NoUndef()}
}
