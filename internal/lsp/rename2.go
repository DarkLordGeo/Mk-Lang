// Why: an exported name is used by other packages; renaming it in one package
// alone breaks them.
//
// What: the cross-package part of rename: dependants (the packages whose pkg.mk
// names this one as a path dependency) and their edits.
//
// How: dependants are found among the open roots and under the workspace
// folders, or the package's parent directory. The rename is refused while a
// dependant is not open, so no package the user cannot see changes. Edits
// are lexical, through the import bindings.

package lsp

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// maxDependantScan bounds the directories a rename visits when it looks
// for dependant packages, so a huge workspace folder costs a bounded walk.
const maxDependantScan = 20000

// pathDepRoot is the root that declares a definition outside r, when that
// root is one of r's path dependencies (not vendored).
func (s *Server) pathDepRoot(r *root, uri string) (*root, bool) {
	path, ok := uriPath(uri)
	if !ok || r.file != "" {
		return nil, false
	}
	key := rootKeyOf(path)
	dir, ok := strings.CutPrefix(key, "pkg:")
	if !ok {
		return nil, false
	}
	for name, rel := range s.manifestDeps(r) {
		if rel == "" || vendors(r.dir, name) {
			continue
		}
		if pathKey(filepath.Join(r.dir, filepath.FromSlash(rel))) == pathKey(dir) {
			return s.rootFor(key), true
		}
	}
	return nil, false
}

// vendors reports whether the package in dir vendors the dependency name,
// which then resolves to vendor/<name>, not to its path.
func vendors(dir, name string) bool {
	fi, err := os.Stat(filepath.Join(dir, "vendor", name))
	return err == nil && fi.IsDir()
}

// dependant is a package that imports the declaring package through a path dependency.
type dependant struct {
	dir  string
	name string // the dependency's name in the dependant's pkg.mk
}

// dependants lists the packages that name owner's directory as a path
// dependency, sorted by directory.
func (s *Server) dependants(owner *root) []dependant {
	cands := map[string]string{} // path key to directory
	for _, d := range s.docs.all() {
		if dir, ok := strings.CutPrefix(rootKeyOf(d.path), "pkg:"); ok {
			cands[pathKey(dir)] = dir
		}
	}
	s.mu.Lock()
	folders := append([]string(nil), s.folders...)
	s.mu.Unlock()
	held := false
	for _, f := range folders {
		if within(owner.dir, f) {
			held = true
		}
	}
	if !held {
		folders = append(folders, filepath.Dir(owner.dir))
	}
	budget := maxDependantScan
	for _, f := range folders {
		packagesUnder(f, &budget, func(dir string) { cands[pathKey(dir)] = dir })
	}
	var out []dependant
	for k, dir := range cands {
		if k == pathKey(owner.dir) {
			continue
		}
		for name, rel := range s.manifestDeps(&root{dir: dir}) {
			if rel != "" && !vendors(dir, name) && pathKey(filepath.Join(dir, filepath.FromSlash(rel))) == pathKey(owner.dir) {
				out = append(out, dependant{dir: dir, name: name})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].dir < out[j].dir || out[i].dir == out[j].dir && out[i].name < out[j].name
	})
	return out
}

// within reports whether path is dir or below it.
func within(path, dir string) bool {
	p, d := pathKey(path), pathKey(dir)
	return p == d || strings.HasPrefix(p, strings.TrimSuffix(d, string(filepath.Separator))+string(filepath.Separator))
}

// packagesUnder calls found for each directory under dir holding pkg.mk,
// skipping dot directories, gen/ views and vendor trees, and visiting at
// most *budget directories.
func packagesUnder(dir string, budget *int, found func(string)) {
	_ = filepath.WalkDir(dir, func(p string, e fs.DirEntry, err error) error {
		if err != nil || !e.IsDir() {
			return nil
		}
		if *budget <= 0 {
			return filepath.SkipAll
		}
		*budget--
		name := e.Name()
		if p != dir && (strings.HasPrefix(name, ".") || name == "gen" || name == "vendor" || name == "node_modules") {
			return filepath.SkipDir
		}
		if fi, err := os.Stat(filepath.Join(p, "pkg.mk")); err == nil && !fi.IsDir() {
			found(p)
		}
		return nil
	})
}

