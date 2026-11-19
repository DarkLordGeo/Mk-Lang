// Why: a Dep with a source names a git host and a minimum version, not a
// tree; the build must choose the versions, fetch and verify their trees,
// and record what it read, with the network touched as little as possible.
//
// What: the source half of the dependency walk: sources and bindPending
// (the build path), vendoredSources and vendoredSource (an indexed
// vendor/), replace handling (replaced, replacedReqs), readManifest,
// fetchSource, loadSum, saveSum and the E0202 helpers.
//
// How: a Dep with a source is a requirement. The walk (deps_walk.go)
// collects the requirements of the root and of every path and url package;
// minimal version selection (deps_mvs.go) reads each visited version's
// pkg.mk alone, through git.Client.FetchFile (the commit with "filter
// blob:none" and the one blob, or the whole commit where the server has no
// filter); then each chosen version's tree is fetched, checked against
// pkg.sum and walked like any package, and every Dep with a source is bound,
// in the manifest that wrote it, to the chosen version of its identity (a
// source and its major). pkg.sum gains the lines it lacks: one per chosen
// version (`<source> v<version> <commit> tree-sha256:<hex>`) and one per
// version whose manifest was read (`/pkg.mk`, `sha256:<hex of pkg.mk>`),
// written atomically beside the root manifest. A tag that names another
// commit than its line is E0202 tag-moved; other bytes are sum-mismatch.
//
// A visited version's manifest is kept at
// <cache>/pkg/manifests/<canonical id>@v<version>.mk and reused when
// pkg.sum's /pkg.mk line holds its hash, so a repeated walk reads no
// manifest from the network. A chosen version's tree is kept in the
// package cache, <cache>/pkg/<tree hash>/ (deps_pkgcache.go).
//
// replace (root manifest only): an entry whose name is a source and which
// gives a path makes every Dep on that source a path dependency on the
// path (relative to the root); one that gives a source and version makes
// every requirement on it a requirement on the replacement.

package driver

import (
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"mk/internal/fetch"
	"mk/internal/git"
)

// pendingDep is a Dep with a source, waiting for its version: the
// manifest that wrote it and that manifest's local names.
type pendingDep struct {
	man   *Manifest
	local map[string]*depPkg
	d     Dep
	owner *depPkg // the package whose manifest man is; nil for the root's
}

// replaced is d after the root's replace entries: a path Dep when one
// gives a path (true); a source Dep with the replacement's source and
// version otherwise.
func (dw *depWalk) replaced(d Dep) (Dep, bool) {
	// Entries chain whatever their order: the replacement is
	// replaced again until no entry names it. An entry naming its own
	// source pins the version, as Go's `replace x => x v1.1.0`, and is the
	// chain's last step. Each source has one entry (two are
	// E0202 replace), and validation refuses a cycle; seen stops one here
	// regardless.
	seen := map[string]bool{}
	for {
		i := slices.IndexFunc(dw.replace, func(r Dep) bool { return r.Name == d.Source })
		if i < 0 || seen[d.Source] {
			return d, false
		}
		r := dw.replace[i]
		if r.Path != "" {
			return Dep{Name: d.Name, Path: r.Path, Field: d.Field}, true
		}
		if r.Source == "" {
			return d, false
		}
		if r.Source == d.Source {
			d.Version = r.Version
			return d, false
		}
		seen[d.Source] = true
		d.Source, d.Version = r.Source, r.Version
	}
}

// replacedReqs are the requirements of m, a visited version's manifest,
// after the root's replace entries: a replacement by source
// and version is required instead of the original, and a replacement by
// path stands for the requirements of the manifest in that directory, so
// the version walk never reads, fetches or records a replaced source.
func (dw *depWalk) replacedReqs(m *Manifest, by string) ([]requirement, error) {
	return dw.replacedReqsSeen(m, by, map[string]bool{})
}

