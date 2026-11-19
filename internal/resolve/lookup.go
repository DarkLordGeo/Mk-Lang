// Why: a bare name has one meaning, found by one fixed chain of scopes, and
// a miss should come with the most useful fix: a folded spelling, a habit
// from another language, or an import.
//
// What: the walker's lookup methods: find (the exact chain), name, the
// capture recording (useLocal, useResult, useField, captureChain), miss and
// missWith (the miss path), foldedMatch, synonym, qualify, importEdit,
// unknown, and the folded candidate lists.
//
// How: the exact chain is locals, type parameters, the module scope, import
// bindings, the prelude, the universe. The miss path then tries pending
// names, the pattern heads `ok` and `err` in value position, the folded
// fallback, the habit names (E1209, E1221) and last E0201 with the import
// fix. Each lookup records the reads a cached result depends on, and repeated
// unknown spellings group under the first diagnostic.

package resolve

import (
	"strings"

	"mk/internal/ast"
	"mk/internal/diag"
	"mk/internal/source"
	"mk/internal/types"
)

// pos is where a name is used: it filters the folded fallback.
type pos uint8

const (
	posValue pos = iota + 1
	posType
	posPattern
	posBound
)

func (p pos) isType() bool { return p == posType || p == posBound }

// hit is an exact answer of the chain.
type hit struct {
	ref  Ref
	step int // 1 locals and captures, 2 scoped names, 3 module, 4 import, 5 prelude, 6 universe
	// binding is the index of the import binding at step 4.
	binding int
	// local is the stack entry at step 1, and idx its index in locals.
	local *scoped
	idx   int
}

// find runs the exact chain for one name (steps 1 to 6 of hit.step).
func (w *walker) find(sym source.Symbol) (hit, bool) {
	for i := len(w.locals) - 1; i >= 0; i-- {
		if w.locals[i].sym == sym {
			sc := &w.locals[i]
			b := w.bodies[sc.body]
			l := &b.body.Locals[sc.local]
			kind := RefLocal
			if l.Kind == LocalParam || l.Kind == LocalSelf {
				kind = RefParam
			}
			return hit{ref: Ref{Kind: kind, Segs: 1, A: uint32(sc.local), B: uint32(b.rec.item.ID)}, step: 1, local: sc, idx: i}, true
		}
	}
	for i := len(w.tparams) - 1; i >= 0; i-- {
		if g := w.tparams[i]; g.sym == sym {
			return hit{ref: Ref{Kind: RefTypeParam, Segs: 1, A: uint32(g.idx), B: uint32(g.item)}, step: 2}, true
		}
	}
	for _, f := range w.fields {
		if f.sym == sym {
			return hit{ref: Ref{Kind: RefField, Segs: 1, A: uint32(f.idx), B: uint32(f.item)}, step: 2}, true
		}
	}
	if sym == source.SymResult && w.result != types.NoItem {
		return hit{ref: Ref{Kind: RefResult, Segs: 1, B: uint32(w.result)}, step: 2}, true
	}
	if sym == source.SymSelfType && w.selfType != types.NoItem {
		return hit{ref: Ref{Kind: RefSelfType, Segs: 1, B: uint32(w.selfType)}, step: 2}, true
	}
	if !w.literalName() {
		if id, ok := w.m.rec.names.Get(sym); ok {
			return hit{ref: itemRef(id), step: 3}, true
		}
	}
	for i, b := range w.bindings() {
		if b.Name == sym {
			return hit{ref: Ref{Kind: RefModule, Segs: 1, A: uint32(b.Module)}, step: 4, binding: i}, true
		}
	}
	if id, ok := w.m.preludeName(sym); ok {
		return hit{ref: itemRef(id), step: 5}, true
	}
	if t, ok := primType(sym); ok {
		return hit{ref: Ref{Kind: RefPrim, Segs: 1, A: uint32(t)}, step: 6}, true
	}
	if source.IsUniverse(sym) && sym != source.SymOk && sym != source.SymErr {
		return hit{ref: Ref{Kind: RefUniverse, Segs: 1, A: uint32(sym)}, step: 6}, true
	}
	return hit{}, false
}

// literalName reports whether the current token is a NameLiteral of
// generated code, which never sees a module scope.
func (w *walker) literalName() bool { return w.origin.Kind == NameLiteral }

