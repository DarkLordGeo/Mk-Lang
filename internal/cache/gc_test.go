// Why: the cache must stay under its budget without deleting what a running
// build still needs.
// What: tests of raw files (PutFile, FilePath, RemoveFile), last-use marks,
// GC and MaybeGC, the GC lock, Stats, Clean, Dir, and the driver's bin and
// work directories.
// How: a fake clock moves entries in and out of Grace, and stores are opened
// with small budgets.

package cache

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sync"
	"testing"
	"time"

	"mk/internal/source"
)

// present reports whether an entry's file exists, without marking it as Get
// would.
func present(s *Store, kind Kind, key source.Hash) bool {
	_, err := os.Stat(s.entryPath(kind, key))
	return err == nil
}

func writeSrc(t *testing.T, dir string, content []byte) string {
	t.Helper()
	f, err := os.CreateTemp(dir, "obj-*.o")
	if err != nil {
		t.Fatal(err)
	}
	f.Write(content)
	f.Close()
	return f.Name()
}

func TestPutFileAndFilePath(t *testing.T) {
	t.Parallel()
	s := openTest(t, Options{})
	src := t.TempDir()
	key := testKey(1)
	want := testPayload(1, 100_000)
	p := writeSrc(t, src, want)
	if err := s.PutFile(KindObj, key, p); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Errorf("the source file is still there: %v", err)
	}
	got, ok, err := s.FilePath(KindObj, key)
	if err != nil || !ok {
		t.Fatalf("FilePath = %q, %v, %v", got, ok, err)
	}
	if b, _ := os.ReadFile(got); !bytes.Equal(b, want) {
		t.Error("the stored file is not the raw bytes")
	}
	// A second PutFile of the same key keeps the entry and consumes the file.
	p2 := writeSrc(t, src, want)
	if err := s.PutFile(KindObj, key, p2); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p2); !os.IsNotExist(err) {
		t.Errorf("the second source file is still there: %v", err)
	}
	// Wrong length: a miss, and the sidecar goes so the next PutFile rewrites.
	os.WriteFile(got, append(want, 'x'), 0o644)
	if _, ok, _ := s.FilePath(KindObj, key); ok {
		t.Error("a file of the wrong length hit")
	}
	p3 := writeSrc(t, src, want)
	if err := s.PutFile(KindObj, key, p3); err != nil {
		t.Fatal(err)
	}
	if got, ok, _ := s.FilePath(KindObj, key); !ok {
		t.Error("PutFile did not repair the entry")
	} else if b, _ := os.ReadFile(got); !bytes.Equal(b, want) {
		t.Error("repaired entry has the wrong bytes")
	}
	// Missing sidecar: a miss.
	os.Remove(got + ".sum")
	if _, ok, _ := s.FilePath(KindObj, key); ok {
		t.Error("a raw file without its sidecar hit")
	}
	// A damaged sidecar: a miss.
	p4 := writeSrc(t, src, want)
	s.PutFile(KindObj, key, p4)
	b, _ := os.ReadFile(got + ".sum")
	b[len(b)-1] ^= 1
	os.WriteFile(got+".sum", b, 0o644)
	if _, ok, _ := s.FilePath(KindObj, key); ok {
		t.Error("a damaged sidecar hit")
	}
	// A raw file is not a framed entry.
	if _, ok, _ := s.Get(KindObj, key); ok {
		t.Error("Get of a raw entry hit")
	}
}

