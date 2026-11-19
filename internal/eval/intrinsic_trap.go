// Why: std.sys.TrapEntry is the address of the support pack's mk_trap_call
// (internal/codegen trapform.go), a fact of the link that no constant may
// depend on, so the evaluator must refuse it.
//
// What: the evaluator's entry for std.sys.TrapEntry.
//
// How: registered in eval's own intrinsic table as a refusal, as
// std.rt.FramePointers is; the runtime's list of refused intrinsics is
// pinned by TestRuntimeIntrinsicsRefused.

package eval

import (
	"mk/internal/mir"
	"mk/internal/types"
)

func init() {
	intrinsics["std.sys.TrapEntry"] = func(*Machine, *frame, *mir.Terminator, types.FnRef) {
		refuse(types.RefuseExtern, "sys.TrapEntry is a fact of the link")
	}
}
