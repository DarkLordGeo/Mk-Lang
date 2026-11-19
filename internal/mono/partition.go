// Why: the driver caches object code per pack of instances, so the packs
// must change as little as possible when one instance changes.
//
// What: Partition, which cuts each module's instances into packs.
//
// How: instances are ordered by (owning module path, InstanceName); a cut
// falls after an instance exactly when iface.PackCutAfter of its name
// holds, and an instance over iface.PackSoloOps operations gets a pack of
// its own. The pack key itself is the driver's (iface.PackKey).

package mono

import (
	"bytes"
	"cmp"
	"encoding/hex"
	"slices"

	"mk/internal/iface"
)

// Partition cuts each module's instances into packs. Every rule reads only one
// instance's own name or size, so an edit to one instance's body changes one
// pack's instance list, and an added instance changes only the pack it lands
// in (which it may split). A pack never crosses a module boundary, so Owner is
// one module. Bodiless instances (externs, intrinsics) have no code and are in
// no pack. A pack's name is its module path, ".", and the first 16 hex digits
// of its first member's name, never an ordinal. PackInstance.Rows is left
// zero: positions are the driver's.
func Partition(env Env, in *Instances, opt Options) []Pack {
	type member struct {
		i    int
		path string
	}
	var ms []member
	for i := range in.List {
		if in.List[i].Body != nil {
			ms = append(ms, member{i, env.Items().Module(in.List[i].Owner).Path})
		}
	}
	slices.SortFunc(ms, func(a, b member) int {
		return cmp.Or(cmp.Compare(a.path, b.path), bytes.Compare(in.List[a.i].Key.Hash[:], in.List[b.i].Key.Hash[:]))
	})
	var packs []Pack
	var cur *Pack
	cut := true
	for k, m := range ms {
		inst := &in.List[m.i]
		solo := inst.Ops > iface.PackSoloOps
		if cut || solo || (k > 0 && ms[k-1].path != m.path) {
			packs = append(packs, Pack{Name: m.path + "." + hex.EncodeToString(inst.Key.Hash[:8]), Owner: inst.Owner})
			cur = &packs[len(packs)-1]
		}
		cur.Members = append(cur.Members, m.i)
		cur.Instances = append(cur.Instances, iface.PackInstance{Name: inst.Key.Hash, Content: inst.Content})
		cut = solo || iface.PackCutAfter(inst.Key.Hash)
	}
	return packs
}
