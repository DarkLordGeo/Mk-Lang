// Why: modules resolve in parallel and in depth order, and each needs the
// scopes of the modules it imports, whether they were resolved in this
// process or loaded from the cache.
//
// What: Session (the build's registry of module scope records), modRecord,
// preludeScope, the method indexes MethodsOf and PrimMethods, and Load and
// LoadWith for cache-loaded modules.
//
// How: a module's record is written only by its own Begin, Load and
// AddGenerated, then published once and read lock-free by importers. Load
// rebuilds a record from types.Items; LoadWith adds the import lines, so a
// generator's quote sees the bindings a whole load would give it.

package resolve

import (
	"slices"
	"sync"

	"mk/internal/source"
	"mk/internal/types"
)

// Session holds the module scopes of one build. Safe for concurrent use.
type Session struct {
	cfg *Config
	in  *source.Interner

	mu      sync.Mutex
	recs    map[types.ModuleID]*modRecord
	methods map[types.ItemID][]types.ItemID
	prims   map[types.TypeID][]types.ItemID
	prelude *preludeScope
}

// modRecord is one module's scope record: its module-level
// names, the folded index over them and each generic item's kinds. Written
// only by the module's Begin, Load and AddGenerated, read lock-free by
// importers after it is registered.
type modRecord struct {
	id    types.ModuleID
	path  string
	std   bool
	names source.IndexMap[source.Symbol, types.ItemID]
	// folded maps Fold(name) to the module-level items with that folded name,
	// in declaration order.
	folded map[string][]types.ItemID
	// kinds holds, per item with generics, the const flag of each generic in
	// Signature.Generics order (owner arguments first).
	kinds map[types.ItemID][]bool
	// lit is the union of the import bindings of the module's files, a name
	// bound to two different modules left out: what a NameLiteral token of
	// a generator in this module sees.
	lit []Binding
	// quote is what a NameLiteral token of a generator in this module sees:
	// lit, then the module itself by the last segment of its path, as an
	// importer names it, unless an import of one of its files takes that
	// name. Through that binding, as through any, only exported
	// items are reachable (E0204).
	quote []Binding
	// noLines is set by Load, which has no import lines: lit and quote are
	// unknown, and a generator quote against the record is an ICE rather
	// than a binding a whole load would not give.
	noLines bool
}

func newRecord(m *types.Module) *modRecord {
	return &modRecord{id: m.ID, path: m.Path, std: m.Std, folded: map[string][]types.ItemID{}, kinds: map[types.ItemID][]bool{}}
}

// add inserts a module-level name; the caller has checked collisions.
func (r *modRecord) add(in *source.Interner, name source.Symbol, id types.ItemID) {
	r.names.Insert(name, id)
	f := Fold(in.Str(name))
	r.folded[f] = append(r.folded[f], id)
}

// preludeScope is the names of Config.PreludeNames that the prelude module
// declares as exported top-level items. A listed name it
// does not declare is in no scope: a use is the ordinary E0201.
type preludeScope struct {
	mod    types.ModuleID
	names  map[source.Symbol]types.ItemID
	folded map[string][]types.ItemID
}

// NewSession makes a Session. It does not copy cfg's tables.
func NewSession(cfg *Config) *Session {
	if cfg == nil || cfg.Interner == nil || cfg.Items == nil {
		source.ICEf(source.Span{}, "resolve.NewSession: Config needs Interner and Items")
	}
	return &Session{
		cfg:     cfg,
		in:      cfg.Interner,
		recs:    map[types.ModuleID]*modRecord{},
		methods: map[types.ItemID][]types.ItemID{},
		prims:   map[types.TypeID][]types.ItemID{},
	}
}

// register publishes a module's record once and builds the
// prelude scope when the module is the prelude.
func (s *Session) register(r *modRecord) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, dup := s.recs[r.id]; dup {
		source.ICEf(source.Span{}, "resolve: module %s registered twice", r.path)
	}
	s.recs[r.id] = r
	if s.cfg.PreludePath != "" && r.path == s.cfg.PreludePath {
		s.prelude = s.buildPrelude(r)
	}
}