func (dw *depWalk) replacedReqsSeen(m *Manifest, by string, seen map[string]bool) ([]requirement, error) {
	var out []requirement
	for _, d := range append(slices.Clone(m.Deps), m.TestDeps...) {
		if d.Source == "" {
			continue
		}
		rd, isPath := dw.replaced(d)
		if isPath {
			fm, err := dw.replacementManifest(rd)
			if err != nil || fm == nil || seen[pathKey(fm.dir)] {
				if err != nil {
					return nil, err
				}
				continue
			}
			seen[pathKey(fm.dir)] = true
			sub, err := dw.replacedReqsSeen(fm.man, by, seen)
			if err != nil {
				return nil, err
			}
			out = append(out, sub...)
			continue
		}
		v, ok := parseSemver(rd.Version)
		if !ok {
			return nil, &mvsError{"invalid", fmt.Sprintf("%s: version %q is not X.Y.Z", d.Field, rd.Version)}
		}
		out = append(out, requirement{Source: rd.Source, Version: v, By: by})
	}
	return out, nil
}

// replacedManifest is a path replacement's directory and manifest.
type replacedManifest struct {
	dir string
	man *Manifest
}

// replacementManifest loads the manifest of rd, a path Dep a replace
// entry gives (relative to the workspace root), once: the walk of the
// package later reuses it (dw.loaded). A load failure is in dw.diags.
func (dw *depWalk) replacementManifest(rd Dep) (*replacedManifest, error) {
	dir := filepath.Join(dw.wsRoot, filepath.FromSlash(rd.Path))
	if m := dw.loaded[pathKey(dir)]; m != nil {
		return &replacedManifest{dir, m}, nil
	}
	if !hasManifest(dir) {
		return nil, &mvsError{"invalid", fmt.Sprintf("replace %s: %s is not a package directory (a directory holding pkg.mk)", rd.Name, rd.Path)}
	}
	id, _ := dw.identityOf(rd, dw.wsRoot)
	m, ds := dw.ms.load(dw.s.w.diskTree(dir, dw.prefixOf(&depPkg{identity: id}, dir)), "pkg.mk", rd.Name)
	if len(ds) > 0 {
		dw.diags = append(dw.diags, ds...)
		return nil, &mvsError{"invalid", "replace " + rd.Name + ": its manifest does not load"}
	}
	dw.loaded[pathKey(dir)] = m
	return &replacedManifest{dir, m}, nil
}

// require records d, a source Dep of man, whose local names are local.
func (dw *depWalk) require(man *Manifest, local map[string]*depPkg, d Dep) {
	d, _ = dw.replaced(d)
	v, ok := parseSemver(d.Version)
	if !ok {
		dw.diags = append(dw.diags, depFail(man, d.Field+".version", "invalid", "dependency %s: version %q is not X.Y.Z", d.Name, d.Version))
		return
	}
	dw.pending = append(dw.pending, pendingDep{man: man, local: local, d: d, owner: dw.owner()})
	dw.reqs = append(dw.reqs, requirement{Source: d.Source, Version: v})
}

// sourceID is a source package's identity and canonical id: its source
// and major (every major its own identity, v0 and v1 included).
func sourceID(source string, v semver) (id, cid string) {
	key := sourceMajor(source, v)
	return "source " + key, canonicalID(path.Base(source), key)
}

// sources runs the version walk from the requirements the walk met, then
// fetches and walks each chosen version and binds every source Dep.
func (dw *depWalk) sources(root *Manifest) {
	if dw.vendor && !dw.vindex.empty() {
		dw.vendoredSources(root)
		return
	}
	if dw.loadSum(root) == nil {
		return
	}
	list, _, err := selectVersionsWith(dw.reqs, dw.readManifest, dw.replacedReqs)
	if len(dw.diags) > 0 {
		return // a replacement's manifest failed to load
	}
	if err != nil {
		man, field := dw.pending[0].man, dw.pending[0].d.Field
		var me *mvsError
		if errors.As(err, &me) {
			// The Dep of the source the failure names, when one is known.
			for _, pd := range dw.pending {
				if strings.Contains(me.Msg, pd.d.Source+" ") {
					man, field = pd.man, pd.d.Field
					break
				}
			}
		}
		dw.diags = append(dw.diags, depFail(man, field, reasonOf(err), "%v", err))
		return
	}
	dw.chosen = list
	byID := map[string]*depPkg{}
	ids := sortedIdentities(list)
	for _, key := range ids {
		sel := list[key]
		p := dw.fetchSource(root, sel)
		if p == nil {
			return
		}
		byID[key] = p
	}
	// Bind every source Dep met so far, then walk each chosen package's
	// own Deps (its url Deps; its source Deps were read by the version
	// walk and bind to chosen versions too).
	dw.bindPending(byID)
	for _, key := range ids {
		dw.walkChosen(byID[key])
	}
	dw.bindPending(byID)
	if len(dw.diags) > 0 {
		return
	}
	dw.reorder(root)
}