func TestTouchInterval(t *testing.T) {
	t.Parallel()
	clk := newClock()
	s := openTest(t, Options{Now: clk.Now})
	key := testKey(1)
	s.Put(KindFunc, key, []byte("x"))
	p := s.entryPath(KindFunc, key)
	t0 := clk.Now()
	os.Chtimes(p, t0, t0)
	mtime := func() time.Time {
		fi, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		return fi.ModTime()
	}
	clk.Add(30 * time.Minute)
	mustGet(t, s, KindFunc, key)
	if !mtime().Equal(t0) {
		t.Errorf("a hit within TouchInterval re-marked: %v", mtime())
	}
	clk.Add(31 * time.Minute)
	mustGet(t, s, KindFunc, key)
	if m := mtime(); !m.Equal(clk.Now()) {
		t.Errorf("a hit after TouchInterval did not re-mark: %v, want %v", m, clk.Now())
	}
	// Raw files are marked on their sidecar.
	src := writeSrc(t, t.TempDir(), []byte("obj"))
	s.PutFile(KindObj, key, src)
	sum := s.entryPath(KindObj, key) + ".sum"
	os.Chtimes(sum, t0, t0)
	s.FilePath(KindObj, key)
	if fi, _ := os.Stat(sum); !fi.ModTime().Equal(clk.Now()) {
		t.Errorf("FilePath did not mark the sidecar: %v", fi.ModTime())
	}
}

// stage puts n entries of size bytes, entry i marked age(i) before now.
func stage(t *testing.T, s *Store, clk *fakeClock, n, size int, age func(int) time.Duration) {
	t.Helper()
	for i := 0; i < n; i++ {
		key := testKey(i)
		if err := s.Put(KindFunc, key, testPayload(i, size)); err != nil {
			t.Fatal(err)
		}
		m := clk.Now().Add(-age(i))
		os.Chtimes(s.entryPath(KindFunc, key), m, m)
	}
}

func TestGCEvictsLRU(t *testing.T) {
	t.Parallel()
	clk := newClock()
	s := openTest(t, Options{Now: clk.Now})
	// Entries 0..9; entry i is (10-i) hours old, so 0 is oldest; 8 and 9 are
	// within Grace.
	stage(t, s, clk, 10, 1000, func(i int) time.Duration { return time.Duration(10-i)*time.Hour - time.Minute })
	src := writeSrc(t, t.TempDir(), testPayload(99, 1000))
	s.PutFile(KindObj, testKey(99), src)
	old := clk.Now().Add(-20 * time.Hour)
	os.Chtimes(s.entryPath(KindObj, testKey(99)), old, old)
	os.Chtimes(s.entryPath(KindObj, testKey(99))+".sum", old, old)
	before, err := s.Stats()
	if err != nil {
		t.Fatal(err)
	}
	unit := before.Bytes[KindFunc] / 10
	// Leave room for 6 func entries: the raw pair (oldest) and entries 0..3 go.
	st, err := s.GC(6 * unit)
	if err != nil {
		t.Fatal(err)
	}
	if st.Removed != 5 || st.Entries[KindObj] != 1 || st.Entries[KindFunc] != 4 {
		t.Errorf("GC removed %d (%v), want the raw pair and 4 func entries", st.Removed, st.Entries)
	}
	for i := 0; i < 10; i++ {
		ok := present(s, KindFunc, testKey(i))
		if ok != (i >= 4) {
			t.Errorf("entry %d present=%v after GC", i, ok)
		}
	}
	if _, err := os.Stat(s.entryPath(KindObj, testKey(99)) + ".sum"); !os.IsNotExist(err) {
		t.Error("the raw entry's sidecar survived")
	}
	// Target 0: everything older than Grace goes; 8 and 9 stay.
	if _, err := s.GC(0); err != nil {
		t.Fatal(err)
	}
	for i := 4; i < 10; i++ {
		ok := present(s, KindFunc, testKey(i))
		if ok != (i >= 8) {
			t.Errorf("entry %d present=%v after GC(0)", i, ok)
		}
	}
	after, _ := s.Stats()
	if after.Total != 2*unit || st.Total != 6*unit {
		t.Errorf("totals: after GC %d (want %d), store %d (want %d)", st.Total, 6*unit, after.Total, 2*unit)
	}
}

