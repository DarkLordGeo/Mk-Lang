// Why: the checker knows what every name resolves to and every expression's
// type; asking it again for each request would cost a check per keystroke.
//
// What: exact (a root's last check that reached the types phase, with its
// session), query, typeAt, the exact references and rename, and the text
// mapping textDiff and toChecked.
//
// How: the answers are read from the kept session (driver QueryChecked,
// TypeAt, Lookup, Items, UsesOf); the lexical answers stay as the fallback
// before the first such check, for a file the check did not read, and at a
// position the edits since that check changed. Config.KeepSyntax, which
// TypeAt and UsesOf need, costs a warm check its module-cache hits, so only
// a root's first check keeps the syntax; references and rename ask a full
// session over the current texts. An editor offset maps to the checked text
// through the longest common prefix and suffix of the two texts: the prefix
// is the same, the suffix moves by the length difference, and the changed
// region between has no checked counterpart.

package lsp

import (
	"strings"

	"mk/internal/driver"
	"mk/internal/source"
)

// exact is a root's last check that reached the types phase.
type exact struct {
	sess *driver.Session
	sm   *source.SourceMap
	// byKey are the session's files by the path key of their OS path.
	byKey map[string]*source.File
}

// keepExact records a check that reached the types phase as the root's
// exact answers; full when it kept every package module's syntax.
func (s *Server) keepExact(r *root, sess *driver.Session, sm *source.SourceMap, full bool) {
	if sess == nil || sm == nil {
		return
	}
	x := &exact{sess: sess, sm: sm, byKey: map[string]*source.File{}}
	for _, f := range sm.Files() {
		if p := s.absPath(r, f.Path); p != "" {
			x.byKey[pathKey(p)] = f
		}
	}
	r.exact = x
	if full {
		r.full = x
	}
}

// current reports whether every file of the root reads as x checked it:
// the open buffers and the files on disk, none added or gone.
func (s *Server) current(r *root, x *exact) bool {
	if x == nil {
		return false
	}
	for _, p := range s.rootFiles(r) {
		sf := x.fileOfKey(p)
		if sf == nil && s.isStdRoot(r) {
			continue // a file the browse check does not load (stdroot.go)
		}
		f, ok := s.fileOf(p)
		if sf == nil || !ok || sf.Text != f.text.s {
			return false
		}
	}
	return true
}

// fullExact is a session of the root that kept every package module's
// syntax and read the root's files as they are now: the last full one when
// nothing changed since, else a check of its own now (the cost of one
// check; published nowhere). Nil when the root does not check to types,
// so the caller answers lexically.
//
// A check of the root still queued runs now in its place (it
// publishes, and keeps the syntax since navSince is set); the texts of an
// on-demand check that did not reach types are remembered, so a red root
// costs one check per edit, not one per request; and navSince makes the
// root's next check keep the syntax, so the request after the next edit
// reads that check's session instead of checking again.
func (s *Server) fullExact(r *root) *exact {
	r.navSince = true
	if s.current(r, r.full) {
		return r.full
	}
	if r.fullFailed != nil && s.sameTexts(r, r.fullFailed) {
		return nil
	}
	if s.takePending(r.key) {
		s.runCheck(r.key)
		r.navSince = true
		if s.current(r, r.full) {
			return r.full
		}
		if s.sameTexts(r, r.fullFailed) {
			return nil
		}
	}
	sess, o, err := s.sessionWith(r, false, true)
	if err != nil {
		return nil
	}
	res := sess.Check()
	s.checks.Add(1)
	if !reachedTypes(res) {
		r.fullFailed = s.rootTexts(r)
		return nil
	}
	for _, st := range o.served {
		s.docs.setGood(st.path, st.raw)
	}
	s.keepExact(r, sess, res.Sources, true)
	return r.full
}

// rootTexts are the root's files' current texts by path key.
func (s *Server) rootTexts(r *root) map[string]string {
	out := map[string]string{}
	for _, p := range s.rootFiles(r) {
		if f, ok := s.fileOf(p); ok {
			out[pathKey(p)] = f.text.s
		}
	}
	return out
}

