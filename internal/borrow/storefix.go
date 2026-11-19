// Why: E0604 (a store the signature does not declare) is fixed by writing
// an `into` clause on the parameter; the error should print that clause and
// carry it as an edit mk fix can apply.
//
// What: storeClause and clauseSlot (the clause text and where it goes),
// storeFix, mergeStoreFix, noClauseAlt, implMethod, privateIntoProven, and
// noteStores and storeSite (E0505's store site).
//
// How: an outer loan's clause is `x: T into p`, written at x; an inner
// loan's is `*x into p`, written after the signature's result and its
// clauses. A fix on a private function is proven when the catalogue says
// so; otherwise it compiles but mk fix does not apply it.

package borrow

import (
	"slices"
	"strings"

	"mk/internal/diag"
	"mk/internal/mir"
	"mk/internal/source"
	"mk/internal/types"
)

// storeClause is the clause E0604 asks for: storing loan l (of parameter
// x) into parameter p. An outer loan of x is `x: T into p`, written at x;
// an inner loan (what x's reference holds) is `*x into p`, written after
// the signature's result. ok is false when no clause can say it: a
// receiver's own outer loan, or x already stores into another parameter.
func (c *checker) storeClause(l loanID, p types.ParamIndex) (text string, edit diag.Edit, ok bool) {
	s := c.env.Sig
	lo := &c.loans[l]
	x := lo.param
	if s == nil || x == types.NoParam || int(x) > len(s.Params) || int(p) > len(s.Params) || p == types.NoParam {
		return "", diag.Edit{}, false
	}
	recv := func(i types.ParamIndex) bool { return int(i) == 1 && s.Receiver != types.NoReceiver }
	inner := lo.inner || lo.tag != nil
	if inner && x == p && c.byValueParamIndex(x) {
		// A by-value parameter's own inner loan stored into its p#1
		// `*x into x` needs a reference or a slice (E0301),
		// and no clause declares a store between two of its referents.
		return "", diag.Edit{}, false
	}
	if inner {
		text = "*" + sigParam(c, s, x) + " into " + sigParam(c, s, p)
		at, sep, ok := c.clauseSlot()
		if !ok {
			return text, diag.Edit{}, true
		}
		return text, diag.Edit{Span: at, Text: sep + text}, true
	}
	if recv(x) {
		return "", diag.Edit{}, false
	}
	px := &s.Params[x-1]
	if px.Into != types.NoParam {
		return "", diag.Edit{}, false
	}
	text = sigParam(c, s, x) + ": " + c.tab.TypeString(px.Type, s.Generics) + " into " + sigParam(c, s, p)
	if px.Span.File == source.NoFile || px.Span.End <= px.Span.Start {
		return text, diag.Edit{}, true
	}
	end := source.Span{File: px.Span.File, Start: px.Span.End, End: px.Span.End}
	return text, diag.Edit{Span: end, Text: " into " + sigParam(c, s, p)}, true
}

// clauseSlot is where a `*x into p` clause goes in the body's signature:
// after the result and any clause written, before a `cpu` or `section`
// clause and the body's `{` (the grammar's order: flows, contracts, cpu,
// section), with ", " after another store clause and " " otherwise.
// False when the text is not at hand, the signature has contracts (they
// follow the clauses), or the result type ends in a fn type, whose flows
// a clause written after it would be.
func (c *checker) clauseSlot() (source.Span, string, bool) {
	s := c.env.Sig
	if c.env.File == nil || len(s.Contracts) > 0 || s.Span.File == source.NoFile || endsInFn(c.tab, s.Result) {
		return source.Span{}, "", false
	}
	f := c.env.File(s.Span.File)
	if f == nil || s.Span.End > f.Len() {
		return source.Span{}, "", false
	}
	text := f.Slice(source.Span{File: s.Span.File, Start: s.Span.Start, End: f.Len()})
	open := strings.IndexByte(text[s.Span.End-s.Span.Start:], '{')
	if open < 0 {
		return source.Span{}, "", false
	}
	end := s.Span.End + uint32(open)
	// Back over the blanks before the `{`, then over a trailing `section
	// "..."` and a `cpu "..."` clause (each a word and one string), so the
	// clause lands before them.
	back := func() {
		for end > s.Span.End && (text[end-1-s.Span.Start] == ' ' || text[end-1-s.Span.Start] == '\t') {
			end--
		}
	}
	back()
	for _, word := range []string{"section", "cpu"} {
		if at, ok := trailingClause(text[:end-s.Span.Start], word); ok && uint32(at) >= s.Span.End-s.Span.Start {
			end = s.Span.Start + uint32(at)
			back()
		}
	}
	sep := " "
	if len(s.Flows.Stores) > 0 && strings.HasSuffix(strings.TrimRight(text[:end-s.Span.Start], " \t"), " into "+lastStoreInto(c, s)) {
		sep = ", "
	}
	return source.Span{File: s.Span.File, Start: end, End: end}, sep, true
}

