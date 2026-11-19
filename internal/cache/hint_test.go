// Why: the stat hint lets a warm build skip hashing unchanged files, so it
// must never report a changed file as unchanged where the system can tell.
// What: tests of the stat hint (racy entries, same-size edits, no opens, never
// in a key) and the executable's hash memo.
// How: edits files in place with restored mtimes and fake FileInfo values,
// and checks hits and misses per system.

package cache

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"mk/internal/source"
)

// fakeInfo is an os.FileInfo with no system data (no change identity). A
// zero mode is a regular file, 0o644.
type fakeInfo struct {
	size  int64
	mtime time.Time
	mode  os.FileMode
}

func (f fakeInfo) Name() string { return "f" }
func (f fakeInfo) Size() int64  { return f.size }
func (f fakeInfo) Mode() os.FileMode {
	if f.mode == 0 {
		return 0o644
	}
	return f.mode
}
func (f fakeInfo) ModTime() time.Time { return f.mtime }
func (f fakeInfo) IsDir() bool        { return false }
func (f fakeInfo) Sys() any           { return nil }

// dirInfo returns the FileInfo of name in dir as one os.ReadDir gives it.
func dirInfo(t *testing.T, dir, name string) os.FileInfo {
	t.Helper()
	ds, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range ds {
		if d.Name() == name {
			fi, err := d.Info()
			if err != nil {
				t.Fatal(err)
			}
			return fi
		}
	}
	t.Fatalf("%s not in %s", name, dir)
	return nil
}

// writeAged writes content to dir/name with its mtime set to mtime.
func writeAged(t *testing.T, dir, name, content string, mtime time.Time) {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(p, mtime, mtime); err != nil {
		t.Fatal(err)
	}
}

func TestHintRacy(t *testing.T) {
	t.Parallel()
	clk := newClock()
	s := openTest(t, Options{Now: clk.Now})
	src := t.TempDir()
	m := clk.Now().Add(-time.Hour)
	writeAged(t, src, "a.mk", "aaaa", m)
	fi := dirInfo(t, src, "a.mk")
	h := s.LoadHints(src)
	// A stat taken within 2 seconds of the file's mtime: never a hit.
	for _, d := range []time.Duration{0, time.Second, 2 * time.Second} {
		h.Record("a.mk", fi, m.Add(d), testKey(1))
		if _, ok := h.Lookup("a.mk", fi); ok {
			t.Errorf("a record of a stat taken %v after the mtime hit", d)
		}
	}
	h.Record("a.mk", fi, m.Add(2*time.Second+time.Millisecond), testKey(1))
	if got, ok := h.Lookup("a.mk", fi); !ok || got != testKey(1) {
		t.Errorf("a record of a stat outside the racy window missed")
	}
	// A zero statAt never hits.
	h.Record("a.mk", fi, time.Time{}, testKey(1))
	if _, ok := h.Lookup("a.mk", fi); ok {
		t.Error("a record with a zero statAt hit")
	}
	// The racy flag survives a save and a load.
	h.Record("a.mk", fi, m.Add(time.Second), testKey(1))
	if err := h.Save(); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.LoadHints(src).Lookup("a.mk", fi); ok {
		t.Error("a saved racy record hit")
	}
}

func TestHintSameSizeEdit(t *testing.T) {
	t.Parallel()
	s := openTest(t, Options{})
	src := t.TempDir()
	h := s.LoadHints(src)

	// Both systems: an edit within the racy window, mtime restored.
	now := time.Now()
	writeAged(t, src, "racy.mk", "aaaa", now)
	statAt := s.Now()
	fi := dirInfo(t, src, "racy.mk")
	h.Record("racy.mk", fi, statAt, testKey(1))
	writeAged(t, src, "racy.mk", "bbbb", fi.ModTime())
	if _, ok := h.Lookup("racy.mk", dirInfo(t, src, "racy.mk")); ok {
		t.Error("a same-size edit within the racy window hit")
	}

	// An edit with the mtime restored outside the racy window.
	old := now.Add(-time.Hour).Truncate(time.Second)
	writeAged(t, src, "old.mk", "aaaa", old)
	statAt = s.Now()
	fi = dirInfo(t, src, "old.mk")
	h.Record("old.mk", fi, statAt, testKey(2))
	if _, ok := h.Lookup("old.mk", fi); !ok {
		t.Fatal("an unchanged old file missed")
	}
	time.Sleep(20 * time.Millisecond) // past any ctime granularity
	writeAged(t, src, "old.mk", "bbbb", old)
	_, hit := h.Lookup("old.mk", dirInfo(t, src, "old.mk"))
	switch runtime.GOOS {
	case "linux":
		if hit {
			t.Error("linux: a same-size edit with a restored mtime was not caught by ctime")
		}
	case "windows":
		// The documented limit: nothing the standard library
		// reads without an open changes on an in-place write, so this hits.
		if !hit {
			t.Error("windows: the documented restored-mtime miss changed; update section 4.11 and I-13")
		}
	}

	// A delete and recreate outside the tunnelling window (Windows: a new
	// creation time; Linux: a new inode and ctime).
	writeAged(t, src, "new.mk", "aaaa", old)
	statAt = s.Now()
	fi = dirInfo(t, src, "new.mk")
	h.Record("new.mk", fi, statAt, testKey(3))
	keep := filepath.Join(src, "keep.mk")
	os.WriteFile(keep, nil, 0o644) // holds the inode number apart from reuse
	os.Remove(filepath.Join(src, "new.mk"))
	time.Sleep(20 * time.Millisecond)
	writeAged(t, src, "new.mk", "bbbb", old)
	setCreationLater(t, filepath.Join(src, "new.mk"), fi)
	if _, ok := h.Lookup("new.mk", dirInfo(t, src, "new.mk")); ok && runtime.GOOS != "darwin" {
		t.Error("a delete and recreate with an equal size and mtime was not caught")
	}
}

