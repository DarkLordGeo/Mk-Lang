// Why: a generic caller restates a callee's `requires` clause it cannot
// evaluate; matching by text alone would let a caller whose own const
// shadows the callee's (`Lim`), or whose const generic is named as the
// callee module's const, meet a demand its clause does not state, and the
// callee would run with its clause false.
//
// What: demand and demandOf (a demand to restate, with the names it keeps),
// restateMet, callerNames, keptMatch, calleeName, substWords and their
// helpers.
//
// How: a demand is met by a caller's requires that spells the same text and
// whose every name the substitution left alone means, in the caller, what it
// means in the callee. The callee's side is read from its Contract.Text and
// the item table, never its AST, so a module Load decoded answers as a
// checked one does; each lookup is a recorded read. The caller's side is its
// own clause's AST with resolve's refs. A module-qualified name of an
// imported callee's clause (`lim.Lim`) is still matched by text: its file
// binding is not in the contract (calleeName).

package check

import (
	"strings"

	"mk/internal/ast"
	"mk/internal/resolve"
	"mk/internal/source"
	"mk/internal/types"
)

// demand is a clause a caller must restate (requireRestated): text as the
// caller writes it, kept the names the substitution left alone, each at
// its offset in text with what it means in the callee, and why, when not
// empty, the reason no caller here can write it (a name private to the
// callee's module, one of a module this file does not import, or one the
// caller's own generic or parameter hides).
type demand struct {
	text string
	kept []keptName
	why  string
	// noFix: text names another module's item bare, which this file may
	// not see (only the prelude's it does), so no fix is offered.
	noFix bool
	// byText: text keeps a name matched by its text alone (calleeName's
	// empty id), so the fix that states it is not proven.
	byText bool
}

// keptName is a name of a demand's text that the callee's clause spells:
// at is the offset of its first byte in the demand's text, id its meaning
// (nameID's spelling), and bare its word when id is "f:" (a prelude or
// universe name, which the caller matches by word).
type keptName struct {
	at   int
	id   string
	bare string
}

// plainDemand is a demand that keeps no name of a callee (a clause the
// caller spells itself).
func plainDemand(text string) demand { return demand{text: text} }

// demandOf spells clause ct of sig, a callee's, for the caller u checks:
// each name sub holds is replaced by its text (substClause), and each
// other name is kept with its meaning in the callee's module, respelled
// so this file names it: a callee module's exported item through this
// file's import binding (`lib.Lim`), as spellVariant spells a variant.
func (u *unit) demandOf(ct types.Contract, sig *types.Signature, sub map[source.Symbol]string) demand {
	c := u.c
	pred := c.cfg.Items.Get(ct.Body)
	callee := c.cfg.Items.Module(pred.Module)
	own := map[string]bool{} // the callee's own generics and parameters
	for _, g := range sig.Generics {
		own[c.cfg.Interner.Str(g.Name)] = true
	}
	for _, p := range sig.Params {
		own[c.cfg.Interner.Str(p.Name)] = true
	}
	byName := make(map[string]string, len(sub))
	for k, v := range sub {
		byName[c.cfg.Interner.Str(k)] = v
	}
	// imported is the module the callee's file binds w to, for a
	// same-module callee, whose file is here, aliases and all. An imported
	// callee's file bindings are not in its contract: types.Module.Imports
	// is the union over the module's files, and which module a file binds
	// w to is not recorded (guessing it by the last path segment would
	// meet `J < q.Lim` for a callee whose `lim` is another module, and
	// would read the import list with no recorded read), so calleeName
	// keeps such a name as text (false here).
	imported := func(w string) (types.ModuleID, bool) {
		if callee.ID != u.m.ID || u.m.byItem[pred.ID] == nil {
			return types.NoModule, false
		}
		_, fx := u.m.nodeOf(pred.ID)
		for _, b := range fx.info.Imports {
			if c.cfg.Interner.Str(b.Name) == w && b.Module != types.NoModule {
				return b.Module, true
			}
		}
		return types.NoModule, false
	}
	var d demand
	d.text = substWords(ct.Text, func(w string, at int, member string) (string, bool) {
		if s, ok := byName[w]; ok {
			return s, false
		}
		id, spell, why := u.calleeName(callee, w, member, own[w], imported)
		if why != "" && d.why == "" {
			d.why = why
		}
		if id == "" {
			d.byText = true
			return spell, false // matched by its text alone (calleeName)
		}
		k := keptName{at: at, id: id}
		if strings.HasPrefix(id, "f:") {
			k.bare = w
		}
		if spell == w && strings.HasPrefix(id, "i:") && !strings.HasPrefix(id, "i:"+u.m.mod.Path+".") {
			// Another module's item spelled bare, its module not imported
			// here: the prelude's names it, any other is no compiling fix.
			d.noFix = true
		}
		d.kept = append(d.kept, k)
		return spell, true
	})
	return d
}