func TestGCLockExcludes(t *testing.T) {
	t.Parallel()
	clk := newClock()
	s := openTest(t, Options{Now: clk.Now})
	stage(t, s, clk, 4, 100, func(int) time.Duration { return 5 * time.Hour })
	// A fresh lock held by another process: GC returns ErrBusy, removes nothing.
	lock := filepath.Join(s.Root(), lockFile)
	os.WriteFile(lock, []byte("999 other\n"), 0o644)
	os.Chtimes(lock, clk.Now(), clk.Now())
	if _, err := s.GC(0); !errors.Is(err, ErrBusy) {
		t.Fatalf("GC under a fresh lock = %v, want ErrBusy", err)
	}
	if err := s.Clean(); !errors.Is(err, ErrBusy) {
		t.Fatalf("Clean under a fresh lock = %v, want ErrBusy", err)
	}
	if err := s.MaybeGC(); err != nil {
		t.Fatalf("MaybeGC under a fresh lock = %v, want nil", err)
	}
	if !present(s, KindFunc, testKey(0)) {
		t.Fatal("a busy GC removed an entry")
	}
	if b, _ := os.ReadFile(lock); string(b) != "999 other\n" {
		t.Fatal("a busy GC touched another process's lock")
	}
	// The lock goes stale: GC breaks it, runs and releases its own.
	clk.Add(LockStale + time.Minute)
	st, err := s.GC(0)
	if err != nil || st.Removed != 4 {
		t.Fatalf("GC over a stale lock = %v, removed %d", err, st.Removed)
	}
	if _, err := os.Stat(lock); !os.IsNotExist(err) {
		t.Errorf("GC left gc.lock: %v", err)
	}
	// Concurrent GCs on one root: each runs or is busy; none fails.
	stage(t, s, clk, 50, 100, func(int) time.Duration { return 5 * time.Hour })
	var wg sync.WaitGroup
	results := make([]error, 8)
	for i := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s2, err := Open(filepath.Dir(s.Root()), Options{Now: clk.Now})
			if err == nil {
				_, err = s2.GC(0)
			}
			results[i] = err
		}()
	}
	wg.Wait()
	ran := 0
	for _, err := range results {
		switch {
		case err == nil:
			ran++
		case !errors.Is(err, ErrBusy):
			t.Errorf("concurrent GC: %v", err)
		}
	}
	if ran == 0 {
		t.Error("no concurrent GC ran")
	}
	if st, _ := s.Stats(); st.Total != 0 {
		t.Errorf("after concurrent GCs %d bytes remain", st.Total)
	}
}

func TestGCSkipsUndeletable(t *testing.T) {
	t.Parallel()
	clk := newClock()
	s := openTest(t, Options{Now: clk.Now})
	stage(t, s, clk, 5, 100, func(int) time.Duration { return 5 * time.Hour })
	f, err := os.Open(s.entryPath(KindFunc, testKey(2)))
	if err != nil {
		t.Fatal(err)
	}
	st, err := s.GC(0)
	f.Close()
	if err != nil {
		t.Fatalf("GC with an open entry: %v", err)
	}
	_, held := mustGet(t, s, KindFunc, testKey(2))
	for _, i := range []int{0, 1, 3, 4} {
		if _, ok := mustGet(t, s, KindFunc, testKey(i)); ok {
			t.Errorf("entry %d survived", i)
		}
	}
	if runtime.GOOS == "windows" && (!held || st.Removed != 4) {
		t.Errorf("windows: held entry present=%v, removed %d; want it skipped", held, st.Removed)
	}
	t.Logf("%s: held entry kept=%v, removed %d", runtime.GOOS, held, st.Removed)
}