// bindings are the import bindings the current token sees: the file's, or
// for a NameLiteral token the union of the generator module's files'.
func (w *walker) bindings() []Binding {
	if w.origin.Kind == NameLiteral {
		return w.m.literalBindings(w.origin.Module)
	}
	return w.fs.fi.Imports
}

// recordReads applies the read rules to one lookup.
func (w *walker) recordReads(sym source.Symbol, h hit, found bool) {
	if !found || h.step >= 4 {
		w.read(types.Read{Kind: types.ReadNoItem, Key: types.ModuleKey(w.m.mod.Path), Name: w.m.str(sym)})
		if found && w.m.hasGen && !w.m.settled {
			w.t.pastModule[sym] = true
		}
	}
	if found && (h.step == 3 || h.step == 5) {
		w.read(types.Read{Kind: types.ReadSignature, Key: w.m.s.cfg.Items.Get(types.ItemID(h.ref.A)).Key})
	}
}

// read records a read on the current item.
func (w *walker) read(r types.Read) { w.rec.info.Reads.Add(r) }

// name resolves one bare name at pos p and records its Ref on the ident.
func (w *walker) name(id *ast.Ident, p pos) Ref {
	if id == nil {
		return poisonRef
	}
	w.setOrigin(id)
	defer w.clearOrigin()
	if r, ok := w.pinned(id); ok {
		w.fs.setRef(id.ID(), r)
		return r
	}
	h, ok := w.find(id.Name)
	w.recordReads(id.Name, h, ok)
	var r Ref
	switch {
	case ok && h.step == 1:
		r = w.useLocal(id, h)
	case ok && h.step == 4:
		w.useBinding(h.binding)
		d := diag.New(diag.E0201, id.Span(), "`"+w.m.str(id.Name)+"` is a module, not a value").Text("name", w.m.str(id.Name))
		if p.isType() {
			d.Text("expected", "type")
		} else {
			d.Text("expected", "value")
		}
		w.report(d, id.Name)
		r = poisonRef
	case ok && h.ref.Kind == RefResult:
		r = w.useResult(id, h)
	case ok && h.ref.Kind == RefField:
		r = w.useField(id, h)
	case ok:
		r = h.ref
	default:
		r = w.miss(id, p, ast.NoNode)
	}
	w.fs.setRef(id.ID(), r)
	return r
}

// useLocal marks a local used and records a capture by fn literals: for
// every literal frame from the outermost one above the local's body to the
// innermost, the name joins the frame's captures, its Of the local itself
// for the outermost and the next outer frame's capture for the others; the
// use is RefCapture of the innermost. Resolve reports no E0609 for it
// (check does).
func (w *walker) useLocal(id *ast.Ident, h hit) Ref {
	sc := h.local
	b := w.bodies[sc.body]
	b.body.Locals[sc.local].Used = true
	lit := w.innermostLiteral()
	if lit == nil || sc.body >= lit.base {
		return h.ref
	}
	first := len(w.lits) - 1
	for first > 0 && w.lits[first-1].base > sc.body {
		first--
	}
	return w.captureChain(id.Name, id.Span(), id.ID(), first, h.ref, sc.constFn, func(f *litFrame) bool { return w.declaredInFieldsLoop(f, sc.body, h.idx) })
}

// useResult is useLocal for result inside a fn literal of an ensures
// predicate: result is the predicate's parameter, so a literal of the
// predicate captures it as it captures a parameter, with Of the RefResult.
// Outside a literal it is the RefResult itself.
func (w *walker) useResult(id *ast.Ident, h hit) Ref {
	lit := w.innermostLiteral()
	if lit == nil || lit.base <= w.resultBody {
		return h.ref
	}
	first := len(w.lits) - 1
	for first > 0 && w.lits[first-1].base > w.resultBody {
		first--
	}
	return w.captureChain(id.Name, id.Span(), id.ID(), first, h.ref, false, nil)
}

