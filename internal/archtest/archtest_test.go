// Why: the compiler's layering and its Go rules hold only while a test
// checks them over every file.
// What: TestDAGIsAcyclic, TestPackageDAG, TestTestEdgesAreSafe,
// TestStandardLibraryOnly, TestNoUnsafe, TestNoReflectInPasses and
// TestFilesAreSmallAndFormatted, over every .go file of the module.
// How: files reads each file and its imports with go/parser; dagTable turns
// the DAG data into allowed edges, and each test fails on what it forbids.

package archtest

import (
	"bytes"
	"go/format"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
)

const module = "mk"

// goFile is one parsed file of the module.
type goFile struct {
	rel     string // path from the module root, '/' separators
	pkg     string // DAG name of its directory
	test    bool
	imports []string
	src     []byte
}

func root(t *testing.T) string {
	t.Helper()
	r, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(r, "go.mod")); err != nil {
		t.Fatalf("module root not found from %s: %v", r, err)
	}
	return r
}

// dagName maps a directory (relative, '/') to its DAG name.
func dagName(dir string) string {
	if rest, ok := strings.CutPrefix(dir, "internal/"); ok {
		return rest
	}
	return dir
}

func files(t *testing.T) []goFile {
	t.Helper()
	r := root(t)
	var out []goFile
	fset := token.NewFileSet()
	err := filepath.WalkDir(r, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		if d.IsDir() {
			if path != r && (strings.HasPrefix(name, ".") || name == "testdata") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(name, ".go") {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		f, err := parser.ParseFile(fset, path, src, parser.ImportsOnly)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(r, path)
		rel = filepath.ToSlash(rel)
		g := goFile{rel: rel, pkg: dagName(filepath.ToSlash(filepath.Dir(rel))), test: strings.HasSuffix(name, "_test.go"), src: src}
		for _, im := range f.Imports {
			p, _ := strconv.Unquote(im.Path.Value)
			g.imports = append(g.imports, p)
		}
		out = append(out, g)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) == 0 {
		t.Fatal("no Go files found")
	}
	return out
}

func dagTable() (map[string][]string, map[string]map[string]bool) {
	direct := map[string][]string{}
	for _, e := range DAG {
		direct[e.Pkg] = e.Imports
	}
	// closure[p] is everything p reaches.
	closure := map[string]map[string]bool{}
	var reach func(p string, seen map[string]bool)
	reach = func(p string, seen map[string]bool) {
		for _, q := range direct[p] {
			if !seen[q] {
				seen[q] = true
				reach(q, seen)
			}
		}
	}
	for p := range direct {
		s := map[string]bool{}
		reach(p, s)
		closure[p] = s
	}
	return direct, closure
}

// TestDAGIsAcyclic: no package reaches itself, and every edge names a package.
func TestDAGIsAcyclic(t *testing.T) {
	direct, closure := dagTable()
	for p, s := range closure {
		if s[p] {
			t.Errorf("%s reaches itself", p)
		}
	}
	for p, ims := range direct {
		for _, q := range ims {
			if _, ok := direct[q]; !ok {
				t.Errorf("%s imports %s, which is not in the DAG", p, q)
			}
		}
	}
}

// TestPackageDAG: every package of the module is in the DAG; a non-test file
// imports only its package's listed edges; a test file may import anything
// its package reaches (which cannot form a cycle). Imports outside the module
// must be the standard library.
func TestPackageDAG(t *testing.T) {
	direct, closure := dagTable()
	for _, f := range files(t) {
		if _, ok := direct[f.pkg]; !ok {
			t.Errorf("%s: package %s is not in the package DAG (internal/archtest)", f.rel, f.pkg)
			continue
		}
		for _, im := range f.imports {
			rest, inModule := strings.CutPrefix(im, module+"/")
			if !inModule {
				continue // checked by TestStandardLibraryOnly
			}
			q := dagName(rest)
			allowed := slices.Contains(direct[f.pkg], q)
			if f.test {
				allowed = allowed || closure[f.pkg][q] || q == f.pkg || testEdge(f.pkg, q)
			}
			if !allowed {
				t.Errorf("%s imports %s: edge %s -> %s is not in the DAG", f.rel, im, f.pkg, q)
			}
		}
	}
}

// testEdge reports whether TestEdges lets p's tests import q.
func testEdge(p, q string) bool {
	for _, e := range TestEdges {
		if e.Pkg == p && slices.Contains(e.Imports, q) {
			return true
		}
	}
	return false
}

// TestTestEdgesAreSafe: each test-only edge names DAG packages, is not already
// a DAG edge, and its target does not reach its source, so a test import can
// never close a cycle.
func TestTestEdgesAreSafe(t *testing.T) {
	direct, closure := dagTable()
	for _, e := range TestEdges {
		if _, ok := direct[e.Pkg]; !ok {
			t.Errorf("test edge from %s, which is not in the DAG", e.Pkg)
		}
		for _, q := range e.Imports {
			if _, ok := direct[q]; !ok {
				t.Errorf("test edge %s -> %s names a package not in the DAG", e.Pkg, q)
			}
			if closure[e.Pkg][q] {
				t.Errorf("test edge %s -> %s is already reachable; drop it", e.Pkg, q)
			}
			if closure[q][e.Pkg] || q == e.Pkg {
				t.Errorf("test edge %s -> %s closes a cycle", e.Pkg, q)
			}
		}
	}
}

// TestStandardLibraryOnly: no third-party module, no cgo.
func TestStandardLibraryOnly(t *testing.T) {
	for _, f := range files(t) {
		for _, im := range f.imports {
			if im == "C" {
				t.Errorf("%s uses cgo", f.rel)
				continue
			}
			first, _, _ := strings.Cut(im, "/")
			if first != module && strings.Contains(first, ".") {
				t.Errorf("%s imports %s, which is not the standard library", f.rel, im)
			}
		}
	}
	mod, err := os.ReadFile(filepath.Join(root(t), "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(mod), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "require") || strings.HasPrefix(line, "toolchain") || strings.HasPrefix(line, "replace") {
			t.Errorf("go.mod: %q (standard library only, no toolchain line)", line)
		}
	}
}

// TestNoUnsafe: no file under the module imports unsafe.
func TestNoUnsafe(t *testing.T) {
	for _, f := range files(t) {
		if slices.Contains(f.imports, "unsafe") {
			t.Errorf("%s imports unsafe", f.rel)
		}
	}
}

// TestNoReflectInPasses: compiler code does not use reflection; tests
// may.
func TestNoReflectInPasses(t *testing.T) {
	for _, f := range files(t) {
		if !f.test && slices.Contains(f.imports, "reflect") {
			t.Errorf("%s imports reflect", f.rel)
		}
	}
}

// TestFilesAreSmallAndFormatted: at most MaxFileLines lines, and gofmt
// leaves every file unchanged.
func TestFilesAreSmallAndFormatted(t *testing.T) {
	var unformatted []string
	for _, f := range files(t) {
		if n := bytes.Count(f.src, []byte("\n")); n > MaxFileLines {
			t.Errorf("%s has %d lines, over %d", f.rel, n, MaxFileLines)
		}
		out, err := format.Source(f.src)
		if err != nil {
			t.Errorf("%s: %v", f.rel, err)
			continue
		}
		// Strict, as gofmt -l is: a CRLF file is unformatted.
		if !bytes.Equal(out, f.src) {
			unformatted = append(unformatted, f.rel)
		}
	}
	sort.Strings(unformatted)
	for _, u := range unformatted {
		t.Errorf("%s is not gofmt-formatted", u)
	}
}
