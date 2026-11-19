// Why: a sidecar is written after its file, so a sidecar names a complete
// file, and GC deletes a pair sidecar first; a race must never leave a
// sidecar naming a removed file.
// What: TestSidecarNeverOutlivesFile, TestSidecarHeldOpenAtRemoval
// and TestFilePathRemovesOrphanSidecar.
// How: forces the interleavings of PutFile with GC and Clean deterministically
// through the Store's test hooks, one Store standing in for each process.

package cache

import (
	"os"
	"runtime"
	"testing"
	"time"
)

// TestSidecarNeverOutlivesFile: whatever step of a PutFile a GC or
// Clean runs at, and whatever step of a GC a PutFile runs at, no sidecar is
// left naming a missing file, and the key is a miss or a correct hit.
func TestSidecarNeverOutlivesFile(t *testing.T) {
	t.Parallel()
	key, obj := testKey(3000), procObj(0)
	ahead := func() time.Time { return time.Now().Add(Grace + time.Hour) }
	type env struct {
		w, g *Store // w writes, g collects
		src  string // a fresh object file for w to put
	}
	cases := []struct {
		name string
		// old: a complete pair stands before, aged 3 h by the real clock.
		old bool
		// real: g uses the real clock; otherwise one Grace + 1 h ahead, so
		// every file is evictable.
		real bool
		// run forces the interleaving and reports whether it happened.
		run func(t *testing.T, e env) bool
		// hit: the entry is a hit afterwards (else it is a miss).
		hit bool
		// late: g's second sidecar removal must have taken a sidecar.
		late bool
	}{
		{
			// The interleaving seen in practice: the walk sees
			// the file alone, GC removes it, then the sidecar lands and
			// PutFile's check rolls it back.
			name: "GC between file and sidecar",
			run: func(t *testing.T, e env) bool {
				gone := false
				e.w.hooks.afterObj = func() {
					mustGC(t, e.g)
					_, err := os.Stat(e.w.entryPath(KindObj, key))
					gone = os.IsNotExist(err)
				}
				mustPutFile(t, e.w, key, e.src)
				return gone && e.w.r5.rollbacks.Load() == 1
			},
		},
		{
			name: "GC between sidecar and check",
			run: func(t *testing.T, e env) bool {
				gone := false
				e.w.hooks.afterSum = func() {
					mustGC(t, e.g)
					_, err := os.Stat(e.w.entryPath(KindObj, key))
					gone = os.IsNotExist(err)
				}
				mustPutFile(t, e.w, key, e.src)
				return gone
			},
		},
		{
			// The walk sees the file alone; the whole rest of the PutFile runs
			// before GC removes anything, so its check finds the file
			// standing. Only GC's second sidecar removal is left to take the
			// sidecar.
			name: "PutFile ends between GC's walk and its removals", late: true,
			run: func(t *testing.T, e env) bool {
				return walkThenFinish(t, e.w, e.g, key, e.src, func() error { _, err := e.g.GC(0); return err })
			},
		},
		{
			// The same against Clean (os.RemoveAll listed the directory once
			// and never saw the sidecar).
			name: "PutFile ends between Clean's walk and its removals", late: true,
			run: func(t *testing.T, e env) bool {
				return walkThenFinish(t, e.w, e.g, key, e.src, e.g.Clean)
			},
		},
		{
			// GC removed the old pair's sidecar; a PutFile (whose standing
			// check now fails) renames a new file over the old one and writes
			// its sidecar; then GC removes the file it walked, now the new
			// one, and its second removal takes the new sidecar.
			name: "PutFile between GC's sidecar and file", old: true, late: true,
			run: func(t *testing.T, e env) bool {
				return putInsideGC(t, e.w, e.g, key, e.src)
			},
		},
		{
			// The same with the real clock: the file renamed onto the walked
			// path is young, so GC's re-check of Grace keeps it, and the new
			// pair is a hit (entries marked within Grace are never evicted).
			name: "young file renamed onto a walked path is kept", old: true, real: true, hit: true,
			run: func(t *testing.T, e env) bool {
				return putInsideGC(t, e.w, e.g, key, e.src)
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			w, err := Open(root, Options{})
			if err != nil {
				t.Fatal(err)
			}
			if c.old {
				mustPutFile(t, w, key, writeSrc(t, t.TempDir(), obj))
				old := time.Now().Add(-3 * time.Hour)
				p := w.entryPath(KindObj, key)
				for _, f := range []string{p, p + ".sum"} {
					if err := os.Chtimes(f, old, old); err != nil {
						t.Fatal(err)
					}
				}
			}
			gopts := Options{Now: ahead}
			if c.real {
				gopts = Options{}
			}
			g, err := Open(root, gopts)
			if err != nil {
				t.Fatal(err)
			}
			if !c.run(t, env{w, g, writeSrc(t, t.TempDir(), obj)}) {
				t.Fatal("the interleaving was not forced")
			}
			if c.late && g.r5.late.Load() != 1 {
				t.Errorf("the collector's second removal took %d sidecars, want 1", g.r5.late.Load())
			}
			endState(t, w, key, c.hit)
		})
	}
}