// useField is a bare field name of an invariant (RefField): inside a fn
// literal of the predicate it is a field of the predicate's self, which the
// literal captures as it captures any parameter. The name keeps its
// RefField; check reads the field through the literal's capture of self.
func (w *walker) useField(id *ast.Ident, h hit) Ref {
	lit := w.innermostLiteral()
	if lit == nil || lit.base <= w.fieldsBody {
		return h.ref
	}
	sh, ok := w.find(source.SymSelf)
	if !ok || sh.step != 1 || sh.local == nil || sh.local.body != w.fieldsBody {
		return h.ref // a receiver of the literal is the parser's E0101
	}
	w.bodies[sh.local.body].body.Locals[sh.local.local].Used = true
	first := len(w.lits) - 1
	for first > 0 && w.lits[first-1].base > w.fieldsBody {
		first--
	}
	w.captureChain(source.SymSelf, id.Span(), ast.NoNode, first, sh.ref, false, nil)
	return h.ref
}

// captureChain records name as a capture of every literal frame from
// w.lits[first] to the innermost, first used at at: Of is of for the
// outermost and the next outer frame's capture for the others; fieldsLoop,
// when set, gives the FieldsLoop flag per frame. It returns the RefCapture
// of the innermost, which use (when it is a node) resolves to.
func (w *walker) captureChain(name source.Symbol, at source.Span, use ast.NodeID, first int, of Ref, constFn bool, fieldsLoop func(*litFrame) bool) Ref {
	var outer *litFrame
	for _, f := range w.lits[first:] {
		c := Capture{Name: name, Of: of, First: at, ConstFn: constFn,
			FieldsLoop: fieldsLoop != nil && fieldsLoop(f)}
		i, isNew := f.capture(c)
		if outer != nil && isNew {
			// Once per (literal, name): closeCaptures rewrites the Of of
			// each entry, and a second entry would rewrite it twice.
			outer.inner = append(outer.inner, innerCap{info: f.rec.info, name: name})
		}
		of = Ref{Kind: RefCapture, Segs: 1, A: i, B: uint32(f.rec.item.ID)}
		outer = f
	}
	if use != ast.NoNode {
		lit := w.innermostLiteral()
		lit.uses = append(lit.uses, use)
	}
	return of
}

// declaredInFieldsLoop is the FieldsLoop flag for literal frame f and a name
// declared at index idx of locals in body decl: some fields loop that
// contains f is a statement of decl, and the name is declared by its head
// or inside its body. A loop of an outer body does not count: a literal
// inside that loop is a unit of its own, checked once, so the locals it
// declares have one type.
func (w *walker) declaredInFieldsLoop(f *litFrame, decl, idx int) bool {
	for _, fl := range w.fieldsLoops[:f.loops] {
		if fl.body == decl && idx >= fl.locals {
			return true
		}
	}
	return false
}

// useBinding marks an import binding used by this task.
func (w *walker) useBinding(i int) {
	if w.origin.Kind == NameLiteral {
		return
	}
	if i < len(w.t.used) {
		w.t.used[i] = true
	}
}

// report adds a diagnostic of the current task, grouping a repeated
// spelling's unknown-name diagnostics under the first.
func (w *walker) report(d *diag.Diagnostic, sym source.Symbol) {
	if sym.IsValid() && (d.Code == diag.E0201 || d.Code == diag.E1209 || d.Code == diag.E1221) {
		k := causeKey{item: w.rec.item.ID, code: d.Code, sym: sym}
		if first, ok := w.causes[k]; ok {
			d.ConsequenceOf(d.Code, first)
		} else {
			w.causes[k] = d.At
		}
	}
	w.t.diags = append(w.t.diags, d)
}

type causeKey struct {
	item types.ItemID
	code diag.Code
	sym  source.Symbol
}

// miss runs the miss path for a name no step answered. holder, when set,
// is the node whose Ref a pending retry must also update.
func (w *walker) miss(id *ast.Ident, p pos, holder ast.NodeID) Ref {
	f := Fold(w.m.str(id.Name))
	if w.m.missesPend() {
		w.t.pending = append(w.t.pending, pendingName{file: w.fs.idx, node: id.ID(), id: id, pos: p, item: w.rec, folded: w.localCandidates(p, f)})
		return Ref{Kind: RefPending, Segs: 1}
	}
	return w.missWith(id, p, w.localCandidates(p, f))
}

