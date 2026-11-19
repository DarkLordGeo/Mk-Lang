// Why: these driver behaviours are easy to break: poison that must spread to
// importers, the semantics gate by phase, case in manifest directories,
// worker slots in stage 6, and directory mode.
// What: TestTransitivePoisonNoICE, TestUnusedDoesNotHideTypeErrors,
// TestPhaseRuleSameBytesTypesPhaseResolve, TestManifestDirsMatchListingCase,
// TestStage6GoroutinesBoundedByJobs and TestDirectoryModeEmptyDirIsUsageError.
// How: checks small trees in temporary directories and compares reports;
// the stage-6 test samples the goroutine count while the stage runs.

package driver

import (
	"errors"
	"fmt"
	"io/fs"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"mk/internal/types"
)

// TestTransitivePoisonNoICE: a module whose bodies were not lowered
// because a dependency failed is tainted, never clean, so its importers are
// not lowered either: cc imports only b, b's signatures read a.N, and a is
// rejected. The report is a's error alone, never an E0000 from lowering cc
// against b's poison.
func TestTransitivePoisonNoICE(t *testing.T) {
	r := check(t, Config{Root: writeTree(t, pkgFiles(map[string]string{
		"a/a.mk": "export Bad(): int {\n    return \"x\"\n}\n\nexport const N: int = Bad()\n",
		"b/b.mk": "import a\n\nexport struct T {\n    arr: [a.N]u8\n}\n\n" +
			"export Arr(t: &T): &[a.N]u8 {\n    return &t.arr\n}\n",
		"cc/c.mk": "import b\n\nexport K(t: &b.T): u8 {\n    var x := b.Arr(t)\n    return x[0]\n}\n",
	}))})
	got := lines(r)
	if len(got) == 0 || slices.ContainsFunc(got, func(l string) bool { return !strings.HasPrefix(l, "a/a.mk:2:") }) {
		t.Errorf("diagnostics %q, want only a's type error at a/a.mk:2\n%s", got, r.Report.Text(r.Sources))
	}
	if st := ExitStatus(r.Report); st != 1 {
		t.Errorf("exit %d, want 1", st)
	}
}

// u1 has a types error (E0301) and an unused local (resolve's E0208, also
// of the types phase).
const unusedAndTypeError = "Widen(n: i32): i64 {\n    return n\n}\n\nUnused(): int {\n    var k := 1\n    return 2\n}\n"

// TestUnusedDoesNotHideTypeErrors: a stage-3 diagnostic of the
// types phase (E0208) does not skip stages 5 and 6; every types error of
// the build is found in one run.
func TestUnusedDoesNotHideTypeErrors(t *testing.T) {
	dir := writeTree(t, map[string]string{"u1.mk": unusedAndTypeError})
	r := check(t, Config{Root: filepath.Join(dir, "u1.mk")})
	if got := codes(r); !slices.Equal(got, []string{"E0301", "E0208"}) {
		t.Errorf("codes %q, want [E0301 E0208]\n%s", got, r.Report.Text(r.Sources))
	}
	if st := ExitStatus(r.Report); st != 1 {
		t.Errorf("exit %d, want 1", st)
	}
}

// TestPhaseRuleSameBytesTypesPhaseResolve: with an E0208 as the only stage-3
// diagnostic, the report is the same bytes whether TestHooks.CheckPastNames
// is set or not, and whatever the worker count, and stages 5 and 6 ran.
func TestPhaseRuleSameBytesTypesPhaseResolve(t *testing.T) {
	dir := writeTree(t, pkgFiles(map[string]string{
		"n/n.mk": "export N(): int {\n    var k := 1\n    return 2\n}\n",
		"a/a.mk": "export A(): int {\n    return \"a\"\n}\n",
	}))
	var want string
	for _, past := range []bool{false, true} {
		for _, jobs := range []int{1, 2, 8} {
			r := check(t, Config{Root: dir, Jobs: jobs, TestHooks: TestHooks{CheckPastNames: past}})
			got := string(r.Report.WriteJSON(r.Sources))
			if r.Reached != StageFlow {
				t.Errorf("past %v jobs %d: reached %v, want flow", past, jobs, r.Reached)
			}
			if want == "" {
				want = got
				if c := codes(r); !slices.Equal(c, []string{"E0301", "E0208"}) {
					t.Fatalf("codes %q, want [E0301 E0208]\n%s", c, r.Report.Text(r.Sources))
				}
			} else if got != want {
				t.Errorf("past %v jobs %d: report differs:\n%s\n%s", past, jobs, got, want)
			}
		}
	}
}

// foldFS resolves every path segment case-insensitively against the
// listing, as a case-insensitive file system (Windows, macOS) does, so the
// rule is tested on every OS.
type foldFS struct{ fs.FS }

func (f foldFS) Open(name string) (fs.File, error) {
	if name == "." || !fs.ValidPath(name) {
		return f.FS.Open(name)
	}
	cur := "."
	for seg := range strings.SplitSeq(name, "/") {
		ents, err := fs.ReadDir(f.FS, cur)
		if err != nil {
			return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
		}
		i := slices.IndexFunc(ents, func(e fs.DirEntry) bool { return strings.EqualFold(e.Name(), seg) })
		if i < 0 {
			return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
		}
		cur = path.Join(cur, ents[i].Name())
	}
	return f.FS.Open(cur)
}