// bindPending binds every pending source Dep to the package of its
// identity in byID, in the manifest that wrote it, and empties the list.
func (dw *depWalk) bindPending(byID map[string]*depPkg) {
	for _, pd := range dw.pending {
		v, _ := parseSemver(pd.d.Version)
		if p := byID[sourceMajor(pd.d.Source, v)]; p != nil {
			pd.local[pd.d.Name] = p
			if p.viaMan == nil {
				p.parent, p.viaMan, p.via = pd.owner, pd.man, pd.d.Field
			}
			if !slices.Contains(p.names, pd.d.Name) {
				p.names = append(p.names, pd.d.Name)
			}
		}
	}
	dw.pending = nil
}

// walkChosen walks a chosen source package's own Deps.
func (dw *depWalk) walkChosen(p *depPkg) {
	dw.stack = append(dw.stack, p)
	p.local = dw.walk(p.man, "")
	dw.stack = dw.stack[:len(dw.stack)-1]
	p.done = true
}

// vendoredSources is sources for a build that reads vendor/ with an index:
// the index is the build list. Each requirement binds to the index's line
// for its source and major, read from vendor/<canonical id>/, and that
// package's own requirements are met the same way; there is no version walk
// and no network, and pkg.sum is read only to check each index line against
// its line, never written. A requirement the index does not hold, or whose
// minimum is above the index's version, is E0202 invalid naming mk vendor:
// the index and a walk are never mixed.
func (dw *depWalk) vendoredSources(root *Manifest) {
	byID := map[string]*depPkg{}
	dw.chosen = map[string]*selected{}
	// The walk of each package appends its own requirements to pending.
	for i := 0; i < len(dw.pending); i++ {
		pd := dw.pending[i]
		v, _ := parseSemver(pd.d.Version)
		key := sourceMajor(pd.d.Source, v)
		p := byID[key]
		if p == nil {
			e, ok := dw.vindex.lookupSource(pd.d.Source, v.Major)
			if !ok {
				dw.diags = append(dw.diags, depFail(pd.man, pd.d.Field, "invalid",
					"dependency %s: %s v%d is not in vendor/index.txt; run mk vendor again", pd.d.Name, pd.d.Source, v.Major))
				return
			}
			if p = dw.vendoredSource(pd, e); p == nil {
				return
			}
			byID[key] = p
			ev, _ := parseSemver(p.version)
			dw.chosen[key] = &selected{Source: p.source, Version: ev, Man: p.man, Minimums: map[string]semver{}}
			if dw.walkChosen(p); len(dw.diags) > 0 {
				return
			}
		}
		sel := dw.chosen[key]
		if compareSemver(v, sel.Version) > 0 {
			dw.diags = append(dw.diags, depFail(pd.man, pd.d.Field, "invalid",
				"dependency %s needs %s %s, but vendor/index.txt holds %s; run mk vendor again", pd.d.Name, pd.d.Source, v.Tag(), sel.Version.Tag()))
			return
		}
		by := "" // mk tree and mk why: who states which minimum
		if pd.owner != nil && pd.owner.source != "" {
			by = pd.owner.source + "@" + pd.owner.version
		}
		if old, ok := sel.Minimums[by]; !ok || compareSemver(v, old) > 0 {
			sel.Minimums[by] = v
		}
	}
	dw.bindPending(byID)
	dw.reorder(root)
}

