// Why: adding, removing or upgrading a dependency must edit pkg.mk the way
// a person would and leave pkg.sum consistent, without the user editing
// either by hand.
//
// What: the package commands mk new, install, remove, update, tree and why
// (NewPackage, Session.Install, Remove, Update, Tree, Why), with the seed
// directory, version selection (tagVersions, newestWithin) and the
// recheck that follows an edit. mk query perimeter is deps_grants.go, mk
// vendor vendor.go.
//
// How: each command edits pkg.mk through manifest_edit.go, so only the
// edited list changes, then checks the package, which records the pkg.sum
// lines it lacks; a check that cannot fetch puts pkg.mk and pkg.sum back.
// The flags are cmd/mk/pkgcmd.go's; every command registers itself there.

package driver

import (
	"cmp"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"mk/internal/ast"
	"mk/internal/diag"
	"mk/internal/fetch"
	"mk/internal/format"
	"mk/internal/git"
)

// NewPackage is mk new: <dir>/<name>/pkg.mk with mk set to version (the
// running mk's X.Y) and one bin, and src/main.mk whose Main prints one
// line. Both are fixed points of mk fmt. An existing directory is refused.
func NewPackage(dir, name, version string) (string, error) {
	if !validDepName(name) {
		return "", usagef("%q is not a package name (one identifier: letters, digits and _)", name)
	}
	root := filepath.Join(dir, name)
	if _, err := os.Stat(root); err == nil {
		return "", usagef("%s exists", root)
	}
	man := "const Package = pkg.Package(\n" +
		"    name: " + mkQuote(name) + ",\n" +
		"    mk: " + mkQuote(version) + ",\n" +
		"    bins: [pkg.Bin(name: " + mkQuote(name) + ", root: \"src\", subsystem: \"console\")],\n" +
		")\n"
	main := "export Main(sys: &System): WriteError!void {\n    try sys.Print(\"hello from " + name + "\\n\")\n}\n"
	files := map[string]string{"pkg.mk": man, filepath.Join("src", "main.mk"): main}
	for rel, text := range files {
		out, err := format.Source(rel, []byte(text))
		if err != nil || string(out) != text {
			return "", fmt.Errorf("%s is not a fixed point of mk fmt (%v)", rel, err)
		}
	}
	if err := os.MkdirAll(filepath.Join(root, "src"), 0o755); err != nil {
		return "", err
	}
	for rel, text := range files {
		if err := writeFileAtomic(filepath.Join(root, rel), []byte(text)); err != nil {
			return "", err
		}
	}
	return root, nil
}

// MinorOf is version's X.Y, the form mk new writes.
func MinorOf(version string) string {
	if v, ok := parseSemver(version); ok {
		return fmt.Sprintf("%d.%d", v.Major, v.Minor)
	}
	return version
}

// PkgOptions are the package commands' options.
type PkgOptions struct {
	Name   string // mk install --name
	DryRun bool   // mk update --dry-run
}

// PkgResult is what a package command did: lines for the user, and the
// check of the package afterwards (its diagnostics, an E0215 among them).
type PkgResult struct {
	Lines []string
	Check Result
}

// seedDir is the toolchain's seed directory: <toolchain>/seed beside the
// running mk's bin/. Tests replace it.
var seedDir = func() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	return filepath.Join(filepath.Dir(filepath.Dir(exe)), "seed")
}

// seedLine is one line of seed/first-party.txt.
type seedLine struct{ short, source, version, commit, hash string }

