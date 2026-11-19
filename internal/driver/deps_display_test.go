// Why: a diagnostic in a dependency must name it as the reader knows it,
// and the same way on every run.
// What: tests of display names: deterministic, per holding package, and
// rewriting whole canonical ids only.
// How: checks packages whose dependencies have several local names and
// compares the diagnostic text; text the user wrote is never rewritten.

package driver

import (
	"path/filepath"
	"strings"
	"testing"
)

// A package two root names call is shown by one of them, every run.
func TestDep_DisplayNameDeterministic(t *testing.T) {
	ws := writeTree(t, map[string]string{
		"app/pkg.mk":    depManifest("app", pathDep("u", "../util"), pathDep("v", "../util")),
		"app/a.mk":      "import u.src\n\nexport A(): int {\n    return src.Hidden()\n}\n",
		"util/pkg.mk":   depManifest("util"),
		"util/src/u.mk": "Hidden(): int {\n    return 2\n}\n",
	})
	var first string
	for i := range 8 {
		r := depCheck(t, filepath.Join(ws, "app"), Config{})
		if len(r.Report.Diags) != 1 {
			t.Fatalf("got %q", lines(r))
		}
		text := r.Report.Text(r.Sources)
		if i == 0 {
			first = text
			if !strings.Contains(text, "u.src") || strings.Contains(text, utilID) {
				t.Errorf("run 0:\n%s", text)
			}
		} else if text != first {
			t.Fatalf("run %d differs:\n%s\nfirst:\n%s", i, text, first)
		}
	}
}

// Inside package other, which calls util helper, a diagnostic says
// helper.src, not the root's name for it.
func TestDep_DisplayNamePerHolder(t *testing.T) {
	ws := writeTree(t, map[string]string{
		"app/pkg.mk":    depManifest("app", pathDep("u", "../util"), pathDep("other", "../other")),
		"app/a.mk":      "import u.src\nimport other\n\nexport A(): int {\n    return src.Two() + other.Four()\n}\n",
		"other/pkg.mk":  depManifest("other", pathDep("helper", "../util")),
		"other/o.mk":    "import helper.src\n\nexport Four(): int {\n    return src.Hidden() * 2\n}\n",
		"util/pkg.mk":   depManifest("util"),
		"util/src/u.mk": "export Two(): int {\n    return 2\n}\n\nHidden(): int {\n    return 2\n}\n",
	})
	r := depCheck(t, filepath.Join(ws, "app"), Config{})
	if len(r.Report.Diags) != 1 {
		t.Fatalf("got %q", lines(r))
	}
	text := r.Report.Text(r.Sources)
	if !strings.Contains(text, "helper.src") || strings.Contains(text, "u.src") || strings.Contains(text, utilID) {
		t.Errorf("in other's file:\n%s", text)
	}
}

// Only whole ids are rewritten, and a name or an import the user wrote is
// echoed as written.
func TestDep_LocalizeWholeIDsOnly(t *testing.T) {
	ws := workspace(t, []string{pathDep("u", "../util")}, map[string]string{
		"app/src/a.mk": "import u.src\n\nexport A(): int {\n    return src.Two() + x" + utilID + "\n}\n",
	})
	r := depCheck(t, filepath.Join(ws, "app"), Config{})
	if len(r.Report.Diags) != 1 {
		t.Fatalf("got %q", lines(r))
	}
	d := r.Report.Diags[0].Diag
	if !strings.Contains(d.Message, "x"+utilID) || fact(d, "name") != "x"+utilID {
		t.Errorf("an identifier holding the id was rewritten: %s %v", d.Message, d.Facts)
	}
	writeFiles(t, ws, map[string]string{"app/src/a.mk": "import " + utilID + ".src\n"})
	r = depCheck(t, filepath.Join(ws, "app"), Config{})
	if len(r.Report.Diags) != 1 {
		t.Fatalf("got %q", lines(r))
	}
	d = r.Report.Diags[0].Diag
	if !strings.Contains(d.Message, utilID+".src") || fact(d, "module") != utilID+".src" {
		t.Errorf("an import as written was rewritten: %s %v", d.Message, d.Facts)
	}
	if got := localizeText("a "+utilID+".src b x"+utilID+" "+utilID, map[string]string{utilID: "u"}); got != "a u.src b x"+utilID+" u" {
		t.Errorf("localizeText: %q", got)
	}
}
