// Why: not every changed fact breaks a caller. The compatibility rules say
// which changes are additions or only reported, and mk apidiff must apply
// them the same way every time.
//
// What: the rules (apiMeta, openSets, closedGrowth, grewOpen, relaxed,
// signatureGrewOpen, openTwin) and APIDiffStd, which compares two std trees.
// An Experimental module's changes are reported, not breaking. A member
// added to an open set (written `open` in the older release, or carrying
// the older doc line `/// May gain members.`) is an addition, while writing
// the word on a set with neither mark is breaking and removing it reported.
// A field added to an options type is an addition.
//
// How: each rule reads the older release's declaration, since that is what
// its callers were promised: members added to a closed set are breaking,
// members added to an open one are not.

package driver

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"mk/internal/types"
)

// openSetDoc is the doc line that marks an open set in a tree written
// before the word open existed, so such a tree is read by it.
const openSetDoc = "/// May gain members."

// apiMeta is what the rules read of one item besides its facts.
type apiMeta struct {
	mod string
	// open: the declaration is written open, or its doc has
	// openSetDoc (a tree from before the word).
	open bool
	// decl is the declaration as mk query sig spells it, with the word
	// open: what a change shows, while the "declaration" fact compares
	// the head without it.
	decl string
	// options: a struct with an unexported field and an exported constant
	// Default<Name> of its type in its module.
	options bool
	// set: an error set alias. members are its members, or a function's
	// error members, "Name  module" each, sorted.
	set     bool
	members []string
	// names are the type names its declaration's head spells (an alias's
	// right side, a function's signature): `Error`, `net.Error`.
	names []string
	// refs are names resolved to keys of the same release (resolveRefs).
	refs []string
}

// defaultConsts are, by module, the struct items that an exported const of
// the module named Default<Name> has as its type.
func (qx *queryIndex) defaultConsts() map[string]map[types.ItemID]bool {
	out := map[string]map[types.ItemID]bool{}
	for _, e := range qx.entries {
		it := qx.w.items.Get(e.id)
		d, ok := it.Decl.(*types.ConstDecl)
		if !ok || !it.Exported {
			continue
		}
		ty := qx.w.tab.Get(d.Type)
		if ty.Kind != types.KNamed || "Default"+qx.str(qx.w.items.Get(ty.Item).Name) != e.name {
			continue
		}
		if out[e.mod] == nil {
			out[e.mod] = map[types.ItemID]bool{}
		}
		out[e.mod][ty.Item] = true
	}
	return out
}

// apiMetaOf is e's meta.
func (qx *queryIndex) apiMetaOf(e qEntry, defaults map[string]map[types.ItemID]bool) apiMeta {
	it := qx.w.items.Get(e.id)
	m := apiMeta{mod: e.mod, open: declOpen(it.Decl) || slices.Contains(qx.doc(it), openSetDoc)}
	if declOpen(it.Decl) {
		m.decl = qx.declLine(e)
	}
	switch d := it.Decl.(type) {
	case *types.StructDecl:
		private := false
		for _, f := range d.Fields {
			private = private || !f.Exported
		}
		m.options = private && defaults[e.mod][e.id]
		m.names = qx.bodyNames(it)
	case *types.AliasDecl:
		if ty := qx.w.tab.Get(d.Type); ty.Kind == types.KErrorSet {
			m.set, m.members, m.names = true, qx.memberNames(ty.Errors), qx.headNames(it)
		}
	case *types.FuncDecl:
		m.members, m.names = qx.memberNames(d.Sig.Errors), qx.headNames(it)
	}
	return m
}

// openFact is an enum's or an alias's openness, the word or openSetDoc's
// line alike, so a tree of 1.2 and one with the word compare equal; the
// declaration fact is spelled without the word (declHead).
func openFact(m apiMeta) apiFact {
	v := "closed"
	if m.open {
		v = "open"
	}
	return apiFact{"open", v, Breaking}
}

// shown is how a change shows fact f of the item m describes: a
// declaration with the word open where the item is written open.
func (m apiMeta) shown(f apiFact) string {
	if f.what == "declaration" && m.decl != "" && f.value != "" {
		return m.decl
	}
	return f.value
}