// readSeed reads seed/first-party.txt: `<short name> <source> v<version>
// <commit> tree-sha256:<hex>`, '#' comments.
func readSeed() ([]seedLine, error) {
	dir := seedDir()
	if dir == "" {
		return nil, nil
	}
	b, err := os.ReadFile(filepath.Join(dir, "first-party.txt"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []seedLine
	for _, l := range strings.Split(string(b), "\n") {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		f := strings.Fields(l)
		if len(f) != 5 || !strings.HasPrefix(f[2], "v") {
			return nil, fmt.Errorf("%s: bad line %q", filepath.Join(dir, "first-party.txt"), l)
		}
		out = append(out, seedLine{f[0], f[1], f[2][1:], f[3], f[4]})
	}
	return out, nil
}

// seedTree is the seed's copy of the tree whose hash is h, or "".
func seedTree(h fetch.TreeHash) string {
	dir := seedDir()
	if dir == "" {
		return ""
	}
	p := filepath.Join(dir, "pkg", fmt.Sprintf("%x", h[:]))
	if fi, err := os.Stat(p); err == nil && fi.IsDir() {
		return p
	}
	return ""
}

// fetcher is a fetcher for the commands' tag lists.
func (s *Session) cmdFetcher() (*fetch.Fetcher, error) {
	f := &fetch.Fetcher{Client: s.cfg.TestHooks.HTTPClient, Offline: s.cfg.Offline}
	n, err := git.LoadNetrc(git.NetrcPath(os.Getenv, runtime.GOOS == "windows"))
	if err != nil {
		return nil, err
	}
	f.Netrc = n
	return f, nil
}

// tagVersions is source's release versions (tags vX.Y.Z), plus pre-release
// tags when pre.
func (s *Session) tagVersions(source string, pre bool) ([]semver, error) {
	f, err := s.cmdFetcher()
	if err != nil {
		return nil, err
	}
	refs, err := f.GitTags(fetch.SourceURL(source))
	if err != nil {
		return nil, err
	}
	var out []semver
	for _, r := range refs {
		t, ok := strings.CutPrefix(r.Name, "refs/tags/v")
		if !ok {
			continue
		}
		if v, ok := parseSemver(t); ok && (pre || v.Pre == nil) {
			out = append(out, v)
		}
	}
	slices.SortFunc(out, compareSemver)
	return out, nil
}

// newestWithin is the newest of vs whose leading parts equal want's
// (major; major and minor), or false.
func newestWithin(vs []semver, parts int, want semver) (semver, bool) {
	for i := len(vs) - 1; i >= 0; i-- {
		v := vs[i]
		if parts >= 1 && v.Major != want.Major || parts >= 2 && v.Minor != want.Minor {
			continue
		}
		return v, true
	}
	return semver{}, false
}

// recheck is a fresh check of the package after an edit.
func (s *Session) recheck() (*Session, Result, error) {
	n, err := NewSession(s.cfg)
	if err != nil {
		return nil, Result{}, err
	}
	n.noVendor = s.noVendor
	return n, n.Check(), nil
}

// pastVendor makes s's checks walk with vendor/ ignored: mk install, mk
// update and mk remove change requirements, which vendor/index.txt, the old
// build list, cannot answer. It returns the note saying vendor/ is now
// stale, when it has an index.
func (s *Session) pastVendor() []string {
	s.noVendor = true
	if _, err := os.Stat(filepath.Join(s.root.dir, "vendor", vendorIndexFile)); err != nil {
		return nil
	}
	return []string{"note: vendor/ was not changed; run mk vendor"}
}

// Install is mk install <short name> | <source>[@<version>]: a Dep added to
// deps, its pkg.sum lines written by the check that follows, and the grants
// it needs printed (never added).
func (s *Session) Install(spec string, opt PkgOptions) (PkgResult, error) {
	var res PkgResult
	if s.root.kind != rootPackage {
		return res, usagef("mk install needs a package (a directory holding pkg.mk)")
	}
	res.Lines = append(res.Lines, s.pastVendor()...)
	src, ver, hasAt := strings.Cut(spec, "@")
	var v semver
	var seeded *seedLine
	if !hasAt && !strings.ContainsAny(src, "./") {
		seed, err := readSeed()
		if err != nil {
			return res, err
		}
		for i := range seed {
			if seed[i].short == src {
				seeded = &seed[i]
			}
		}
		if seeded == nil {
			return res, usagef("mk install: %s is no first-party package of this toolchain's seed, and no source (host/path)", src)
		}
		src = seeded.source
		v, _ = parseSemver(seeded.version)
		if opt.Name == "" {
			opt.Name = seeded.short
		}
	} else {
		if err := checkSource(src); err != nil {
			return res, usagef("mk install: %v", err)
		}
		parts := strings.Split(ver, ".")
		switch {
		case hasAt && len(parts) == 3:
			pv, ok := parseSemver(ver)
			if !ok {
				return res, usagef("mk install: %q is not a version X.Y.Z", ver)
			}
			v = pv
		default:
			var want semver
			n := 0
			if hasAt {
				pv, ok := parseSemver(ver + strings.Repeat(".0", 3-len(parts)))
				if !ok || len(parts) > 2 {
					return res, usagef("mk install: %q is not X, X.Y or X.Y.Z", ver)
				}
				want, n = pv, len(parts)
			}
			vs, err := s.tagVersions(src, false)
			if err != nil {
				return res, err
			}
			nv, ok := newestWithin(vs, n, want)
			if !ok {
				return res, &fetch.Error{Reason: "no-version", Msg: fmt.Sprintf("%s has no release tag%s", src, cond(hasAt, " within "+ver, ""))}
			}
			v = nv
		}
	}
	name := cmp.Or(opt.Name, identifierOf(path.Base(src)))
	if !validDepName(name) {
		return res, usagef("mk install: %q is not a local name; choose one with --name", name)
	}
	e, err := openManifest(s.root.dir)
	if err != nil {
		return res, err
	}
	if field, _, _, err := e.depElem(name); err != nil {
		return res, err
	} else if field != "" {
		return res, usagef("mk install: the name %s is already used in %s; choose another with --name", name, field)
	}
	dep := "pkg.Dep(name: " + mkQuote(name) + ", source: " + mkQuote(src) + ", version: " + mkQuote(v.String()) + ")"
	if err := e.add("deps", dep); err != nil {
		return res, err
	}
	// The edited manifest checks before anything is written,
	// and a check that cannot fetch the new version puts pkg.mk and
	// pkg.sum back as they were, with no "added" line.
	if _, err := e.finish(s.cfg, true); err != nil {
		return res, err
	}
	ws := s.root.dir
	if wsi, _ := s.workspaceDir(); wsi != "" {
		ws = wsi
	}
	restore := snapshotFiles(e.path, filepath.Join(ws, sumFile))
	if seeded != nil {
		// The seed's line goes into pkg.sum first, so the check reads the
		// seed's tree and fetches nothing.
		sum, err := readPkgSum(ws)
		if err != nil {
			return res, err
		}
		l := sumLine{Source: src, Version: v.String(), Commit: seeded.commit, Hash: seeded.hash}
		if reason, old := sum.check(l); reason != "" {
			return res, &fetch.Error{Reason: reason, Msg: fmt.Sprintf("pkg.sum records %s for %s %s, the seed %s", old.Hash, src, v.Tag(), l.Hash)}
		}
		if sum.add(l) {
			if err := writePkgSum(ws, sum); err != nil {
				return res, err
			}
		}
	}
	if _, err := e.finish(s.cfg, false); err != nil {
		restore()
		return res, err
	}
	_, r, err := s.recheck()
	if err != nil {
		restore()
		return res, err
	}
	res.Check = r
	if fetchFailed(r) {
		restore()
		return res, nil
	}
	res.Lines = append(res.Lines, fmt.Sprintf("added %s (%s %s)", name, src, v.Tag()))
	for _, d := range r.Report.Diags {
		if d.Diag.Code.String() == "E0215" {
			res.Lines = append(res.Lines, "needs a grant: "+d.Diag.Message)
		}
	}
	return res, nil
}

// snapshotFiles reads each path, and returns the function that puts every
// one back as it was (removing one that did not exist).
func snapshotFiles(paths ...string) func() {
	type was struct {
		data []byte
		ok   bool
	}
	old := make([]was, len(paths))
	keep := make([]bool, len(paths)) // could not read it: never touch it
	for i, p := range paths {
		b, err := os.ReadFile(p)
		old[i] = was{b, err == nil}
		keep[i] = err != nil && !errors.Is(err, fs.ErrNotExist)
	}
	return func() {
		for i, p := range paths {
			switch {
			case keep[i]:
			case old[i].ok:
				_ = writeFileAtomic(p, old[i].data)
			default:
				_ = os.Remove(p)
			}
		}
	}
}

// fetchFailed reports whether r holds an E0202 whose reason says the
// dependency could not be had (fetched, found or matched).
func fetchFailed(r Result) bool {
	for _, d := range r.Report.Diags {
		if d.Diag.Code != diag.E0202 {
			continue
		}
		reason, _ := d.Diag.FactValue("reason")
		switch reason.Str {
		case "fetch", "no-version", "offline", "protocol", "sum-mismatch", "sum-missing", "tag-moved", "hash-mismatch", "unsafe-archive":
			return true
		}
	}
	return false
}

// cond is a if c, else b.
func cond(c bool, a, b string) string {
	if c {
		return a
	}
	return b
}

// workspaceDir is the directory holding the package's pkg.sum: its
// workspace root's, else its own.
func (s *Session) workspaceDir() (string, error) {
	ws, ds := s.workspaceOnly()
	if len(ds) > 0 {
		return "", &diagError{ds: ds}
	}
	if ws == nil {
		return "", nil
	}
	return ws.dir, nil
}

// Remove is mk remove <name>: the Dep leaves deps or test_deps, and the
// pkg.sum lines no package of the workspace still uses leave pkg.sum.
func (s *Session) Remove(name string) (PkgResult, error) {
	var res PkgResult
	if s.root.kind != rootPackage {
		return res, usagef("mk remove needs a package (a directory holding pkg.mk)")
	}
	res.Lines = append(res.Lines, s.pastVendor()...)
	e, err := openManifest(s.root.dir)
	if err != nil {
		return res, err
	}
	field, al, i, err := e.depElem(name)
	if err != nil {
		return res, err
	}
	if field == "" {
		return res, usagef("mk remove: no dependency is named %s in deps or test_deps", name)
	}
	if err := e.remove(al, i); err != nil {
		return res, err
	}
	if _, err := e.finish(s.cfg, false); err != nil {
		return res, err
	}
	res.Lines = append(res.Lines, "removed "+name+" from "+field)
	// The lines still used: every member's build (or this package's).
	n, r, err := s.recheck()
	if err != nil {
		return res, err
	}
	res.Check = r
	ws, _ := n.workspaceOnly()
	dirs := []string{s.root.dir}
	sumDir := s.root.dir
	if ws != nil {
		dirs, sumDir = ws.members, ws.dir
	}
	used := map[string]bool{}
	for _, d := range dirs {
		cfg := s.cfg
		cfg.Root = d
		m, err := NewSession(cfg)
		if err != nil {
			return res, err
		}
		m.noVendor = s.noVendor
		if mr := m.Check(); len(mr.Report.Diags) > 0 && m.walkedSum == nil {
			res.Lines = append(res.Lines, "pkg.sum kept whole: "+d+" does not check")
			return res, nil
		}
		if m.walkedSum != nil {
			for k := range m.walkedSum.used {
				used[k] = true
			}
		}
	}
	sum, err := readPkgSum(sumDir)
	if err != nil {
		return res, err
	}
	var gone []string
	if sum.keep(func(l sumLine) bool {
		if !used[l.key()] {
			gone = append(gone, l.String())
			return false
		}
		return true
	}) {
		if err := writePkgSum(sumDir, sum); err != nil {
			return res, err
		}
	}
	slices.Sort(gone)
	for _, g := range gone {
		res.Lines = append(res.Lines, "pkg.sum: removed "+g)
	}
	return res, nil
}

// Update is mk update [name] [--dry-run]: each direct source requirement
// of the package (or the named one) is raised to the newest release tag of
// its major; then every version and perimeter line that changed is
// printed. With DryRun nothing is written, and only the raised
// requirements are printed.
func (s *Session) Update(name string, opt PkgOptions) (PkgResult, error) {
	var res PkgResult
	if s.root.kind != rootPackage {
		return res, usagef("mk update needs a package (a directory holding pkg.mk)")
	}
	res.Lines = append(res.Lines, s.pastVendor()...)
	before, _, err := s.recheck()
	if err != nil {
		return res, err
	}
	e, err := openManifest(s.root.dir)
	if err != nil {
		return res, err
	}
	found := false
	raised := 0
	for _, field := range []string{"deps", "test_deps"} {
		al, err := e.list(field)
		if err != nil {
			return res, err
		}
		if al == nil {
			continue
		}
		// From the last element, so earlier spans stay valid after a splice.
		for i := len(al.Elems) - 1; i >= 0; i-- {
			c, ok := al.Elems[i].(*ast.CallExpr)
			if !ok || !isPkgCall(c, "Dep", e.in) {
				continue
			}
			n, _ := e.strArg(c, "name")
			src, ok1 := e.strArg(c, "source")
			vs, ok2 := e.strArg(c, "version")
			if !ok1 || !ok2 || src == "" || name != "" && n != name {
				continue
			}
			found = true
			cur, ok := parseSemver(vs)
			if !ok {
				continue
			}
			tags, err := s.tagVersions(src, false)
			if err != nil {
				return res, err
			}
			nv, ok := newestWithin(tags, 1, cur)
			if !ok || compareSemver(nv, cur) <= 0 {
				continue
			}
			e.setStrArg(c, "version", nv.String())
			raised++
			res.Lines = append(res.Lines, fmt.Sprintf("%s (%s): %s -> %s", n, src, cur.Tag(), nv.Tag()))
		}
		if err := e.edited(); err != nil {
			return res, err
		}
	}
	if name != "" && !found {
		return res, usagef("mk update: no source dependency is named %s", name)
	}
	slices.Sort(res.Lines)
	if raised == 0 {
		res.Lines = append(res.Lines, "every requirement is at the newest release of its major")
		return res, nil
	}
	if _, err := e.finish(s.cfg, opt.DryRun); err != nil {
		return res, err
	}
	if opt.DryRun {
		return res, nil
	}
	after, r, err := s.recheck()
	if err != nil {
		return res, err
	}
	res.Check = r
	res.Lines = append(res.Lines, versionChanges(before.walkedChosen, after.walkedChosen)...)
	res.Lines = append(res.Lines, lineDiff("perimeter", perimeterLines(before.perimeter()), perimeterLines(after.perimeter()))...)
	return res, nil
}

// versionChanges is one line per identity whose chosen version changed.
func versionChanges(a, b map[string]*selected) []string {
	keys := map[string]bool{}
	for k := range a {
		keys[k] = true
	}
	for k := range b {
		keys[k] = true
	}
	var out []string
	for k := range keys {
		var av, bv string
		if s := a[k]; s != nil {
			av = s.Version.Tag()
		}
		if s := b[k]; s != nil {
			bv = s.Version.Tag()
		}
		if av != bv {
			out = append(out, fmt.Sprintf("version: %s %s -> %s", strings.Split(k, "@v")[0], cmp.Or(av, "(none)"), cmp.Or(bv, "(none)")))
		}
	}
	slices.Sort(out)
	return out
}

// lineDiff is the lines of b not in a ("+") and of a not in b ("-").
func lineDiff(what string, a, b []string) []string {
	var out []string
	for _, l := range a {
		if !slices.Contains(b, l) {
			out = append(out, what+": - "+strings.TrimSpace(l))
		}
	}
	for _, l := range b {
		if !slices.Contains(a, l) {
			out = append(out, what+": + "+strings.TrimSpace(l))
		}
	}
	return out
}

// display is a package as a reader sees it: `name (source vX.Y.Z)`, or
// `name (path)` for a path package.
func (p *depPkg) display(name string) string {
	switch {
	case p.version != "":
		return name + " (" + p.source + " v" + p.version + ")"
	case p.fetched():
		return name + " (" + redactURL(p.grantSource()) + ")"
	}
	return name + " (" + p.rel + ")"
}

// Tree is mk tree: the build list as a tree from the package, each
// package with its selected version and the minimum its requirer states;
// a package met again is shown once, then "(above)".
func (s *Session) Tree() (PkgResult, error) {
	var res PkgResult
	n, r, err := s.recheck()
	res.Check = r
	if err != nil || n.walkedSelf == nil {
		return res, err
	}
	root := s.root.name
	if m := n.manifestOf(nil); m != nil {
		root = m.Name
	}
	res.Lines = append(res.Lines, root)
	seen := map[*depPkg]bool{}
	var visit func(man *Manifest, local map[string]*depPkg, indent string)
	visit = func(man *Manifest, local map[string]*depPkg, indent string) {
		if man == nil {
			return
		}
		for _, d := range append(slices.Clone(man.Deps), man.TestDeps...) {
			p := local[d.Name]
			if p == nil {
				continue
			}
			l := indent + p.display(d.Name)
			if d.Version != "" && d.Version != p.version {
				l += ", wants v" + d.Version
			}
			if seen[p] {
				res.Lines = append(res.Lines, l+" (above)")
				continue
			}
			seen[p] = true
			res.Lines = append(res.Lines, l)
			visit(p.man, p.local, indent+"  ")
		}
	}
	visit(n.manifestOf(nil), n.walkedRoot, "  ")
	return res, nil
}

// manifestOf is p's manifest, or the package's own for nil.
func (s *Session) manifestOf(p *depPkg) *Manifest {
	if p != nil {
		return p.man
	}
	return s.walkedSelf
}

// Why is mk why <name or source>: every requirement path from the package
// to it, and the manifest whose minimum selected its version.
func (s *Session) Why(target string) (PkgResult, error) {
	var res PkgResult
	n, r, err := s.recheck()
	res.Check = r
	if err != nil {
		return res, err
	}
	root := n.manifestOf(nil)
	if root == nil {
		return res, usagef("mk why: the package does not check far enough to answer")
	}
	match := func(p *depPkg) bool {
		return p.source == target || p.grantSource() == target || p.rel == target || slices.Contains(p.names, target)
	}
	var found *depPkg
	var walk func(man *Manifest, local map[string]*depPkg, chain []string, on map[*depPkg]bool)
	walk = func(man *Manifest, local map[string]*depPkg, chain []string, on map[*depPkg]bool) {
		if man == nil || len(res.Lines) >= 100 {
			return
		}
		for _, d := range append(slices.Clone(man.Deps), man.TestDeps...) {
			p := local[d.Name]
			if p == nil || on[p] {
				continue
			}
			c := append(slices.Clone(chain), p.display(d.Name))
			if match(p) {
				found = p
				res.Lines = append(res.Lines, strings.Join(c, " -> "))
				continue
			}
			on[p] = true
			walk(p.man, p.local, c, on)
			delete(on, p)
		}
	}
	walk(root, n.walkedRoot, []string{root.Name}, map[*depPkg]bool{})
	if found == nil {
		return res, usagef("mk why: no package of the build is named %s or has that source", target)
	}
	if sel := n.walkedChosen[sourceMajorOf(found)]; sel != nil {
		var by []string
		for who, v := range sel.Minimums {
			if compareSemver(v, sel.Version) == 0 {
				by = append(by, cmp.Or(who, root.Name+"'s pkg.mk"))
			}
		}
		slices.Sort(by)
		res.Lines = append(res.Lines, fmt.Sprintf("%s is selected by the minimum in %s", sel.Version.Tag(), strings.Join(by, ", ")))
	}
	if found.man != nil && (len(found.man.Members) > 0 || len(found.man.Replace) > 0) {
		res.Lines = append(res.Lines, "its manifest's members and replace are ignored (they are read in the root manifest only)")
	}
	return res, nil
}

// sourceMajorOf is a source package's identity key in the version choice.
func sourceMajorOf(p *depPkg) string {
	return strings.TrimPrefix(p.identity, "source ")
}
