// Why: the cache is content-addressed and shared by processes, so a torn,
// corrupt or mismatched entry must read as a miss, never as data.
// What: tests of the on-disk layout, the frame, Open, Get, Put, atomic
// rename, manifest updates, read-only stores and corrupt-is-miss.
// How: writes entries, damages or races them, and checks what Get returns.

package cache

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"testing"
	"time"

	"mk/internal/source"
)

var allKinds = []Kind{KindParse, KindManifest, KindIface, KindFunc, KindEval, KindGen, KindObj, KindPos, KindLib, KindLayout, KindProfile}

func testKey(i int) source.Hash { return source.HashString("mk.cachetest.v1", strconv.Itoa(i)) }

// testPayload is a pure function of i, so any goroutine or process can check
// any hit.
func testPayload(i, n int) []byte {
	out := make([]byte, 0, n)
	for len(out) < n {
		h := source.HashString("mk.cachepayload.v1", fmt.Sprint(i, len(out)))
		out = append(out, h[:]...)
	}
	return out[:n]
}

// fakeClock is an injectable clock.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newClock() *fakeClock { return &fakeClock{t: time.Now().Truncate(time.Second)} }

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func openTest(t *testing.T, opts Options) *Store {
	t.Helper()
	s, err := Open(t.TempDir(), opts)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func mustGet(t *testing.T, s *Store, kind Kind, key source.Hash) ([]byte, bool) {
	t.Helper()
	b, ok, err := s.Get(kind, key)
	if err != nil {
		t.Fatalf("Get(%s): %v", kind, err)
	}
	return b, ok
}

func TestPutGetRoundTrip(t *testing.T) {
	t.Parallel()
	s := openTest(t, Options{})
	for _, kind := range allKinds {
		for i, n := range []int{0, 1, 1 << 20} {
			key := testKey(100*len(kind) + i)
			want := testPayload(i, n)
			if err := s.Put(kind, key, want); err != nil {
				t.Fatalf("Put(%s, %d bytes): %v", kind, n, err)
			}
			got, ok := mustGet(t, s, kind, key)
			if !ok || !bytes.Equal(got, want) {
				t.Errorf("Get(%s, %d bytes) = %d bytes, %v", kind, n, len(got), ok)
			}
		}
	}
	// The on-disk layout: <kind>/<first two hex digits>/<truncated key>.
	key := testKey(7)
	s.Put(KindFunc, key, []byte("x"))
	name := key.Trunc128().String()
	if _, err := os.Stat(filepath.Join(s.Root(), "func", name[:2], name)); err != nil {
		t.Errorf("entry not at v1/func/hh/name: %v", err)
	}
}

func TestMissIsNotError(t *testing.T) {
	t.Parallel()
	s := openTest(t, Options{})
	b, ok, err := s.Get(KindIface, testKey(1))
	if b != nil || ok || err != nil {
		t.Errorf("Get of a never-written key = (%v, %v, %v), want (nil, false, nil)", b, ok, err)
	}
	p, ok, err := s.FilePath(KindObj, testKey(1))
	if p != "" || ok || err != nil {
		t.Errorf("FilePath of a never-written key = (%q, %v, %v)", p, ok, err)
	}
}

func TestKeyMismatchIsMiss(t *testing.T) {
	t.Parallel()
	s := openTest(t, Options{})
	k1 := testKey(1)
	k2 := k1
	k2[31] ^= 0xff // same Trunc128, another full key
	if k1.Trunc128() != k2.Trunc128() || k1 == k2 {
		t.Fatal("bad forged key")
	}
	if err := s.Put(KindFunc, k1, []byte("one")); err != nil {
		t.Fatal(err)
	}
	if _, ok := mustGet(t, s, KindFunc, k2); ok {
		t.Error("a colliding key hit")
	}
	if err := s.Put(KindFunc, k2, []byte("two")); err != nil {
		t.Fatal(err)
	}
	if got, ok := mustGet(t, s, KindFunc, k1); !ok || string(got) != "one" {
		t.Errorf("the first key's entry was replaced or removed: %q, %v", got, ok)
	}
}

func TestCorruptEntryIsMiss(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		damage func([]byte) []byte
	}{
		{"flipped payload byte", func(b []byte) []byte { b[frameFixed+len("iface")-sha256Len()] ^= 1; return b }},
		{"flipped trailer byte", func(b []byte) []byte { b[len(b)-1] ^= 1; return b }},
		{"truncated", func(b []byte) []byte { return b[:len(b)-5] }},
		{"empty", func(b []byte) []byte { return nil }},
		{"wrong magic", func(b []byte) []byte { b[0] = 'X'; return b }},
		{"wrong version", func(b []byte) []byte { b[4] = 2; return b }},
		{"appended byte", func(b []byte) []byte { return append(b, 0) }},
	}
	for i, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := openTest(t, Options{})
			key := testKey(i)
			if err := s.Put(KindIface, key, []byte("payload bytes")); err != nil {
				t.Fatal(err)
			}
			p := s.entryPath(KindIface, key)
			b, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, c.damage(b), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, ok := mustGet(t, s, KindIface, key); ok {
				t.Error("a damaged entry hit")
			}
			if _, err := os.Stat(p); !os.IsNotExist(err) {
				t.Errorf("a damaged entry was not removed: %v", err)
			}
			// A fresh Put replaces it.
			if err := s.Put(KindIface, key, []byte("again")); err != nil {
				t.Fatal(err)
			}
			if got, ok := mustGet(t, s, KindIface, key); !ok || string(got) != "again" {
				t.Errorf("after a fresh Put: %q, %v", got, ok)
			}
		})
	}
}