// TestSidecarHeldOpenAtRemoval: the collector's second sidecar removal
// fails while a reader holds the sidecar open (Windows; on Linux it never
// fails), and the reader's own check, once it has closed the sidecar, removes
// it. The reader here is the test holding a handle and then calling
// FilePath, as FilePath's readFile does for the length of one read.
func TestSidecarHeldOpenAtRemoval(t *testing.T) {
	t.Parallel()
	key, obj := testKey(3001), procObj(1)
	root := t.TempDir()
	w, err := Open(root, Options{})
	if err != nil {
		t.Fatal(err)
	}
	g, err := Open(root, Options{Now: func() time.Time { return time.Now().Add(Grace + time.Hour) }})
	if err != nil {
		t.Fatal(err)
	}
	sum := w.entryPath(KindObj, key) + ".sum"
	var held *os.File
	forced := walkThenFinishThen(t, w, g, key, writeSrc(t, t.TempDir(), obj), func() error { _, err := g.GC(0); return err }, func() {
		f, err := os.Open(sum)
		if err != nil {
			t.Fatalf("open the landed sidecar: %v", err)
		}
		held = f
	})
	if !forced {
		t.Fatal("the interleaving was not forced")
	}
	_, serr := os.Stat(sum)
	t.Logf("%s: sidecar present after GC while held=%v; late %d, stuck %d", runtime.GOOS, serr == nil, g.r5.late.Load(), g.r5.stuck.Load())
	if runtime.GOOS == "windows" {
		if serr != nil || g.r5.stuck.Load() != 1 {
			t.Fatalf("windows: the held sidecar was removed (stat %v, stuck %d); the case was not forced", serr, g.r5.stuck.Load())
		}
	} else if serr == nil || g.r5.late.Load() != 1 {
		t.Fatalf("%s: removing an open sidecar failed: stat %v", runtime.GOOS, serr)
	}
	held.Close()
	if _, ok, err := w.FilePath(KindObj, key); ok || err != nil {
		t.Fatalf("FilePath = %v, %v; want a silent miss", ok, err)
	}
	endState(t, w, key, false)
}

// TestFilePathRemovesOrphanSidecar: a sidecar whose file is missing
// (only a process killed between two steps, or a holder outside the store,
// leaves one) is a miss, and FilePath removes it, as it does a sidecar of the
// wrong length; PutFile's standing check removes one too.
func TestFilePathRemovesOrphanSidecar(t *testing.T) {
	t.Parallel()
	for _, via := range []string{"FilePath", "PutFile"} {
		s := openTest(t, Options{})
		key := testKey(1)
		mustPutFile(t, s, key, writeSrc(t, t.TempDir(), testPayload(1, 300)))
		p := s.entryPath(KindObj, key)
		if err := os.Remove(p); err != nil {
			t.Fatal(err)
		}
		if via == "FilePath" {
			if _, ok, err := s.FilePath(KindObj, key); ok || err != nil {
				t.Fatalf("FilePath of an orphan sidecar = %v, %v; want a silent miss", ok, err)
			}
			if _, err := os.Stat(p + ".sum"); !os.IsNotExist(err) {
				t.Errorf("orphan sidecar still present after FilePath: %v", err)
			}
		} else if _, ok := s.sidecarStands(p+".sum", p, KindObj, key); ok {
			t.Error("an orphan sidecar stands")
		} else if _, err := os.Stat(p + ".sum"); !os.IsNotExist(err) {
			t.Errorf("orphan sidecar still present after the standing check: %v", err)
		}
		if s.r5.orphans.Load() != 1 {
			t.Errorf("%s: %d orphans counted, want 1", via, s.r5.orphans.Load())
		}
	}
}

