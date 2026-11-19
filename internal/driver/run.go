// Why: mk run builds and starts one bin in a single step, and its exit
// status must be the program's, so scripts can use mk run like the binary.
//
// What: RunIO and Session.Run, runPlacement (where the executable goes),
// sweepStaleRunDirs, and Session.cacheDir.
//
// How: the bin is built into a content-named path under the cache's bin
// directory (one already there has the same bytes and is used as is), then
// run with the terminal inherited; its exit status becomes mk's. Temporary
// run directories a killed run left behind are swept once per process.

package driver

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"mk/internal/diag"
	"mk/internal/toolchain"
)

// RunIO is where mk run's child reads and writes; nil fields inherit mk's
// own streams. Tests set them.
type RunIO struct {
	Stdin          io.Reader
	Stdout, Stderr io.Writer
}

// Run builds one bin, Config.Bin or the only bin, into the store's bin/<link
// key>/<bin>[.exe] (an executable already there has the same bytes and is
// used as is, so a running one is never overwritten), then runs it with args
// and returns its exit status. A package with several bins and no
// Config.Bin, a target this host cannot run, and an unknown bin are
// *UsageError (exit 2). A compile failure returns status -1 and runs
// nothing.
func (s *Session) Run(args []string, rio RunIO) (Result, int, error) {
	s.forBuild = true
	s.startTools()
	defer s.joinTools()
	r := s.runCheck()
	defer s.w.maybeGC() // once, after the child has exited
	if len(r.Report.Diags) > 0 {
		return s.verifiedReport(r), -1, nil
	}
	bins, err := s.bins()
	if err != nil {
		return r, -1, err
	}
	if len(bins) != 1 {
		var names []string
		for _, b := range bins {
			names = append(names, b.name)
		}
		if len(bins) == 0 {
			return r, -1, usagef("the package has no bin to run")
		}
		return r, -1, usagef("the package has several bins; choose one with --bin: %s", strings.Join(names, ", "))
	}
	if d := notBuilt(s.cfg.Target); d != nil {
		// A checked-only target is refused before the runner lookup.
		s.p.diags = append(s.p.diags, d)
		return s.p.result(s.w, s.cfg.MaxDiags), -1, nil
	}
	prefix, ok := s.cfg.runner()
	if !ok {
		return r, -1, usagef("cannot run %s executables on this machine", s.cfg.Target.Name)
	}
	bin := bins[0]
	if _, lib := libKind(bin.kind); lib {
		// A library has no entry to run; refused before the
		// build, not as the link's missing CallMain.
		return r, -1, usagef("bin %s is a %s library: mk run runs an executable; build the library with mk build", bin.name, bin.kind)
	}
	place, keyed := s.runPlacement(bin.name)
	exe, ds := s.native(bin, place, keyed)
	if len(ds) > 0 {
		s.p.diags = append(s.p.diags, ds...)
		return s.p.result(s.w, s.cfg.MaxDiags), -1, nil
	}
	if s.cfg.NoCache && exe != "" {
		// --no-cache: the executable's temporary directory goes with
		// the run.
		defer os.RemoveAll(filepath.Dir(exe))
	}
	r = s.p.result(s.w, s.cfg.MaxDiags)
	r.Reached = StageNative
	argv := append(append([]string(nil), prefix...), toolchain.HostPathFor(prefix, exe))
	argv = append(argv, args...)
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if rio.Stdin != nil {
		cmd.Stdin = rio.Stdin
	}
	if rio.Stdout != nil {
		cmd.Stdout = rio.Stdout
	}
	if rio.Stderr != nil {
		cmd.Stderr = rio.Stderr
	}
	err = cmd.Run()
	var ee *exec.ExitError
	switch {
	case err == nil:
		return r, 0, nil
	case errors.As(err, &ee):
		return r, ee.ExitCode(), nil
	}
	return r, -1, err
}

// runPlacement is where mk run's executable goes: the file name under the
// store's bin/<link key>/ (native's keyed), where an executable already
// there (equal inputs, a deterministic link) is used as is; the cache
// directory is Config.CacheDir, MK_CACHE, else <package root>/.mk-cache.
// With --no-cache the executable goes to a fresh temporary directory, which
// Run removes after the child exits.
func (s *Session) runPlacement(name string) (func(tmp string) (string, []*diag.Diagnostic), string) {
	file := name + exeSuffix(s.cfg.Target)
	if !s.cfg.NoCache {
		return nil, file
	}
	return func(tmp string) (string, []*diag.Diagnostic) {
		sweepRunDirs.Do(func() { sweepStaleRunDirs(os.TempDir(), time.Now().Add(-staleRunAge)) })
		dir, err := os.MkdirTemp("", "mk-run-")
		if err != nil {
			return "", []*diag.Diagnostic{diag.Toolchain("fs", "mkdir", err.Error(), "cannot make a directory for the executable")}
		}
		exe, ds := placeExe(tmp, filepath.Join(dir, file))
		if len(ds) > 0 {
			os.RemoveAll(dir)
		}
		return exe, ds
	}, ""
}

// staleRunAge is how old a `mk-run-*` directory must be before a later
// run removes it: one a killed run left behind (such directories can fill
// the temp volume). A day is far longer than any run.
const staleRunAge = 24 * time.Hour

// sweepRunDirs makes the sweep once per process.
var sweepRunDirs sync.Once

// sweepStaleRunDirs removes every `mk-run-*` directory directly under tmp
// last modified before cutoff. A directory still in use stays: a running
// executable cannot be removed on Windows, and nothing runs for a day.
// Errors are ignored; the sweep is housekeeping.
func sweepStaleRunDirs(tmp string, cutoff time.Time) {
	ents, err := os.ReadDir(tmp)
	if err != nil {
		return
	}
	for _, e := range ents {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), "mk-run-") {
			continue
		}
		if info, err := e.Info(); err == nil && info.ModTime().Before(cutoff) {
			os.RemoveAll(filepath.Join(tmp, e.Name()))
		}
	}
}

// cacheDir is the build's cache directory, always absolute:
// Config.CacheDir (which normalize fills from MK_CACHE and makes absolute),
// else <package root>/.mk-cache.
func (s *Session) cacheDir() string {
	if s.cfg.CacheDir != "" {
		return s.cfg.CacheDir
	}
	return filepath.Join(s.root.dir, ".mk-cache")
}
