// Why: several packages developed together must build against the same
// dependency versions, with one pkg.sum and one set of grants.
//
// What: wsInfo, findWorkspace and ancestorWorkspace (finding the
// workspace), ancestorMembers and namesMember (reading members without
// evaluating), memberRequirements, reachableOnly, grantSet and
// WorkspaceMembers.
//
// How: mk finds the workspace by walking up from the package directory to
// the first manifest whose `members` lists it (the package itself when its
// own manifest has members). The workspace root's directory holds the one
// pkg.sum, identities are relative to it, and its manifest gives the build's
// replace entries and grants. The version choice runs over every member's
// source requirements, so each member builds against the same versions; a
// package only another member needs is chosen and recorded in pkg.sum, and
// not built. A member keeps its manifest name as its canonical id (members
// are the user's own packages; the names must be unique in the workspace,
// else id-clash). A member that is itself a workspace root is E0202
// nested-workspace.
//
// Members' requirements are their manifests' own source Deps (deps and
// test_deps); a requirement reached only through a member's path
// dependency outside the workspace is met when that member is built.

package driver

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"mk/internal/ast"
	"mk/internal/diag"
	"mk/internal/syntax"
)

// wsInfo is the workspace a package belongs to.
type wsInfo struct {
	dir string    // the root's directory
	man *Manifest // the root's manifest
	// members are the member directories, cleaned and absolute, in the
	// manifest's order, and mans their manifests (the root's own when it
	// is a member, nil for the package being built).
	members []string
	mans    []*Manifest
}

// isMember reports whether dir is a member directory of ws.
func (ws *wsInfo) isMember(dir string) bool {
	k := pathKey(dir)
	return slices.ContainsFunc(ws.members, func(m string) bool { return pathKey(m) == k })
}

// memberDirs is the member directories root's manifest lists, absolute.
func memberDirs(dir string, man *Manifest) []string {
	out := make([]string, 0, len(man.Members))
	for _, m := range man.Members {
		out = append(out, filepath.Join(dir, filepath.FromSlash(m)))
	}
	return out
}

// mayHaveMembers reports whether dir's pkg.mk can name members: a cheap
// look at its text before the manifest is evaluated.
func mayHaveMembers(dir string) bool {
	raw, err := os.ReadFile(filepath.Join(dir, "pkg.mk"))
	return err == nil && strings.Contains(string(raw), "members")
}

// ancestorMembers reads the `members` argument of dir's pkg.mk without
// evaluating it: has is false when the file does not parse or
// has no members argument, and lit is false when the argument is not an
// array of string literals (a computed list, which only evaluation reads).
func ancestorMembers(dir string) (members []string, has, lit bool) {
	e, err := openManifest(dir)
	if err != nil {
		return nil, false, false
	}
	al, err := e.list("members")
	if al == nil && err == nil {
		return nil, false, false
	}
	if err != nil {
		return nil, true, false
	}
	for _, x := range al.Elems {
		bl, ok := x.(*ast.BasicLit)
		if !ok || bl.Kind != ast.TokStr {
			return nil, true, false
		}
		v, ok := syntax.StringValue(bl.Kind, bl.Raw)
		if !ok {
			return nil, true, false
		}
		members = append(members, string(v))
	}
	return members, true, true
}

// namesMember reports whether an ancestor's literal members name pkgDir.
func namesMember(dir string, members []string, pkgDir string) bool {
	k := pathKey(pkgDir)
	for _, m := range members {
		if pathKey(filepath.Join(dir, filepath.FromSlash(m))) == k {
			return true
		}
	}
	return false
}

