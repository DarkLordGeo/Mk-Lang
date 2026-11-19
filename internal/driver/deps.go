// Why: a manifest's dependencies must be checked before anything is
// fetched or built, and a few package-wide questions (which modules a bin
// links, whether std.libc joins them) depend on the dependency set.
//
// What: the per-manifest Dep rules (validateDeps, validateDepSource,
// validDepName, sameDep, depFail), moduleFirsts, inTreeDeps, and the link
// sets linkedMods and binMods.
//
// How: each Dep is validated where it is written; every fault is E0202 at
// the Dep. The walk itself (deps_walk.go) then reads vendor/<name> first,
// a path dependency from its directory and a url dependency through
// internal/fetch; a dependency becomes one build (session.go) whose module
// names carry the dependency's name (load.go, graph.go).

package driver

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"mk/internal/diag"
	"mk/internal/fetch"
)

// depFail is E0202 at a Dep of m, with its field path and reason.
func depFail(m *Manifest, field, reason, format string, args ...any) *diag.Diagnostic {
	return diag.New(diag.E0202, m.spanOf(field), fmt.Sprintf(format, args...)).
		Text("manifest", field).Text("reason", reason)
}

// validDepName reports whether n is one import path segment.
func validDepName(n string) bool {
	if n == "" {
		return false
	}
	for i, c := range []byte(n) {
		letter := c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
		if !letter && (i == 0 || c < '0' || c > '9') {
			return false
		}
	}
	return true
}

// validateDeps is the per-manifest half of the Dep rules, for deps and
// test_deps alike: the fields (exactly one of path, url and source; a url
// with a sha256 hash and an https scheme; a source with a version, X.Y.Z;
// a hash only with a url) and the name (one segment, not reserved,
// not main, not the first segment of a module of this package, not used
// twice in the manifest). rootName "" is std's manifest, which takes no
// dependency.
func validateDeps(m *Manifest, t *srcTree, rootName string) []*diag.Diagnostic {
	all := append(slices.Clone(m.Deps), m.TestDeps...)
	if len(all) == 0 {
		return nil
	}
	var out []*diag.Diagnostic
	if rootName == "" {
		return []*diag.Diagnostic{depFail(m, all[0].Field, "invalid", "the standard library takes no dependency")}
	}
	firsts := moduleFirsts(t, m, rootName)
	seen := map[string]string{}
	first := map[string]Dep{}
	for _, d := range all {
		if p, ok := first[d.Name]; ok && sameDep(t, p, d) && strings.HasPrefix(p.Field, "deps[") && strings.HasPrefix(d.Field, "deps[") {
			// A Dep repeated inside deps for one package is accepted
			// (the walk takes the first), as older releases accepted it.
			continue
		}
		f := d.Field
		switch {
		case !validDepName(d.Name):
			out = append(out, depFail(m, f+".name", "invalid", "dependency name %q is not one import path segment", d.Name))
		case slices.Contains(reservedSegments, d.Name):
			out = append(out, depFail(m, f+".name", "invalid",
				"dependency name %s is reserved (std, c and pkg are std's, C import's and the manifest's)", d.Name))
		case d.Name == "main" || d.Name == rootName && rootName != "main":
			out = append(out, depFail(m, f+".name", "invalid", "dependency name %s is the name of this package's root module", d.Name))
		case firsts[d.Name] != "":
			out = append(out, depFail(m, f+".name", "invalid",
				"dependency name %s is the first segment of this package's module in %s; its modules would be imported as %s.<dir>",
				d.Name, firsts[d.Name], d.Name))
		case seen[d.Name] != "":
			out = append(out, depFail(m, f+".name", "invalid", "dependency name %s is used twice, at %s and here; one local name names one package",
				d.Name, seen[d.Name]))
		}
		if seen[d.Name] == "" {
			seen[d.Name], first[d.Name] = f, d
		}
		out = append(out, validateDepSource(m, d, "dependency "+d.Name)...)
	}
	return out
}