// rebuildPrelude replaces the prelude scope after the prelude module gained
// generated items (its Settle runs before any importer's).
func (s *Session) rebuildPrelude(r *modRecord) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cfg.PreludePath != "" && r.path == s.cfg.PreludePath {
		s.prelude = s.buildPrelude(r)
	}
}

func (s *Session) buildPrelude(r *modRecord) *preludeScope {
	p := &preludeScope{mod: r.id, names: map[source.Symbol]types.ItemID{}, folded: map[string][]types.ItemID{}}
	for _, n := range s.cfg.PreludeNames {
		sym := s.in.Intern(n)
		id, ok := r.names.Get(sym)
		if ok && s.cfg.Items.Get(id).Exported {
			p.names[sym] = id
			f := Fold(n)
			p.folded[f] = append(p.folded[f], id)
		}
	}
	return p
}

// record returns a registered module's record, nil when it is unknown.
func (s *Session) record(id types.ModuleID) *modRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.recs[id]
}

// preludeFor returns the prelude scope when id is the prelude module.
func (s *Session) preludeFor(id types.ModuleID) *preludeScope {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.prelude != nil && s.prelude.mod == id {
		return s.prelude
	}
	return nil
}

// addMethod indexes a same-module Type.Name method under its owner type.
func (s *Session) addMethod(owner types.ItemID, m types.ItemID) {
	s.mu.Lock()
	s.methods[owner] = append(s.methods[owner], m)
	s.mu.Unlock()
}

// addPrimMethod indexes a std module's method on a primitive type.
func (s *Session) addPrimMethod(t types.TypeID, m types.ItemID) {
	s.mu.Lock()
	s.prims[t] = append(s.prims[t], m)
	s.mu.Unlock()
}

func (s *Session) sortedByKey(ids []types.ItemID) []types.ItemID {
	out := slices.Clone(ids)
	slices.SortFunc(out, func(a, b types.ItemID) int {
		return s.cfg.Items.Get(a).Key.Compare(s.cfg.Items.Get(b).Key)
	})
	return out
}

// MethodsOf returns the Type.Name methods whose owner is the type item t
// (a struct, enum, error, alias or interface) and that are declared in t's
// own module, in key order. check builds member sets from it. A
// method declared on a type of another module is not listed; its Item.Owner
// still names t, and check reports it (E0704).
func (s *Session) MethodsOf(t types.ItemID) []types.ItemID {
	s.mu.Lock()
	ids := s.methods[t]
	s.mu.Unlock()
	return s.sortedByKey(ids)
}

// PrimMethods returns the Type.Name methods std modules declare on a
// primitive type (str.Len), in key order. A user module's
// method on a primitive is not listed (check's E0704).
func (s *Session) PrimMethods(t types.TypeID) []types.ItemID {
	s.mu.Lock()
	ids := s.prims[t]
	s.mu.Unlock()
	return s.sortedByKey(ids)
}

// Load registers a module that was not resolved in this process (an
// interface loaded from the cache by iface): its scope and folded index are
// rebuilt from types.Items (Module.Items, Item.Name, Kind, Owner, Exported),
// and each item's generic kinds from Item.Decl, which iface fills on load,
// so importers can resolve against it. Loading a module twice is an ICE.
//
// Load has no import lines, so the record has no quote bindings: a quote of
// one of its generators is an ICE (literalBindings). Module.Imports cannot
// stand in for the lines: the driver lists there the prelude a module sees and
// std.rt with no import line written, and an alias is not in it, so bindings
// guessed from it would differ from a whole load's silently. A module whose
// generator may run is loaded with LoadWith.
func (s *Session) Load(m types.ModuleID) {
	s.load(m, nil, true)
}

