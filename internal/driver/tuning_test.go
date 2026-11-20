// Why: Config.Tune switches release code-generation experiments (fatlto,
// unroll, novec) on one cache shared with default builds; an experiment
// must never take a default build's objects or links, nor leave them
// changed, and a warm link must equal a cold one.
// What: tuneFiles, tuneTargets, TestTuningKeysSeparate,
// TestMultiBinColdEqualsNoCache and
// TestStagedCopyNeverWritesThrough.
// How: builds one program under each Tune into one cache and compares every
// result with a --no-cache build of the same Tune, on COFF and on ELF; the
// link hooks check that every link reads its objects under the cold names
// in its own work directory.

package driver

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"mk/internal/cache"
	"mk/internal/source"
	"mk/internal/toolchain"
	"mk/internal/types"
)

// tuneFiles is a small program with loops the unroller and the vectorizers
// can see, in two modules, so the link has more than one pack. Digits'
// loop has a constant trip count of 21, as fmtints' digit loop
// (tests/bench/same/fmtints/mk/fmtints.mk:40) has, so the full unroller of
// the pack compile (-O3 -flto=thin, LLVM's threshold 300) unrolls it, and
// one with unroll=20 does not.
func tuneFiles() map[string]string {
	return map[string]string{
		"pkg.mk": manifest(map[string]string{"bins": `[pkg.Bin(name: "app", root: "app", link_script: "", cpu: "", subsystem: "console")]`}),
		"sum/sum.mk": "export Sum(n: int): i64 {\n    var s: i64 = 0\n    for i in 0..n {\n        s = s +% Cast[i64](i) *% 3\n    }\n    return s\n}\n\n" +
			"export Digits(x: u64): u64 {\n    var v: u64 = x\n    var s: u64 = 0\n    for _ in 0..21 {\n        s = s *% 3 +% v % 10\n        v = v / 10\n    }\n    return s\n}\n",
		"app/app.mk": "import sum\n\nexport Main(sys: &System): int {\n    return Cast[int]((sum.Sum(1000) +% Cast[i64](sum.Digits(Cast[u64](sum.Sum(77))) % 1000)) % 7)\n}\n",
	}
}

// tuneTargets are the targets TestTuningKeysSeparate builds for: the
// host's, and x86_64-linux cross-built when the host is not Linux, since
// ld.lld's in-process ThinLTO is where an all-hit warm link once read the
// store's object paths and linked other code.
func tuneTargets() []types.Target {
	if pkTarget() == types.X86_64Linux {
		return []types.Target{types.X86_64Linux}
	}
	return []types.Target{pkTarget(), types.X86_64Linux}
}