// trailingClause is the offset in text of a `word "..."` clause that
// ends text (a Cpu or Section clause: the word, any blanks, one string with
// no escape in it), and whether there is one.
func trailingClause(text, word string) (int, bool) {
	if len(text) < 2 || text[len(text)-1] != '"' {
		return 0, false
	}
	q := strings.LastIndexByte(text[:len(text)-1], '"')
	if q < 0 || strings.ContainsAny(text[q+1:len(text)-1], "\\n") {
		return 0, false
	}
	head := strings.TrimRight(text[:q], " \t")
	if !strings.HasSuffix(head, word) {
		return 0, false
	}
	at := len(head) - len(word)
	if at > 0 && isWordByte(head[at-1]) {
		return 0, false
	}
	return at, true
}

func isWordByte(b byte) bool {
	return b == '_' || b >= '0' && b <= '9' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z'
}

// endsInFn reports whether type t, as written, ends in a fn type: t is
// one, or an optional, slice, array or reference of one.
func endsInFn(tab *types.Table, t types.TypeID) bool {
	for k := 0; k < 16 && t != types.NoType; k++ {
		switch tab.Kind(t) {
		case types.KFn:
			return true
		case types.KOptional, types.KSlice, types.KMutSlice, types.KArray, types.KRef, types.KMut:
			t = tab.Get(t).Elem
		default:
			return false
		}
	}
	return false
}

// lastStoreInto is the target of s's last store clause, as written.
func lastStoreInto(c *checker, s *types.Signature) string {
	return sigParam(c, s, s.Flows.Stores[len(s.Flows.Stores)-1].Into)
}

// storeFix finishes E0604 d: the message ends with the clause to write, the
// fact `clause` holds it, `write_at` points at the parameter (or the
// signature for `*x into p`), and the fix is the edit. The fix is proven
// for a private function, whose callers all recompile in the package, when
// the catalogue's E0604 row says so; compiles otherwise (a clause on a
// public function can move errors to callers in other packages).
func (c *checker) storeFix(d *diag.Diagnostic, l loanID, p types.ParamIndex) {
	text, edit, ok := c.storeClause(l, p)
	if !ok {
		c.noClauseAlt(d, l)
		return
	}
	d.Message += "; write `" + text + "`"
	d.Text("clause", text)
	s := c.env.Sig
	if lo := &c.loans[l]; !(lo.inner || lo.tag != nil) && int(lo.param) <= len(s.Params) {
		if sp := s.Params[lo.param-1].Span; sp.File != source.NoFile {
			d.Loc("write_at", sp)
		}
	} else if s.Span.File != source.NoFile {
		d.Loc("write_at", s.Span)
	}
	if edit.Span.File == source.NoFile {
		return
	}
	class := diag.Compiles
	if !s.Exported && !c.implMethod() && privateIntoProven() {
		class = diag.Proven
	}
	d.Fix(diag.Fix{Class: class, Edits: []diag.Edit{edit}, Why: "write `" + text + "`: the signature declares the store the body makes"})
}

// mergeStoreFix folds the clause of E0604 d, dropped by the rule of one
// diagnostic per (code, primary span), into the kept E0604 k: one store that
// reaches two undeclared targets (a list's arena beside the local it holds:
// `*prog into a` and `*prog into prog`) needs both clauses, and a fix naming
// one of them leaves an E0604 at the same place, so mk fix judged it no
// progress. Two `*x into p` clauses at the same slot become one list; clauses
// at different places, two edits. Nothing is merged when either has no clause
// edit, when k's list already holds d's clause (one store reaching the same
// target through several holders, as recorded abstract nodes give, would write
// `*self into prog` several times), or both would put an `into` on the same
// parameter (a parameter takes one).
func mergeStoreFix(k, d *diag.Diagnostic) {
	kc, dc := storeFactText(k, "clause"), storeFactText(d, "clause")
	if kc == "" || dc == "" || slices.Contains(strings.Split(kc, ", "), dc) || len(k.Fixes) != 1 || len(d.Fixes) != 1 ||
		len(k.Fixes[0].Edits) != 1 || len(d.Fixes[0].Edits) != 1 || !strings.HasSuffix(k.Message, "; write `"+kc+"`") {
		return
	}
	ke, de := k.Fixes[0].Edits[0], d.Fixes[0].Edits[0]
	edits := []diag.Edit{ke, de}
	if ke.Span == de.Span {
		if !strings.HasPrefix(kc, "*") || !strings.HasPrefix(dc, "*") {
			// Two `x: T into p` on one parameter: no clause says both, so
			// k offers none (a free function whose `out` holds both `arena`
			// and `m`), as noClauseAlt does for a second `into`.
			x, p1, _ := strings.Cut(kc, ":")
			_, p2, _ := strings.Cut(dc, ":")
			p1, p2 = p1[strings.LastIndex(p1, " into ")+len(" into "):], p2[strings.LastIndex(p2, " into ")+len(" into "):]
			k.Message = strings.TrimSuffix(k.Message, "; write `"+kc+"`")
			k.Facts = slices.DeleteFunc(k.Facts, func(f diag.Field) bool { return f.Key == "clause" || f.Key == "write_at" })
			k.Fixes = nil
			k.Alt("`" + x + "` is stored into both `" + p1 + "` and `" + p2 + "`, and a parameter takes one `into`: keep both in one struct and pass that, or copy what is stored (`Dup` it into an arena)")
			return
		}
		edits = []diag.Edit{{Span: ke.Span, Text: ke.Text + ", " + dc}}
	} else if ke.Span.File == de.Span.File && ke.Span.Start < de.Span.End && de.Span.Start < ke.Span.End {
		return
	}
	both := kc + ", " + dc
	k.Message = strings.TrimSuffix(k.Message, "`"+kc+"`") + "`" + both + "`"
	for i := range k.Facts {
		if k.Facts[i].Key == "clause" {
			k.Facts[i].Value = diag.TextFact(both)
		}
	}
	k.Fixes[0] = diag.Fix{Class: max(k.Fixes[0].Class, d.Fixes[0].Class), Edits: edits,
		Why: "write `" + both + "`: the signature declares the stores the body makes"}
}

