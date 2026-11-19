// Why: mk cache and mk profile merge manage files a user keeps across
// builds, so their output must be deterministic and their errors safe.
// What: tests of MergeProfiles, CacheSize, CacheClean and CacheDirFor.
// How: writes pgo profiles and cache trees in temp directories and compares
// the merged bytes, warnings, listed sizes and chosen directories.

package driver

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mk/internal/cache"
	"mk/internal/pgo"
	"mk/internal/source"
)

func writeProfile(t *testing.T, path string, p *pgo.Profile) {
	t.Helper()
	b, err := p.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func sum128(n byte) source.Hash128 {
	var h source.Hash128
	h[0] = n
	return h
}

// TestProfileMergeDeterministic: merging the same profiles in
// two orders gives the same bytes, and they are pgo.Merge's sums; a
// malformed input is an error and leaves out unwritten.
func TestProfileMergeDeterministic(t *testing.T) {
	dir := t.TempDir()
	a := &pgo.Profile{Records: []pgo.Record{
		{Key: "mk.app.Main", Checksum: sum128(1), Counts: []uint64{5, 2, 3}},
		{Key: "mk.lib.F", Checksum: sum128(2), Counts: []uint64{7}},
	}}
	b := &pgo.Profile{Records: []pgo.Record{
		{Key: "mk.a.first", Checksum: sum128(3), Counts: []uint64{9, 9}},
		{Key: "mk.app.Main", Checksum: sum128(1), Counts: []uint64{1, 1, 0}},
		{Key: "mk.lib.F", Checksum: sum128(2), Counts: []uint64{1}},
	}}
	pa, pb := filepath.Join(dir, "a.mkprof"), filepath.Join(dir, "b.mkprof")
	writeProfile(t, pa, a)
	writeProfile(t, pb, b)
	ab, ba := filepath.Join(dir, "ab.mkprof"), filepath.Join(dir, "ba.mkprof")
	if w, err := MergeProfiles(ab, []string{pa, pb}); err != nil {
		t.Fatal(err)
	} else if len(w) != 0 {
		t.Errorf("one checksum per key, warnings %q", w)
	}
	if _, err := MergeProfiles(ba, []string{pb, pa}); err != nil {
		t.Fatal(err)
	}
	x, _ := os.ReadFile(ab)
	y, _ := os.ReadFile(ba)
	if len(x) == 0 || !bytes.Equal(x, y) {
		t.Fatal("merge order changed the merged profile's bytes")
	}
	want, err := pgo.Merge(a, b)
	if err != nil {
		t.Fatal(err)
	}
	wb, _ := want.Bytes()
	if !bytes.Equal(x, wb) {
		t.Error("merged file is not pgo.Merge's result")
	}
	bad := filepath.Join(dir, "bad.mkprof")
	os.WriteFile(bad, []byte("not a profile"), 0o644)
	out := filepath.Join(dir, "out.mkprof")
	if _, err := MergeProfiles(out, []string{pa, bad}); err == nil || !strings.Contains(err.Error(), "bad.mkprof") {
		t.Errorf("malformed input: err %v, want one naming bad.mkprof", err)
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Error("a failed merge wrote its output")
	}
	if _, err := MergeProfiles(out, nil); err == nil {
		t.Error("no inputs accepted")
	}
}

// TestProfileMergeWarnsOnTwoChecksums: merging profiles of two builds of one
// function keeps both records and writes the merge, with one warning per
// such key, in key order; keys with one checksum give none.
func TestProfileMergeWarnsOnTwoChecksums(t *testing.T) {
	dir := t.TempDir()
	a := &pgo.Profile{Records: []pgo.Record{
		{Key: "mk.lib.F", Checksum: sum128(1), Counts: []uint64{5}},
		{Key: "mk.lib.G", Checksum: sum128(4), Counts: []uint64{1}},
		{Key: "mk.lib.H", Checksum: sum128(6), Counts: []uint64{1}},
	}}
	b := &pgo.Profile{Records: []pgo.Record{
		{Key: "mk.lib.F", Checksum: sum128(2), Counts: []uint64{7, 1}},
		{Key: "mk.lib.G", Checksum: sum128(4), Counts: []uint64{2}},
		{Key: "mk.lib.H", Checksum: sum128(7), Counts: []uint64{1}},
	}}
	pa, pb := filepath.Join(dir, "a.mkprof"), filepath.Join(dir, "b.mkprof")
	writeProfile(t, pa, a)
	writeProfile(t, pb, b)
	out := filepath.Join(dir, "out.mkprof")
	w, err := MergeProfiles(out, []string{pb, pa})
	if err != nil {
		t.Fatal(err)
	}
	if len(w) != 2 || !strings.HasPrefix(w[0], "mk.lib.F has records for 2 checksums") || !strings.HasPrefix(w[1], "mk.lib.H has records for 2 checksums") {
		t.Errorf("warnings %q, want one for mk.lib.F then one for mk.lib.H", w)
	}
	f, err := os.Open(out)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	got, err := pgo.Read(f)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Records) != 5 {
		t.Errorf("merged %d records, want 5 (F twice, G once, H twice)", len(got.Records))
	}
}

