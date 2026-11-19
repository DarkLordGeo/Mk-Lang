// Why: inline assembly crosses every layer at once (operand evaluation order,
// places, constants, register classes), so its lowering lives apart from the
// ordinary statements.
//
// What: asmStmt, which lowers an asm statement to one StAsm, and its helpers
// asmDirection, asmConst, asmReg, asmDiscardType and asmCanonical; naked
// reports a naked function's body.
//
// How: the template names operands by their index in the StAsm's operand
// list and holds every const operand's value as decimal text, rewritten from
// check's canonical template by asmCanonical. A naked body is the StAsm, then
// unreachable.

package lower

import (
	"strconv"
	"strings"

	"mk/internal/ast"
	"mk/internal/mir"
	"mk/internal/source"
	"mk/internal/types"
)

// asmKinds maps ast's direction, const and sym kinds to mir's.
var asmKinds = map[ast.AsmOperandKind]mir.AsmOperandKind{
	ast.AsmIn: mir.AsmIn, ast.AsmOut: mir.AsmOut, ast.AsmLateOut: mir.AsmLateOut,
	ast.AsmInOut: mir.AsmInOut, ast.AsmInLateOut: mir.AsmInLateOut,
	ast.AsmConst: mir.AsmConst, ast.AsmSym: mir.AsmSym,
}

// naked reports whether the body is a naked function's.
func (bl *builder) naked() bool {
	if bl.kind != BodyFunc {
		return false
	}
	fd, ok := bl.cfg.Items.Get(bl.in.Item()).Decl.(*types.FuncDecl)
	return ok && fd.Naked
}

// asmStmt lowers s: in values are evaluated left to right into operands, as
// call arguments are (a place read is slotted when a later operand's
// evaluation could write it); out places are places; inout x is In copy x
// and Out x; a const operand is its constant and its decimal text in the
// template; a sym operand is its item. clobber_abi and
// options are the Asm's own fields. A noreturn asm, and every asm of a naked
// body, ends its block with TermUnreachable.
func (bl *builder) asmStmt(s *ast.AsmStmt) {
	text, opts := bl.in.AsmTemplate(s)
	a := mir.Asm{Options: mir.AsmOptions(opts), Span: bl.span(s.Span())}
	if bl.naked() {
		a.Options |= mir.AsmNoreturn // implicitly noreturn
	}
	// index maps an ast operand to its mir operand (-1 for clobber_abi and
	// options); consts holds a const operand's decimal text.
	index := make([]int, len(s.Operands))
	consts := make([]string, len(s.Operands))
	var later []ast.Expr
	for i, op := range s.Operands {
		index[i] = -1
		if op.Kind == ast.AsmClobberABI {
			if r, ok := asmString(op.ABI); ok {
				a.ClobberABI = r
			}
			continue
		}
		if op.Kind == ast.AsmOptions {
			continue
		}
		index[i] = len(a.Operands)
		mo := mir.AsmOperand{Kind: asmKinds[op.Kind]}
		switch op.Kind {
		case ast.AsmConst:
			if bl.constParamRef(op.Value) {
				// A const parameter: its value is the instance's, so the
				// hole stays and In carries the parameter; mono pastes the
				// value and sets Const (mono instance.go asmConsts). Const is
				// a zero of the type until then (the MIR verifier wants one).
				mo.In = bl.operand(op.Value)
				mo.Type = bl.asmOperandType(mo.In, op.Value)
				size, _ := intShape(bl.tab, mo.Type)
				mo.Const = bl.cfg.Consts.Intern(types.ConstValue{Type: mo.Type, Align: uint32(max(size, 1)), Bytes: make([]byte, size)})
				break
			}
			mo.Const, mo.Type, consts[i] = bl.asmConst(op.Value)
		case ast.AsmSym:
			u := bl.in.Use(op.Sym.ID())
			if u.Item == types.NoItem {
				source.ICEf(op.Span(), "lower: sym operand names no item")
			}
			mo.Sym = u.Item
		default:
			mo.Reg, mo.Explicit = bl.asmReg(op.Reg)
			later = later[:0]
			for _, o := range s.Operands[i+1:] {
				if o.Kind.IsDirection() && o.Value != nil && !o.Discard {
					later = append(later, o.Value)
				}
			}
			bl.asmDirection(op, &mo, later)
		}
		a.Operands = append(a.Operands, mo)
	}
	if a.Options&mir.AsmRaw == 0 {
		a.Template = asmCanonical(text, index, consts)
	} else {
		a.Template = text
	}
	id := bl.b.AddAsm(a)
	bl.emit(mir.Statement{Kind: mir.StAsm, Asm: id, Span: bl.span(s.Span())})
	if a.Options&mir.AsmNoreturn != 0 {
		bl.term(mir.Terminator{Kind: mir.TermUnreachable}, s.Span())
	}
}

