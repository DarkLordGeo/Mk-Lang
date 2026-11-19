// Why: users read generated code in the gen view, so what std's generators
// emit must be code written to doc/style.md.
// What: TestRwCore_GeneratedCodeIsInTheStandard, over gen derive.Eq, Hash and
// Format (struct and enum forms) and gen dyn.Of.
// How: each view must be a fixed point of mk fmt, and the view pasted into
// the module in place of its gen lines a fixed point of mk fix -canonical
// (with no cache, so the canonical rounds run).

package std

import (
	"strings"
	"testing"
)

const rwCoreDeriveSrc = `import std.derive

struct User {
    name: str
    age: int
}

enum Shape {
    Circle(r: int)
    Rect(w: int, h: int)
    Empty
}

gen derive.Eq(User)
gen derive.Hash(User)
gen derive.Format(User)
gen derive.Eq(Shape)
gen derive.Hash(Shape)
gen derive.Format(Shape)

export Main(sys: &System) {
    _ = sys
}
`

func TestRwCore_GeneratedCodeIsInTheStandard(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ name, src string }{
		{"derive", rwCoreDeriveSrc},
		{"dyn-store", storeIface + "\nexport Main(sys: &System) {\n    _ = sys\n}\n"},
		{"dyn-device", kernelDevice + "\nexport Main(sys: &System) {\n    _ = sys\n}\n"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			out, status, view := dynView(t, c.src)
			if status != 0 {
				t.Fatalf("mk check: exit %d\n%s", status, out)
			}

			// The view alone through mk fmt.
			r := newDynRoot(t, nil)
			r.write(t, "view.mk", view)
			if out, status := r.mk(t, "fmt", "-l", "view.mk"); status != 0 || strings.TrimSpace(out) != "" {
				t.Errorf("mk fmt -l of the gen view: exit %d\n%s\nview:\n%s", status, out, view)
			}

			// The module with the view in place of its gen lines through
			// mk fix -canonical: no round may change it.
			// An import that only the gen lines used goes with them (E0208).
			var keep []string
			for _, l := range strings.Split(c.src, "\n") {
				if !strings.HasPrefix(l, "gen ") {
					keep = append(keep, l)
				}
			}
			body := strings.Join(keep, "\n") + "\n" + view
			keep = keep[:0]
			for _, l := range strings.Split(body, "\n") {
				if mod, ok := strings.CutPrefix(l, "import std."); ok && !strings.Contains(body, mod+".") {
					continue
				}
				keep = append(keep, l)
			}
			p := newDynRoot(t, nil)
			p.write(t, "pkg.mk", rwCoreCanonPkg)
			p.write(t, "app/app.mk", strings.Join(keep, "\n"))
			out, status = p.mk(t, "fix", "-canonical", "-dry-run", "-no-cache", ".")
			if status != 0 || strings.Contains(out, "would write") || !strings.Contains(out, "stopped: clean") {
				t.Errorf("mk fix -canonical over the module with its gen view: exit %d\n%s\nview:\n%s", status, out, view)
			}
		})
	}
}

// rwCoreCanonPkg is dynPkg in mk fix -canonical's form: a std.pkg field
// holding its empty value is left out (the canonical form drops it), so
// the fix run above judges the module alone.
const rwCoreCanonPkg = `const Package = pkg.Package(
    name: "p",
    bins: [pkg.Bin(name: "app", root: "app", subsystem: "console")],
)
`
