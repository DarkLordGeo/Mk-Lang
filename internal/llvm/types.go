// Why: codegen builds IR through typed values, and every type must be
// interned so that comparing two types is comparing two words.
//
// What: Type and the Module's type constructors: opaque ptr, iN, float,
// double, arrays, literal structs, vectors and function types.
//
// How: only literal types, never a named struct type; a Type holds its
// table pointer, so String works without the Module.

// Package llvm is a small builder and printer for textual LLVM IR, in a
// syntax that LLVM 18 to 21 all parse (the supported floor is LLVM 21,
// toolchain.MinMajor). It knows nothing about Mk and runs no tool. Misuse of
// the builder (a type mismatch, an instruction after a terminator, a
// duplicate name) is an internal compiler error: the method panics with a
// message starting "llvm: ", which the caller's source.Guard turns into an
// ICE. This package imports nothing internal, so it cannot call source.ICEf.
package llvm

import (
	"fmt"
	"strconv"
)

type typeKind uint8

const (
	tkNone typeKind = iota
	tkVoid
	tkInt
	tkPtr
	tkFloat
	tkDouble
	tkArray
	tkStruct
	tkVector
	tkFunc
)

type typeInfo struct {
	kind     typeKind
	bits     int    // tkInt
	n        uint64 // tkArray, tkVector
	elem     Type   // tkArray, tkVector
	fields   []Type // tkStruct fields, tkFunc params
	packed   bool   // tkStruct
	ret      Type   // tkFunc
	variadic bool   // tkFunc
	str      string // the printed form, also the interning key
}

// typeTable interns types by their printed form; literal types are equal
// exactly when they print equally. Index 0 is the reserved "no type".
type typeTable struct {
	infos []typeInfo
	byStr map[string]uint32
}

func newTypeTable() *typeTable {
	return &typeTable{infos: []typeInfo{{}}, byStr: map[string]uint32{}}
}

// internText interns the type printed as text (the module's scratch): an
// existing type is found without allocating; a new one is made by mk.
func (tt *typeTable) internText(text []byte, mk func(str string) typeInfo) Type {
	if id, ok := tt.byStr[string(text)]; ok {
		return Type{id: id, tab: tt}
	}
	return tt.intern(mk(string(text)))
}

func (tt *typeTable) intern(ti typeInfo) Type {
	if id, ok := tt.byStr[ti.str]; ok {
		return Type{id: id, tab: tt}
	}
	id := uint32(len(tt.infos))
	tt.infos = append(tt.infos, ti)
	tt.byStr[ti.str] = id
	return Type{id: id, tab: tt}
}

// Type is an LLVM type, interned per Module, printed by String. Comparable:
// two Types of one Module are equal exactly when they are the same LLVM type.
// The zero Type is "no type" and prints as "". The table pointer is what lets
// String work without the Module.
type Type struct {
	id  uint32
	tab *typeTable
}

func (t Type) info() *typeInfo {
	if t.tab == nil {
		return &typeInfo{}
	}
	return &t.tab.infos[t.id]
}

// String is the LLVM spelling of t: i32, ptr, [16 x i8], <{ i8, ptr }>.
func (t Type) String() string { return t.info().str }

// IsZero reports whether t is the zero "no type".
func (t Type) IsZero() bool { return t.tab == nil }

// IsVoid reports whether t is void.
func (t Type) IsVoid() bool { return t.info().kind == tkVoid }

// IsInt reports whether t is an integer type iN.
func (t Type) IsInt() bool { return t.info().kind == tkInt }

// IsPtr reports whether t is ptr.
func (t Type) IsPtr() bool { return t.info().kind == tkPtr }

// IsFloat reports whether t is float or double.
func (t Type) IsFloat() bool { k := t.info().kind; return k == tkFloat || k == tkDouble }

// IsFunc reports whether t is a function type.
func (t Type) IsFunc() bool { return t.info().kind == tkFunc }

// IsAggregate reports whether t is an array or a struct.
func (t Type) IsAggregate() bool { k := t.info().kind; return k == tkArray || k == tkStruct }

// IsVector reports whether t is a vector type.
func (t Type) IsVector() bool { return t.info().kind == tkVector }

// Bits is N for iN, 32 for float, 64 for double and ptr, else 0.
func (t Type) Bits() int {
	switch ti := t.info(); ti.kind {
	case tkInt:
		return ti.bits
	case tkFloat:
		return 32
	case tkDouble, tkPtr:
		return 64
	}
	return 0
}

// Len is the element count of an array or vector, the field count of a
// struct, the parameter count of a function type, else 0.
func (t Type) Len() uint64 {
	switch ti := t.info(); ti.kind {
	case tkArray, tkVector:
		return ti.n
	case tkStruct, tkFunc:
		return uint64(len(ti.fields))
	}
	return 0
}

// Elem is the element type of an array or vector, field i of a struct, or
// parameter i of a function type; the zero Type otherwise.
func (t Type) Elem(i int) Type {
	switch ti := t.info(); ti.kind {
	case tkArray, tkVector:
		return ti.elem
	case tkStruct, tkFunc:
		if i >= 0 && i < len(ti.fields) {
			return ti.fields[i]
		}
	}
	return Type{}
}