// TestTuningKeysSeparate: per target, in one cache, release:
//   - a default build, and a --no-cache default build with the same bytes;
//   - fatlto (every pack compiled again: its objects are -flto=full,
//     another object key), and fatlto with unroll=600;
//   - unroll=llvm and novec, whose ThinLTO objects are the default's by key
//     (no pack compiled), but not their links;
//   - unroll=20, whose pack compiles take the threshold: every pack
//     compiled again, and Digits' pack object differs from the default's;
//
// each byte-equal to a --no-cache build with the same Tune (on ELF that
// needs the all-hit warm link to read its objects under the names a cold
// link does); then the default again, which compiles and links nothing
// new and has the first build's bytes; then the default once more with
// the stored link and lld's own ThinLTO cache removed, so the backends
// rerun on objects that all hit, which must still equal --no-cache.
// The targets run in parallel: each has its own cache and output
// directory, and the builds only read the source tree.
func TestTuningKeysSeparate(t *testing.T) {
	tools := needTools(t)
	dir := writeTree(t, tuneFiles())
	for _, target := range tuneTargets() {
		t.Run(target.Name, func(t *testing.T) {
			t.Parallel()
			out := t.TempDir()
			cacheDir := filepath.Join(t.TempDir(), "cache")
			base := Config{Root: dir, Target: target, Profile: types.Release, Tools: tools, CacheDir: cacheDir}
			var coldNames []string // the cold build's object names, in link order
			build := func(label string, tu toolchain.Tuning, noCache bool) (Result, []byte, [][]byte) {
				t.Helper()
				cfg := base
				cfg.Tune, cfg.NoCache = tu, noCache
				// A link whose LTO runs inside lld (every ELF one,
				// COFF fatlto) reads every object, stored or compiled,
				// under the name a cold build gives it, in the work
				// directory, never under the store's path. This holds
				// whatever the store's names are; the byte checks below
				// see the effect only when ld.lld's output happens to
				// depend on them.
				staged := toolchain.FormatOf(target.LLVMTriple) == toolchain.ELF || tu.FatLTO
				cfg.TestHooks.OnLink = func(j toolchain.LinkJob) {
					var names []string
					for _, o := range j.Objects {
						names = append(names, filepath.Base(o))
					}
					if coldNames == nil {
						coldNames = names
						return
					}
					if staged && !slices.Equal(names, coldNames) {
						t.Errorf("%s: the link reads its objects as %q, a cold build as %q", label, names, coldNames)
					}
				}
				return incBuild(t, cfg, filepath.Join(out, label+".exe"))
			}
			r0, def, defObjs := build("default", toolchain.Tuning{}, false)
			if len(r0.Trace.PacksCompiled) == 0 || !r0.Trace.Linked {
				t.Fatalf("cold default build compiled %v, linked %v", r0.Trace.PacksCompiled, r0.Trace.Linked)
			}
			if want := len(r0.Trace.PacksCompiled); len(coldNames) != want || coldNames[want-1] != "support.o" {
				t.Fatalf("the cold build's objects are %q, want %d packs ending in support.o", coldNames, want)
			}
			if _, fresh, _ := build("default-nocache", toolchain.Tuning{}, true); !bytes.Equal(def, fresh) {
				t.Fatal("the cold default build differs from a --no-cache one")
			}
			nPacks := len(r0.Trace.PacksCompiled)
			for _, c := range []struct {
				name         string
				tu           toolchain.Tuning
				wantCompiled bool // the packs' objects have other keys
				wantObjDiff  bool // and other bytes, besides the support pack's
			}{
				{"fatlto", toolchain.Tuning{FatLTO: true}, true, true},
				// fatlto's pack compiles take the Tune's LLVM options, so
				// another option beside it gives other objects again.
				{"fatlto-unroll-600", toolchain.Tuning{FatLTO: true, Unroll: 600}, true, false},
				{"unroll-llvm", toolchain.Tuning{Unroll: toolchain.UnrollLLVM}, false, false},
				{"novec", toolchain.Tuning{NoLoopVec: true}, false, false},
				{"unroll-20", toolchain.Tuning{Unroll: 20}, true, true},
			} {
				r, exe, objs := build(c.name, c.tu, false)
				_, fresh, _ := build(c.name+"-nocache", c.tu, true)
				if !bytes.Equal(exe, fresh) {
					t.Errorf("%s: the warm build's executable differs from a --no-cache build with the same Tune", c.name)
				}
				if !r.Trace.Linked {
					t.Errorf("%s: not linked; the link key must hash the Tune", c.name)
				}
				// The support pack is native -O3 in every Tune, so it always hits.
				got := len(r.Trace.PacksCompiled)
				if c.wantCompiled && got != nPacks-1 || !c.wantCompiled && got != 0 {
					t.Errorf("%s: compiled %v after the default build (cold: %d packs)", c.name, r.Trace.PacksCompiled, nPacks)
				}
				same := sameObjects(objs, defObjs)
				t.Logf("%s: compiled %d packs, objects equal to the default's %v, executable equal to the default's %v", c.name, got, same, bytes.Equal(exe, def))
				if c.wantObjDiff && same {
					t.Errorf("%s: every object is the default's: the experiment did not reach the pack compile", c.name)
				}
				if c.wantObjDiff && bytes.Equal(exe, def) {
					t.Errorf("%s: the executable is the default's: the experiment did not reach the build", c.name)
				}
			}
			r, again, _ := build("default-again", toolchain.Tuning{}, false)
			if !bytes.Equal(again, def) || len(r.Trace.PacksCompiled) != 0 {
				t.Errorf("default after the experiments: equal to the first default build %v, compiled %v", bytes.Equal(again, def), r.Trace.PacksCompiled)
			}
			// lld's prune (48 h, or its size bound) can empty the
			// ThinLTO cache while every object stays in the store.
			for _, d := range []string{cache.DirPath(cacheDir, cache.BinDir), cache.DirPath(cacheDir, "thinlto")} {
				if err := os.RemoveAll(d); err != nil {
					t.Fatal(err)
				}
			}
			r, pruned, _ := build("default-pruned", toolchain.Tuning{}, false)
			if !r.Trace.Linked || len(r.Trace.PacksCompiled) != 0 {
				t.Errorf("default with the stored link and lld's cache removed: linked %v, compiled %v", r.Trace.Linked, r.Trace.PacksCompiled)
			}
			if !bytes.Equal(pruned, def) {
				t.Error("default with the stored link and lld's cache removed: the executable differs from a --no-cache build")
			}
		})
	}
}

