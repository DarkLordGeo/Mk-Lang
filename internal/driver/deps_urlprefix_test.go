// Why: a cache key or site path that depends on a local name would differ
// between packages that use the same dependency.
// What: TestDep_URLPrefixIndependentOfLocalName.
// How: a url package's files are cited under `<canonical id>@<12 hex>/`,
// whatever a manifest calls it.

package driver

import (
	"slices"
	"strings"
	"testing"
)

func TestDep_URLPrefixIndependentOfLocalName(t *testing.T) {
	arch := tarGzOf(t, "", iniFiles())
	srv, _ := archiveServer(t, map[string][]byte{"/ini.tar.gz": arch})
	url := srv.URL + "/ini.tar.gz"
	want := urlCID(url) + "@" + sha(arch)[7:19] + "/src/parse/p.mk"
	for _, local := range []string{"ini", "conf"} {
		app := writeTree(t, map[string]string{
			"pkg.mk":   depManifest("app", urlDep(local, url, sha(arch))),
			"src/a.mk": "import " + local + ".src\n\nexport A(): int {\n    return src.Keys()\n}\n",
		})
		r := depCheck(t, app, Config{})
		clean(t, r)
		var paths []string
		for _, f := range r.Sources.Files() {
			paths = append(paths, f.Path)
		}
		if !slices.Contains(paths, want) {
			t.Errorf("local name %s: no %s among %v", local, want, paths)
		}
		for _, p := range paths {
			if strings.HasPrefix(p, local+"@") {
				t.Errorf("local name %s: a file is cited under it: %s", local, p)
			}
		}
	}
}
