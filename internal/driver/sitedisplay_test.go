// Why: a fetched dependency's files live under a canonical id, but users
// know it by its local name; panics, errors and traces must print that name.
// What: TestPanicDisplay_SitePath and TestPanicDisplay_DependencyByLocalName.
// How: calls displaySitePath on fixed paths, then builds and runs a program
// with a fetched dependency and reads stderr and the binary's site strings.

package driver

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"mk/internal/types"
)

// TestPanicDisplay_SitePath: the canonical id of a fetched or vendored
// dependency's site path becomes its local name; other paths are kept.
func TestPanicDisplay_SitePath(t *testing.T) {
	names := map[string]string{"lib_dbf98d23": "lib", "own_00000000": ""}
	for _, c := range [][2]string{
		{"lib_dbf98d23@0123456789ab/src/a.mk", "lib@0123456789ab/src/a.mk"},
		{"vendor/lib_dbf98d23/src/a.mk", "vendor/lib/src/a.mk"},
		{"../lib/lib.mk", "../lib/lib.mk"},
		{"src/main.mk", "src/main.mk"},
		{"std/core/core.mk", "std/core/core.mk"},
		{"other_11111111@ab/x.mk", "other_11111111@ab/x.mk"},
		{"own_00000000@ab/x.mk", "own_00000000@ab/x.mk"},
	} {
		if got := displaySitePath(c[0], names); got != c[1] {
			t.Errorf("%s: got %s, want %s", c[0], got, c[1])
		}
	}
	if got := displaySitePath("lib_dbf98d23@ab/x.mk", nil); got != "lib_dbf98d23@ab/x.mk" {
		t.Errorf("nil names changed the path: %s", got)
	}
}

// TestPanicDisplay_DependencyByLocalName: a dependency's error that
// escapes Main prints by the local name (error: lib.Bad(code: 7), not
// lib_<hex>.Bad), and a panic inside a fetched dependency names its file
// under <local name>@<hash>/.
func TestPanicDisplay_DependencyByLocalName(t *testing.T) {
	tools := needTools(t)
	target := types.X86_64Windows
	if runtime.GOOS != "windows" {
		target = types.X86_64Linux
	}
	lib := map[string]string{
		"pkg.mk": depManifest("lib"),
		"lib.mk": "export error Bad(code: i64)\n\nexport Fail(): Bad!i64 {\n    fail Bad(code: 7)\n}\n\n" +
			"export Boom(n: i64): i64 {\n    if n > 0 {\n        Panic(\"boom from lib\")\n    }\n    return n\n}\n",
	}
	arch := tarGzOf(t, "lib-1.0", lib)
	srv, _ := archiveServer(t, map[string][]byte{"/lib.tar.gz": arch})
	bin := `[pkg.Bin(name: "app", root: ".", link_script: "", cpu: "", subsystem: "console")]`
	ws := writeTree(t, map[string]string{
		"lib/pkg.mk":  lib["pkg.mk"],
		"lib/lib.mk":  lib["lib.mk"],
		"err/pkg.mk":  manifest(map[string]string{"name": `"app"`, "bins": bin, "deps": "[" + pathDep("lib", "../lib") + "]"}),
		"err/main.mk": "import lib\n\nexport Main(sys: &System): lib.Bad!void {\n    v := try lib.Fail()\n    _ = v\n}\n",
		"pan/pkg.mk": manifest(map[string]string{"name": `"app"`, "bins": bin,
			"deps": "[" + urlDep("lib", srv.URL+"/lib.tar.gz", sha(arch)) + "]"}),
		"pan/main.mk": "import lib\n\nexport Main(sys: &System): void {\n    _ = lib.Boom(3)\n}\n",
		"tr/pkg.mk": manifest(map[string]string{"name": `"app"`, "bins": bin,
			"deps": "[" + urlDep("lib", srv.URL+"/lib.tar.gz", sha(arch)) + "]"}),
		"tr/main.mk": "import lib\n\nexport Main(sys: &System): lib.Bad!void {\n    v := try lib.Fail()\n    _ = v\n}\n",
	})
	var exe []byte
	var last *Session
	run := func(dir string) (string, int) {
		s, err := NewSession(Config{Root: filepath.Join(ws, dir), Target: target, Tools: tools, CacheDir: t.TempDir()})
		if err != nil {
			t.Fatal(err)
		}
		r, paths, err := s.Build(filepath.Join(t.TempDir(), "app"+exeSuffix(target)))
		if err != nil {
			t.Fatal(err)
		}
		if len(r.Report.Diags) > 0 || len(paths) != 1 {
			t.Fatalf("build %s: %d paths\n%s", dir, len(paths), r.Report.Text(r.Sources))
		}
		last = s
		if exe, err = os.ReadFile(paths[0]); err != nil {
			t.Fatal(err)
		}
		_, errOut, code := runExe(t, target, paths[0], "")
		return errOut, code
	}
	const escaped = "error: lib.Bad(code: 7)\n    at ../lib/lib.mk:4:5\n    at main.mk:4:10"
	if errOut, code := run("err"); code != 1 || strings.TrimSpace(strings.ReplaceAll(errOut, "\r\n", "\n")) != escaped {
		t.Errorf("escaped error: exit %d, stderr %q; want 1 and %q", code, errOut, escaped)
	}
	want := "panic: boom from lib at lib@" + sha(arch)[7:19] + "/lib.mk:9:9"
	if errOut, code := run("pan"); code != 101 || !strings.HasPrefix(strings.TrimSpace(errOut), want) {
		t.Errorf("panic: exit %d, stderr %q; want 101 and %s", code, errOut, want)
	}
	// The error-return trace's sites (lower's TraceFail and
	// TraceTry, w.traceSitePath) name a fetched dependency's file by its
	// local name too, never its canonical id: in the trace Main writes
	// (with no task, from the RtState page's header) and in the binary's
	// site strings, where none names lib's canonical id.
	trace := "    at lib@" + sha(arch)[7:19] + "/lib.mk:4:5\n"
	if errOut, code := run("tr"); code != 1 || !strings.Contains(strings.ReplaceAll(errOut, "\r\n", "\n"), trace) {
		t.Fatalf("trace: exit %d, stderr %q; want 1 and %q", code, errOut, trace)
	}
	cid := ""
	for _, dp := range last.deps {
		if dp.name == "lib" {
			cid = dp.cid
		}
	}
	if cid == "" || cid == "lib" {
		t.Fatalf("lib's canonical id is %q; the test needs one apart from its local name", cid)
	}
	if bytes.Contains(exe, []byte(cid+"@")) {
		t.Errorf("the binary names lib's file by its canonical id %s@...", cid)
	}
	if !bytes.Contains(exe, []byte("lib@"+sha(arch)[7:19]+"/lib.mk")) {
		t.Errorf("no site string names lib@%s/lib.mk", sha(arch)[7:19])
	}
}
