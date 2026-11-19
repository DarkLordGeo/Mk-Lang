// Why: on x86_64 Windows, COFF section flags stop at 8192 bytes of
// alignment, and lld-link places an image section at a multiple of its
// section alignment only, 4096 by default, so a const or var aligned to
// 8192 or more would land off its alignment.
//
// What: bigStatic, staticSection, padStatic, StaticAlign, staticAlignOf and
// unitConsts.
//
// How: such a datum is emitted aligned to a page, in a section of its own
// kind and alignment, its size a multiple of its alignment, and the link
// sets the image's section alignment to the largest (StaticAlign,
// toolchain.LinkJob.SectionAlign).

package codegen

import (
	"math/bits"

	"mk/internal/llvm"
	"mk/internal/mir"
	"mk/internal/source"
	"mk/internal/types"
)

// bigStatic reports whether a static datum aligned to align needs this
// file's placement on the build's target. Such a datum must be one StaticAlign
// counted, or the image's section alignment would be too small for it and
// the datum would sit off its alignment: that is an ICE. The guard reads
// the whole unit (staticAlignOf), outside the pack's members, but it only
// raises the ICE and never changes what the pack prints, so PackFacts'
// rule for reads outside the pack does not apply to it.
func (g *packGen) bigStatic(align uint32) bool {
	if align <= pageBytes || g.cfg.Target.OS != types.OSWindows {
		return false
	}
	if !g.staticDone {
		g.staticMax, g.staticDone = staticAlignOf(g.u, g.env), true
	}
	if min(align, maxAlign) > g.staticMax {
		source.ICEf(source.Span{}, "codegen: a static datum aligned %d, over the unit's static alignment %d (StaticAlign)", align, g.staticMax)
	}
	return true
}

// staticSection is the section of a static datum aligned to align, over a
// page: ".mkalr$<k>" for a constant and ".mkalw$<k>" for a var, k from "a"
// for 65536 down to "d" for 8192. lld-link makes each name before the "$"
// one image section and orders its parts by the whole name, so the largest
// alignment comes first. Every part's size is a multiple of its alignment
// (padStatic), so a section that starts at a multiple of the largest
// alignment keeps every part at its own.
func staticSection(align uint32, constant bool) string {
	k := string(rune('a' + bits.TrailingZeros32(maxAlign) - bits.TrailingZeros32(align)))
	if constant {
		return ".mkalr$" + k
	}
	return ".mkalw$" + k
}

// staticPin: each such global is in @llvm.compiler.used, so the optimizer
// leaves it as emitted. Without it, clang 21.1.8 -O3 of a hand-written
// module made an internal var that is only read "constant", which gave
// the object's whole .mkalw$d section read-only flags, so a write to
// another var there would fault; and the optimizer may split or shrink a
// global, which would break the multiples the layout needs.

// padStatic is init, of size bytes, followed by zero bytes up to a multiple
// of align.
func (g *packGen) padStatic(init llvm.Value, size uint64, align uint32) llvm.Value {
	pad := (size+uint64(align)-1)&^(uint64(align)-1) - size
	if pad == 0 {
		return init
	}
	m := g.m
	return m.ConstStruct(true, init, m.ZeroInit(m.Array(pad, m.I(8))))
}

// StaticAlign is the section alignment the build's x86_64 Windows image
// needs: the largest alignment over a page among the consts u's IR
// can emit (unitConsts) and the types of u's var globals, else 0 (and 0 on
// every other target). The const table itself is not the measure: it
// holds every const the session evaluated, std.core's test consts among
// them, so it would give every image the largest alignment. bigStatic
// checks every datum it places against the same
// value.
func StaticAlign(u *Unit, env Env, cfg Config) uint32 {
	if cfg.Target.OS != types.OSWindows {
		return 0
	}
	return staticAlignOf(u, env)
}

// staticAlignOf is StaticAlign on x86_64 Windows.
func staticAlignOf(u *Unit, env Env) uint32 {
	var a uint32
	items := env.Items()
	for _, gl := range u.Globals {
		if d, ok := items.Get(gl.Item).Decl.(*types.GlobalDecl); ok && !d.Extern && d.Section == "" {
			a = max(a, env.Layout(d.Type).Shape.Align)
		}
	}
	// The walk is needed only when the table holds a value over a page.
	ct, big := env.Consts(), false
	for id := 1; id < ct.Len() && !big; id++ {
		big = ct.Get(types.ConstID(id)).Align > pageBytes
	}
	if big {
		unitConsts(u, env, func(cv *types.ConstValue) { a = max(a, cv.Align) })
	}
	if a <= pageBytes {
		return 0
	}
	return min(a, maxAlign)
}

// unitConsts calls visit once per const value u's IR can emit through
// constGlobal or a global's initializer: the values of u's globals (a var's
// and a section const's), and every const a function body names (an
// OpConst operand, an RvConstRef or RvGlobalRef of a const item, an asm
// operand), with every const they reach through a data relocation. These
// are PackConstTypes' roots over every body of the unit; a function
// relocation's target is a Func whose body is walked on its own. It may
// over-approximate (a global's own value is its initializer, not a
// constGlobal), never under: bigStatic's guard makes a miss an ICE.
func unitConsts(u *Unit, env Env, visit func(*types.ConstValue)) {
	consts, items := env.Consts(), env.Items()
	seen := map[types.ConstID]bool{}
	var walk func(id types.ConstID)
	walk = func(id types.ConstID) {
		if id == types.NoConst || seen[id] {
			return
		}
		seen[id] = true
		cv := consts.Get(id)
		if cv == nil {
			return
		}
		visit(cv)
		for _, rl := range cv.Relocs {
			if rl.Kind == types.RelocData {
				walk(rl.Data)
			}
		}
	}
	for _, gl := range u.Globals {
		switch d := items.Get(gl.Item).Decl.(type) {
		case *types.GlobalDecl:
			if d.HasValue {
				walk(d.Value)
			}
		case *types.ConstDecl:
			if d.HasValue {
				walk(d.Value)
			}
		}
	}
	op := func(o *mir.Operand) {
		if o.Kind == mir.OpConst {
			walk(o.Const)
		}
	}
	for fi := range u.Funcs {
		b := u.Funcs[fi].Body
		if b == nil {
			continue
		}
		for i := range b.Operands {
			op(&b.Operands[i])
		}
		for i := range b.Args {
			op(&b.Args[i].Operand)
		}
		for i := range b.Blocks {
			bl := &b.Blocks[i]
			for j := range bl.Stmts {
				rv := &bl.Stmts[j].Rvalue
				op(&rv.A)
				op(&rv.B)
				if rv.Kind == mir.RvConstRef || rv.Kind == mir.RvGlobalRef {
					if d, ok := items.Get(rv.Item).Decl.(*types.ConstDecl); ok && d.HasValue {
						walk(d.Value)
					}
				}
			}
			t := &bl.Term
			op(&t.On)
			op(&t.Msg)
			op(&t.Callee.Value)
		}
		for i := range b.Asms {
			for k := range b.Asms[i].Operands {
				a := &b.Asms[i].Operands[k]
				op(&a.In)
				walk(a.Const)
			}
		}
	}
}
