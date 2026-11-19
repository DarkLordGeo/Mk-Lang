// Why: a module whose sources did not change is loaded from the cache as a
// whole interface record, and its readers recheck only when the facts they
// can see change. This file defines that record and its two part hashes.
//
// What: Interface (items, shapes, part hashes, body, function and gen
// entries, diagnostics, imports, external reads), APIHash (mk.api),
// ShapesHash (mk.shapes), EncodeInterface, DecodeInterface and
// DecodeInterfaceInto.
//
// How: the API hash covers exported names, signatures with flows, exported
// fields and the "has private fields" bit, const values and the impl table;
// never private items, bodies, spans or diagnostics. Decoding re-interns into
// a session and returns ErrStale or ErrCorrupt (a miss) without side effects.

package iface

import (
	"errors"
	"fmt"
	"slices"

	"mk/internal/diag"
	"mk/internal/source"
	"mk/internal/types"
)

// Interface is one module's cached interface: the three parts, plus what the
// module's own replay needs.
type Interface struct {
	Path   string        // module path
	Module *types.Module // decoded into Env.Items
	// Items are all the module's items, private included; encoding sorts
	// them by key, decoding returns them in key order.
	Items []types.ItemID
	// Shapes are the shape records of the module's non-generic type items
	// (a generic type's term is its decl's Shape). Encoding sorts them by key.
	Shapes    []ShapeRec
	APIHash   source.Hash // mk.api over the API part, computed by EncodeInterface
	ShapeHash source.Hash // mk.shapes over the shape part, computed by EncodeInterface
	Bodies    []BodyRef   // body store: generics, inline candidates, evaluated bodies
	Funcs     []FuncRef   // every function, test and initializer: its result entry
	Gens      []GenRef    // every gen line of the module: its output entry
	GenText   source.Hash // content hash of the virtual generated file, zero without gen lines
	Diags     []diag.Diagnostic
	// Imports are the module's direct imports' paths (Module.Imports,
	// encoded as paths, never IDs), in path order.
	Imports []string
	// ExternalReads are the reads of anything outside this module.
	ExternalReads []types.Read
	// Anchors are every anchor a stored span uses, filled by decoding.
	Anchors []AnchorUse
}

// ShapeRec is the shape record of one non-generic type item.
type ShapeRec struct {
	Item  types.ItemID
	Shape types.Shape
}

// BodyRef names a body in the body store. Contract bodies of every function
// whose signature is in the record are always there.
type BodyRef struct {
	Item     types.Key
	BodyHash source.Hash
	Entry    source.Hash
}

// GenRef names one gen line's output entry, loaded on a module hit to
// rebuild the virtual generated file.
type GenRef struct {
	Gen   types.Key
	Entry source.Hash
}

// FuncRef names a function's result entry. HasDiags is true when the result
// holds diagnostics: on a module hit the driver loads exactly those entries
// to replay them.
type FuncRef struct {
	Item     types.Key
	Entry    source.Hash
	HasDiags bool
}

// errLoaded is returned when a module's items are already in the session.
var errLoaded = errors.New("iface: the module's items are already in the session")

func (h *Hasher) sortedItems(ids []types.ItemID) []types.ItemID {
	s := slices.Clone(ids)
	slices.SortFunc(s, func(a, b types.ItemID) int { return h.items.Get(a).Key.Compare(h.items.Get(b).Key) })
	return s
}

// APIHash is mk.api over a module's API part: per exported
// item in key order its key, its mk.sig or mk.decl hash and its
// foreign mk.fields hash; per impl in key order its mk.impl hash; per
// exported const or global with a value its value hash; then mk.names
// over the exported top-level names.
func (h *Hasher) APIHash(items []types.ItemID) source.Hash {
	var e Encoder
	var names []nameEntry
	sorted := h.sortedItems(items)
	for _, id := range sorted {
		it := h.items.Get(id)
		if !it.Exported || it.Kind == types.KindImpl {
			continue
		}
		if it.Decl == nil {
			// Hash form is total: an exported item with no
			// checked declaration cannot be hashed into the API part.
			source.ICEf(it.Span, "iface: API hash of exported %s, which has no declaration", it.Key)
		}
		e.Uvarint(1)
		e.String(it.Key.String())
		e.Hash(h.Decl(it))
		if fh, ok := h.Fields(it, false); ok {
			e.Bool(true)
			e.Hash(fh)
		} else {
			e.Bool(false)
		}
		switch d := it.Decl.(type) {
		case *types.ConstDecl:
			if d.HasValue {
				e.Uvarint(3)
				e.Hash(h.Const(d.Value))
			}
		case *types.GlobalDecl:
			if d.HasValue {
				e.Uvarint(3)
				e.Hash(h.Const(d.Value))
			}
		}
		if topLevel(it) {
			names = append(names, nameEntry{name: h.in.Str(it.Name), kind: uint8(it.Kind), exported: true})
		}
	}
	for _, id := range sorted {
		it := h.items.Get(id)
		if it.Kind == types.KindImpl && it.Decl != nil {
			e.Uvarint(2)
			e.String(it.Key.String())
			e.Hash(h.Impl(it))
		}
	}
	e.Uvarint(0)
	e.Hash(namesHash(names))
	return e.sum(DomAPI)
}

