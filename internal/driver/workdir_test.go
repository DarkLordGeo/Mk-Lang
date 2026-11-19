// Why: a build's scratch files must land where GC can clean them, and
// --no-cache must leave nothing behind.
// What: tests of the work directory under <cache>/tmp, the probe cache,
// moving an executable across volumes (TestMoveFile), and mk run
// --no-cache leaving nothing.
// How: builds and runs small programs with and without a cache directory
// and lists the mk- entries left (mkEntries).

package driver

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"mk/internal/cache"
	"mk/internal/types"
)

// TestWorkParent: with a store the work directory's parent is the store's
// tmp, <cache>/v1/tmp, where GC removes a crashed process's work
// directory; without one (--no-cache) it is the OS temporary directory.
func TestWorkParent(t *testing.T) {
	if got, err := workParent(nil); err != nil || got != os.TempDir() {
		t.Errorf(`workParent(nil) = %q, %v; want %q`, got, err, os.TempDir())
	}
	dir := filepath.Join(t.TempDir(), "c")
	st, err := cache.Open(dir, cache.Options{})
	if err != nil {
		t.Fatal(err)
	}
	got, err := workParent(st)
	if err != nil || got != filepath.Join(dir, "v1", "tmp") {
		t.Fatalf("workParent(store) = %q, %v; want <cache>/v1/tmp", got, err)
	}
	if fi, err := os.Stat(got); err != nil || !fi.IsDir() {
		t.Errorf("<cache>/v1/tmp was not made: %v", err)
	}
}

// otherVolumeDir is a fresh directory on another volume than the OS
// temporary directory (under this package's directory), or "" when both
// are on one volume. It is removed at the end of the test.
func otherVolumeDir(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if strings.EqualFold(filepath.VolumeName(wd), filepath.VolumeName(os.TempDir())) {
		return ""
	}
	d, err := os.MkdirTemp(wd, ".zz-volume-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(d) })
	return d
}

// TestMoveFile: moveFile renames on one volume and copies then renames
// across volumes (the Windows "cannot move the file to a different disk
// drive" that broke mk build -o on another drive than %TEMP%), keeps the
// mode, replaces an existing file, and leaves no temporary on failure.
func TestMoveFile(t *testing.T) {
	src := t.TempDir()
	write := func(name, text string) string {
		p := filepath.Join(src, name)
		if err := os.WriteFile(p, []byte(text), 0o755); err != nil {
			t.Fatal(err)
		}
		return p
	}
	check := func(dest, want string) {
		t.Helper()
		b, err := os.ReadFile(dest)
		if err != nil || string(b) != want {
			t.Errorf("%s holds %q, %v; want %q", dest, b, err, want)
		}
		if runtime.GOOS != "windows" {
			if st, _ := os.Stat(dest); st == nil || st.Mode().Perm()&0o100 == 0 {
				t.Errorf("%s lost its executable mode", dest)
			}
		}
	}
	same := filepath.Join(t.TempDir(), "a.exe")
	if err := moveFile(write("a", "one"), same); err != nil {
		t.Fatal(err)
	}
	check(same, "one")
	if err := moveFile(write("b", "two"), same); err != nil {
		t.Fatal(err)
	}
	check(same, "two")

	if other := otherVolumeDir(t); other != "" {
		dest := filepath.Join(other, "x.exe")
		if err := moveFile(write("c", "three"), dest); err != nil {
			t.Fatalf("across volumes: %v", err)
		}
		check(dest, "three")
		if err := moveFile(write("d", "four"), dest); err != nil {
			t.Fatalf("across volumes, replacing: %v", err)
		}
		check(dest, "four")
		ents, _ := os.ReadDir(other)
		if len(ents) != 1 {
			t.Errorf("%s holds %d entries, want only x.exe", other, len(ents))
		}
	} else {
		t.Logf("the OS temporary directory and %s share a volume: the cross-volume case is not exercised", "this package")
	}

	// A destination that cannot be replaced (a non-empty directory) is an
	// error, and the copy's temporary is removed.
	blockDir := t.TempDir()
	dest := filepath.Join(blockDir, "busy")
	if err := os.MkdirAll(filepath.Join(dest, "in"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := moveFile(write("e", "five"), dest); err == nil {
		t.Errorf("moveFile onto a non-empty directory succeeded")
	}
	ents, _ := os.ReadDir(blockDir)
	if len(ents) != 1 {
		t.Errorf("%d entries after a failed move, want only the directory (a temporary was left)", len(ents))
	}
}

// TestProbeCachedPerWorld: one clang probe per (toolchain, triple) in a
// world (toolchain.Probe's contract; mk corpus --build otherwise ran one
// clang per unit to read the data layout).
func TestProbeCachedPerWorld(t *testing.T) {
	tools := needTools(t)
	w := newWorld(Config{Jobs: 2})
	ctx := context.Background()
	win := types.X86_64Windows.LLVMTriple
	a, err := w.probe(ctx, tools, nil, win)
	if err != nil {
		t.Fatal(err)
	}
	b, err := w.probe(ctx, tools, nil, win)
	if err != nil || a != b || w.probeRuns != 1 {
		t.Errorf("second probe: %+v, %v, runs %d; want the first facts and one run", b, err, w.probeRuns)
	}
	if _, err := w.probe(ctx, tools, nil, types.X86_64Linux.LLVMTriple); err != nil || w.probeRuns != 2 {
		t.Errorf("another triple: %v, runs %d; want a second run", err, w.probeRuns)
	}
}

// osTempDir points the OS temporary directory at a fresh directory for
// this test and returns it.
func osTempDir(t *testing.T) string {
	t.Helper()
	d := t.TempDir()
	for _, k := range []string{"TMP", "TEMP", "TMPDIR"} {
		t.Setenv(k, d)
	}
	return d
}

// mkEntries lists the entries of dir whose name starts with mk-: the work
// and run directories.
func mkEntries(dir string) []string {
	var out []string
	ents, _ := os.ReadDir(dir)
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), "mk-") {
			out = append(out, e.Name())
		}
	}
	return out
}

