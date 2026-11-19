// Why: the manifest decides which directories are modules, so it must be
// validated against the tree before any other file is listed.
//
// What: validateManifest and validateTargets, with the walk helpers they
// use (inNestedPackage, walkedModules, walkSkipped, selectedDirs,
// cleanDirs, relDir).
//
// How: a decoded manifest (manifest.go) is checked against the source
// tree: bin roots are module directories, unsafe module directories exist,
// target selections are well formed and form no duplicate module. A
// missing std/pkg.mk is an empty std manifest.

package driver

import (
	"cmp"
	"fmt"
	"path"
	"slices"
	"strconv"
	"strings"

	"mk/internal/diag"
)

// validateManifest checks a decoded manifest: bin roots are module directories,
// unsafe module directories exist, and target selections are well formed.
// rootName is the module the package root's own files form ("main", or a corpus
// unit's name); "" for std's manifest, whose layout is std's own, so the checks
// against the package walk are skipped (the import step's duplicate-path check
// still covers std).
func validateManifest(m *Manifest, t *srcTree, rootName string) []*diag.Diagnostic {
	var out []*diag.Diagnostic
	fail := func(field, format string, args ...any) {
		out = append(out, diag.New(diag.E0202, m.spanOf(field), fmt.Sprintf(format, args...)).
			Text("manifest", field).Text("reason", "invalid"))
	}
	pkg := rootName != ""
	selected := m.walkSkipped()
	for k, b := range m.Bins {
		f := m.binField(k) + ".root"
		// isDir first: the directory must be in the listing as spelled, so
		// mkFiles never walks a path that only a case-folding file system
		// resolves.
		var files []string
		var err error
		listed := t.isDir(b.Root)
		if listed {
			files, err = t.mkFiles(b.Root, true, nil)
		}
		if b.Root == "" {
			files = slices.DeleteFunc(files, func(f string) bool { return f == "pkg.mk" })
		}
		switch {
		case !listed || err != nil || len(files) == 0:
			fail(f, "bin %s: root %s is not a module directory", b.Name, cmp.Or(b.Root, "."))
		case pkg && (outsideWalk(b.Root, selected) || reservedDir(b.Root)):
			// The walk forms no module of this directory, so the
			// bin would never be checked for its Main.
			fail(f, "bin %s: root %s is not a module directory: the module walk skips it "+
				"(hidden, gen, reserved or target-selected)", b.Name, b.Root)
		case pkg && inNestedPackage(t, b.Root):
			// A directory under one holding its own pkg.mk is
			// another package's, so the walk forms no module of it either.
			fail(f, "bin %s: root %s is not a module directory: the module walk skips it "+
				"(it is inside another package, a directory with its own pkg.mk)", b.Name, b.Root)
		}
	}
	for i, u := range m.UnsafeModules {
		if !t.isDir(u) {
			fail("unsafe_modules["+strconv.Itoa(i)+"]", "unsafe module directory %s does not exist (as spelled)", u)
		}
	}
	out = append(out, validateTargets(m, t, rootName)...)
	out = append(out, validateDeps(m, t, rootName)...)
	out = append(out, validateC(m, t)...)
	out = append(out, validateLinks(m, t)...)
	return out
}

// inNestedPackage reports whether dir, or a directory above it short of the
// package root, holds its own pkg.mk: packageDirs forms no module there.
func inNestedPackage(t *srcTree, dir string) bool {
	for d := dir; d != ""; d = dirOf(d) {
		files, err := t.mkFiles(d, true, nil)
		if err == nil && slices.Contains(files, d+"/pkg.mk") {
			return true
		}
	}
	return false
}