// sameDep reports whether a and b, two Deps of the manifest in t, name one
// package: one identity, however the path is spelled (a trailing slash,
// "./", a case difference on Windows), as the older walk compared them.
func sameDep(t *srcTree, a, b Dep) bool {
	switch {
	case a.Name != b.Name:
		return false
	case a.Path != "" && b.Path != "":
		dir := ""
		if t != nil {
			dir = t.osRoot
		}
		return a.URL == "" && b.URL == "" && a.Source == "" && b.Source == "" && a.Hash == "" && b.Hash == "" &&
			pathKey(filepath.Join(dir, filepath.FromSlash(a.Path))) == pathKey(filepath.Join(dir, filepath.FromSlash(b.Path)))
	}
	return a == Dep{Name: b.Name, Path: b.Path, URL: b.URL, Hash: b.Hash, Source: b.Source, Version: b.Version, Field: a.Field}
}

// validateDepSource checks d's source fields: exactly one of path, url and
// source; hash only with url; version exactly with source. what
// names d in messages.
func validateDepSource(m *Manifest, d Dep, what string) []*diag.Diagnostic {
	var out []*diag.Diagnostic
	f := d.Field
	n := 0
	for _, s := range []string{d.Path, d.URL, d.Source} {
		if s != "" {
			n++
		}
	}
	switch {
	case n > 1:
		out = append(out, depFail(m, f, "invalid", "%s sets more than one of path, url and source; set exactly one", what))
	case n == 0:
		out = append(out, depFail(m, f, "invalid", "%s sets none of path, url and source; set exactly one", what))
	case d.URL != "":
		// A url's tree hash is a pkg.sum line; an archive hash written in
		// the Dep (the older form) still reads.
		if _, ok := fetch.ParseHash(d.Hash); d.Hash != "" && !ok {
			out = append(out, depFail(m, f+".hash", "invalid",
				"%s: hash is \"sha256:<64 hex digits>\", the SHA-256 of the archive, or left out for pkg.sum's line", what))
		}
		if d.Hash == "" && checkSumURL(d.URL) != nil {
			out = append(out, depFail(m, f+".url", "invalid",
				"%s: a url with no hash is recorded in pkg.sum, which holds no user information, query or fragment", what))
		}
		if err := fetch.CheckURL(d.URL); err != nil {
			out = append(out, depFail(m, f+".url", "invalid", "%s: %v", what, err))
		}
	case d.Hash != "" && d.Source != "":
		out = append(out, depFail(m, f+".hash", "invalid",
			"%s: a source dependency takes no hash (its tree hash is a pkg.sum line)", what))
	case d.Hash != "":
		out = append(out, depFail(m, f+".hash", "invalid",
			"%s: a path dependency takes no hash (it is read as it is on disk)", what))
	}
	switch {
	case d.Source != "":
		if err := checkSource(d.Source); err != nil {
			out = append(out, depFail(m, f+".source", "invalid", "%s: %v", what, err))
		}
		if d.Version == "" {
			out = append(out, depFail(m, f, "invalid", "%s: a source needs a version, the oldest accepted, X.Y.Z", what))
		} else if _, ok := parseSemver(d.Version); !ok {
			out = append(out, depFail(m, f+".version", "invalid",
				"%s: version %q is not X.Y.Z with an optional pre-release (semantic versioning 2.0)", what, d.Version))
		}
	case d.Version != "":
		out = append(out, depFail(m, f+".version", "invalid", "%s: a version is given only with a source", what))
	}
	return out
}

// moduleFirsts maps the first segment of every module path the package in
// t forms (its walk and its target selection names) to the directory that
// forms it.
func moduleFirsts(t *srcTree, m *Manifest, rootName string) map[string]string {
	firsts := map[string]string{}
	for name, dir := range walkedModules(t, m, rootName) {
		first, _, _ := strings.Cut(name, ".")
		if prev, ok := firsts[first]; !ok || dir < prev {
			firsts[first] = dir
		}
	}
	for _, sel := range m.Targets {
		for _, ms := range sel.Modules {
			first, _, _ := strings.Cut(ms.Name, ".")
			if _, ok := firsts[first]; !ok {
				firsts[first] = ms.Dir
			}
		}
	}
	return firsts
}

