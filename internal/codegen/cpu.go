// Why: a function may be compiled for extra CPU features (its cpu clause),
// and a generic it calls must run with the same features, so codegen needs
// each instance's feature set and the right variant at each call.
//
// What: keyCPU, cpuIndexKey, ownCPU, cpuOf, callIndex, unionCPU and
// minusCPU.
//
// How: an instance's feature set is its cpu clause united with the set it
// inherits, which mono writes as the " @cpu(...)" suffix of its key text
// (the driver copies mono's Key.Text into Func.Key). A direct call of a
// generic instance from a function with a set finds the variant mono made
// for that set, by the same rule mono applies (mono/cpu.go instanceCPU).

package codegen

import (
	"slices"
	"strings"

	"mk/internal/mir"
	"mk/internal/source"
	"mk/internal/types"
)

// The key suffix of an instance that inherits features: mono's format.
const (
	cpuOpen  = " @cpu("
	cpuClose = ")"
)

// keyCPU is the inherited set a key text names in its suffix, or nil.
func keyCPU(key string) []string {
	if !strings.HasSuffix(key, cpuClose) {
		return nil
	}
	i := strings.LastIndex(key, cpuOpen)
	if i < 0 {
		return nil
	}
	return strings.Split(key[i+len(cpuOpen):len(key)-len(cpuClose)], ",")
}

// cpuIndexKey is the symIndex key of item's instance at args that inherits
// extra (non-empty).
func cpuIndexKey(item types.ItemID, args []types.GenericArg, extra []string) string {
	return argsKey(item, args) + "|cpu:" + strings.Join(extra, ",")
}

// ownCPU is item's own cpu clause (Signature.CPU, sorted and closed).
func (g *packGen) ownCPU(item types.ItemID) []string {
	if item == types.NoItem {
		return nil
	}
	if fd, ok := g.items.Get(item).Decl.(*types.FuncDecl); ok {
		return fd.Sig.CPU
	}
	return nil
}

// cpuOf is Funcs[fi]'s feature set: its clause and what it inherits.
func (g *packGen) cpuOf(fi int) []string {
	f := &g.u.Funcs[fi]
	return unionCPU(g.ownCPU(f.Item), keyCPU(f.Key))
}

// callIndex is the Funcs index of the callee of the direct call t in fg's
// function: the variant inheriting fg's features when the call creates a
// generic instance (it has arguments; not a fn literal) and those features
// reach beyond the callee's clause and the baseline, else the plain one.
func (fg *fnGen) callIndex(t *mir.Terminator) int {
	g := fg.g
	item, args := t.Callee.Fn.Item, fg.calleeArgs(t)
	if len(args) > 0 && g.items.Get(item).Kind != types.KindLiteral {
		if caller := g.cpuOf(fg.fi); len(caller) > 0 {
			own := g.ownCPU(item)
			extra := minusCPU(unionCPU(caller, own), own, types.CPUBaseline(g.cfg.Target))
			if len(extra) > 0 {
				if i, ok := g.u.index().byKey[cpuIndexKey(item, args, extra)]; ok {
					return i
				}
				source.ICEf(fg.spanOf(t.Span), "codegen: no instance of %s inheriting %s in the unit (mono 4.5 step 1)",
					g.items.Get(item).Key, strings.Join(extra, ","))
			}
		}
	}
	return g.funcIndex(item, args)
}

// unionCPU is the sorted union of two sorted feature sets.
func unionCPU(a, b []string) []string {
	if len(b) == 0 {
		return a
	}
	if len(a) == 0 {
		return b
	}
	out := append(append(make([]string, 0, len(a)+len(b)), a...), b...)
	slices.Sort(out)
	return slices.Compact(out)
}

// minusCPU is set without the names of drop, in set's order.
func minusCPU(set []string, drop ...[]string) []string {
	var out []string
	for _, n := range set {
		gone := false
		for _, d := range drop {
			gone = gone || slices.Contains(d, n)
		}
		if !gone {
			out = append(out, n)
		}
	}
	return out
}