// sameObjects reports whether two builds linked objects with the same
// bytes, in order.
func sameObjects(a, b [][]byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !bytes.Equal(a[i], b[i]) {
			return false
		}
	}
	return true
}

// TestMultiBinColdEqualsNoCache: a cold x86_64-linux release build,
// with a store, of a package with two bins that share a module (sum). Bin a
// compiles and stores every pack; bin b, in the same build, finds std's
// packs and sum's stored. Without the staging bin b's link reads those at
// the store's paths, so its executable can differ from a --no-cache
// build's. Each bin of each build must equal its --no-cache build, and
// every link must read its objects under the cold names (p0000.o, ...,
// support.o) in its one work directory.
//
// Cold builds, each into a fresh store. In the first, lld's ThinLTO cache
// keeps bin a's backend outputs, which bin b's shared modules reuse, so on
// this small program bin b's bytes match --no-cache even unstaged (lld's
// cache masks it); the name check is what catches a lost staging there,
// whatever the store's hashes are. The others empty lld's cache just before
// bin b's link, as lld's own prune after bin a's link can when the cache
// passes its size bound (a quarter of the budget), so bin b's backends run
// on its objects. Each runs under a fixed build ID, so its store names, and
// their order, do not move with the test binary; with the staging removed,
// bin b's bytes differ under IDs 1 and 3.
func TestMultiBinColdEqualsNoCache(t *testing.T) {
	tools := needTools(t)
	files := tuneFiles()
	files["pkg.mk"] = manifest(map[string]string{"bins": `[pkg.Bin(name: "a", root: "a", link_script: "", cpu: "", subsystem: "console"), pkg.Bin(name: "b", root: "b", link_script: "", cpu: "", subsystem: "console")]`})
	delete(files, "app/app.mk")
	files["a/a.mk"] = "import sum\n\nexport Main(sys: &System): int {\n    return Cast[int]((sum.Sum(1000) +% Cast[i64](sum.Digits(Cast[u64](sum.Sum(77))) % 1000)) % 7)\n}\n"
	files["b/b.mk"] = "import sum\n\nexport Main(sys: &System): int {\n    var t: u64 = 0\n    for i in 0..9 {\n        t = t +% sum.Digits(Cast[u64](i) *% 7919)\n    }\n    return Cast[int](t % 5)\n}\n"
	dir := writeTree(t, files)
	// build builds both bins into cacheDir ("" for --no-cache) as the
	// compiler id (zero: the test binary's own); with prune, lld's
	// ThinLTO cache is removed before the second link.
	build := func(t *testing.T, label, cacheDir string, id source.Hash, prune bool) (Result, map[string][]byte) {
		t.Helper()
		cfg := Config{Root: dir, Target: types.X86_64Linux, Profile: types.Release, Tools: tools, CacheDir: cacheDir, NoCache: cacheDir == "", BuildID: id}
		if cfg.NoCache {
			cfg.CacheDir = filepath.Join(t.TempDir(), "unused")
		}
		links := 0
		cfg.TestHooks.OnLink = func(j toolchain.LinkJob) {
			links++
			if prune && links == 2 {
				if j.LTOCacheDir == "" {
					t.Errorf("%s: the second link has no ThinLTO cache to empty", label)
				} else if err := os.RemoveAll(j.LTOCacheDir); err != nil {
					t.Fatal(err)
				}
			}
			if len(j.Objects) == 0 {
				t.Errorf("%s: link %d reads no object", label, links)
				return
			}
			work := filepath.Dir(j.Objects[0])
			for i, o := range j.Objects {
				want := fmt.Sprintf("p%04d.o", i)
				if i == len(j.Objects)-1 {
					want = "support.o"
				}
				if filepath.Dir(o) != work || filepath.Base(o) != want {
					t.Errorf("%s: link %d reads object %d as %s, want %s in its work directory %s", label, links, i, o, want, work)
				}
			}
		}
		s, err := NewSession(cfg)
		if err != nil {
			t.Fatal(err)
		}
		r, paths, err := s.Build(filepath.Join(t.TempDir(), label))
		if err != nil {
			t.Fatal(err)
		}
		if len(r.Report.Diags) > 0 || len(paths) != 2 {
			skipKnownBlockers(t, r)
			t.Fatalf("%s: %d paths\n%s", label, len(paths), r.Report.Text(r.Sources))
		}
		if links != 2 {
			t.Errorf("%s: %d links, want one per bin", label, links)
		}
		exes := map[string][]byte{}
		for _, p := range paths {
			b, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			exes[filepath.Base(p)] = b
		}
		return r, exes
	}
	type buildCase struct {
		label string
		id    source.Hash
		prune bool
	}
	cases := []buildCase{{"cold", source.Hash{}, false}}
	// The store's names are hashes of keys that hold the build ID, so
	// each ID orders them another way; not every order changes ld.lld's
	// output, so the emptied case runs under four.
	for k := range 4 {
		cases = append(cases, buildCase{fmt.Sprintf("cold-lld-cache-emptied-%d", k), source.HashString("TestMultiBinColdEqualsNoCache", fmt.Sprint(k)), true})
	}
	// The cases run in parallel: each has its own store and output
	// directories, and the builds only read the source tree.
	for _, c := range cases {
		t.Run(c.label, func(t *testing.T) {
			t.Parallel()
			_, fresh := build(t, c.label+"-nocache", "", c.id, false)
			r, cold := build(t, c.label, filepath.Join(t.TempDir(), "cache"), c.id, c.prune)
			// Bin b took bin a's stored packs: the case is not vacuous.
			if !slices.ContainsFunc(r.Trace.PacksFromCache, func(p string) bool { return p != "support" }) {
				t.Fatalf("%s: packs from the store %v, want bin b to find bin a's shared packs", c.label, r.Trace.PacksFromCache)
			}
			for _, bin := range []string{"a", "b"} {
				if cold[bin] == nil || fresh[bin] == nil {
					t.Fatalf("%s: bin %s missing: %d bytes, --no-cache %d bytes", c.label, bin, len(cold[bin]), len(fresh[bin]))
				}
				if !bytes.Equal(cold[bin], fresh[bin]) {
					t.Errorf("%s: bin %s differs from its --no-cache build", c.label, bin)
				}
			}
		})
	}
}

