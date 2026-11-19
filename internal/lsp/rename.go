// Why: a rename that changes what the code means is worse than none.
//
// What: prepareRename and rename, with the checks that refuse a rename.
//
// How: an item of a package that checks is renamed at its resolved uses
// (exact.go). Otherwise the checks are lexical and conservative: a rename
// goes ahead only when every token spelling the old name in its reach means
// the one declaration, and the new name is free there. An exported name is
// also renamed in the dependants (rename2.go).

package lsp

import (
	"errors"
	"path/filepath"
	"strings"
)

type renameParams struct {
	textDocumentPositionParams
	NewName string `json:"newName"`
}

// universeNames may not be declared (E0102): renaming to one
// would change meaning.
var universeNames = setOf("true false some none fields Assert Panic Cast Trunc SizeOf AlignOf Target Plain Send Share Fields EvalArena " +
	"int uint i8 i16 i32 i64 u8 u16 u32 u64 f32 f64 bool char str void never Self self result")

// renameTarget is what a rename at a position would change.
type renameTarget struct {
	c     cursor
	local bool       // a local of one item
	refs  []Location // every token to rewrite, the declaration included
	// owner is the root that declares the name; deps are the dependant
	// roots whose uses refs includes (rename2.go).
	owner  *root
	deps   []*root
	member bool // a field, variant or method, not a top-level item
}

// renameAt finds the target at a position, or the reason it cannot be
// renamed.
func (s *Server) renameAt(p textDocumentPositionParams) (renameTarget, error) {
	c, ok, e := s.cursorAt(p.TextDocument.URI, p.Position)
	if e != nil {
		return renameTarget{}, errors.New(e.Message)
	}
	if !ok || keywords[c.name] || universeNames[c.name] {
		return renameTarget{}, errors.New("no name to rename here")
	}
	r := s.rootOfPath(c.f.path)
	// An item of the root whose uses the last full check resolves
	// (exact.go): those uses, whatever else spells the name.
	if locs, ok := s.exactRename(c); ok && s.inRoot(r, locs[0].URI) {
		t := renameTarget{c: c, owner: r, refs: locs}
		if err := s.renameDependants(&t, locs[0]); err != nil {
			return renameTarget{}, err
		}
		return t, nil
	}
	defs := s.defs(c)
	if len(defs) != 1 {
		if len(defs) == 0 {
			return renameTarget{}, errors.New("cannot rename " + c.name + ": no declaration found in the package")
		}
		return renameTarget{}, errors.New("cannot rename " + c.name + ": " + itoa(len(defs)) + " declarations may be meant")
	}
	owner := r
	if !s.inRoot(r, defs[0].URI) {
		o, ok := s.pathDepRoot(r, defs[0].URI)
		if !ok {
			return renameTarget{}, errors.New("cannot rename " + c.name + ": it is declared outside the package (std, or a fetched or vendored dependency)")
		}
		owner = o
	}
	t := renameTarget{c: c, owner: owner}
	text := c.f.text.s
	a, b := itemOf(text, c.ct, c.i)
	if c.qual == "" && owner == r {
		if _, ok := localDecl(text, c.ct, a, b, c.ct[c.i].End, c.name); ok && !notLocalUse(text, c.ct, a, b, c.i) {
			// The local's tokens by scope (references): never a
			// field `p.x` or a label `x:` that spells the same name, nor a
			// sibling scope's local. rename refuses a new name the item
			// already spells.
			var rp referenceParams
			rp.textDocumentPositionParams = p
			rp.Context.IncludeDeclaration = true
			res, re := s.references(rp)
			if re != nil {
				return renameTarget{}, errors.New(re.Message)
			}
			t.refs = res.([]Location)
			t.local = true
			return t, nil
		}
	}
	// An item, method, field or variant: the name must mean one
	// declaration across the package, and no local may spell it.
	n := len(s.search(owner, func(d decl) bool { return d.Name == c.name }))
	if n > 1 {
		return renameTarget{}, errors.New("cannot rename " + c.name + ": " + itoa(n) + " declarations in the package share the name, and references are not resolved yet")
	}
	if err := s.noLocalSpells(owner, c.name); err != nil {
		return renameTarget{}, err
	}
	t.refs = s.nameTokens(owner, c.name, nil)
	if err := s.renameDependants(&t, defs[0]); err != nil {
		return renameTarget{}, err
	}
	return t, nil
}

// noLocalSpells refuses a rename of an item, method, field or variant
// when a local of the root spells the same name: a lexical rename cannot
// tell the two apart.
func (s *Server) noLocalSpells(r *root, name string) error {
	for _, path := range s.rootFiles(r) {
		f, ok := s.fileOf(path)
		if !ok {
			continue
		}
		ct := code(f.toks)
		for _, ir := range items(f.text.s, ct) {
			for k := ir[0]; k <= ir[1]; k++ {
				if word(f.text.s, ct, k) != name {
					continue
				}
				if at, ok := localDecl(f.text.s, ct, ir[0], ir[1], ct[k].End, name); ok && at == ct[k].Start {
					return errors.New("cannot rename " + name + ": a local in " + filepath.Base(path) + " spells the same name")
				}
			}
		}
	}
	return nil
}