// findWorkspace is the workspace of the package being built, whose
// manifest is root, or nil when it belongs to none. Each member's manifest
// is loaded once (dw.loaded) for its requirements and the nested check.
func (dw *depWalk) findWorkspace(root *Manifest) *wsInfo {
	pkgDir := dw.rootDir
	var ws *wsInfo
	if len(root.Members) > 0 {
		ws = &wsInfo{dir: pkgDir, man: root, members: memberDirs(pkgDir, root)}
	} else {
		for dir := filepath.Dir(pkgDir); ; dir = filepath.Dir(dir) {
			c, stop := dw.ancestorWorkspace(dir, pkgDir)
			if stop {
				return nil
			}
			if c != nil {
				ws = c
				break
			}
			if filepath.Dir(dir) == dir {
				break
			}
		}
	}
	if ws == nil {
		return nil
	}
	for i, md := range ws.members {
		var m *Manifest
		switch {
		case pathKey(md) == pathKey(pkgDir):
			m = root
		case pathKey(md) == pathKey(ws.dir):
			m = ws.man
		default:
			lm, ds := dw.ms.load(dw.s.w.diskTree(md, dw.prefixAt(md)), "pkg.mk", "member")
			if len(ds) > 0 {
				dw.diags = append(dw.diags, ds...)
				return nil
			}
			m = lm
			dw.loaded[pathKey(md)] = m
		}
		ws.mans = append(ws.mans, m)
		if m != nil && len(m.Members) > 0 && pathKey(md) != pathKey(ws.dir) {
			f := "members[" + strconv.Itoa(i) + "]"
			dw.diags = append(dw.diags, depFail(ws.man, f, "nested-workspace",
				"member %s is itself a workspace root (its manifest lists members); a workspace holds no workspace", ws.man.Members[i]))
		}
	}
	if len(dw.diags) > 0 {
		return nil
	}
	for i, m := range ws.mans {
		if pathKey(ws.members[i]) == pathKey(ws.dir) {
			continue
		}
		if m == nil {
			m = root
		}
		for _, f := range []struct {
			name string
			set  bool
		}{{"unsafe_deps", len(m.UnsafeDeps) > 0}} { // replace has its own note (validateManifestExt)
			if f.set {
				// The workspace root's apply to every member.
				dw.note("note: member %s's %s is ignored; the workspace root's %s applies to every member", ws.man.Members[i], f.name, f.name)
			}
		}
	}
	return ws
}

// ancestorWorkspace is the workspace rooted at dir, an ancestor of
// pkgDir, when dir's manifest lists pkgDir as a member. An ancestor is
// loaded only when its members, read syntactically, name the package (or
// are computed): a manifest above an unrelated package, or one that only
// mentions members, never breaks its build. stop is true when
// the workspace's manifest fails to load (its diagnostics are dw's).
func (dw *depWalk) ancestorWorkspace(dir, pkgDir string) (ws *wsInfo, stop bool) {
	if !hasManifest(dir) || !mayHaveMembers(dir) {
		return nil, false
	}
	lm, has, lit := ancestorMembers(dir)
	if !has {
		return nil, false
	}
	if lit && !namesMember(dir, lm, pkgDir) {
		dw.note("note: %s lists workspace members and does not list this package, so it is not this package's workspace", filepath.Join(dir, "pkg.mk"))
		return nil, false
	}
	m, ds := dw.ms.load(dw.s.w.diskTree(dir, dw.prefixAt(dir)), "pkg.mk", "main")
	if len(ds) > 0 {
		dw.diags = append(dw.diags, ds...)
		return nil, true
	}
	if m == nil || len(m.Members) == 0 {
		return nil, false
	}
	c := &wsInfo{dir: dir, man: m, members: memberDirs(dir, m)}
	if !c.isMember(pkgDir) {
		return nil, false
	}
	dw.loaded[pathKey(dir)] = m
	return c, false
}

// prefixAt is the SourceMap prefix of a directory read for the workspace:
// its path relative to the package root, as prefixOf gives a path
// dependency's, so a member walked later as a path dependency has the
// same one.
func (dw *depWalk) prefixAt(dir string) string {
	rel, err := filepath.Rel(dw.rootDir, dir)
	if err != nil || filepath.IsAbs(rel) || strings.ContainsRune(rel, ':') {
		return filepath.ToSlash(filepath.Clean(dir)) + "/"
	}
	if rel == "." {
		return ""
	}
	return filepath.ToSlash(rel) + "/"
}