// sevenProgram writes the Main that exits 7 into dir.
func sevenProgram(t *testing.T, dir string) string {
	t.Helper()
	src := filepath.Join(dir, "seven.mk")
	if err := os.WriteFile(src, []byte("export Main(sys: &System): int {\n    return 7\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return src
}

// TestNativeWorkDirUnderCache: mk build's work directory is made in
// <cache>/tmp and removed (the directory itself stays); with --no-cache it
// is made in the OS temporary directory and removed, and no cache
// directory is created.
func TestNativeWorkDirUnderCache(t *testing.T) {
	tools := needTools(t)
	osTmp := osTempDir(t)
	dir := t.TempDir()
	src := sevenProgram(t, dir)
	for _, noCache := range []bool{false, true} {
		cache := filepath.Join(dir, "cache")
		if noCache {
			cache = filepath.Join(dir, "unused")
		}
		s, err := NewSession(Config{Root: src, Tools: tools, CacheDir: cache, NoCache: noCache})
		if err != nil {
			t.Fatal(err)
		}
		r, paths, err := s.Build(filepath.Join(dir, "out", "seven"+exeSuffix(s.cfg.Target)))
		if err != nil {
			t.Fatal(err)
		}
		skipKnownBlockers(t, r)
		if len(r.Report.Diags) > 0 || len(paths) != 1 {
			t.Fatalf("no-cache %v: %d paths\n%s", noCache, len(paths), r.Report.Text(r.Sources))
		}
		if left := mkEntries(osTmp); len(left) > 0 {
			t.Errorf("no-cache %v: the OS temporary directory holds %v after the build", noCache, left)
		}
		ents, err := os.ReadDir(filepath.Join(cache, "v1", "tmp"))
		switch {
		case noCache && !os.IsNotExist(err):
			t.Errorf("--no-cache made %s/v1/tmp (%v)", cache, err)
		case !noCache && (err != nil || len(ents) != 0):
			t.Errorf("<cache>/v1/tmp after the build: %d entries, %v; want it present and empty", len(ents), err)
		}
	}
}

// TestRunNoCacheLeavesNothing: mk run --no-cache runs the program from a
// temporary directory that is removed after it exits, and writes no cache.
func TestRunNoCacheLeavesNothing(t *testing.T) {
	tools := needTools(t)
	osTmp := osTempDir(t)
	dir := t.TempDir()
	src := sevenProgram(t, dir)
	s, err := NewSession(Config{Root: src, Tools: tools, NoCache: true})
	if err != nil {
		t.Fatal(err)
	}
	r, status, err := s.Run(nil, RunIO{Stdout: io.Discard, Stderr: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	skipKnownBlockers(t, r)
	if len(r.Report.Diags) > 0 || status != 7 {
		t.Fatalf("status %d\n%s", status, r.Report.Text(r.Sources))
	}
	if left := mkEntries(osTmp); len(left) > 0 {
		t.Errorf("the OS temporary directory holds %v after mk run --no-cache", left)
	}
	if _, err := os.Stat(filepath.Join(dir, ".mk-cache")); !os.IsNotExist(err) {
		t.Errorf("mk run --no-cache made a cache directory (%v)", err)
	}
}

// TestBuildOutputOtherVolume: mk build -o to another volume than the work
// directory's links there (Windows refused the rename across drives, E0001
// "cannot move the file to a different disk drive"), and the executable
// runs.
func TestBuildOutputOtherVolume(t *testing.T) {
	tools := needTools(t)
	other := otherVolumeDir(t)
	if other == "" {
		t.Skip("the OS temporary directory and this package share a volume")
	}
	dir := t.TempDir()
	src := sevenProgram(t, dir)
	s, err := NewSession(Config{Root: src, Tools: tools, CacheDir: filepath.Join(dir, "cache")})
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(other, "seven"+exeSuffix(s.cfg.Target))
	r, paths, err := s.Build(out)
	if err != nil {
		t.Fatal(err)
	}
	skipKnownBlockers(t, r)
	if len(r.Report.Diags) > 0 || len(paths) != 1 {
		t.Fatalf("%d paths\n%s", len(paths), r.Report.Text(r.Sources))
	}
	if _, _, code := runExe(t, s.cfg.Target, paths[0], ""); code != 7 {
		t.Errorf("exit %d, want 7", code)
	}
}