// walkThenFinish runs collect (a GC or Clean of g) while w's PutFile of key
// is paused with its file in place and no sidecar, and finishes the PutFile
// inside collect, after its walk and before it removes anything. It reports
// whether that happened: the walk ran while the PutFile was paused, and the
// PutFile finished with its pair standing (its check saw the file).
func walkThenFinish(t *testing.T, w, g *Store, key [32]byte, src string, collect func() error) bool {
	return walkThenFinishThen(t, w, g, key, src, collect, nil)
}

// walkThenFinishThen is walkThenFinish, calling then (if not nil) after the
// PutFile has finished and before collect removes anything.
func walkThenFinishThen(t *testing.T, w, g *Store, key [32]byte, src string, collect func() error, then func()) bool {
	t.Helper()
	paused, resume, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	w.hooks.afterObj = func() {
		close(paused)
		<-resume
	}
	go func() { done <- w.PutFile(KindObj, key, src) }()
	<-paused
	stood := false
	g.hooks.afterWalk = func() {
		g.hooks.afterWalk = nil // Clean walks again only if a directory was refilled
		close(resume)
		if err := <-done; err != nil {
			t.Errorf("PutFile: %v", err)
		}
		p := w.entryPath(KindObj, key)
		_, ferr := os.Stat(p)
		_, serr := os.Stat(p + ".sum")
		stood = ferr == nil && serr == nil && w.r5.rollbacks.Load() == 0
		if then != nil {
			then()
		}
	}
	if err := collect(); err != nil {
		t.Errorf("collect: %v", err)
	}
	if g.hooks.afterWalk != nil {
		close(resume)
		<-done
		return false
	}
	return stood
}

// putInsideGC runs a GC of g in which w's whole PutFile of key runs after GC
// removed the old pair's sidecar and before it removes the file. It reports
// whether the new sidecar landed.
func putInsideGC(t *testing.T, w, g *Store, key [32]byte, src string) bool {
	t.Helper()
	landed := false
	g.hooks.afterSide = func() {
		g.hooks.afterSide = nil
		mustPutFile(t, w, key, src)
		_, err := os.Stat(w.entryPath(KindObj, key) + ".sum")
		landed = err == nil
	}
	mustGC(t, g)
	return landed
}

// endState checks the invariant as TestConcurrentProcesses does, then
// that the key is a hit (hit) or a miss, and that a fresh PutFile after it
// writes the entry whole.
func endState(t *testing.T, w *Store, key [32]byte, hit bool) {
	t.Helper()
	verifyAll(t, w)
	p := w.entryPath(KindObj, key)
	_, ferr := os.Stat(p)
	_, serr := os.Stat(p + ".sum")
	t.Logf("file present=%v, sidecar present=%v; writer: dropped %d, rollbacks %d, orphans %d", ferr == nil, serr == nil, w.Dropped(), w.r5.rollbacks.Load(), w.r5.orphans.Load())
	if _, ok, _ := w.FilePath(KindObj, key); ok != hit {
		t.Errorf("FilePath hit = %v, want %v", ok, hit)
	}
	w.hooks.afterObj, w.hooks.afterSum = nil, nil
	mustPutFile(t, w, key, writeSrc(t, t.TempDir(), procObj(0)))
	if _, ok, err := w.FilePath(KindObj, key); !ok || err != nil {
		t.Errorf("FilePath after a fresh PutFile = %v, %v", ok, err)
	}
}

func mustGC(t *testing.T, g *Store) {
	t.Helper()
	if _, err := g.GC(0); err != nil {
		t.Errorf("GC: %v", err)
	}
}

func mustPutFile(t *testing.T, w *Store, key [32]byte, src string) {
	t.Helper()
	if err := w.PutFile(KindObj, key, src); err != nil {
		t.Errorf("PutFile: %v", err)
	}
}
