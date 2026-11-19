// Why: a fetched package's unsafe code is a trust decision, which only the
// root package may make.
// What: tests that a fetched package's unsafe code takes part only when
// the root grants it (E0215 grant-missing), for source, path and url
// packages, and of mk query perimeter.
// How: grantRoot writes root manifests with and without unsafe_deps; e0215
// checks the one diagnostic and where it points.

package driver

import (
	"path/filepath"
	"strings"
	"testing"

	"mk/internal/diag"
)

// grantRoot is a root manifest with deps and, when grants is not "", an
// unsafe_deps field (omitted fields left out).
func grantRoot(name, grants string, deps ...string) string {
	s := "const Package = pkg.Package(\n    name: \"" + name + "\",\n    deps: [" + strings.Join(deps, ", ") + "],\n"
	if grants != "" {
		s += "    unsafe_deps: [" + grants + "],\n"
	}
	return s + ")\n"
}

// unsafeToml is the toml package whose manifest lists src as an unsafe
// module and a link.
func unsafeToml() map[string]string {
	f := tomlFiles("1")
	f["pkg.mk"] = "const Package = pkg.Package(\n    name: \"toml\",\n    unsafe_modules: [\"src\"],\n" +
		"    links: [pkg.Link(name: \"tomlc\", kind: \"dynamic\", path: \"\", target: \"x86_64-windows\")],\n)\n"
	return f
}

// e0215 fails unless r holds exactly one E0215, at a path with the prefix
// at, and returns it.
func e0215(t *testing.T, r Result, at string) *diag.Diagnostic {
	t.Helper()
	got := lines(r)
	if len(got) != 1 || !strings.HasSuffix(got[0], ":E0215") || !strings.HasPrefix(got[0], at) {
		t.Fatalf("want one E0215 at %s, got %v:\n%s", at, got, r.Report.Text(r.Sources))
	}
	return r.Report.Diags[0].Diag
}

func TestGrant_SourcePackage(t *testing.T) {
	emptyNetrc(t)
	gs := newGitServer(t)
	gs.tag("someone/toml", "1.0.0", unsafeToml())
	src := gs.source("someone/toml")
	use := "import toml.src\n\nexport A(): int {\n    return src.V()\n}\n"
	for _, tc := range []struct{ name, grants, want string }{
		{"no grant", "", "E0215"},
		{"another source", `"example.com/other/x"`, "E0215"},
		{"by source", `"` + src + `"`, ""},
		{"by local name", `"toml"`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ws := writeTree(t, map[string]string{
				"app/pkg.mk": grantRoot("app", tc.grants, srcDep("toml", src, "1.0.0")),
				"app/a.mk":   use,
			})
			r := depCheck(t, filepath.Join(ws, "app"), sourceCfg(gs, ""))
			if tc.want == "" {
				clean(t, r)
				return
			}
			d := e0215(t, r, "pkg.mk:3:")
			for _, w := range []string{"toml (" + src + " v1.0.0)", "unsafe module src", "link tomlc", `"` + src + `"`} {
				if !strings.Contains(d.Message, w) {
					t.Errorf("message %q lacks %q", d.Message, w)
				}
			}
			if len(d.Alternatives) != 1 || !strings.Contains(d.Alternatives[0], `"`+src+`"`) {
				t.Errorf("alternatives %q", d.Alternatives)
			}
		})
	}
}

