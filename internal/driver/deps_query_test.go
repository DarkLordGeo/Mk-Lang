// Why: a query answer is read by people and editors who know a dependency
// by the root's local name, not by its canonical id.
// What: tests of mk query, mk lsp's QueryChecked and the query index over
// packages with dependencies.
// How: the localizeText hook in query.go rewrites canonical ids; the tests
// compare the answers' text.

package driver

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestQuery_DependencyByLocalName(t *testing.T) {
	ws := workspace(t, []string{pathDep("u", "../util")}, map[string]string{
		"util/src/t.mk": "export error Bad\n\nexport F(n: int): Bad!int {\n    if n < 0 {\n        fail Bad\n    }\n    return n\n}\n",
		"app/src/a.mk":  "import u.src\n\nexport A(): src.Bad!int {\n    x := try src.F(1)\n    return x\n}\n",
	})
	s, err := NewSession(Config{Root: filepath.Join(ws, "app"), StdDir: fakeStd(t)})
	if err != nil {
		t.Fatal(err)
	}
	a, res, err := s.Query("errors", []string{"A"})
	if err != nil || a == nil {
		t.Fatalf("%v, %q", err, lines(res))
	}
	// The error set names Bad's module: u.src, as the root imports it.
	if text := a.Text(); strings.Contains(text, utilID) || !strings.Contains(text, "Bad  u.src") {
		t.Errorf("errors A:\n%s", text)
	}
}

// mk lsp's QueryChecked localizes as mk query does.
func TestQuery_CheckedDependencyByLocalName(t *testing.T) {
	ws := workspace(t, []string{pathDep("u", "../util")}, map[string]string{
		"util/src/t.mk": "export error Bad\n\nexport F(n: int): Bad!int {\n    if n < 0 {\n        fail Bad\n    }\n    return n\n}\n",
		"app/src/a.mk":  "import u.src\n\nexport A(): src.Bad!int {\n    x := try src.F(1)\n    return x\n}\n",
	})
	s, err := NewSession(Config{Root: filepath.Join(ws, "app"), StdDir: fakeStd(t)})
	if err != nil {
		t.Fatal(err)
	}
	if r := s.Check(); len(r.Report.Diags) > 0 {
		t.Fatalf("%q", lines(r))
	}
	a, err := s.QueryChecked("errors", []string{"A"})
	if err != nil || a == nil {
		t.Fatalf("%v", err)
	}
	if text := a.Text(); strings.Contains(text, utilID) || !strings.Contains(text, "Bad  u.src") {
		t.Errorf("errors A:\n%s", text)
	}
}

// A dependency's exported items are in the query index, named
// by the root's local name.
func TestQuery_FindsDependencyItems(t *testing.T) {
	ws := workspace(t, []string{pathDep("u", "../util")}, map[string]string{
		"util/src/t.mk": "export Twelve(): int {\n    return 12\n}\n\nHidden(): int {\n    return 1\n}\n",
		"app/src/a.mk":  "import u.src\n\nexport A(): int {\n    return src.Twelve()\n}\n",
	})
	s, err := NewSession(Config{Root: filepath.Join(ws, "app"), StdDir: fakeStd(t)})
	if err != nil {
		t.Fatal(err)
	}
	a, res, err := s.Query("find", []string{"Twelve"})
	if err != nil || a == nil {
		t.Fatalf("%v, %q", err, lines(res))
	}
	if text := a.Text(); !strings.Contains(text, "u.src") || strings.Contains(text, utilID) {
		t.Errorf("find Twelve:\n%s", text)
	}
	if a, _, _ := s.Query("find", []string{"Hidden"}); a != nil && strings.Contains(a.Text(), "u.src") {
		t.Errorf("an unexported dependency item answers:\n%s", a.Text())
	}
}