// vendoredSource makes the index's line e, for the requirement pd, a
// package of the build read from vendor/<canonical id>/. It keeps its
// source's identity (so it needs its grant), and the canonical id
// the line names must be the one its source and major give.
func (dw *depWalk) vendoredSource(pd pendingDep, e vendorEntry) *depPkg {
	v, _ := parseSemver(e.version)
	id, cid := sourceID(pd.d.Source, v)
	if e.cid != cid {
		dw.diags = append(dw.diags, depFail(pd.man, pd.d.Field, "invalid",
			"dependency %s: vendor/index.txt names %s for %s, but its canonical id is %s; run mk vendor again", pd.d.Name, e.cid, pd.d.Source, cid))
		return nil
	}
	// The index line agrees with pkg.sum's line for the version, when it
	// has one: vendor/ and pkg.sum record one tree.
	sum := dw.loadSum(dw.root)
	if sum == nil {
		return nil
	}
	if l, ok := sum.get(pd.d.Source, v.String(), false); ok && l.Hash != e.hash.String() {
		dw.diags = append(dw.diags, depFail(pd.man, pd.d.Field, fetch.ReasonSumMismatch,
			"dependency %s: vendor/index.txt records %s for %s %s, but pkg.sum records %s; run mk vendor again", pd.d.Name, e.hash, pd.d.Source, v.Tag(), l.Hash))
		return nil
	}
	dir, ok := dw.vendoredDir(e, pd.man, pd.d)
	if !ok {
		return nil
	}
	p := &depPkg{name: path.Base(pd.d.Source), identity: id, cid: cid, from: pd.d.Source + " " + v.Tag(),
		source: pd.d.Source, version: v.String(), vendored: true, osDir: dir, indexCID: e.cid} // fetched: no origDir
	p.tree = dw.s.w.diskTree(dir, "vendor/"+cid+"/")
	p.tree.sitePrefix = p.tree.prefix
	m, ds := dw.ms.load(p.tree, "pkg.mk", p.name)
	if len(ds) > 0 {
		dw.diags = append(dw.diags, ds...)
		return nil
	}
	p.man = m
	if q := dw.byCID[cid]; q != nil {
		dw.diags = append(dw.diags, depFail(pd.man, pd.d.Field, "id-clash", "%s and %s have one canonical id, %s", pd.d.Source, q.from, cid))
		return nil
	}
	dw.byID[id], dw.byCID[cid] = p, p
	dw.order = append(dw.order, p)
	return p
}

// reasonSumMissing is E0202's reason for a version with no pkg.sum line
// that an offline build, or one under --locked, meets.
const reasonSumMissing = "sum-missing"

// sumMissingMsg is sum-missing's message for what (a source and version,
// or a url).
func sumMissingMsg(cfg Config, what string) string {
	why := "--locked"
	if !cfg.Locked {
		why = "offline"
	}
	return fmt.Sprintf("pkg.sum has no line for %s, and a build that is %s records none; build it once online without --locked, and commit pkg.sum", what, why)
}

// loadSum is the root's pkg.sum, read once; nil after a diagnostic.
func (dw *depWalk) loadSum(root *Manifest) *pkgSum {
	if dw.sum == nil {
		sum, err := readPkgSum(dw.wsRoot)
		if err != nil {
			dw.diags = append(dw.diags, depFail(root, "deps", "invalid", "%v", err))
			return nil
		}
		dw.sum = sum
	}
	return dw.sum
}

// saveSum writes pkg.sum when the walk added a line, atomically.
func (dw *depWalk) saveSum() {
	if !dw.sumDirt || dw.s.cfg.Locked {
		return // --locked never writes pkg.sum (each missing line was refused)
	}
	if err := mergePkgSum(dw.wsRoot, dw.sum); err != nil {
		// A note, not a fault: the build checked every tree, and builds
		// of older releases never wrote pkg.sum; the next build that can
		// write it records the lines.
		dw.note("note: pkg.sum was not written (%s); the build goes on, and a build that can write it records its lines", pathErrText(err))
	}
}