func TestHintLookupOpensNothing(t *testing.T) {
	t.Parallel()
	s := openTest(t, Options{})
	src := t.TempDir()
	writeAged(t, src, "a.mk", "aaaa", time.Now().Add(-time.Hour))
	fi := dirInfo(t, src, "a.mk")
	h := s.LoadHints(src)
	h.Record("a.mk", fi, s.Now(), testKey(1))
	if err := os.Rename(filepath.Join(src, "a.mk"), filepath.Join(src, "gone")); err != nil {
		t.Fatal(err)
	}
	if got, ok := h.Lookup("a.mk", fi); !ok || got != testKey(1) {
		t.Error("Lookup touched the path: it missed once the file was renamed away")
	}
}

func TestHintNeverInKey(t *testing.T) {
	t.Parallel()
	s := openTest(t, Options{})
	src := t.TempDir()
	writeAged(t, src, "a.mk", "aaaa", time.Now().Add(-time.Hour))
	fi := dirInfo(t, src, "a.mk")
	h := s.LoadHints(src)
	h.Record("a.mk", fi, s.Now(), testKey(1))
	if err := h.Save(); err != nil {
		t.Fatal(err)
	}
	if got, ok := s.LoadHints(src).Lookup("a.mk", fi); !ok || got != testKey(1) {
		t.Fatal("a saved hint did not load")
	}
	// Another package root has its own file.
	if _, ok := s.LoadHints(t.TempDir()).Lookup("a.mk", fi); ok {
		t.Error("hints leaked between package roots")
	}
	// A corrupt hint file is an empty Hints: the cost is a rehash, never a
	// wrong hash.
	ds, _ := os.ReadDir(filepath.Join(s.Root(), hintsDir))
	for _, d := range ds {
		p := filepath.Join(s.Root(), hintsDir, d.Name())
		b, _ := os.ReadFile(p)
		b[len(b)/2] ^= 0x40
		os.WriteFile(p, b, 0o644)
	}
	h2 := s.LoadHints(src)
	if _, ok := h2.Lookup("a.mk", fi); ok {
		t.Error("a corrupt hint file hit")
	}
	h2.Record("a.mk", fi, s.Now(), testKey(1))
	if err := h2.Save(); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.LoadHints(src).Lookup("a.mk", fi); !ok {
		t.Error("Save did not replace a corrupt hint file")
	}
	// Save with nothing recorded writes nothing; a ReadOnly store never writes.
	ro, _ := Open(filepath.Dir(s.Root()), Options{ReadOnly: true})
	hr := ro.LoadHints(src)
	hr.Record("b.mk", fi, s.Now(), testKey(2))
	if err := hr.Save(); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.LoadHints(src).Lookup("b.mk", fi); ok {
		t.Error("a ReadOnly store saved hints")
	}
}

func TestFileHashMemo(t *testing.T) {
	t.Parallel()
	clk := newClock()
	s := openTest(t, Options{Now: clk.Now})
	dir := t.TempDir()
	exe := filepath.Join(dir, "mk.exe")
	content := string(testPayload(1, 200_000))
	writeAged(t, dir, "mk.exe", content, clk.Now().Add(-time.Hour))
	h, err := s.FileHash(exe)
	if err != nil {
		t.Fatal(err)
	}
	if want := source.HashBytes("mk.exe.v1", []byte(content)); h != want {
		t.Fatal("FileHash is not HashBytes under mk.exe.v1")
	}
	if n := s.reads.Load(); n != 1 {
		t.Fatalf("reads = %d, want 1", n)
	}
	// Unchanged stat: no read, also from a fresh process.
	s2, _ := Open(filepath.Dir(s.Root()), Options{Now: clk.Now})
	for _, st := range []*Store{s, s2} {
		if h2, _ := st.FileHash(exe); h2 != h {
			t.Error("memoized hash differs")
		}
	}
	if s.reads.Load() != 1 || s2.reads.Load() != 0 {
		t.Errorf("an unchanged file was reread: %d, %d", s.reads.Load(), s2.reads.Load())
	}
	// Changed size: rehash.
	writeAged(t, dir, "mk.exe", content+"x", clk.Now().Add(-time.Hour))
	if h3, _ := s.FileHash(exe); h3 == h || s.reads.Load() != 2 {
		t.Errorf("a changed size did not rehash (reads %d)", s.reads.Load())
	}
	// Changed mtime only: rehash.
	writeAged(t, dir, "mk.exe", content+"x", clk.Now().Add(-50*time.Minute))
	s.FileHash(exe)
	if s.reads.Load() != 3 {
		t.Errorf("a changed mtime did not rehash (reads %d)", s.reads.Load())
	}
	// Racy: the file's mtime is the clock's now, so every call rehashes.
	writeAged(t, dir, "mk.exe", content+"x", clk.Now())
	s.FileHash(exe)
	s.FileHash(exe)
	if s.reads.Load() != 5 {
		t.Errorf("a racy record was trusted (reads %d)", s.reads.Load())
	}
}
