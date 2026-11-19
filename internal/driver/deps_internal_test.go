// Why: a package's internal modules are its own business, and another
// package must not come to depend on them.
// What: TestDep_InternalModule.
// How: an import of a dependency's internal module is E0214, naming the
// package and the nearest public module; the package's own imports of it
// stay clean.

package driver

import (
	"path/filepath"
	"strings"
	"testing"

	"mk/internal/diag"
)

func TestDep_InternalModule(t *testing.T) {
	ws := writeTree(t, map[string]string{
		"app/pkg.mk":  depManifest("app", pathDep("util", "../util")),
		"app/a.mk":    "import util.src\n\nexport A(): int {\n    return src.Two()\n}\n",
		"util/pkg.mk": depManifest("util"),
		// util's own import of its internal module is unaffected.
		"util/src/u.mk":               "import h = src.internal.help\n\nexport Two(): int {\n    return h.One() + h.One()\n}\n",
		"util/src/internal/help/h.mk": "export One(): int {\n    return 1\n}\n",
	})
	clean(t, depCheck(t, filepath.Join(ws, "app"), Config{}))
	// app's import of it is E0214, naming util and the nearest public
	// module.
	writeFiles(t, ws, map[string]string{"app/b.mk": "import util.src.internal.help\n"})
	r := depCheck(t, filepath.Join(ws, "app"), Config{})
	got := lines(r)
	if len(got) != 1 || got[0] != "b.mk:1:8:E0214" {
		t.Fatalf("got %q\n%s", got, r.Report.Text(r.Sources))
	}
	d := r.Report.Diags[0].Diag
	if d.Code != diag.E0214 || fact(d, "package") != "util" || fact(d, "module") != "util.src.internal.help" ||
		len(d.Alternatives) != 1 || !strings.Contains(d.Alternatives[0], "import util.src,") {
		t.Errorf("%s facts %v alternatives %q", d.Message, d.Facts, d.Alternatives)
	}
	// A package's own internal module, imported from its root module.
	writeFiles(t, ws, map[string]string{"app/b.mk": "import internal.x\n\nexport B(): int {\n    return x.X()\n}\n", "app/internal/x/x.mk": "export X(): int {\n    return 0\n}\n"})
	clean(t, depCheck(t, filepath.Join(ws, "app"), Config{}))
}