// reorder puts dw.order in build order again (every package after the
// packages its local names bind), from the root's local names, and refuses
// a cycle through a chosen version.
func (dw *depWalk) reorder(root *Manifest) {
	state := map[*depPkg]int{} // 1 visiting, 2 done
	var out []*depPkg
	var visit func(p *depPkg, chain []string) bool
	visit = func(p *depPkg, chain []string) bool {
		switch state[p] {
		case 1:
			dw.diags = append(dw.diags, depFail(root, "deps", "cycle", "dependency cycle: %s",
				strings.Join(append(chain, p.name), " -> ")))
			return false
		case 2:
			return true
		}
		state[p] = 1
		names := make([]string, 0, len(p.local))
		for n := range p.local {
			names = append(names, n)
		}
		slices.Sort(names)
		for _, n := range names {
			if !visit(p.local[n], append(chain, p.name)) {
				return false
			}
		}
		state[p] = 2
		out = append(out, p)
		return true
	}
	names := make([]string, 0, len(dw.rootLocal))
	for n := range dw.rootLocal {
		names = append(names, n)
	}
	slices.Sort(names)
	for _, n := range names {
		if !visit(dw.rootLocal[n], nil) {
			return
		}
	}
	// A package no root name reaches still builds (a test dependency's,
	// later): keep it after the rest.
	for _, p := range dw.order {
		if state[p] == 0 {
			visit(p, nil)
		}
	}
	dw.order = out
}

// gitClient is the walk's git client: the fetcher's HTTP client and agent,
// netrc from the environment (NETRC, ~/.netrc, %USERPROFILE%\_netrc).
func (dw *depWalk) gitClient() (*git.Client, error) {
	f := dw.fetcherFor()
	n, err := git.LoadNetrc(git.NetrcPath(os.Getenv, runtime.GOOS == "windows"))
	if err != nil {
		return nil, err
	}
	return &git.Client{HTTP: f.Client, Agent: "mk/" + Release, Netrc: n,
		Limits: git.Limits{MaxBytes: fetch.DefaultMaxArchive, MaxFiles: fetch.DefaultMaxFiles}}, nil
}

// manifestCache is where a visited version's pkg.mk is kept, or "" under
// --no-cache.
func (dw *depWalk) manifestCache(source string, v semver) string {
	if dw.s.cfg.NoCache {
		return ""
	}
	_, cid := sourceID(source, v)
	return filepath.Join(dw.s.cacheDir(), "pkg", "manifests", cid+"@"+v.Tag()+".mk")
}

// readManifest is the version walk's reader: a version's pkg.mk from the
// cache when pkg.sum vouches for it, else through FetchFile, checked and
// recorded in pkg.sum, then evaluated alone by the manifest step.
func (dw *depWalk) readManifest(source string, v semver) (*Manifest, error) {
	var data []byte
	line, have := dw.sum.get(source, v.String(), true)
	if p := dw.manifestCache(source, v); p != "" && have {
		if b, err := os.ReadFile(p); err == nil && fetch.Sum(b).String() == line.Hash {
			data = b
		}
	}
	if tl, ok := dw.sum.get(source, v.String(), false); data == nil && ok {
		// The version's whole tree is in the package cache or the seed
		// (pkg.sum names it): its pkg.mk is that tree's, with no fetch.
		if h, ok := fetch.ParseTreeHash(tl.Hash); ok {
			dir, _ := dw.s.cachedTree(h)
			if dir == "" {
				if sd := seedTree(h); sd != "" {
					if got, err := dirTreeHash(sd); err == nil && got == h {
						dir = sd // a seed tree is read only when it hashes to its name
					}
				}
			}
			if dir != "" {
				if b, err := os.ReadFile(filepath.Join(dir, "pkg.mk")); err == nil {
					data = b
				}
			}
		}
	}
	if data == nil {
		if _, tree := dw.sum.get(source, v.String(), false); !have && !tree && (dw.s.cfg.Offline || dw.s.cfg.Locked) {
			return nil, &mvsError{reasonSumMissing, sumMissingMsg(dw.s.cfg, source+" "+v.Tag())}
		}
		if dw.s.cfg.Offline {
			return nil, &mvsError{fetch.ReasonOffline, fmt.Sprintf("the manifest of %s %s is not in the cache, and the build is offline", source, v.Tag())}
		}
		c, err := dw.gitClient()
		if err != nil {
			return nil, &mvsError{"fetch", err.Error()}
		}
		fa, err := c.FetchFile(fetch.SourceURL(source), v.Tag(), "pkg.mk")
		if err != nil {
			return nil, gitError(err, source+" "+v.Tag()+": ")
		}
		got := sumLine{Source: source, Version: v.String(), Manifest: true, Commit: fa.Commit.String(), Hash: fetch.Sum(fa.Data).String()}
		if reason, old := dw.sum.check(got); reason != "" {
			return nil, sumFault(reason, got, old)
		}
		if dw.sum.add(got) {
			dw.sumDirt = true
		}
		data = fa.Data
		if p := dw.manifestCache(source, v); p != "" {
			if os.MkdirAll(filepath.Dir(p), 0o755) == nil {
				_ = writeFileAtomic(p, data)
			}
		}
	}
	_, cid := sourceID(source, v)
	t := &srcTree{mem: map[string]string{"pkg.mk": string(data)}, prefix: cid + "@" + v.Tag() + "/pkg.mk/"}
	dw.ms.loose = true
	m, ds := dw.ms.load(t, "pkg.mk", path.Base(source))
	dw.ms.loose = false
	if len(ds) > 0 {
		dw.diags = append(dw.diags, ds...)
		return nil, &mvsError{"invalid", fmt.Sprintf("the manifest of %s %s does not check", source, v.Tag())}
	}
	return m, nil
}

