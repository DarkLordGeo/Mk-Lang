// Why: a staged release link copies stored objects into the work directory,
// and the store can be emptied under a running build; that must give one
// clear error, or a recompile, never a flood of errors or a stale object.
// What: TestUnplacedObjectsReportedOnce and
// TestCompileStepHitRecompiled.
// How: builds for x86_64-linux release (stagedHits stages every hit) and
// makes object placement fail through TestHooks.PlaceObject.

package driver

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"mk/internal/cache"
	"mk/internal/types"
)

// TestUnplacedObjectsReportedOnce: when the stored objects of a staged
// release link cannot be placed in the work directory (the store emptied
// under the build, `mk cache clean` in another process), the build reports
// one E0001, the lowest pack's, with how many more packs failed, not one
// E0001 per pack. x86_64-linux, whose release link stages every hit
// (stagedHits).
func TestUnplacedObjectsReportedOnce(t *testing.T) {
	tools := needTools(t)
	root := writeTree(t, map[string]string{"pkg.mk": ffiManifest(nil),
		"app/main.mk": "export Main(sys: &System): WriteError!void {\n    try sys.Print(\"staged\\n\")\n}\n"})
	cacheDir := filepath.Join(t.TempDir(), "cache")
	cfg := Config{Root: root, StdDir: ffiStd(), Target: types.X86_64Linux, Profile: types.Release, Tools: tools, CacheDir: cacheDir}
	out := t.TempDir()
	incBuild(t, cfg, filepath.Join(out, "cold"))
	// A warm build whose link key hits links nothing.
	if err := os.RemoveAll(cache.DirPath(cacheDir, cache.BinDir)); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	cfg.TestHooks.PlaceObject = func(src, dst string) error {
		calls.Add(1)
		return &fs.PathError{Op: "link", Path: dst, Err: fs.ErrNotExist}
	}
	s, err := NewSession(cfg)
	if err != nil {
		t.Fatal(err)
	}
	r, _, err := s.Build(filepath.Join(out, "warm"))
	if err != nil {
		t.Fatal(err)
	}
	n := int(calls.Load())
	if n < 2 {
		t.Fatalf("%d stored objects placed; the row needs several hits", n)
	}
	text := r.Report.Text(r.Sources)
	t.Logf("%d stored objects unplaced:\n%s", n, text)
	if len(r.Report.Diags) != 1 || r.Report.Diags[0].Diag.Code.String() != "E0001" {
		t.Fatalf("%d hits unplaced: want one E0001, got %d diagnostics:\n%s", n, len(r.Report.Diags), text)
	}
	if !strings.Contains(text, "cannot place the stored object of p0000 in the work directory") {
		t.Errorf("want the lowest pack named:\n%s", text)
	}
	if more := n - 1; !strings.Contains(text, "nor those of "+strconv.Itoa(more)+" more packs") {
		t.Errorf("want %d more packs counted:\n%s", more, text)
	}
	if strings.Contains(text, cacheDir) {
		t.Errorf("the store path is in the text:\n%s", text)
	}
}

// TestCompileStepHitRecompiled: a pack whose stored object hits only at
// the compile step (emitPacks missed it, so its .ll is in the work
// directory) and cannot be placed is compiled, as a miss is, instead of
// reported. EmitMissObjects makes every pack such a hit; the warm build
// links with no diagnostic, compiles every pack whose placement failed, and
// gives the cold build's bytes.
func TestCompileStepHitRecompiled(t *testing.T) {
	tools := needTools(t)
	root := writeTree(t, map[string]string{"pkg.mk": ffiManifest(nil),
		"app/main.mk": "export Main(sys: &System): WriteError!void {\n    try sys.Print(\"staged\\n\")\n}\n"})
	cacheDir := filepath.Join(t.TempDir(), "cache")
	cfg := Config{Root: root, StdDir: ffiStd(), Target: types.X86_64Linux, Profile: types.Release, Tools: tools, CacheDir: cacheDir}
	out := t.TempDir()
	_, cold, _ := incBuild(t, cfg, filepath.Join(out, "cold"))
	if err := os.RemoveAll(cache.DirPath(cacheDir, cache.BinDir)); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var placed []string
	cfg.TestHooks.EmitMissObjects = true
	cfg.TestHooks.PlaceObject = func(src, dst string) error {
		mu.Lock()
		placed = append(placed, strings.TrimSuffix(filepath.Base(dst), ".o"))
		mu.Unlock()
		return &fs.PathError{Op: "link", Path: dst, Err: fs.ErrNotExist}
	}
	r, warm, _ := incBuild(t, cfg, filepath.Join(out, "warm"))
	if len(placed) < 2 {
		t.Fatalf("%d compile-step hits; the row needs several", len(placed))
	}
	for _, p := range placed {
		if !slices.Contains(r.Trace.PacksCompiled, p) {
			t.Errorf("pack %s was a hit it could not place, and was not compiled (compiled %q)", p, r.Trace.PacksCompiled)
		}
	}
	if !bytes.Equal(cold, warm) {
		t.Errorf("the warm build's %d bytes differ from the cold build's %d", len(warm), len(cold))
	}
}
