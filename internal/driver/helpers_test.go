// Why: most driver tests need a package tree on disk, a small std and a
// compact way to compare diagnostics.
// What: writeTree, manifest, the stand-in std (fakePreludeText,
// fakeStdFiles), check, checkMemPackage and the line-rendering helpers.
// How: package trees go to temporary directories; the stand-in std declares
// every prelude name; reports render as "path:line:col:code" lines.

package driver

import (
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"mk/internal/diag"
	"mk/internal/source"
	"mk/std"
)

// -update rewrites this package's golden files under testdata. Read the
// diff before keeping it.
var update = flag.Bool("update", false, "rewrite golden files under testdata")

// writeTree writes files (slash paths to text) under a new temporary
// directory and returns it.
func writeTree(t testing.TB, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for p, text := range files {
		full := filepath.Join(dir, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// manifest is a literal pkg.mk naming every field, with the given
// values spliced in for the fields set in over.
func manifest(over map[string]string) string {
	vals := map[string]string{"name": `"p"`, "bins": "[]", "deps": "[]", "c_sources": "[]", "c_imports": "[]",
		"asm_sources": "[]", "targets": "[]", "unsafe_modules": "[]", "links": "[]"}
	for k, v := range over {
		vals[k] = v
	}
	var b strings.Builder
	b.WriteString("const Package = pkg.Package(\n")
	for _, f := range manifestFields {
		fmt.Fprintf(&b, "    %s: %s,\n", f, vals[f])
	}
	b.WriteString(")\n")
	return b.String()
}

// fakePreludeText declares every prelude name (Path as a struct).
func fakePreludeText() string {
	var b strings.Builder
	generic := map[string]string{"Box": "[T]", "Cell": "[T]", "Chan": "[T]", "Guard": "[T]", "HeapList": "[T]",
		"HeapMap": "[K, V]", "List": "[T]", "Map": "[K, V]", "MapIter": "[K, V]", "Mutex": "[T]", "Set": "[T]",
		"Scope": "[E]", "TextMap": "[V]", "TextMapIter": "[V]"}
	for _, n := range preludeNames {
		switch n {
		case "Canceled", "Closed", "OutOfMemory", "ParseError", "ReadError", "Utf8Error", "WriteError":
			fmt.Fprintf(&b, "export error %s\n", n)
		case "Eq", "Ord", "Hash", "Format", "Writer", "Reader":
			fmt.Fprintf(&b, "export interface %s {}\n", n)
		case "Slice":
			b.WriteString("export interface Slice[T] {}\n")
		case "Print":
			b.WriteString("export Print(x: int)\n")
		case "Arch", "Os", "StackSize":
			fmt.Fprintf(&b, "export enum %s { A }\n", n)
		default:
			fmt.Fprintf(&b, "export struct %s%s {}\n", n, generic[n])
		}
	}
	return b.String()
}

// fakeStdFiles is a stand-in std tree in std's directory layout.
func fakeStdFiles() map[string]string {
	return map[string]string{
		"core/core.mk":          "import std.raw\n\n" + fakePreludeText() + "export UseRaw(): int {\n    var n := 0\n    unsafe \"test\" {\n        n = raw.One()\n    }\n    return n\n}\n",
		"raw/raw.mk":            "export One(): int\n",
		"fs/fs.mk":              "export ReadText(): int {\n    return 1\n}\n",
		"sys/windows/sys.mk":    "export Os(): int {\n    unsafe \"test\" {\n    }\n    return 1\n}\n",
		"sys/linux/sys.mk":      "export Os(): int {\n    return 2\n}\n",
		"corpus/testprelude.mk": "export struct Point {\n    x: int\n}\n",
		"pkg/pkg.mk":            stdPkgText(),
		"pkg.mk": manifest(map[string]string{"name": `"std"`,
			"unsafe_modules": `["core", "sys/windows", "sys/linux"]`,
			"targets": `[pkg.Select(os: "windows", modules: [pkg.Module(name: "sys", dir: "sys/windows")]), ` +
				`pkg.Select(os: "linux", modules: [pkg.Module(name: "sys", dir: "sys/linux")])]`}),
	}
}

// fakeStd writes the stand-in std and returns its directory.
func fakeStd(t testing.TB) string { return writeTree(t, fakeStdFiles()) }

// check runs a Session over root with cfg and returns the result.
func check(t testing.TB, cfg Config) Result {
	t.Helper()
	if cfg.CacheDir == "" && !cfg.NoCache {
		cfg.CacheDir = t.TempDir() // a cache of its own: no hit from another test
	}
	s, err := NewSession(cfg)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	return s.Check()
}

// lines renders a report as sorted "path:line:col:code" strings.
func lines(r Result) []string {
	var out []string
	for _, e := range r.Report.Diags {
		out = append(out, diag.Location(r.Sources, e.Diag.At)+":"+e.Diag.Code.String())
	}
	return out
}

// codes is the report's codes in order.
func codes(r Result) []string {
	var out []string
	for _, e := range r.Report.Diags {
		out = append(out, e.Diag.Code.String())
	}
	return out
}

// fact is a text fact of a diagnostic, "" when absent.
func fact(d *diag.Diagnostic, key string) string {
	f, ok := d.FactValue(key)
	if !ok {
		return ""
	}
	return f.Str
}

// expectLines fails unless the report's lines equal want, in order.
func expectLines(t testing.TB, r Result, want ...string) {
	t.Helper()
	got := lines(r)
	if !slices.Equal(got, want) {
		t.Errorf("diagnostics:\n got %q\nwant %q\n%s", got, want, r.Report.Text(r.Sources))
	}
}

// clean fails unless the report is empty.
func clean(t testing.TB, r Result) {
	t.Helper()
	if len(r.Report.Diags) > 0 {
		t.Errorf("want no diagnostics, got:\n%s", r.Report.Text(r.Sources))
	}
}

// memReport is a report with its source map, from checkMemPackage.
type memReport struct {
	diag.Report
	sm *source.SourceMap
}

func (r memReport) line(i int) string {
	return diag.Location(r.sm, r.Diags[i].Diag.At) + ":" + r.Diags[i].Diag.Code.String()
}

func (r memReport) lines() []string {
	var out []string
	for i := range r.Diags {
		out = append(out, r.line(i))
	}
	return out
}

// checkMemPackage checks a package held in memory (package mode when it
// has a pkg.mk) through the same pipeline as Session.Check, with the
// embedded std, so listing-only rules can be tested on any file system.
func checkMemPackage(t testing.TB, files map[string]string) memReport {
	t.Helper()
	cfg, err := Config{}.normalize()
	if err != nil {
		t.Fatal(err)
	}
	w := newWorld(cfg)
	tr := &srcTree{mem: files}
	p := &pipeline{w: w}
	var man *Manifest
	if _, ok := files["pkg.mk"]; ok {
		var ds []*diag.Diagnostic
		man, ds = w.newManifestStep(w.stdTree()).load(tr, "pkg.mk", "main")
		p.diags = ds
	}
	if len(p.diags) == 0 {
		b := newBuild(w, newBuild(w, nil))
		if err := b.listTree(tr, man, "main"); err != nil {
			t.Fatal(err)
		}
		for _, m := range b.mods {
			m.path = m.name
		}
		b.permitUnsafe(man)
		b.findCaseDups()
		p.builds = []*build{b}
		if p.front() {
			w.startResolve(nil)
			b.register()
			b.pipelineAll(false)
			p.diags = append(p.diags, b.stageDiags()...)
		}
	}
	return memReport{diag.Order(p.diags, w.sm, diag.NoCap), w.sm}
}

// stdPkgText is the embedded std/pkg/pkg.mk: a stand-in std evaluates its
// manifests with the real std.pkg.
func stdPkgText() string {
	b, err := fs.ReadFile(std.FS, "pkg/pkg.mk")
	if err != nil {
		panic(err)
	}
	return string(b)
}