// missesPend reports whether a bare miss is pending: the
// module has unsettled gen lines, or its prelude module has, whose
// generated names a bare name may be once the prelude settles (the rule
// for m.X applied to the prelude as the implicit qualifier).
func (m *Module) missesPend() bool {
	return !m.settled && (m.hasGen || m.preludeGen)
}

// missWith is the miss path after pending.
func (w *walker) missWith(id *ast.Ident, p pos, local []candidate) Ref {
	sym := id.Name
	spell := w.m.str(sym)
	if (sym == source.SymOk || sym == source.SymErr) && p == posValue {
		d := diag.New(diag.E0201, id.Span(), "`"+spell+"` is a pattern head, not a value").Text("name", spell).Text("reason", "pattern-head")
		headAlts(d, sym)
		w.report(d, sym)
		return poisonRef
	}
	cands := w.foldedCandidates(spell, p, local)
	w.exportsReads(cands)
	if len(cands) == 1 {
		return w.foldedMatch(id, p, cands[0])
	}
	if p == posValue && (spell == "null" || spell == "nil") {
		d := diag.New(diag.E1209, id.Span(), "`"+spell+"` is written `none` in Mk")
		d.Fix(diag.Fix{Class: diag.Proven, Edits: []diag.Edit{{Span: id.Span(), Text: "none"}}, Why: "the empty optional is `none`"})
		w.report(d, sym)
		w.habit(d)
		return poisonRef
	}
	if syn, ok := synonymFor(spell); ok {
		w.synonym(id.Span(), spell, syn, sym)
		return poisonRef
	}
	w.unknown(id, p, cands)
	return poisonRef
}

// headAlts adds the alternatives for ok or err used as a value.
func headAlts(d *diag.Diagnostic, sym source.Symbol) {
	if sym == source.SymOk {
		d.Alt("return the value itself")
	} else {
		d.Alt("`fail e`")
	}
}

// exportsReads records ReadExports of every other module whose names the
// folded candidates came from (only the prelude's).
func (w *walker) exportsReads(cands []candidate) {
	items := w.m.s.cfg.Items
	for _, c := range cands {
		if c.ref.Kind != RefItem {
			continue
		}
		if it := items.Get(types.ItemID(c.ref.A)); it.Module != w.m.mod.ID {
			w.read(types.Read{Kind: types.ReadExports, Key: types.ModuleKey(items.Module(it.Module).Path)})
		}
	}
}

// foldedMatch reports E0207 (a) for the one folded match.
func (w *walker) foldedMatch(id *ast.Ident, p pos, c candidate) Ref {
	spell := w.m.str(id.Name)
	d := diag.New(diag.E0207, id.Span(), "`"+spell+"` is written `"+c.name+"`").Text("declared", c.name)
	if c.at.IsValid() {
		d.Loc("declared_at", c.at)
	}
	// `Some`, `Ok`, `Err` and `None` are habits, merged per line.
	isHabit := (c.head || c.ref.Kind == RefUniverse) && (spell == "Some" || spell == "None" || spell == "Ok" || spell == "Err")
	if c.head && p == posValue {
		headAlts(d, w.m.s.in.Intern(c.name))
		d.Alt("`ok` and `err` are pattern heads only")
		w.report(d, id.Name)
		if isHabit {
			w.habit(d)
		}
		return poisonRef
	}
	d.Fix(diag.Fix{Class: diag.Proven, Edits: []diag.Edit{{Span: id.Span(), Text: c.name}}, Why: "names are exact; this is the declared spelling"})
	w.report(d, id.Name)
	if isHabit {
		w.habit(d)
	}
	if c.head {
		return poisonRef
	}
	switch c.ref.Kind {
	case RefLocal, RefParam:
		w.markUsed(c.ref)
	case RefModule:
		w.useBinding(c.binding)
	}
	return c.ref
}

