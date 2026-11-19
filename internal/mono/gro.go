// Why: a bin built general-regs-only (a kernel, a freestanding image) has
// no FPU or vector state to use, so a float or vector value in its code is a
// build error, found per instance.
//
// What: the E1108 check: at the first local, parameter or result of type
// f32, f64, a Vec or a Mask (or an aggregate holding one by value), or an asm
// operand in an xmm, ymm or zmm register, with the instance, the bin and the
// call path from the entry.
//
// How: an instance whose cpu set holds sse2 saves and restores the FPU
// state itself, so only its own parameters and result are judged: the
// general-regs-only caller has no register to pass a float in.

package mono

import (
	"slices"
	"strings"

	"mk/internal/diag"
	"mk/internal/mir"
	"mk/internal/types"
)

// generalRegs judges node i, once per instance.
func (c *collector) generalRegs(i int) {
	if !c.opt.GeneralRegsOnly {
		return
	}
	in := &c.nodes[i].inst
	b := in.Body
	if b == nil {
		return
	}
	sse2 := slices.Contains(c.ownCPU(in.Item), "sse2")
	if !sse2 && slices.Contains(in.CPU, "sse2") {
		// sse2 inherited: a direct call from a function that has it
		// created this instance, so every caller has the registers and
		// saves the FPU state; nothing in it is judged.
		return
	}
	report := func(span mir.SpanID, what string) {
		d := diag.New(diag.E1108, b.Span(span), what+" in a general-regs-only bin: the function has no SSE register to use").
			Text("instance", in.Name).Text("path", c.callPath(i))
		if c.opt.Bin != "" {
			d.Text("bin", c.opt.Bin)
		}
		d.Alt("give the function `cpu \"sse2\"` and save and restore the FPU state in it, or compute in integers")
		c.diags = append(c.diags, d)
	}
	n := len(b.Locals)
	if sse2 {
		n = int(b.ParamCount) + 1 // the result and the parameters only
	}
	for l := 0; l < n; l++ {
		if t := b.Locals[l].Type; c.holdsFloat(t, 0) {
			report(b.Locals[l].Span, "a value of type `"+c.env.Table().TypeString(t, nil)+"`")
			return
		}
	}
	if sse2 {
		return
	}
	for bi := range b.Blocks {
		for _, st := range b.Blocks[bi].Stmts {
			if st.Kind != mir.StAsm {
				continue
			}
			for _, op := range b.Asms[st.Asm].Operands {
				r := strings.ToLower(op.Reg)
				if r == "xmm_reg" || r == "ymm_reg" || r == "zmm_reg" || op.Explicit && (strings.HasPrefix(r, "xmm") || strings.HasPrefix(r, "ymm") || strings.HasPrefix(r, "zmm")) {
					report(st.Span, "an asm operand in `"+op.Reg+"`")
					return
				}
			}
		}
	}
}

// holdsFloat reports whether t is or holds by value f32, f64, a std.simd
// Vec or Mask (through arrays, records, optionals, structs and enum
// payloads; never through a pointer or reference).
func (c *collector) holdsFloat(t types.TypeID, depth int) bool {
	if depth > 32 {
		return false
	}
	tab := c.env.Table()
	ty := tab.Get(t)
	switch ty.Kind {
	case types.KFloat:
		return true
	case types.KArray, types.KOptional:
		return c.holdsFloat(ty.Elem, depth+1)
	case types.KErrorUnion:
		return c.holdsFloat(ty.Elem, depth+1)
	case types.KRecord:
		for _, f := range ty.Fields {
			if c.holdsFloat(f.Type, depth+1) {
				return true
			}
		}
	case types.KNamed:
		it := c.env.Items().Get(ty.Item)
		if k := it.Key.String(); k == "std.simd.Vec" || k == "std.simd.Mask" {
			return true
		}
		switch d := it.Decl.(type) {
		case *types.StructDecl:
			for _, f := range d.Fields {
				if c.holdsFloat(Subst(tab, f.Type, ty.Args), depth+1) {
					return true
				}
			}
		case *types.EnumDecl:
			for _, v := range d.Variants {
				for _, f := range v.Fields {
					if c.holdsFloat(Subst(tab, f.Type, ty.Args), depth+1) {
						return true
					}
				}
			}
		}
	}
	return false
}

// callPath is the chain of first requesters from the root to node i, by
// instance name: "Main -> Run -> Mix".
func (c *collector) callPath(i int) string {
	var names []string
	for n := i; n >= 0 && len(names) < 64; n = c.nodes[n].parent {
		name := c.nodes[n].inst.Name
		if name == "" {
			name = c.env.Items().Get(c.nodes[n].inst.Item).Key.String()
		}
		names = append(names, name)
	}
	for l, r := 0, len(names)-1; l < r; l, r = l+1, r-1 {
		names[l], names[r] = names[r], names[l]
	}
	return strings.Join(names, " -> ")
}
