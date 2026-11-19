// Why: tools/bench ir reads the pack IR that mk build's hidden --emit-ll
// writes; its layout and content must stay what the tool expects.
// What: tests of --emit-ll (TestHooks.LLDir), with the helper readLLDir.
// How: builds bins with the flag set, cold and warm, and compares each
// <dir>/<bin>/<pack>.ll file with the IR TestHooks.OnLL sees.

package driver

import (
	"bytes"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"mk/internal/types"
)

// readLLDir is the files of one --emit-ll directory: its single bin
// subdirectory's name and, by file name, every file in it.
func readLLDir(t *testing.T, dir string) (string, map[string][]byte) {
	t.Helper()
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(ents) != 1 || !ents[0].IsDir() {
		t.Fatalf("%s: want one bin directory, got %v", dir, ents)
	}
	bin := ents[0].Name()
	files, err := os.ReadDir(filepath.Join(dir, bin))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string][]byte{}
	for _, f := range files {
		b, err := os.ReadFile(filepath.Join(dir, bin, f.Name()))
		if err != nil {
			t.Fatal(err)
		}
		out[f.Name()] = b
	}
	return bin, out
}

// TestEmitLLDump: with LLDir a release build writes every pack's IR,
// byte for byte the text OnLL sees, to <dir>/<bin>/<pack>.ll. The dump is
// whole on a warm build whose every object is a cache hit, a rerun
// replaces the earlier pack files and leaves other files alone, and the
// dump, the linked objects and the executable are the same cold and warm,
// with 1 and 8 workers, and with the flag off. It is in no key: a build
// without the flag into the flagged builds' cache takes every pack from it.
func TestEmitLLDump(t *testing.T) {
	tools := needTools(t)
	dir := writeTree(t, incPkg("x + 1"))
	out := t.TempDir()
	base := Config{Root: dir, Profile: types.Release, Tools: tools}

	plain := base
	plain.CacheDir, plain.Jobs = filepath.Join(t.TempDir(), "c0"), 8
	_, plainExe, plainObjs := incBuild(t, plain, filepath.Join(out, "plain"))

	// Cold, 8 workers: every pack compiled, the dump equals OnLL's texts.
	ll1 := filepath.Join(t.TempDir(), "ll")
	cache := filepath.Join(t.TempDir(), "c1")
	var mu sync.Mutex
	seen := map[string][]byte{}
	cold := base
	cold.CacheDir, cold.Jobs, cold.TestHooks.LLDir = cache, 8, ll1
	cold.TestHooks.OnLL = func(name, ll string) { mu.Lock(); seen[name+".ll"] = []byte(ll); mu.Unlock() }
	cr, coldExe, coldObjs := incBuild(t, cold, filepath.Join(out, "cold"))
	if len(cr.Trace.PacksCompiled) == 0 || len(cr.Trace.PacksFromCache) != 0 {
		t.Fatalf("cold: compiled %v, from cache %v", cr.Trace.PacksCompiled, cr.Trace.PacksFromCache)
	}
	bin, coldLL := readLLDir(t, ll1)
	if bin != "app" {
		t.Errorf("bin directory %q, want app", bin)
	}
	if len(seen) < 2 || !maps.EqualFunc(coldLL, seen, bytes.Equal) {
		t.Errorf("dump %v, want OnLL's %v, byte for byte", slices.Sorted(maps.Keys(coldLL)), slices.Sorted(maps.Keys(seen)))
	}
	if _, ok := coldLL["support.ll"]; !ok {
		t.Error("no support.ll in the dump")
	}

	// Warm, 1 worker, the same directory with a stale pack file and a
	// file of the user's: every object from the cache, the dump whole.
	binDir := filepath.Join(ll1, "app")
	if err := os.WriteFile(filepath.Join(binDir, "p9999.ll"), []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(binDir, "notes.txt"), []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	warm := base
	warm.CacheDir, warm.Jobs, warm.TestHooks.LLDir = cache, 1, ll1
	wr, warmExe, warmObjs := incBuild(t, warm, filepath.Join(out, "warm"))
	if len(wr.Trace.PacksCompiled) != 0 || !slices.Equal(wr.Trace.PacksFromCache, cr.Trace.PacksCompiled) {
		t.Fatalf("warm: compiled %v, from cache %v, want every pack of %v from the cache", wr.Trace.PacksCompiled, wr.Trace.PacksFromCache, cr.Trace.PacksCompiled)
	}
	_, warmLL := readLLDir(t, ll1)
	if string(warmLL["notes.txt"]) != "mine" {
		t.Error("the rerun removed a file that is not a pack's")
	}
	delete(warmLL, "notes.txt")
	if _, ok := warmLL["p9999.ll"]; ok {
		t.Error("the rerun left an earlier build's pack file")
	}
	if !maps.EqualFunc(warmLL, coldLL, bytes.Equal) {
		t.Errorf("warm dump %v differs from the cold one %v", slices.Sorted(maps.Keys(warmLL)), slices.Sorted(maps.Keys(coldLL)))
	}

	// Cold, 1 worker, a fresh cache and directory.
	ll2 := filepath.Join(t.TempDir(), "ll")
	one := base
	one.CacheDir, one.Jobs, one.TestHooks.LLDir = filepath.Join(t.TempDir(), "c2"), 1, ll2
	_, oneExe, oneObjs := incBuild(t, one, filepath.Join(out, "one"))
	_, oneLL := readLLDir(t, ll2)
	if !maps.EqualFunc(oneLL, coldLL, bytes.Equal) {
		t.Error("the 1-worker dump differs from the 8-worker one")
	}

	// Warm without the flag, into the flagged builds' cache: the
	// directory is in no key, so every pack is a hit.
	nf := base
	nf.CacheDir, nf.Jobs = cache, 8
	nr, nfExe, nfObjs := incBuild(t, nf, filepath.Join(out, "noflag"))
	if len(nr.Trace.PacksCompiled) != 0 || !slices.Equal(nr.Trace.PacksFromCache, cr.Trace.PacksCompiled) {
		t.Errorf("warm without the flag: compiled %v, from cache %v, want every pack of %v from the cache", nr.Trace.PacksCompiled, nr.Trace.PacksFromCache, cr.Trace.PacksCompiled)
	}

	if len(plainObjs) < 2 {
		t.Fatalf("the plain build linked %d objects", len(plainObjs))
	}
	for name, b := range map[string]struct {
		exe  []byte
		objs [][]byte
	}{"cold": {coldExe, coldObjs}, "warm": {warmExe, warmObjs}, "one worker": {oneExe, oneObjs}, "warm without the flag": {nfExe, nfObjs}} {
		if !bytes.Equal(b.exe, plainExe) {
			t.Errorf("%s: the executable differs from the build without the flag", name)
		}
		// The warm builds' link keys hit, so the stored link is reused: they
		// link nothing, and their executables are the stored link's,
		// compared above.
		if strings.HasPrefix(name, "warm") && len(b.objs) == 0 {
			continue
		}
		if !slices.EqualFunc(b.objs, plainObjs, bytes.Equal) {
			t.Errorf("%s: the %d linked objects differ from the %d of the build without the flag", name, len(b.objs), len(plainObjs))
		}
	}
}

// TestEmitLLUnwritable: a directory that cannot be made is E0001
// under fs, and no executable is written.
func TestEmitLLUnwritable(t *testing.T) {
	tools := needTools(t)
	dir := writeTree(t, incPkg("x + 1"))
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := NewSession(Config{Root: dir, Tools: tools, CacheDir: t.TempDir(), TestHooks: TestHooks{LLDir: filepath.Join(file, "ll")}})
	if err != nil {
		t.Fatal(err)
	}
	r, paths, err := s.Build(filepath.Join(t.TempDir(), "app"+exeSuffix(s.cfg.Target)))
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 0 || !slices.Equal(codes(r), []string{"E0001"}) || fact(r.Report.Diags[0].Diag, "tool") != "fs" {
		t.Fatalf("paths %v, report:\n%s", paths, r.Report.Text(r.Sources))
	}
}

// TestEmitLLWriteFails: a pack file that cannot be written (here a
// directory stands at p0000.ll, which the rerun's cleanup leaves, since it
// removes regular files only) is E0001 under fs naming the file, and no
// executable is written.
func TestEmitLLWriteFails(t *testing.T) {
	tools := needTools(t)
	dir := writeTree(t, incPkg("x + 1"))
	ll := filepath.Join(t.TempDir(), "ll")
	if err := os.MkdirAll(filepath.Join(ll, "app", "p0000.ll", "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	s, err := NewSession(Config{Root: dir, Tools: tools, CacheDir: t.TempDir(), TestHooks: TestHooks{LLDir: ll}})
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "app"+exeSuffix(s.cfg.Target))
	r, paths, err := s.Build(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 0 || !slices.Equal(codes(r), []string{"E0001"}) {
		t.Fatalf("paths %v, report:\n%s", paths, r.Report.Text(r.Sources))
	}
	d := r.Report.Diags[0].Diag
	if fact(d, "tool") != "fs" || !strings.Contains(d.Message, "cannot write p0000.ll into the --emit-ll directory") {
		t.Errorf("got %q, tool %q", d.Message, fact(d, "tool"))
	}
	if _, err := os.Stat(out); err == nil {
		t.Errorf("%s was written", out)
	}
}

// TestIsPackLL: the names a rerun may remove.
func TestIsPackLL(t *testing.T) {
	for name, want := range map[string]bool{"p0000.ll": true, "p12345.ll": true, "support.ll": true,
		"p000.ll": false, "p00a0.ll": false, "q0000.ll": false, "p0000.ll.txt": false, "support.o": false, "notes.ll": false} {
		if got := isPackLL(name); got != want {
			t.Errorf("isPackLL(%q) = %v, want %v", name, got, want)
		}
	}
}