// ShapesHash is mk.shapes over a module's shape part: per exported struct
// or enum item in key order its key, then 1 and its shape record hash (from
// shapes), or 2 and its shape term hash (the decl's Shape). An exported type
// with neither is an ICE: hash form is total, so a shape part
// that left a type out would hash differently in a cold and a warm session.
func (h *Hasher) ShapesHash(items []types.ItemID, shapes []ShapeRec) source.Hash {
	rec := make(map[types.ItemID]types.Shape, len(shapes))
	for _, s := range shapes {
		rec[s.Item] = s.Shape
	}
	var e Encoder
	for _, id := range h.sortedItems(items) {
		it := h.items.Get(id)
		if !it.Exported || (it.Kind != types.KindStruct && it.Kind != types.KindEnum) {
			continue
		}
		e.String(it.Key.String())
		var term *types.ShapeTerm
		switch d := it.Decl.(type) {
		case *types.StructDecl:
			term = d.Shape
		case *types.EnumDecl:
			term = d.Shape
		}
		if s, ok := rec[id]; ok {
			e.Uvarint(1)
			e.Hash(h.Shape(s))
		} else if term != nil {
			e.Uvarint(2)
			e.Hash(h.ShapeTerm(term))
		} else {
			source.ICEf(it.Span, "iface: shape hash of exported %s, which has neither a shape record nor a shape term", it.Key)
		}
	}
	return e.sum(DomShapes)
}

