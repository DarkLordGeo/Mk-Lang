// Why: the module cache's speed comes from a few layout choices that a
// later change can undo without any test failing on output alone.
// What: tests of function results bundled per stored module
// (funcbundle.go), the instance-limit walk cached per module
// (walkcache.go), lazy decoding of hits' function results (lazybody.go),
// toolchain discovery started before the check, and pack IR hashed in
// memory (native_steps.go).
// How: counts cache entry files by kind with cacheFiles, and compares warm
// and cold reports and traces.

package driver

import (
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"mk/internal/cache"
	"mk/internal/eval"
	"mk/internal/source"
	"mk/internal/toolchain"
	"mk/internal/types"
)

// cacheFiles counts the entry files of kind under the cache root.
func cacheFiles(t *testing.T, dir string, kind cache.Kind) int {
	t.Helper()
	n := 0
	root := filepath.Join(dir, "v1", string(kind))
	filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			n++
		}
		return nil
	})
	return n
}

// TestPerfBack_BundlesOneFuncEntryPerModule: a cold check writes at most
// one func entry (a bundle) and three manifests (the module's candidate
// list, its function index and its parse index) per stored module,
// not one of each per function or file, and no per-file parse entry;
// a warm check loads every module from its bundle; after a body edit of
// lib.F the unchanged lib.G is reused from lib's bundle through the index,
// and the report equals a cold one's.
func TestPerfBack_BundlesOneFuncEntryPerModule(t *testing.T) {
	dir := writeTree(t, incPkg("x + 1"))
	cfg := Config{Root: dir, CacheDir: t.TempDir(), Target: types.X86_64Windows}
	cold := frontCheck(t, cfg)
	if len(cold.Report.Diags) > 0 || len(cold.Trace.NotStored) > 0 {
		t.Fatalf("cold: %v, not stored %q", codes(cold), cold.Trace.NotStored)
	}
	mods := len(cold.Trace.ModulesResolved)
	funcs := cacheFiles(t, cfg.CacheDir, cache.KindFunc)
	mans := cacheFiles(t, cfg.CacheDir, cache.KindManifest)
	parses := cacheFiles(t, cfg.CacheDir, cache.KindParse)
	if funcs == 0 || funcs > mods || mans > 3*mods || parses > 0 {
		t.Fatalf("%d modules stored, %d func entries, %d manifests, %d parse entries: want at most one bundle and three manifests per module, and no parse entry",
			mods, funcs, mans, parses)
	}
	warm := frontCheck(t, cfg)
	if !slices.Contains(warm.Trace.ModulesFromCache, "lib") || !slices.Contains(warm.Trace.ModulesFromCache, "app") {
		t.Fatalf("warm: from cache %q", warm.Trace.ModulesFromCache)
	}
	if err := os.WriteFile(filepath.Join(dir, "lib", "lib.mk"), []byte(incPkg("x + 2")["lib/lib.mk"]), 0o644); err != nil {
		t.Fatal(err)
	}
	edit := frontCheck(t, cfg)
	var reused []string
	for _, k := range edit.Trace.ItemsFromCache {
		reused = append(reused, k.String())
	}
	if !slices.Contains(reused, "lib.G") || slices.Contains(reused, "lib.F") {
		t.Errorf("after editing lib.F: reused %q, want lib.G and not lib.F", reused)
	}
	cfg2 := cfg
	cfg2.CacheDir = t.TempDir()
	if a, b := jsonOf(t, edit), jsonOf(t, frontCheck(t, cfg2)); string(a) != string(b) {
		t.Errorf("edited warm check %s, cold %s", a, b)
	}
}

