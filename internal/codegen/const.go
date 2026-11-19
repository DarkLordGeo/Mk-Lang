// Why: a const value is computed at compile time as canonical bytes with
// relocations, and the IR must reproduce those exact bytes, pointers
// included, in read-only data.
//
// What: scalarConst, constGlobal, constDataFirst, relocValue and
// bytesGlobal.
//
// How: a const is a private unnamed_addr constant global, a packed struct of
// byte runs and relocated pointers; float bytes are copied as they are.
// constDataFirst emits a const's data relocations deepest first on an
// explicit stack, so a deep value costs heap, not Go stack. Byte strings are
// deduplicated per pack and carry no NUL.

package codegen

import (
	"strconv"

	"mk/internal/llvm"
	"mk/internal/source"
	"mk/internal/types"
)

// scalarConst is a scalar const's value: its bits, or for a pointer the
// relocated function or data, else the address its bytes hold (null: a none
// niche or a null *T).
func (g *packGen) scalarConst(id types.ConstID, r *repr) llvm.Value {
	cv := g.env.Consts().Get(id)
	m := g.m
	switch r.class {
	case sPtr, sRaw:
		if len(cv.Relocs) == 1 {
			return g.relocValue(cv.Relocs[0])
		}
		// No relocation: an address the bytes hold (raw.FromAddr), or null.
		// A null &T or mut T would be loaded under !nonnull (nonnull.go).
		var bits uint64
		if len(cv.Bytes) >= 8 {
			bits = types.LoadUint(cv.Bytes, 8)
		}
		if bits != 0 {
			return m.ConstIntToPtr(bits)
		}
		if r.class == sPtr {
			if size, align := g.sizeAlign(r.pointee); size == 0 {
				return m.ConstIntToPtr(uint64(max(align, 1)))
			}
			source.ICEf(source.Span{}, "codegen: const %s is a null reference", g.tt.TypeString(cv.Type, nil))
		}
		return m.Null()
	}
	if uint64(len(cv.Bytes)) < r.size {
		source.ICEf(source.Span{}, "codegen: const of %d bytes for a %d-byte scalar", len(cv.Bytes), r.size)
	}
	bits := types.LoadUint(cv.Bytes, int(r.size))
	switch {
	case r.class == sFloat && r.size == 4:
		return m.ConstF32(uint32(bits))
	case r.class == sFloat:
		return m.ConstF64(bits)
	}
	return m.ConstInt(r.ssa, bits)
}

// constGlobal is the private constant holding a const's bytes, one per const
// per pack. Names are ordinals in first-use order, which is deterministic
// (functions in key order, statements in order); private names never cross
// a pack.
func (g *packGen) constGlobal(id types.ConstID) llvm.Value {
	if v, ok := g.consts[id]; ok {
		return v
	}
	if g.constDepth >= maxConstDepth {
		g.constDataFirst(id)
	}
	g.constDepth++
	defer func() { g.constDepth-- }()
	cv := g.env.Consts().Get(id)
	m := g.m
	init, site := g.displaySite(cv) // a trace site's file by its local name (support_rt.go)
	if !site {
		init = g.constInit(cv)
	}
	align, sec := max(cv.Align, 1), ""
	if g.bigStatic(align) { // over a page on Windows
		init, sec, align = g.padStatic(init, uint64(len(cv.Bytes)), align), staticSection(align, true), pageBytes
	}
	g.nconst++
	gl := m.AddGlobal("mk.c."+strconv.Itoa(g.nconst), llvm.GlobalAttrs{Linkage: llvm.Private, Constant: true,
		UnnamedAddr: true, Align: align, Type: init.Ty, Init: init, Section: sec})
	v := m.GlobalRef(gl)
	if sec != "" {
		m.CompilerUsed(v) // pinned (staticPin)
	}
	g.consts[id] = v
	return v
}

// maxConstDepth is how deep constGlobal recurses through data relocations
// before constDataFirst takes over; below it the emission order is the
// recursion's, so no shallower value's output changes.
const maxConstDepth = 4096

// constDataFirst emits, on an explicit stack, the consts id's data
// relocations reach, deepest first, so each constGlobal below meets its
// own data relocations already emitted and recurses one level: a deep
// value (a 2,000,000-node list) costs heap, not Go stack.
// Const values form no cycle (ConstTable.Intern takes only known targets).
func (g *packGen) constDataFirst(id types.ConstID) {
	ct := g.env.Consts()
	type frame struct {
		id   types.ConstID
		next int
	}
	on := map[types.ConstID]bool{id: true}
	stack := []frame{{id: id}}
	for len(stack) > 0 {
		top := len(stack) - 1
		cv := ct.Get(stack[top].id)
		child := types.NoConst
		for stack[top].next < len(cv.Relocs) && child == types.NoConst {
			r := cv.Relocs[stack[top].next]
			stack[top].next++
			if r.Kind != types.RelocData || on[r.Data] {
				continue
			}
			if _, ok := g.consts[r.Data]; !ok {
				child = r.Data
			}
		}
		if child != types.NoConst {
			on[child] = true
			stack = append(stack, frame{id: child})
			continue
		}
		if top > 0 {
			saved := g.constDepth
			g.constDepth = 0
			g.constGlobal(stack[top].id)
			g.constDepth = saved
		}
		stack = stack[:top]
	}
}

// relocValue is the pointer a relocation names.
func (g *packGen) relocValue(rl types.Reloc) llvm.Value {
	switch rl.Kind {
	case types.RelocData:
		return g.constGlobal(rl.Data)
	case types.RelocFn:
		return g.fnRef(rl.Fn.Item, rl.Fn.Args, -1)
	}
	source.ICEf(source.Span{}, "codegen: relocation kind %d", rl.Kind)
	return llvm.Value{}
}

// bytesGlobal is a private constant [n x i8] holding b, with no NUL,
// deduplicated per pack by content.
func (g *packGen) bytesGlobal(b []byte) llvm.Value {
	key := string(b)
	if gl, ok := g.files[key]; ok {
		return g.m.GlobalRef(gl)
	}
	g.nconst++
	init := g.m.Bytes(b)
	gl := g.m.AddGlobal("mk.s."+strconv.Itoa(g.nconst), llvm.GlobalAttrs{Linkage: llvm.Private, Constant: true,
		UnnamedAddr: true, Align: 1, Type: init.Ty, Init: init})
	g.files[key] = gl
	return g.m.GlobalRef(gl)
}
