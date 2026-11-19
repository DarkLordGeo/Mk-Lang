// Why: a manifest written for a newer mk may use std.pkg fields this mk
// lacks; the user should read "this needs a newer mk", not an E0305 for
// each unknown field.
//
// What: `pkg.Package.mk`, the oldest mk that builds the package:
// minimumFirst, mkTooOld (E0216 `mk-too-old`), version parsing and
// comparison (parseMkVersion, compareVersions), and manifestNotes.
//
// How: the manifest step reads the field from the parsed manifest before it
// checks anything, so E0216 comes alone. Every manifest of the build passes
// through the step (the root's, each dependency's, each version the walk
// reads), so the build needs the highest minimum of them all.

package driver

import (
	"fmt"
	"strconv"
	"strings"

	"mk/internal/ast"
	"mk/internal/diag"
	"mk/internal/source"
	"mk/internal/syntax"
)

// minimumFirst returns E0216 when a manifest file of b writes a literal
// `mk:` in its `pkg.Package(...)` call that names a newer mk than the
// running one (ms.runningVersion).
// A value that is no version is left to validateManifest.
func (ms *manifestStep) minimumFirst(b *build) []*diag.Diagnostic {
	in := ms.w.in
	for _, m := range b.mods {
		if m.std {
			continue
		}
		for _, f := range m.files {
			if f.ast == nil {
				continue
			}
			for _, it := range f.ast.Items {
				c, ok := it.(*ast.ConstDecl)
				if !ok || c.Name == nil || in.Str(c.Name.Name) != "Package" {
					continue
				}
				call, ok := unparen(c.Value).(*ast.CallExpr)
				if !ok || !isPkgCall(call, "Package", in) {
					continue // only pkg.Package(...)
				}
				for _, a := range call.Args {
					if a == nil || a.Name == nil || in.Str(a.Name.Name) != "mk" {
						continue
					}
					lit, ok := a.Value.(*ast.BasicLit)
					if !ok || lit.Kind != ast.TokStr {
						continue
					}
					// Mk's string rules, not Go's.
					b, ok := syntax.StringValue(lit.Kind, lit.Raw)
					if !ok {
						continue
					}
					want := string(b)
					need, ok := parseMkVersion(want)
					if !ok {
						continue
					}
					have, _ := parseMkVersion(ms.runningVersion())
					if compareVersions(need, have) > 0 {
						return []*diag.Diagnostic{mkTooOld(lit.Span(), want)}
					}
				}
			}
		}
	}
	return nil
}

// isPkgCall reports whether c's callee is `pkg.<name>`.
func isPkgCall(c *ast.CallExpr, name string, in *source.Interner) bool {
	fe, ok := c.Fun.(*ast.FieldExpr)
	if !ok || fe.Name == nil || in.Str(fe.Name.Name) != name {
		return false
	}
	x, ok := fe.X.(*ast.Ident)
	return ok && in.Str(x.Name) == "pkg"
}

// mkTooOld is E0216 at sp for a package that needs mk want. The running
// version is in neither the message nor the facts, so no snapshot changes
// when Release does; `mk version` prints it.
func mkTooOld(sp source.Span, want string) *diag.Diagnostic {
	return diag.New(diag.E0216, sp, fmt.Sprintf("this package needs mk %s or newer, and this mk is older", want)).
		Text("needed", want).
		Alt("run `mk upgrade` to install a newer mk")
}

// runningVersion is the mk version manifests are compared with: Release,
// unless a test set ms.running.
func (ms *manifestStep) runningVersion() string {
	if ms.running != "" {
		return ms.running
	}
	return Release
}

// tooOld is E0216 for a decoded manifest whose mk is newer than the running
// mk: the value the pre-check could not read, a constant or a call.
func (ms *manifestStep) tooOld(m *Manifest) *diag.Diagnostic {
	need, ok := parseMkVersion(m.Mk)
	have, _ := parseMkVersion(ms.runningVersion())
	if !ok || compareVersions(need, have) <= 0 {
		return nil
	}
	return mkTooOld(m.spanOf("mk"), m.Mk)
}

// unparen strips parentheses around e.
func unparen(e ast.Expr) ast.Expr {
	for {
		p, ok := e.(*ast.ParenExpr)
		if !ok {
			return e
		}
		e = p.X
	}
}

// parseMkVersion reads the manifest's `mk` field: "X.Y" or "X.Y.Z", each a
// decimal number with no sign and no leading zero (semantic versioning's
// numeric identifiers). "X.Y" reads as "X.Y.0".
func parseMkVersion(s string) ([3]uint64, bool) {
	var out [3]uint64
	parts := strings.Split(s, ".")
	if len(parts) != 2 && len(parts) != 3 {
		return out, false
	}
	for i, p := range parts {
		n, ok := numericID(p)
		if !ok {
			return out, false
		}
		out[i] = n
	}
	return out, true
}

// numericID reads one semantic versioning numeric identifier.
func numericID(p string) (uint64, bool) {
	if p == "" || len(p) > 1 && p[0] == '0' {
		return 0, false
	}
	for _, c := range []byte(p) {
		if c < '0' || c > '9' {
			return 0, false
		}
	}
	n, err := strconv.ParseUint(p, 10, 64)
	return n, err == nil
}

// compareVersions orders two X.Y.Z triples.
func compareVersions(a, b [3]uint64) int {
	for i := range a {
		switch {
		case a[i] < b[i]:
			return -1
		case a[i] > b[i]:
			return 1
		}
	}
	return 0
}

// manifestNotes are the manifest step's notes of the last check (an
// ignored members or replace field), printed before the other
// notes.
func (s *Session) manifestNotes() []string {
	if s.ms == nil {
		return nil
	}
	return s.ms.notes
}
