// Why: some intrinsics (syscall, TEB and stack-pointer reads), Mk's asm
// statements and the target-support routines need assembly that IR cannot
// spell.
//
// What: InlineAsm, InlineAsmDialect and ModuleAsm, with AsmFlags.
//
// How: assembly text is escaped as LLVM strings are (\XX for '"', a
// backslash and non-printable bytes). InlineAsmDialect adds the
// alignstack and inteldialect flags and call-site function attributes.

package llvm

// ModuleAsm appends module assembly: one module asm "..." line per line of
// text, in call order.
func (m *Module) ModuleAsm(text string) {
	start := 0
	for i := 0; i <= len(text); i++ {
		if i == len(text) || text[i] == '\n' {
			if i > start || i < len(text) {
				m.asm = append(m.asm, text[start:i])
			}
			start = i + 1
		}
	}
}

// InlineAsm calls inline assembly of type fnTy (non-variadic) with the
// given constraint string; sideEffect adds sideeffect. The result is the
// zero Value for a void fnTy.
func (b *Block) InlineAsm(fnTy Type, asm, constraints string, sideEffect bool, args []Value, md ...Attach) Value {
	if !fnTy.IsFunc() || fnTy.Variadic() {
		panic("llvm: InlineAsm type " + fnTy.String())
	}
	b.checkCall(fnTy, args, nil, "InlineAsm")
	var v Value
	if fnTy.Ret().IsVoid() {
		b.begin(md)
	} else {
		v = b.beginValue(fnTy.Ret(), md)
	}
	b.str("call ")
	b.typ(fnTy.Ret())
	if sideEffect {
		b.str(" asm sideeffect ")
	} else {
		b.str(" asm ")
	}
	b.text = appendQuoted(b.text, asm)
	b.str(", ")
	b.text = appendQuoted(b.text, constraints)
	b.args(args, nil)
	b.end(md)
	return v
}

// AsmFlags are the inline-asm flags of an asm statement.
type AsmFlags struct {
	SideEffect bool // sideeffect: unless options(pure)
	AlignStack bool // alignstack: unless options(nostack)
	Intel      bool // inteldialect: unless options(att)
}

// InlineAsmDialect is InlineAsm with the alignstack and inteldialect flags
// and call-site function attributes (memory(none), memory(read), noreturn,
// nounwind), for an asm statement.
func (b *Block) InlineAsmDialect(fnTy Type, asm, constraints string, fl AsmFlags, args []Value, fnAttrs Attrs, md ...Attach) Value {
	if !fnTy.IsFunc() || fnTy.Variadic() {
		panic("llvm: InlineAsmDialect type " + fnTy.String())
	}
	b.f.m.checkAttrs(fnAttrs, "InlineAsmDialect")
	b.checkCall(fnTy, args, nil, "InlineAsmDialect")
	var v Value
	if fnTy.Ret().IsVoid() {
		b.begin(md)
	} else {
		v = b.beginValue(fnTy.Ret(), md)
	}
	b.str("call ")
	b.typ(fnTy.Ret())
	b.str(" asm ")
	if fl.SideEffect {
		b.str("sideeffect ")
	}
	if fl.AlignStack {
		b.str("alignstack ")
	}
	if fl.Intel {
		b.str("inteldialect ")
	}
	b.text = appendQuoted(b.text, asm)
	b.str(", ")
	b.text = appendQuoted(b.text, constraints)
	b.args(args, nil)
	b.text = appendSpAttrs(b.text, fnAttrs)
	b.end(md)
	return v
}