// validateTargets checks the target selections: each OS selected once;
// within one selection, names and directories unique; every selection
// mapping the same names (a name missing for some OS is reported at the
// pkg.Module that has it); every selected directory present and
// not the package root. In a package (rootName != ""), a name must not be
// reserved or equal the import path of a module the walk forms,
// and a directory must not be reserved; either would form two
// modules with one path, or one file under two modules.
func validateTargets(m *Manifest, t *srcTree, rootName string) []*diag.Diagnostic {
	var out []*diag.Diagnostic
	fail := func(field, format string, args ...any) {
		out = append(out, diag.New(diag.E0202, m.spanOf(field), fmt.Sprintf(format, args...)).
			Text("manifest", field).Text("reason", "invalid"))
	}
	if len(m.Targets) == 0 {
		return nil
	}
	pkg := rootName != ""
	var walked map[string]string // import path to directory, of the modules the walk forms
	if pkg {
		walked = walkedModules(t, m, rootName)
	}
	sel := func(i int) string { return "targets[" + strconv.Itoa(i) + "]" }
	var oses, collided []string
	names := make([][]string, len(m.Targets))
	for i, ts := range m.Targets {
		if slices.Contains(oses, ts.OS) {
			fail(sel(i)+".os", "target os %s is selected twice", ts.OS)
		}
		oses = append(oses, ts.OS)
		var dirs []string
		for j, ms := range ts.Modules {
			f := sel(i) + ".modules[" + strconv.Itoa(j) + "]"
			switch {
			case slices.Contains(names[i], ms.Name):
				fail(f+".name", "module name %s is selected twice for %s", ms.Name, ts.OS)
			case slices.Contains(collided, ms.Name):
				// A bad name is reported once, at its first selection:
				// every OS maps it.
			case pkg && reservedImport(ms.Name):
				collided = append(collided, ms.Name)
				fail(f+".name", "module name %s begins with a reserved segment (std, c and pkg are std's, "+
					"C import's and the manifest's)", ms.Name)
			case walked[ms.Name] != "":
				collided = append(collided, ms.Name)
				fail(f+".name", "module name %s is selected for %s, but directory %s already forms module %s",
					ms.Name, ts.OS, walked[ms.Name], ms.Name)
			}
			names[i] = append(names[i], ms.Name)
			switch {
			case ms.Dir == "":
				fail(f+".dir", "the package root cannot be a selected directory")
			case slices.Contains(dirs, ms.Dir):
				fail(f+".dir", "directory %s is selected twice for %s", ms.Dir, ts.OS)
			case !t.isDir(ms.Dir):
				fail(f+".dir", "selected directory %s does not exist (as spelled)", ms.Dir)
			case pkg && reservedDir(ms.Dir):
				fail(f+".dir", "directory %s is reserved: its import path begins with std, c or pkg", ms.Dir)
			}
			dirs = append(dirs, ms.Dir)
		}
	}
	// Every selection maps the same names: a name some selection lacks is
	// reported once, at its first pkg.Module.
	var reported []string
	for i, ts := range m.Targets {
		for j, ms := range ts.Modules {
			if slices.Contains(reported, ms.Name) {
				continue
			}
			var lacking []string
			for k, other := range m.Targets {
				if k != i && !slices.Contains(names[k], ms.Name) && !slices.Contains(lacking, other.OS) {
					lacking = append(lacking, other.OS)
				}
			}
			if len(lacking) > 0 {
				reported = append(reported, ms.Name)
				fail(sel(i)+".modules["+strconv.Itoa(j)+"]", "module name %s is selected for %s but not for %s; "+
					"every target must map the same names", ms.Name, ts.OS, strings.Join(lacking, ", "))
			}
		}
	}
	return out
}

// walkedModules maps the import path of every module the package walk
// forms from the package's own directories (target selections and reserved
// directories excluded) to its directory, "." for the root. A listing
// failure gives what was listed; the load reports the failure.
func walkedModules(t *srcTree, m *Manifest, rootName string) map[string]string {
	dirs, _, _ := packageDirs(t, m)
	out := map[string]string{}
	for _, d := range dirs {
		if reservedDir(d) {
			continue
		}
		name := dirToImport(d)
		if d == "" {
			name = rootName
		}
		if _, ok := out[name]; !ok {
			out[name] = cmp.Or(d, ".")
		}
	}
	return out
}

// walkSkipped is every package directory the walk forms no module in, with
// everything under it: the target-selected directories, vendor and
// the in-tree path dependencies.
func (m *Manifest) walkSkipped() []string {
	if m == nil {
		return nil
	}
	out := append(m.selectedDirs(), "vendor")
	return append(out, m.inTreeDeps()...)
}

// selectedDirs is every directory any target selection names.
func (m *Manifest) selectedDirs() []string {
	if m == nil {
		return nil
	}
	var out []string
	for _, sel := range m.Targets {
		for _, ms := range sel.Modules {
			out = append(out, ms.Dir)
		}
	}
	return out
}

// cleanDirs puts every directory of the manifest into the tree's rel form
// before validation: "/" separators, no "." or empty element, and the
// package root itself as "". So `root: "."` names the package root's module
// (tests/bench writes it so) and `"src/"` is "src".
func (m *Manifest) cleanDirs() {
	for i := range m.Bins {
		m.Bins[i].Root = relDir(m.Bins[i].Root)
	}
	for i := range m.UnsafeModules {
		m.UnsafeModules[i] = relDir(m.UnsafeModules[i])
	}
	for i := range m.Targets {
		for j := range m.Targets[i].Modules {
			m.Targets[i].Modules[j].Dir = relDir(m.Targets[i].Modules[j].Dir)
		}
	}
}

// relDir is one manifest directory in rel form. A path that leaves the tree
// ("../x", "/x") stays as written, and validation refuses it.
func relDir(s string) string {
	if c := path.Clean(s); c != "." {
		return c
	}
	return ""
}