// TestPerfBack_WalkMarkers: a clean check marks the walk of every package
// module whose interfaces are known; a check with a diagnostic marks none
// of its modules (a module whose bodies fail is still stored, with its
// diagnostics, so it has a walk key, but the walk is not marked).
func TestPerfBack_WalkMarkers(t *testing.T) {
	dir := writeTree(t, incPkg("x + 1"))
	cfg := Config{Root: dir, CacheDir: t.TempDir(), Target: types.X86_64Windows}
	s, err := NewSession(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if r := s.Check(); len(r.Report.Diags) > 0 {
		t.Fatalf("cold: %v", codes(r))
	}
	for _, name := range []string{"lib", "app"} {
		m := s.pkgB.byName[name]
		if m == nil {
			t.Fatalf("no module %s", name)
		}
		if !s.w.front.walkedClean(m) {
			t.Errorf("%s: no walk marker after a clean check", name)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "lib", "lib.mk"), []byte(incPkg("x + true")["lib/lib.mk"]), 0o644); err != nil {
		t.Fatal(err)
	}
	s2, err := NewSession(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if r := s2.Check(); len(r.Report.Diags) == 0 {
		t.Fatalf("broken lib: %v", codes(r))
	}
	for _, name := range []string{"lib", "app"} {
		if m := s2.pkgB.byName[name]; m != nil {
			if s2.w.front.walkedClean(m) {
				t.Errorf("%s: walk marked clean in a build whose lib does not check", name)
			}
		}
	}
}

// TestPerfBack_LazyBodyDecodeFailure: a hit's function result that does
// not decode is an ICE where the body is used and an E0000 at the join,
// never a crash of the prefetch (lazybody.go).
func TestPerfBack_LazyBodyDecodeFailure(t *testing.T) {
	fc := &frontCache{}
	e := fc.lazyEntry("lib", types.ItemKey("lib", "F"), []byte{0xff, 0xff, 0xff})
	fc.prefetch([]*bodyEntry{e})
	ds := fc.join()
	if len(ds) != 1 || ds[0].Code.String() != "E0000" {
		t.Fatalf("join: %v, want one E0000", ds)
	}
	ie := source.Guard("use", source.Span{}, func() { e.force() })
	if ie == nil || !strings.Contains(ie.What, "lib.F") {
		t.Fatalf("force of a bad result: %v, want an ICE naming lib.F", ie)
	}
	if e.state != eval.BodyPoisoned || e.mir != nil {
		t.Errorf("bad result: state %v, mir %v; want poisoned with no MIR", e.state, e.mir)
	}
	if len(fc.join()) != 0 {
		t.Error("a second join reported the failure again")
	}
}

// TestPerfBack_WarmHitsDecodeLazily: a warm check's module hits leave their
// function results undecoded until used or prefetched, and every one is
// decoded once the stage is through (the join); the report equals the cold
// one's.
func TestPerfBack_WarmHitsDecodeLazily(t *testing.T) {
	dir := writeTree(t, incPkg("x + 1"))
	cfg := Config{Root: dir, CacheDir: t.TempDir(), Target: types.X86_64Windows}
	cold := frontCheck(t, cfg)
	s, err := NewSession(cfg)
	if err != nil {
		t.Fatal(err)
	}
	warm := s.Check()
	if a, b := jsonOf(t, cold), jsonOf(t, warm); string(a) != string(b) {
		t.Fatalf("warm %s, cold %s", a, b)
	}
	lazy := 0
	for _, m := range s.pkgB.mods {
		if m.hit == nil {
			t.Fatalf("%s: no hit", m.path)
		}
		for id, e := range m.hit.bodies {
			if e.lazy == nil {
				continue
			}
			lazy++
			if e.lazy.decode == nil || e.lazy.err != nil {
				t.Errorf("%v: lazy entry %+v", id, e.lazy)
			}
			// incPkg has no rejected function, so every result is a ready body.
			if got := s.w.sem5.body(id); got != e || e.mir == nil || e.state != eval.BodyReady {
				t.Errorf("%v: after the check, body %v state %v", id, e.mir, e.state)
			}
		}
	}
	if lazy == 0 {
		t.Error("no module hit holds a lazy function result")
	}
}

// TestPerfBack_ToolsAfterCheck: a build starts toolchain discovery before
// its check, but a check that fails reports only its own diagnostics, and a
// clean one with no usable LLVM still reports the E0001.
func TestPerfBack_ToolsAfterCheck(t *testing.T) {
	noLLVM := t.TempDir()
	bad := writeTree(t, incPkg("x + true"))
	s, err := NewSession(Config{Root: bad, LLVMDir: noLLVM, NoCache: true, Target: types.X86_64Windows})
	if err != nil {
		t.Fatal(err)
	}
	r, _, err := s.Build(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if cs := codes(r); len(cs) == 0 || slices.Contains(cs, "E0001") {
		t.Errorf("failing check: %v, want its diagnostics and no E0001", cs)
	}
	good := writeTree(t, incPkg("x + 1"))
	s, err = NewSession(Config{Root: good, LLVMDir: noLLVM, NoCache: true, Target: types.X86_64Windows})
	if err != nil {
		t.Fatal(err)
	}
	r, _, err = s.Build(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if cs := codes(r); !slices.Equal(cs, []string{"E0001"}) {
		t.Errorf("clean check, no LLVM: %v, want E0001", cs)
	}
}

// TestPerfBack_HitPacksWriteNoIR: a body edit's build writes the IR of the
// packs it compiles and of no pack whose object is in the cache (the keys
// are computed from the IR in memory, native_steps.go emitPacks).
func TestPerfBack_HitPacksWriteNoIR(t *testing.T) {
	tools := needTools(t)
	dir := writeTree(t, incPkg("x + 1"))
	cfg := Config{Root: dir, Target: types.X86_64Windows, Tools: tools, CacheDir: filepath.Join(t.TempDir(), "cache")}
	out := t.TempDir()
	incBuild(t, cfg, filepath.Join(out, "cold"))
	if err := os.WriteFile(filepath.Join(dir, "lib", "lib.mk"), []byte(incPkg("x + 2")["lib/lib.mk"]), 0o644); err != nil {
		t.Fatal(err)
	}
	var lls []string
	cfg.TestHooks.OnLink = func(j toolchain.LinkJob) {
		ents, _ := os.ReadDir(filepath.Dir(j.Output))
		for _, e := range ents {
			if strings.HasSuffix(e.Name(), ".ll") {
				lls = append(lls, e.Name())
			}
		}
	}
	s, err := NewSession(cfg)
	if err != nil {
		t.Fatal(err)
	}
	r, _, err := s.Build(filepath.Join(out, "edit"))
	if err != nil || len(r.Report.Diags) > 0 {
		t.Fatalf("edit build: %v %v", err, codes(r))
	}
	if len(r.Trace.PacksCompiled) != 1 || len(r.Trace.PacksFromCache) == 0 {
		t.Fatalf("edit build compiled %v, from cache %v", r.Trace.PacksCompiled, r.Trace.PacksFromCache)
	}
	if len(lls) != 1 || lls[0] != r.Trace.PacksCompiled[0]+".ll" {
		t.Errorf("IR files at link %q, want only the compiled pack %q", lls, r.Trace.PacksCompiled)
	}
}
