// Why: a clean build and a one-function edit do not show what an editor
// waits for: a rebuild with nothing changed, and the diagnostics after an
// edit. Mk states compile-speed budgets for those, and the table should say
// whether each is met.
//
// What: the extra kinds of the compile table (tests/bench/README.md
// "Compile speed"): measureWarm, the no-change rebuild of every language;
// measureCheck and editSig, Mk's two `mk check` rows against the
// edit-to-diagnostic budget; compileBudgets and renderCompileBudgets.
//
// How: compile.go runs these kinds after the clean and edit builds, which
// they do not change. A check row's edit names a token the edit number makes
// new, so a check that did not read the edit gives the wrong verdict.

package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

// checkCmd is `mk check -j 8` of the program in the profile's cache, the
// editor's command after an edit (the edit-to-diagnostic budget).
func (s compileSetup) checkCmd(t *toolchain) buildCmd {
	return buildCmd{Dir: s.Src, Env: []string{"MK_CACHE=" + filepath.Join(s.State, "mkcache"), "MK_STD=" + t.MkStd},
		Args: []string{t.Mk, "check", "-j", strconv.Itoa(compileJobs), s.Src}}
}

// checkRunner is how measureCheck runs a check: runCheck, or a test's fake
// that records which state each timed sample came from.
var checkRunner = runCheck

// runCheck runs one `mk check` and returns its wall time, its exit status
// and its combined output. A check that could not start is an error.
func runCheck(c buildCmd) (time.Duration, int, string, error) {
	cmd := exec.Command(c.Args[0], c.Args[1:]...)
	cmd.Dir = c.Dir
	cmd.Env = compileEnv(os.Environ(), c.Env)
	start := time.Now()
	out, err := cmd.CombinedOutput()
	d := time.Since(start)
	var ee *exec.ExitError
	switch {
	case err == nil:
		return d, 0, string(out), nil
	case errors.As(err, &ee):
		return d, ee.ExitCode(), string(out), nil
	}
	return d, -1, string(out), err
}

// checkVerdict is a check row's test of one `mk check`: a good variant
// exits 0, a bad one exits 1 and quotes its token.
func checkVerdict(code int, out string, bad bool, token string) error {
	if !bad {
		if code != 0 {
			return fmt.Errorf("mk check of a valid edit exited %d: %s", code, firstLines(out, 8))
		}
		return nil
	}
	// The diagnostic quotes the name in backticks (E0201), so a longer
	// token of a later edit cannot match a shorter one.
	if code != 1 || !strings.Contains(out, "`"+token+"`") {
		return fmt.Errorf("mk check of an edit with an unknown name exited %d without naming %s (a stale check): %s", code, token, firstLines(out, 8))
	}
	return nil
}

// measureWarm times no-change builds after the edit builds (edit number
// last is in the sources): one untimed, then WarmRuns timed, each checked
// against last's checksum. A make-like tool may run nothing (C's link is
// skipped when the executable is newer than every object).
func measureWarm(t *toolchain, s compileSetup, o compileOptions, last int, c *compileCell) error {
	want := strconv.FormatUint(o.Spec.checksum(last), 10)
	c.Nothing = true
	for r := 0; r <= o.WarmRuns; r++ {
		steps, err := s.steps(t, false)
		if err != nil {
			return err
		}
		if len(slices.Concat(steps...)) > 0 {
			c.Nothing = false
		}
		d, err := runSteps(steps)
		if err != nil {
			return fmt.Errorf("no-change build: %w", err)
		}
		out, _, err := runOnce(s.Out, nil)
		if err != nil {
			return err
		}
		if out != want {
			return fmt.Errorf("after a no-change build the program printed %q, want %s (a stale build)", out, want)
		}
		if r == 0 {
			continue
		}
		if r == 1 {
			c.Builds = slices.Concat(steps...)
		}
		c.Times = append(c.Times, d.Seconds())
	}
	c.Sum = summarize(c.Times)
	return nil
}

// measureCheck times `mk check` in the dev profile's cache after an edit,
// with edit numbers from base+1. Every timed check follows a state whose
// verdict is the opposite, so a check that did not read the edit gives the
// wrong verdict (checkVerdict):
//   - check-body (kind mkBadBody): a valid body edit (checked, untimed),
//     then the same body with an unknown name, timed: edit to diagnostic;
//   - check-sig (kind mkSig): a new exported function whose parameter has
//     an unknown type (checked, untimed), then the valid function, timed:
//     a signature edit, which changes the module's interface. It is made
//     in module sigAt (m0, the root of the import tree, which every other
//     module imports through its parents), with every body held at edit
//     base, so the timed state differs from the one before it in G alone.
//
// The first pair is untimed, and the sources end valid (check-body at
// edit base+n, check-sig at edit base, both without G), so later cells
// see the program the table describes.
func measureCheck(t *toolchain, s compileSetup, o compileOptions, base int, kind string, c *compileCell) error {
	cmd := s.checkCmd(t)
	c.Builds = []buildCmd{cmd}
	for k := 1; k <= o.CheckRuns+1; k++ {
		n := base + k
		first, timed := "", mkBadBody // check-body: valid, then the unknown name
		if kind == mkSig {
			first, timed = mkBadSig, mkSig
		}
		for _, v := range []string{first, timed} {
			var err error
			if kind == mkSig {
				err = s.editSig(o.Spec, base, n, v)
			} else {
				err = s.editMk(o.Spec, n, v)
			}
			if err != nil {
				return err
			}
			d, code, out, err := checkRunner(cmd)
			if err != nil {
				return err
			}
			bad := v == mkBadBody || v == mkBadSig
			if err := checkVerdict(code, out, bad, o.Spec.mkCheckToken(v, n)); err != nil {
				return fmt.Errorf("%s, edit %d: %w", c.Kind, n, err)
			}
			if v == timed && k > 1 {
				c.Times = append(c.Times, d.Seconds())
			}
		}
	}
	c.Sum = summarize(c.Times)
	if kind == mkSig {
		return s.editSig(o.Spec, base, 0, "")
	}
	return s.editMk(o.Spec, base+o.CheckRuns+1, "")
}