// TestManifestDirsMatchListingCase: a manifest directory that differs from
// the listed directory only in case is E0202 reason invalid at its field, on
// every OS, never accepted on a case-insensitive file system and then
// silently matched against nothing.
func TestManifestDirsMatchListingCase(t *testing.T) {
	main := "export F(): int {\n    return 1\n}\n"
	cases := []struct {
		name, field string
		over        map[string]string
		files       map[string]string
	}{
		{"bin root", "bins[0].root",
			map[string]string{"bins": `[pkg.Bin(name: "app", root: "App", link_script: "", cpu: "", subsystem: "console")]`},
			map[string]string{"app/a.mk": main}},
		{"unsafe module", "unsafe_modules[0]",
			map[string]string{"unsafe_modules": `["Lib"]`},
			map[string]string{"lib/l.mk": "export G(): int {\n    unsafe \"x\" {\n    }\n    return 1\n}\n"}},
		{"selected dir", "targets[0].modules[0].dir",
			map[string]string{"targets": `[pkg.Select(os: "windows", modules: [pkg.Module(name: "osn", dir: "OS/Windows")]), ` +
				`pkg.Select(os: "linux", modules: [pkg.Module(name: "osn", dir: "os/linux")])]`},
			map[string]string{"os/windows/w.mk": main, "os/linux/l.mk": main}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			files := map[string]string{"pkg.mk": manifest(c.over)}
			for k, v := range c.files {
				files[k] = v
			}
			dir := writeTree(t, files)
			for _, wrap := range []bool{false, true} {
				cfg := Config{Root: dir, Target: types.X86_64Windows}
				if wrap {
					cfg.TestHooks.WrapFS = func(f fs.FS) fs.FS { return foldFS{f} }
				}
				r := check(t, cfg)
				d := only(t, r, "E0202")
				if fact(d, "manifest") != c.field || fact(d, "reason") != "invalid" {
					t.Errorf("fold %v: manifest %q reason %q, want %s invalid\n%s", wrap,
						fact(d, "manifest"), fact(d, "reason"), c.field, r.Report.Text(r.Sources))
				}
			}
		})
	}
	// The spelling of the listing is accepted, folded or not.
	dir := writeTree(t, map[string]string{
		"pkg.mk":   manifest(map[string]string{"bins": `[pkg.Bin(name: "app", root: "app", link_script: "", cpu: "", subsystem: "console")]`}),
		"app/a.mk": main,
	})
	r := check(t, Config{Root: dir, TestHooks: TestHooks{WrapFS: func(f fs.FS) fs.FS { return foldFS{f} }}})
	if got := lines(r); !slices.Equal(got, []string{"app/a.mk:1:1:E0201"}) {
		t.Errorf("bin root as listed: %q, want the missing-Main E0201\n%s", got, r.Report.Text(r.Sources))
	}
}

// TestStage6GoroutinesBoundedByJobs: stage 6 takes a worker slot before
// it starts a unit's goroutine, so a module of many functions never parks
// one goroutine per function. Jobs 1 makes the wrong behaviour (every
// unit's goroutine parked on the semaphore) last for the whole
// stage, so a sampler sees it.
func TestStage6GoroutinesBoundedByJobs(t *testing.T) {
	const n = 2000
	var b strings.Builder
	for i := range n {
		fmt.Fprintf(&b, "export F%d(): int {\n    return %d\n}\n\n", i, i)
	}
	dir := writeTree(t, pkgFiles(map[string]string{"a/a.mk": b.String()}))
	base := runtime.NumGoroutine()
	var peak atomic.Int64
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			if g := int64(runtime.NumGoroutine()); g > peak.Load() {
				peak.Store(g)
			}
			time.Sleep(100 * time.Microsecond)
		}
	}()
	r := check(t, Config{Root: dir, Jobs: 1})
	close(stop)
	wg.Wait()
	clean(t, r)
	t.Logf("peak goroutines %d, baseline %d, Jobs 1, %d functions", peak.Load(), base, n)
	// The bound: Jobs unit goroutines, the module goroutines of the
	// schedule, the sampler, and slack for the runtime's own.
	if extra := peak.Load() - int64(base); extra > 100 {
		t.Errorf("peak goroutines %d over a baseline of %d with Jobs 1 and %d functions", peak.Load(), base, n)
	}
}

// TestDirectoryModeEmptyDirIsUsageError: directory mode on a directory
// holding no .mk file (a subdirectory's files are not the module's) is a
// usage error, exit 2, never a module of zero files with an unlocated
// missing-Main E0201.
func TestDirectoryModeEmptyDirIsUsageError(t *testing.T) {
	for name, files := range map[string]map[string]string{
		"empty":     {},
		"other":     {"notes.txt": "x\n"},
		"subdir mk": {"sub/s.mk": "export F(): int {\n    return 1\n}\n"},
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if len(files) > 0 {
				dir = writeTree(t, files)
			}
			_, err := NewSession(Config{Root: dir})
			var ue *UsageError
			if !errors.As(err, &ue) {
				t.Fatalf("NewSession: %v, want a *UsageError", err)
			}
		})
	}
	// A directory with a .mk file of its own is still directory mode.
	dir := writeTree(t, map[string]string{"m.mk": "export F(): int {\n    return 1\n}\n"})
	r := check(t, Config{Root: dir})
	if got := lines(r); !slices.Equal(got, []string{"m.mk:1:1:E0201"}) {
		t.Errorf("directory mode: %q, want the missing-Main E0201 at m.mk:1:1\n%s", got, r.Report.Text(r.Sources))
	}
}
