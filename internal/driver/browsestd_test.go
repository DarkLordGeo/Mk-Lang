// Why: an editor browsing std's own sources must see std's unexported items,
// while an ordinary check must not.
// What: TestBrowseStd, a check of a file that imports a std module, with the
// repository's std as the standard library, as mk lsp checks a std tree.
// How: with Config.BrowseStd the query index and UsesOf see std's unexported
// items and uses; without it they do not.

package driver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBrowseStd(t *testing.T) {
	std, err := filepath.Abs(filepath.Join("..", "..", "std"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(std, "compress", "inflate.mk")); err != nil {
		t.Skip("no std/compress in this tree")
	}
	dir := t.TempDir()
	root := filepath.Join(dir, "browse.mk")
	if err := os.WriteFile(root, []byte("import std.compress\n\nexport Main(sys: &System) {\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	check := func(browse bool) *Session {
		s, err := NewSession(Config{Root: root, StdDir: std, CacheDir: filepath.Join(dir, "cache"), NoCache: true, KeepSyntax: true, BrowseStd: browse})
		if err != nil {
			t.Fatal(err)
		}
		s.Check()
		return s
	}

	s := check(true)
	it, ok := s.Lookup("std.compress.Run")
	if !ok || it.Def.Path != "std/compress/inflate.mk" || it.Exported {
		t.Errorf("Lookup(std.compress.Run) = %+v, %v; want the unexported Run of std/compress/inflate.mk", it, ok)
	}
	a, err := s.QueryChecked("shape", []string{"std.compress.Dec"})
	if err != nil || a == nil || !a.Found || !strings.Contains(strings.Join(a.Answer, "\n"), "size ") {
		t.Errorf("shape std.compress.Dec = %+v, %v; want its size and align", a, err)
	}
	if _, ok := s.Lookup("std.raw.FenceOrd"); !ok {
		t.Error("Lookup(std.raw.FenceOrd): an internal module's item is not found")
	}
	uses := s.UsesOf("std.compress.Run")
	found := false
	for _, u := range uses {
		found = found || u.Path == "std/compress/compress.mk"
	}
	if !found {
		t.Errorf("UsesOf(std.compress.Run) = %v; want its call in std/compress/compress.mk", uses)
	}

	s = check(false)
	if _, ok := s.Lookup("std.compress.Run"); ok {
		t.Error("without BrowseStd, Lookup finds an unexported std item")
	}
	if _, ok := s.Lookup("std.raw.FenceOrd"); ok {
		t.Error("without BrowseStd, Lookup finds an item of an internal module")
	}
}