// calleeName is what name w of the callee module's clause text means (id)
// and how the caller spells it (spell), with why when it cannot (the
// demand is then unwritable). member is the word after w's '.', if any.
// isOwn marks a generic or parameter of the callee that the substitution
// did not replace, which nothing outside the callee names; imported is
// the module a same-module callee's file binds a word to (demandOf). An
// empty id is a name kept by its text alone: a module-qualified name of
// an imported callee that is not an item of its module.
func (u *unit) calleeName(callee *types.Module, w, member string, isOwn bool, imported func(string) (types.ModuleID, bool)) (id, spell, why string) {
	c := u.c
	if isOwn {
		return "?:" + w, w, "it names the callee's own `" + w + "`"
	}
	if iid, ok := c.cfg.Items.ByKey(types.ItemKey(callee.Path, w)); ok {
		it := c.cfg.Items.Get(iid)
		if it.Module != u.m.ID {
			u.x.rec.signature(it.Key)
		}
		id = "i:" + it.Key.String()
		switch {
		case it.Module == u.m.ID:
			return id, w, u.hiddenHere(w, false)
		case !it.Exported:
			return id, w, "`" + w + "` is private to " + callee.Path
		}
		if b := u.bindingOf(it.Module); b != "" {
			return id, b + "." + w, u.hiddenHere(b, false)
		}
		// No import of its module here: the prelude's item (std.core's
		// `Target`) is named bare; any other bare spelling resolves to
		// something else, which keptMatch refuses, and hiddenHere says so
		// when this module or function hides the word.
		return id, w, u.hiddenHere(w, true)
	}
	if callee.ID != u.m.ID {
		u.x.rec.noItem(types.ModuleKey(callee.Path), w)
		if member != "" {
			// `lim.Lim` or `Target.os` of an imported callee: an import
			// binding of its file or a prelude item, which its contract
			// does not tell apart, so the name is kept as the callee
			// writes it and matched by text (id "").
			// Known hole: a caller that binds another module to the same
			// word meets it; resolved keys for such names are a contract
			// change.
			return "", w, u.hiddenHere(w, true)
		}
	}
	if mid, ok := imported(w); ok && member != "" {
		// An import binding of the callee's file and its member.
		mp := c.cfg.Items.Module(mid).Path
		mk := types.ItemKey(mp, member)
		if mi, ok := c.cfg.Items.ByKey(mk); ok {
			it := c.cfg.Items.Get(mi)
			if mid != u.m.ID {
				u.x.rec.signature(it.Key)
			}
			id = "i:" + mk.String()
			if !it.Exported && mid != u.m.ID {
				return id, w, "`" + w + "." + member + "` is private to " + mp
			}
			if mid == u.m.ID {
				return id, w, "it names this module through an import of the callee's"
			}
			b := u.bindingOf(mid)
			if b == "" {
				return id, w, "it names `" + w + "." + member + "` of " + mp + ", which this file does not import"
			}
			return id, b, u.hiddenHere(b, false)
		}
	}
	return "f:" + w, w, u.hiddenHere(w, true)
}

// hiddenHere is why name w, spelled bare in the caller's clause, would not
// mean the callee's: a generic or parameter of the function whose
// requires the caller states (ownerSig) hides it, or, for a prelude or
// universe name (free), an item of this module does.
func (u *unit) hiddenHere(w string, free bool) string {
	_, sig := u.ownerSig()
	if sig != nil {
		for _, g := range sig.Generics {
			if u.c.cfg.Interner.Str(g.Name) == w {
				return "`" + w + "` is this function's own generic here"
			}
		}
		for _, p := range sig.Params {
			if u.c.cfg.Interner.Str(p.Name) == w {
				return "`" + w + "` is this function's own parameter here"
			}
		}
	}
	if free {
		if _, ok := u.c.cfg.Items.ByKey(types.ItemKey(u.m.mod.Path, w)); ok {
			return "`" + w + "` is this module's own item here"
		}
	}
	return ""
}

// bindingOf is the name this file binds module mid to, "" when it does
// not import it.
func (u *unit) bindingOf(mid types.ModuleID) string {
	if u.fx == nil {
		return ""
	}
	for _, b := range u.fx.info.Imports {
		if b.Module == mid {
			return u.c.cfg.Interner.Str(b.Name)
		}
	}
	return ""
}