// Ret is the result type of a function type, else the zero Type.
func (t Type) Ret() Type { return t.info().ret }

// Variadic reports whether t is a variadic function type.
func (t Type) Variadic() bool { return t.info().variadic }

func (m *Module) checkType(t Type, what string) {
	if t.tab != m.types {
		panic("llvm: " + what + ": type from another module or zero type")
	}
}

// I is iN, 1 <= N <= 128.
func (m *Module) I(bits int) Type {
	if bits < 1 || bits > 128 {
		panic(fmt.Sprintf("llvm: integer width %d out of 1..128", bits))
	}
	if t := m.ints[bits]; !t.IsZero() {
		return t
	}
	t := m.types.intern(typeInfo{kind: tkInt, bits: bits, str: "i" + strconv.Itoa(bits)})
	m.ints[bits] = t
	return t
}

// cached returns *c, interning ti into it on first use.
func (m *Module) cached(c *Type, ti typeInfo) Type {
	if c.IsZero() {
		*c = m.types.intern(ti)
	}
	return *c
}

// Ptr is ptr (opaque, address space 0).
func (m *Module) Ptr() Type { return m.cached(&m.ptrT, typeInfo{kind: tkPtr, str: "ptr"}) }

// Float is the IEEE single type, float.
func (m *Module) Float() Type { return m.cached(&m.fltT, typeInfo{kind: tkFloat, str: "float"}) }

// Double is the IEEE double type, double.
func (m *Module) Double() Type { return m.cached(&m.dblT, typeInfo{kind: tkDouble, str: "double"}) }

// Void is void; it is only a function result type.
func (m *Module) Void() Type { return m.cached(&m.voidT, typeInfo{kind: tkVoid, str: "void"}) }

// Array is [n x elem]; elem is a first-class non-void type.
func (m *Module) Array(n uint64, elem Type) Type {
	m.checkType(elem, "Array")
	if elem.IsVoid() || elem.IsFunc() {
		panic("llvm: Array of " + elem.String())
	}
	return m.types.intern(typeInfo{kind: tkArray, n: n, elem: elem,
		str: "[" + strconv.FormatUint(n, 10) + " x " + elem.String() + "]"})
}

// Struct is a literal struct, { a, b } or packed <{ a, b }>, never named.
func (m *Module) Struct(packed bool, fields ...Type) Type {
	b := m.tsc[:0]
	if packed {
		b = append(b, '<')
	}
	if len(fields) == 0 {
		b = append(b, "{}"...)
	} else {
		b = append(b, "{ "...)
		for i, f := range fields {
			m.checkType(f, "Struct")
			if f.IsVoid() || f.IsFunc() {
				panic("llvm: Struct field of type " + f.String())
			}
			if i > 0 {
				b = append(b, ", "...)
			}
			b = append(b, f.String()...)
		}
		b = append(b, " }"...)
	}
	if packed {
		b = append(b, '>')
	}
	m.tsc = b
	return m.types.internText(b, func(str string) typeInfo {
		return typeInfo{kind: tkStruct, fields: append([]Type(nil), fields...), packed: packed, str: str}
	})
}

// Vector is <n x elem>, used for ABI coercion only; elem is an integer,
// float or ptr type and n >= 1.
func (m *Module) Vector(n int, elem Type) Type {
	m.checkType(elem, "Vector")
	if n < 1 || !(elem.IsInt() || elem.IsFloat() || elem.IsPtr()) {
		panic(fmt.Sprintf("llvm: Vector(%d, %s)", n, elem))
	}
	return m.types.intern(typeInfo{kind: tkVector, n: uint64(n), elem: elem,
		str: "<" + strconv.Itoa(n) + " x " + elem.String() + ">"})
}

// FuncType is ret (params...[, ...]); ret may be void, params may not.
func (m *Module) FuncType(ret Type, params []Type, variadic bool) Type {
	m.checkType(ret, "FuncType")
	b := append(m.tsc[:0], ret.String()...)
	b = append(b, " ("...)
	for i, p := range params {
		m.checkType(p, "FuncType")
		if p.IsVoid() || p.IsFunc() {
			panic("llvm: FuncType parameter of type " + p.String())
		}
		if i > 0 {
			b = append(b, ", "...)
		}
		b = append(b, p.String()...)
	}
	if variadic {
		if len(params) > 0 {
			b = append(b, ", "...)
		}
		b = append(b, "..."...)
	}
	b = append(b, ')')
	m.tsc = b
	return m.types.internText(b, func(str string) typeInfo {
		return typeInfo{kind: tkFunc, ret: ret, fields: append([]Type(nil), params...), variadic: variadic, str: str}
	})
}

// mangle is the overload suffix of an intrinsic name for t (p0, i32, f64,
// v4i32), as LLVM's Intrinsic::getName spells it.
func mangle(t Type) string {
	switch ti := t.info(); ti.kind {
	case tkPtr:
		return "p0"
	case tkInt:
		return "i" + strconv.Itoa(ti.bits)
	case tkFloat:
		return "f32"
	case tkDouble:
		return "f64"
	case tkVector:
		return "v" + strconv.FormatUint(ti.n, 10) + mangle(ti.elem)
	}
	panic("llvm: no intrinsic mangling for " + t.String())
}