// synonym reports E1221 for a foreign name. A target in another
// module (`Rc` to `sync.Shared`) is spelled through the file's binding of
// that module when it has one (`sy.Shared` under `import sy = std.sync`),
// else through the binding the import line adds.
func (w *walker) synonym(at source.Span, spell string, syn Synonym, sym source.Symbol) {
	text, needImport, free := syn.MkName, false, true
	if syn.Import != "" {
		path := strings.TrimPrefix(syn.Import, "import ")
		member := syn.MkName[strings.IndexByte(syn.MkName, '.')+1:]
		text, needImport, free = w.qualify(path, member)
	}
	d := diag.New(diag.E1221, at, "`"+spell+"` is written `"+text+"` in Mk").Text("mk_name", text)
	if needImport {
		d.Text("import", syn.Import)
	}
	switch {
	case syn.Rename && free:
		edits := []diag.Edit{{Span: at, Text: text}}
		if needImport {
			edits = append(edits, w.importEdit(syn.Import))
		}
		d.Fix(diag.Fix{Class: diag.Proven, Edits: edits, Why: "the Mk name of `" + spell + "`"})
	case needImport:
		d.Alt("`" + syn.Import + "` and write `" + text + "`")
	default:
		d.Alt("use `" + text + "`")
	}
	w.report(d, sym)
	w.habit(d)
}

// qualify spells the item member of the module at path as this file can
// name it: through the file's binding of path when it has one (needImport
// false), else through the binding `import path` would add, the path's last
// segment. free is false when that binding name already means something at
// this use (a local, an item, another binding), so no rewrite is proven
// (Mk imports bind only the module name).
func (w *walker) qualify(path, member string) (text string, needImport, free bool) {
	for _, b := range w.fs.fi.Imports {
		if b.Path == path {
			return w.m.str(b.Name) + "." + member, false, true
		}
	}
	bind := path[strings.LastIndexByte(path, '.')+1:]
	if sym, ok := w.m.s.in.Lookup(bind); ok {
		if _, hit := w.find(sym); hit {
			return bind + "." + member, true, false
		}
	}
	return bind + "." + member, true, true
}

// importEdit inserts an import line after the file's last import, or at the
// start of the file.
func (w *walker) importEdit(line string) diag.Edit {
	f := w.fs.fi.File
	var last ast.Node
	for _, it := range f.Items {
		if im, ok := it.(*ast.Import); ok {
			last = im
		}
	}
	if last == nil {
		return diag.Edit{Span: source.Span{File: w.fs.fi.FileID}, Text: line + "\n"}
	}
	end := last.Span().End
	return diag.Edit{Span: source.Span{File: w.fs.fi.FileID, Start: end, End: end}, Text: "\n" + line}
}

// unknown reports E0201 with the catalog's suggestion.
func (w *walker) unknown(id *ast.Ident, p pos, folded []candidate) {
	spell := w.m.str(id.Name)
	d := diag.New(diag.E0201, id.Span(), "unknown name `"+spell+"`").Text("name", spell)
	var paths []string
	if cat := w.m.s.cfg.Catalog; cat != nil {
		var searched []string
		paths, searched = cat.ExportedBy(spell)
		for _, s := range searched {
			w.read(types.Read{Kind: types.ReadExports, Key: types.ModuleKey(s)})
		}
	}
	switch {
	case len(paths) == 1:
		// The fix qualifies the use too: an import binds only the module
		// name, so `ReadText` becomes `fs.ReadText`.
		line := "import " + paths[0]
		text, needImport, free := w.qualify(paths[0], spell)
		d.Text("module", paths[0])
		if needImport {
			d.Text("import", line)
		}
		edits := []diag.Edit{{Span: id.Span(), Text: text}}
		if needImport {
			edits = append(edits, w.importEdit(line))
		}
		switch {
		case free:
			d.Fix(diag.Fix{Class: diag.Proven, Edits: edits, Why: "`" + paths[0] + "` exports `" + spell + "`"})
		case needImport:
			d.Alt("`" + line + "` and write `" + text + "`")
		default:
			d.Alt("write `" + text + "`")
		}
	case len(paths) > 1:
		d.Fact("candidates", diag.TextList(paths...))
		for _, pth := range paths {
			d.Alt("import " + pth)
		}
	}
	for _, c := range folded {
		d.Alt("`" + c.name + "`")
	}
	if p.isType() && len(paths) == 0 && len(folded) == 0 {
		d.Text("expected", "type")
	}
	w.report(d, id.Name)
}