// sameTexts reports whether the root's files read as texts (rootTexts)
// still; false for nil.
func (s *Server) sameTexts(r *root, texts map[string]string) bool {
	if texts == nil {
		return false
	}
	n := 0
	for _, p := range s.rootFiles(r) {
		f, ok := s.fileOf(p)
		t, had := texts[pathKey(p)]
		if !ok || !had || t != f.text.s {
			return false
		}
		n++
	}
	return n == len(texts)
}

// query answers `mk query q args` for the root: from the last check that
// reached the types phase when there is one, else from a session of its
// own (which checks first).
func (s *Server) query(r *root, q string, args []string) (*driver.QueryAnswer, error) {
	if r.exact != nil {
		if a, err := r.exact.sess.QueryChecked(q, args); err == nil && a != nil {
			return a, nil
		}
	}
	sess, err := s.session(r)
	if err != nil {
		return nil, err
	}
	a, _, err := sess.Query(q, args)
	return a, err
}

// fileOfKey is the checked file of an OS path, or nil.
func (x *exact) fileOfKey(path string) *source.File {
	if x == nil {
		return nil
	}
	return x.byKey[pathKey(path)]
}

// textDiff is the common prefix and suffix lengths of the checked text old
// and the editor's text cur, the suffix never overlapping the prefix.
func textDiff(old, cur string) (pre, suf int) {
	n := min(len(old), len(cur))
	for pre < n && old[pre] == cur[pre] {
		pre++
	}
	for suf < n-pre && old[len(old)-1-suf] == cur[len(cur)-1-suf] {
		suf++
	}
	return pre, suf
}

// toChecked maps an offset of the editor's text cur to the checked text
// old; false inside the region the edits changed.
func toChecked(old, cur string, off int) (int, bool) {
	if old == cur {
		return off, off <= len(old)
	}
	pre, suf := textDiff(old, cur)
	switch {
	case off <= pre:
		return off, true
	case off >= len(cur)-suf:
		return off - (len(cur) - len(old)), true
	}
	return 0, false
}

// fromChecked maps an offset of the checked text old to the editor's text
// cur; false inside the region the edits changed.
func fromChecked(old, cur string, off int) (int, bool) {
	return toChecked(cur, old, off)
}

// typeAt is the checker's type of the expression or pattern of f that
// ends at off (the editor's text), when the root's last good check read f
// and the edits since left off's text alone.
//
// The last check answers first; a module it loaded whole from the module
// cache has no syntax there, so the last full check answers next.
func (s *Server) typeAt(f srcFile, off int) (string, bool) {
	r := s.rootOfPath(f.path)
	for _, x := range []*exact{r.exact, r.full} {
		sf := x.fileOfKey(f.path)
		if sf == nil {
			continue
		}
		o, ok := toChecked(sf.Text, f.text.s, off)
		if !ok {
			continue
		}
		if t, ok := x.sess.TypeAt(sf.Path, uint32(o)); ok && t != "" {
			return t, true
		}
	}
	return "", false
}

// exactLoc is the checked span [p, p+n) of x (n bytes; or up to end when
// end is valid) as a Location of the editor's text; false when the file is
// not on disk or the edits since changed the span.
func (s *Server) exactLoc(r *root, x *exact, p, end source.Position, n int) (Location, bool) {
	sf, ok := x.sm.ByPath(p.Path)
	if !ok {
		return Location{}, false
	}
	off, ok := sf.Offset(p.LineCol)
	if !ok {
		return Location{}, false
	}
	stop := int(off) + n
	if end.IsValid() && end.Path == p.Path {
		if e, ok := sf.Offset(end.LineCol); ok && e >= off {
			stop = int(e)
		}
	}
	path := s.absPath(r, p.Path)
	if path == "" {
		return Location{}, false
	}
	f, ok := s.fileOf(path)
	if !ok {
		return Location{}, false
	}
	a, ok1 := fromChecked(sf.Text, f.text.s, int(off))
	b, ok2 := fromChecked(sf.Text, f.text.s, stop)
	if !ok1 || !ok2 || b < a {
		return Location{}, false
	}
	return Location{URI: s.uriOf(path), Range: f.text.span(a, b, s.utf8.Load())}, true
}

