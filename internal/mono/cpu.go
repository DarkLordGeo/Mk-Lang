// Why: a generic body called from a cpu "avx2" function must be compiled
// with avx2 too, so an instance's CPU feature set is part of what it is.
//
// What: feature-keyed instances: the feature set collection carries with
// each instance, and the rules that compute it.
//
// How: a root's set is its own cpu clause; a generic instance a direct call
// creates gets the caller's set united with the callee's own clause. A plain
// function (compiled once), an extern, a fn literal, and every function
// named as a value or reached through a const or a sym operand keep their
// own clause, since a fn value may be called from code without the caller's
// features.

package mono

import (
	"slices"
	"strings"

	"mk/internal/types"
)

// cpuOpen and cpuClose frame the key suffix of an instance that inherits
// features: Key.Text + " @cpu(" + names joined by "," + ")". codegen reads
// the suffix back from the key text the driver copies into codegen.Func.Key
// (keyCPU there), so the format is part of mono's output.
const (
	cpuOpen  = " @cpu("
	cpuClose = ")"
)

// ownCPU is item's own cpu clause (Signature.CPU: CPUClosure of its names
// for the build target's architecture, sorted); nil for anything else.
func (c *collector) ownCPU(item types.ItemID) []string {
	if fd, ok := c.env.Items().Get(item).Decl.(*types.FuncDecl); ok {
		return fd.Sig.CPU
	}
	return nil
}

// instanceCPU is the feature set of the instance request r of a node with
// set caller creates, and the part of it the instance inherits beyond its
// own clause and the target's baseline (the key suffix; empty for every
// instance of a build with no cpu clause, so no existing key changes).
func (c *collector) instanceCPU(caller []string, r request) (set, extra []string) {
	own := c.ownCPU(r.item)
	if !r.call || len(r.args) == 0 || len(caller) == 0 || c.env.Items().Get(r.item).Kind == types.KindLiteral {
		return own, nil
	}
	set = unionCPU(caller, own)
	extra = minusCPU(set, own, types.CPUBaseline(c.opt.Target))
	if len(extra) == 0 {
		return own, nil
	}
	return set, extra
}

// keyCPU is key with the inherited set extra: the suffix on its text and
// iface.Hasher.InstanceNameCPU as its hash (equal to InstanceName when extra
// is empty).
func (c *collector) keyCPU(k Key, item types.ItemID, args []types.GenericArg, extra []string) Key {
	if len(extra) == 0 {
		return k
	}
	it := c.env.Items().Get(item)
	return Key{Text: k.Text + cpuSuffix(extra), Hash: [32]byte(c.env.Hasher().InstanceNameCPU(it.Key, args, extra))}
}

// cpuSuffix is the key and name suffix of an inherited set.
func cpuSuffix(extra []string) string {
	return cpuOpen + strings.Join(extra, ",") + cpuClose
}

// unionCPU is the sorted union of two sorted feature sets.
func unionCPU(a, b []string) []string {
	if len(b) == 0 {
		return a
	}
	if len(a) == 0 {
		return b
	}
	out := make([]string, 0, len(a)+len(b))
	out = append(append(out, a...), b...)
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
