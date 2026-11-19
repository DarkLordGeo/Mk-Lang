// Why: every body check accepts must lower and validate with no ICE; check
// cannot import lower, so the property is tested here.
// What: FuzzLower, TestEveryUnitLowers, TestFuzzBoundNeedsTiming,
// TestSideTablesSizedByBody.
// How: FuzzLower mutates sources through syntax, resolve, check and lower;
// TestEveryUnitLowers runs its seeds as a plain test: every doc block (any
// verdict: a reject block's other units, and every E04xx to E06xx block,
// reach lower), every tests/programs and tests/conformance .mk file.

package lower

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// lowerSeeds is every source FuzzLower starts from, keyed for messages, in
// a fixed order.
func lowerSeeds(t testing.TB) []corpusUnit {
	keys, texts := docBlocks(t)
	var out []corpusUnit
	for _, k := range keys {
		out = append(out, corpusUnit{key: k, src: texts[k]})
	}
	var files []string
	for _, root := range []string{filepath.Join("..", "..", "tests", "programs"), filepath.Join("..", "..", "tests", "conformance")} {
		err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
			if err == nil && !d.IsDir() && strings.HasSuffix(p, ".mk") {
				files = append(files, p)
			}
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	sort.Strings(files)
	for _, p := range files {
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		src := strings.ReplaceAll(string(data), "\r\n", "\n")
		rel, _ := filepath.Rel(filepath.Join("..", ".."), p)
		// tests/programs are binaries without the test prelude; a
		// conformance unit compiles with it when it says so.
		bin := strings.Contains(filepath.ToSlash(rel), "tests/programs/") && !strings.Contains(src, "// prelude: yes")
		out = append(out, corpusUnit{key: filepath.ToSlash(rel), src: src, bin: bin})
	}
	return out
}

// lowerNoICE builds u's module and lowers every unit check accepts (and,
// when that raises no ICE, again with the trim pass on). It returns the
// number of bodies lowered, every ICE that is lower's own, every ICE tagged
// as another package's gap (corpusPending), and the number of units stage 6
// rejected (counted, not lowered: lower's precondition is a body check
// accepted; a reject block's bodies and most fuzz inputs land
// here legitimately). A module with a diagnostic before stage 6 is skipped,
// as the corpus test does.
func lowerNoICE(t testing.TB, core, pre string, u corpusUnit) (n int, own, pending []string, rejected int) {
	w, b, ok := corpusWorld(t, core, pre, u)
	if !ok {
		return 0, nil, nil, 0
	}
	bodies, ices, rs := lowerUnitsJudged(w, b, false)
	if len(ices) == 0 {
		// The trim pass too: Validate after it.
		_, ices = lowerUnitsWith(w, b, true)
	}
	for _, s := range ices {
		if corpusPending(s) {
			pending = append(pending, s)
		} else {
			own = append(own, s)
		}
	}
	return len(bodies), own, pending, len(rs)
}

// otherLanes are the seed ICEs that are not lower's and carry no
// corpusPending tag: the source key, a fragment of the message, and the
// package it waits on. An entry that stops matching fails the test, so the
// list stays exact. It is empty now.
var otherLanes []struct{ key, frag, on string }

func otherLane(key, s string) bool {
	for _, o := range otherLanes {
		if o.key == key && strings.Contains(s, o.frag) {
			return true
		}
	}
	return false
}

// TestEveryUnitLowers is FuzzLower's property on its seeds.
func TestEveryUnitLowers(t *testing.T) {
	if testing.Short() {
		t.Skip("the seeds take a few seconds")
	}
	core, pre := corpusPrelude(t), testPrelude(t)
	seeds := lowerSeeds(t)
	lowered, judged, rejected := 0, 0, 0
	var pending []string
	seen := map[string]bool{}
	for _, u := range seeds {
		n, own, pend, rs := lowerNoICE(t, core, pre, u)
		if n > 0 || len(own) > 0 || len(pend) > 0 || rs > 0 {
			judged++
		}
		lowered += n
		rejected += rs
		for _, s := range pend {
			pending = append(pending, u.key+": "+s)
		}
		for _, s := range own {
			if otherLane(u.key, s) {
				seen[u.key] = true
				pending = append(pending, u.key+": "+s)
				continue
			}
			t.Errorf("%s: %s", u.key, s)
		}
	}
	for _, o := range otherLanes {
		if !seen[o.key] {
			t.Errorf("%s no longer raises %q (waiting on %s): drop it from otherLanes", o.key, o.frag, o.on)
		}
	}
	t.Logf("%d sources, %d reached stage 6, %d bodies lowered and validated, %d units rejected by check, %d pending ICEs (other lanes' gaps)", len(seeds), judged, lowered, rejected, len(pending))
	for _, s := range pending {
		t.Logf("pending: %s", s)
	}
}

// FuzzLower: arbitrary source through the front end and lower with
// Validate on. The property is no ICE of lower's own, and, under
// MK_TIMING=1 only, termination within lowerFuzzBound per input.
func FuzzLower(f *testing.F) {
	core, pre := corpusPrelude(f), testPrelude(f)
	for _, u := range lowerSeeds(f) {
		f.Add(u.src, u.bin)
	}
	f.Fuzz(func(t *testing.T, src string, bin bool) {
		start := time.Now()
		_, own, _, _ := lowerNoICE(t, core, pre, corpusUnit{key: "fuzz", src: src, bin: bin})
		for _, s := range own {
			t.Errorf("%s\n%s", s, src)
		}
		checkFuzzBound(t, time.Since(start))
	})
}

// lowerFuzzBound is FuzzLower's per-input bound (check's proposed,
// unmeasured bound). It is a wall-clock assertion, so it is made only under
// MK_TIMING=1: Go runs every committed seed as a plain subtest of go test
// ./internal/lower, and on a loaded machine the seed 62bc5c9760190c31,
// which passes alone in about 0.3 s, went over it.
const lowerFuzzBound = 2 * time.Second

// checkFuzzBound reports an input that took d: an error over the bound
// under MK_TIMING=1, otherwise a log line.
func checkFuzzBound(t testing.TB, d time.Duration) {
	t.Helper()
	if d <= lowerFuzzBound {
		return
	}
	if !timingRun() {
		t.Logf("%s: the %v bound is not asserted (took %v)", timingOff, lowerFuzzBound, d)
		return
	}
	t.Errorf("took %v, over the %v bound", d, lowerFuzzBound)
}

// boundRec is a testing.TB that records Errorf and Logf, for
// TestFuzzBoundNeedsTiming.
type boundRec struct {
	testing.TB
	errs, logs int
}

func (r *boundRec) Helper()               {}
func (r *boundRec) Errorf(string, ...any) { r.errs++ }
func (r *boundRec) Logf(string, ...any)   { r.logs++ }

// TestFuzzBoundNeedsTiming: FuzzLower's 2 s bound fails an input
// only under MK_TIMING=1. Before, the bound was asserted in every run, so a
// committed seed failed plain go test under load.
func TestFuzzBoundNeedsTiming(t *testing.T) {
	for _, c := range []struct {
		env        string
		d          time.Duration
		errs, logs int
	}{
		{"", 3 * time.Second, 0, 1},
		{"0", 3 * time.Second, 0, 1},
		{"", time.Second, 0, 0},
		{"1", 3 * time.Second, 1, 0},
		{"1", time.Second, 0, 0},
		{"1", lowerFuzzBound, 0, 0},
	} {
		t.Setenv("MK_TIMING", c.env)
		r := &boundRec{TB: t}
		checkFuzzBound(r, c.d)
		if r.errs != c.errs || r.logs != c.logs {
			t.Errorf("MK_TIMING=%q, %v: %d errors and %d logs, want %d and %d", c.env, c.d, r.errs, r.logs, c.errs, c.logs)
		}
	}
}

// TestSideTablesSizedByBody: the per-node side tables of one body (the
// adapter's node index, the builder's binding locals) span the item's node
// range, not the file's NodeCount, so lowering every body of a file stays
// linear in the file (BenchmarkLowerManyBodies). They were once sized
// by the file: about 1.09 MB allocated per 6-statement body of a
// 2,000-function file.
func TestSideTablesSizedByBody(t *testing.T) {
	var sb strings.Builder
	for i := 0; i < 200; i++ {
		sb.WriteString("F" + itoa(i) + "(a: int, b: int): int {\n\tv := a + b\n\treturn v\n}\n")
	}
	w := newWorld(t, true)
	m := w.add("m", opt{}, sb.String())
	cfg := w.lowerCfg()
	file := m.files[0]
	for _, id := range m.m.Units() {
		if m.key(id) != "F100" {
			continue
		}
		body := w.c.Check(m.m, id)
		in := FromCheck(cfg, w.c, body).(*checkInput)
		lo, n := in.Nodes()
		if n > int(file.NodeCount)/50 || len(in.nodes) != n {
			t.Fatalf("node range [%d, +%d) for one body of a file of %d nodes; nodes table %d", lo, n, file.NodeCount, len(in.nodes))
		}
		bl := newBuilder(cfg, in)
		if len(bl.locals) != n || bl.nodeBase != lo {
			t.Fatalf("builder locals %d at base %d, want %d at %d", len(bl.locals), bl.nodeBase, n, lo)
		}
		mb := Lower(cfg, in) // Validate on: v binds inside the range
		if len(mb.Locals) < 4 {
			t.Fatalf("F100 lowered to %d locals", len(mb.Locals))
		}
		return
	}
	t.Fatal("no unit m.F100")
}
