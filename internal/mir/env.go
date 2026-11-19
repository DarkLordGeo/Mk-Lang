// Why: a capturing function literal takes its captures through a hidden
// trailing parameter, and the validator, borrow and codegen must all
// recognize that parameter's type the same way.
//
// What: IsEnvType, the test for an environment type.
//
// How: an environment type is &R, where R is a record with at least one
// field and every field a shared reference: one shared borrow per captured
// name.

package mir

import "mk/internal/types"

// IsEnvType reports whether t is an environment type: &R, where R is a record
// with at least one field and every field a shared reference (&T). A
// capturing literal's environment holds one shared borrow per captured name,
// so it is never empty; a literal that captures nothing has no environment.
//
// Locals len(Sig.Params)+1 .. ParamCount of a body (its hidden parameters)
// and the arguments of a call past its callee's declared parameters (its
// environment arguments) have such a type (invariants M1, M5).
func IsEnvType(tab *types.Table, t types.TypeID) bool {
	if t == types.NoType || tab.Kind(t) != types.KRef {
		return false
	}
	r := tab.Get(tab.Get(t).Elem)
	if r.Kind != types.KRecord || len(r.Fields) == 0 {
		return false
	}
	for _, f := range r.Fields {
		if tab.Kind(f.Type) != types.KRef {
			return false
		}
	}
	return true
}
