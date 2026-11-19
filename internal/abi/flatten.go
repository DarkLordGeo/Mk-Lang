// Why: every target's rules look at a value's scalar leaves (which integers,
// pointers and floats lie at which offsets), not at its Mk type; computing
// them once keeps the per-target files short.
//
// What: view (one classification's context), scalarOf (types passed as one C
// scalar), flatten and walk (the leaves of an aggregate, with an unaligned
// flag), and hfa (the homogeneous float aggregate test).
//
// How: walk descends a layout's fields and array elements at absolute
// offsets, so a packed struct nested at an odd offset is caught as unaligned.
// Values above maxLeafBytes are not walked: no target needs their leaves.

package abi

import (
	"mk/internal/types"
)

// view is one classification's read-only context.
type view struct {
	tt     *types.Table
	layout LayoutOf
	target types.Target
}

func newView(tt *types.Table, layout LayoutOf, target types.Target) *view {
	if tt == nil || layout == nil {
		ice("abi: nil table or layout function")
	}
	return &view{tt: tt, layout: layout, target: target}
}

// leafClass is the class of one scalar leaf.
type leafClass uint8

const (
	leafInt leafClass = iota
	leafPtr
	leafFloat
)

// leaf is one scalar of a flattened layout, at its absolute offset.
type leaf struct {
	off   uint64
	size  uint64
	class leafClass
}

// scalar describes a type that the C ABI treats as one scalar value: an
// integer, bool, char, float, pointer, fn, niche optional, or payload-free
// enum (its tag integer).
type scalar struct {
	class  leafClass
	size   uint64
	signed bool
	isBool bool
}

// scalarOf reports whether t is passed as a C scalar, and which.
func (v *view) scalarOf(t types.TypeID) (scalar, bool) {
	ty := v.tt.Get(t)
	switch ty.Kind {
	case types.KInt:
		return scalar{class: leafInt, size: uint64(ty.Bits) / 8, signed: ty.Signed}, true
	case types.KIntPtr:
		return scalar{class: leafInt, size: uint64(v.target.PointerBytes), signed: ty.Signed}, true
	case types.KFloat:
		return scalar{class: leafFloat, size: uint64(ty.Bits) / 8}, true
	case types.KBool:
		return scalar{class: leafInt, size: 1, isBool: true}, true
	case types.KChar:
		return scalar{class: leafInt, size: 4}, true
	case types.KRef, types.KMut, types.KRawPtr, types.KFn:
		return scalar{class: leafPtr, size: uint64(v.target.PointerBytes)}, true
	case types.KOptional:
		if types.NicheOptional(v.tt.Kind(ty.Elem)) {
			return scalar{class: leafPtr, size: uint64(v.target.PointerBytes)}, true
		}
	case types.KNamed:
		return v.plainEnum(t, ty)
	}
	return scalar{}, false
}

// plainEnum is the tag scalar of a payload-free enum (its layout is an
// integer tag), signed when its declared tag type is.
func (v *view) plainEnum(t types.TypeID, ty *types.Type) (scalar, bool) {
	l := v.layoutOf(t)
	if !l.HasTag || l.Tag.Niche || l.Tag.Offset != 0 || len(l.Fields) != 0 || l.Shape.Size != uint64(l.Tag.Size) {
		return scalar{}, false
	}
	for _, vr := range l.Variants {
		if len(vr.Fields) != 0 {
			return scalar{}, false
		}
	}
	s := scalar{class: leafInt, size: uint64(l.Tag.Size)}
	if it := v.tt.Items().Get(ty.Item); it != nil {
		if d, ok := it.Decl.(*types.EnumDecl); ok && d.Tag != types.NoType {
			s.signed = v.tt.Get(d.Tag).Signed
		}
	}
	return s, true
}

func (v *view) layoutOf(t types.TypeID) *types.Layout {
	l := v.layout(t)
	if l == nil {
		ice("abi: no layout for %s", v.tt.TypeString(t, nil))
	}
	return l
}

// sizeAlign is the size and alignment of a concrete type.
func (v *view) sizeAlign(t types.TypeID) (uint64, uint32) {
	ty := v.tt.Get(t)
	switch ty.Kind {
	case types.KVoid, types.KNever:
		return 0, 1
	case types.KStr, types.KSlice, types.KMutSlice:
		p := uint64(v.target.PointerBytes)
		return 2 * p, uint32(p)
	case types.KParam, types.KSelf, types.KInfer, types.KPoison, types.KNone:
		ice("abi: type %s is not concrete", ty.Kind)
	}
	if s, ok := v.scalarOf(t); ok {
		return s.size, uint32(s.size)
	}
	l := v.layoutOf(t)
	return l.Shape.Size, l.Shape.Align
}

