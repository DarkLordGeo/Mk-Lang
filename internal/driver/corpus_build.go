// Why: some corpus units are judged on what they do when run, not only on
// whether they check; the corpus session must build and run them the same
// way on every host.
//
// What: the corpus session's build step: tools, cacheDir, buildUnit and
// runUnit, with unitRunTimeout.
//
// How: a clean unit judged through flow is built with UnitBuild and
// UnitRun, and with UnitRun a unit that declares Main is linked and run
// with no arguments, empty standard input and a fresh working directory
// under a 10 s timeout. A host that cannot run the target's executables
// builds the unit and skips the run.

package driver

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"mk/internal/diag"
	"mk/internal/toolchain"
	"mk/internal/types"
)

// unitRunTimeout bounds one corpus unit's run.
const unitRunTimeout = 10 * time.Second

// tools is the corpus session's toolchain, discovered once on the first
// build (mk corpus without --build never discovers it).
func (c *CorpusSession) tools() (*toolchain.Tools, *diag.Diagnostic) {
	c.toolsOnce.Do(func() {
		if c.w.cfg.Tools != nil {
			c.tl = c.w.cfg.Tools
			return
		}
		t, err := toolchain.Discover(toolchain.Options{LLVMDir: c.w.cfg.LLVMDir})
		if err != nil {
			c.tlErr = toolErr(err, "no usable LLVM toolchain: "+err.Error())
			return
		}
		c.tl = t
	})
	return c.tl, c.tlErr
}

// cacheDir is the corpus session's cache directory: Config.CacheDir,
// MK_CACHE, else mk-corpus under the OS temporary directory. Unit
// executables are linked into its store's bin, and the work directories are
// made in its store's tmp. Names inside bin are content hashes, so sharing
// it is safe.
//
// Config.CacheDir is already MK_CACHE when unset, and absolute (normalize).
func (c *CorpusSession) cacheDir() string {
	if c.w.cfg.CacheDir != "" {
		return c.w.cfg.CacheDir
	}
	if d, err := filepath.Abs(filepath.Join(os.TempDir(), "mk-corpus")); err == nil {
		return d
	}
	return filepath.Join(os.TempDir(), "mk-corpus")
}

// buildUnit is stages 8 to 10 of a clean unit judged through flow. Its
// E0000 and E0001 are returned like any diagnostic of the unit.
func (c *CorpusSession) buildUnit(u UnitInput, b *build, out *UnitOutcome) []*diag.Diagnostic {
	w := c.w
	tools, d := c.tools()
	if d != nil {
		return []*diag.Diagnostic{d}
	}
	root := unitModulePath(u.Name, "")
	_, hasMain := w.items.ByKey(types.ItemKey(root, "Main"))
	run := u.Mode == UnitRun && hasMain
	// A host that cannot run the target's executables builds
	// the unit and does not link or run it; RunSkipped says why,
	// so the judge counts a build pass with no run, never a run
	// failure.
	var noRunner string
	prefix, canRun := w.cfg.runner()
	if run && !canRun {
		run, noRunner = false, "no runner for "+w.cfg.Target.Name+" executables on this machine"
	}
	var exe, skipped string
	j := nativeJob{tools: tools, name: "unit", mainMod: root, pkg: b.mods, timed: func(_ string, f func()) { f() },
		allFuncs: !hasMain, noBody: &skipped, cache: c.cacheDir()}
	if run {
		j.keyed = "unit" + exeSuffix(w.cfg.Target)
	}
	exe, ds := w.native(j)
	// Cache collection for the corpus: its run ends in internal/corpus, where no
	// driver call follows, so each unit's build ends with MaybeGC. It only
	// stats gc.stamp unless a collection is due, and the GC lock serializes
	// concurrent units. Before the unit's run, which reads its executable
	// from the store's bin: GC never evicts an entry marked within Grace,
	// and native marks an executable it reuses.
	w.maybeGC()
	if len(ds) > 0 {
		return ds
	}
	out.Built = true
	switch {
	case noRunner != "":
		out.RunSkipped = noRunner
		return nil
	case !run:
		return nil
	case skipped != "":
		out.RunSkipped = skipped
		return nil
	}
	status, timedOut, d := runUnit(prefix, exe)
	if d != nil {
		// Main was linked but could not be started: E0001, never a run
		// with status -1.
		return []*diag.Diagnostic{d}
	}
	out.Ran = true
	out.RunStatus, out.TimedOut = status, timedOut
	return nil
}

// runUnit runs a unit's executable through the runner prefix under the
// timeout: no arguments, empty standard input, a fresh temporary working
// directory, output discarded. It returns the exit status (-1 when the
// timeout killed it) and whether the timeout killed it, or an E0001 when
// the run could not start (no working directory, or the process did not
// start). Only the process is killed: a Mk program starts no child process.
func runUnit(prefix []string, exe string) (int, bool, *diag.Diagnostic) {
	dir, err := os.MkdirTemp("", "mk-unit-")
	if err != nil {
		return -1, false, diag.Toolchain("fs", "mkdir", pathErrText(err), "cannot make a working directory for the unit's run")
	}
	defer os.RemoveAll(dir)
	ctx, cancel := context.WithTimeout(context.Background(), unitRunTimeout)
	defer cancel()
	argv := append(append([]string(nil), prefix...), toolchain.HostPathFor(prefix, exe))
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = dir
	var discard bytes.Buffer
	cmd.Stdin, cmd.Stdout, cmd.Stderr = bytes.NewReader(nil), &discard, &discard
	err = cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		return -1, true, nil
	}
	var ee *exec.ExitError
	switch {
	case err == nil:
		return 0, false, nil
	case errors.As(err, &ee):
		return ee.ExitCode(), false, nil
	}
	return -1, false, diag.Toolchain(filepath.Base(argv[0]), "", err.Error(), "cannot start the unit's executable")
}
