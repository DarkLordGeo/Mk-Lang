// Why: the native build path has edge cases that are easy to break: relative
// cache directories, work-directory names in errors, the release link's
// ThinLTO, stage names in --timings, corpus units with no runner, and a run
// that should skip the link on a cache hit.
// What: regression tests of cache and work directories, timings and
// release links, and TestHelloWorldSize.
// How: builds and runs small programs with the embedded std in temporary
// directories and inspects the toolchain jobs, timings and cache.

package driver

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"mk/internal/toolchain"
	"mk/internal/types"
)

// helloSrc is tests/programs/hello.mk's text.
func helloSrc(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "tests", "programs", "hello.mk"))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestRelativeCacheDir: a relative Config.CacheDir or MK_CACHE is
// made absolute, so mk run and mk build work and the executable of
// mk run lands under the absolute <cache>/bin, never an E0001 "compile
// paths must be absolute".
func TestRelativeCacheDir(t *testing.T) {
	tools := needTools(t)
	src := helloSrc(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "hello.mk"), src, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	want := "hello world, 1 args\n"
	for _, tc := range []struct {
		name, cacheDir, env string
	}{{"config", "relcache", ""}, {"MK_CACHE", "", "envcache"}} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("MK_CACHE", tc.env)
			rel := tc.cacheDir + tc.env
			cfg := Config{Root: "hello.mk", CacheDir: tc.cacheDir, Tools: tools}
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
			if len(r.Report.Diags) > 0 || status != 0 || out.String() != want {
				t.Fatalf("run: status %d, stdout %q\n%s", status, out.String(), r.Report.Text(r.Sources))
			}
			abs, _ := filepath.Abs(rel)
			ents, err := os.ReadDir(binOf(abs))
			if err != nil || len(ents) != 1 {
				t.Errorf("%s: %d entries, %v; want the one executable", binOf(abs), len(ents), err)
			} else if _, err := os.Stat(filepath.Join(binOf(abs), ents[0].Name(), "hello"+exeSuffix(s.cfg.Target))); err != nil {
				t.Errorf("executable not under %s/<key>/: %v", binOf(abs), err)
			}
			s, err = NewSession(cfg)
			if err != nil {
				t.Fatal(err)
			}
			r, paths, err := s.Build(filepath.Join(dir, tc.name+"-hello"+exeSuffix(s.cfg.Target)))
			if err != nil || len(r.Report.Diags) > 0 || len(paths) != 1 {
				t.Fatalf("build: err %v, paths %q\n%s", err, paths, r.Report.Text(r.Sources))
			}
		})
	}
}

