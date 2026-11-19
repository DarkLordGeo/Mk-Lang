// Why: pkg.sum vouches for every fetched version, so its parse, order,
// checks and write must be exact.
// What: TestSum_ParseOrderWrite, TestSum_Checks, TestSum_ParseRefusals and
// TestSum_URLWithQueryNotAdded.
// How: parses and writes pkg.sum texts with made-up hashes (sumHash) and
// compares the lines, the refusals and the file on disk.

package driver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	sumCommitA = "a77b301e5c0e0d6b8e0f9c1b2a3d4e5f60718293"
	sumCommitB = "3f2a9c1e0b7d6c5a4f3e2d1c0b9a8f7e6d5c4b3a"
)

func sumHash(prefix, c string) string { return prefix + strings.Repeat(c, 64) }

func TestSum_ParseOrderWrite(t *testing.T) {
	lines := []string{
		"github.com/someone/toml v0.9.3 " + sumCommitB + " " + sumHash("tree-sha256:", "4"),
		"github.com/someone/golden v2.0.0 " + sumCommitA + " " + sumHash("tree-sha256:", "c"),
		"github.com/someone/toml v0.9.1/pkg.mk " + sumCommitA + " " + sumHash("sha256:", "9"),
		"github.com/someone/toml v0.10.0 " + sumCommitA + " " + sumHash("tree-sha256:", "1"),
		"github.com/someone/toml v0.9.3/pkg.mk " + sumCommitB + " " + sumHash("sha256:", "2"),
		"https://example.com/ini.tar.gz - - " + sumHash("tree-sha256:", "e"),
	}
	s, err := parsePkgSum(strings.Join(lines, "\n") + "\n")
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{lines[1], lines[2], lines[0], lines[4], lines[3], lines[5]}, "\n") + "\n"
	if got := s.text(); got != want {
		t.Errorf("order:\n%s\nwant:\n%s", got, want)
	}
	dir := t.TempDir()
	if err := writePkgSum(dir, s); err != nil {
		t.Fatal(err)
	}
	back, err := readPkgSum(dir)
	if err != nil || back.text() != want {
		t.Fatalf("round trip: %v\n%s", err, back.text())
	}
	ents, _ := os.ReadDir(dir)
	if len(ents) != 1 {
		t.Errorf("a temporary file was left: %v", ents)
	}
	// An empty pkg.sum is never created.
	empty := t.TempDir()
	if err := writePkgSum(empty, newPkgSum()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(empty, sumFile)); !os.IsNotExist(err) {
		t.Errorf("an empty build wrote pkg.sum: %v", err)
	}
}

func TestSum_Checks(t *testing.T) {
	s := newPkgSum()
	l := sumLine{Source: "github.com/someone/toml", Version: "0.9.3", Commit: sumCommitB, Hash: sumHash("tree-sha256:", "4")}
	if r, _ := s.check(l); r != "" || !s.add(l) || s.add(l) {
		t.Fatalf("first add: reason %q", r)
	}
	moved := l
	moved.Commit = sumCommitA
	if r, old := s.check(moved); r != "tag-moved" || old.Commit != sumCommitB {
		t.Errorf("moved tag: %q", r)
	}
	bad := l
	bad.Hash = sumHash("tree-sha256:", "5")
	if r, _ := s.check(bad); r != "sum-mismatch" {
		t.Errorf("other hash: %q", r)
	}
	if _, ok := s.get("github.com/someone/toml", "0.9.3", false); !ok {
		t.Error("get")
	}
	if !s.keep(func(sumLine) bool { return false }) || len(s.lines) != 0 {
		t.Error("keep")
	}
}

func TestSum_ParseRefusals(t *testing.T) {
	for _, tc := range []struct{ name, line string }{
		{"three fields", "github.com/x/y v1.0.0 " + sumCommitA},
		{"no v", "github.com/x/y 1.0.0 " + sumCommitA + " " + sumHash("tree-sha256:", "a")},
		{"short commit", "github.com/x/y v1.0.0 abc " + sumHash("tree-sha256:", "a")},
		{"tree hash on pkg.mk", "github.com/x/y v1.0.0/pkg.mk " + sumCommitA + " " + sumHash("tree-sha256:", "a")},
		{"archive hash on a tree", "github.com/x/y v1.0.0 " + sumCommitA + " " + sumHash("sha256:", "a")},
		{"bad source", "https://github.com/x/y v1.0.0 " + sumCommitA + " " + sumHash("tree-sha256:", "a")},
		{"url with commit", "https://example.com/a.tar.gz - " + sumCommitA + " " + sumHash("tree-sha256:", "a")},
		{"user information", "https://me:secret@example.com/a.tar.gz - - " + sumHash("tree-sha256:", "a")},
		// A token in a query or fragment never reaches pkg.sum.
		{"query", "https://example.com/a.tar.gz?token=secret - - " + sumHash("tree-sha256:", "a")},
		{"fragment", "https://example.com/a.tar.gz#secret - - " + sumHash("tree-sha256:", "a")},
		{"two spaces", "github.com/x/y  v1.0.0 " + sumCommitA + " " + sumHash("tree-sha256:", "a")},
	} {
		if _, err := parsePkgSum("\n" + tc.line + "\n"); err == nil || !strings.HasPrefix(err.Error(), "pkg.sum:2:") {
			t.Errorf("%s: %v", tc.name, err)
		}
	}
	two := "github.com/x/y v1.0.0 " + sumCommitA + " " + sumHash("tree-sha256:", "a") + "\n" +
		"github.com/x/y v1.0.0 " + sumCommitA + " " + sumHash("tree-sha256:", "b") + "\n"
	if _, err := parsePkgSum(two); err == nil {
		t.Error("two lines for one version were accepted")
	}
}

// A url with a query is never recorded.
func TestSum_URLWithQueryNotAdded(t *testing.T) {
	s := newPkgSum()
	if s.add(sumLine{Source: "https://example.com/a.tar.gz?token=x", Hash: sumHash("tree-sha256:", "a")}) || len(s.lines) != 0 {
		t.Error("a url with a query was recorded")
	}
}
