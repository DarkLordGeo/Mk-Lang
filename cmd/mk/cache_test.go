// Why: the cache and profile commands are scripted by users, so their exit
// statuses and streams are part of the interface.
// What: TestCacheCommand (mk cache size | clean) and TestProfileCommand
// (mk profile merge).
// How: runs the commands with a temporary cache and checks status,
// stdout and stderr.

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mk/internal/pgo"
	"mk/internal/source"
)

// TestCacheCommand and TestProfileCommand: mk cache size | clean and mk
// profile merge on the command line (exit statuses and streams).

func TestCacheCommand(t *testing.T) {
	cache := filepath.Join(t.TempDir(), "c")
	t.Setenv("MK_CACHE", cache)
	root := writeFiles(t, map[string]string{"main.mk": "export Main(sys: &System): int {\n    return 0\n}\n"})
	if st, _, errOut := mk("cache"); st != 2 || !strings.Contains(errOut, "usage: mk cache") {
		t.Errorf("mk cache: status %d stderr %q", st, errOut)
	}
	if st, _, errOut := mk("cache", "frob"); st != 2 || !strings.Contains(errOut, "usage: mk cache") {
		t.Errorf("mk cache frob: status %d stderr %q", st, errOut)
	}
	st, out, errOut := mk("cache", "size", root)
	if st != 0 || !strings.Contains(out, "total    0 bytes") || errOut != "" {
		t.Errorf("mk cache size: status %d stdout %q stderr %q", st, out, errOut)
	}
	exe := filepath.Join(cache, "v1", "bin", "k", "a.exe") // the store's Dir("bin")
	os.MkdirAll(filepath.Dir(exe), 0o755)
	os.WriteFile(exe, make([]byte, 10), 0o644)
	if st, out, _ := mk("cache", "size", root); st != 0 || !strings.Contains(out, "bin      1 entries 10 bytes") {
		t.Errorf("mk cache size with a bin: status %d stdout %q", st, out)
	}
	if st, _, errOut := mk("cache", "clean", root); st != 0 || errOut != "" {
		t.Errorf("mk cache clean: status %d stderr %q", st, errOut)
	}
	if _, err := os.Stat(exe); !os.IsNotExist(err) {
		t.Error("mk cache clean left an executable under bin")
	}
	if st, _, _ := mk("cache", "size", root, "extra"); st != 2 {
		t.Errorf("two paths: status %d, want 2", st)
	}
}

func TestProfileCommand(t *testing.T) {
	if st, _, errOut := mk("profile"); st != 2 || !strings.Contains(errOut, "usage: mk profile merge") {
		t.Errorf("mk profile: status %d stderr %q", st, errOut)
	}
	if st, _, _ := mk("profile", "merge", "-o", filepath.Join(t.TempDir(), "o.mkprof")); st != 2 {
		t.Errorf("no inputs: status %d, want 2", st)
	}
	bad := filepath.Join(t.TempDir(), "bad.mkprof")
	os.WriteFile(bad, []byte("junk"), 0o644)
	if st, _, errOut := mk("profile", "merge", bad, "-o", filepath.Join(t.TempDir(), "o.mkprof")); st != 1 || !strings.Contains(errOut, "bad.mkprof") {
		t.Errorf("malformed input: status %d stderr %q", st, errOut)
	}
	// Two builds' records of one function: merged, exit 0, one warning.
	dir := t.TempDir()
	var ins []string
	for i, p := range []*pgo.Profile{
		{Records: []pgo.Record{{Key: "mk.app.Main", Checksum: source.Hash128{1}, Counts: []uint64{3}}}},
		{Records: []pgo.Record{{Key: "mk.app.Main", Checksum: source.Hash128{2}, Counts: []uint64{3, 4}}}},
	} {
		b, err := p.Bytes()
		if err != nil {
			t.Fatal(err)
		}
		in := filepath.Join(dir, fmt.Sprintf("p%d.mkprof", i))
		os.WriteFile(in, b, 0o644)
		ins = append(ins, in)
	}
	out := filepath.Join(dir, "o.mkprof")
	st, _, errOut := mk(append([]string{"profile", "merge", "-o", out}, ins...)...)
	if st != 0 || strings.Count(errOut, "mk profile merge: warning: mk.app.Main has records for 2 checksums") != 1 {
		t.Errorf("two checksums: status %d stderr %q", st, errOut)
	}
	if _, err := os.Stat(out); err != nil {
		t.Errorf("two checksums: output not written: %v", err)
	}
}
