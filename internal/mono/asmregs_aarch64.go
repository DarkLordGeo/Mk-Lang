// Why: asm on aarch64 names other registers and classes than on x86_64, and
// some of them LLVM reserves for itself; those must be refused before
// codegen hands LLVM a constraint it would miscompile or reject.
//
// What: arm64Kind, the register file of an aarch64 class or explicit
// register, and arm64Registers, the per-operand check asmRegisters runs on
// an aarch64 target.
//
// How: the classes are reg (x0 to x30 as 64-bit general registers) and vreg
// (v0 to v31, a float or a vector of at most 16 bytes). x18 is the platform
// register on macOS and Windows, x19 LLVM's base pointer, x29 the frame
// pointer and sp the stack pointer, so they are refused with reason
// reserved, as rbx and rbp are on x86_64. A discarded output (`out("v2") _`)
// is a clobber and holds no value, so any register of a known file takes it.

package mono

import (
	"strings"

	"mk/internal/mir"
	"mk/internal/types"
)

// arm64Reserved are the registers an aarch64 asm operand may not name.
var arm64Reserved = map[string]bool{"x18": true, "w18": true, "x19": true, "w19": true, "x29": true, "w29": true,
	"fp": true, "sp": true, "wsp": true, "xzr": true, "wzr": true}

// arm64Kind is the register file of an aarch64 class (explicit false) or
// register name: rkGPR for reg, x0 to x30 and w0 to w30, rkXMM (the 128-bit
// file) for vreg and v0 to v31, rkNone otherwise.
func arm64Kind(r string, explicit bool) regKind {
	if !explicit {
		switch r {
		case "reg":
			return rkGPR
		case "vreg":
			return rkXMM
		}
		return rkNone
	}
	r = strings.ToLower(r)
	for _, p := range []string{"x", "w"} {
		if n, ok := regNumber(r, p); ok && n <= 30 {
			return rkGPR
		}
	}
	for _, p := range []string{"v", "q", "d", "s"} {
		if n, ok := regNumber(r, p); ok && n <= 31 {
			return rkXMM
		}
	}
	if r == "lr" {
		return rkGPR
	}
	return rkNone
}

// arm64Registers judges one aarch64 asm statement's operands, reporting
// through report as asmRegisters does.
func (c *collector) arm64Registers(a *mir.Asm, report func(a *mir.Asm, reason, msg, reg string)) {
	for oi := range a.Operands {
		op := &a.Operands[oi]
		if op.Reg == "" {
			continue
		}
		if op.Explicit && arm64Reserved[strings.ToLower(op.Reg)] {
			report(a, "reserved", "`"+op.Reg+"` is reserved: LLVM or the platform holds the frame, base, platform or stack register there", op.Reg)
			continue
		}
		kind := arm64Kind(op.Reg, op.Explicit)
		if kind == rkNone {
			what := "class"
			if op.Explicit {
				what = "register"
			}
			report(a, "register", "`"+op.Reg+"` is no aarch64 register "+what, op.Reg)
			continue
		}
		discard := (op.Kind == mir.AsmOut || op.Kind == mir.AsmLateOut) && op.Out.Local == mir.NoLocal
		if !discard && !c.regFits(kind, op) {
			report(a, "class", "a value of type `"+c.env.Table().TypeString(op.Type, nil)+"` does not fit `"+op.Reg+"`", op.Reg)
		}
	}
}

// arm64Target reports whether t's asm is judged by the aarch64 table.
func arm64Target(t types.Target) bool { return t.Arch == types.ArchArm64 }
