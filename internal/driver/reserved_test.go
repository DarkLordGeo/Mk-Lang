// Why: the driver's front steps (listing, manifest, module walk, import
// paths) report user mistakes, and each of these reports must be right and
// never a crash.
// What: tests of reserved first segments (std, c, pkg) and one source path
// per file, ICEs in sequential steps becoming E0000, target-selection and
// bin-root validation, the E0205 cycle fact, package-relative listing
// failures and --llvm reaching toolchain discovery.
// How: checks small trees in temporary directories and compares
// diagnostics, anchors and exit statuses.

package driver

import (
	"io/fs"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"mk/internal/diag"
)

// only fails unless the report holds exactly one diagnostic, and returns it.
func only(t *testing.T, r Result, code string) *diag.Diagnostic {
	t.Helper()
	if got := codes(r); len(got) != 1 || got[0] != code {
		t.Fatalf("codes %q, want one %s\n%s", got, code, r.Report.Text(r.Sources))
	}
	return r.Report.Diags[0].Diag
}

// listFact is a list fact's strings.
func listFact(d *diag.Diagnostic, key string) []string {
	f, _ := d.FactValue(key)
	var out []string
	for _, x := range f.List {
		out = append(out, x.Str)
	}
	return out
}

// TestReservedStdSegment: a package directory whose import path begins
// with std, c or pkg forms no module and is refused, never read (a std/
// directory's files would take std's own source paths, and SourceMap.Add
// would ICE).
func TestReservedStdSegment(t *testing.T) {
	std := fakeStd(t)
	for _, c := range []struct{ dir, module string }{
		{"std/core", "std.core"}, {"std.x", "std.x"}, {"c", "c"}, {"c.lib", "c.lib"}, {"pkg", "pkg"},
	} {
		t.Run(c.module, func(t *testing.T) {
			r := check(t, Config{StdDir: std, Root: writeTree(t, pkgFiles(map[string]string{
				"lib/l.mk":           "export L(): int {\n    return 1\n}\n",
				c.dir + "/core.mk":   "export F(): int {\n    return 1\n}\n",
				c.dir + "/second.mk": "this is never read (\n",
			}))})
			d := only(t, r, "E0202")
			if fact(d, "reason") != "reserved" || fact(d, "module") != c.module || d.Phase != diag.PhaseImports {
				t.Errorf("facts %q %q phase %v\n%s", fact(d, "reason"), fact(d, "module"), d.Phase, r.Report.Text(r.Sources))
			}
			if st := ExitStatus(r.Report); st != 1 {
				t.Errorf("exit status %d, want 1", st)
			}
			if slices.Contains(r.Trace.FilesParsed, c.dir+"/second.mk") {
				t.Errorf("a reserved directory was parsed: %q", r.Trace.FilesParsed)
			}
		})
	}
	// A nested std/ is an ordinary directory: its import path is a.std.
	clean(t, check(t, Config{StdDir: std, Root: writeTree(t, pkgFiles(map[string]string{
		"a/std/core.mk": "export F(): int {\n    return 1\n}\n",
	}))}))
}

// TestSingleFileInStdNamespace: in single-file mode, `mk check
// std/core/core.mk` run from a directory holding its own std/ cites the
// file as ./std/core/core.mk, so it never shares a source path with std's
// std/core/core.mk.
func TestSingleFileInStdNamespace(t *testing.T) {
	std := fakeStd(t)
	dir := writeTree(t, map[string]string{"std/core/core.mk": "export F(): int {\n    return 1\n}\n"})
	t.Chdir(dir)
	r := check(t, Config{StdDir: std, Root: "std/core/core.mk"})
	clean(t, r)
	if !slices.Contains(r.Trace.FilesParsed, "./std/core/core.mk") || !slices.Contains(r.Trace.FilesParsed, "std/core/core.mk") {
		t.Errorf("FilesParsed %q, want ./std/core/core.mk beside std's std/core/core.mk", r.Trace.FilesParsed)
	}
	// The same for std's manifest path.
	dir = writeTree(t, map[string]string{"std/pkg.mk": "export F(): int {\n    return 1\n}\n"})
	t.Chdir(dir)
	clean(t, check(t, Config{StdDir: std, Root: filepath.FromSlash("std/pkg.mk")}))
}