// fetchSource fetches sel's tag, checks its tree against pkg.sum (adding
// the line it lacks), and makes it a package of the build.
func (dw *depWalk) fetchSource(root *Manifest, sel *selected) *depPkg {
	fail := func(reason, format string, args ...any) *depPkg {
		man, field := dw.fieldOf(root, sel.Source)
		dw.diags = append(dw.diags, depFail(man, field, reason, format, args...))
		return nil
	}
	id, cid := sourceID(sel.Source, sel.Version)
	p := &depPkg{name: path.Base(sel.Source), identity: id, cid: cid, from: sel.Source + " " + sel.Version.Tag(),
		source: sel.Source, version: sel.Version.String()}
	// The package cache first: pkg.sum names the tree, and a
	// cached tree of that hash is that tree.
	if line, ok := dw.sum.get(sel.Source, sel.Version.String(), false); ok {
		h, _ := fetch.ParseTreeHash(line.Hash)
		dir, err := dw.s.cachedTree(h)
		if err != nil {
			return fail(reasonOf(err), "%s %s: %v", sel.Source, sel.Version.Tag(), err)
		}
		if dir != "" {
			pfx := cid + "@" + hex.EncodeToString(h[:6]) + "/"
			// osDir too: its C sources and links are read in place, so no
			// copy keyed by identity alone (cdeps/) is needed.
			p.treeDir, p.osDir = dir, dir
			p.tree = dw.s.w.diskTree(dir, pfx)
			p.tree.sitePrefix = pfx
		}
	}
	if line, ok := dw.sum.get(sel.Source, sel.Version.String(), false); ok && p.tree == nil {
		// The toolchain's seed: a tree checked by its name.
		h, _ := fetch.ParseTreeHash(line.Hash)
		if dir := seedTree(h); dir != "" {
			if got, err := dirTreeHash(dir); err != nil || got != h {
				// Never silent: the seed is damaged.
				dw.note("note: the toolchain's seed tree for %s %s does not hash to %s; it is not used, and the version is fetched", sel.Source, sel.Version.Tag(), h)
			} else {
				pfx := cid + "@" + hex.EncodeToString(h[:6]) + "/"
				p.treeDir, p.osDir = dir, dir
				p.tree = dw.s.w.diskTree(dir, pfx)
				p.tree.sitePrefix = pfx
			}
		}
	}
	if p.tree == nil {
		if _, ok := dw.sum.get(sel.Source, sel.Version.String(), false); !ok && (dw.s.cfg.Offline || dw.s.cfg.Locked) {
			return fail(reasonSumMissing, "%s", sumMissingMsg(dw.s.cfg, sel.Source+" "+sel.Version.Tag()))
		}
		if dw.s.cfg.Offline {
			return fail(fetch.ReasonOffline, "%s %s is not in the cache, and the build is offline", sel.Source, sel.Version.Tag())
		}
		f := dw.fetcherFor()
		n, err := git.LoadNetrc(git.NetrcPath(os.Getenv, runtime.GOOS == "windows"))
		if err != nil {
			return fail("fetch", "%v", err)
		}
		f.Netrc = n
		gt, err := f.GitTag(fetch.SourceURL(sel.Source), sel.Version.Tag())
		if err != nil {
			return fail(reasonOf(err), "%v", err)
		}
		got := sumLine{Source: sel.Source, Version: sel.Version.String(), Commit: gt.Commit, Hash: gt.Hash.String()}
		if reason, old := dw.sum.check(got); reason != "" {
			return fail(reason, "%v", sumFault(reason, got, old))
		}
		if dw.sum.add(got) {
			dw.sumDirt = true
		}
		mem := map[string]string{}
		for _, f := range gt.Tree.Files {
			mem[f.Path] = string(f.Data)
		}
		if _, ok := mem["pkg.mk"]; !ok {
			return fail("invalid", "%s %s holds no pkg.mk at its root", sel.Source, sel.Version.Tag())
		}
		pfx := cid + "@" + hex.EncodeToString(gt.Hash[:6]) + "/"
		p.archive = gt.Tree
		p.tree = &srcTree{mem: mem, prefix: pfx, sitePrefix: pfx}
		if dir := dw.s.storeTree(gt.Tree, gt.Hash); dir != "" {
			p.treeDir, p.osDir = dir, dir
		}
	}
	m, ds := dw.ms.load(p.tree, "pkg.mk", p.name)
	if len(ds) > 0 {
		dw.diags = append(dw.diags, ds...)
		return nil
	}
	p.man = m
	if q := dw.byCID[cid]; q != nil {
		return fail("id-clash", "%s and %s have one canonical id, %s", sel.Source, q.from, cid)
	}
	dw.byID[id], dw.byCID[cid] = p, p
	dw.order = append(dw.order, p)
	return p
}