func TestMaybeGCInterval(t *testing.T) {
	t.Parallel()
	clk := newClock()
	s := openTest(t, Options{Now: clk.Now, Budget: 1 << 20})
	if err := s.MaybeGC(); err != nil { // no stamp yet: walks
		t.Fatal(err)
	}
	if w := s.walks.Load(); w != 1 {
		t.Fatalf("first MaybeGC walked %d times, want 1", w)
	}
	s.Put(KindFunc, testKey(1), testPayload(1, 1000))
	clk.Add(30 * time.Minute)
	s.MaybeGC()
	if w := s.walks.Load(); w != 1 {
		t.Errorf("MaybeGC within GCInterval and under budget walked (%d)", w)
	}
	clk.Add(31 * time.Minute)
	s.MaybeGC()
	if w := s.walks.Load(); w != 2 {
		t.Errorf("MaybeGC after GCInterval did not walk (%d)", w)
	}
	// Over budget within the interval: walks, from this process's puts alone.
	for i := 0; i < 3; i++ {
		s.Put(KindFunc, testKey(10+i), testPayload(i, 400_000))
	}
	s.MaybeGC()
	if w := s.walks.Load(); w != 3 {
		t.Errorf("MaybeGC over budget did not walk (%d)", w)
	}
	// That GC could evict nothing (every entry is within Grace), so another
	// process over the stamped budget does not walk again until a kept entry
	// can leave Grace. TestMaybeGCOverBudgetWithinGrace
	// shows the stamped total making a fresh process walk once one can.
	s2, _ := Open(filepath.Dir(s.Root()), Options{Now: clk.Now, Budget: 1 << 20})
	s2.MaybeGC()
	if w := s2.walks.Load(); w != 0 {
		t.Errorf("a fresh process walked while the last GC was blocked by Grace (%d)", w)
	}
}

func TestCleanEmpties(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	s, _ := Open(root, Options{})
	for i, kind := range []Kind{KindFunc, KindIface, KindManifest} {
		s.Put(kind, testKey(i), []byte("x"))
	}
	s.PutFile(KindObj, testKey(9), writeSrc(t, t.TempDir(), []byte("obj")))
	d, err := s.Dir("thinlto")
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(d, "llvmcache-1"), []byte("lto"), 0o644)
	h := s.LoadHints(t.TempDir())
	h.Record("a.mk", fakeInfo{size: 1, mtime: time.Unix(1, 0)}, s.Now(), testKey(1))
	h.Save()
	s.GC(1 << 40) // writes a stamp
	if err := s.Clean(); err != nil {
		t.Fatal(err)
	}
	st, err := s.Stats()
	if err != nil {
		t.Fatal(err)
	}
	if st.Total != 0 || len(st.SortedKinds()) != 0 {
		t.Errorf("after Clean: %d bytes, kinds %v", st.Total, st.SortedKinds())
	}
	ds, _ := os.ReadDir(s.Root())
	for _, d := range ds {
		if d.Name() != tmpDir {
			t.Errorf("after Clean v1 holds %s", d.Name())
		}
	}
	// Usable without reopening, and reopenable.
	if err := s.Put(KindFunc, testKey(1), []byte("y")); err != nil {
		t.Fatal(err)
	}
	s2, err := Open(root, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := mustGet(t, s2, KindFunc, testKey(1)); !ok || string(got) != "y" {
		t.Errorf("after Clean and reopen: %q, %v", got, ok)
	}
}

func TestStatsSorted(t *testing.T) {
	t.Parallel()
	s := openTest(t, Options{})
	for i, kind := range allKinds {
		if kind == KindObj || kind == KindLib {
			s.PutFile(kind, testKey(i), writeSrc(t, t.TempDir(), []byte("raw")))
			continue
		}
		s.Put(kind, testKey(i), []byte("x"))
	}
	d, _ := s.Dir("thinlto")
	os.WriteFile(filepath.Join(d, "f"), []byte("abc"), 0o644)
	st, err := s.Stats()
	if err != nil {
		t.Fatal(err)
	}
	ks := st.SortedKinds()
	if !slices.IsSorted(ks) || len(ks) != len(allKinds)+1 {
		t.Errorf("SortedKinds = %v", ks)
	}
	var sum int64
	for _, k := range ks {
		if st.Entries[k] != 1 {
			t.Errorf("kind %s has %d entries, want 1 (a raw pair is one)", k, st.Entries[k])
		}
		sum += st.Bytes[k]
	}
	if sum != st.Total || st.Oldest.IsZero() {
		t.Errorf("Total %d, sum %d, Oldest %v", st.Total, sum, st.Oldest)
	}
}

