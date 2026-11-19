// Why: an asm operand bound to a const parameter has no value until the
// generic function is instantiated, but codegen wants every const operand
// as literal text in the template.
//
// What: the per-instance pass that turns such operands into literals.
//
// How: lower keeps the operand's hole and passes the parameter in In. Each
// instance knows the value, so mono pastes its decimal text into the
// template in canonical form, sets Const and clears In; codegen then sees
// the body as if the operand had been a literal.

package mono

import (
	"slices"
	"strconv"
	"strings"

	"mk/internal/mir"
	"mk/internal/types"
)

// asmOperands substitutes the operands of b's asm statements (types and
// inputs, as every other operand of the body; a copy of a const value
// parameter becomes its constant) and resolves each const operand that names
// a const parameter. b is the instance's clone: its Asms are copied first,
// since cloneBody shares them with the generic body.
func (bl *builder) asmOperands(b *mir.Body) {
	if len(b.Asms) == 0 {
		return
	}
	b.Asms = slices.Clone(b.Asms)
	for ai := range b.Asms {
		a := &b.Asms[ai]
		a.Operands = slices.Clone(a.Operands)
		for oi := range a.Operands {
			op := &a.Operands[oi]
			if op.Kind == mir.AsmSym {
				continue
			}
			op.Type = bl.s.typ(op.Type)
			switch op.Kind {
			case mir.AsmIn, mir.AsmInOut, mir.AsmInLateOut:
				bl.operand(&op.In)
			case mir.AsmConst:
				if !asmParamConst(op) {
					continue
				}
				bl.operand(&op.In)
				bits, ok := bl.asmConstBits(b, op.In)
				if !ok {
					ice("asm const operand %d: the const parameter has no value in the instance", oi)
				}
				id, text := asmConstValue(bl.tab, bl.env.Consts(), op.Type, bits)
				op.Const, op.In = id, mir.Operand{}
				if a.Options&mir.AsmRaw == 0 {
					a.Template = pasteAsmConst(a.Template, oi, text)
				}
			}
		}
	}
}

// asmParamConst reports a const operand lower left for mono: its In names
// the parameter (a literal's In is the zero operand, a copy of local 0).
func asmParamConst(op *mir.AsmOperand) bool {
	return op.In != (mir.Operand{})
}

// asmConstBits is the value of an asm const operand's input after
// substitution: an immediate or const, or a temporary assigned one (a
// generic `const N` reaches the asm through RvConstArg, which became an
// RvUse of an immediate).
func (bl *builder) asmConstBits(b *mir.Body, o mir.Operand) (uint64, bool) {
	switch o.Kind {
	case mir.OpImm:
		return o.Bits, true
	case mir.OpConst:
		v := bl.env.Consts().Get(o.Const)
		if len(v.Relocs) != 0 || len(v.Bytes) > 8 {
			return 0, false
		}
		return types.LoadUint(v.Bytes, len(v.Bytes)), true
	case mir.OpCopy, mir.OpMove:
		if o.Place.Proj.Len != 0 {
			return 0, false
		}
		for bi := range b.Blocks {
			for _, st := range b.Blocks[bi].Stmts {
				if st.Kind == mir.StAssign && st.Place.Local == o.Place.Local && st.Place.Proj.Len == 0 &&
					st.Rvalue.Kind == mir.RvUse && (st.Rvalue.A.Kind == mir.OpImm || st.Rvalue.A.Kind == mir.OpConst) {
					return bl.asmConstBits(b, st.Rvalue.A)
				}
			}
		}
	}
	return 0, false
}

// asmConstValue interns an integer constant of type t and spells it in
// decimal, signed types with their sign (lower's asmConst).
func asmConstValue(tab *types.Table, ct *types.ConstTable, t types.TypeID, bits uint64) (types.ConstID, string) {
	ty := tab.Get(t)
	size, signed := 8, ty.Signed
	if ty.Kind == types.KInt {
		size = int(ty.Bits / 8)
	}
	if size < 8 {
		bits &= 1<<(8*size) - 1
	}
	n := make([]byte, size)
	types.StoreUint(n, size, bits)
	id := ct.Intern(types.ConstValue{Type: t, Align: uint32(size), Bytes: n})
	text := strconv.FormatUint(bits, 10)
	switch {
	case signed && size < 8 && bits&(1<<(8*size-1)) != 0:
		text = strconv.FormatInt(int64(bits)-int64(1)<<(8*size), 10)
	case signed && size == 8:
		text = strconv.FormatInt(int64(bits), 10)
	}
	return id, text
}

// pasteAsmConst replaces every hole {k} and {k:mod} of the canonical
// template with text, keeping {{ and }}.
func pasteAsmConst(tmpl string, k int, text string) string {
	var sb strings.Builder
	for i := 0; i < len(tmpl); i++ {
		c := tmpl[i]
		switch {
		case (c == '{' || c == '}') && i+1 < len(tmpl) && tmpl[i+1] == c:
			sb.WriteByte(c)
			sb.WriteByte(c)
			i++
		case c == '{':
			j := strings.IndexByte(tmpl[i+1:], '}')
			if j < 0 {
				sb.WriteString(tmpl[i:])
				return sb.String()
			}
			hole := tmpl[i+1 : i+1+j]
			num, _, _ := strings.Cut(hole, ":")
			if n, err := strconv.Atoi(num); err == nil && n == k {
				sb.WriteString(text)
			} else {
				sb.WriteString("{" + hole + "}")
			}
			i += j + 1
		default:
			sb.WriteByte(c)
		}
	}
	return sb.String()
}