// maxLeafBytes bounds flattening: no target looks at the leaves of a value
// larger than four doubles (an AAPCS64 HFA), so larger values are not walked.
const maxLeafBytes = 32

// flat is a flattened layout.
type flat struct {
	leaves []leaf
	// unaligned is set when some field, array or leaf sits at an absolute
	// offset (from the start of the value being classified) that is not a
	// multiple of its type's alignment: the SysV MEMORY rule ("any unaligned
	// leaf"), judged as clang's X86_64ABIInfo::classify does, with
	// OffsetBase + field offset at every level. A packed struct nested at an
	// odd offset is aligned relative to itself but not absolutely, and is
	// MEMORY.
	unaligned bool
}

// flatten returns t's leaves; only meaningful for size <= maxLeafBytes.
func (v *view) flatten(t types.TypeID) flat {
	var f flat
	v.walk(&f, t, 0)
	return f
}

func (v *view) walk(f *flat, t types.TypeID, base uint64) {
	ty := v.tt.Get(t)
	if s, ok := v.scalarOf(t); ok {
		f.leaves = append(f.leaves, leaf{off: base, size: s.size, class: s.class})
		return
	}
	switch ty.Kind {
	case types.KVoid, types.KNever:
		return
	case types.KStr, types.KSlice, types.KMutSlice:
		p := uint64(v.target.PointerBytes)
		f.leaves = append(f.leaves, leaf{off: base, size: p, class: leafPtr}, leaf{off: base + p, size: p, class: leafInt})
		return
	case types.KArray:
		es, ea := v.sizeAlign(ty.Elem)
		if es == 0 {
			return
		}
		// clang checks OffsetBase % align(elem) once; elements after the
		// first are at multiples of the element size, itself a multiple of
		// the element alignment.
		v.checkAligned(f, base, ea)
		n := v.layoutOf(t).Shape.Size / es
		for i := uint64(0); i < n; i++ {
			v.walk(f, ty.Elem, base+i*es)
		}
		return
	}
	l := v.layoutOf(t)
	for _, fl := range l.Fields {
		v.field(f, fl, base)
	}
	if !l.HasTag {
		return
	}
	cls := leafInt
	if l.Tag.Niche {
		cls = leafPtr
	}
	v.checkAligned(f, base+l.Tag.Offset, uint32(l.Tag.Size))
	f.leaves = append(f.leaves, leaf{off: base + l.Tag.Offset, size: uint64(l.Tag.Size), class: cls})
	for _, vr := range l.Variants {
		for _, fl := range vr.Fields {
			v.field(f, fl, base)
		}
	}
}

// field walks one field at base+fl.Offset. fl.Offset is relative to the
// enclosing type and base is that type's absolute offset, so the alignment
// test is on the absolute offset; the walk then checks the field's
// own fields and leaves at their absolute offsets.
func (v *view) field(f *flat, fl types.FieldLayout, base uint64) {
	_, a := v.sizeAlign(fl.Type)
	v.checkAligned(f, base+fl.Offset, a)
	v.walk(f, fl.Type, base+fl.Offset)
}

// checkAligned marks f unaligned when an object of alignment a sits at the
// absolute offset off.
func (v *view) checkAligned(f *flat, off uint64, a uint32) {
	if a > 1 && off%uint64(a) != 0 {
		f.unaligned = true
	}
}

// hfa reports whether leaves form a homogeneous float aggregate of 1 to 4
// members of one float type covering exactly size bytes (AAPCS64 5.9.5.1),
// and its member width.
func hfa(fl flat, size uint64) (members int, base uint64, ok bool) {
	if len(fl.leaves) == 0 || len(fl.leaves) > 4 {
		return 0, 0, false
	}
	base = fl.leaves[0].size
	for _, l := range fl.leaves {
		if l.class != leafFloat || l.size != base {
			return 0, 0, false
		}
	}
	if uint64(len(fl.leaves))*base != size {
		return 0, 0, false
	}
	seen := uint64(0)
	for _, l := range fl.leaves {
		if l.off%base != 0 || l.off+base > size {
			return 0, 0, false
		}
		bit := uint64(1) << (l.off / base)
		if seen&bit != 0 {
			return 0, 0, false
		}
		seen |= bit
	}
	return len(fl.leaves), base, true
}