// EncodeInterface writes the store form. ItemIDs, TypeIDs and
// the rest are replaced by pool indices; the output is a function of the
// module's content only, so equal modules give equal bytes whatever the
// intern order. It computes and stores APIHash and ShapeHash (and sets them
// on m). Every item of the module that the record mentions must be in
// m.Items, else it is an ICE.
func EncodeInterface(env *Env, h *Hasher, m *Interface) []byte {
	p := newPools(h, env, m.Module.ID)
	var e Encoder
	w := walker{e: &e, h: h, p: p}
	items := h.sortedItems(m.Items)
	listed := make(map[types.ItemID]bool, len(items))
	for _, id := range items {
		listed[id] = true
	}
	e.String(m.Path)
	e.Bool(m.Module.UnsafePermitted)
	e.Bool(m.Module.Std)
	var paths []string
	for _, f := range m.Module.Files {
		paths = append(paths, env.Sources.File(f).Path)
	}
	slices.Sort(paths)
	e.Uvarint(uint64(len(paths)))
	for _, s := range paths {
		e.String(s)
	}
	e.Uvarint(uint64(len(items)))
	for _, id := range items {
		it := env.Items.Get(id)
		if it.Module != m.Module.ID {
			source.ICEf(it.Span, "iface: item %s of another module in interface %s", it.Key, m.Path)
		}
		p.prefer = []AnchorKey{AnchorKey(it.Key.String())}
		w.item(id)
		w.sym(it.Name)
		e.Bool(it.Exported)
		w.item(it.Owner)
		w.span(it.Span)
		e.Bool(it.Origin != nil)
		if it.Origin != nil {
			w.span(it.Origin.Output)
			w.span(it.Origin.Emitter)
			w.span(it.Origin.Gen)
		}
		w.writeDecl(it.Decl)
		w.typ(it.OwnerPrim)
	}
	p.prefer = nil
	shapes := slices.Clone(m.Shapes)
	slices.SortFunc(shapes, func(a, b ShapeRec) int { return env.Items.Get(a.Item).Key.Compare(env.Items.Get(b.Item).Key) })
	e.Uvarint(uint64(len(shapes)))
	for i := range shapes {
		w.item(shapes[i].Item)
		w.writeShape(&shapes[i].Shape)
	}
	m.APIHash = h.APIHash(items)
	m.ShapeHash = h.ShapesHash(items, shapes)
	e.Hash(m.APIHash)
	e.Hash(m.ShapeHash)
	bodies := slices.Clone(m.Bodies)
	slices.SortFunc(bodies, func(a, b BodyRef) int { return a.Item.Compare(b.Item) })
	e.Uvarint(uint64(len(bodies)))
	for _, b := range bodies {
		w.key(b.Item)
		e.Hash(b.BodyHash)
		e.Hash(b.Entry)
	}
	funcs := slices.Clone(m.Funcs)
	slices.SortFunc(funcs, func(a, b FuncRef) int { return a.Item.Compare(b.Item) })
	e.Uvarint(uint64(len(funcs)))
	for _, f := range funcs {
		w.key(f.Item)
		e.Hash(f.Entry)
		e.Bool(f.HasDiags)
	}
	gens := slices.Clone(m.Gens)
	slices.SortFunc(gens, func(a, b GenRef) int { return a.Gen.Compare(b.Gen) })
	e.Uvarint(uint64(len(gens)))
	for _, g := range gens {
		w.key(g.Gen)
		e.Hash(g.Entry)
	}
	e.Hash(m.GenText)
	w.writeDiags(m.Diags)
	imps := slices.Clone(m.Imports)
	if len(imps) == 0 {
		for _, id := range m.Module.Imports {
			imps = append(imps, env.Items.Module(id).Path)
		}
	}
	slices.Sort(imps)
	e.Uvarint(uint64(len(imps)))
	for _, s := range imps {
		e.String(s)
	}
	writeReads(&e, m.ExternalReads)
	for id, i := range p.itemIdx {
		if p.items[i-1].own && !listed[id] {
			source.ICEf(source.Span{}, "iface: interface %s mentions its item %s but does not list it", m.Path, env.Items.Get(id).Key)
		}
	}
	return p.finish(recInterface, e.Out())
}

// DecodeInterface re-interns into env: it adds the module and its items. A
// foreign key missing from env.Items, an anchor missing from env.Anchors, a
// token-hash mismatch on an anchor, or a file missing from env.Sources
// returns ErrStale (a miss); malformed bytes return ErrCorrupt (a miss).
// Either leaves env.Items as it was.
func DecodeInterface(env *Env, b []byte) (*Interface, error) {
	return decodeInterface(env, b, nil)
}

// DecodeInterfaceInto is DecodeInterface into a module the caller already
// added to env.Items (the driver registers every module of a build first,
// so module IDs are a function of the source tree). into must hold no
// items yet; its Path must equal the record's, else ErrStale. Its Imports
// are the caller's (set from the import graph) and are compared with
// the record's paths: a difference is ErrStale.
func DecodeInterfaceInto(env *Env, b []byte, into *types.Module) (*Interface, error) {
	if into == nil || len(into.Items) > 0 {
		return nil, fmt.Errorf("%w: module to decode into is nil or has items", errLoaded)
	}
	return decodeInterface(env, b, into)
}

