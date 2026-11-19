// Why: pkg.Package's `links` field must decode faithfully, and manifests
// written before the field existed must keep building unchanged.
// What: TestFFIM_DecodeLinks and TestFFIM_OldManifestNeedsNoFix, with the
// manifest builder ffimManifest.
// How: decodes manifests with zero, one and two links, and against a std
// tree with the field cut out; runs mk fix on manifests that omit fields.
// Link validation and the no-link link line are tested in ffi_link_test.go.

package driver

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"mk/internal/types"
)

const ffimMain = "export Main(sys: &System): WriteError!void {\n    try sys.Print(\"hi\\n\")\n}\n"

// ffimManifest is a manifest of one console bin with the given links value
// ("" leaves the field out, as a manifest from before the field does).
func ffimManifest(links string) string {
	text := `const Package = pkg.Package(
    name: "app",
    bins: [pkg.Bin(name: "app", root: "src", link_script: "", cpu: "", subsystem: "console")],
    deps: [],
    c_sources: [],
    c_imports: [],
    asm_sources: [],
    targets: [],
    unsafe_modules: [],
`
	if links != "" {
		text += "    links: " + links + ",\n"
	}
	return text + ")\n"
}

func TestFFIM_DecodeLinks(t *testing.T) {
	t.Parallel()
	load := func(links string, cfg Config) *Manifest {
		t.Helper()
		// lib/z.a exists, as the build phase's validation reads it.
		cfg.Root = writeTree(t, map[string]string{"pkg.mk": ffimManifest(links), "src/main.mk": ffimMain, "lib/z.a": "!<arch>\n"})
		cfg.CacheDir, cfg.Target = t.TempDir(), types.X86_64Windows
		s, err := NewSession(cfg)
		if err != nil {
			t.Fatal(err)
		}
		r := s.Check()
		clean(t, r)
		if s.pkgMan == nil {
			t.Fatal("no manifest")
		}
		return s.pkgMan
	}
	two := `[
        pkg.Link(name: "version.dll", kind: "dynamic", path: "", target: "x86_64-windows"),
        pkg.Link(name: "zlib", kind: "static", path: "lib/z.a", target: "x86_64-linux"),
    ]`
	m := load(two, Config{})
	want := []Link{
		{Name: "version.dll", Kind: "dynamic", Path: "", Target: "x86_64-windows", Field: "links[0]"},
		{Name: "zlib", Kind: "static", Path: "lib/z.a", Target: "x86_64-linux", Field: "links[1]"},
	}
	if !slices.Equal(m.Links, want) {
		t.Errorf("links %+v, want %+v", m.Links, want)
	}
	empty := load("[]", Config{})
	if len(empty.Links) != 0 {
		t.Errorf("links: [] decoded as %+v", empty.Links)
	}
	if empty.Fields["links"] == m.Fields["links"] {
		t.Error("two links values have one field hash")
	}
	if want := len(manifestFields) + len(manifestFieldsExt); len(empty.Fields) != want {
		t.Errorf("field hashes %d, want %d", len(empty.Fields), want)
	}
	// A std tree from before links existed: std.pkg without Link and the field,
	// std's own manifest without the value. Such a manifest reads as
	// `links: []`, with that value's field hash.
	old := copyStd(t, nil, func(p string, b []byte) []byte {
		switch p {
		case "pkg/pkg.mk":
			text := string(b)
			text = strings.Replace(text, "    export links: []Link\n", "", 1)
			if i := strings.Index(text, "/// A library the package's extern functions"); i >= 0 {
				if j := strings.Index(text[i:], "\n}\n"); j >= 0 {
					text = text[:i] + text[i+j+3:]
				}
			}
			return []byte(text)
		case "pkg.mk":
			return []byte(strings.Replace(string(b), "    links: [],\n", "", 1))
		}
		return b
	})
	if b, _ := os.ReadFile(filepath.Join(old, "pkg", "pkg.mk")); strings.Contains(string(b), "links") || strings.Contains(string(b), "struct Link") {
		t.Fatalf("the old std.pkg still names links:\n%s", b)
	}
	pre := load("", Config{StdDir: old})
	if len(pre.Links) != 0 || pre.Fields["links"] != empty.Fields["links"] {
		t.Errorf("a std.pkg without links: links %+v, hash equal to `links: []`'s %v", pre.Links, pre.Fields["links"] == empty.Fields["links"])
	}
}

// TestFFIM_OldManifestNeedsNoFix: a manifest written before `links` or
// `subsystem` existed leaves them out. The manifest step supplies an
// omitted field whose value is "", [] or false, which links what the
// package linked before (a "" subsystem is console), so such a manifest
// builds as written and mk fix has nothing to do.
func TestFFIM_OldManifestNeedsNoFix(t *testing.T) {
	t.Parallel()
	fix := func(text string, extra map[string]string) (string, *FixResult) {
		t.Helper()
		files := map[string]string{"pkg.mk": text, "src/main.mk": ffimMain}
		for k, v := range extra {
			files[k] = v
		}
		dir := writeTree(t, files)
		s, err := NewSession(Config{Root: dir, CacheDir: t.TempDir(), Target: types.X86_64Windows})
		if err != nil {
			t.Fatal(err)
		}
		res, err := s.Fix(FixOptions{})
		if err != nil {
			t.Fatal(err)
		}
		got, _ := os.ReadFile(filepath.Join(dir, "pkg.mk"))
		return string(got), res
	}
	line := `const Package = pkg.Package(name: "app", bins: [pkg.Bin(name: "app", root: "src", link_script: "", cpu: "", subsystem: "console")], deps: [], c_sources: [], c_imports: [], asm_sources: [], targets: [], unsafe_modules: [])` + "\n"
	for _, tc := range []struct{ name, text string }{
		{"links missing", ffimManifest("")},
		{"one line", line},
		{"subsystem and links missing", strings.Replace(ffimManifest(""), `, subsystem: "console"`, "", 1)},
		{"links and targets missing", strings.Replace(ffimManifest(""), "    targets: [],\n", "", 1)},
	} {
		got, res := fix(tc.text, nil)
		if res.Stopped != "clean" || len(res.Rounds) != 0 || got != tc.text {
			t.Errorf("%s: mk fix %q %+v\n%s", tc.name, res.Stopped, res.Rounds, got)
		}
	}
	// A path dependency written before the field builds too.
	root := writeTree(t, map[string]string{
		"app/pkg.mk": strings.Replace(ffimManifest(""), "deps: [],",
			`deps: [pkg.Dep(name: "util", path: "../util", url: "", hash: "")],`, 1),
		"app/src/main.mk": "import util\n\nexport Main(sys: &System): WriteError!void {\n    _ = util.One()\n    try sys.Print(\"hi\\n\")\n}\n",
		"util/pkg.mk": strings.Replace(strings.Replace(ffimManifest(""), `"app"`, `"util"`, 1),
			`[pkg.Bin(name: "app", root: "src", link_script: "", cpu: "", subsystem: "console")]`, "[]", 1),
		"util/util.mk": "export One(): int {\n    return 1\n}\n",
	})
	clean(t, check(t, Config{Root: filepath.Join(root, "app"), Target: types.X86_64Windows}))
}
