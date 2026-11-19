// Why: a note from the manifest step is as useful on mk build as on mk
// check.
// What: TestDep_ManifestNotesOnBuild.
// How: builds a package whose manifest draws a note and looks for it.

package driver

import (
	"path/filepath"
	"strings"
	"testing"

	"mk/internal/types"
)

func TestDep_ManifestNotesOnBuild(t *testing.T) {
	tools := needTools(t)
	ws := writeTree(t, map[string]string{
		"app/pkg.mk": manifest(map[string]string{"name": `"app"`,
			"bins": `[pkg.Bin(name: "app", root: "src", link_script: "", cpu: "", subsystem: "console")]`,
			"deps": "[" + pathDep("util", "../util") + "]"}),
		"app/src/main.mk": "import util\n\nexport Main(sys: &System): int {\n    return util.One()\n}\n",
		"util/pkg.mk":     "const Package = pkg.Package(name: \"util\", members: [\"sub\"])\n",
		"util/u.mk":       "export One(): int {\n    return 0\n}\n",
		"util/sub/s.mk":   "export S(): int {\n    return 1\n}\n",
	})
	s, err := NewSession(Config{Root: filepath.Join(ws, "app"), Tools: tools, CacheDir: t.TempDir(), Target: types.X86_64Windows})
	if err != nil {
		t.Fatal(err)
	}
	r, paths, err := s.Build(filepath.Join(t.TempDir(), "app.exe"))
	if err != nil || len(r.Report.Diags) > 0 || len(paths) != 1 {
		t.Fatalf("build %v\n%s", err, r.Report.Text(r.Sources))
	}
	if len(r.Notes) != 1 || !strings.Contains(r.Notes[0], "read in the root manifest only") {
		t.Errorf("notes %q", r.Notes)
	}
}