// lookupDef is the declaration the last check resolves subject to (driver
// Lookup: the package before std), as a Location; never a case-folded
// match, and in module mod when mod is not "".
func (s *Server) lookupDef(r *root, subject, mod string) (Location, bool) {
	x := r.exact
	if x == nil {
		return Location{}, false
	}
	it, ok := x.sess.LookupExact(subject)
	if !ok || !it.Def.IsValid() || it.Name != lastSegment(subject) || mod != "" && it.Module != mod {
		return Location{}, false
	}
	return s.exactLoc(r, x, it.Def, it.End, len(it.Name))
}

// exactRefs are the resolved uses of the item under the cursor (driver
// UsesOf), with its declaration when decl is set; false when no full
// session of the current texts resolves the cursor to an item (a local, a
// file outside the root, a root that does not check), so the caller
// answers lexically. The cursor names the item when it is one of the
// item's uses, or its declared name.
func (s *Server) exactRefs(c cursor, decl bool) ([]Location, bool) {
	locs, _, ok := s.exactUses(c, decl)
	return locs, ok
}

// exactUses is exactRefs with the item; the declaration, when decl is
// set, is the first location.
func (s *Server) exactUses(c cursor, decl bool) ([]Location, driver.ItemInfo, bool) {
	r := s.rootOfPath(c.f.path)
	inRoot := false
	for _, p := range s.rootFiles(r) {
		if pathKey(p) == pathKey(c.f.path) {
			inRoot = true
		}
	}
	if !inRoot {
		return nil, driver.ItemInfo{}, false
	}
	x := s.fullExact(r)
	sf := x.fileOfKey(c.f.path)
	if sf == nil {
		return nil, driver.ItemInfo{}, false
	}
	at := sf.Position(uint32(c.ct[c.i].Start)) // x is current: offsets agree
	var subjects []string
	for _, it := range x.sess.Items(sf.Path) {
		if it.Def == at && it.Name == c.name {
			subjects = []string{it.Name}
			if it.Owner != "" {
				subjects[0] = it.Owner + "." + it.Name
			}
			if strings.HasPrefix(it.Module, "std") {
				// std's own items: module-qualified, since another
				// std module may declare the name too.
				subjects = append([]string{it.Module + "." + subjects[0]}, subjects...)
			}
		}
	}
	if subjects == nil {
		subjects = s.hoverSubjects(r, c, hit{}, false)
	}
	for _, sub := range subjects {
		it, ok := x.sess.LookupExact(sub)
		if !ok || it.Name != c.name {
			continue
		}
		uses := x.sess.UsesOf(sub)
		mine := it.Def == at
		for _, u := range uses {
			mine = mine || u == at
		}
		if !mine {
			continue
		}
		out := []Location{}
		if decl && it.Def.IsValid() {
			l, ok := s.exactLoc(r, x, it.Def, it.End, len(it.Name))
			if !ok {
				return nil, driver.ItemInfo{}, false
			}
			out = append(out, l)
		}
		for _, u := range uses {
			if l, ok := s.exactLoc(r, x, u, source.Position{}, len(it.Name)); ok {
				out = append(out, l)
			}
		}
		return out, it, true
	}
	return nil, driver.ItemInfo{}, false
}

// renameKinds are the item kinds whose every use UsesOf resolves (resolve's
// side tables); a field's or a variant's labels and patterns are left to
// the lexical rules.
var renameKinds = setOf("struct enum error interface alias const global func")

// exactRename is the edit set of a rename of the item under the cursor
// from its resolved uses, the declaration first: a top-level item of the
// root, or a method that no interface or impl of the root or std names
// (UsesOf leaves out interface dispatch, so renaming such a method would
// split it from its interface). False leaves the rename to the lexical
// rules (rename.go).
func (s *Server) exactRename(c cursor) ([]Location, bool) {
	locs, it, ok := s.exactUses(c, true)
	if !ok || !renameKinds[it.Kind] || len(locs) == 0 || it.Def.Path == "" || strings.HasPrefix(it.Def.Path, "std/") {
		return nil, false
	}
	if it.Owner != "" {
		if it.Kind != "func" {
			return nil, false
		}
		names := func(d decl) bool {
			if d.Detail != "interface" && d.Detail != "impl" {
				return false
			}
			for _, ch := range d.Children {
				if ch.Name == c.name {
					return true
				}
			}
			return false
		}
		if len(s.search(s.rootOfPath(c.f.path), names)) > 0 || len(s.searchStd("", names)) > 0 {
			return nil, false
		}
	}
	return locs, true
}
