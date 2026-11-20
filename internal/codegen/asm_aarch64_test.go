// Why: aarch64 inline asm reaches LLVM with other constraints than x86_64's:
// no Intel dialect, the condition flags as cc, AAPCS64's caller-saved set
// for clobber_abi, and vreg as the w constraint.
// What: TestAsmIRArm64.
// How: a hand-built MIR body is emitted for aarch64-linux and aarch64-macos,
// the IR text is checked, and clang compiles it at -O2.

package codegen

import (
	"strings"
	"testing"

	"mk/internal/mir"
	"mk/internal/types"
)

// TestAsmIRArm64: a reg output early-clobbered, a vreg input as w with the
// d modifier, an explicit w1 output that keeps clobber_abi from writing x1,
// x0 to x17 and q0 to q31 as discarded outputs, the link register and cc as
// clobbers, and no inteldialect.
func TestAsmIRArm64(t *testing.T) {
	for _, target := range []types.Target{types.Aarch64Linux, types.Aarch64Macos} {
		t.Run(target.Name, func(t *testing.T) {
			e := newTestEnv()
			f := e.body(e.fn("A", []types.TypeID{types.U64, types.F64}, types.U64, 0))
			r := f.local(types.U64)
			w := f.local(types.U32)
			f.asmBody(mir.Asm{Template: "add {0}, {2}, {2}\nfmov d1, {3:d}\nmov {1}, #1", ClobberABI: "C", Operands: []mir.AsmOperand{
				{Kind: mir.AsmOut, Reg: "reg", Out: place(r), Type: types.U64},
				{Kind: mir.AsmOut, Reg: "w1", Explicit: true, Out: place(w), Type: types.U32},
				{Kind: mir.AsmIn, Reg: "reg", In: mir.Copy(place(1)), Type: types.U64},
				{Kind: mir.AsmIn, Reg: "vreg", In: mir.Copy(place(2)), Type: types.F64},
			}}, f.at(3, 1))
			f.assign(place(0), use(mir.Copy(place(r))), f.at(4, 1))
			f.ret(f.at(4, 1))
			ll := emit(t, e, unitOf(e, []*mir.Body{f.b}), target, types.Release)
			text := fnText(ll, "mk.main.A")
			for _, want := range []string{`asm sideeffect alignstack "add ${0}, ${`, `"=&r,=&{w1},={x0},`, `~{lr},`, `,r,w,`, `={q0},`, `={q31},`, `~{lr},~{cc},~{memory}"`} {
				if !strings.Contains(text, want) {
					t.Errorf("no %q in:\n%s", want, text)
				}
			}
			if strings.Contains(text, "inteldialect") || strings.Contains(text, "={x1}") || strings.Contains(text, "~{flags}") {
				t.Errorf("an x86_64 form, or clobber_abi writes x1, which w1 is part of:\n%s", text)
			}
			clangC(t, target, t.TempDir(), "asm", ll, "-O2")
		})
	}
}
