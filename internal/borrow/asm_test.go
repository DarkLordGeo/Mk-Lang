// Why: an inline asm statement reads its inputs and writes its outputs, so
// the borrow and initialization rules must apply to it like any other access.
// What: the StAsm builders asmStmt, asmIn, asmOut and asmInOut, and the
// transfer test of StAsm on hand-built MIR.
// How: StAsm's transfer is a call of a bodiless extern with the In operands
// read and each Out place assigned as by =, with H(e) := {}: E0502 for a place
// under a live loan, E0403 for an input read before assignment, E0404 for an
// output to a non-var local already assigned; an out operand initializes a
// declared local.

package borrow

import (
	"testing"

	"mk/internal/diag"
	"mk/internal/mir"
	"mk/internal/types"
)

// asmStmt appends a StAsm with the operands and returns its span.
func (m *mb) asmStmt(ops ...mir.AsmOperand) mir.SpanID {
	s := m.sp()
	id := m.b.AddAsm(mir.Asm{Template: "nop", Operands: ops, Span: s})
	blk := &m.b.Blocks[m.cur]
	blk.Stmts = append(blk.Stmts, mir.Statement{Kind: mir.StAsm, Asm: id, Span: s})
	return s
}

func asmIn(p mir.Place) mir.AsmOperand {
	return mir.AsmOperand{Kind: mir.AsmIn, Reg: "reg", In: mir.Copy(p), Type: types.U64}
}

func asmOut(p mir.Place) mir.AsmOperand {
	return mir.AsmOperand{Kind: mir.AsmOut, Reg: "reg", Out: p, Type: types.U64}
}

func asmInOut(p mir.Place) mir.AsmOperand {
	return mir.AsmOperand{Kind: mir.AsmInOut, Reg: "reg", In: mir.Copy(p), Out: p, Type: types.U64}
}

// TestAsmTransfer: the accepting forms (an out initializing a declared
// local that is read after, an inout of a var, a discarded output) and the
// three rejections.
func TestAsmTransfer(t *testing.T) {
	t.Run("out initializes, inout of a var", func(t *testing.T) {
		k := newKit(t)
		m := k.body(k.sig("F", nil, types.U64, types.EmptyErrors))
		x := m.user("x", types.U64, false)
		v := m.user("v", types.U64, true)
		m.set(v, use(imm(types.U64, 1)))
		m.asmStmt(asmOut(m.local(x)), asmInOut(m.local(v)),
			mir.AsmOperand{Kind: mir.AsmOut, Reg: "rcx", Explicit: true, Out: mir.Place{Local: mir.NoLocal}, Type: types.U64})
		m.set(0, use(cp(m.local(x))))
		m.end(v, x)
		m.ret(mir.ExitNormal)
		m.expect(t, m.check(t))
	})
	t.Run("E0403 input before assignment", func(t *testing.T) {
		k := newKit(t)
		m := k.body(k.sig("F", nil, types.Void, types.EmptyErrors))
		x := m.user("x", types.U64, false)
		s := m.asmStmt(asmIn(m.local(x)))
		m.end(x)
		m.ret(mir.ExitNormal)
		m.expect(t, m.check(t), diag.E0403, s)
	})
	t.Run("E0404 output to an assigned non-var", func(t *testing.T) {
		k := newKit(t)
		m := k.body(k.sig("F", nil, types.Void, types.EmptyErrors))
		x := m.user("x", types.U64, false)
		m.set(x, use(imm(types.U64, 1)))
		s := m.asmStmt(asmInOut(m.local(x)))
		m.end(x)
		m.ret(mir.ExitNormal)
		m.expect(t, m.check(t), diag.E0404, s)
	})
	t.Run("E0502 write under a live loan", func(t *testing.T) {
		k := newKit(t)
		m := k.body(k.sig("F", nil, types.Void, types.EmptyErrors))
		v := m.user("v", types.U64, true)
		m.set(v, use(imm(types.U64, 1)))
		r := m.user("r", k.tab.Ref(types.U64), false)
		m.set(r, ref(m.local(v)))
		s := m.asmStmt(asmOut(m.local(v)))
		m.callv("Use", []types.TypeID{types.U64}, argCopy(m.local(r)))
		m.end(r, v)
		m.ret(mir.ExitNormal)
		m.expect(t, m.check(t), diag.E0502, s)
	})
	t.Run("write after the loan's last use", func(t *testing.T) {
		k := newKit(t)
		m := k.body(k.sig("F", nil, types.Void, types.EmptyErrors))
		v := m.user("v", types.U64, true)
		m.set(v, use(imm(types.U64, 1)))
		r := m.user("r", k.tab.Ref(types.U64), false)
		m.set(r, ref(m.local(v)))
		m.callv("Use", []types.TypeID{types.U64}, argCopy(m.local(r)))
		m.asmStmt(asmOut(m.local(v)))
		m.end(r, v)
		m.ret(mir.ExitNormal)
		m.expect(t, m.check(t))
	})
}
