// Why: loans and accesses are on places (x, x.f, x.*[i]), and every
// conflict test compares two of them; interning places makes that compare
// cheap.
//
// What: step and its kinds and deref modes, pathID and pathInfo, pathTable
// (intern, extend, prefix, ofPlace), overlap and overlapLen.
//
// How: a path is a root and a step list; a slice or str index is a deref
// then an index step. Different roots never overlap; a step list that is a
// prefix of the other overlaps; a longer loan place overlaps a shallow
// access only when its extra steps hold no deref.

package borrow

import (
	"encoding/binary"

	"mk/internal/mir"
	"mk/internal/source"
	"mk/internal/types"
)

type stepKind uint8

const (
	stField stepKind = iota
	stVariant
	stDeref
	stIndex
)

type derefMode uint8

const (
	dmShared derefMode = iota
	dmExcl
	dmRaw
)

// step is one step of a path. For stField, a is the field index and any marks
// a generic fields-loop step, which equals every field step. For
// stVariant, sel and a are the downcast's. For stDeref, mode is read from the
// type before the step.
type step struct {
	kind stepKind
	mode derefMode
	sel  mir.VariantSel
	any  bool
	a    uint32
}

func (s step) equalAt(o step) bool {
	if s.kind != o.kind {
		return false
	}
	switch s.kind {
	case stField:
		return s.any || o.any || s.a == o.a
	case stVariant:
		return s.sel == o.sel && s.a == o.a
	}
	return true // all derefs of one place are one step; all indices are one
}

type pathID uint32

const noPath pathID = ^pathID(0)

// pathInfo is one interned path: its root, its steps, the types after each
// step (types[i] is the type after step i; the root's type is rootType), and
// the number of derefs.
type pathInfo struct {
	root     rootID
	steps    []step
	types    []types.TypeID
	rootType types.TypeID
	derefs   int
}

// typ is the path's type.
func (p *pathInfo) typ() types.TypeID {
	if len(p.types) == 0 {
		return p.rootType
	}
	return p.types[len(p.types)-1]
}

type pathTable struct {
	paths  []pathInfo
	index  map[string]pathID
	places map[mir.Place]pathID
	key    []byte
}

func newPathTable() *pathTable {
	return &pathTable{index: make(map[string]pathID), places: make(map[mir.Place]pathID)}
}

func (t *pathTable) get(id pathID) *pathInfo { return &t.paths[id] }

// intern returns the ID of a path with these steps (copied when new).
func (t *pathTable) intern(root rootID, rootType types.TypeID, steps []step, tys []types.TypeID) pathID {
	t.key = binary.LittleEndian.AppendUint32(t.key[:0], uint32(root))
	for _, s := range steps {
		var any byte
		if s.any {
			any = 1
		}
		t.key = append(t.key, byte(s.kind), byte(s.mode), byte(s.sel), any)
		t.key = binary.LittleEndian.AppendUint32(t.key, s.a)
	}
	if id, ok := t.index[string(t.key)]; ok {
		return id
	}
	id := pathID(len(t.paths))
	n := 0
	for _, s := range steps {
		if s.kind == stDeref {
			n++
		}
	}
	t.paths = append(t.paths, pathInfo{root: root, rootType: rootType, steps: append([]step(nil), steps...),
		types: append([]types.TypeID(nil), tys...), derefs: n})
	t.index[string(t.key)] = id
	return id
}

// extend is path p followed by one more step of type after.
func (t *pathTable) extend(p pathID, s step, after types.TypeID) pathID {
	pi := t.get(p)
	steps := append(append([]step(nil), pi.steps...), s)
	tys := append(append([]types.TypeID(nil), pi.types...), after)
	return t.intern(pi.root, pi.rootType, steps, tys)
}

// prefix is the path of p's first n steps.
func (t *pathTable) prefix(p pathID, n int) pathID {
	pi := t.get(p)
	if n >= len(pi.steps) {
		return p
	}
	return t.intern(pi.root, pi.rootType, pi.steps[:n], pi.types[:n])
}

