// Why: a module-level name may hide a prelude name, and module-level names
// share one namespace, so a function `List` hides the prelude's `List` in
// every position; the error that follows is baffling without a pointer.
//
// What: hidesPreludeType, the note naming the hiding declaration, and
// isTypeItem.
//
// How: when a diagnostic's item is a module-level non-type whose name the
// prelude declares as a type, the note and a rename alternative are added.

package check

import (
	"mk/internal/diag"
	"mk/internal/types"
)

// hidesPreludeType adds the hiding note to d when it is a module-level item
// that is not a type and the prelude module declares a type of its name.
func (c *Checker) hidesPreludeType(d *diag.Diagnostic, it *types.Item) {
	if d == nil || it == nil || isTypeItem(it.Kind) {
		return
	}
	mod := c.cfg.Items.Module(it.Module)
	if mod.Path == "std.core" {
		return
	}
	name := c.cfg.Interner.Str(it.Name)
	id, ok := c.cfg.Items.ByKey(types.ItemKey("std.core", name))
	if !ok || !isTypeItem(c.cfg.Items.Get(id).Kind) {
		return
	}
	d.Text("note", "the "+it.Kind.String()+" `"+name+"` of this module hides the prelude type `"+name+"`: a module-level name hides a prelude name in every position").
		Loc("declared_at", it.Span).
		Alt("rename the " + it.Kind.String() + " `" + name + "`")
}

// isTypeItem reports whether an item of kind k names a type.
func isTypeItem(k types.ItemKind) bool {
	switch k {
	case types.KindStruct, types.KindEnum, types.KindAlias, types.KindInterface, types.KindError:
		return true
	}
	return false
}
