// Why: the newest pkg.Package fields (mk, members, replace, unsafe_deps)
// have value rules the type alone cannot state.
//
// What: validateManifestExt: `mk` is X.Y or X.Y.Z; `members` holds relative
// directories inside the root; a `replace` entry names a source and gives
// a path, or another source and version; an `unsafe_deps` entry is a
// source or a local name. Each fault is E0202 with fact `manifest` and
// reason `invalid`.
//
// How: `members` and `replace` are read in the root manifest only: in
// another manifest they are ignored, with a note. A replace entry is a
// pkg.Dep whose `name` holds the source it replaces (the struct has one
// `source` field, so the replaced source takes the name's place):
//
//	replace: [pkg.Dep(name: "github.com/someone/toml", path: "../toml")]
//	replace: [pkg.Dep(name: "github.com/someone/toml", source: "github.com/fork/toml", version: "0.9.4")]

package driver

import (
	"fmt"
	"path"
	"slices"
	"strconv"
	"strings"

	"mk/internal/diag"
	"mk/internal/fetch"
)

// validateManifestExt checks the newest fields of m, the manifest in t; root
// says it is the build's root manifest (rootName "main"). It returns the
// diagnostics and the notes for ignored fields.
func validateManifestExt(m *Manifest, t *srcTree, rootName string) ([]*diag.Diagnostic, []string) {
	var out []*diag.Diagnostic
	var notes []string
	if m.Mk != "" {
		if _, ok := parseMkVersion(m.Mk); !ok {
			out = append(out, depFail(m, "mk", "invalid", "mk %q is not a version X.Y or X.Y.Z", m.Mk))
		}
	}
	for i, u := range m.UnsafeDeps {
		// A source, a url package's url (it has no source, and the grant
		// is by source), or a direct dependency's local name.
		if !validDepName(u) && checkSource(u) != nil && (fetch.CheckURL(u) != nil || checkSumURL(u) != nil) {
			out = append(out, depFail(m, "unsafe_deps["+strconv.Itoa(i)+"]", "invalid",
				"unsafe_deps entry %q is neither a source (host/path), a url package's url, nor a dependency's local name", u))
		}
	}
	root := rootName == "main"
	if !root && (len(m.Members) > 0 || len(m.Replace) > 0) {
		notes = append(notes, fmt.Sprintf("note: %s: members and replace are read in the root manifest only; this package's are ignored (mk why repeats this)",
			t.path("pkg.mk")))
		return out, notes
	}
	seen := map[string]bool{}
	for i, mem := range m.Members {
		f := "members[" + strconv.Itoa(i) + "]"
		c := path.Clean(mem)
		switch {
		case strings.Contains(mem, "\\") || path.IsAbs(mem) || strings.Contains(mem, ":"):
			out = append(out, depFail(m, f, "invalid", "member %q is not a relative directory with / separators", mem))
		case c == "." || c == ".." || strings.HasPrefix(c, "../"):
			out = append(out, depFail(m, f, "invalid", "member %q is not a directory inside the workspace root", mem))
		case seen[c]:
			out = append(out, depFail(m, f, "invalid", "member %q is listed twice", mem))
		case !t.isDir(c):
			out = append(out, depFail(m, f, "invalid", "member directory %s does not exist (as spelled)", mem))
		}
		seen[c] = true
	}
	replaced := map[string]bool{}
	for i, r := range m.Replace {
		f := "replace[" + strconv.Itoa(i) + "]"
		if err := checkSource(r.Name); err != nil {
			out = append(out, depFail(m, f+".name", "invalid", "a replace entry's name is the source it replaces: %v", err))
		} else if replaced[r.Name] {
			// Two entries would take one source to two identities: E0202
			// replace (doc/packages.md).
			out = append(out, depFail(m, f+".name", "replace", "source %s is replaced twice, so it would reach two identities; keep one replace entry for it", r.Name))
		}
		replaced[r.Name] = true
		if r.URL != "" {
			out = append(out, depFail(m, f+".url", "invalid", "a replace entry gives a path, or a source and version, not a url"))
			continue
		}
		out = append(out, validateDepSource(m, r, "replace of "+r.Name)...)
	}
	// A chain of source replacements that comes back to its start would
	// never settle: E0202 replace. A self-pin is no cycle.
	next := map[string]string{}
	for _, r := range m.Replace {
		if r.Source != "" && r.Source != r.Name {
			next[r.Name] = r.Source
		}
	}
	for i, r := range m.Replace {
		chain := []string{r.Name}
		for at := next[r.Name]; at != ""; at = next[at] {
			chain = append(chain, at)
			if at == r.Name {
				if slices.Min(chain) != r.Name {
					break // one report per cycle, at its least source's entry
				}
				out = append(out, depFail(m, "replace["+strconv.Itoa(i)+"]", "replace",
					"replace entries form a cycle: %s; a source is replaced once, by a path or by another source", strings.Join(chain, " -> ")))
				break
			}
			if len(chain) > len(m.Replace) {
				break // a cycle not through r: reported at its own entries
			}
		}
	}
	return out, notes
}