// LoadWith is Load with the module's import lines, so a generator's quote
// sees the bindings a whole load gives it, aliases and implicit bindings
// included: files holds ImportsOf of each of the module's files in
// Module.Files order (the driver has them from each file's parse summary),
// and imports and implicit are what ModuleInput.Imports and Implicit would
// be. The lines are covered by the module's cache prefix through each
// file's iface.FileTokens.Outside, the token hash of the file outside its
// items (FileAnchor.TokenHash, import lines included), so an edited import
// misses before it reaches here.
func (s *Session) LoadWith(m types.ModuleID, files [][]ImportLine, imports map[string]types.ModuleID, implicit []Implicit) {
	per := make([][]Binding, len(files))
	for i, lines := range files {
		per[i] = s.lineBindings(lines, imports, implicit)
	}
	s.load(m, per, false)
}

// lineBindings is one file's bindings as declareImports makes them, with
// no diagnostics: the implicit bindings, then each import line whose name
// no earlier binding of the file takes by Fold (a duplicate is E0206 there
// and binds nothing).
func (s *Session) lineBindings(lines []ImportLine, imports map[string]types.ModuleID, implicit []Implicit) []Binding {
	var out []Binding
	seen := map[string]bool{}
	for _, im := range implicit {
		out = append(out, Binding{Name: im.Name, Module: im.Module, Path: s.cfg.Items.Module(im.Module).Path})
		seen[Fold(s.in.Str(im.Name))] = true
	}
	for _, il := range lines {
		f := Fold(s.in.Str(il.Binding))
		if seen[f] {
			continue
		}
		seen[f] = true
		out = append(out, Binding{Name: il.Binding, Module: imports[il.Path], Path: il.Path, Node: il.Node})
	}
	return out
}

// load is Load (noLines, no bindings) and LoadWith over the module's
// per-file import bindings.
func (s *Session) load(m types.ModuleID, files [][]Binding, noLines bool) {
	mod := s.cfg.Items.Module(m)
	r := newRecord(mod)
	for _, id := range mod.Items {
		it := s.cfg.Items.Get(id)
		if it.Owner == types.NoItem && it.OwnerPrim == types.NoType && isNamedKind(it.Kind) && it.Name.IsValid() {
			if _, had := r.names.Get(it.Name); !had {
				r.add(s.in, it.Name, id)
			}
		}
		if it.Kind == types.KindFunc && it.Owner != types.NoItem && s.cfg.Items.Get(it.Owner).Module == m &&
			isTypeKind(s.cfg.Items.Get(it.Owner).Kind) {
			s.addMethod(it.Owner, id)
		}
		if it.Kind == types.KindFunc && it.OwnerPrim != types.NoType && mod.Std {
			s.addPrimMethod(it.OwnerPrim, id)
		}
		if k := kindsFromDecl(it.Decl); k != nil {
			r.kinds[id] = k
		}
	}
	// As Begin: the union of the files' bindings, then the module itself
	// by the last segment of its path unless a binding takes that name.
	r.noLines = noLines
	if !noLines {
		r.lit = unionOf(files)
		r.quote = quoteBindings(r.lit, selfBinding(s.in, m, mod.Path, takenIn(files)))
	}
	s.register(r)
}

// kindsFromDecl reads generic kinds from a checked declaration (Load only).
func kindsFromDecl(d types.Decl) []bool {
	var gs []types.GenericParam
	switch d := d.(type) {
	case *types.StructDecl:
		gs = d.Generics
	case *types.EnumDecl:
		gs = d.Generics
	case *types.InterfaceDecl:
		gs = d.Generics
	case *types.AliasDecl:
		gs = d.Generics
	case *types.FuncDecl:
		gs = d.Sig.Generics
	}
	if len(gs) == 0 {
		return nil
	}
	out := make([]bool, len(gs))
	for i, g := range gs {
		out[i] = g.Const
	}
	return out
}

// isNamedKind reports whether items of the kind have a module-level name.
func isNamedKind(k types.ItemKind) bool {
	switch k {
	case types.KindStruct, types.KindEnum, types.KindError, types.KindInterface, types.KindAlias,
		types.KindConst, types.KindGlobal, types.KindFunc:
		return true
	}
	return false
}

// isTypeKind reports whether items of the kind are types.
func isTypeKind(k types.ItemKind) bool {
	switch k {
	case types.KindStruct, types.KindEnum, types.KindError, types.KindInterface, types.KindAlias:
		return true
	}
	return false
}