// storeFactText is d's text fact key, "" when it has none.
func storeFactText(d *diag.Diagnostic, key string) string {
	for _, f := range d.Facts {
		if f.Key == key {
			return f.Value.Str
		}
	}
	return ""
}

// noClauseAlt says why E0604 d offers no clause for loan l, and what does
// instead: a parameter already flowing into another takes no second `into`,
// and a receiver's own loan has no clause.
func (c *checker) noClauseAlt(d *diag.Diagnostic, l loanID) {
	s := c.env.Sig
	lo := &c.loans[l]
	x := lo.param
	if s == nil || x == types.NoParam || int(x) > len(s.Params) || lo.inner || lo.tag != nil {
		return
	}
	name := sigParam(c, s, x)
	if px := &s.Params[x-1]; px.Into != types.NoParam {
		d.Alt("`" + name + "` already flows into `" + sigParam(c, s, px.Into) + "`, and a parameter takes one `into`: keep both in one struct and pass that")
		return
	}
	if int(x) == 1 && s.Receiver != types.NoReceiver {
		d.Alt("no clause stores the receiver's own loan: copy what is stored (`Dup` it into an arena), or take the container as a parameter")
	}
}

// implMethod reports whether the body is an impl's method. Its flows must
// be covered by the interface method's (E0608), so a clause written on it
// is not proven by the package's callers recompiling: the `into` fix is
// proven for a private function, and an impl method answers
// to its interface, wherever that is declared.
func (c *checker) implMethod() bool {
	s := c.env.Sig
	if c.tab == nil || s.Item == types.NoItem {
		return false
	}
	items := c.tab.Items()
	if items == nil {
		return false
	}
	it := items.Get(s.Item)
	return it != nil && it.Owner != types.NoItem && items.Get(it.Owner).Kind == types.KindImpl
}

// privateIntoProven reports whether the catalogue's E0604 row makes the
// `into` clause proven for a private function (as E0602's `from` is). While
// the catalogue and internal/diag do not say so, the fix stays at compiles,
// which mk fix offers and does not apply.
func privateIntoProven() bool {
	e, ok := diag.E0604.Entry()
	return ok && strings.Contains(strings.ToLower(e.FixClass), "proven for a private")
}

// noteStores records the current statement as where each loan of added
// entered root r's held set, the first time it does (for storeSite).
func (c *checker) noteStores(r rootID, added setID) {
	for _, l := range c.sets.get(added) {
		k := uint64(r)<<32 | uint64(l)
		if c.storedAt == nil {
			c.storedAt = map[uint64]mir.SpanID{}
		}
		if _, ok := c.storedAt[k]; !ok {
			c.storedAt[k] = c.curSpan
		}
	}
}

// storeSite is E0505's primary location: the statement that stored loan l into
// holder, when it is on another line than the loan's creation (the call that
// made the loan may be many lines above the statement that stored it); else
// the creation site. The second result is true when the store site is used,
// and the creation site becomes the note `created_at`.
func (c *checker) storeSite(l loanID, holder rootID) (mir.SpanID, bool) {
	lo := &c.loans[l]
	sp, ok := c.storedAt[uint64(holder)<<32|uint64(l)]
	if !ok || sp == lo.site || c.env.File == nil {
		return lo.site, false
	}
	a, b := c.body.Span(sp), c.body.Span(lo.site)
	if a.File != b.File {
		return lo.site, false
	}
	f := c.env.File(a.File)
	if f == nil || a.Start > f.Len() || b.Start > f.Len() || f.LineCol(a.Start).Line == f.LineCol(b.Start).Line {
		return lo.site, false
	}
	return sp, true
}