// ofPlace translates a MIR place: ProjField is a field step, a
// ProjDowncast a variant step, a ProjDeref a deref whose mode is the kind of
// the type before it, and a ProjIndex on an array an index step, on a slice or
// str a shared deref then an index, on a mut []T an exclusive deref then an
// index. The root of local l is root l.
func (t *pathTable) ofPlace(b *mir.Body, tab *types.Table, pl mir.Place) pathID {
	if id, ok := t.places[pl]; ok {
		return id
	}
	before := b.Locals[pl.Local].Type
	var steps []step
	var tys []types.TypeID
	for _, pr := range b.Proj(pl) {
		switch pr.Kind {
		case mir.ProjField:
			steps = append(steps, step{kind: stField, a: pr.A, any: pr.FieldLoop})
			tys = append(tys, pr.Type)
		case mir.ProjDowncast:
			steps = append(steps, step{kind: stVariant, sel: pr.Sel, a: pr.A})
			tys = append(tys, pr.Type)
		case mir.ProjDeref:
			steps = append(steps, step{kind: stDeref, mode: modeOf(tab, before)})
			tys = append(tys, pr.Type)
		case mir.ProjIndex:
			switch tab.Kind(before) {
			case types.KArray:
			case types.KSlice, types.KStr:
				steps = append(steps, step{kind: stDeref, mode: dmShared})
				tys = append(tys, before)
			case types.KMutSlice:
				steps = append(steps, step{kind: stDeref, mode: dmExcl})
				tys = append(tys, before)
			default:
				source.ICEf(b.Spans[0], "borrow: index step on %s", tab.TypeString(before, nil))
			}
			steps = append(steps, step{kind: stIndex})
			tys = append(tys, pr.Type)
		}
		before = pr.Type
	}
	id := t.intern(rootID(pl.Local), b.Locals[pl.Local].Type, steps, tys)
	t.places[pl] = id
	return id
}

func modeOf(tab *types.Table, t types.TypeID) derefMode {
	switch tab.Kind(t) {
	case types.KRef:
		return dmShared
	case types.KMut:
		return dmExcl
	}
	return dmRaw
}

// overlap is the overlap test of a loan's place l against an access place p.
func (t *pathTable) overlap(l, p pathID, deep bool) bool {
	if l == p {
		return true
	}
	li, pi := t.get(l), t.get(p)
	if li.root != pi.root {
		return false
	}
	n := min(len(li.steps), len(pi.steps))
	for i := 0; i < n; i++ {
		if !li.steps[i].equalAt(pi.steps[i]) {
			return false
		}
	}
	if len(li.steps) <= len(pi.steps) || deep {
		return true
	}
	for _, s := range li.steps[len(pi.steps):] {
		if s.kind == stDeref {
			return false
		}
	}
	return true
}

// overlapLen is overlap for a length read of place p (colLen): l is p or a
// prefix of it. A longer l is a loan on an element (or below), which a
// length read never touches.
func (t *pathTable) overlapLen(l, p pathID) bool {
	if l == p {
		return true
	}
	li, pi := t.get(l), t.get(p)
	if li.root != pi.root || len(li.steps) > len(pi.steps) {
		return false
	}
	for i := range li.steps {
		if !li.steps[i].equalAt(pi.steps[i]) {
			return false
		}
	}
	return true
}

// onlyFieldSteps reports whether every step is a field or variant step (a
// take or a discriminant of a taken temporary).
func (p *pathInfo) onlyFieldSteps() bool {
	for _, s := range p.steps {
		if s.kind != stField && s.kind != stVariant {
			return false
		}
	}
	return true
}

// firstDeref is the index of the first deref step, or -1.
func (p *pathInfo) firstDeref() int {
	for i, s := range p.steps {
		if s.kind == stDeref {
			return i
		}
	}
	return -1
}
