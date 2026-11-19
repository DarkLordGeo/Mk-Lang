// Why: types are written in the same files as expressions but have their
// own small grammar; their node types live here.
//
// What: the type productions of doc/grammar.md: ErrorType, UnionType,
// RefType, MutType, MutSliceType, OptionalType, RawPtrType, SliceType,
// ArrayType, FnType, FnParam, RecordType, ParenType and PathType.
//
// How: "mut" "[" "]" has its own production, so mut []T is MutSliceType
// while mut ([]T) is MutType over ParenType.

package ast

// ErrorType is UnionType "!" Type: E!T. Without "!" the parser returns the
// UnionType (or the PrefixType) itself.
type ErrorType struct {
	Base
	Errors Type
	Ok     Type
}

// UnionType is PrefixType "|" PrefixType { "|" PrefixType }: an error set.
type UnionType struct {
	Base
	Types []Type
}

// RefType is "&" PrefixType.
type RefType struct {
	Base
	Elem Type
}

// MutType is "mut" PrefixType.
type MutType struct {
	Base
	Elem Type
}

// MutSliceType is "mut" "[" "]" PrefixType.
type MutSliceType struct {
	Base
	Elem Type
}

// OptionalType is "?" PrefixType.
type OptionalType struct {
	Base
	Elem Type
}

// RawPtrType is "*" PrefixType.
type RawPtrType struct {
	Base
	Elem Type
}

// SliceType is "[" "]" PrefixType.
type SliceType struct {
	Base
	Elem Type
}

// ArrayType is "[" Expr "]" PrefixType.
type ArrayType struct {
	Base
	Len  Expr
	Elem Type
}

// FnType is "fn" "(" [ FnParams ] ")" [ ":" Type ] { Flow }.
type FnType struct {
	Base
	Params []*FnParam
	Result Type // nil when absent
	Flows  []Flow
}

// FnParam is [ Name ":" ] Type [ ParamFlow ].
type FnParam struct {
	Base
	Name *Ident // nil when absent
	Type Type
	Into *Ident // nil when absent
}

// RecordType is "(" Fields ")".
type RecordType struct {
	Base
	Fields []*Field
}

// ParenType is "(" Type ")". It is kept, not dropped: mut ([]T) differs from
// mut []T.
type ParenType struct {
	Base
	Type Type
}

// PathType is Path [ TypeArgs ]: a named type, a primitive name, or a type
// parameter.
type PathType struct {
	Base
	Path *Path
	Args *TypeArgs // nil when absent
}
