// Why: a package's tests may need packages its users never should link;
// test_deps must type-check with every build yet join the program only
// under mk test.
//
// What: testNamesOf, classifyTests and markTestOnly (which packages are
// test-only), and build.testDepUses (E0217 outside a test block).
//
// How: the root manifest's test_deps are walked like its deps whenever the
// package is checked: they take part in the version choice and pkg.sum
// (deps_mvs.go reads every manifest's test_deps), their grants are checked,
// and their local names bind in the root's modules, so test blocks always
// type-check. A package that only test_deps reach is test-only:
//
//   - only `mk test` (and --prop) links its C sources, assembly and links:
//     any other command sees its manifest without them.
//
// Every package's test_deps are walked, the root's and each dependency's:
// a package's modules import them for their test blocks, and the module
// is checked whole. Each tree records the names its package binds through
// test_deps (srcTree.testNames): an import through one is part of the
// importer's cache key (resolvedImports), so moving a Dep between deps and
// test_deps never replays a stale check, and a use of its names outside a
// test block is E0217, reported by check (internal/check testdeps.go)
// through the hook in stages.go.

package driver

import (
	"maps"
	"slices"
	"strings"

	checker "mk/internal/check"
	"mk/internal/diag"
)

// testNamesOf are the local names man binds through its test_deps.
func testNamesOf(man *Manifest) map[string]bool {
	if man == nil || len(man.TestDeps) == 0 {
		return nil
	}
	out := map[string]bool{}
	for _, d := range man.TestDeps {
		out[d.Name] = true
	}
	return out
}

// classifyTests marks every package of the build that no chain of
// non-test names from the root reaches (testOnly), and, among them, every
// package that the root's own names do not reach either, through each
// package's non-test names: a dependency's own test_deps (depTestOnly).
// Those are resolved, checked and recorded in pkg.sum, so the dependency's
// modules resolve, but the root never grants them and never links them,
// mk test included: the root's tests never run a dependency's (as Go's
// module graph keeps a dependency's test-only modules).
func (dw *depWalk) classifyTests(root *Manifest) {
	reach := func(rootTest bool) map[*depPkg]bool {
		seen := map[*depPkg]bool{}
		var visit func(local map[string]*depPkg, test map[string]bool)
		visit = func(local map[string]*depPkg, test map[string]bool) {
			for _, n := range slices.Sorted(maps.Keys(local)) {
				p := local[n]
				if test[n] || p == nil || seen[p] {
					continue
				}
				seen[p] = true
				visit(p.local, testNamesOf(p.man))
			}
		}
		rt := testNamesOf(root)
		if rootTest {
			rt = nil // the root's test_deps are the root's own
		}
		visit(dw.rootLocal, rt)
		return seen
	}
	plain, rooted := reach(false), reach(true)
	for _, p := range dw.order {
		p.testOnly = !plain[p]
		p.depTestOnly = !rooted[p]
	}
}

// markTestOnly records each package's test names on its tree (the root's
// on pkgT) and marks the test-only trees. A package the root's tests do
// not use is seen without what it links: a test-only package outside mk
// test (testing false), a dependency's own test dependency always. The
// manifest as written stays for the perimeter.
func (dw *depWalk) markTestOnly(root *Manifest, pkgT *srcTree, testing bool) {
	if pkgT != nil {
		pkgT.testNames = testNamesOf(root)
	}
	for _, p := range dw.order {
		if p.tree != nil {
			p.tree.testNames = testNamesOf(p.man)
			p.tree.testOnly = p.testOnly
		}
		if (p.testOnly && !testing || p.depTestOnly) && p.man != nil && p.wholeMan == nil {
			m := *p.man
			m.CSources, m.AsmSources, m.Links = nil, nil, nil
			p.wholeMan, p.man = p.man, &m
		}
	}
}

// testDepUses is E0217 for m: each use, outside a test block, of a name of
// a module that m's file imports through a test_deps name of m's package.
func (b *build) testDepUses(m *modNode) []*diag.Diagnostic {
	if m.std || m.info == nil || m.tree == nil || len(m.tree.testNames) == 0 {
		return nil
	}
	names := m.tree.testNames
	return checker.TestDepUses(m.info, b.w.items, func(p string) bool {
		first, _, _ := strings.Cut(p, ".")
		return names[first]
	})
}