// A path package is the user's own code: no grant. A source package it
// brings in needs the root's grant by source (a local name in another
// manifest is no grant), reported at the root's Dep that leads to it.
func TestGrant_PathAndTransitive(t *testing.T) {
	emptyNetrc(t)
	gs := newGitServer(t)
	gs.tag("someone/toml", "1.0.0", unsafeToml())
	src := gs.source("someone/toml")
	util := map[string]string{
		"util/pkg.mk":   "const Package = pkg.Package(\n    name: \"util\",\n    deps: [" + srcDep("toml", src, "1.0.0") + "],\n    unsafe_modules: [\"src\"],\n)\n",
		"util/src/u.mk": "import toml.src\n\nexport U(): int {\n    return src.V()\n}\n",
	}
	for _, tc := range []struct{ name, grants, want string }{
		{"no grant", "", "E0215"},
		{"by util's local name", `"toml"`, "E0215"},
		{"by source", `"` + src + `"`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			files := map[string]string{
				"app/pkg.mk": grantRoot("app", tc.grants, pathDep("util", "../util")),
				"app/a.mk":   "import util.src\n\nexport A(): int {\n    return src.U()\n}\n",
			}
			for k, v := range util {
				files[k] = v
			}
			ws := writeTree(t, files)
			r := depCheck(t, filepath.Join(ws, "app"), sourceCfg(gs, ""))
			if tc.want == "" {
				clean(t, r)
				return
			}
			e0215(t, r, "pkg.mk:3:")
		})
	}
}

func TestGrant_URLPackage(t *testing.T) {
	files := iniFiles()
	files["pkg.mk"] = "const Package = pkg.Package(\n    name: \"ini\",\n    c_sources: [pkg.CSource(path: \"c/ini.c\", flags: [])],\n)\n"
	files["c/ini.c"] = "int ini_c(void) { return 1; }\n"
	arch := tarGzOf(t, "", files)
	srv, _ := archiveServer(t, map[string][]byte{"/ini.tar.gz": arch})
	url := srv.URL + "/ini.tar.gz"
	use := "import ini.src\n\nexport A(): int {\n    return src.Keys()\n}\n"
	app := writeTree(t, map[string]string{"pkg.mk": grantRoot("app", "", urlDep("ini", url, "")), "a.mk": use})
	d := e0215(t, depCheck(t, app, Config{}), "pkg.mk:3:")
	if !strings.Contains(d.Message, "C file c/ini.c") {
		t.Errorf("message %q", d.Message)
	}
	app = writeTree(t, map[string]string{"pkg.mk": grantRoot("app", `"`+url+`"`, urlDep("ini", url, "")), "a.mk": use})
	r := depCheck(t, app, Config{})
	for _, l := range lines(r) {
		if strings.HasSuffix(l, ":E0215") || strings.HasSuffix(l, ":E0202") {
			t.Errorf("granted by url, still %s", l)
		}
	}
}

func TestGrant_QueryPerimeter(t *testing.T) {
	emptyNetrc(t)
	gs := newGitServer(t)
	gs.tag("someone/toml", "1.0.0", unsafeToml())
	src := gs.source("someone/toml")
	ws := writeTree(t, map[string]string{
		"app/pkg.mk":    grantRoot("app", "", srcDep("toml", src, "1.0.0"), pathDep("util", "../util")),
		"app/a.mk":      "import toml.src\n\nexport A(): int {\n    return src.V()\n}\n",
		"util/pkg.mk":   depManifest("util"),
		"util/src/u.mk": "export U(): int {\n    return 1\n}\n",
	})
	cfg := sourceCfg(gs, "")
	cfg.Root, cfg.StdDir = filepath.Join(ws, "app"), fakeStd(t)
	s, err := NewSession(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ans, res, err := s.Query("perimeter", nil)
	if err != nil || ans == nil {
		t.Fatalf("%v %v", ans, err)
	}
	if c := codes(res); len(c) != 1 || c[0] != "E0215" {
		t.Errorf("codes %v", c)
	}
	want := []string{"app (.)", "  (nothing unsafe)", "toml (" + src + " v1.0.0) not granted", "  unsafe module src", "  link tomlc",
		"util (../util)", "  (nothing unsafe)"}
	if strings.Join(ans.Answer, "\n") != strings.Join(want, "\n") {
		t.Errorf("perimeter:\n%s\nwant:\n%s", strings.Join(ans.Answer, "\n"), strings.Join(want, "\n"))
	}
	if j := string(ans.JSON()); !strings.Contains(j, `"perimeter":[`) || !strings.Contains(j, `"unsafe_modules":["src"]`) {
		t.Errorf("json %s", j)
	}
}
