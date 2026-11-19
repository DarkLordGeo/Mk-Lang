// Why: `.Or(x)` on ?T and E!T is removed, and the documentation must say what
// internal/check emits for it.
// What: TestOrIsE0207InHabitsAndCatalogue.
// How: reads doc/reference.md's habit table and doc/errors.md's E0207 row and
// looks for the code and the rewrite `e else { x }`.

package std

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// `.Or(x)` on a ?T or E!T is E0207 with the fix `e else { x }`
// (internal/check/orremoved.go). The reference's habit table holds
// a `.Or(x)` row with that code and that rewrite, and the catalogue's
// E0207 row names the removed method.
func TestOrIsE0207InHabitsAndCatalogue(t *testing.T) {
	read := func(parts ...string) string {
		b, err := os.ReadFile(filepath.Join(parts...))
		if err != nil {
			t.Fatal(err)
		}
		return strings.ReplaceAll(string(b), "\r\n", "\n")
	}
	chk := read("..", "internal", "check", "orremoved.go")
	if !strings.Contains(chk, "diag.E0207") || !strings.Contains(chk, "have no `Or`") {
		t.Fatal("internal/check/orremoved.go no longer reports Or as E0207: update this test with the design")
	}

	ref := read("..", "doc", "reference.md")
	s := strings.Index(ref, "\n## 16. ")
	e := strings.Index(ref, "\n## 17. ")
	if s < 0 || e < s {
		t.Fatal("reference: no habits table")
	}
	var row string
	for _, line := range strings.Split(ref[s:e], "\n") {
		if strings.HasPrefix(line, "| ") && strings.Contains(line, "`.Or(x)`") {
			row = line
		}
	}
	cells := strings.Split(row, "|")
	if len(cells) != 5 || strings.TrimSpace(cells[3]) != "E0207" || !strings.Contains(cells[2], "`e else { x }`") {
		t.Errorf("the habits table has no `.Or(x)` row written `e else { x }` with code E0207: %q", row)
	}

	cat := read("..", "doc", "errors.md")
	var e0207 string
	for _, line := range strings.Split(cat, "\n") {
		if strings.HasPrefix(line, "| E0207 |") {
			e0207 = line
		}
	}
	if !strings.Contains(e0207, "`.Or(x)`") || !strings.Contains(e0207, "`e else { x }`") {
		t.Errorf("the catalogue's E0207 row does not name the removed `.Or(x)` and its fix: %q", e0207)
	}
}
