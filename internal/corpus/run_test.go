// Why: mk corpus's options decide what is judged and how a run exits; a
// wrong --root or a silent pending unit would hide failures.
// What: tests of Run on a synthetic repository (--root is the root, never a
// starting point; --no-pending fails an unlisted pending unit; the
// Compiler starts once), a parallel race test, and BenchmarkCorpus.
// How: a fake Compiler counts its calls; the benchmark runs the whole doc/
// corpus through Run against a member-less stand-in std.

package corpus

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"mk/internal/diag"
	"mk/internal/driver"
)

// writeRoot writes a repository whose only corpus document is
// doc/reference.md holding text, and returns its directory.
func writeRoot(t testing.TB, text string) string {
	t.Helper()
	dir := t.TempDir()
	full := filepath.Join(dir, "doc", "reference.md")
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// twoOKBlocks is a reference with two ok blocks, keyed
// doc/reference.md:4 and doc/reference.md:11.
const twoOKBlocks = "# Ref\n\n" + fence + "mk\n// verdict: ok\nA() { }\n" + fence + "\n\nProse.\n\n" +
	fence + "mk\n// verdict: ok\nB() { }\n" + fence + "\n"

// fakeCompiler is a Compiler whose every unit comes back with an empty
// report judged through types, over the stages avail. It counts its calls.
type fakeCompiler struct {
	avail  driver.StageSet
	starts atomic.Int32
	checks atomic.Int32
}

func (f *fakeCompiler) Start() (StdInfo, error) {
	f.starts.Add(1)
	return StdInfo{Through: diag.PhaseTypes}, nil
}

func (f *fakeCompiler) Check(u *Unit, mode driver.UnitMode) driver.BlockResult {
	f.checks.Add(1)
	return driver.BlockResult{Through: diag.PhaseTypes, RunStatus: -1}
}

func (f *fakeCompiler) Available() driver.StageSet { return f.avail }

// TestOnlyModulePages: a block of doc/std/ is keyed by its path under the
// root, and --only "doc/std/*.md:*" selects it and nothing of doc/*.md.
func TestOnlyModulePages(t *testing.T) {
	root := writeRoot(t, twoOKBlocks)
	page := filepath.Join(root, "doc", "std", "x.md")
	if err := os.MkdirAll(filepath.Dir(page), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(page, []byte("# std.x\n\n"+fence+"mk\n// verdict: ok\nC() { }\n"+fence+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r, err := Run(Options{Root: root, Jobs: 1, Only: "doc/std/*.md:*", Compiler: &fakeCompiler{avail: stagesResolve}})
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	for _, u := range r.Units {
		keys = append(keys, u.Unit.Key)
	}
	if len(keys) != 1 || keys[0] != "doc/std/x.md:4" {
		t.Errorf("--only doc/std/*.md:* judged %q, want [doc/std/x.md:4]", keys)
	}
}

// An explicit --root is the root. A directory inside the repository is a
// usage error (exit 2), not the enclosing repository; the empty root still
// walks up from the working directory.
func TestExplicitRootIsNotWalked(t *testing.T) {
	root := repoRoot(t)
	for _, dir := range []string{filepath.Join(root, "internal"), filepath.Join(root, "internal", "corpus"), t.TempDir()} {
		if r, err := Run(Options{Root: dir}); err == nil {
			t.Errorf("Run(Root: %s) judged %d units; want a root error", dir, len(r.Units))
		} else if !strings.Contains(err.Error(), "no repository root") {
			t.Errorf("Run(Root: %s): %v; want a root error", dir, err)
		}
	}
	if got, err := FindRoot(""); err != nil || got != root {
		t.Errorf("FindRoot(\"\") = %q, %v; want %q (walked up from the working directory)", got, err, root)
	}
	syn := writeRoot(t, twoOKBlocks)
	r, err := Run(Options{Root: syn, Compiler: &fakeCompiler{avail: stagesResolve}})
	if err != nil || len(r.Units) != 2 {
		t.Fatalf("Run(Root: synthetic root) = %v; want its 2 blocks", err)
	}
}

// --no-pending: an unlisted PENDING unit is FAIL "unexplained pending:
// <reason>" and makes the exit status 1; a unit pending.txt lists stays
// PENDING with its reason. Without NoPending both stay PENDING and the exit
// status is 0.
func TestNoPendingFailsUnlisted(t *testing.T) {
	root := writeRoot(t, twoOKBlocks)
	const listed, unlisted = "doc/reference.md:4", "doc/reference.md:11"
	allow, err := ParseAllowlist(listed + " waits for signatures\n")
	if err != nil {
		t.Fatal(err)
	}
	for _, noPending := range []bool{true, false} {
		r, err := Run(Options{Root: root, NoPending: noPending, Compiler: &fakeCompiler{avail: stagesResolve}, allow: &allow})
		if err != nil {
			t.Fatal(err)
		}
		if len(r.Units) != 2 || r.Units[0].Unit.Key != listed || r.Units[1].Unit.Key != unlisted {
			t.Fatalf("units = %v", r.Units)
		}
		l, u := r.Units[0], r.Units[1]
		if l.Status != StatusPending || l.Explained != "waits for signatures" || l.Reason != "needs StageSignatures" {
			t.Errorf("NoPending %v: listed unit is %s %q (listed %q); want PENDING needs StageSignatures, listed", noPending, l.Status, l.Reason, l.Explained)
		}
		wantSt, wantReason, wantExit := StatusPending, "needs StageSignatures", 0
		if noPending {
			wantSt, wantReason, wantExit = StatusFail, "unexplained pending: needs StageSignatures", 1
		}
		if u.Status != wantSt || u.Reason != wantReason || u.Explained != "" {
			t.Errorf("NoPending %v: unlisted unit is %s %q; want %s %q", noPending, u.Status, u.Reason, wantSt, wantReason)
		}
		if got := ExitStatus(r); got != wantExit {
			t.Errorf("NoPending %v: ExitStatus = %d, want %d", noPending, got, wantExit)
		}
		if s := r.Summary; s.Explained != 1 || s.Fail+s.Pending != 2 {
			t.Errorf("NoPending %v: summary %+v", noPending, s)
		}
	}
}

// Run starts its Compiler (which loads and checks std, DriverCompiler's one
// CorpusSession) exactly once, however many units and workers, and checks
// every unit once.
func TestRunStartsCompilerOnce(t *testing.T) {
	root := writeRoot(t, twoOKBlocks)
	for _, jobs := range []int{1, 8} {
		c := &fakeCompiler{avail: stagesResolve}
		if _, err := Run(Options{Root: root, Jobs: jobs, Compiler: c}); err != nil {
			t.Fatal(err)
		}
		if s, k := c.starts.Load(), c.checks.Load(); s != 1 || k != 2 {
			t.Errorf("-j %d: Start called %d times, Check %d; want 1 and 2", jobs, s, k)
		}
	}
}

// BenchmarkCorpus measures that the harness does not hide the compiler's
// cost: the whole doc/ corpus through Run, with
// the member-less stand-in std so that units are judged through types. One
// iteration is one session: std is checked once, units in parallel.
func BenchmarkCorpus(b *testing.B) {
	std := standInStd(b, repoRoot(b))
	units := 0
	for b.Loop() {
		r, err := Run(Options{Driver: driver.Config{StdDir: std}})
		if err != nil {
			b.Fatal(err)
		}
		if r.Std.Through < diag.PhaseTypes {
			b.Fatalf("judged through %s, want types or later", r.Std.Through)
		}
		units = len(r.Units)
	}
	b.ReportMetric(float64(units), "units/op")
}

// Units share one types.Items, and reads of the shared tables take no
// lock, so no published entry may change: units that each declare a linear
// type with a consuming method, judged in parallel, must not race. It
// detects only under -race (`go test -race -run TestParallelUnitsDoNotRace
// ./internal/corpus`). Today it reports a race: borrow's consumers
// (internal/borrow/fixes.go) scans every item of the shared table while
// another unit's resolve sets Owner on an item it has already added
// (internal/resolve/declare.go, declareMethod), which borrow and resolve
// have yet to fix. Without -race it checks that every unit reports its one
// E0402 with the release fact.
func TestParallelUnitsDoNotRace(t *testing.T) {
	// workers goroutines each check n/workers units one after another, so
	// one unit's stage 7 overlaps another's resolve (units started all at
	// once run in step and miss the overlap).
	const n, workers = 64, 8
	comp := DriverCompiler(repoRoot(t), driver.Config{})
	std, err := comp.Start()
	if err != nil {
		t.Fatal(err)
	}
	if std.Through < diag.PhaseFlow {
		t.Fatalf("std judged through %s, want flow (borrow runs on the units only then):\n%s", std.Through, std.Text)
	}
	units := make([]*Unit, n)
	for i := range units {
		tok := fmt.Sprintf("Tok%d", i)
		// F never consumes t, so borrow's E0402 lists Tok's consuming
		// methods (releaseFacts, consumers).
		src := fmt.Sprintf("linear struct %[1]s {\n    id: i32\n}\n\n%[1]s.Close(self): i32 {\n    %[1]s(id) := self\n    return id\n}\n\nPeek(t: &%[1]s): i32 {\n    return t.id\n}\n\nF(): i32 {\n    t := %[1]s(id: 1)\n    return Peek(&t)\n}\n", tok)
		key := fmt.Sprintf("race/%d.mk", i)
		units[i] = &Unit{Key: key, Name: key, Verdict: VerdictOK, Files: []UnitFile{{Path: "u.mk", Text: src}}}
	}
	got := make([]driver.BlockResult, n)
	var wg sync.WaitGroup
	for w := range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := w; i < n; i += workers {
				got[i] = comp.Check(units[i], driver.UnitCheck)
			}
		}()
	}
	wg.Wait()
	for i, r := range got {
		ds := r.Report.Diags
		if len(ds) != 1 || ds[0].Diag.Code != diag.E0402 || !strings.Contains(r.Report.Text(r.Sources), `release: ["Close"]`) {
			t.Errorf("%s: want one E0402 listing release Close, got:\n%s", units[i].Key, r.Report.Text(r.Sources))
		}
	}
}
