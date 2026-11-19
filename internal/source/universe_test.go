// Why: the universe names are reserved in every module, so the interner and
// the reference must list the same ones.
// What: TestUniverseNamesMatchReference.
// How: reads doc/reference.md's "Universe names" list and compares it with
// IsUniverse, in order.

package source

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestUniverseNamesMatchReference: IsUniverse holds exactly the names of
// doc/reference.md's "Universe names" list, in order, and the prelude's
// built-in bounds Plain Send Share Fields are among them. Fields stays a
// universe name, so a documentation example that declares Fields() is wrong,
// not the interner.
func TestUniverseNamesMatchReference(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile(filepath.Join("..", "..", "doc", "reference.md"))
	if err != nil {
		t.Fatal(err)
	}
	ref := strings.ReplaceAll(string(raw), "\r\n", "\n")
	m := regexp.MustCompile("(?m)Universe names: `([^`]*)`").FindStringSubmatch(ref)
	if m == nil {
		t.Fatal("universe line not found in doc/reference.md")
	}
	in := NewInterner()
	var got []string
	for s := Symbol(0); int(s) < NumPredeclared; s++ {
		if IsUniverse(s) {
			got = append(got, in.Str(s))
		}
	}
	if want := strings.Fields(m[1]); strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("IsUniverse = %q, doc/reference.md = %q", got, want)
	}
	b := regexp.MustCompile("built-in bounds `([^`]*)` are universe names").FindStringSubmatch(ref)
	if b == nil {
		t.Fatal("built-in bounds line not found in the reference")
	}
	for _, name := range strings.Fields(b[1]) {
		if !IsUniverse(in.Intern(name)) {
			t.Errorf("built-in bound %q is not a universe name", name)
		}
	}
	if !IsUniverse(SymFieldsBound) || in.Intern("Fields") != SymFieldsBound {
		t.Error("Fields is not the universe symbol SymFieldsBound")
	}
}