// editSig writes module sigAt with its bodies at edit body and, for a
// signature variant, the function G<n>.
func (s compileSetup) editSig(g genSpec, body, n int, variant string) error {
	p, text := genMkModuleSig(g, g.sigAt(), body, n, variant)
	return os.WriteFile(filepath.Join(s.Src, filepath.FromSlash(p)), []byte(text), 0o644)
}

// compileBudget is one compile-speed budget that a cell of the compile table
// measures: its kind and profile, the platform, the bound in seconds and the
// program size it is stated at, in lines. Upper marks a budget of the user
// code that the cell measures with std included (the whole clean build in a
// fresh MK_CACHE): the cell is an upper bound of what the budget times, so
// it can show the budget met but never missed.
type compileBudget struct {
	Row, Kind, Profile, Platform string
	Bound                        float64
	At                           int
	Upper                        bool
}

var compileBudgets = []compileBudget{
	{"Edit to diagnostic, body edit (at most 100 ms at 1 MLOC)", "check-body", "dev", "", 0.100, 1_000_000, false},
	{"Edit to diagnostic, signature edit (at most 300 ms at 100 KLOC)", "check-sig", "dev", "", 0.300, 100_000, false},
	{"Dev build, one-function edit, COFF (at most 350 ms at 100 KLOC, no PDB)", "edit", "dev", "", 0.350, 100_000, false},
	{"Dev build, one-function edit, ELF (at most 200 ms at 100 KLOC)", "edit", "dev", "linux", 0.200, 100_000, false},
	{"Clean dev build, user code (at most 2 s at 100 KLOC)", "clean", "dev", "", 2, 100_000, true},
	{"Release, one-function edit (at most 30 s at 100 KLOC)", "edit", "release", "", 30, 100_000, false},
	{"Release, clean (at most 5 minutes at 100 KLOC)", "clean", "release", "", 300, 100_000, false},
}

// renderCompileBudgets prints each compile-speed budget that the table's Mk
// cells measure. A row gets a verdict only when the program is within 10
// percent of the size the budget is stated at; the verdict is the median
// against the bound (a point, not an interval: a budget is a time, not a
// ratio between cells). Every budget is still proposed, not committed. The
// Windows clean rows are of the whole build, std included (a fresh
// MK_CACHE), where the budget says "user code".
func renderCompileBudgets(w *strings.Builder, res *compileResults) {
	w.WriteString("**Against the compile-speed budgets.**\n\n| Budget | Mk cell | Lines | Verdict |\n|---|---|---|---|\n")
	for _, b := range compileBudgets {
		if b.Platform != res.Platform {
			continue
		}
		var c *compileCell
		for i := range res.Cells {
			x := &res.Cells[i]
			if x.Lang == "mk" && x.Kind == b.Kind && x.Profile == b.Profile {
				c = x
			}
		}
		if c == nil {
			continue
		}
		verdict := ""
		switch {
		case c.Excluded != "" || c.Sum.N == 0:
			verdict = "no verdict: the cell was excluded"
		case float64(c.Lines) < 0.9*float64(b.At) || float64(c.Lines) > 1.1*float64(b.At):
			verdict = fmt.Sprintf("no verdict: the program has %d lines, and the budget is stated at %d", c.Lines, b.At)
		case c.Sum.Median <= b.Bound && b.Upper:
			verdict = fmt.Sprintf("met: median %s for the whole build, std included, an upper bound of the user code's", fmtSec(c.Sum.Median))
		case c.Sum.Median <= b.Bound:
			verdict = fmt.Sprintf("met: median %s", fmtSec(c.Sum.Median))
		case b.Upper:
			verdict = fmt.Sprintf("no verdict: the cell includes std (median %s for the whole build, over the bound; the user code's share is not measured)", fmtSec(c.Sum.Median))
		default:
			verdict = fmt.Sprintf("**missed**: median %s", fmtSec(c.Sum.Median))
		}
		fmt.Fprintf(w, "| %s | %s %s | %d | %s |\n", b.Row, b.Profile, b.Kind, c.Lines, verdict)
	}
	w.WriteString("\n")
}
