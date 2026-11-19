// Why: a value that can only be read (a str read out of a `mut List[str]`)
// still carries the list's mut loan as provenance; without marks the
// writable walk would treat that loan as a path for stores.
//
// What: markStore, shallowHop, argShallow, referentShallow and addMarked:
// the shallow marks of loans in held sets.
//
// How: the marks live in each root's entry as entry.sh, a subset of held,
// and deep wins wherever two marks meet.
//
// A loan in a root's held set is deep unless a call's step 3 or 4 stored it
// there from a value whose static type is shallow (shallow): such a
// value has only shared references, to referents that are no carrier, not
// writable by type, no allocator and not opaque, so nothing reached through
// the value can be written, whatever loans it carries as provenance (a str
// read out of a `mut List[str]` carries the list's mut loan). The writable
// walk does not go on through a shallow loan; liveness, conflicts, the
// behind sets (B), the boundary and results see every loan.
//
// The marks are per (root, loan): entry.sh is the shallow subset of held.
// Deep wins: at a join (joinSh), at a store that adds a loan the root holds
// shallow (markStore), and at every write that is not a marked store
// (setHeld and writeSplit drop the marks of what they write). A split root
// keeps one mark per loan for all its fields, so a loan deep in one field
// is deep in all of them: conservative against per-field marks, and the
// walk reads the root's whole held set anyway.

package borrow

import (
	"mk/internal/mir"
	"mk/internal/types"
)

// markStore sets root r's marks after a store of s into it, given its held
// set and marks before the store: the loans of s the store adds deep
// (outside c.shIn) lose their mark, and those it adds shallow that r did
// not hold before gain one; a loan r held already keeps its mark only if
// the store adds it shallow too.
func (c *checker) markStore(st *state, r rootID, old, oldSh, s setID) {
	e := st.get(r)
	sh := c.sets.minus(oldSh, c.sets.minus(s, c.shIn))
	if c.shIn != emptySet {
		sh = c.sets.unite(sh, c.sets.minus(c.sets.meet(s, c.shIn), old))
	}
	sh = c.sets.meet(sh, e.held)
	if sh == e.sh {
		return
	}
	e.sh = sh
	c.put(st, r, e)
}

// shallowHop reports whether the writable walk skips root t's loan m:
// t holds m shallow. The self-reach walk and spawnStores' (csrConst) read
// no mark: they are a second line of defence against an unsafe or extern
// callee that writes past its signature, so they do not lean on the
// shallow type that the marks trust.
func (c *checker) shallowHop(st *state, t rootID, m uint32) bool {
	if c.csrConst {
		return false
	}
	sh := st.get(t).sh
	return sh != emptySet && c.sets.has(sh, m)
}

// argShallow is step 2's marks: the loans of argument a's set S that
// are shallow, all of them when the argument's type as passed (the caller's
// type, so a generic callee's T is judged at its instance) is
// shallow, else none. A reborrow's type is a mut reference: deep.
func (c *checker) argShallow(a mir.Arg, S setID) setID {
	if S == emptySet || a.Mode == mir.ArgReborrow || a.Operand.Kind != mir.OpMove && a.Operand.Kind != mir.OpCopy {
		return emptySet
	}
	if c.w.shallow(c.body.PlaceType(a.Operand.Place)) {
		return S
	}
	return emptySet
}

// referentShallow is step 4's mark for `*q into p` (the type judged is
// q's referent): the set read through q is shallow when the referent of
// q's argument type as passed is.
func (c *checker) referentShallow(a mir.Arg, S setID) bool {
	if S == emptySet || a.Mode == mir.ArgReborrow || a.Operand.Kind != mir.OpMove && a.Operand.Kind != mir.OpCopy {
		return false
	}
	ty := c.tab.Get(c.body.PlaceType(a.Operand.Place))
	switch ty.Kind {
	case types.KRef, types.KMut, types.KSlice, types.KMutSlice:
		return c.w.shallow(ty.Elem)
	}
	return false
}

// addMarked is argH[p] ∪= S with S's marks sh (steps 3 and 4: a chained
// loan keeps the mark it first had, deep winning): argSh[p] keeps the marks
// of the loans S does not add deep, and gains those S adds shallow.
func (c *checker) addMarked(argH, argSh []setID, p int, S, sh setID) {
	old := argH[p]
	n := c.sets.minus(argSh[p], c.sets.minus(S, sh))
	if sh != emptySet {
		n = c.sets.unite(n, c.sets.minus(c.sets.meet(S, sh), old))
	}
	argSh[p] = n
}
