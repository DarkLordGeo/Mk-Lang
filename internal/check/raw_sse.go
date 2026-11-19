// Why: on x86_64, std.raw's crypto intrinsics execute in SSE registers
// (AES-NI, PCLMULQDQ and the SHA extensions operate on xmm), so a bin built
// general-regs-only must not call them unless it opts into sse2.
//
// What: rawSSE, the set of such intrinsics (each crypto file adds its own
// from init), and rawNeedsSSE, the E1106 `needs: sse2` check.
//
// How: a call compiles only in a function whose context (the target's
// baseline and its cpu clause) holds sse2. A hosted target's baseline holds
// it, so the rule never fires there; on x86_64-freestanding the caller says
// `cpu "sse2, ..."`, a clause exempt from E1108 (it saves and restores the
// FPU state itself). Like E1106 for Fma it holds inside unsafe: unsafe waives
// the cpu call rule (E1101), not the registers a bin has.

package check

import (
	"mk/internal/ast"
	"mk/internal/types"
)

// rawSSE are std.raw's free functions that execute in SSE registers on
// x86_64, by name. Filled from init only.
var rawSSE = map[string]bool{}

// rawNeedsSSE is E1106 `needs: sse2` for a call of, or a fn value naming,
// one of rawSSE's intrinsics where the context lacks sse2 (x86_64 only;
// needsFeature ignores other architectures).
func (u *unit) rawNeedsSSE(at ast.Node, it types.ItemID) {
	if len(rawSSE) == 0 || it == types.NoItem {
		return
	}
	item := u.c.cfg.Items.Get(it)
	if item.Owner != types.NoItem || item.OwnerPrim != types.NoType {
		return
	}
	name := u.c.cfg.Interner.Str(item.Name)
	if !rawSSE[name] {
		return
	}
	mod := u.c.cfg.Items.Module(item.Module)
	if mod == nil || !mod.Std || mod.Path != "std.raw" {
		return
	}
	u.needsFeature(at, "raw."+name, "sse2")
}