// constParamRef reports whether e names a const parameter: a generic
// `const N: int` (UseConstParam) or a const value parameter (UseParam of a
// Const parameter), the const operands mono resolves per instance.
func (bl *builder) constParamRef(e ast.Expr) bool {
	u := bl.in.Use(unparen(e).ID())
	switch u.Kind {
	case UseConstParam:
		return true
	case UseParam:
		return bl.sig != nil && u.Param >= 1 && int(u.Param) <= len(bl.sig.Params) && bl.sig.Params[u.Param-1].Const
	}
	return false
}

// asmDirection fills a direction operand: In for the three reading kinds,
// Out for the four writing ones ("_" leaves Out.Local NoLocal, typed by its
// class), Type the value's or place's type.
func (bl *builder) asmDirection(op *ast.AsmOperand, mo *mir.AsmOperand, later []ast.Expr) {
	if op.Discard || op.Value == nil {
		mo.Out = mir.Place{Local: mir.NoLocal}
		mo.Type = asmDiscardType(mo.Reg)
		if mo.Kind == mir.AsmIn || mo.Kind == mir.AsmInOut || mo.Kind == mir.AsmInLateOut {
			source.ICEf(op.Span(), "lower: an asm input with no value")
		}
		return
	}
	if mo.Kind == mir.AsmIn {
		v := bl.operand(op.Value)
		if bl.readsLate(v) && !bl.inertAfter(later, v.Place) {
			v = bl.slot(v, op.Value.Span())
		}
		mo.In, mo.Type = v, bl.asmOperandType(v, op.Value)
		return
	}
	p := bl.mutPlace(op.Value)
	mo.Out, mo.Type = p, bl.b.PlaceType(p)
	if mo.Kind == mir.AsmInOut || mo.Kind == mir.AsmInLateOut {
		mo.In = mir.Copy(p)
	}
}

// asmOperandType is an in operand's type: its place's, else check's.
func (bl *builder) asmOperandType(v mir.Operand, e ast.Expr) types.TypeID {
	switch v.Kind {
	case mir.OpCopy, mir.OpMove:
		return bl.b.PlaceType(v.Place)
	case mir.OpImm, mir.OpConst:
		if v.Type != types.NoType {
			return v.Type
		}
	}
	return bl.typeOf(e)
}

// asmDiscardType types a discarded output ("_") by its class: the widest
// value the class holds that every target has (an explicit register is a
// general one unless its name says xmm, ymm, zmm or k).
func asmDiscardType(reg string) types.TypeID {
	r := strings.ToLower(reg)
	switch {
	case r == "reg_byte":
		return types.U8
	case r == "xmm_reg" || r == "ymm_reg" || r == "zmm_reg",
		strings.HasPrefix(r, "xmm"), strings.HasPrefix(r, "ymm"), strings.HasPrefix(r, "zmm"):
		return types.F64
	}
	return types.U64
}