// nameTokens are the identifier tokens of the root's files spelling the
// name, those keep accepts (nil: all).
func (s *Server) nameTokens(r *root, name string, keep func(f srcFile, ct []token, k int) bool) []Location {
	u8 := s.utf8.Load()
	var out []Location
	for _, path := range s.rootFiles(r) {
		f, ok := s.fileOf(path)
		if !ok {
			continue
		}
		ct := code(f.toks)
		uri := s.uriOf(f.path)
		for k, t := range ct {
			if t.Kind != tIdent || t.End-t.Start != len(name) || f.text.s[t.Start:t.End] != name {
				continue
			}
			if keep == nil || keep(f, ct, k) {
				out = append(out, Location{URI: uri, Range: f.text.span(t.Start, t.End, u8)})
			}
		}
	}
	return out
}

// usesName reports which file of the roots spells the name, or "".
func (s *Server) usesName(roots []*root, name string) string {
	for _, r := range roots {
		for _, path := range s.rootFiles(r) {
			if f, ok := s.fileOf(path); ok {
				for _, tk := range f.toks {
					if tk.Kind == tIdent && f.text.s[tk.Start:tk.End] == name {
						return path
					}
				}
			}
		}
	}
	return ""
}

// itemName names an item for a message: its declared name, else its
// first word.
func itemName(text string, ct []token, a int) string {
	for _, r := range items(text, ct) {
		if r[0] == a {
			if d, ok := itemDecl(text, ct, r[0], r[1]); ok {
				return d.Name
			}
		}
	}
	return word(text, ct, a)
}

// inRoot reports whether a URI names a file of the root.
func (s *Server) inRoot(r *root, uri string) bool {
	path, ok := uriPath(uri)
	if !ok {
		return false
	}
	for _, p := range s.rootFiles(r) {
		if pathKey(p) == pathKey(path) {
			return true
		}
	}
	return false
}

// prepareRename answers the range of the name and a placeholder, or an
// error saying why the name cannot be renamed.
func (s *Server) prepareRename(p textDocumentPositionParams) (any, *rpcError) {
	t, err := s.renameAt(p)
	if err != nil {
		return nil, &rpcError{Code: codeRequestFailed, Message: err.Error()}
	}
	tok := t.c.ct[t.c.i]
	return map[string]any{"range": t.c.f.text.span(tok.Start, tok.End, s.utf8.Load()), "placeholder": t.c.name}, nil
}

// rename rewrites every reference, after checking the new name is a name,
// not a keyword or universe name, and free where the old one reaches.
func (s *Server) rename(p renameParams) (any, *rpcError) {
	fail := func(msg string) (any, *rpcError) { return nil, &rpcError{Code: codeRequestFailed, Message: msg} }
	nn := p.NewName
	if nn == "" || !isIdentStart(nn[0]) || strings.IndexFunc(nn, func(r rune) bool { return r > 127 || !isIdentByte(byte(r)) }) >= 0 {
		return fail("cannot rename to " + nn + ": not a name")
	}
	if keywords[nn] || flowWords[nn] || universeNames[nn] {
		return fail("cannot rename to " + nn + ": it is a keyword or a universe name")
	}
	t, err := s.renameAt(p.textDocumentPositionParams)
	if err != nil {
		return fail(err.Error())
	}
	if nn == t.c.name {
		return workspaceEdit{Changes: map[string][]textEdit{}}, nil
	}
	text := t.c.f.text.s
	if t.local {
		a, b := itemOf(text, t.c.ct, t.c.i)
		for k := a; k <= b; k++ {
			if word(text, t.c.ct, k) == nn {
				return fail("cannot rename " + t.c.name + " to " + nn + ": " + nn + " is already used in " + itemName(text, t.c.ct, a))
			}
		}
	} else {
		// A top-level item reaches a dependant only through its import
		// binding (`u.Two`), so only a member's dependants must not spell
		// the new name.
		roots := []*root{t.owner}
		if t.member {
			roots = append(roots, t.deps...)
		}
		if path := s.usesName(roots, nn); path != "" {
			return fail("cannot rename " + t.c.name + " to " + nn + ": " + filepath.Base(path) + " already uses " + nn)
		}
	}
	we := workspaceEdit{Changes: map[string][]textEdit{}}
	for _, l := range t.refs {
		we.Changes[l.URI] = append(we.Changes[l.URI], textEdit{Range: l.Range, NewText: nn})
	}
	return we, nil
}