func TestDirCountsTowardBudget(t *testing.T) {
	t.Parallel()
	clk := newClock()
	s := openTest(t, Options{Now: clk.Now})
	d, err := s.Dir("thinlto")
	if err != nil {
		t.Fatal(err)
	}
	f := filepath.Join(d, "llvmcache-0")
	os.WriteFile(f, make([]byte, 5000), 0o644)
	old := clk.Now().Add(-5 * time.Hour)
	os.Chtimes(f, old, old)
	st, _ := s.Stats()
	if st.Bytes["thinlto"] != 5000 {
		t.Errorf("thinlto bytes = %d", st.Bytes["thinlto"])
	}
	s.GC(0)
	if _, err := os.Stat(f); !os.IsNotExist(err) {
		t.Error("GC did not evict an old thinlto file")
	}
}

// TestRemoveFileThenPutReplaces: RemoveFile deletes a raw-file entry and its
// sidecar, so FilePath misses and the next PutFile stores the new bytes (the
// driver's MK_CACHE_VERIFY repair). A missing entry is not an error.
func TestRemoveFileThenPutReplaces(t *testing.T) {
	t.Parallel()
	s := openTest(t, Options{})
	src := t.TempDir()
	key := testKey(7)
	if err := s.PutFile(KindObj, key, writeSrc(t, src, testPayload(1, 1000))); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveFile(KindObj, key); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := s.FilePath(KindObj, key); ok {
		t.Fatal("FilePath hits after RemoveFile")
	}
	want := testPayload(2, 1000)
	if err := s.PutFile(KindObj, key, writeSrc(t, src, want)); err != nil {
		t.Fatal(err)
	}
	got, ok, _ := s.FilePath(KindObj, key)
	if b, _ := os.ReadFile(got); !ok || !bytes.Equal(b, want) {
		t.Error("PutFile after RemoveFile did not store the new bytes")
	}
	if err := s.RemoveFile(KindObj, testKey(8)); err != nil {
		t.Errorf("RemoveFile of a missing entry: %v", err)
	}
}

// TestMaybeGCCountsEveryProcessPuts: commands that each put less than the
// budget, one Store each (one process each), within GCInterval, still keep
// the store under the budget: a MaybeGC that does not collect carries its
// puts into gc.stamp (carryPut), so a later process's estimate crosses the
// budget and it collects. Without the carry each estimate is the last GC's
// total plus one command's puts, and the store grows to 20 times the
// entry size here.
func TestMaybeGCCountsEveryProcessPuts(t *testing.T) {
	t.Parallel()
	clk := newClock()
	clk.Add(3 * time.Hour) // entries written now are past Grace on this clock
	dir := t.TempDir()
	opts := Options{Now: clk.Now, Budget: 10_000}
	s0, err := Open(dir, opts)
	if err != nil {
		t.Fatal(err)
	}
	if err := s0.MaybeGC(); err != nil { // no stamp yet: the first GC writes one
		t.Fatal(err)
	}
	walks := int64(0)
	for i := 0; i < 20; i++ {
		s, err := Open(dir, opts)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Put(KindFunc, testKey(100+i), testPayload(i, 1500)); err != nil {
			t.Fatal(err)
		}
		if err := s.MaybeGC(); err != nil {
			t.Fatal(err)
		}
		walks += s.walks.Load()
		st, err := s.Stats()
		if err != nil {
			t.Fatal(err)
		}
		if st.Total > opts.Budget {
			t.Fatalf("after command %d the store holds %d bytes, over the budget of %d (walks %d)", i, st.Total, opts.Budget, walks)
		}
	}
	if walks == 0 {
		t.Error("no command collected")
	}
}

