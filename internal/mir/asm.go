// Why: inline assembly is one MIR statement whose operands, options and
// ABI need their own payload, checks and text form, kept apart from the
// ordinary statements.
//
// What: AsmID, Asm, AsmOperandKind, AsmOperand and AsmOptions, Body.AddAsm,
// the validator's asm and asmType (invariant M13), and Printer.asm.
//
// How: a StAsm statement holds an AsmID into the Body's Asms. The validator
// accepts only scalar operand types (integers, bool, floats, raw pointers,
// std.simd vectors and masks), and a noreturn asm must be its block's last
// statement before TermUnreachable.

package mir

import (
	"strconv"
	"strings"

	"mk/internal/types"
)

// AsmID indexes Body.Asms.
type AsmID uint32

// Asm is one inline-assembly statement (StAsm). Template has its holes
// resolved to operand indices ({0}, {1:e}); {{ and }} are kept as written.
type Asm struct {
	Template   string
	Operands   []AsmOperand
	ClobberABI string // "", "C", "sysv64" or "win64"
	Options    AsmOptions
	Span       SpanID
}

// AsmOperandKind tags an AsmOperand: ast's first seven kinds, in its order
// (clobber_abi and options are Asm's own fields here).
type AsmOperandKind uint8

const (
	AsmIn AsmOperandKind = iota + 1
	AsmOut
	AsmLateOut
	AsmInOut
	AsmInLateOut
	AsmConst
	AsmSym
)

var asmKindWords = [...]string{"", "in", "out", "lateout", "inout", "inlateout", "const", "sym"}

func (k AsmOperandKind) String() string {
	if k != 0 && int(k) < len(asmKindWords) {
		return asmKindWords[k]
	}
	return "asmoperand(" + strconv.Itoa(int(k)) + ")"
}

// reads and writes say which of In and Out a kind uses.
func (k AsmOperandKind) reads() bool { return k == AsmIn || k == AsmInOut || k == AsmInLateOut }
func (k AsmOperandKind) writes() bool {
	return k == AsmOut || k == AsmLateOut || k == AsmInOut || k == AsmInLateOut
}

// AsmOperand is one operand of an Asm. Which fields are meaningful depends
// on Kind: In for AsmIn, AsmInOut and AsmInLateOut; Out for the four kinds
// that write (Out.Local is NoLocal for "_"); Const for AsmConst, an integer
// const; Sym for AsmSym, a non-generic function, a var or an extern var. Reg
// is a class name ("reg", "xmm_reg") or, with Explicit, a register ("rax"),
// set for the five directions. Type is the operand's value type (NoType for
// AsmSym).
type AsmOperand struct {
	Kind     AsmOperandKind
	Reg      string
	Explicit bool
	In       Operand
	Out      Place
	Const    types.ConstID
	Sym      types.ItemID
	Type     types.TypeID
}

// AsmOptions is the set of an asm's options(...) names.
type AsmOptions uint16

const (
	AsmPure AsmOptions = 1 << iota
	AsmNomem
	AsmReadonly
	AsmPreservesFlags
	AsmNostack
	AsmNoreturn
	AsmAtt
	AsmRaw

	asmOptionsAll = AsmRaw<<1 - 1
)

var asmOptionWords = [...]string{"pure", "nomem", "readonly", "preserves_flags", "nostack", "noreturn", "att", "raw"}

// String lists the options as the source spells them, comma-separated.
func (o AsmOptions) String() string {
	var s []string
	for i, w := range asmOptionWords {
		if o&(1<<i) != 0 {
			s = append(s, w)
		}
	}
	if rest := o &^ asmOptionsAll; rest != 0 {
		s = append(s, "options("+strconv.Itoa(int(rest))+")")
	}
	return strings.Join(s, ", ")
}

// AddAsm appends an Asm and returns its ID, for a StAsm statement's Asm.
func (b *Body) AddAsm(a Asm) AsmID {
	b.Asms = append(b.Asms, a)
	return AsmID(len(b.Asms) - 1)
}

