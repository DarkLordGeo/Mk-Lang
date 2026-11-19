// Why: codegen's trapped call on x86_64-windows (internal/codegen/trapform.go)
// ends in llvm.ubsantrap, which LLVM lowers to ud1l <imm>(%eax), %eax; the
// IR builder must know its signature.
//
// What: the ubsantrap entry of the intrinsic table.
//
// How: one init that adds the signature: void, one i8 immediate argument.

package llvm

func init() {
	// ubsantrap(i8 immarg kind): noreturn; the kind is the instruction's
	// immediate.
	intrinsics["ubsantrap"] = intrinsicSig{0, func(m *Module, o []Type) (Type, []Type, []bool) {
		return m.Void(), []Type{m.I(8)}, []bool{true}
	}}
}