// TestBinDirAgedByGC: the driver's executables under Dir(BinDir)
// count toward the budget, GC evicts an old one with its now empty <link
// key> directory and keeps a young one, a ".sum" there is a file like any
// other (no sidecar), Account counts a placed executable toward MaybeGC's
// estimate, and Mark makes an old executable young again.
func TestBinDirAgedByGC(t *testing.T) {
	t.Parallel()
	clk := newClock()
	s := openTest(t, Options{Now: clk.Now})
	d, err := s.Dir(BinDir)
	if err != nil {
		t.Fatal(err)
	}
	put := func(key, name string, age time.Duration) string {
		p := filepath.Join(d, key, name)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, make([]byte, 1000), 0o755)
		at := clk.Now().Add(-age)
		os.Chtimes(p, at, at)
		return p
	}
	old := put("aa", "app.exe", 5*time.Hour)
	young := put("bb", "app.exe", time.Minute)
	sum := put("cc", "x.sum", 5*time.Hour)
	marked := put("dd", "app.exe", 5*time.Hour)
	s.Mark(marked)
	st, _ := s.Stats()
	if st.Entries[BinDir] != 4 || st.Bytes[BinDir] != 4000 {
		t.Fatalf("bin: %d entries, %d bytes; want 4 and 4000", st.Entries[BinDir], st.Bytes[BinDir])
	}
	s.Account(1000)
	if s.put.Load() != 1000 {
		t.Errorf("Account: put %d, want 1000", s.put.Load())
	}
	s.GC(0)
	for _, p := range []string{old, sum} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("GC kept the old %s", p)
		}
		if _, err := os.Stat(filepath.Dir(p)); !os.IsNotExist(err) {
			t.Errorf("GC kept the empty directory of %s", p)
		}
	}
	for _, p := range []string{young, marked} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("GC removed %s, which is within Grace: %v", p, err)
		}
	}
	if err := s.Clean(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(d); !os.IsNotExist(err) {
		t.Errorf("Clean left %s: %v", d, err)
	}
	if got := DirPath(filepath.Dir(s.Root()), BinDir); got != d {
		t.Errorf("DirPath = %q, Dir = %q", got, d)
	}
}

// TestStaleWorkDirsInTmp: a work directory in TmpDir whose newest
// file is older than Grace is removed by GC and by Clean, whole; one with a
// young file stays.
func TestStaleWorkDirsInTmp(t *testing.T) {
	t.Parallel()
	for _, clean := range []bool{false, true} {
		clk := newClock()
		s := openTest(t, Options{Now: clk.Now})
		mk := func(name string, age time.Duration) string {
			d := filepath.Join(s.TmpDir(), name)
			f := filepath.Join(d, "deep", "p.o")
			os.MkdirAll(filepath.Dir(f), 0o755)
			os.WriteFile(f, []byte("o"), 0o644)
			at := clk.Now().Add(-age)
			for _, p := range []string{f, filepath.Dir(f), d} {
				os.Chtimes(p, at, at)
			}
			return d
		}
		stale, live := mk("mk-1", 3*time.Hour), mk("mk-2", time.Minute)
		if clean {
			if err := s.Clean(); err != nil {
				t.Fatal(err)
			}
		} else if _, err := s.GC(1 << 40); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(stale); !os.IsNotExist(err) {
			t.Errorf("clean=%v: the stale work directory stayed: %v", clean, err)
		}
		if _, err := os.Stat(filepath.Join(live, "deep", "p.o")); err != nil {
			t.Errorf("clean=%v: the live work directory was removed: %v", clean, err)
		}
	}
}