// asmConst is a const operand's constant, its integer type and its decimal
// text (the value is pasted into the template).
func (bl *builder) asmConst(e ast.Expr) (types.ConstID, types.TypeID, string) {
	l := bl.in.Literal(e.ID())
	t := bl.typeOf(e)
	if t == types.NoType || t == types.Poison {
		t = l.Type
	}
	var bits uint64
	switch l.Kind {
	case LitScalar:
		bits = l.Bits
	case LitConst:
		v := bl.cfg.Consts.Get(l.Const)
		if len(v.Relocs) != 0 || len(v.Bytes) > 8 {
			source.ICEf(e.Span(), "lower: a const operand is no integer constant")
		}
		bits = types.LoadUint(v.Bytes, len(v.Bytes))
	default:
		source.ICEf(e.Span(), "lower: a const operand has no value (check records it)")
	}
	size, signed := intShape(bl.tab, t)
	if size == 0 {
		source.ICEf(e.Span(), "lower: a const operand of type %s", bl.tab.TypeString(t, nil))
	}
	if size < 8 {
		bits &= 1<<(8*size) - 1
	}
	n := make([]byte, size)
	types.StoreUint(n, size, bits)
	id := bl.cfg.Consts.Intern(types.ConstValue{Type: t, Align: uint32(size), Bytes: n})
	text := strconv.FormatUint(bits, 10)
	if signed && size < 8 && bits&(1<<(8*size-1)) != 0 {
		text = strconv.FormatInt(int64(bits)-int64(1)<<(8*size), 10)
	} else if signed && size == 8 {
		text = strconv.FormatInt(int64(bits), 10)
	}
	return id, t, text
}

// intShape is an integer type's size in bytes and signedness; 0 for any
// other type.
func intShape(tab *types.Table, t types.TypeID) (int, bool) {
	ty := tab.Get(t)
	switch ty.Kind {
	case types.KInt:
		return int(ty.Bits / 8), ty.Signed
	case types.KIntPtr:
		return 8, ty.Signed
	}
	return 0, false
}

// asmReg is a direction operand's class or register: an Ident class, or a
// string literal naming a register (Explicit).
func (bl *builder) asmReg(n ast.Node) (string, bool) {
	switch r := n.(type) {
	case *ast.Ident:
		return bl.cfg.Interner.Str(r.Name), false
	case *ast.BasicLit:
		if s, ok := asmString(r); ok {
			return strings.ToLower(s), true
		}
	}
	return "", false
}

// asmString decodes a plain or raw string literal.
func asmString(l *ast.BasicLit) (string, bool) {
	if l == nil {
		return "", false
	}
	s := l.Raw
	switch {
	case strings.HasPrefix(s, `r"`) && strings.HasSuffix(s, `"`) && len(s) >= 3:
		return s[2 : len(s)-1], true
	case strings.HasPrefix(s, `"`) && strings.HasSuffix(s, `"`) && len(s) >= 2:
		if u, err := strconv.Unquote(s); err == nil {
			return u, true
		}
		return s[1 : len(s)-1], true
	}
	return "", false
}

// asmCanonical rewrites check's canonical template, whose holes name ast
// operand indices, to mir's: {i} and {i:mod} name the mir operand, a const
// operand's hole is its decimal text, and {{ and }} are kept.
func asmCanonical(text string, index []int, consts []string) string {
	var sb strings.Builder
	for i := 0; i < len(text); i++ {
		c := text[i]
		switch {
		case (c == '{' || c == '}') && i+1 < len(text) && text[i+1] == c:
			sb.WriteByte(c)
			sb.WriteByte(c)
			i++
		case c == '{':
			j := strings.IndexByte(text[i+1:], '}')
			if j < 0 {
				sb.WriteString(text[i:])
				return sb.String()
			}
			hole := text[i+1 : i+1+j]
			num, mod, _ := strings.Cut(hole, ":")
			k, err := strconv.Atoi(num)
			switch {
			case err != nil || k < 0 || k >= len(index):
				sb.WriteString("{" + hole + "}")
			case consts[k] != "":
				sb.WriteString(consts[k])
			default:
				sb.WriteString("{" + strconv.Itoa(index[k]))
				if mod != "" {
					sb.WriteString(":" + mod)
				}
				sb.WriteByte('}')
			}
			i += j + 1
		default:
			sb.WriteByte(c)
		}
	}
	return sb.String()
}
