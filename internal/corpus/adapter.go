// Why: the corpus must judge thousands of units without reaching into the
// driver's internals, and std must be loaded and checked once, not per unit.
//
// What: the Compiler interface the runner calls (Start, Check, Available),
// StdInfo, DriverCompiler over driver.CorpusSession, and ToUnitInput.
//
// How: DriverCompiler builds the session and the test prelude once, lazily,
// on the first Start, and Check is safe from many goroutines. The corpus
// reaches the driver only through CorpusSession.

package corpus

import (
	"slices"
	"strings"
	"sync"

	"mk/internal/diag"
	"mk/internal/driver"
	"mk/internal/source"
)

// StdInfo is what a Compiler knows about std before any unit runs.
type StdInfo struct {
	State   driver.StdState // std's report and the names and modules it lacks
	Through diag.Phase      // the last phase any unit can be judged through
	Text    string          // std's report rendered as text; "" when clean
}

// Compiler compiles corpus units. Run calls Start once, then Check from
// many goroutines; Check must be safe for concurrent use and each unit's
// Name is distinct.
type Compiler interface {
	// Start loads std once; the error is a usage error (exit 2).
	Start() (StdInfo, error)
	// Check compiles one unit as far as the session can judge it.
	Check(u *Unit, mode driver.UnitMode) driver.BlockResult
	// Available is the set of stages this compiler wires.
	Available() driver.StageSet
}

// DriverCompiler is the Compiler over driver.CorpusSession. The session and
// the test prelude (Prelude(root)) are made once, on the first Start.
func DriverCompiler(root string, cfg driver.Config) Compiler {
	return &driverCompiler{root: root, cfg: cfg}
}

type driverCompiler struct {
	root string
	cfg  driver.Config

	once    sync.Once
	sess    *driver.CorpusSession
	prelude string
	info    StdInfo
	err     error
}

func (d *driverCompiler) Start() (StdInfo, error) {
	d.once.Do(func() {
		d.prelude, d.err = Prelude(d.root)
		if d.err != nil {
			return
		}
		d.sess, d.err = driver.NewCorpusSession(d.cfg)
		if d.err != nil {
			return
		}
		res := d.sess.Std()
		d.info = StdInfo{State: d.sess.StdState(), Through: d.sess.Through()}
		if len(res.Report.Diags) > 0 {
			d.info.Text = res.Report.Text(res.Sources)
		}
	})
	return d.info, d.err
}

func (d *driverCompiler) Check(u *Unit, mode driver.UnitMode) driver.BlockResult {
	if _, err := d.Start(); err != nil {
		panic("corpus: Check after a failed Start: " + err.Error()) // Run never does this
	}
	return d.sess.CheckUnit(ToUnitInput(u, d.prelude, mode))
}

func (d *driverCompiler) Available() driver.StageSet { return driver.Available() }

// ToUnitInput is the adapter's mapping: Name and the files
// copied, sorted by path; the test prelude added as prelude.mk when
// u.Meta.Prelude is set; ParseOnly for verdict parse, which never gets the
// prelude; mode only for ok units (a run never applies to the others);
// Design for a design block, judged as a module unsafe_modules lists, never
// for a conformance unit.
func ToUnitInput(u *Unit, prelude string, mode driver.UnitMode) driver.UnitInput {
	in := driver.UnitInput{Name: u.Name, ParseOnly: u.ParseOnly(), Design: u.Block != nil}
	for _, f := range u.Files {
		in.Files = append(in.Files, driver.UnitFile{Path: f.Path, Text: f.Text})
	}
	if u.Meta.Prelude && !in.ParseOnly {
		// corpus-3: Units refuses such a unit; a hand-made one is an internal
		// error (Run's Guard turns it into a FAIL), never a silent swap.
		source.Assertf(!u.Owns(PreludeFile), source.Span{}, "unit %s has its own %s beside the test prelude", u.Key, PreludeFile)
		in.Files = append(in.Files, driver.UnitFile{Path: PreludeFile, Text: prelude})
		in.Prelude = PreludeFile
	}
	slices.SortFunc(in.Files, func(a, b driver.UnitFile) int { return strings.Compare(a.Path, b.Path) })
	if u.Verdict == VerdictOK {
		in.Mode = mode
	}
	return in
}