// substWords scans a clause's text as substClause does and writes the
// result: each name that is not a field (no '.' before it), not a field
// label and not inside a literal or a number goes to f, with its offset in
// the result and the word after its '.' ("" if none); f returns its
// replacement. keep reports a name f kept (substClause ignores it).
func substWords(text string, f func(w string, at int, member string) (spell string, keep bool)) string {
	word := func(r byte) bool {
		return r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r >= 0x80
	}
	var b strings.Builder
	for i := 0; i < len(text); {
		switch ch := text[i]; {
		case ch == '"' || ch == '\'':
			j := litEnd(text, i, false)
			b.WriteString(text[i:j])
			i = j
		case (ch == 'b' || ch == 'r') && i+1 < len(text) && (text[i+1] == '"' || text[i+1] == '\'') && (i == 0 || !word(text[i-1])):
			j := litEnd(text, i+1, ch == 'r')
			b.WriteString(text[i:j])
			i = j
		case word(ch):
			j := i
			for j < len(text) && word(text[j]) {
				j++
			}
			w := text[i:j]
			if (i == 0 || text[i-1] != '.') && !(ch >= '0' && ch <= '9') && !labelAt(text, j) {
				member := ""
				if j+1 < len(text) && text[j] == '.' {
					k := j + 1
					for k < len(text) && word(text[k]) {
						k++
					}
					member = text[j+1 : k]
				}
				w, _ = f(w, b.Len(), member)
			}
			b.WriteString(w)
			i = j
		default:
			b.WriteByte(ch)
			i++
		}
	}
	return b.String()
}

// restateMet reports whether a demand to restate d asks nothing: the
// caller (ownerSig) states it as one of its requires, the same text with
// each name d keeps meaning the callee's (callerNames), or it is a struct
// invariant, where nothing can state it and it stays unchecked.
func (u *unit) restateMet(d demand) bool {
	owner, sig := u.ownerSig()
	if sig == nil && owner != nil && owner.Kind == types.KindStruct {
		return true
	}
	if sig == nil || d.why != "" {
		return false
	}
	for _, ct := range sig.Contracts {
		if ct.Kind != types.Requires {
			continue
		}
		pred := u.c.cfg.Items.Get(ct.Body)
		if u.c.clauseText(pred) != d.text {
			continue
		}
		if len(d.kept) == 0 {
			return true
		}
		if names, ok := u.callerNames(pred); ok && keptMatch(d.kept, names) {
			return true
		}
	}
	return false
}

// keptMatch reports whether every name of kept means at its offset of the
// caller's clause (names) what it means in the callee. A prelude or
// universe name of the callee ("f:") is met by the same universe name, or
// by an item of another module with its word (the prelude's, std.core's
// re-export of std.target's `Os` included).
func keptMatch(kept []keptName, names map[int]callerName) bool {
	for _, k := range kept {
		n, ok := names[k.at]
		switch {
		case !ok:
			return false
		case n.id == k.id:
		case k.bare != "" && n.foreign == k.bare:
		default:
			return false
		}
	}
	return true
}

// callerName is what a name of the caller's clause means: id as calleeName
// spells it, and foreign its word when it is an item of another module
// named bare (a prelude item).
type callerName struct {
	id      string
	foreign string
}

// callerNames maps each name of the caller's requires predicate pred, by
// its offset in clauseText's spelling, to what resolve made of it. False
// when pred has no AST here or exprText's spelling differs from its text.
func (u *unit) callerNames(pred *types.Item) (map[int]callerName, bool) {
	if u.m.byItem[pred.ID] == nil {
		return nil, false
	}
	n, fx := u.m.nodeOf(pred.ID)
	ct, ok := n.(*ast.Contract)
	if !ok || ct.X == nil || fx == nil {
		return nil, false
	}
	c := u.c
	out := map[int]callerName{}
	ref := func(x ast.Node) resolve.Ref {
		if id := x.ID(); int(id) < len(fx.info.Refs) {
			return fx.info.Refs[id]
		}
		return resolve.Ref{}
	}
	item := func(id types.ItemID, word string) callerName {
		it := c.cfg.Items.Get(id)
		n := callerName{id: "i:" + it.Key.String()}
		if it.Module != u.m.ID && word != "" {
			n.foreign = word
		}
		return n
	}
	var b strings.Builder
	c.exprTextNames(&b, ct.X, func(x ast.Node, at int) {
		if _, set := out[at]; set {
			return // a module-qualified item, set at its FieldExpr
		}
		r := ref(x)
		switch x := x.(type) {
		case *ast.FieldExpr:
			if r.Kind == resolve.RefItem {
				out[at] = item(types.ItemID(r.A), "")
			}
			return
		case *ast.PathType:
			word := ""
			if len(x.Path.Names) == 1 {
				word = c.cfg.Interner.Str(x.Path.Names[0].Name)
			}
			switch r.Kind {
			case resolve.RefItem:
				out[at] = item(types.ItemID(r.A), word)
			case resolve.RefPrim:
				out[at] = callerName{id: "f:" + word}
			default:
				out[at] = callerName{id: "x:" + r.Kind.String()}
			}
			return
		case *ast.Ident:
			word := c.cfg.Interner.Str(x.Name)
			switch r.Kind {
			case resolve.RefItem:
				out[at] = item(types.ItemID(r.A), word)
			case resolve.RefUniverse, resolve.RefPrim:
				out[at] = callerName{id: "f:" + word}
			default:
				// A generic, a parameter or a module binding: nothing a
				// name the callee's clause keeps means.
				out[at] = callerName{id: "x:" + r.Kind.String()}
			}
		}
	})
	if b.String() != c.clauseText(pred) {
		return nil, false
	}
	return out, true
}