// declOpen reports whether a declaration is written open.
func declOpen(d types.Decl) bool {
	switch d := d.(type) {
	case *types.EnumDecl:
		return d.Open
	case *types.AliasDecl:
		return d.Open
	}
	return false
}

// headNames are the dotted names of an item's source up to its body: the
// sets a declaration names, which its flattened members do not show.
func (qx *queryIndex) headNames(it *types.Item) []string {
	f := qx.w.sm.File(it.Span.File)
	if f == nil || int(it.Span.Start) > len(f.Text) {
		return nil
	}
	// The item's line from its name on, and the indented lines that
	// continue it (a long signature, its clauses), up to a body's brace.
	lines := strings.Split(f.Text[it.Span.Start:], "\n")
	head := lines[0]
	for _, l := range lines[1:] {
		if strings.Contains(head, "{") || l == "" || l[0] != ' ' && l[0] != '\t' {
			break
		}
		head += " " + l
	}
	if i := strings.IndexByte(head, '{'); i >= 0 {
		head = head[:i]
	}
	var out []string
	word := func(r rune) bool {
		return r == '_' || r == '.' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9'
	}
	for _, w := range strings.FieldsFunc(head, func(r rune) bool { return !word(r) }) {
		if w[0] >= 'A' && w[0] <= 'Z' || strings.Contains(w, ".") {
			out = append(out, w)
		}
	}
	return out
}

// bodyNames are the dotted names of a struct's declaration up to its
// closing brace: the types its fields name, so a field whose type spells
// an open set out grows with it (std.http.Route's handler field and
// ProxyRefused, for example).
func (qx *queryIndex) bodyNames(it *types.Item) []string {
	f := qx.w.sm.File(it.Span.File)
	if f == nil || int(it.Span.Start) > len(f.Text) {
		return nil
	}
	text := f.Text[it.Span.Start:]
	if i := strings.Index(text, "\n}"); i >= 0 {
		text = text[:i]
	}
	var out []string
	word := func(r rune) bool {
		return r == '_' || r == '.' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9'
	}
	for _, w := range strings.FieldsFunc(text, func(r rune) bool { return !word(r) }) {
		if w[0] >= 'A' && w[0] <= 'Z' || strings.Contains(w, ".") {
			out = append(out, w)
		}
	}
	return out
}

// resolveRefs resolves each meta's names to keys of the same release: X in
// module m is m.X, p.X is the X of the module whose last segment is p.
func resolveRefs(meta map[string]apiMeta) {
	byLast := map[string][]string{}
	for k, m := range meta {
		name := strings.TrimPrefix(k, m.mod+".")
		byLast[lastSeg(m.mod)+"."+name] = append(byLast[lastSeg(m.mod)+"."+name], k)
	}
	for k, m := range meta {
		if len(m.names) == 0 {
			continue
		}
		for _, n := range m.names {
			if !strings.Contains(n, ".") {
				if _, ok := meta[m.mod+"."+n]; ok {
					m.refs = append(m.refs, m.mod+"."+n)
				}
				continue
			}
			if ks := byLast[n]; len(ks) == 1 {
				m.refs = append(m.refs, ks[0])
			}
		}
		meta[k] = m
	}
}

// memberNames spells a set's error items as qx.errors does, "Name  module",
// sorted; a generic error parameter is left out.
func (qx *queryIndex) memberNames(set types.ErrorSetID) []string {
	var out []string
	for _, em := range qx.w.tab.Members(set) {
		if !em.IsParam {
			it := qx.w.items.Get(em.Item)
			out = append(out, qx.str(it.Name)+"  "+qx.w.items.Module(it.Module).Path)
		}
	}
	slices.Sort(out)
	return out
}

// openSet is one set the older release declared open, with its members in
// each release.
type openSet struct{ old, nw []string }