// hasManifest reports whether dir holds a pkg.mk file.
func hasManifest(dir string) bool {
	fi, err := os.Stat(filepath.Join(dir, "pkg.mk"))
	return err == nil && fi.Mode().IsRegular()
}

// inTreeDeps are the package-relative directories of m's path
// dependencies that lie inside the package tree: the walk forms no module
// there (their files are the dependency's).
func (m *Manifest) inTreeDeps() []string {
	if m == nil {
		return nil
	}
	var out []string
	for _, d := range m.Deps {
		if d.Path == "" || filepath.IsAbs(d.Path) {
			continue
		}
		if r := relDir(filepath.ToSlash(d.Path)); r != "" && r != ".." && !strings.HasPrefix(r, "../") {
			out = append(out, r)
		}
	}
	return out
}

// linkedMods are the modules whose exported externs a bin links: the
// package's and every dependency's (a dependency's export extern is part
// of the program). A std module's export extern is not, unless the package
// has c_sources or its graph a static link for the target: then
// the C may call it with no Mk caller, so std.libc's and those of every std
// module the package's modules import, directly or not, join them, as
// runtimeRoots do.
func (s *Session) linkedMods() []*modNode {
	var out []*modNode
	for _, b := range s.depBs {
		out = append(out, b.mods...)
	}
	out = append(out, s.pkgB.mods...)
	if s.libcMode() || s.pkgMan == nil || len(s.pkgMan.CSources) == 0 && !s.hasStaticLink() || s.stdB == nil { // no std.libc in system-libc mode
		return out
	}
	seen := map[*modNode]bool{}
	var std []*modNode
	var visit func(m *modNode)
	visit = func(m *modNode) {
		if m == nil || seen[m] {
			return
		}
		seen[m] = true
		if m.std {
			std = append(std, m)
		}
		for _, d := range m.deps {
			visit(d)
		}
	}
	visit(s.stdB.byName[libcModule])
	for _, m := range out {
		for _, d := range m.deps {
			visit(d)
		}
	}
	slices.SortFunc(std, modOrder)
	return append(out, std...)
}

// libcModule is std.libc, the C library functions written in Mk that a
// package's c_sources call.
const libcModule = "std.libc"

// binMods are linkedMods for the bin whose root module is root: a module
// that another bin's root reaches through its imports and this bin's root
// does not is that bin's alone, so its export extern functions are no
// roots here (were every bin to link every root module, two roots
// exporting one symbol would fail the link). A module no bin root reaches
// stays in every bin (an export a C source calls).
func (s *Session) binMods(root *modNode) []*modNode {
	all := s.linkedMods()
	if s.pkgMan == nil || len(s.pkgMan.Bins) < 2 || s.pkgB == nil {
		return all
	}
	reach := func(m *modNode) map[*modNode]bool {
		seen := map[*modNode]bool{}
		var visit func(m *modNode)
		visit = func(m *modNode) {
			if m == nil || seen[m] {
				return
			}
			seen[m] = true
			for _, d := range m.deps {
				visit(d)
			}
		}
		visit(m)
		return seen
	}
	own := reach(root)
	others := map[*modNode]bool{}
	for _, bin := range s.pkgMan.Bins {
		for _, m := range s.pkgB.mods {
			if m.dir == bin.Root && m != root {
				for o := range reach(m) {
					others[o] = true
				}
			}
		}
	}
	pkg := map[*modNode]bool{}
	for _, m := range s.pkgB.mods {
		pkg[m] = true
	}
	out := make([]*modNode, 0, len(all))
	for _, m := range all {
		// Only the package's own modules are filtered: std's and a
		// dependency's stay, as linkedMods gives them.
		if !pkg[m] || own[m] || !others[m] {
			out = append(out, m)
		}
	}
	return out
}