func sha256Len() int { return 32 }

func TestKindInStoredFrame(t *testing.T) {
	t.Parallel()
	s := openTest(t, Options{})
	key := testKey(3)
	s.Put(KindEval, key, []byte("e"))
	// A frame copied into another kind's directory is corrupt there.
	src := s.entryPath(KindEval, key)
	dst := s.entryPath(KindGen, key)
	os.MkdirAll(filepath.Dir(dst), 0o755)
	b, _ := os.ReadFile(src)
	os.WriteFile(dst, b, 0o644)
	if _, ok := mustGet(t, s, KindGen, key); ok {
		t.Error("a frame of kind eval hit as gen")
	}
}

func TestTornWriteIsInvisible(t *testing.T) {
	t.Parallel()
	clk := newClock()
	s := openTest(t, Options{Now: clk.Now})
	key := testKey(9)
	full := encodeFrame(KindFunc, key, testPayload(9, 4096))
	// A writer killed between write and rename leaves a partial file in tmp/.
	torn := filepath.Join(s.Root(), tmpDir, "12345-00112233445566778899")
	if err := os.WriteFile(torn, full[:len(full)/2], 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := mustGet(t, s, KindFunc, key); ok {
		t.Fatal("a torn write is visible")
	}
	if _, err := s.GC(1 << 40); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(torn); err != nil {
		t.Fatalf("GC removed a tmp file younger than Grace: %v", err)
	}
	os.Chtimes(torn, clk.Now(), clk.Now())
	clk.Add(Grace + time.Minute)
	if _, err := s.GC(1 << 40); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(torn); !os.IsNotExist(err) {
		t.Errorf("GC kept a tmp file older than Grace: %v", err)
	}
	// A torn file at the entry's own name (power loss with no fsync) is a
	// verified miss.
	p := s.entryPath(KindFunc, key)
	os.MkdirAll(filepath.Dir(p), 0o755)
	os.WriteFile(p, full[:len(full)-1], 0o644)
	if _, ok := mustGet(t, s, KindFunc, key); ok {
		t.Error("a torn entry hit")
	}
}

func TestConcurrentPutSameKey(t *testing.T) {
	t.Parallel()
	s := openTest(t, Options{})
	key := testKey(42)
	want := testPayload(42, 64<<10)
	var wg sync.WaitGroup
	errs := make(chan error, 256)
	for i := 0; i < 64; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			if err := s.Put(KindFunc, key, want); err != nil {
				errs <- fmt.Errorf("Put: %w", err)
			}
		}()
		go func() {
			defer wg.Done()
			for j := 0; j < 8; j++ {
				got, ok, err := s.Get(KindFunc, key)
				if err != nil {
					errs <- fmt.Errorf("Get: %w", err)
					return
				}
				if ok && !bytes.Equal(got, want) {
					errs <- fmt.Errorf("Get returned %d wrong bytes", len(got))
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	if got, ok := mustGet(t, s, KindFunc, key); !ok || !bytes.Equal(got, want) {
		t.Error("final Get is not the payload")
	}
	if n := countFiles(t, filepath.Join(s.Root(), tmpDir)); n != 0 {
		t.Errorf("%d files left in tmp/", n)
	}
}

func countFiles(t *testing.T, dir string) int {
	t.Helper()
	ds, err := os.ReadDir(dir)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return len(ds)
}

func TestRenameOverOpenFile(t *testing.T) {
	t.Parallel()
	s := openTest(t, Options{})
	key := testKey(5)
	want := []byte("content-addressed")
	if err := s.Put(KindIface, key, want); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(s.entryPath(KindIface, key))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := s.Put(KindIface, key, want); err != nil {
		t.Errorf("Put over an open entry: %v", err)
	}
	if got, ok := mustGet(t, s, KindIface, key); !ok || !bytes.Equal(got, want) {
		t.Errorf("Get while open: %q, %v", got, ok)
	}
	// The rename path itself: a damaged entry held open cannot be renamed
	// over on Windows; the write is retried, then dropped, never an error.
	k2 := testKey(6)
	p := s.entryPath(KindIface, k2)
	os.MkdirAll(filepath.Dir(p), 0o755)
	os.WriteFile(p, []byte("not a frame"), 0o644)
	g, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Put(KindIface, k2, want); err != nil {
		t.Errorf("Put over an open damaged entry: %v", err)
	}
	got, ok := mustGet(t, s, KindIface, k2)
	g.Close()
	if ok && !bytes.Equal(got, want) {
		t.Errorf("Get returned %q", got)
	}
	if runtime.GOOS != "windows" && !ok {
		t.Error("Put did not replace a damaged entry")
	}
}

func TestManifestUpdatePersists(t *testing.T) {
	t.Parallel()
	s := openTest(t, Options{})
	p := testKey(1)
	s.Put(KindManifest, p, []byte("m1"))
	s.Put(KindManifest, p, []byte("m2"))
	if got, ok := mustGet(t, s, KindManifest, p); !ok || string(got) != "m2" {
		t.Errorf("manifest = %q, %v; want m2", got, ok)
	}
	k := testKey(2)
	s.Put(KindFunc, k, []byte("first"))
	s.Put(KindFunc, k, []byte("second"))
	if got, ok := mustGet(t, s, KindFunc, k); !ok || string(got) != "first" {
		t.Errorf("content-addressed entry = %q, %v; want first", got, ok)
	}
	for _, kind := range allKinds {
		if Mutable(kind) != (kind == KindManifest) {
			t.Errorf("Mutable(%s) = %v", kind, Mutable(kind))
		}
	}
}

func TestReplaceManifestOverOpenFile(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	var logged []string
	s := openTest(t, Options{Logf: func(f string, a ...any) {
		mu.Lock()
		logged = append(logged, fmt.Sprintf(f, a...))
		mu.Unlock()
	}})
	p := testKey(1)
	if err := s.Put(KindManifest, p, []byte("m1")); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(s.entryPath(KindManifest, p))
	if err != nil {
		t.Fatal(err)
	}
	err = s.Put(KindManifest, p, []byte("m2"))
	f.Close()
	if err != nil {
		t.Fatalf("manifest Put over an open handle returned %v", err)
	}
	got, ok := mustGet(t, s, KindManifest, p)
	if !ok || (string(got) != "m1" && string(got) != "m2") {
		t.Fatalf("manifest = %q, %v: torn or lost", got, ok)
	}
	mu.Lock()
	n := len(logged)
	mu.Unlock()
	switch {
	case string(got) == "m1" && (s.Dropped() != 1 || n != 1):
		t.Errorf("old manifest kept but dropped=%d logged=%d", s.Dropped(), n)
	case string(got) == "m2" && s.Dropped() != 0:
		t.Errorf("manifest replaced but dropped=%d", s.Dropped())
	}
	t.Logf("%s: manifest after Put over an open handle: %s, dropped %d", runtime.GOOS, got, s.Dropped())
	if n := countFiles(t, filepath.Join(s.Root(), tmpDir)); n != 0 {
		t.Errorf("%d files left in tmp/", n)
	}
	// With the handle closed the write lands.
	if err := s.Put(KindManifest, p, []byte("m3")); err != nil {
		t.Fatal(err)
	}
	if got, _ := mustGet(t, s, KindManifest, p); string(got) != "m3" {
		t.Errorf("manifest = %q, want m3", got)
	}
}

func TestReadOnlyWritesNothing(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	ro, err := Open(root, Options{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := ro.Put(KindFunc, testKey(1), []byte("x")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "v1")); !os.IsNotExist(err) {
		t.Errorf("a read-only Open or Put created the store: %v", err)
	}
	rw, _ := Open(root, Options{})
	rw.Put(KindFunc, testKey(1), []byte("x"))
	if got, ok := mustGet(t, ro, KindFunc, testKey(1)); !ok || string(got) != "x" {
		t.Errorf("read-only Get = %q, %v", got, ok)
	}
}

func TestInvalidKindIsICE(t *testing.T) {
	t.Parallel()
	s := openTest(t, Options{})
	for _, k := range []Kind{"", "Func", "toolongkind", "tmp", "hints", "thinlto", "gc.lock", "a/b"} {
		ie := source.Guard("test", source.Span{}, func() { s.Put(k, testKey(1), nil) })
		if ie == nil || ie.Runtime {
			t.Errorf("Put with kind %q: %v, want an ICE", k, ie)
		}
	}
	ie := source.Guard("test", source.Span{}, func() { s.Dir("func") })
	if ie == nil || ie.Runtime {
		t.Errorf("Dir(\"func\"): %v, want an ICE", ie)
	}
}
