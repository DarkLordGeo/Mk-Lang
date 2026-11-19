// Why: a corpus unit may take dependencies, so the codes E0214, E0215 and
// E0217 can have units of their own under tests/conformance/codes/.
//
// What: CorpusSession.unitDeps, the dependency walk of one unit, and
// hasMem.
//
// How: a unit is held in memory, so the walk is the dependency walk's
// in-memory subset:
//
//   - a path Dep names a directory inside the unit, relative to the
//     manifest that writes it (a directory holding its own pkg.mk is
//     another package, never a module of the unit);
//   - a url Dep is read from the unit's vendor/<name>/ in the vendor layout,
//     offline: it stays a fetched package, so it needs its grant (E0215);
//   - a source Dep, and a url Dep with no vendored copy, is E0202 invalid:
//     a unit fetches nothing.
//
// test_deps bind like deps, and the root's grants and E0217
// follow the build's rules (deps_grants.go, deps_testdeps.go). Each
// dependency's modules take module paths under the unit's name, so two
// units with the same layout never share one.

package driver

import (
	"cmp"
	"path"
	"slices"
	"strings"

	"mk/internal/diag"
)

// unitDeps walks the unit root's dependencies (t is the unit's tree, man
// its manifest), checks the grants, and returns one build per dependency
// with b.depBuilds bound; or the diagnostics of the first faults.
func (c *CorpusSession) unitDeps(u UnitInput, t *srcTree, man *Manifest, b *build) ([]*build, []*diag.Diagnostic) {
	dw := &depWalk{byID: map[string]*depPkg{}}
	var walk func(m *Manifest, dir string, fetched bool, parent *depPkg) map[string]*depPkg
	walk = func(m *Manifest, dir string, fetched bool, parent *depPkg) map[string]*depPkg {
		local := map[string]*depPkg{}
		for _, d := range append(slices.Clone(m.Deps), m.TestDeps...) {
			var sub, id string
			switch {
			case d.Path != "" && !fetched:
				sub = path.Clean(path.Join(dir, d.Path))
				id = "path " + sub
			case d.URL != "":
				sub = "vendor/" + d.Name
				id = "url " + d.URL
			}
			if sub == "" || sub == "." || strings.HasPrefix(sub, "../") || !hasMem(t, sub+"/pkg.mk") {
				dw.diags = append(dw.diags, depFail(m, d.Field, "invalid",
					"dependency %s: a corpus unit takes a path dependency inside the unit, or a url dependency vendored in its vendor/%s/", d.Name, d.Name))
				continue
			}
			if p := dw.byID[id]; p != nil {
				if !p.done {
					// As the build's walk (deps_walk.go).
					dw.diags = append(dw.diags, depFail(m, d.Field, "cycle", "dependency cycle through %s", d.Name))
					continue
				}
				local[d.Name] = p
				if !slices.Contains(p.names, d.Name) {
					p.names = append(p.names, d.Name)
				}
				continue
			}
			p := &depPkg{name: d.Name, names: []string{d.Name}, identity: id, from: cmp.Or(d.Path, d.URL),
				parent: parent, viaMan: m, via: d.Field, vendored: d.URL != ""}
			if d.Path != "" {
				p.rel = sub
			}
			p.tree = &srcTree{prefix: u.Name + "/" + sub + "/", mem: map[string]string{}}
			for f, text := range t.mem {
				if rest, ok := strings.CutPrefix(f, sub+"/"); ok {
					p.tree.mem[rest] = text
				}
			}
			pm, ds := c.ms.load(p.tree, "pkg.mk", d.Name)
			if len(ds) > 0 {
				dw.diags = append(dw.diags, ds...)
				continue
			}
			p.man = pm
			p.cid = cidOf(p)
			dw.byID[id] = p
			local[d.Name] = p
			p.local = walk(pm, sub, p.fetched(), p)
			p.done = true
			dw.order = append(dw.order, p)
		}
		return local
	}
	dw.rootLocal = walk(man, "", false, nil)
	if len(dw.diags) > 0 {
		return nil, dw.diags
	}
	dw.classifyTests(man)
	dw.checkGrants(man, dw.grantSet(man))
	if len(dw.diags) > 0 {
		return nil, dw.diags
	}
	dw.markTestOnly(man, t, false)
	byPkg := map[*depPkg]*build{}
	var out []*build
	for _, dp := range dw.order {
		db := newBuild(c.w, c.std)
		db.unit = true
		db.prefix = dp.cid + "."
		if err := db.listTree(dp.tree, dp.man, dp.cid); err != nil {
			return nil, []*diag.Diagnostic{listFailure(dp.tree, err)}
		}
		for _, m := range db.mods {
			m.path = unitModulePath(u.Name, path.Join(strings.TrimSuffix(dp.tree.prefix[len(u.Name)+1:], "/"), m.dir))
		}
		db.permitUnsafe(dp.man)
		db.findCaseDups()
		db.depBuilds = depsOf(dp.local, byPkg)
		byPkg[dp] = db
		out = append(out, db)
	}
	b.depBuilds = depsOf(dw.rootLocal, byPkg)
	return out, nil
}

// hasMem reports whether the memory tree holds rel.
func hasMem(t *srcTree, rel string) bool {
	_, ok := t.mem[rel]
	return ok
}