// fieldOf is the manifest and field path of the first pending Dep on
// source, for a diagnostic's place; the root's deps field otherwise.
func (dw *depWalk) fieldOf(root *Manifest, source string) (*Manifest, string) {
	for _, pd := range dw.pending {
		if pd.d.Source == source && pd.man != nil {
			return pd.man, pd.d.Field
		}
	}
	return root, "deps"
}

// sumFault is the E0202 message of a pkg.sum disagreement.
func sumFault(reason string, got, want sumLine) error {
	if reason == "tag-moved" {
		return &mvsError{reason, fmt.Sprintf("%s %s names commit %s, but pkg.sum records %s", got.Source, "v"+got.Version, got.Commit, want.Commit)}
	}
	return &mvsError{reason, fmt.Sprintf("%s %s has %s, but pkg.sum records %s", got.Source, "v"+got.Version, got.Hash, want.Hash)}
}

// gitError is an internal/git failure as an mvsError with E0202's reason
// (as internal/fetch maps them).
func gitError(err error, prefix string) error {
	var ge *git.Error
	if !errors.As(err, &ge) {
		return &mvsError{"fetch", prefix + err.Error()}
	}
	reason := "fetch"
	switch ge.Reason {
	case git.ReasonProtocol:
		reason = fetch.ReasonProtocol
	case git.ReasonRefused:
		reason = "unsafe-archive"
	case git.ReasonNotFound:
		reason = fetch.ReasonNoVersion
	case git.ReasonInvalid:
		reason = "invalid"
	}
	return &mvsError{reason, prefix + ge.Msg}
}

// reasonOf is err's E0202 reason.
func reasonOf(err error) string {
	var me *mvsError
	if errors.As(err, &me) {
		return me.Reason
	}
	var fe *fetch.Error
	if errors.As(err, &fe) {
		return fe.Reason
	}
	return "fetch"
}