func decodeInterface(env *Env, b []byte, into *types.Module) (*Interface, error) {
	mod := into
	module := func() *types.Module {
		if mod == nil {
			mod = &types.Module{}
			env.Items.AddModule(mod)
		}
		return mod
	}
	own := func(k types.Key, kind types.ItemKind) types.ItemID {
		return env.Items.AddItem(&types.Item{Key: k, Kind: kind, Module: module().ID})
	}
	var out *Interface
	err := decodeRecord(env, b, recInterface, own, func(r *reader) error {
		out = &Interface{}
		d := r.d
		out.Path = d.String()
		unsafe, std := d.Bool(), d.Bool()
		var files []source.FileID
		n := d.Count(1)
		for i := 0; i < n && d.Err == nil; i++ {
			path := d.String()
			f, ok := env.Sources.ByPath(path)
			if !ok {
				return fmt.Errorf("%w: file %s not loaded", ErrStale, path)
			}
			files = append(files, f.ID)
		}
		n = d.Count(8)
		for i := 0; i < n && d.Err == nil; i++ {
			idx := d.U32()
			if idx == 0 || int(idx) > len(r.items) || !r.items[idx-1].own {
				d.Failf("interface item index %d", idx)
				break
			}
			id := r.itemIDs[idx]
			name := r.sym(d)
			exported := d.Bool()
			owner := r.item(d)
			span := r.span(d)
			var origin *types.GenOrigin
			if d.Bool() {
				origin = &types.GenOrigin{Output: r.span(d), Emitter: r.span(d), Gen: r.span(d)}
			}
			decl := r.readDecl(d)
			prim := r.typ(d, 0)
			if !r.dry && d.Err == nil {
				it := env.Items.Get(id)
				it.Name, it.Exported, it.Owner, it.Span, it.Origin, it.Decl, it.OwnerPrim = name, exported, owner, span, origin, decl, prim
			}
			out.Items = append(out.Items, id)
		}
		n = d.Count(30)
		for i := 0; i < n && d.Err == nil; i++ {
			out.Shapes = append(out.Shapes, ShapeRec{Item: r.item(d), Shape: r.readShape(d)})
		}
		out.APIHash = d.Hash()
		out.ShapeHash = d.Hash()
		n = d.Count(65)
		for i := 0; i < n && d.Err == nil; i++ {
			out.Bodies = append(out.Bodies, BodyRef{Item: r.key(d), BodyHash: d.Hash(), Entry: d.Hash()})
		}
		n = d.Count(34)
		for i := 0; i < n && d.Err == nil; i++ {
			out.Funcs = append(out.Funcs, FuncRef{Item: r.key(d), Entry: d.Hash(), HasDiags: d.Bool()})
		}
		n = d.Count(33)
		for i := 0; i < n && d.Err == nil; i++ {
			out.Gens = append(out.Gens, GenRef{Gen: r.key(d), Entry: d.Hash()})
		}
		out.GenText = d.Hash()
		out.Diags = r.readDiags(d)
		n = d.Count(1)
		for i := 0; i < n && d.Err == nil; i++ {
			out.Imports = append(out.Imports, d.String())
		}
		if into != nil && d.Err == nil {
			if into.Path != out.Path {
				return fmt.Errorf("%w: interface of %s decoded into module %s", ErrStale, out.Path, into.Path)
			}
			var have []string
			for _, id := range into.Imports {
				have = append(have, env.Items.Module(id).Path)
			}
			slices.Sort(have)
			if !slices.Equal(have, out.Imports) {
				return fmt.Errorf("%w: %s imports changed", ErrStale, out.Path)
			}
		}
		out.ExternalReads = readReads(d)
		out.Anchors = slices.Clone(r.uses)
		sortAnchorUses(out.Anchors)
		if !r.dry && d.Err == nil {
			m := module()
			// A module decoded into an existing entry already has its path
			// (it was found by it); not writing it again keeps other
			// goroutines' SessionWorld.init, which reads every module's
			// path, free of a race with this decode.
			if m.Path != out.Path {
				m.Path = out.Path
			}
			m.Files, m.Items, m.UnsafePermitted, m.Std = files, slices.Clone(out.Items), unsafe, std
			if into == nil {
				for _, p := range out.Imports {
					if id, ok := moduleByPath(env.Items, p); ok {
						m.Imports = append(m.Imports, id)
					}
				}
			}
			out.Module = m
		}
		return d.Err
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// moduleByPath finds a module of items by path (a linear scan: decoding
// without a target module only, in tests).
func moduleByPath(items *types.Items, path string) (types.ModuleID, bool) {
	for i := 1; i < items.NumModules(); i++ {
		if m := items.Module(types.ModuleID(i)); m.Path == path {
			return m.ID, true
		}
	}
	return types.NoModule, false
}

// checkOwnAbsent is run by decoding before any own item is created.
func (r *reader) checkOwnAbsent() error {
	// By key, not by key-pool index: the pool may list one key string at two
	// indices, and two own items with one key are corrupt (FuzzDecodeInterface).
	seen := make(map[types.Key]bool)
	for _, it := range r.items {
		if it.own {
			if seen[r.keys[it.key]] {
				return fmt.Errorf("%w: own item %s twice", ErrCorrupt, r.keys[it.key])
			}
			seen[r.keys[it.key]] = true
			if _, ok := r.env.Items.ByKey(r.keys[it.key]); ok {
				return fmt.Errorf("%w: %s", errLoaded, r.keys[it.key])
			}
		}
	}
	return nil
}
