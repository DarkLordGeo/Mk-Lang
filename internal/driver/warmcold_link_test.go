// Why: a release executable must not depend on which packs a warm cache
// happened to hold.
// What: TestReleaseWarmMixEqualsCold and
// TestDevHitLinksStorePath.
// How: builds the same program warm (some std packs hit, others compile),
// with a fresh cache and with --no-cache, and compares the bytes; the dev
// test checks where each hit is read from.

package driver

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mk/internal/cache"
	"mk/internal/toolchain"
	"mk/internal/types"
)

// TestReleaseWarmMixEqualsCold: a release bin built on a warm cache,
// where some std packs hit and others are compiled, equals a fresh-cache and
// a --no-cache build of the same package byte for byte, at 1 and 8 jobs. The
// sequence: a static release build of a tasks program, then a system-libc
// release build of the same program on the same cache, whose std.sys packs
// miss while the rest hit. x86_64-linux only: ld.lld reads each object at
// the path the driver gives it, while a COFF release link re-links every
// bitcode object under a fixed name first (toolchain thinLink). Before the
// staged hits (stagedHits) the warm bin differed from the --no-cache one at
// both job counts.
func TestReleaseWarmMixEqualsCold(t *testing.T) {
	tools := needTools(t)
	tasks := "import std.atomic\n\nWork(n: &atomic.AtomicInt) {\n    _ = n.Add(1)\n}\n\n" +
		"export Main(sys: &System): WriteError!void {\n    n := atomic.AtomicInt.New(0)\n" +
		"    scope s := sys.io.Scope() {\n        for _ in 0..64 {\n            s.go Work(&n)\n        }\n    }\n" +
		"    try sys.Print(\"tasks {k}\\n\", (k: n.Load()))\n}\n"
	static := writeTree(t, map[string]string{"pkg.mk": ffiManifest(nil), "app/main.mk": tasks})
	libc := writeTree(t, map[string]string{"pkg.mk": ffiManifest([]string{libcLink}), "app/main.mk": tasks})
	cfg := func(root, cache string, jobs int) Config {
		return Config{Root: root, StdDir: ffiStd(), Target: types.X86_64Linux, Profile: types.Release,
			Tools: tools, CacheDir: cache, Jobs: jobs}
	}
	out := t.TempDir()
	nc := cfg(libc, filepath.Join(t.TempDir(), "unused"), 8)
	nc.NoCache = true
	_, ncExe, _ := incBuild(t, nc, filepath.Join(out, "nocache"))
	if _, err := os.Stat(nc.CacheDir); !os.IsNotExist(err) {
		t.Errorf("--no-cache created its cache directory: %v", err)
	}
	if _, coldExe, _ := incBuild(t, cfg(libc, filepath.Join(t.TempDir(), "cold"), 1), filepath.Join(out, "cold")); !bytes.Equal(coldExe, ncExe) {
		t.Errorf("fresh-cache bin differs from the --no-cache bin")
	}
	for _, jobs := range []int{1, 8} {
		t.Run(fmt.Sprintf("jobs%d", jobs), func(t *testing.T) {
			cache := filepath.Join(t.TempDir(), "warm")
			incBuild(t, cfg(static, cache, jobs), filepath.Join(out, fmt.Sprintf("static%d", jobs)))
			warm, warmExe, _ := incBuild(t, cfg(libc, cache, jobs), filepath.Join(out, fmt.Sprintf("warm%d", jobs)))
			hits, compiled := len(warm.Trace.PacksFromCache), len(warm.Trace.PacksCompiled)
			if hits == 0 || compiled == 0 {
				t.Fatalf("warm build: %d packs from the cache, %d compiled; the row needs both", hits, compiled)
			}
			t.Logf("warm build: %d packs from the cache, %d compiled", hits, compiled)
			if !bytes.Equal(warmExe, ncExe) {
				t.Errorf("warm bin (%d bytes) differs from the --no-cache bin (%d bytes)", len(warmExe), len(ncExe))
			}
		})
	}
}

// TestDevHitLinksStorePath: only a release ELF link reads a hit through
// its link in the work directory. A dev build on the same target reads every
// hit at its store path, under <cache>/v1/obj, and makes no file in the work
// directory for it (native_steps.go stagedHits).
func TestDevHitLinksStorePath(t *testing.T) {
	tools := needTools(t)
	root := writeTree(t, map[string]string{"pkg.mk": ffiManifest(nil),
		"app/main.mk": "export Main(sys: &System): WriteError!void {\n    try sys.Print(\"dev\\n\")\n}\n"})
	cacheDir := filepath.Join(t.TempDir(), "cache")
	store := filepath.Join(cacheDir, "v1", "obj")
	out := t.TempDir()
	for _, run := range []string{"cold", "warm"} {
		if run == "warm" {
			// A warm build whose link key hits links nothing, so
			// the cache's copy of the executable goes first and the warm
			// build links the cached objects.
			if err := os.RemoveAll(cache.DirPath(cacheDir, cache.BinDir)); err != nil {
				t.Fatal(err)
			}
		}
		var objs []string
		var work string
		cfg := Config{Root: root, StdDir: ffiStd(), Target: types.X86_64Linux, Profile: types.Dev,
			Tools: tools, CacheDir: cacheDir}
		cfg.TestHooks.OnLink = func(j toolchain.LinkJob) {
			work = filepath.Dir(j.Output)
			objs = append(objs, j.Objects...)
		}
		s, err := NewSession(cfg)
		if err != nil {
			t.Fatal(err)
		}
		r, paths, err := s.Build(filepath.Join(out, run))
		if err != nil {
			t.Fatal(err)
		}
		if len(r.Report.Diags) > 0 || len(paths) != 1 {
			skipKnownBlockers(t, r)
			t.Fatalf("%s build: %d paths\n%s", run, len(paths), r.Report.Text(r.Sources))
		}
		if run == "cold" {
			continue
		}
		if len(r.Trace.PacksFromCache) == 0 || len(r.Trace.PacksCompiled) != 0 {
			t.Fatalf("warm build: %d packs from the cache, %d compiled; the row needs every pack to hit",
				len(r.Trace.PacksFromCache), len(r.Trace.PacksCompiled))
		}
		inStore := 0
		for _, o := range objs {
			if rel, err := filepath.Rel(work, o); err == nil && !strings.HasPrefix(rel, "..") {
				if name := filepath.Base(o); name == "support.o" || (strings.HasPrefix(name, "p") && strings.HasSuffix(name, ".o")) {
					t.Errorf("dev hit %s is linked from the work directory", o)
				}
			}
			if rel, err := filepath.Rel(store, o); err == nil && !strings.HasPrefix(rel, "..") {
				inStore++
			}
		}
		if packs := len(r.Trace.PacksFromCache); inStore < packs {
			t.Errorf("dev link read %d objects at store paths under %s, want the %d hits: %v", inStore, store, packs, objs)
		}
	}
}
