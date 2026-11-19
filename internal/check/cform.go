// Why: a library's export externs are called from C, so each parameter and
// result must have one fixed C spelling; check, the header generator and
// the E1109 emitter must agree on which Mk types have one.
//
// What: CForms (a type's C type, the structs it needs, whether MkStr is
// used), HasCForm, CFormFunc and CFormMiss with its kinds.
//
// How: one predicate, walked recursively over the type: scalars, str, raw
// pointers, references and non-generic exported plain-layout structs have a
// C form; the driver's header generator (libheader.go) spells it in C, and
// E1109 refuses what it leaves out.

package check

import (
	"fmt"

	"mk/internal/source"
	"mk/internal/types"
)

// CForms walks types for their C form. A type has one exactly when it is
//
//   - an integer: i8 to i64 and u8 to u64 as int8_t to uint64_t, int as
//     int64_t and uint as uint64_t (pointer-wide on every supported
//     target); f32 as float, f64 as double; bool as bool (one byte, 0 or
//     1); char as uint32_t (a Unicode scalar); void as a result;
//   - str, as MkStr { const uint8_t *ptr; int64_t len; }, its two words,
//     the bytes UTF-8, which C must keep;
//   - a raw pointer *T, as T * when T has a C form, else void *;
//   - a reference &T or mut T to a T with a C form other than void, as
//     const T * or T * (MkPanicHook's &PanicInfo);
//   - a non-generic exported struct, not packed and with no align(n),
//     whose every field has a C form, as a C struct of the same name and
//     fields in declaration order (every struct has C layout).
//
// Nothing else has one: a slice, an optional, an error union (a function
// that can fail), an error set, an enum, a record, an array, a fn value,
// a generic or private type. A private struct stays E0412 and a vector
// E1107.
type CForms struct {
	Tab   *types.Table
	Items *types.Items
	// Name spells a symbol (a struct's name); nil gives "" for every name,
	// enough for HasCForm.
	Name func(source.Symbol) string
	// Structs are the structs met, in the order their C definitions must
	// come (a field's struct before the struct holding it).
	Structs []types.ItemID
	// UsesStr is set once a str was met (the header then defines MkStr).
	UsesStr bool
	seen    map[types.ItemID]bool
}

// Type is t's C type, or false when t has no C form; result admits void.
func (c *CForms) Type(t types.TypeID, result bool) (string, bool) {
	if c.seen == nil {
		c.seen = map[types.ItemID]bool{}
	}
	ty := c.Tab.Get(t)
	switch ty.Kind {
	case types.KInt:
		if ty.Signed {
			return fmt.Sprintf("int%d_t", ty.Bits), true
		}
		return fmt.Sprintf("uint%d_t", ty.Bits), true
	case types.KIntPtr:
		if ty.Signed {
			return "int64_t", true
		}
		return "uint64_t", true
	case types.KFloat:
		if ty.Bits == 32 {
			return "float", true
		}
		return "double", true
	case types.KBool:
		return "bool", true
	case types.KChar:
		return "uint32_t", true
	case types.KVoid:
		return "void", result
	case types.KStr:
		c.UsesStr = true
		return "MkStr", true
	case types.KRawPtr:
		if e := c.Tab.Get(ty.Elem); e.Kind != types.KVoid {
			if s, ok := c.Type(ty.Elem, false); ok {
				return s + " *", true
			}
		}
		return "void *", true
	case types.KRef, types.KMut:
		s, ok := c.Type(ty.Elem, false)
		if !ok {
			return "", false
		}
		if ty.Kind == types.KRef {
			return "const " + s + " *", true
		}
		return s + " *", true
	case types.KNamed:
		if len(ty.Args) > 0 {
			return "", false
		}
		it := c.Items.Get(ty.Item)
		sd, ok := it.Decl.(*types.StructDecl)
		if !ok || !it.Exported || len(sd.Generics) > 0 || sd.Packed != 0 || sd.Align != 0 {
			return "", false
		}
		name := ""
		if c.Name != nil {
			name = c.Name(it.Name)
		}
		if c.seen[ty.Item] {
			return name, true
		}
		c.seen[ty.Item] = true
		for _, f := range sd.Fields {
			if _, ok := c.Type(f.Type, false); !ok {
				delete(c.seen, ty.Item)
				return "", false
			}
		}
		c.Structs = append(c.Structs, ty.Item)
		return name, true
	}
	return "", false
}

// HasCForm reports whether t has a C form across a library boundary, as
// a parameter (result false) or a result (result true, which admits void).
func HasCForm(tab *types.Table, items *types.Items, t types.TypeID, result bool) bool {
	c := &CForms{Tab: tab, Items: items}
	_, ok := c.Type(t, result)
	return ok
}

// CFormMiss is one reason a function's signature has no C form (CFormFunc).
type CFormMiss struct {
	Kind  CFormMissKind
	Param int // the parameter's index, for MissConst and MissParam
}

// CFormMissKind is what a CFormMiss names.
type CFormMissKind uint8

const (
	MissErrors   CFormMissKind = iota + 1 // it can fail: its result is an error union
	MissVariadic                          // it ends in "..."
	MissGeneric                           // it has generic parameters
	MissConst                             // a const parameter (a compile-time argument)
	MissParam                             // a parameter's type has no C form
	MissResult                            // the result's type has no C form
)

// CFormFunc is every reason sig has no C form across a library boundary,
// in order: the function-level ones (can fail, variadic, generic), then
// each parameter (const, or a type with no C form), then the result; none
// when the whole signature has one. It is the one predicate E1109
// (e1109.go) and the driver's header writer (libheader.go) both use, so
// what check refuses and what the header leaves out never drift.
func CFormFunc(tab *types.Table, items *types.Items, sig *types.Signature) []CFormMiss {
	var out []CFormMiss
	if sig.Errors != types.EmptyErrors {
		out = append(out, CFormMiss{Kind: MissErrors, Param: -1})
	}
	if sig.Variadic {
		out = append(out, CFormMiss{Kind: MissVariadic, Param: -1})
	}
	if len(sig.Generics) > 0 {
		out = append(out, CFormMiss{Kind: MissGeneric, Param: -1})
	}
	for i, p := range sig.Params {
		switch {
		case p.Const:
			out = append(out, CFormMiss{Kind: MissConst, Param: i})
		case !HasCForm(tab, items, p.Type, false):
			out = append(out, CFormMiss{Kind: MissParam, Param: i})
		}
	}
	if sig.Errors == types.EmptyErrors && !HasCForm(tab, items, sig.Result, true) {
		out = append(out, CFormMiss{Kind: MissResult, Param: -1})
	}
	return out
}