// openSets are, for each key of a, the open error sets (a's open sets that
// b still has) that its declaration names, directly or through the sets
// it names: a set that lists the same members by hand does not grow with
// an open one, so it gets none.
func openSets(a, b map[string]apiMeta) map[string][]openSet {
	open := map[string]openSet{}
	for k, ma := range a {
		if mb, ok := b[k]; ok && ma.open && ma.set {
			open[k] = openSet{ma.members, mb.members}
		}
	}
	out := map[string][]openSet{}
	for k := range a {
		seen := map[string]bool{}
		var walk func(k string)
		walk = func(k string) {
			for _, r := range a[k].refs {
				if seen[r] {
					continue
				}
				seen[r] = true
				if o, ok := open[r]; ok {
					out[k] = append(out[k], o)
				}
				if a[r].set {
					walk(r)
				}
			}
		}
		walk(k)
	}
	return out
}

// closedGrowth reports, for each key of a, whether its declaration names,
// directly or through the sets it names, a set that is not open and whose
// members changed other than by growing with an open set it names. Such a
// signature is not an open-set addition even where its text is: `e: Error,
// c: Closed` flattens both sets alike.
func closedGrowth(a, b map[string]apiMeta, opens map[string][]openSet) map[string]bool {
	out := map[string]bool{}
	for k := range a {
		seen := map[string]bool{}
		var walk func(k string)
		walk = func(k string) {
			for _, r := range a[k].refs {
				ma := a[r]
				if seen[r] || !ma.set {
					continue
				}
				seen[r] = true
				mb, ok := b[r]
				if !ma.open && (!ok || !slices.Equal(ma.members, mb.members) && !grewOpen(ma.members, mb.members, opens[r])) {
					out[k] = true
				}
				walk(r)
			}
		}
		walk(k)
	}
	return out
}

