// Why: the std.simd intrinsics lower to vector instructions, and a malformed
// one should panic in the builder, never in clang.
//
// What: ExtractElement, InsertElement, ShuffleVector and constant vectors,
// with the lane helpers.
//
// How: each is checked like its scalar form: operand types, shuffle
// indexes and constant lanes are checked before the text is written, and a
// misuse panics with a message starting "llvm: ".

package llvm

import (
	"strconv"
	"strings"
)

// ExtractElement is lane idx (an integer) of the vector v.
func (b *Block) ExtractElement(v, idx Value, md ...Attach) Value {
	b.val(v, "ExtractElement")
	b.val(idx, "ExtractElement")
	if !v.Ty.IsVector() || !idx.Ty.IsInt() {
		panic("llvm: extractelement " + v.String() + ", " + idx.String())
	}
	r := b.beginValue(v.Ty.Elem(0), md)
	b.str("extractelement ")
	b.typed(v)
	b.commaTyped(idx)
	b.end(md)
	return r
}

// InsertElement is v with lane idx replaced by x.
func (b *Block) InsertElement(v, x, idx Value, md ...Attach) Value {
	b.val(v, "InsertElement")
	b.val(x, "InsertElement")
	b.val(idx, "InsertElement")
	if !v.Ty.IsVector() || v.Ty.Elem(0) != x.Ty || !idx.Ty.IsInt() {
		panic("llvm: insertelement " + v.String() + ", " + x.String() + ", " + idx.String())
	}
	r := b.beginValue(v.Ty, md)
	b.str("insertelement ")
	b.typed(v)
	b.commaTyped(x)
	b.commaTyped(idx)
	b.end(md)
	return r
}

// ShuffleVector picks lane mask[j] of the concatenation of a and c (both
// one vector type) for lane j of the result; -1 is a poison lane. The
// result has len(mask) lanes of a's element type.
func (b *Block) ShuffleVector(a, c Value, mask []int, md ...Attach) Value {
	b.val(a, "ShuffleVector")
	b.val(c, "ShuffleVector")
	if !a.Ty.IsVector() || a.Ty != c.Ty || len(mask) == 0 {
		panic("llvm: shufflevector " + a.String() + ", " + c.String())
	}
	n := int(a.Ty.Len())
	var sb strings.Builder
	sb.WriteString("<")
	for j, k := range mask {
		if k < -1 || k >= 2*n {
			panic("llvm: shufflevector index " + strconv.Itoa(k) + " outside two vectors of " + strconv.Itoa(n))
		}
		if j > 0 {
			sb.WriteString(", ")
		}
		if k < 0 {
			sb.WriteString("i32 poison")
		} else {
			sb.WriteString("i32 " + strconv.Itoa(k))
		}
	}
	sb.WriteString(">")
	m := b.f.m
	r := b.beginValue(m.Vector(len(mask), a.Ty.Elem(0)), md)
	b.str("shufflevector ")
	b.typed(a)
	b.commaTyped(c)
	b.str(", <")
	b.str(strconv.Itoa(len(mask)))
	b.str(" x i32> ")
	b.str(sb.String())
	b.end(md)
	return r
}

// GEPVector is getelementptr inbounds i8, ptr p, <N x i64> offs: the
// vector of the N byte addresses p + offs[j] (Gather's addresses).
func (b *Block) GEPVector(p, offs Value, md ...Attach) Value {
	b.ptr(p, "GEPVector")
	b.val(offs, "GEPVector")
	if !offs.Ty.IsVector() || !offs.Ty.Elem(0).IsInt() {
		panic("llvm: GEPVector offsets " + offs.String())
	}
	m := b.f.m
	r := b.beginValue(m.Vector(int(offs.Ty.Len()), m.Ptr()), md)
	b.str("getelementptr inbounds i8")
	b.commaTyped(p)
	b.commaTyped(offs)
	b.end(md)
	return r
}

// ConstVector is the constant vector <elem v0, elem v1, ...>; every value
// is a constant of type elem.
func (m *Module) ConstVector(elem Type, vs ...Value) Value {
	if len(vs) == 0 {
		panic("llvm: ConstVector of no lanes")
	}
	t := m.Vector(len(vs), elem)
	var sb strings.Builder
	sb.WriteString("<")
	for i, v := range vs {
		m.checkValue(v, "ConstVector")
		if v.Ty != elem || !v.isConst() {
			panic("llvm: ConstVector lane " + v.String() + " is not a constant " + elem.String())
		}
		if i > 0 {
			sb.WriteString(", ")
		}
		sb.WriteString(v.String())
	}
	sb.WriteString(">")
	return Value{Ty: t, ref: sb.String()}
}

// reduce is a vector reduction: the element type from the vector o[0].
func reduce(m *Module, o []Type) (Type, []Type, []bool) {
	return o[0].Elem(0), []Type{o[0]}, nil
}

// lanesOfI1 is <N x i1> for the vector t.
func lanesOfI1(m *Module, t Type) Type { return m.Vector(int(t.Len()), m.I(1)) }

func init() {
	for _, op := range []string{"add", "and", "or", "xor", "smin", "smax", "umin", "umax", "fminimum", "fmaximum"} {
		intrinsics["vector.reduce."+op] = intrinsicSig{1, reduce}
	}
	// masked.load.<vec>.p0(ptr, i32 align, <N x i1> mask, <vec> passthru)
	intrinsics["masked.load"] = intrinsicSig{2, func(m *Module, o []Type) (Type, []Type, []bool) {
		return o[0], []Type{o[1], m.I(32), lanesOfI1(m, o[0]), o[0]}, []bool{false, true, false, false}
	}}
	// masked.store.<vec>.p0(<vec> value, ptr, i32 align, <N x i1> mask)
	intrinsics["masked.store"] = intrinsicSig{2, func(m *Module, o []Type) (Type, []Type, []bool) {
		return m.Void(), []Type{o[0], o[1], m.I(32), lanesOfI1(m, o[0])}, []bool{false, false, true, false}
	}}
	// masked.gather.<vec>.<N x ptr>(<N x ptr>, i32 align, <N x i1>, <vec>)
	intrinsics["masked.gather"] = intrinsicSig{2, func(m *Module, o []Type) (Type, []Type, []bool) {
		return o[0], []Type{o[1], m.I(32), lanesOfI1(m, o[0]), o[0]}, []bool{false, true, false, false}
	}}
	// fptosi.sat.<int>.<float>: saturating, NaN to 0.
	sat := func(m *Module, o []Type) (Type, []Type, []bool) { return o[0], []Type{o[1]}, nil }
	intrinsics["fptosi.sat"] = intrinsicSig{2, sat}
	intrinsics["fptoui.sat"] = intrinsicSig{2, sat}
	// pshufb on 16 bytes (SSSE3): Lookup16's lowering on x86_64.
	intrinsics["x86.ssse3.pshuf.b.128"] = intrinsicSig{0, func(m *Module, o []Type) (Type, []Type, []bool) {
		v := m.Vector(16, m.I(8))
		return v, []Type{v, v}, nil
	}}
}
