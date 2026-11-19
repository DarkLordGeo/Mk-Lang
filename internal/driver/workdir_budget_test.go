// Why: the cache budget, the --cpu flag, the link key and the timeout text
// are user-visible contracts of the driver, each easy to break.
// What: tests of executables and work directories kept
// inside the store and aged by GC, --cpu=native and unknown CPUs, the
// build ID in the link key, the cached data-layout probe, and the
// "timeout after 60s" text.
// How: runs commands against a cache with an advancing store clock and
// inspects the store, the IR and the jobs.

package driver

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mk/internal/cache"
	"mk/internal/diag"
	"mk/internal/source"
	"mk/internal/toolchain"
	"mk/internal/types"
)

// binOf is the store's bin under the cache directory dir, where mk run,
// mk test and a corpus unit's run link their executables.
func binOf(dir string) string { return cache.DirPath(dir, cache.BinDir) }

// helloRun is one Session.Run of the hello program at src with cfg; it
// fails on any diagnostic or a wrong output.
func helloRun(t *testing.T, cfg Config, src string) Result {
	t.Helper()
	cfg.Root = src
	s, err := NewSession(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	r, status, err := s.Run(nil, RunIO{Stdout: &out, Stderr: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	skipKnownBlockers(t, r)
	if len(r.Report.Diags) > 0 || status != 0 || out.String() != "hello world, 1 args\n" {
		t.Fatalf("run: status %d stdout %q\n%s", status, out.String(), r.Report.Text(r.Sources))
	}
	return r
}

// helloFile writes tests/programs/hello.mk into a fresh directory.
func helloFile(t *testing.T) string {
	t.Helper()
	src := filepath.Join(t.TempDir(), "hello.mk")
	if err := os.WriteFile(src, helloSrc(t), 0o644); err != nil {
		t.Fatal(err)
	}
	return src
}

// TestCacheStaysUnderBudgetRun: mk run links each edit's executable
// into the store's bin, which GC ages and counts like every entry, so a run
// of commands with one-function edits keeps the whole cache directory under
// the budget. Executables outside the store would stay, one per edit. The
// store's clock advances three hours per command, past cache.Grace, as in
// TestCacheStaysUnderBudget.
func TestCacheStaysUnderBudgetRun(t *testing.T) {
	tools := needTools(t)
	runs := 20
	if testing.Short() {
		runs = 6
	}
	dir := writeTree(t, incPkg("x + 0"))
	dir2 := t.TempDir()
	cdir := filepath.Join(dir2, "cache")
	cfg := Config{Root: dir, CacheDir: cdir, Tools: tools}
	run := func(i int) {
		t.Helper()
		s, err := NewSession(cfg)
		if err != nil {
			t.Fatal(err)
		}
		r, status, err := s.Run(nil, RunIO{Stdout: io.Discard, Stderr: io.Discard})
		if err != nil {
			t.Fatal(err)
		}
		skipKnownBlockers(t, r)
		if len(r.Report.Diags) > 0 || status != 5+i {
			t.Fatalf("run %d: status %d, want %d\n%s", i, status, 5+i, r.Report.Text(r.Sources))
		}
	}
	run(0)
	if ents, err := os.ReadDir(binOf(cdir)); err != nil || len(ents) != 1 {
		t.Fatalf("the store's bin after one run: %d entries, %v; want the executable", len(ents), err)
	}
	one := dirBytes(t, cdir)
	budget := one + one/20
	cfg.CacheBudget = budget
	clock := time.Now()
	cfg.TestHooks.CacheNow = func() time.Time { return clock }
	lib := filepath.Join(dir, "lib", "lib.mk")
	src := incPkg("x + 0")["lib/lib.mk"]
	peak := int64(0)
	for i := 1; i <= runs; i++ {
		clock = clock.Add(3 * time.Hour)
		if err := os.WriteFile(lib, []byte(strings.Replace(src, "x + 0", fmt.Sprintf("x + %d", i), 1)), 0o644); err != nil {
			t.Fatal(err)
		}
		run(i)
		got := dirBytes(t, cdir)
		peak = max(peak, got)
		if got > budget {
			ents, _ := os.ReadDir(binOf(cdir))
			t.Fatalf("after run %d the cache directory holds %d bytes (%d executables), over the budget of %d", i, got, len(ents), budget)
		}
	}
	if _, err := os.Stat(filepath.Join(cdir, "bin")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("<cache>/bin outside the store exists: %v", err)
	}
	t.Logf("one cold run: %d bytes; budget %d; peak after %d runs: %d", one, budget, runs, peak)
}

// TestStaleWorkDirRemoved: a work directory a crashed
// or killed mk left in the store's tmp goes once its newest file is older
// than cache.Grace, by the next collection or by mk cache clean; a live
// one stays.
func TestStaleWorkDirRemoved(t *testing.T) {
	for _, how := range []string{"gc", "clean"} {
		t.Run(how, func(t *testing.T) {
			dir := t.TempDir()
			st, err := cache.Open(dir, cache.Options{})
			if err != nil {
				t.Fatal(err)
			}
			old := time.Now().Add(-3 * time.Hour)
			stale := filepath.Join(st.TmpDir(), "mk-stale")
			live := filepath.Join(st.TmpDir(), "mk-live")
			for _, d := range []string{stale, live} {
				if err := os.MkdirAll(filepath.Join(d, "sub"), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(d, "sub", "p0000.ll"), []byte("x"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			for _, p := range []string{filepath.Join(stale, "sub", "p0000.ll"), filepath.Join(stale, "sub"), stale} {
				if err := os.Chtimes(p, old, old); err != nil {
					t.Fatal(err)
				}
			}
			// The live directory's newest file is fresh, though the
			// directory itself is old.
			if err := os.Chtimes(live, old, old); err != nil {
				t.Fatal(err)
			}
			if how == "gc" {
				w := newWorld(Config{CacheDir: dir})
				if w.openStore(dir) == nil {
					t.Fatal("no store")
				}
				w.maybeGC() // no gc.stamp yet: it collects
			} else if err := CacheClean(dir); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(stale); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("the stale work directory is still there: %v", err)
			}
			if _, err := os.Stat(filepath.Join(live, "sub", "p0000.ll")); err != nil {
				t.Errorf("the live work directory was removed: %v", err)
			}
		})
	}
}

// TestCPUNativeResolved: --cpu=native builds, and every pack's IR
// names a concrete CPU, never "native" (toolchain refuses "native" in a
// compile job, so a clean build is also the proof that no job carried it). A
// target of another architecture than the host's refuses "native" before any
// tool runs, a usage error.
func TestCPUNativeResolved(t *testing.T) {
	tools := needTools(t)
	want, err := tools.ResolveCPU(context.Background(), HostTarget().LLVMTriple, toolchain.CPUNative)
	if err != nil || want == "" || want == toolchain.CPUNative {
		t.Fatalf("ResolveCPU(native) = %q, %v", want, err)
	}
	var lls []string
	cfg := Config{Root: helloFile(t), NoCache: true, Tools: tools, CPU: toolchain.CPUNative,
		TestHooks: TestHooks{OnLL: func(_, ll string) { lls = append(lls, ll) }}}
	s, err := NewSession(cfg)
	if err != nil {
		t.Fatal(err)
	}
	r, paths, err := s.Build(filepath.Join(t.TempDir(), "hello"+exeSuffix(s.cfg.Target)))
	if err != nil {
		t.Fatal(err)
	}
	skipKnownBlockers(t, r)
	if st := ExitStatus(r.Report); st != 0 || len(paths) != 1 {
		t.Fatalf("mk build --cpu=native: exit %d, %d paths\n%s", st, len(paths), r.Report.Text(r.Sources))
	}
	if len(lls) == 0 {
		t.Fatal("no IR seen")
	}
	for _, ll := range lls {
		if strings.Contains(ll, `"target-cpu"="native"`) {
			t.Fatal(`a pack's IR has "target-cpu"="native"`)
		}
		if strings.Contains(ll, `"target-cpu"=`) && !strings.Contains(ll, `"target-cpu"="`+want+`"`) {
			t.Errorf("a pack's IR names another CPU than %q", want)
		}
	}
	_, err = NewSession(Config{Root: cfg.Root, CPU: toolchain.CPUNative, Target: types.Aarch64Linux})
	var ue *UsageError
	if !errors.As(err, &ue) || !strings.Contains(ue.Msg, "--cpu=native") {
		t.Errorf("--cpu=native for aarch64-linux on an x86_64 host: %v, want a usage error", err)
	}
}

// TestCPUUnknownIsUsageError: a CPU clang does not know is one E0001
// naming the flag, exit 2, never an E0000 per pack; and an explicit CPU
// clang accepted is remembered in the store, so a warm build with it starts
// no check process.
func TestCPUUnknownIsUsageError(t *testing.T) {
	tools := needTools(t)
	src := helloFile(t)
	build := func(cpu, cdir string) (Result, *Session) {
		t.Helper()
		s, err := NewSession(Config{Root: src, CacheDir: cdir, NoCache: cdir == "", Tools: tools, CPU: cpu})
		if err != nil {
			t.Fatal(err)
		}
		r, _, err := s.Build(filepath.Join(t.TempDir(), "hello"+exeSuffix(s.cfg.Target)))
		if err != nil {
			t.Fatal(err)
		}
		skipKnownBlockers(t, r)
		return r, s
	}
	r, _ := build("bogus", "")
	if st := ExitStatus(r.Report); st != 2 || len(r.Report.Diags) != 1 {
		t.Fatalf("--cpu=bogus: exit %d, %d diagnostics, want 2 and one E0001\n%s", st, len(r.Report.Diags), r.Report.Text(r.Sources))
	}
	d := r.Report.Diags[0].Diag
	if d.Code != diag.E0001 || !strings.Contains(d.Message, `"bogus"`) || !hasFact(d, "flag", "--cpu") {
		t.Errorf("--cpu=bogus: %s %q, want E0001 naming the CPU with flag --cpu\n%s", d.Code, d.Message, r.Report.Text(r.Sources))
	}
	cdir := filepath.Join(t.TempDir(), "cache")
	for i, wantRuns := range []int{1, 0} {
		r, s := build("x86-64-v3", cdir)
		if len(r.Report.Diags) > 0 {
			t.Fatalf("--cpu=x86-64-v3, build %d:\n%s", i, r.Report.Text(r.Sources))
		}
		if s.w.cpuRuns != wantRuns {
			t.Errorf("--cpu=x86-64-v3, build %d: %d CPU checks, want %d", i, s.w.cpuRuns, wantRuns)
		}
	}
}

// hasFact reports whether d has the text fact key = val.
func hasFact(d *diag.Diagnostic, key, val string) bool {
	f, ok := d.FactValue(key)
	return ok && f.Kind == diag.FactText && f.Str == val
}

// TestLinkKeyHasBuildID: the executable of mk run is named by a link
// key that holds the build ID, so after the mk binary changes the program is
// linked again into a new path instead of running the old executable; the
// same build ID again is a hit.
func TestLinkKeyHasBuildID(t *testing.T) {
	tools := needTools(t)
	src := helloFile(t)
	cdir := filepath.Join(t.TempDir(), "cache")
	links := 0
	a, b := source.HashString("test.build", "A"), source.HashString("test.build", "B")
	for i, id := range []source.Hash{a, b, a} {
		helloRun(t, Config{CacheDir: cdir, Tools: tools, BuildID: id, Timings: io.Discard,
			TestHooks: TestHooks{OnLink: func(toolchain.LinkJob) { links++ }}}, src)
		if want := min(i+1, 2); links != want {
			t.Errorf("after run %d: %d links, want %d", i, links, want)
		}
	}
	if ents, err := os.ReadDir(binOf(cdir)); err != nil || len(ents) != 2 {
		t.Errorf("the store's bin holds %d entries, %v; want one per build ID", len(ents), err)
	}
}

// TestProbeCachedInStore: the data layout and triple are read from
// the store in a later process, so a warm native command starts no clang
// probe; without the store each world probes once.
func TestProbeCachedInStore(t *testing.T) {
	tools := needTools(t)
	src := helloFile(t)
	cdir := filepath.Join(t.TempDir(), "cache")
	for i, want := range []int{1, 0} {
		s, err := NewSession(Config{Root: src, CacheDir: cdir, Tools: tools})
		if err != nil {
			t.Fatal(err)
		}
		r, _, err := s.Build(filepath.Join(t.TempDir(), "hello"+exeSuffix(s.cfg.Target)))
		if err != nil {
			t.Fatal(err)
		}
		skipKnownBlockers(t, r)
		if len(r.Report.Diags) > 0 {
			t.Fatalf("build %d:\n%s", i, r.Report.Text(r.Sources))
		}
		if s.w.probeRuns != want {
			t.Errorf("build %d: %d probes, want %d", i, s.w.probeRuns, want)
		}
	}
	st, err := cache.Open(cdir, cache.Options{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	triple := HostTarget().LLVMTriple
	b, ok, err := st.Get(cache.KindLayout, layoutKey(tools, triple))
	if f, fok := decodeFacts(b); err != nil || !ok || !fok || f.DataLayout == "" {
		t.Errorf("no layout entry for (toolchain, %s): ok %v, %v, %q", triple, ok, err, b)
	}
}

// TestTimeoutAfter60s: the default --timeout prints as
// "timeout after 60s", not time.Duration's "1m0s".
func TestTimeoutAfter60s(t *testing.T) {
	for _, c := range []struct {
		d    time.Duration
		want string
	}{
		{60 * time.Second, "timeout after 60s"},
		{90 * time.Second, "timeout after 90s"},
		{2 * time.Second, "timeout after 2s"},
		{1500 * time.Millisecond, "timeout after 1.5s"},
	} {
		rep := &TestReport{
			Tests:   []TestCase{{Module: "main", Name: "spins", Result: "timeout", Status: -1}},
			Failed:  1,
			timeout: c.d,
		}
		var b bytes.Buffer
		if err := rep.WriteText(&b, false); err != nil {
			t.Fatal(err)
		}
		first := strings.SplitN(b.String(), "\n", 2)[0]
		if want := `FAIL main "spins": ` + c.want; first != want {
			t.Errorf("timeout %v: got %q, want %q", c.d, first, want)
		}
	}
}
