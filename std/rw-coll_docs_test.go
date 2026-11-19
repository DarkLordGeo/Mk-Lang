// Why: a release call's doc must name the compile-time error its bound gives,
// not the run-time panic the bound replaced; and std must ship no build cache.
// What: TestRwColl_ReleaseBoundDocsNameE0701 and
// TestRwColl_NoBuildCache.
// How: reads the `///` docs of the calls internal/check/freebound.go judges,
// and looks for .mk-cache in std/coll, std/mem and std/db.

package std

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// rwCollRowPat is one releaseRows entry of internal/check/freebound.go:
// module, owner type and method.
var rwCollRowPat = regexp.MustCompile(`\{"(std\.coll|std\.mem)", "(\w+)", "(\w+)"`)

// rwCollDoc is the `///` block right above the first line of a module's
// files that match accepts, with that file's name; found is false when no
// line does.
func rwCollDoc(files map[string][]string, match func(string) bool) (doc, at string, found bool) {
	for name, lines := range files {
		for i, l := range lines {
			if !match(l) {
				continue
			}
			j := i
			for j > 0 && strings.HasPrefix(lines[j-1], "///") {
				j--
			}
			return strings.Join(lines[j:i], "\n"), name, true
		}
	}
	return "", "", false
}

// TestRwColl_ReleaseBoundDocsNameE0701: each releaseRows call of
// std.coll and std.mem, and Pool.Free (which has its own bound), is documented
// as E0701 at the call, and no such doc promises a code 8 panic.
func TestRwColl_ReleaseBoundDocsNameE0701(t *testing.T) {
	t.Parallel()
	src, err := os.ReadFile(filepath.Join("..", "internal", "check", "freebound.go"))
	if err != nil {
		t.Fatal(err)
	}
	type row struct{ mod, owner, method string }
	rows := []row{{"std.mem", "Pool", "Free"}}
	for _, m := range rwCollRowPat.FindAllStringSubmatch(string(src), -1) {
		rows = append(rows, row{m[1], m[2], m[3]})
	}
	mods := map[string]map[string][]string{}
	for _, r := range rows {
		if mods[r.mod] != nil {
			continue
		}
		dir := strings.TrimPrefix(r.mod, "std.")
		names, err := filepath.Glob(filepath.Join(dir, "*.mk"))
		if err != nil || len(names) == 0 {
			t.Fatalf("%s: no files (%v)", r.mod, err)
		}
		mods[r.mod] = map[string][]string{}
		for _, n := range names {
			b, err := os.ReadFile(n)
			if err != nil {
				t.Fatal(err)
			}
			mods[r.mod][n] = strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n")
		}
	}
	code8 := regexp.MustCompile(`code 8|panics? \(8\)`)
	seen := 0
	for _, r := range rows {
		if r.mod == "std.coll" || r.mod == "std.mem" {
			seen++
		}
		// A container's Free is written inside its impl of Free, whose
		// doc sits above the impl line.
		match := func(l string) bool {
			return strings.HasPrefix(l, "export "+r.owner+"[") && strings.Contains(l, "]."+r.method+"(")
		}
		if r.method == "Free" && r.owner != "Pool" {
			match = func(l string) bool {
				return strings.HasPrefix(l, "impl[") && strings.Contains(l, "] Free for "+r.owner+"[")
			}
		}
		doc, at, found := rwCollDoc(mods[r.mod], match)
		if !found || doc == "" {
			t.Errorf("%s.%s.%s: no /// doc found", r.mod, r.owner, r.method)
			continue
		}
		if !strings.Contains(doc, "E0701") {
			t.Errorf("%s: %s.%s's doc does not name E0701:\n%s", at, r.owner, r.method, doc)
		}
		if code8.MatchString(doc) {
			t.Errorf("%s: %s.%s's doc promises the code 8 panic the bound replaced:\n%s", at, r.owner, r.method, doc)
		}
	}
	if seen < 10 {
		t.Fatalf("read %d release rows of std.coll and std.mem from freebound.go, want at least 10", seen)
	}
}

// TestRwColl_NoBuildCache: std/coll, std/mem and std/db hold no
// .mk-cache (an mk run from inside std leaves one, named for a process).
func TestRwColl_NoBuildCache(t *testing.T) {
	t.Parallel()
	for _, dir := range []string{"coll", "mem", "db"} {
		err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() && d.Name() == ".mk-cache" {
				t.Errorf("%s: a build cache in std; run mk from outside std or with --no-cache, and remove it", filepath.ToSlash(path))
				return filepath.SkipDir
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}
