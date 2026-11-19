// Why: validating a cached check reads namespaces (a module's top-level
// items, a type's methods, every impl), and a linear scan of the session's
// item table per read would make warm builds grow with the session's size.
//
// What: itemIndex, the Hasher's index of its item table by module, method
// owner name and impls, with moduleItems, methodItems and implItems.
//
// How: the index groups items by fields fixed when the item is added
// (Module, Kind, Key) and scans only slots added since its last use, since
// the table is append-only. Readers filter candidates by their current
// fields, so a namespace read costs the size of the namespace.

package iface

import (
	"strings"
	"sync"

	"mk/internal/types"
)

// itemIndex groups the Hasher's item table into candidate lists by fields
// that are fixed when the item is added: Module, Kind and Key, which
// internal/resolve passes to AddItem. Owner and OwnerPrim are not: resolve
// sets them after AddItem (declareMethod), and for a method whose owner a
// gen line may produce, only when the module settles. So the index never
// decides by them; readers filter each candidate by its current fields
// (topLevel, Owner, Decl, Exported) when they read it. The table is
// append-only, so the index scans only the slots added since its last use.
type itemIndex struct {
	mu      sync.Mutex
	scanned int // item slots indexed, slot 0 included
	// top: per module, its items of a kind that can be top-level.
	top map[types.ModuleID][]types.ItemID
	// methods: KindFunc items keyed like a Type.Name method
	// (types.MemberKey: "module.Owner:Name", no '#'), by the owner name.
	methods map[string][]types.ItemID
	impls   []types.ItemID
}

// methodOwnerName is the owner type's name in a MemberKey-shaped key, and
// false for any other key (impl members, literals and contract or invariant
// predicates all have a '#').
func methodOwnerName(k types.Key) (string, bool) {
	s := k.String()
	i := strings.LastIndexByte(s, ':')
	if i < 0 || strings.IndexByte(s, '#') >= 0 {
		return "", false
	}
	return s[strings.LastIndexByte(s[:i], '.')+1 : i], true
}

// refresh indexes the slots added since the last call. Callers hold mu.
func (x *itemIndex) refresh(items *types.Items) {
	if x.scanned == 0 {
		x.scanned = 1
		x.top = make(map[types.ModuleID][]types.ItemID)
		x.methods = make(map[string][]types.ItemID)
	}
	n := items.NumItems()
	for i := x.scanned; i < n; i++ {
		// The slot is the ID. AddItem publishes the slot (the list's atomic
		// length) after Kind, Module and Key are set, but writes it.ID only
		// after that, so it.ID read here can still be NoItem, which the
		// index would keep for the session.
		id := types.ItemID(i)
		it := items.Get(id)
		switch it.Kind {
		case types.KindImpl:
			x.impls = append(x.impls, id)
			continue
		case types.KindStruct, types.KindEnum, types.KindError, types.KindInterface, types.KindAlias,
			types.KindConst, types.KindGlobal, types.KindFunc:
			x.top[it.Module] = append(x.top[it.Module], id)
		}
		if it.Kind == types.KindFunc {
			if owner, ok := methodOwnerName(it.Key); ok {
				x.methods[owner] = append(x.methods[owner], id)
			}
		}
	}
	x.scanned = n
}

// moduleItems is the candidates for module m's top-level items, in item
// order; the caller keeps those topLevel accepts now. The returned slice is
// never written again (appends land past its length).
func (h *Hasher) moduleItems(m types.ModuleID) []types.ItemID {
	h.idx.mu.Lock()
	defer h.idx.mu.Unlock()
	h.idx.refresh(h.items)
	s := h.idx.top[m]
	return s[:len(s):len(s)]
}

// methodItems is the candidates for the Type.Name methods of a type named
// owner, of any module; the caller keeps those whose Owner is the type.
func (h *Hasher) methodItems(owner string) []types.ItemID {
	h.idx.mu.Lock()
	defer h.idx.mu.Unlock()
	h.idx.refresh(h.items)
	s := h.idx.methods[owner]
	return s[:len(s):len(s)]
}

// implItems is every impl item of the session, in item order.
func (h *Hasher) implItems() []types.ItemID {
	h.idx.mu.Lock()
	defer h.idx.mu.Unlock()
	h.idx.refresh(h.items)
	s := h.idx.impls
	return s[:len(s):len(s)]
}
