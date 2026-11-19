// Why: every pass after resolution speaks about types through one data
// model, hash-consed so that type equality is ==.
//
// What: TypeID and the fixed primitive IDs, TypeKind, ConstArg, GenericArg,
// RecordField, FnParam, FnType, Type, ErrorSetID and ErrorMember.
//
// How: records keep their fields in name order, fn types compare flows
// positionally (parameter names dropped), a single error is the set of one,
// and an item's generics share one index space (the type's first, then the
// method's), so equal types have equal encodings.

package types

import (
	"strconv"

	"mk/internal/source"
)

// TypeID names one interned type of a Table. Structurally equal types have
// equal IDs, so type equality is ==. The zero TypeID means "none"; the
// primitives have fixed IDs in every Table. TypeIDs follow intern order, which
// follows goroutine timing: never order output by them or hash them.
type TypeID uint32

// NoType is the zero TypeID.
const NoType TypeID = 0

// The primitive types, interned by NewTable at these IDs in every Table.
const (
	Void TypeID = iota + 1
	Never
	Bool
	Char
	I8
	I16
	I32
	I64
	U8
	U16
	U32
	U64
	Int
	Uint
	F32
	F64
	Str
	// Poison is the checker's error type: it accepts every operation without a
	// diagnostic, so one cause gives one diagnostic. It never survives into
	// a Body, Signature or interface that leaves stage 6 (MIR validator rule
	// M11).
	Poison

	numFixed
)

// NumFixed is the number of table slots the fixed types occupy, slot 0
// included.
const NumFixed = int(numFixed)

// TypeKind is the constructor of a Type.
type TypeKind uint8

const (
	// KNone is the zero kind, only in the reserved slot 0.
	KNone TypeKind = iota
	// KInt is i8..i64 and u8..u64: Bits 8, 16, 32 or 64, and Signed.
	KInt
	// KIntPtr is int (Signed) and uint: Target.pointer_bytes wide (64 bits on
	// every supported target).
	KIntPtr
	// KFloat is f32 and f64: Bits 32 or 64.
	KFloat
	KBool
	// KChar is a Unicode scalar, 4 bytes.
	KChar
	KVoid
	KNever
	KStr
	// KRef is &Elem.
	KRef
	// KMut is mut Elem.
	KMut
	// KSlice is []Elem.
	KSlice
	// KMutSlice is mut []Elem, distinct from mut ([]Elem), which is KMut of a
	// KSlice.
	KMutSlice
	// KRawPtr is *Elem.
	KRawPtr
	// KArray is [Len]Elem.
	KArray
	// KOptional is ?Elem.
	KOptional
	// KErrorUnion is Errors!Elem: E!T; E!void has Elem Void. Errors is never
	// the empty set.
	KErrorUnion
	// KErrorSet is the type of an error value on its own: a bound error
	// (else e), fail's operand after widening, the error side of an edge. A
	// single error is the set of one.
	KErrorSet
	// KRecord is (a: T, b: U): Fields unique and sorted by name string.
	KRecord
	// KFn is fn(..): R with flows; parameter names are dropped.
	KFn
	// KNamed is a struct or enum item applied to Args (one per generic of the
	// item). Interfaces are never KNamed values: they are Bounds.
	KNamed
	// KParam is the Index-th generic of the owning item's combined list: the
	// type's own generics first, then the method's. It never escapes its
	// item except inside that item's Body or Signature.
	KParam
	// KSelf is Self inside an interface declaration, substituted by the
	// implementing type at impl check.
	KSelf
	// KInfer is an inference variable, checker-internal only (MIR validator
	// rule M11).
	KInfer
	// KPoison is the Poison type.
	KPoison

	numTypeKinds
)

var typeKindNames = [numTypeKinds]string{
	"none", "int", "intptr", "float", "bool", "char", "void", "never", "str", "ref", "mut", "slice",
	"mutslice", "rawptr", "array", "optional", "errorunion", "errorset", "record", "fn", "named",
	"param", "self", "infer", "poison",
}