// isOpenRoot reports whether one of the root's files is an open document.
func (s *Server) isOpenRoot(dir string) bool {
	k := foldKey("pkg:" + dir)
	for _, d := range s.docs.all() {
		if foldKey(rootKeyOf(d.path)) == k {
			return true
		}
	}
	return false
}

// targetDecl is the declaration at a definition, with its parent type
// (nil for a top-level item).
func (s *Server) targetDecl(def Location) (decl, *decl, bool) {
	path, ok := uriPath(def.URI)
	if !ok {
		return decl{}, nil, false
	}
	f, ok := s.fileOf(path)
	if !ok {
		return decl{}, nil, false
	}
	off := f.text.off(def.Range.Start, s.utf8.Load())
	for i := range f.decls {
		d := &f.decls[i]
		if d.NameStart == off {
			return *d, nil, true
		}
		for _, ch := range d.Children {
			if ch.NameStart == off {
				return ch, d, true
			}
		}
	}
	return decl{}, nil, false
}

// renameDependants adds the dependants' uses of an exported name to the
// target, or refuses the rename.
func (s *Server) renameDependants(t *renameTarget, def Location) error {
	d, parent, ok := s.targetDecl(def)
	if !ok {
		return nil
	}
	t.member = parent != nil || d.Owner != ""
	exported := d.Exported
	if parent != nil && (d.Kind == symEnumMember || parent.Detail == "interface" || parent.Detail == "impl") {
		exported = parent.Exported || parent.Detail == "impl"
	}
	if !exported {
		return nil
	}
	defPath, _ := uriPath(def.URI)
	defDir := pathKey(filepath.Dir(defPath))
	name := t.c.name
	for _, dep := range s.dependants(t.owner) {
		if !s.isOpenRoot(dep.dir) {
			return errors.New("cannot rename " + name + ": the package " + dep.dir + " depends on " + filepath.Base(t.owner.dir) +
				" and is not open; open one of its files first")
		}
		dr := s.rootFor("pkg:" + dep.dir)
		// The module a binding imports, as a directory key, when the
		// import names the dependency (`util.src`).
		bound := func(f srcFile, ct []token, q string) (string, bool) {
			mod, ok := imports(f.text.s, ct)[q]
			if !ok {
				return "", false
			}
			first, rest, _ := strings.Cut(mod, ".")
			if first != dep.name {
				return "", true
			}
			dir := t.owner.dir
			if rest != "" {
				dir = joinModule(dir, rest)
			}
			return pathKey(dir), true
		}
		qualifier := func(f srcFile, ct []token, k int) (string, bool) {
			if k >= 2 && word(f.text.s, ct, k-1) == "." && isIdent(ct, k-2) && ct[k-2].End == ct[k-1].Start {
				return word(f.text.s, ct, k-2), true
			}
			return "", false
		}
		var uses []Location
		if !t.member {
			uses = s.nameTokens(dr, name, func(f srcFile, ct []token, k int) bool {
				q, ok := qualifier(f, ct, k)
				if !ok {
					return false
				}
				dir, ok := bound(f, ct, q)
				return ok && dir == defDir
			})
		} else {
			if n := len(s.search(dr, func(x decl) bool { return x.Name == name })); n > 0 {
				return errors.New("cannot rename " + name + ": the dependant package " + filepath.Base(dep.dir) + " declares " + name + " too, and references are not resolved yet")
			}
			if err := s.noLocalSpells(dr, name); err != nil {
				return err
			}
			uses = s.nameTokens(dr, name, func(f srcFile, ct []token, k int) bool {
				q, ok := qualifier(f, ct, k)
				if !ok {
					return true
				}
				_, isBinding := bound(f, ct, q)
				return !isBinding
			})
		}
		if len(uses) > 0 {
			t.deps = append(t.deps, dr)
			t.refs = append(t.refs, uses...)
		}
	}
	return nil
}

// folderURIs are the URIs of initialize's workspace folders.
func folderURIs(p initializeParams) []string {
	var out []string
	for _, f := range p.WorkspaceFolders {
		out = append(out, f.URI)
	}
	return out
}
