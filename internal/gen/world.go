// Why: a generator runs before bodies are checked and layouts exist, so it
// must see declarations through a narrow view that the driver can build
// early and hash for the incremental cache.
//
// What: the World and FuncWorld interfaces and the views they return:
// DeclView, FieldView, VariantView, MethodView and FlowView.
//
// How: the driver implements World with resolve and check's
// declaration-level translation, as if gen/ were empty; a view holds
// resolved declarations only, exported fields only for another module.

// Package gen runs gen lines (stage 4): binding, the generator run on eval,
// quote expansion, the generated file, origins and E0905 wrapping.
package gen

import (
	"mk/internal/source"
	"mk/internal/types"
)

// World is the generator's view of declarations at stage 4:
// resolved declarations, no layouts, no bodies, and the module of
// the gen lines as if gen/ were empty. The driver implements it with resolve
// and check's declaration-level translation.
type World interface {
	// Decl describes a struct, enum, error or interface item. For an item of
	// another module the view holds exported fields only; for an item of the
	// gen lines' own module, all fields.
	Decl(item types.ItemID, from types.ModuleID) (*DeclView, bool)
	// Hash is the hash recorded for a reflection read of item's declaration
	// view, keyed exactly as the types.Read is: ReadFields with name "all"
	// is the all-fields view, ReadFields with name "" the exported-fields
	// view, ReadSignature (name "") the declaration header. The two field
	// views hash differently. Zero before iface exists.
	Hash(item types.ItemID, k types.ReadKind, name string) source.Hash
}

// DeclView is one declaration as a generator sees it.
type DeclView struct {
	Item     types.ItemID
	Kind     types.ItemKind // KindStruct, KindEnum, KindError, KindInterface
	Exported bool
	Generics []types.GenericParam
	Fields   []FieldView   // struct and error, declaration order
	Variants []VariantView // enum, declaration order
	Methods  []MethodView  // interface, declaration order
}

// FieldView is one field. Type is NoType when the stage-4 view cannot
// resolve the written type, which then is Unresolved, a bare name that may be
// declared by gen output of the same module (else E0906).
type FieldView struct {
	Name       source.Symbol
	Type       types.TypeID
	Unresolved source.Symbol
	Exported   bool
	Span       source.Span
}

// VariantView is one enum variant.
type VariantView struct {
	Name   source.Symbol
	Fields []FieldView
	Span   source.Span
}

// MethodView is one interface method, with Self as KSelf, or a
// function: an inherent function of a named struct or enum, or a function
// named as a gen argument.
type MethodView struct {
	Sig  types.Signature
	Span source.Span
	// Flows are the clauses the signature declares, in the stage-4 view's
	// canonical order (by kind, field, parameter, path).
	Flows []FlowView
}

// FlowView is one declared flow clause (std.core's Flow): Kind is
// FlowKind's discriminant (From, Into, Ends, Store); Param, Path and Field
// as check.GenFlow states them.
type FlowView struct {
	Kind               uint8
	Param, Path, Field string
}

// FuncWorld is World's optional extension: functions as a generator sees
// them. A World without it makes a function gen argument E0301 and
// Type.Methods on a struct or enum E0901.
type FuncWorld interface {
	// Func is a function item's stage-4 signature and declared flows; false
	// for any other item.
	Func(item types.ItemID) (*MethodView, bool)
	// Funcs are the inherent functions of a struct or enum item in
	// declaration order, only exported ones for an item of a module other
	// than from; false for any other kind.
	Funcs(item types.ItemID, from types.ModuleID) ([]types.ItemID, bool)
}