// grewOpen reports whether the error members nw keep every one of old and
// each member they add was added to an open set that old held whole: a
// signature or a set that names an open set grows with it, and only then.
func grewOpen(old, nw []string, opens []openSet) bool {
	if !superset(nw, old) {
		return false
	}
	for _, x := range nw {
		if slices.Contains(old, x) {
			continue
		}
		ok := false
		for _, o := range opens {
			if slices.Contains(o.nw, x) && !slices.Contains(o.old, x) && subset(o.old, old) {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	return true
}

// relaxed is the severity and what of a changed fact f, whose new value is
// nw, under the rules of this file; f.sev and f.what when none applies.
// closed is closedGrowth's answer for the item.
func relaxed(f apiFact, nw string, a, b apiMeta, opens []openSet, closed bool) (Severity, string) {
	switch {
	case experimental(a.mod):
		if f.sev == Breaking || f.sev == Recompile {
			return Reported, f.what
		}
	case f.what == "open" && f.value == "open":
		// The older release promised growth; a closed set breaks no
		// match, and only the members it may still add are no longer
		// additions.
		return Reported, "open removed"
	case f.what == "open":
		// Every exhaustive match outside the package now needs `_`.
		return Breaking, "open added"
	case a.set && f.what == "declaration":
		// A set is open when it holds every member of an open alias, so
		// growing one opens no set: every set that holds the grown members
		// held the older ones, and was open.
		if a.open && b.set && superset(b.members, a.members) {
			return Compatible, "members added"
		}
		if b.set && grewOpen(a.members, b.members, opens) {
			return Compatible, "members added to an open set it names"
		}
	case a.open && f.what == "fields" && supersetLines(nw, f.value):
		return Compatible, "members added"
	case a.options && f.what == "fields" && supersetLines(nw, f.value):
		return Compatible, "fields added"
	case f.what == "fields" && !closed && signatureGrewOpen(f.value, nw, a, b, opens):
		return Compatible, f.what + ": errors added to an open set"
	case f.what == "errors" && grewOpen(a.members, b.members, opens):
		return Compatible, f.what + ": errors added to an open set"
	case f.what == "signature" && !closed && signatureGrewOpen(f.value, nw, a, b, opens):
		return Compatible, f.what + ": errors added to an open set"
	}
	return f.sev, f.what
}

// signatureGrewOpen reports whether a function's signature changed only by
// the members added to the open sets it names, wherever it spells them: its
// result, a parameter, a callback's parameter or result (such an addition is
// compatible in any position). Its own result errors must have grown that
// way too, or not at all. A set spelled by hand in the signature (`e:
// NotFound | Denied`) that gains the same member is read as the open set's
// growth: the flattened text cannot tell them apart.
func signatureGrewOpen(old, nw string, a, b apiMeta, opens []openSet) bool {
	if !slices.Equal(a.members, b.members) && !grewOpen(a.members, b.members, opens) {
		return false
	}
	var added []string
	for _, o := range opens {
		for _, x := range o.nw {
			if !slices.Contains(o.old, x) && !slices.Contains(added, x) {
				added = append(added, x)
			}
		}
	}
	return len(added) > 0 && stripAdded(nw, added) == old
}

// stripAdded is a signature with every occurrence of the error members
// added taken out of the error sets it spells (`A|B!T`, `e: A|B`,
// `fn(A|B)`), each member a bare name or a qualified one.
func stripAdded(s string, added []string) string {
	for _, k := range added {
		name, _, _ := strings.Cut(k, "  ")
		q := `(?:\w+\.)*` + regexp.QuoteMeta(name)
		s = regexp.MustCompile(`\|`+q+`($|[^\w.])`).ReplaceAllString(s, "${1}")
		s = regexp.MustCompile(`(^|[^\w.|])`+q+`\|`).ReplaceAllString(s, "${1}")
	}
	return s
}

// subset reports whether every element of sub is in xs.
func subset(sub, xs []string) bool {
	for _, x := range sub {
		if !slices.Contains(xs, x) {
			return false
		}
	}
	return true
}

// superset reports whether every element of sub is in xs, and xs has more.
func superset(xs, sub []string) bool {
	if len(xs) <= len(sub) {
		return false
	}
	for _, x := range sub {
		if !slices.Contains(xs, x) {
			return false
		}
	}
	return true
}

// supersetLines is superset over two "; "-joined facts.
func supersetLines(nw, old string) bool {
	split := func(s string) []string {
		if s == "" {
			return nil
		}
		return strings.Split(s, "; ")
	}
	return superset(split(nw), split(old))
}

// APIDiffStd compares the exported items of std's public modules in two
// std trees, stdA and stdB (`mk apidiff --std A B`). cfg gives the rest of
// each session's configuration; its Root is replaced by an empty file.
func APIDiffStd(cfg Config, stdA, stdB string) (*APIDiffResult, error) {
	dir, err := os.MkdirTemp("", "mk-apidiff-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	root := filepath.Join(dir, "std_api.mk")
	if err := os.WriteFile(root, []byte("// The root of mk apidiff --std: std's facts are compared.\n"), 0o644); err != nil {
		return nil, err
	}
	a, b := cfg, cfg
	a.Root, b.Root = root, root
	a.StdDir, b.StdDir = stdA, stdB
	a.CacheDir, b.CacheDir = filepath.Join(dir, "a"), filepath.Join(dir, "b")
	return apiDiff(a, b, true)
}

// openTwin reports whether m, an item the older release lacks, is an error
// set alias written open that opens a set of the older release. A set is open when
// it holds every member of an open alias, so m opens every set that holds
// its members. When each of them is an exported item of the older release
// (fa), a set of exactly them could be spelled and matched there, by the
// package or by a dependent, and is the same type. It was already open
// there, with every set that holds it, when an exported open alias of the
// older release (ma) has only members among m's (as a subset). An open
// alias that adds a member of its own opens no older set.
func openTwin(m apiMeta, fa map[string][]apiFact, ma map[string]apiMeta) bool {
	if !m.open || !m.set || len(m.members) == 0 {
		return false
	}
	for _, mem := range m.members {
		name, mod, _ := strings.Cut(mem, "  ")
		if _, ok := fa[mod+"."+name]; !ok {
			return false
		}
	}
	for _, o := range ma {
		if o.open && o.set && len(o.members) > 0 && subset(o.members, m.members) {
			return false
		}
	}
	return true
}