// localCandidates are the folded-fallback entries visible only at this use
// whose folded spelling is f: locals, parameters, type parameters, fields,
// self, result and Self. A local is compared by its stored fold, so a miss
// allocates nothing for the locals that do not match.
func (w *walker) localCandidates(p pos, f string) []candidate {
	var out []candidate
	if !p.isType() {
		for i := len(w.locals) - 1; i >= 0; i-- {
			if w.locals[i].fold != f {
				continue
			}
			sc := w.locals[i]
			b := w.bodies[sc.body]
			kind := RefLocal
			if l := b.body.Locals[sc.local]; l.Kind == LocalParam || l.Kind == LocalSelf {
				kind = RefParam
			}
			if lit := w.innermostLiteral(); lit != nil && sc.body < lit.base {
				continue
			}
			out = append(out, candidate{name: w.m.str(sc.sym), ref: Ref{Kind: kind, Segs: 1, A: uint32(sc.local), B: uint32(b.rec.item.ID)}, at: sc.at})
		}
		for _, fd := range w.fields {
			if n := w.m.str(fd.sym); Fold(n) == f {
				out = append(out, candidate{name: n, ref: Ref{Kind: RefField, Segs: 1, A: uint32(fd.idx), B: uint32(fd.item)}, at: fd.at})
			}
		}
		if w.result != types.NoItem && f == "result" {
			out = append(out, candidate{name: "result", ref: Ref{Kind: RefResult, Segs: 1, B: uint32(w.result)}})
		}
	}
	for _, g := range w.tparams {
		if n := w.m.str(g.sym); Fold(n) == f {
			out = append(out, candidate{name: n, ref: Ref{Kind: RefTypeParam, Segs: 1, A: uint32(g.idx), B: uint32(g.item)}, at: g.at, typ: true})
		}
	}
	if w.selfType != types.NoItem && f == "self" {
		out = append(out, candidate{name: "Self", ref: Ref{Kind: RefSelfType, Segs: 1, B: uint32(w.selfType)}, typ: true})
	}
	return out
}

// foldedCandidates collects every entry whose folded spelling equals the
// name's, filtered by position; the pattern heads count
// only when no ordinary entry matches.
func (w *walker) foldedCandidates(spell string, p pos, local []candidate) []candidate {
	f := Fold(spell)
	var out []candidate
	add := func(c candidate) {
		if p.isType() && !c.typ {
			return
		}
		for _, o := range out {
			if o.ref == c.ref {
				return
			}
		}
		out = append(out, c)
	}
	for _, c := range local {
		if Fold(c.name) == f {
			add(c)
		}
	}
	items := w.m.s.cfg.Items
	if !w.literalName() {
		for _, id := range w.m.rec.folded[f] {
			it := items.Get(id)
			add(candidate{name: w.m.str(it.Name), ref: itemRef(id), at: it.Span, typ: isTypeKind(it.Kind)})
		}
	}
	if !p.isType() {
		for i, b := range w.bindings() {
			if Fold(w.m.str(b.Name)) == f {
				add(candidate{name: w.m.str(b.Name), ref: Ref{Kind: RefModule, Segs: 1, A: uint32(b.Module)}, binding: i})
			}
		}
	}
	if w.m.prelude != nil {
		for _, id := range w.m.prelude.folded[f] {
			it := items.Get(id)
			add(candidate{name: w.m.str(it.Name), ref: itemRef(id), at: it.Span, typ: isTypeKind(it.Kind)})
		}
	}
	for s := source.SymI8; s <= source.SymNever; s++ {
		if n := w.m.str(s); Fold(n) == f {
			t, _ := primType(s)
			add(candidate{name: n, ref: Ref{Kind: RefPrim, Segs: 1, A: uint32(t)}, typ: true})
		}
	}
	for s := source.SymTrue; s <= source.SymEvalArena; s++ {
		if s == source.SymOk || s == source.SymErr {
			continue
		}
		if n := w.m.str(s); Fold(n) == f {
			bound := s == source.SymPlain || s == source.SymSend || s == source.SymShare || s == source.SymFieldsBound
			add(candidate{name: n, ref: Ref{Kind: RefUniverse, Segs: 1, A: uint32(s)}, typ: bound})
		}
	}
	if len(out) == 0 && !p.isType() {
		for _, s := range []source.Symbol{source.SymOk, source.SymErr} {
			if n := w.m.str(s); Fold(n) == f {
				out = append(out, candidate{name: n, head: true, ref: poisonRef})
			}
		}
	}
	return out
}