// TestCacheSizeAndClean: size lists the store's kinds in byte order
// and the executables under bin; clean empties both; a missing cache is
// empty and is not created.
func TestCacheSizeAndClean(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "c")
	rep, err := CacheSize(dir)
	if err != nil || rep.Total != 0 || len(rep.Kinds) != 0 {
		t.Fatalf("missing cache: %+v, %v", rep, err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("CacheSize created the cache directory")
	}
	st, err := cache.Open(dir, cache.Options{})
	if err != nil {
		t.Fatal(err)
	}
	for i, k := range []cache.Kind{cache.KindParse, cache.KindFunc, cache.KindFunc} {
		if err := st.Put(k, source.HashString("t", string(rune('a'+i))), bytes.Repeat([]byte{'x'}, 100)); err != nil {
			t.Fatal(err)
		}
	}
	exe := filepath.Join(binOf(dir), "0123", "app.exe")
	os.MkdirAll(filepath.Dir(exe), 0o755)
	os.WriteFile(exe, make([]byte, 50), 0o755)
	rep, err = CacheSize(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Kinds) != 2 || rep.Kinds[0].Kind != "func" || rep.Kinds[0].Entries != 2 || rep.Kinds[1].Kind != "parse" ||
		rep.BinFiles != 1 || rep.BinBytes != 50 || rep.Total <= 0 {
		t.Fatalf("size: %+v", rep)
	}
	if txt := rep.Text(); !strings.Contains(txt, "func     2 entries") || !strings.Contains(txt, "bin      1 entries 50 bytes") {
		t.Errorf("text:\n%s", txt)
	}
	if err := CacheClean(dir); err != nil {
		t.Fatal(err)
	}
	rep, err = CacheSize(dir)
	if err != nil || rep.Total != 0 || len(rep.Kinds) != 0 || rep.BinFiles != 0 {
		t.Fatalf("after clean: %+v, %v", rep, err)
	}
	if err := CacheClean(filepath.Join(t.TempDir(), "none")); err != nil {
		t.Errorf("clean of a missing cache: %v", err)
	}
}

// TestCacheDirFor: an explicit directory wins, then MK_CACHE, then
// <package root>/.mk-cache.
func TestCacheDirFor(t *testing.T) {
	root := writeTree(t, map[string]string{"main.mk": "export Main(sys: &System): int {\n    return 0\n}\n"})
	t.Setenv("MK_CACHE", "")
	got, err := CacheDirFor(root, "")
	if err != nil || got != filepath.Join(root, ".mk-cache") {
		t.Errorf("default: %q, %v", got, err)
	}
	env := filepath.Join(t.TempDir(), "env")
	t.Setenv("MK_CACHE", env)
	if got, _ := CacheDirFor(root, ""); got != env {
		t.Errorf("MK_CACHE: %q, want %q", got, env)
	}
	own := filepath.Join(t.TempDir(), "own")
	if got, _ := CacheDirFor(root, own); got != own {
		t.Errorf("explicit: %q, want %q", got, own)
	}
}
