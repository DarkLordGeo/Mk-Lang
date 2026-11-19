// Why: std.raw's atomics that take a memory order (AddOrd, CasOrd, FenceOrd
// and the rest) must run at compile time as they do natively.
//
// What: the ordered atomic intrinsics, registered in init over the plain
// ones where the meaning is the same.
//
// How: the evaluator runs one thread, so every order behaves as seq_cst,
// which every order permits. In the tagged mode the race detector gives each
// the edges its order makes (tagrace_order.go).

package eval

import (
	"mk/internal/mir"
	"mk/internal/types"
)

func init() {
	for k, op := range map[string]func(old, x uint64) uint64{
		"std.raw.Atomic:AddOrd": func(old, x uint64) uint64 { return old + x },
		"std.raw.Atomic:SubOrd": func(old, x uint64) uint64 { return old - x },
		"std.raw.Atomic:AndOrd": func(old, x uint64) uint64 { return old & x },
		"std.raw.Atomic:OrOrd":  func(old, x uint64) uint64 { return old | x },
		"std.raw.Atomic:XorOrd": func(old, x uint64) uint64 { return old ^ x },
	} {
		intrinsics[k] = func(m *Machine, f *frame, t *mir.Terminator, fn types.FnRef) {
			cell := m.ptrLoc(m.argU(f, t, 0), m.targ(f, fn, 0))
			old := m.load(cell)
			m.store(cell, op(old, m.argU(f, t, 1))&mask(cell.ti.width)) // wraps
			m.store(m.dest(f, t), old)
		}
	}
	intrinsics["std.raw.Atomic:LoadOrd"] = intrinsics["std.raw.Atomic:Load"]
	intrinsics["std.raw.Atomic:StoreOrd"] = intrinsics["std.raw.Atomic:Store"]
	intrinsics["std.raw.Atomic:SwapOrd"] = intrinsics["std.raw.Atomic:Swap"]
	intrinsics["std.raw.FenceOrd"] = intrinsics["std.raw.Fence"]
	// CasOrd's (ok, value): value is what the word held, with its tag
	// as Swap's result; the weak form never fails spuriously here.
	intrinsics["std.raw.Atomic:CasOrd"] = func(m *Machine, f *frame, t *mir.Terminator, fn types.FnRef) {
		cell := m.ptrLoc(m.argU(f, t, 0), m.targ(f, fn, 0))
		old := m.load(cell)
		ot := m.takeVal()
		ok := old == m.argU(f, t, 1)
		m.takeVal()
		if ok {
			m.store(cell, m.argU(f, t, 2))
		}
		if m.tg != nil {
			m.tg.race.casOK = ok // success or failure order (tagrace_order.go)
		}
		dst := m.dest(f, t)
		for i, fd := range dst.ti.ty.Fields {
			fl := dst.ti.layout.Fields[i]
			if m.cfg.Interner.Str(fd.Name) == "ok" {
				m.store(m.at(dst, fl.Offset, fl.Type), b2u(ok))
				continue
			}
			m.setVal(ot)
			m.store(m.at(dst, fl.Offset, fl.Type), old)
		}
	}
}