// TestWorkDirScrubbed: an error naming the work directory carries
// <work> instead of its random name, and a non-ToolError keeps
// the tool the caller ran.
func TestWorkDirScrubbed(t *testing.T) {
	work := filepath.Join(t.TempDir(), "tmp", "mk-123456")
	err := errors.New(`toolchain: compile paths must be absolute: "` + strings.ReplaceAll(work, `\`, `\\`) + `\\p0000.ll"`)
	d := toolErrIn(err, "clang", "clang could not run in "+work, work)
	for _, k := range []string{"tool", "stderr"} {
		v := fact(d, k)
		if strings.Contains(v, "mk-123456") {
			t.Errorf("%s fact %q holds the work directory's name", k, v)
		}
	}
	if fact(d, "tool") != "clang" || strings.Contains(d.Message, "mk-123456") || !strings.Contains(fact(d, "stderr"), "<work>") {
		t.Errorf("tool %q, message %q, stderr %q", fact(d, "tool"), d.Message, fact(d, "stderr"))
	}
}

// TestReleaseLinkThinLTO: a release link asks lld for ThinLTO at
// O3 with Jobs backend jobs; a dev link does not.
// The release build of hello.mk links with it.
func TestReleaseLinkThinLTO(t *testing.T) {
	for _, target := range []types.Target{types.X86_64Windows, types.X86_64Linux} {
		for _, prof := range []types.Profile{types.Dev, types.Release} {
			w := &world{cfg: Config{Target: target, Profile: prof, Jobs: 3}}
			job := w.linkJob(target.LLVMTriple, filepath.Join(t.TempDir(), "x"))
			rel := prof == types.Release
			if job.LTO != rel || job.Release != rel || job.Jobs != 3 {
				t.Errorf("%s %v: LTO %v Release %v Jobs %d", target.Name, prof, job.LTO, job.Release, job.Jobs)
			}
		}
	}
	tools := needTools(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "hello.mk")
	if err := os.WriteFile(src, helloSrc(t), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, target := range []types.Target{types.X86_64Windows, types.X86_64Linux} {
		var jobs []toolchain.LinkJob
		s, err := NewSession(Config{Root: src, Target: target, Profile: types.Release, Jobs: 2, Tools: tools, CacheDir: t.TempDir(),
			TestHooks: TestHooks{OnLink: func(j toolchain.LinkJob) { jobs = append(jobs, j) }}})
		if err != nil {
			t.Fatal(err)
		}
		r, paths, err := s.Build(filepath.Join(dir, target.Name, "hello"+exeSuffix(target)))
		if err != nil {
			t.Fatal(err)
		}
		skipKnownBlockers(t, r)
		if len(r.Report.Diags) > 0 || len(paths) != 1 {
			t.Fatalf("%s: %d paths\n%s", target.Name, len(paths), r.Report.Text(r.Sources))
		}
		if len(jobs) != 1 || !jobs[0].LTO || jobs[0].Jobs != 2 {
			t.Errorf("%s: link jobs %+v, want one with LTO and Jobs 2", target.Name, jobs)
		}
	}
}

// TestTimingsStageNamesUnique: --timings has one line per stage;
// the import step has its own line, "imports", never a second "parse".
func TestTimingsStageNamesUnique(t *testing.T) {
	dir := writeTree(t, map[string]string{"t.mk": "G() {\n}\n"})
	r := check(t, Config{Root: filepath.Join(dir, "t.mk"), StdDir: fakeStd(t), Timings: io.Discard})
	clean(t, r)
	seen := map[string]bool{}
	for _, st := range r.Timings {
		if seen[st.Stage] {
			t.Errorf("stage %q timed twice: %v", st.Stage, r.Timings)
		}
		seen[st.Stage] = true
	}
	for _, want := range []string{"load", "parse", "imports", "resolve"} {
		if !seen[want] {
			t.Errorf("no %q line in %v", want, r.Timings)
		}
	}
}

// TestCorpusUnitRunNoRunner: a UnitRun unit with Main on a host
// with no runner for the target is built, not run, and RunSkipped says
// why; never Ran with status -1. mk run in the same case is a
// usage error before anything is built.
func TestCorpusUnitRunNoRunner(t *testing.T) {
	tools := needTools(t)
	c, err := NewCorpusSession(Config{Tools: tools, CacheDir: t.TempDir(), TestHooks: TestHooks{NoRunner: true}})
	if err != nil {
		t.Fatal(err)
	}
	out := unitOutcome(t, c, "norunner#1", "export Main(sys: &System) {\n}\n", UnitRun)
	if !out.Built || out.Ran || out.RunSkipped == "" {
		t.Errorf("Built %v Ran %v RunStatus %d RunSkipped %q; want built, not run, with a reason",
			out.Built, out.Ran, out.RunStatus, out.RunSkipped)
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "m.mk")
	if err := os.WriteFile(src, []byte("export Main(sys: &System) {\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := NewSession(Config{Root: src, Tools: tools, CacheDir: t.TempDir(), TestHooks: TestHooks{NoRunner: true}})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = s.Run(nil, RunIO{})
	var ue *UsageError
	if !errors.As(err, &ue) {
		t.Errorf("mk run with no runner: err %v, want a usage error", err)
	}
}

// TestHelloWorldSize: the release hello world's size on each target,
// logged with the target, the host, the LLVM and the date. It records the
// size and does not fail on it; a build error fails it.
func TestHelloWorldSize(t *testing.T) {
	tools := needTools(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "hello.mk")
	if err := os.WriteFile(src, helloSrc(t), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, target := range []types.Target{types.X86_64Windows, types.X86_64Linux} {
		t.Run(target.Name, func(t *testing.T) {
			exe := buildProgram(t, src, target, types.Release, t.TempDir())
			fi, err := os.Stat(exe)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("hello world, release, %s: %d bytes (host %s/%s, %s, %s)", target.Name, fi.Size(),
				runtime.GOOS, runtime.GOARCH, strings.TrimSpace(tools.Version.Line), time.Now().Format("2006-01-02"))
		})
	}
}

// TestRunSkipsLinkOnHit: a second mk run of unchanged inputs finds
// <cache>/bin/<link key>/ before clang and skips clang and the link: one
// link over two runs, and no clang or link timing the second time.
func TestRunSkipsLinkOnHit(t *testing.T) {
	tools := needTools(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "hello.mk")
	if err := os.WriteFile(src, helloSrc(t), 0o644); err != nil {
		t.Fatal(err)
	}
	cache := filepath.Join(dir, "cache")
	links := 0
	for i := 0; i < 2; i++ {
		s, err := NewSession(Config{Root: src, Tools: tools, CacheDir: cache, Timings: io.Discard,
			TestHooks: TestHooks{OnLink: func(toolchain.LinkJob) { links++ }}})
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
			t.Fatalf("run %d: status %d stdout %q\n%s", i, status, out.String(), r.Report.Text(r.Sources))
		}
		if i == 1 {
			for _, st := range r.Timings {
				if st.Stage == "clang" || st.Stage == "link" {
					t.Errorf("warm run timed %q: the hit did not skip it", st.Stage)
				}
			}
		}
	}
	if links != 1 {
		t.Errorf("%d links over two identical runs, want 1", links)
	}
}