// String names the kind, for debugging.
func (k TypeKind) String() string {
	if k < numTypeKinds {
		return typeKindNames[k]
	}
	return "kind(" + strconv.Itoa(int(k)) + ")"
}

// ConstArgKind tags a ConstArg.
type ConstArgKind uint8

const (
	// CANone is no const argument.
	CANone ConstArgKind = iota
	// CAParam is the Param-th generic of the owning item, a const parameter.
	CAParam
	// CAValue is an evaluated value: a literal or a named const.
	CAValue
	// CASizeOf is SizeOf[Type]().
	CASizeOf
)

// ConstArg is a const argument: a const parameter, a value,
// or SizeOf[T](). There is no arithmetic: [N - 1]u8 is E0709 and never reaches
// a Table. ConstArg is comparable.
type ConstArg struct {
	Kind  ConstArgKind
	Param uint32
	Value ConstID
	Type  TypeID
}

// GenericArg is one generic argument: a type, or a const argument.
type GenericArg struct {
	IsConst bool
	Type    TypeID
	Const   ConstArg
}

// TypeArg is the GenericArg of a type.
func TypeArg(t TypeID) GenericArg { return GenericArg{Type: t} }

// ConstGenericArg is the GenericArg of a const argument.
func ConstGenericArg(c ConstArg) GenericArg { return GenericArg{IsConst: true, Const: c} }

// RecordField is one field of a record type.
type RecordField struct {
	Name source.Symbol
	Type TypeID
}

// FnParam is one parameter of a fn type: its type and, for x: T into p, the
// ParamIndex of p (NoParam when absent).
type FnParam struct {
	Type TypeID
	Into ParamIndex
}

// FnType is the payload of a KFn type. Flows are positional: two
// spellings that differ only in parameter names are one type.
type FnType struct {
	Params  []FnParam
	Result  TypeID
	Errors  ErrorSetID
	Sources []Source
	// Fields is a per-field clause, as Flows.Fields: part of the
	// type's identity, sorted by Field.
	Fields []FieldSources
	Stores []Store
	Ends   []ParamIndex
}

// Type is one interned type. Which fields are meaningful depends on Kind (see
// TypeKind). A Type returned by Table.Get is shared and must not be modified.
type Type struct {
	Kind TypeKind
	// Bits and Signed describe KInt (and Signed KIntPtr) and KFloat.
	Bits   uint8
	Signed bool
	// Elem is the child of KRef, KMut, KSlice, KMutSlice, KRawPtr, KArray,
	// KOptional, and the ok type of KErrorUnion.
	Elem TypeID
	// Len is the length of KArray.
	Len ConstArg
	// Errors is the set of KErrorUnion and KErrorSet.
	Errors ErrorSetID
	// Item is the struct or enum of KNamed.
	Item ItemID
	// Index is the generic index of KParam and the variable number of KInfer.
	Index uint32
	// Args are the generic arguments of KNamed.
	Args []GenericArg
	// Fields are the fields of KRecord, sorted by name string.
	Fields []RecordField
	// Fn is the payload of KFn.
	Fn *FnType
}

// IsInteger reports whether t is KInt or KIntPtr.
func (t *Type) IsInteger() bool { return t.Kind == KInt || t.Kind == KIntPtr }

// IsReference reports whether t is &T or mut T.
func (t *Type) IsReference() bool { return t.Kind == KRef || t.Kind == KMut }

// IsSliceLike reports whether t is []T, mut []T or str: two words, (ptr, len).
func (t *Type) IsSliceLike() bool { return t.Kind == KSlice || t.Kind == KMutSlice || t.Kind == KStr }

// ErrorSetID names one interned error set. The zero ErrorSetID is the empty
// set (a function that cannot fail).
type ErrorSetID uint32

// EmptyErrors is the empty error set.
const EmptyErrors ErrorSetID = 0

// ErrorMember is one member of an error set: an error item, or a generic
// parameter that stands for an error set (Scope[E]).
type ErrorMember struct {
	IsParam bool
	Item    ItemID
	Param   uint32
}