// TestStagedCopyNeverWritesThrough: the copy fallback of the staging
// opens its destination O_CREATE|O_EXCL, so a destination that is already
// there, a hard link to a stored object above all, is refused and never
// written through.
func TestStagedCopyNeverWritesThrough(t *testing.T) {
	dir := t.TempDir()
	victim, src, dst := filepath.Join(dir, "stored"), filepath.Join(dir, "src.o"), filepath.Join(dir, "p0000.o")
	if err := os.WriteFile(victim, []byte("A"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src, []byte("B"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(victim, dst); err != nil {
		t.Fatalf("cannot make the hard link the test needs: %v", err)
	}
	err := linkOrCopyFile(src, dst)
	if !errors.Is(err, fs.ErrExist) {
		t.Errorf("staging onto an existing file: error %v, want one that is fs.ErrExist", err)
	}
	if b, _ := os.ReadFile(victim); string(b) != "A" {
		t.Errorf("the stored file now reads %q: the copy wrote through the existing link", b)
	}
	// A fresh destination still gets the copy: there the link and the
	// copy give the same bytes.
	fresh := filepath.Join(dir, "p0001.o")
	if err := copyNewFile(src, fresh); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(fresh); string(b) != "B" {
		t.Errorf("a fresh copy reads %q, want %q", b, "B")
	}
}
