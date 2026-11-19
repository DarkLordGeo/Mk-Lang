// Why: a package needs modules that other packages cannot import, so it
// can change them freely.
//
// What: build.internalImport, which reports E0214 `internal-module`.
//
// How: a module whose import path has a segment `internal` is importable
// only from modules of the package that holds it. Another package's import
// of it, a dependency's or a workspace member's (members are different
// packages), is E0214, naming the package and the nearest public module.
// A package's own imports of its internal modules are unaffected, and std
// keeps its own rule for std-internal modules.

package driver

import (
	"fmt"
	"slices"
	"strings"

	"mk/internal/diag"
	"mk/internal/resolve"
)

// internalImport is E0214 for the import line il of m, which resolved to
// dep, when dep is an internal module of another package; else nil.
func (b *build) internalImport(m, dep *modNode, il resolve.ImportLine) *diag.Diagnostic {
	if dep == nil || dep.std || m.std || slices.Contains(b.mods, dep) {
		return nil
	}
	segs := strings.Split(il.Path, ".")
	at := slices.Index(segs[1:], "internal")
	if at < 0 {
		return nil
	}
	at++ // index in segs
	pkg := segs[0]
	// Only a module of another package: a dependency (or, in a
	// workspace, a member), never a C import's binding (`c.internal`).
	if db := b.depBuilds[pkg]; db == nil || !slices.Contains(db.mods, dep) {
		return nil
	}
	d := diag.New(diag.E0214, il.PathSpan,
		fmt.Sprintf("module %s is internal to package %s; only that package's own modules may import it", il.Path, pkg)).
		Text("module", il.Path).Text("package", pkg)
	if db := b.depBuilds[pkg]; db != nil {
		// The nearest public module: the path cut before its first
		// internal segment, then shorter, while no module has it.
		for k := at; k >= 1; k-- {
			p := strings.Join(segs[:k], ".")
			if db.byName[depModule(db, p)] != nil {
				d.Alt("import " + p + ", the nearest public module")
				break
			}
		}
	}
	return d
}
