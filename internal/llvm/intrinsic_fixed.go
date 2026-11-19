// Why: many target intrinsics have one fixed signature, with no overload
// types; their table entries should not each repeat the same closure.
//
// What: fixedSig and the vector type helpers v2i64, v4i32 and v16i8.
//
// How: fixedSig turns a result and parameter list of type functions into an
// intrinsicSig with no immediates.

package llvm

// fixedSig is the entry of an intrinsic whose result and parameters are
// the types the functions give, none an immediate.
func fixedSig(ret func(*Module) Type, params ...func(*Module) Type) intrinsicSig {
	return intrinsicSig{0, func(m *Module, _ []Type) (Type, []Type, []bool) {
		ps := make([]Type, len(params))
		for i, p := range params {
			ps[i] = p(m)
		}
		return ret(m), ps, nil
	}}
}

func v2i64(m *Module) Type { return m.Vector(2, m.I(64)) }
func v4i32(m *Module) Type { return m.Vector(4, m.I(32)) }
func v16i8(m *Module) Type { return m.Vector(16, m.I(8)) }
