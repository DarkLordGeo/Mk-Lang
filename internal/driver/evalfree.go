// Why: compile-time evaluation of a non-generic body that calls
// std.raw.Release or std.raw.HasFree directly needs the impl lookup that
// mono makes for generic bodies; without it eval raises an ICE.
//
// What: program.FreeImpl, the driver's eval.FreeImpls (internal/eval/eval.go).
//
// How: it makes the lookup mono's ImplMethod makes and returns the reads
// it made with the answer.

package driver

import (
	"mk/internal/eval"
	"mk/internal/types"
)

var _ eval.FreeImpls = (*program)(nil)

// FreeImpl resolves the method Free of t's impl of std.core.Free, as
// monoEnv.ImplMethod does, and returns the lookup's reads. False when std
// declares no interface Free or t has no impl; rejectedProgram embeds
// *program and answers the same.
func (p *program) FreeImpl(t types.TypeID) (types.FnRef, bool, []types.Read) {
	ifc, ok := p.w.items.ByKey(types.ItemKey("std.core", "Free"))
	if !ok {
		var rs types.ReadSet
		rs.Add(types.Read{Kind: types.ReadNoItem, Key: types.ModuleKey("std.core"), Name: "Free"})
		return types.FnRef{}, false, rs.Sorted()
	}
	var rs types.ReadSet
	fn, args, ok := p.s.monoEnv(p.w).implMethod(ifc, 0, t, &rs)
	if !ok {
		return types.FnRef{}, false, rs.Sorted()
	}
	return types.FnRef{Item: fn, Args: args}, true, rs.Sorted()
}
