// Why: four defects in the stat hint, the last-use marks and MaybeGC let a
// stale file hit or made GC walk too often; each must stay fixed.
// What: TestHintSymlinkNeverHits, TestHintRacyAnchoredAtStat,
// TestPutStandingRemarks and TestMaybeGCOverBudgetWithinGrace.
// How: builds the exact file states (a symbolic link, a racy stat, a standing
// entry, a GC kept over budget by Grace) with a fake clock.

package cache

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// DirEntry.Info is an Lstat, so a symbolic link's stat record is
// the link's and an edit of its target changes none of it. Such a stat never
// hits, and recording one drops the record the path had.
func TestHintSymlinkNeverHits(t *testing.T) {
	t.Parallel()
	clk := newClock()
	s := openTest(t, Options{Now: clk.Now})
	h := s.LoadHints(t.TempDir())
	m := clk.Now().Add(-time.Hour)
	regular := fakeInfo{size: 47, mtime: m}
	link := fakeInfo{size: 47, mtime: m, mode: os.ModeSymlink | 0o777}

	// Every system: the rule, on a stat that says symlink.
	h.Record("a.mk", regular, clk.Now(), testKey(1))
	if _, ok := h.Lookup("a.mk", regular); !ok {
		t.Fatal("a regular file's old record missed")
	}
	if _, ok := h.Lookup("a.mk", link); ok {
		t.Error("a symlink's stat hit a record with equal fields")
	}
	h.Record("a.mk", link, clk.Now(), testKey(2))
	if _, ok := h.Lookup("a.mk", regular); ok {
		t.Error("recording a symlink's stat kept the path's old record")
	}
	if _, ok := h.Lookup("a.mk", link); ok {
		t.Error("a recorded symlink's stat hit")
	}
	for _, mode := range []os.FileMode{os.ModeIrregular, os.ModeNamedPipe, os.ModeDevice} {
		h.Record("b.mk", fakeInfo{size: 4, mtime: m, mode: mode}, clk.Now(), testKey(3))
		if _, ok := h.Lookup("b.mk", fakeInfo{size: 4, mtime: m, mode: mode}); ok {
			t.Errorf("a stat of mode %v hit", mode)
		}
	}

	// A real link, where the system lets this account make one (Windows
	// needs a privilege or developer mode; the rule above is the assertion).
	src := t.TempDir()
	target := filepath.Join(t.TempDir(), "real.mk")
	if err := os.WriteFile(target, []byte("aaaa"), 0o644); err != nil {
		t.Fatal(err)
	}
	os.Chtimes(target, m, m)
	if err := os.Symlink(target, filepath.Join(src, "a.mk")); err != nil {
		t.Logf("no symlink on this system for this account (%v): the stat-mode cases above stand", err)
		return
	}
	statAt := clk.Now()
	fi := dirInfo(t, src, "a.mk")
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("DirEntry.Info followed the link: mode %v", fi.Mode())
	}
	hs := s.LoadHints(src)
	hs.Record("a.mk", fi, statAt, testKey(1))
	if _, ok := hs.Lookup("a.mk", fi); ok {
		t.Error("a symlink's unchanged stat hit")
	}
	if err := os.WriteFile(target, []byte("bbbbbbbbbbbb"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, ok := hs.Lookup("a.mk", dirInfo(t, src, "a.mk")); ok {
		t.Errorf("a hint hit with the old content hash %v after the symlink's target changed", got)
	}
}

// The racy rule is anchored at the time of the stat, not at the
// Record call, so a same-size edit under a 2-second clock between the stat and
// a late Record is still caught.
func TestHintRacyAnchoredAtStat(t *testing.T) {
	t.Parallel()
	clk := newClock()
	s := openTest(t, Options{Now: clk.Now})
	src := t.TempDir()
	h := s.LoadHints(src)
	T := clk.Now()
	fi := fakeInfo{size: 4, mtime: T} // a coarse clock: an edit at T+1s keeps mtime T
	statAt := s.Now()
	clk.Add(2500 * time.Millisecond)
	h.Record("a.mk", fi, statAt, testKey(1))
	if _, ok := h.Lookup("a.mk", fi); ok {
		t.Error("a record of a stat taken within the racy window hit because Record ran late")
	}
	if err := h.Save(); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.LoadHints(src).Lookup("a.mk", fi); ok {
		t.Error("a saved racy record hit")
	}
	// The same file stat-ed 2.5 s after its mtime is trusted: the rule is
	// anchored, not always racy.
	h.Record("a.mk", fi, clk.Now(), testKey(1))
	if got, ok := h.Lookup("a.mk", fi); !ok || got != testKey(1) {
		t.Error("a record of a stat taken outside the racy window missed")
	}
	// A statAt ahead of the store's clock is clamped to it.
	clk.Add(-1500 * time.Millisecond) // the clock reads T+1s
	h.Record("a.mk", fi, T.Add(time.Hour), testKey(1))
	if _, ok := h.Lookup("a.mk", fi); ok {
		t.Error("a statAt ahead of the store's clock made a racy record trusted")
	}
}

// A Put that finds its content-addressed entry standing marks it,
// as a Get hit would, so the next GC does not evict the entry the driver is
// about to name from a new manifest candidate. PutFile does the same.
func TestPutStandingRemarks(t *testing.T) {
	t.Parallel()
	clk := newClock()
	s := openTest(t, Options{Now: clk.Now})
	key := testKey(1)
	if err := s.Put(KindFunc, key, []byte("x")); err != nil {
		t.Fatal(err)
	}
	p := s.entryPath(KindFunc, key)
	old := clk.Now().Add(-3 * time.Hour)
	os.Chtimes(p, old, old)
	if err := s.Put(KindFunc, key, []byte("x")); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(p); err != nil || !fi.ModTime().Equal(clk.Now()) {
		t.Errorf("Put of a standing entry did not mark it (%v, %v)", fi, err)
	}
	src := writeSrc(t, t.TempDir(), []byte("obj"))
	if err := s.PutFile(KindObj, key, src); err != nil {
		t.Fatal(err)
	}
	obj := s.entryPath(KindObj, key)
	os.Chtimes(obj, old, old)
	os.Chtimes(obj+".sum", old, old)
	if err := s.PutFile(KindObj, key, writeSrc(t, t.TempDir(), []byte("obj"))); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GC(0); err != nil {
		t.Fatal(err)
	}
	if !present(s, KindFunc, key) {
		t.Error("an entry just Put (found standing) was evicted as least recently used")
	}
	if _, ok, _ := s.FilePath(KindObj, key); !ok {
		t.Error("a raw entry just put with PutFile (found standing) was evicted as least recently used")
	}
}

// A GC that Grace kept above the budget does not make every later
// MaybeGC walk; the next walk over the budget comes when a kept entry can
// leave Grace (or GCInterval passes), and the stamp alone carries that.
func TestMaybeGCOverBudgetWithinGrace(t *testing.T) {
	t.Parallel()
	clk := newClock()
	s := openTest(t, Options{Now: clk.Now, Budget: 1000})
	stage(t, s, clk, 5, 1000, func(int) time.Duration { return 0 })
	for i := 0; i < 5; i++ {
		if err := s.MaybeGC(); err != nil {
			t.Fatal(err)
		}
		clk.Add(time.Minute)
	}
	if w := s.walks.Load(); w != 1 {
		t.Errorf("%d walks in 5 minutes of MaybeGC calls over budget within Grace, want 1", w)
	}
	for i := 0; i < 5; i++ {
		if !present(s, KindFunc, testKey(i)) {
			t.Errorf("entry %d within Grace was evicted", i)
		}
	}
	// GCInterval still walks.
	clk.Add(GCInterval)
	s.MaybeGC()
	if w := s.walks.Load(); w != 2 {
		t.Errorf("MaybeGC after GCInterval did not walk (%d)", w)
	}

	// Entries 10 minutes short of leaving Grace: no walk for 10 minutes, then
	// a fresh process (nothing put) walks on the stamped total and evicts.
	clk2 := newClock()
	s2 := openTest(t, Options{Now: clk2.Now, Budget: 1000})
	stage(t, s2, clk2, 5, 1000, func(int) time.Duration { return Grace - 10*time.Minute })
	s2.MaybeGC()
	clk2.Add(5 * time.Minute)
	s2.MaybeGC()
	if w := s2.walks.Load(); w != 1 {
		t.Errorf("walked %d times before a kept entry could leave Grace, want 1", w)
	}
	clk2.Add(6 * time.Minute)
	s3, _ := Open(filepath.Dir(s2.Root()), Options{Now: clk2.Now, Budget: 1000})
	s3.MaybeGC()
	if w := s3.walks.Load(); w != 1 {
		t.Errorf("a fresh process over the stamped budget did not walk once entries left Grace (%d)", w)
	}
	for i := 0; i < 5; i++ {
		if present(s3, KindFunc, testKey(i)) {
			t.Errorf("entry %d past Grace survived the walk", i)
		}
	}

	// A stamp of one number (the old format) over budget reads as not
	// blocked: it walks.
	stamp := filepath.Join(s3.Root(), stampFile)
	if err := os.WriteFile(stamp, []byte("5000"), 0o644); err != nil {
		t.Fatal(err)
	}
	os.Chtimes(stamp, clk2.Now(), clk2.Now())
	s3.MaybeGC()
	if w := s3.walks.Load(); w != 2 {
		t.Errorf("an old over-budget stamp did not walk (%d)", w)
	}
}