// asm checks M13 for the StAsm statement at index si of the current block: the
// Asm exists; every operand's fields fit its kind; In, Out and Const types
// equal the operand's Type, which is a type an asm operand may have (an
// integer, int or uint, bool, f32 or f64, a raw pointer, or a std.simd Vec or
// Mask, told apart only when Env.Known names them; never a reference, slice,
// str, fn value, aggregate or type parameter); a Sym is set exactly on AsmSym;
// the ABI is one of the four names and the options are known bits; and a
// noreturn asm is its block's last statement before TermUnreachable.
func (v *validator) asm(st *Statement, si int) {
	b, tab := v.b, v.tab
	if int(st.Asm) >= len(b.Asms) {
		v.fail("M2", "asm %d out of range", st.Asm)
	}
	a := &b.Asms[st.Asm]
	v.span(a.Span)
	switch a.ClobberABI {
	case "", "C", "sysv64", "win64":
	default:
		v.fail("M13", "clobber_abi %q", a.ClobberABI)
	}
	if a.Options&^asmOptionsAll != 0 {
		v.fail("M13", "unknown options %d", a.Options&^asmOptionsAll)
	}
	if a.Options&AsmNoreturn != 0 {
		bl := &b.Blocks[v.curBlock]
		if si != len(bl.Stmts)-1 || bl.Term.Kind != TermUnreachable {
			v.fail("M13", "a noreturn asm is not its block's last statement before unreachable")
		}
	}
	for i, op := range a.Operands {
		if op.Kind < AsmIn || op.Kind > AsmSym {
			v.fail("M13", "operand %d has kind %d", i, op.Kind)
		}
		dir := op.Kind.reads() || op.Kind.writes()
		if dir != (op.Reg != "") || (op.Explicit && !dir) {
			v.fail("M13", "operand %d (%v) register %q", i, op.Kind, op.Reg)
		}
		if (op.Kind == AsmSym) != (op.Sym != types.NoItem) {
			v.fail("M13", "operand %d (%v) sym %d", i, op.Kind, op.Sym)
		}
		if (op.Kind == AsmConst) != (op.Const != types.NoConst) {
			v.fail("M13", "operand %d (%v) const %d", i, op.Kind, op.Const)
		}
		if op.Kind == AsmSym {
			continue
		}
		v.typeOK(op.Type)
		if !v.asmType(op.Type) || (op.Kind == AsmConst && !isIntKind(tab.Kind(op.Type))) {
			v.fail("M13", "operand %d (%v) of type %s", i, op.Kind, v.ts(op.Type))
		}
		if op.Kind.reads() {
			if t := v.operand(op.In); t != op.Type {
				v.fail("M13", "operand %d reads %s, its type is %s", i, v.ts(t), v.ts(op.Type))
			}
		}
		if op.Kind.writes() && op.Out.Local != NoLocal {
			if t := v.place(op.Out); t != op.Type {
				v.fail("M13", "operand %d writes a place of %s, its type is %s", i, v.ts(t), v.ts(op.Type))
			}
		}
	}
}

func isIntKind(k types.TypeKind) bool { return k == types.KInt || k == types.KIntPtr }

// asmType reports whether t may be an asm operand's type.
func (v *validator) asmType(t types.TypeID) bool {
	ty := v.tab.Get(t)
	switch ty.Kind {
	case types.KInt, types.KIntPtr, types.KBool, types.KFloat, types.KRawPtr:
		return true
	case types.KNamed:
		k := v.env.Known
		if k == nil || (k.Vec == types.NoItem && k.Mask == types.NoItem) {
			return true
		}
		return ty.Item == k.Vec || ty.Item == k.Mask
	}
	return false
}

func (p *Printer) asm(b *Body, st *Statement) string {
	if int(st.Asm) >= len(b.Asms) {
		return "asm ?"
	}
	a := &b.Asms[st.Asm]
	parts := []string{strconv.Quote(a.Template)}
	for _, op := range a.Operands {
		s := op.Kind.String()
		if op.Reg != "" {
			r := op.Reg
			if op.Explicit {
				r = strconv.Quote(r)
			}
			s += "(" + r + ")"
		}
		switch {
		case op.Kind == AsmConst:
			s += " " + p.Types.ConstString(op.Const)
		case op.Kind == AsmSym:
			s += " " + p.item(op.Sym)
		}
		if op.Kind.reads() {
			s += " " + p.op(b, op.In)
		}
		if op.Kind.writes() {
			out := "_"
			if op.Out.Local != NoLocal {
				out = p.PlaceString(b, op.Out)
			}
			if op.Kind.reads() {
				s += " =>"
			}
			s += " " + out
		}
		parts = append(parts, s)
	}
	if a.ClobberABI != "" {
		parts = append(parts, "clobber_abi("+strconv.Quote(a.ClobberABI)+")")
	}
	if a.Options != 0 {
		parts = append(parts, "options("+a.Options.String()+")")
	}
	return "asm " + strings.Join(parts, ", ")
}
