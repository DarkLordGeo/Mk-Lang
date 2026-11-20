// Why: an asm operand can name a register class or register the build
// target lacks, need a CPU feature the function does not enable, or carry a
// type the register cannot hold; these are errors only once the target and
// the instance's feature set are known.
//
// What: the check of every asm operand of a built instance body against the
// target's register table, reporting E1104 (register, feature, class,
// reserved, or no-register-table for a target without a table). The
// aarch64 table is in asmregs_aarch64.go.
//
// How: a feature counts when it is in the target's baseline or the
// instance's cpu set. Only the asm statements still in the body's blocks
// are judged: code under an arch test false for the target was pruned
// before this walk.

package mono

import (
	"strconv"
	"strings"

	"mk/internal/diag"
	"mk/internal/mir"
	"mk/internal/types"
)

// regKind is a register file of x86_64's table.
type regKind uint8

const (
	rkNone regKind = iota
	rkGPR          // reg, and rax..r15 with their parts
	rkByte         // reg_byte
	rkXMM          // xmm_reg, xmm0..xmm31
	rkYMM          // ymm_reg, ymm0..ymm31
	rkZMM          // zmm_reg, zmm0..zmm31
	rkK            // kreg, k1..k7
)

var asmClassKinds = map[string]regKind{"reg": rkGPR, "reg_byte": rkByte, "xmm_reg": rkXMM, "ymm_reg": rkYMM, "zmm_reg": rkZMM, "kreg": rkK}

// gprNames are the general registers and their parts (rbx, rbp, rsp and
// rip are check's E1104 reserved).
var gprNames = func() map[string]bool {
	m := map[string]bool{}
	for _, s := range strings.Fields("rax eax ax al ah rcx ecx cx cl ch rdx edx dx dl dh rsi esi si sil rdi edi di dil") {
		m[s] = true
	}
	for i := 8; i <= 15; i++ {
		n := "r" + strconv.Itoa(i)
		m[n], m[n+"d"], m[n+"w"], m[n+"b"] = true, true, true, true
	}
	return m
}()

// explicitKind is the register file of an explicit register name, and
// whether it needs avx512f (xmm16 and up).
func explicitKind(r string) (regKind, bool) {
	if gprNames[r] {
		return rkGPR, false
	}
	for _, p := range []struct {
		prefix string
		kind   regKind
	}{{"xmm", rkXMM}, {"ymm", rkYMM}, {"zmm", rkZMM}} {
		if n, ok := regNumber(r, p.prefix); ok && n <= 31 {
			return p.kind, n >= 16
		}
	}
	if n, ok := regNumber(r, "k"); ok && n >= 1 && n <= 7 {
		return rkK, false
	}
	return rkNone, false
}

func regNumber(r, prefix string) (int, bool) {
	if !strings.HasPrefix(r, prefix) || len(r) == len(prefix) {
		return 0, false
	}
	n, err := strconv.Atoi(r[len(prefix):])
	return n, err == nil && n >= 0
}

// kindFeature is the feature a register file needs.
func kindFeature(k regKind, high bool) string {
	switch {
	case k == rkZMM || k == rkK || high:
		return "avx512f"
	case k == rkYMM:
		return "avx"
	case k == rkXMM:
		return "sse"
	}
	return ""
}

// asmRegisters judges node i's asm statements.
func (c *collector) asmRegisters(i int) {
	in := &c.nodes[i].inst
	b := in.Body
	if b == nil || len(b.Asms) == 0 {
		return
	}
	// Only the asm statements of the body's blocks are judged, never the
	// whole Asms table: an entry whose statement is gone (a block pruned
	// per instance) is not live code.
	var live []mir.Statement
	for bi := range b.Blocks {
		for _, st := range b.Blocks[bi].Stmts {
			if st.Kind == mir.StAsm {
				live = append(live, st)
			}
		}
	}
	var span mir.SpanID
	report := func(a *mir.Asm, reason, msg, reg string) *diag.Diagnostic {
		d := diag.New(diag.E1104, b.Span(span), msg).Text("reason", reason).Text("instance", in.Name)
		if reg != "" {
			d.Text("register", reg)
		}
		c.diags = append(c.diags, d)
		return d
	}
	features := map[string]bool{}
	for _, f := range types.CPUBaseline(c.opt.Target) {
		features[f] = true
	}
	for _, f := range in.CPU { // the instance's set, inherited features included
		features[f] = true
	}
	for _, st := range live {
		a := &b.Asms[st.Asm]
		span = st.Span
		if arm64Target(c.opt.Target) {
			c.arm64Registers(a, func(a *mir.Asm, reason, msg, reg string) { report(a, reason, msg, reg) })
			continue
		}
		if c.opt.Target.Arch != types.ArchX64 {
			report(a, "no-register-table", "asm has no register table on "+c.opt.Target.Name+" in 1.0", "")
			continue
		}
		for oi := range a.Operands {
			op := &a.Operands[oi]
			if op.Reg == "" {
				continue
			}
			kind, high := asmClassKinds[op.Reg], false
			if op.Explicit {
				kind, high = explicitKind(op.Reg)
			}
			if kind == rkNone {
				what := "class"
				if op.Explicit {
					what = "register"
				}
				report(a, "register", "`"+op.Reg+"` is no x86_64 register "+what, op.Reg)
				continue
			}
			if f := kindFeature(kind, high); f != "" && !features[f] {
				report(a, "feature", "`"+op.Reg+"` needs the feature "+f+", which this function's context lacks: add cpu \""+f+"\"", op.Reg).
					Text("feature", f)
				continue
			}
			if !c.regFits(kind, op) {
				report(a, "class", "a value of type `"+c.env.Table().TypeString(op.Type, nil)+"` does not fit `"+op.Reg+"`", op.Reg).
					Text("type", c.env.Table().TypeString(op.Type, nil))
			}
		}
	}
}

// regFits reports whether op's type fits the register file: reg holds
// integers of 16 to 64 bits, int, uint and raw pointers (an explicit general
// register also bytes and bool); reg_byte i8, u8 and bool; xmm f32, f64 and
// a Vec or Mask of at most 16 bytes; ymm one of 32; zmm one of 64; kreg u8
// to u64.
func (c *collector) regFits(kind regKind, op *mir.AsmOperand) bool {
	tab := c.env.Table()
	ty := tab.Get(op.Type)
	vecBytes := func() uint64 {
		if ty.Kind != types.KNamed || len(ty.Args) != 2 {
			return 0
		}
		k := c.env.Items().Get(ty.Item).Key.String()
		if k != "std.simd.Vec" && k != "std.simd.Mask" {
			return 0
		}
		if l := c.env.Layout(op.Type); l != nil {
			return l.Shape.Size
		}
		return 0
	}
	switch kind {
	case rkGPR:
		switch ty.Kind {
		case types.KIntPtr, types.KRawPtr:
			return true
		case types.KInt:
			return ty.Bits >= 16 || op.Explicit
		case types.KBool:
			return op.Explicit
		}
	case rkByte:
		return ty.Kind == types.KInt && ty.Bits == 8 || ty.Kind == types.KBool
	case rkXMM:
		if ty.Kind == types.KFloat {
			return true
		}
		n := vecBytes()
		return n > 0 && n <= 16
	case rkYMM:
		return vecBytes() == 32 || ty.Kind == types.KFloat && op.Explicit
	case rkZMM:
		return vecBytes() == 64 || ty.Kind == types.KFloat && op.Explicit
	case rkK:
		return ty.Kind == types.KInt && !ty.Signed
	}
	return false
}