// TestE0000_ManifestAndLoadICEBecomeDiagnostic: an ICE in a sequential step
// (the manifest reader, listing, the read step, the import step) is E0000
// with exit status 3, never a Go panic out of Session.Check; the same for
// the corpus session's std load.
func TestE0000_ManifestAndLoadICEBecomeDiagnostic(t *testing.T) {
	root := writeTree(t, pkgFiles(map[string]string{"a/a.mk": "export F(): int {\n    return 1\n}\n"}))
	for _, step := range []string{"manifest", "list", "read", "link"} {
		t.Run(step, func(t *testing.T) {
			r := check(t, Config{StdDir: fakeStd(t), Root: root, TestHooks: TestHooks{ICEAt: step}})
			d := only(t, r, "E0000")
			if !strings.Contains(fact(d, "what"), "step "+step) {
				t.Errorf("what %q", fact(d, "what"))
			}
			if st := ExitStatus(r.Report); st != 3 {
				t.Errorf("exit status %d, want 3", st)
			}
		})
	}
	c, err := NewCorpusSession(Config{StdDir: fakeStd(t), TestHooks: TestHooks{ICEAt: "manifest"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := codes(c.Std()); !slices.Equal(got, []string{"E0000"}) {
		t.Errorf("corpus std codes %q, want [E0000]", got)
	}
}

// targetsManifest is a manifest whose targets field is sels.
func targetsManifest(sels string) string {
	return manifest(map[string]string{"targets": "[" + sels + "]"})
}

// TestTargetSelectionNameCollides: a selection name equal to a walked
// module's import path, and an OS selected twice, are each one E0202
// invalid at the field, not an ICE.
func TestTargetSelectionNameCollides(t *testing.T) {
	both := `pkg.Select(os: "windows", modules: [pkg.Module(name: "os.native", dir: "src/os/windows")]), ` +
		`pkg.Select(os: "linux", modules: [pkg.Module(name: "os.native", dir: "src/os/linux")])`
	twice := `pkg.Select(os: "windows", modules: [pkg.Module(name: "os.native", dir: "src/os/windows")]), ` +
		`pkg.Select(os: "windows", modules: [pkg.Module(name: "os.native", dir: "src/os/linux")])`
	for _, c := range []struct {
		name, sels, field string
		extra             map[string]string
	}{
		{"directory", both, "targets[0].modules[0].name", map[string]string{"os/native/a.mk": "export G(): int {\n    return 1\n}\n"}},
		{"root", strings.ReplaceAll(both, `"os.native"`, `"main"`), "targets[0].modules[0].name", map[string]string{"root.mk": "export G(): int {\n    return 1\n}\n"}},
		{"os twice", twice, "targets[1].os", nil},
		{"reserved name", strings.ReplaceAll(both, `"os.native"`, `"std.os"`), "targets[0].modules[0].name", nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			files := map[string]string{
				"pkg.mk":              targetsManifest(c.sels),
				"src/os/windows/w.mk": "export F(): int {\n    return 1\n}\n",
				"src/os/linux/l.mk":   "export F(): int {\n    return 2\n}\n",
			}
			for k, v := range c.extra {
				files[k] = v
			}
			r := check(t, Config{StdDir: fakeStd(t), Root: writeTree(t, files)})
			d := only(t, r, "E0202")
			if fact(d, "reason") != "invalid" || fact(d, "manifest") != c.field || r.Report.Phase != diag.PhaseImports {
				t.Errorf("reason %q manifest %q phase %v, want invalid %s imports\n%s",
					fact(d, "reason"), fact(d, "manifest"), r.Report.Phase, c.field, r.Report.Text(r.Sources))
			}
		})
	}
}

// TestImportPathCollision: two directories with one import path (the
// root's files and main/, a.b/ and a/b/) are one E0206 at the import step,
// naming the other directory, not a names-phase duplicate item and not a
// silent shadow.
func TestImportPathCollision(t *testing.T) {
	src := "export F(): int {\n    return 1\n}\n"
	for _, c := range []struct {
		files       map[string]string
		line, first string
	}{
		{map[string]string{"root.mk": src, "main/m.mk": src}, "main/m.mk:1:1:E0206", "."},
		{map[string]string{"a.b/x.mk": src, "a/b/y.mk": src}, "a/b/y.mk:1:1:E0206", "a.b"},
		{map[string]string{"a.b/x.mk": src, "a/b/y.mk": "export G(): int {\n    return 1\n}\n",
			"u/u.mk": "import a.b\n\nexport U(): int {\n    return b.G()\n}\n"}, "a/b/y.mk:1:1:E0206", "a.b"},
	} {
		r := check(t, Config{StdDir: fakeStd(t), Root: writeTree(t, pkgFiles(c.files))})
		expectLines(t, r, c.line)
		if len(r.Report.Diags) == 1 {
			d := r.Report.Diags[0].Diag
			if fact(d, "first") != c.first || r.Report.Phase != diag.PhaseImports || strings.Contains(d.Message, "declared") {
				t.Errorf("first %q phase %v message %q", fact(d, "first"), r.Report.Phase, d.Message)
			}
		}
	}
}

// TestPkgManifestBinAnchor: a bad bin root is reported at its own root
// string, with the source index in the manifest fact, though bins are kept
// in name order.
func TestPkgManifestBinAnchor(t *testing.T) {
	text := manifest(map[string]string{"bins": `[pkg.Bin(name: "zed", root: "a", link_script: "", cpu: "", subsystem: "console"), pkg.Bin(name: "app", root: "missing", link_script: "", cpu: "", subsystem: "console")]`})
	r := check(t, Config{StdDir: fakeStd(t), Root: writeTree(t, map[string]string{
		"pkg.mk": text, "a/a.mk": "export Main(sys: &int) {\n}\n"})})
	d := only(t, r, "E0202")
	line, col := 0, 0
	for i, l := range strings.Split(text, "\n") {
		if k := strings.Index(l, `"missing"`); k >= 0 {
			line, col = i+1, k+1
		}
	}
	want := "pkg.mk:" + strconv.Itoa(line) + ":" + strconv.Itoa(col)
	if got := diag.Location(r.Sources, d.At); got != want || fact(d, "manifest") != "bins[1].root" {
		t.Errorf("at %s manifest %q, want %s bins[1].root", got, fact(d, "manifest"), want)
	}
	if !strings.Contains(d.Message, "bin app") {
		t.Errorf("message %q", d.Message)
	}
}

// TestBinRootMustBeModuleDir: a bin root the module walk skips (hidden,
// gen, under a target selection) is E0202 invalid at bins[0].root, never a
// clean report.
func TestBinRootMustBeModuleDir(t *testing.T) {
	sels := `pkg.Select(os: "windows", modules: [pkg.Module(name: "os.native", dir: "os/windows")]), ` +
		`pkg.Select(os: "linux", modules: [pkg.Module(name: "os.native", dir: "os/linux")])`
	noMain := "F(): int {\n    return 1\n}\n"
	for _, root := range []string{".hidden", "gen", "os/windows", "os/windows/sub", "c"} {
		t.Run(root, func(t *testing.T) {
			r := check(t, Config{StdDir: fakeStd(t), Root: writeTree(t, map[string]string{
				"pkg.mk": manifest(map[string]string{"bins": `[pkg.Bin(name: "x", root: "` + root + `", link_script: "", cpu: "", subsystem: "console")]`,
					"targets": "[" + sels + "]"}),
				root + "/h.mk":    noMain,
				"os/windows/w.mk": noMain,
				"os/linux/l.mk":   noMain,
			})})
			d := only(t, r, "E0202")
			if fact(d, "reason") != "invalid" || fact(d, "manifest") != "bins[0].root" {
				t.Errorf("reason %q manifest %q\n%s", fact(d, "reason"), fact(d, "manifest"), r.Report.Text(r.Sources))
			}
		})
	}
	// An ordinary directory is a module, so a bin there without Main gets
	// resolve's E0201.
	r := check(t, Config{StdDir: fakeStd(t), Root: writeTree(t, map[string]string{
		"pkg.mk": manifest(map[string]string{"bins": `[pkg.Bin(name: "x", root: "sub", link_script: "", cpu: "", subsystem: "console")]`}), "sub/h.mk": noMain})})
	if got := codes(r); !slices.Equal(got, []string{"E0201"}) {
		t.Errorf("ordinary root: codes %q, want [E0201]", got)
	}
}

// TestE0205_CycleFactIsACycle: the E0205 cycle fact is a real cycle: every
// consecutive pair is an import edge.
func TestE0205_CycleFactIsACycle(t *testing.T) {
	edges := map[string][]string{"a": {"b"}, "b": {"d", "a"}, "d": {"b"}}
	files := map[string]string{}
	for m, deps := range edges {
		var b strings.Builder
		for _, d := range deps {
			b.WriteString("import " + d + "\n")
		}
		b.WriteString("\nexport F(): int {\n    return 1\n}\n")
		files[m+"/"+m+".mk"] = b.String()
	}
	r := check(t, Config{StdDir: fakeStd(t), Root: writeTree(t, pkgFiles(files))})
	d := only(t, r, "E0205")
	cycle := listFact(d, "cycle")
	if !slices.Equal(cycle, []string{"a", "b", "a"}) {
		t.Errorf("cycle %q, want [a b a]", cycle)
	}
	for i := 0; i+1 < len(cycle); i++ {
		if !slices.Contains(edges[cycle[i]], cycle[i+1]) {
			t.Errorf("cycle %q: %s does not import %s", cycle, cycle[i], cycle[i+1])
		}
	}
	// A longer cycle that needs every step: a -> b -> d -> a.
	r = check(t, Config{StdDir: fakeStd(t), Root: writeTree(t, pkgFiles(map[string]string{
		"a/a.mk": "import b\n", "b/b.mk": "import d\n", "d/d.mk": "import a\n",
	}))})
	if got := listFact(only(t, r, "E0205"), "cycle"); !slices.Equal(got, []string{"a", "b", "d", "a"}) {
		t.Errorf("cycle %q, want [a b d a]", got)
	}
}

// TestTargetSelectionNamesMismatchAnchor: a name one selection maps and
// another does not is reported at the pkg.Module that has it.
func TestTargetSelectionNamesMismatchAnchor(t *testing.T) {
	sels := `pkg.Select(os: "windows", modules: [pkg.Module(name: "os.native", dir: "w"), ` +
		`pkg.Module(name: "os.extra", dir: "x")]), ` +
		`pkg.Select(os: "linux", modules: [pkg.Module(name: "os.native", dir: "l")])`
	text := targetsManifest(sels)
	src := "export F(): int {\n    return 1\n}\n"
	r := check(t, Config{StdDir: fakeStd(t), Root: writeTree(t, map[string]string{
		"pkg.mk": text, "w/w.mk": src, "x/x.mk": src, "l/l.mk": src})})
	d := only(t, r, "E0202")
	line, col := 0, 0
	for i, l := range strings.Split(text, "\n") {
		if k := strings.Index(l, `pkg.Module(name: "os.extra"`); k >= 0 {
			line, col = i+1, k+1
		}
	}
	want := "pkg.mk:" + strconv.Itoa(line) + ":" + strconv.Itoa(col)
	if got := diag.Location(r.Sources, d.At); got != want || fact(d, "manifest") != "targets[0].modules[1]" {
		t.Errorf("at %s manifest %q, want %s targets[0].modules[1]", got, fact(d, "manifest"), want)
	}
}

// failFS fails the listing of one directory, as an unreadable directory
// does; the error carries the tree-relative path, as os.DirFS gives it.
type failFS struct {
	fs.FS
	dir string
}

func (f failFS) ReadDir(name string) ([]fs.DirEntry, error) {
	if name == f.dir {
		return nil, &fs.PathError{Op: "readdir", Path: name, Err: fs.ErrPermission}
	}
	return fs.ReadDir(f.FS, name)
}

// TestE0001_ListFailurePathIsRelative: a listing failure's E0001 cites the
// directory package-relative, the same on every machine and root.
func TestE0001_ListFailurePathIsRelative(t *testing.T) {
	var docs []string
	for range 2 {
		root := writeTree(t, pkgFiles(map[string]string{"sub/s.mk": "export F(): int {\n    return 1\n}\n"}))
		r := check(t, Config{StdDir: fakeStd(t), Root: root,
			TestHooks: TestHooks{WrapFS: func(f fs.FS) fs.FS { return failFS{f, "sub"} }}})
		d := only(t, r, "E0001")
		if fact(d, "command") != "list sub" || strings.Contains(d.Message, root) {
			t.Errorf("command %q message %q", fact(d, "command"), d.Message)
		}
		if st := ExitStatus(r.Report); st != 2 {
			t.Errorf("exit status %d, want 2", st)
		}
		docs = append(docs, string(r.Report.WriteJSON(r.Sources)))
	}
	if docs[0] != docs[1] {
		t.Errorf("the report depends on the root:\n%s\n%s", docs[0], docs[1])
	}
}

// TestLLVMDirReachesToolchainOptions: --llvm reaches the driver's Config
// and toolchain discovery.
func TestLLVMDirReachesToolchainOptions(t *testing.T) {
	cfg, err := Config{LLVMDir: "llvm-dir"}.normalize()
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.toolchainOptions().LLVMDir; got != "llvm-dir" {
		t.Errorf("toolchain.Options.LLVMDir = %q", got)
	}
}