// memberRequirements adds every other member's source requirements to the
// version choice (after the root's replace), so every member chooses the
// same versions.
func (dw *depWalk) memberRequirements() {
	for i, m := range dw.ws.mans {
		if m == nil || pathKey(dw.ws.members[i]) == pathKey(dw.rootDir) {
			continue
		}
		// After the root's replace, as the version walk reads every
		// manifest: a path replacement stands for its manifest's
		// requirements.
		reqs, err := dw.replacedReqs(m, "")
		if err != nil {
			if len(dw.diags) == 0 {
				dw.diags = append(dw.diags, depFail(m, "deps", reasonOf(err), "%v", err))
			}
			return
		}
		for _, r := range reqs {
			dw.reqs = append(dw.reqs, requirement{Source: r.Source, Version: r.Version})
			dw.extraReqs = true
		}
	}
}

// reachableOnly drops from the build list the packages no local name of
// the package being built reaches: in a workspace, the versions only other
// members need are chosen and recorded, not built.
func (dw *depWalk) reachableOnly() {
	seen := map[*depPkg]bool{}
	var visit func(p *depPkg)
	visit = func(p *depPkg) {
		if p == nil || seen[p] {
			return
		}
		seen[p] = true
		for _, q := range p.local {
			visit(q)
		}
	}
	for _, p := range dw.rootLocal {
		visit(p)
	}
	dw.order = slices.DeleteFunc(dw.order, func(p *depPkg) bool { return !seen[p] })
}

// grantSet is the sources and urls the grant manifest's unsafe_deps names:
// an entry that is a local name of one of its own Deps stands for that
// Dep's source or url (after replace), any other entry for itself.
func (dw *depWalk) grantSet(gm *Manifest) map[string]bool {
	out := map[string]bool{}
	for _, g := range gm.UnsafeDeps {
		out[g] = true
		for _, d := range append(slices.Clone(gm.Deps), gm.TestDeps...) {
			if d.Name != g {
				continue
			}
			if d.Source != "" {
				rd, _ := dw.replaced(d)
				out[rd.Source] = true
			}
			if d.URL != "" {
				out[d.URL] = true
			}
		}
	}
	return out
}

// WorkspaceMembers is the member directories of the workspace dir belongs
// to (dir itself when its manifest lists members), for `./...` after mk
// build, mk test and mk check; nil when dir is in no workspace.
func WorkspaceMembers(cfg Config) ([]string, error) {
	cfg.CacheDir = "" // only the manifest step runs, which keeps nothing
	s, err := NewSession(cfg)
	if err != nil {
		return nil, err
	}
	ws, ds := s.workspaceOnly()
	if len(ds) > 0 {
		return nil, &diagError{ds: ds}
	}
	if ws == nil {
		return nil, nil
	}
	return ws.members, nil
}

// diagError is a manifest diagnostic as an error, for WorkspaceMembers.
type diagError struct{ ds []*diag.Diagnostic }

func (e *diagError) Error() string {
	var msgs []string
	for _, d := range e.ds {
		msgs = append(msgs, d.Message)
	}
	return strings.Join(msgs, "; ")
}

// workspaceOnly runs the manifest step far enough to find the workspace.
func (s *Session) workspaceOnly() (*wsInfo, []*diag.Diagnostic) {
	w := newWorld(s.cfg)
	s.w = w
	ms := w.newManifestStep(w.stdTree())
	if _, ds := ms.load(w.stdTree(), stdLayout.Manifest, ""); len(ds) > 0 {
		return nil, ds
	}
	pkgT := w.diskTree(s.root.dir, s.root.prefix)
	man, ds := ms.load(pkgT, "pkg.mk", "main")
	if len(ds) > 0 || man == nil {
		return nil, ds
	}
	dw := &depWalk{s: s, ms: ms, rootDir: s.root.dir, wsRoot: s.root.dir, loaded: map[string]*Manifest{}}
	ws := dw.findWorkspace(man)
	return ws, dw.diags
}
