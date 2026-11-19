// Why: each borrow rule must be shown needed: a compiler with the rule cut
// must accept an unsound program, or reject one the real compiler accepts.
// What: TestSound_Mutants (the parent), TestSound_MutantChild,
// TestSound_MutantBuild and the child timeout and worker helpers.
// How: each mutant is a child test binary built with go test -c -overlay, so
// the mutated borrow exists only there.
//
// TestSound_Mutants (MK_SOUND=1, the parent: one mutant at a time, each a
// child test binary built with go test -c -overlay, so the mutated borrow
// exists only there) and TestSound_MutantChild (the child: MK_SOUND_MUTANT
// set).

package corpus

import (
	"bufio"
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"mk/internal/driver"
)

// mutantSample is the fixed sample of a class P mutant: the first
// 20,000 programs of the run's base.
const mutantSample = 20000

// MutantResult is one mutant's line in the record.
type MutantResult struct {
	ID       string  `json:"id"`
	Rule     string  `json:"rule"`
	Class    string  `json:"class"`
	Outcome  string  `json:"outcome"`  // "needed", "not shown", or "error: ..."
	Accepted int     `json:"accepted"` // class R: accepted programs judged when the run stopped (the first violation stops it); class P: on the sample
	Kind     string  `json:"kind,omitempty"`
	Program  int     `json:"program,omitempty"`
	Seed     string  `json:"seed,omitempty"`
	Run      string  `json:"run,omitempty"`
	Text     string  `json:"text,omitempty"`
	Rate     float64 `json:"acceptance"`           // the mutant's acceptance on what it judged
	BaseRate float64 `json:"acceptance_unmutated"` // class P: the unmutated compiler's on the sample
	Dropped  int     `json:"dropped,omitempty"`    // class P: programs the mutant rejects that the unmutated compiler accepts
	Example  string  `json:"example,omitempty"`    // class P: the first such program's index and seed
	Defects  int     `json:"defects"`
	Wall     float64 `json:"wall_seconds"`
	Workers  int     `json:"workers,omitempty"` // the child's worker count (MK_SOUND_WORKERS, else NumCPU - 1)
	// Std (class P) is std's first diagnostic when the mutant rejects std
	// itself: then the rule is needed by std, and the sample is not run.
	Std string `json:"std,omitempty"`
	// Accepted (class P) is the accepted set of the sample as a hex bitset.
	Set string `json:"set,omitempty"`
}

// acceptedSet checks the first n programs of profile full from base on c
// and returns the accepted ones as a bitset, the count and the defects.
func acceptedSet(c SoundCompiler, base uint64, n, workers int) ([]byte, int, int) {
	set := make([]byte, (n+7)/8)
	var mu sync.Mutex
	var next atomic.Int64
	acc, def := 0, 0
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				i := int(next.Add(1) - 1)
				if i >= n {
					return
				}
				p := GenerateSound(mutantProfile(), ProgramSeed(base, i))
				class, _ := classifySoundProg(p, c.Check(fmt.Sprintf("sound/mutp/%d/%d", soundUnitN.Add(1), i), p.Text).Report)
				mu.Lock()
				switch class {
				case "accepted":
					set[i/8] |= 1 << (i % 8)
					acc++
				case "defect", "ice":
					def++
				}
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	return set, acc, def
}

// TestSound_MutantChild is one mutant's run inside its child binary:
// class R, a sized run of profile full that stops at its
// first violation, bounded by 200,000 accepted programs and
// MK_SOUND_SECONDS (default 900); class P, the accepted set of the fixed
// sample. It prints one line "MUTANT-RESULT <json>".
func TestSound_MutantChild(t *testing.T) {
	id := os.Getenv("MK_SOUND_MUTANT")
	if id == "" {
		t.Skip("MK_SOUND_MUTANT not set (the child of TestSound_Mutants)")
	}
	base := soundBase(t)
	res := MutantResult{ID: id, Class: os.Getenv("MK_SOUND_MUTANT_CLASS")}
	t0 := time.Now()
	c := NewSessionCompiler(driver.Config{}, nil)
	if err := c.Start(); err != nil {
		// The mutant rejects std itself, which the unmutated compiler
		// accepts: std is the program a class P criterion asks for (and the
		// session cannot judge units at flow while std is rejected, so the
		// sample is not run). A class R mutant that does this (M-R8-B: the
		// cut makes the rule stricter on std) counts the same way, needed
		// by std, since no run can follow.
		res.Std = err.Error()
		res.Wall = time.Since(t0).Seconds()
		b, _ := json.Marshal(res)
		fmt.Printf("MUTANT-RESULT %s\n", b)
		return
	}
	if res.Class == "P" {
		res.Workers = mutantWorkers(t)
		set, acc, def := acceptedSet(c, base, mutantSample, res.Workers)
		res.Set, res.Accepted, res.Defects = hex.EncodeToString(set), acc, def
		res.Rate = float64(acc) / mutantSample
	} else {
		cfg := SoundConfig{Profile: mutantProfile(), Base: base, Accepted: soundInt(t, "MK_SOUND_ACCEPTED", 200000),
			Budget: time.Duration(soundInt(t, "MK_SOUND_SECONDS", 900)) * time.Second, Seeds: 4, Stop: true, Label: "mutant-" + id,
			Workers: mutantWorkers(t)}
		rep, err := RunSound(context.Background(), cfg, c)
		if err != nil {
			t.Fatal(err)
		}
		res.Accepted, res.Defects, res.Workers = rep.Accepted, rep.DefectTotal()+rep.ICE, rep.Workers
		if rep.Generated > 0 {
			res.Rate = float64(rep.Accepted) / float64(rep.Generated)
		}
		if len(rep.Violations) > 0 {
			v := rep.Violations[0]
			res.Kind, res.Program, res.Seed, res.Run, res.Text = v.Kind, v.Program, v.Seed, v.Run, v.Text
		}
	}
	res.Wall = time.Since(t0).Seconds()
	b, _ := json.Marshal(res)
	fmt.Printf("MUTANT-RESULT %s\n", b)
}

// TestSound_Mutants is the parent (MK_SOUND=1; MK_SOUND_MUTANTS selects
// IDs, comma-separated): each mutant of tests/soundness/mutants.txt that is
// not pending is built into a child binary through go test -c -overlay and
// run alone; class P mutants are compared with the unmutated compiler's
// accepted set of the same sample, computed here. The record is
// runs/<label>-mutants.json. A mutant not shown needed is a test failure:
// a finding answered with a generator shape aimed at the rule, or a change
// to the rule, never a pass.
func TestSound_Mutants(t *testing.T) {
	soundLong(t)
	root := repoRoot(t)
	base := soundBase(t)
	ms := selectedMutants(t)
	label := soundEnv("MK_SOUND_LABEL", fmt.Sprintf("%s-mutants-%016x", time.Now().Format("2006-01-02"), base))
	work := filepath.Join(os.TempDir(), "mk-mutants")
	var baseSet []byte
	baseAcc := 0
	var out []MutantResult
	for _, m := range ms {
		dir := filepath.Join(work, m.ID)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		res := MutantResult{ID: m.ID, Rule: m.Rule, Class: string(m.Class)}
		if m.Class == 'P' && baseSet == nil {
			s := soundSess(t)
			baseSet, baseAcc, _ = acceptedSet(NewSessionCompiler(driver.Config{}, s), base, mutantSample, mutantWorkers(t))
			t.Logf("unmutated: %d of %d accepted (%.1f%%)", baseAcc, mutantSample, 100*float64(baseAcc)/mutantSample)
		}
		child, err := runMutantChild(t, root, dir, m, base)
		if err != nil {
			res.Outcome = "error: " + err.Error()
			t.Errorf("%s: %v", m.ID, err)
			out = append(out, res)
			continue
		}
		child.Rule = m.Rule
		res = *child
		switch m.Class {
		case 'P':
			if res.Std != "" {
				res.Outcome = "needed"
				res.Example = "std: " + res.Std
				break
			}
			set, _ := hex.DecodeString(res.Set)
			res.BaseRate = float64(baseAcc) / mutantSample
			for i := 0; i < mutantSample && i/8 < len(set) && i/8 < len(baseSet); i++ {
				if baseSet[i/8]&(1<<(i%8)) != 0 && set[i/8]&(1<<(i%8)) == 0 {
					if res.Dropped == 0 {
						res.Example = fmt.Sprintf("%d %#016x", i, ProgramSeed(base, i))
					}
					res.Dropped++
				}
			}
			res.Set = ""
			res.Outcome = "not shown"
			if res.Dropped > 0 {
				res.Outcome = "needed"
			}
		default:
			res.Outcome = "not shown"
			if res.Kind != "" {
				res.Outcome = "needed"
			}
			if res.Std != "" {
				res.Outcome = "needed"
				res.Example = "std: " + res.Std
			}
		}
		t.Logf("%s (%s, class %c): %s; accepted %d, kind %s, program %d, dropped %d, acceptance %.3f (unmutated %.3f), %.0f s, %d workers",
			m.ID, m.Rule, m.Class, res.Outcome, res.Accepted, res.Kind, res.Program, res.Dropped, res.Rate, res.BaseRate, res.Wall, res.Workers)
		if res.Outcome != "needed" {
			t.Errorf("%s (%s): not shown needed (4.6.2): aim a shape at the rule and rerun, or file a design request", m.ID, m.Rule)
		}
		out = append(out, res)
	}
	rec := map[string]any{"label": label, "kind": "mutants", "gen_version": GenVersion, "base": fmt.Sprintf("%#016x", base),
		"machine": soundMachine(), "build": fmt.Sprintf("%x", driver.BuildID()), "mutants": out,
		"command": map[string]string{"test": "TestSound_Mutants", "MK_SOUND_MUTANTS": os.Getenv("MK_SOUND_MUTANTS"), "MK_STD": os.Getenv("MK_STD")}}
	b, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(soundRoot(t), "runs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, label+".json"), append(b, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}

// buildMutantChild builds the corpus test binary with overlay into bin.
func buildMutantChild(root, overlay, bin string) error {
	build := exec.Command(filepath.Join(runtime.GOROOT(), "bin", "go"), "test", "-c", "-overlay="+overlay, "-o", bin, "./internal/corpus")
	build.Dir = root
	if b, err := build.CombinedOutput(); err != nil {
		return fmt.Errorf("build: %v: %s", err, b)
	}
	return nil
}

// TestSound_MutantBuild (MK_SOUND=1, MK_SOUND_MUTANTS as for
// TestSound_Mutants) builds every child binary into %TMP%/mk-mutants/<id>/
// and runs none: built outside the machine lock while the tree builds, the
// binaries are then run by TestSound_Mutants with MK_SOUND_MUTANT_PREBUILT=1,
// so another edit in the middle of the run cannot break a build.
func TestSound_MutantBuild(t *testing.T) {
	soundLong(t)
	root := repoRoot(t)
	work := filepath.Join(os.TempDir(), "mk-mutants")
	for _, m := range selectedMutants(t) {
		dir := filepath.Join(work, m.ID)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		overlay, err := mutantOverlay(dir, root, m)
		if err != nil {
			t.Errorf("%s: %v", m.ID, err)
			continue
		}
		if err := buildMutantChild(root, overlay, filepath.Join(dir, "corpus.test.exe")); err != nil {
			t.Errorf("%s: %v", m.ID, err)
		}
	}
}

// selectedMutants is the table's rows that are not pending, narrowed to
// MK_SOUND_MUTANTS when it is set. A row with witnesses is
// judged on them by TestFlowsMutants, not on the generator's sample;
// with MK_SOUND_MUTANTS_GEN=1 the ones MK_SOUND_MUTANTS names are run on
// the sample too (the per-field stratum's reach: with
// MK_SOUND_PROFILE=fields a generated program must show each needed).
func selectedMutants(t *testing.T) []Mutant {
	want := map[string]bool{}
	for _, id := range strings.Split(os.Getenv("MK_SOUND_MUTANTS"), ",") {
		if id = strings.TrimSpace(id); id != "" {
			want[id] = true
		}
	}
	var ms []Mutant
	for _, m := range readMutantTable(t) {
		gen := m.Witness == "" || os.Getenv("MK_SOUND_MUTANTS_GEN") == "1" && want[m.ID]
		if m.Pending == "" && gen && (len(want) == 0 || want[m.ID]) {
			ms = append(ms, m)
		}
	}
	return ms
}

// runMutantChild builds m's child binary (go test -c -overlay) and runs
// TestSound_MutantChild in it, returning its result line. With
// MK_SOUND_MUTANT_PREBUILT=1 an existing binary is run as it is, and the
// tree's borrow source is not read at all (it may have moved on since the
// build: TestSound_MutantBuild builds from a snapshot).
func runMutantChild(t *testing.T, root, dir string, m Mutant, base uint64) (*MutantResult, error) {
	bin := filepath.Join(dir, "corpus.test.exe")
	if _, err := os.Stat(bin); err != nil || os.Getenv("MK_SOUND_MUTANT_PREBUILT") != "1" {
		overlay, err := mutantOverlay(dir, root, m)
		if err != nil {
			return nil, err
		}
		if err := buildMutantChild(root, overlay, bin); err != nil {
			return nil, err
		}
	}
	run := mutantChildCmd(t, bin, root, m, base)
	var stdout bytes.Buffer
	run.Stdout = &stdout
	run.Stderr = &stdout
	err := run.Run()
	sc := bufio.NewScanner(&stdout)
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	for sc.Scan() {
		if line, ok := strings.CutPrefix(sc.Text(), "MUTANT-RESULT "); ok {
			var r MutantResult
			if jerr := json.Unmarshal([]byte(line), &r); jerr != nil {
				return nil, jerr
			}
			return &r, nil
		}
	}
	tail := stdout.String()
	if len(tail) > 2000 {
		tail = tail[len(tail)-2000:]
	}
	return nil, fmt.Errorf("no result line (err %v):\n%s", err, tail)
}

// mutantChildCmd is the child run of mutant m's binary bin. Its
// -test.timeout comes from the parent, as TestFlowsMutants' child's does
// (witnessChildCmd): the parent's remaining time less a minute, at most
// mutantChildCap, so a child never outlives the parent that reads it.
func mutantChildCmd(t *testing.T, bin, root string, m Mutant, base uint64) *exec.Cmd {
	run := exec.Command(bin, "-test.run", "^TestSound_MutantChild$", "-test.timeout", childTimeout(t, mutantChildCap(t)).String())
	run.Dir = filepath.Join(root, "internal", "corpus")
	run.Env = append(os.Environ(), "MK_SOUND=1", "MK_SOUND_MUTANT="+m.ID, "MK_SOUND_MUTANT_CLASS="+string(m.Class), fmt.Sprintf("MK_SOUND_BASE=%#x", base))
	return run
}

// mutantChildCap bounds a TestSound_Mutants child's run: witnessChildCap, or
// more when the child's own budget (MK_SOUND_SECONDS, which it inherits; 900
// s by default) and a quarter hour for checking std need it.
func mutantChildCap(t testing.TB) time.Duration {
	c := time.Duration(soundInt(t, "MK_SOUND_SECONDS", 900))*time.Second + 15*time.Minute
	return max(c, witnessChildCap)
}

// TestSound_MutantChildTimeout: a TestSound_Mutants child gets a nonzero
// -test.timeout no longer than the parent has left and than its cap, never
// the 0 that let a child outlive its killed parent, and the cap admits the
// child's own budget.
func TestSound_MutantChildTimeout(t *testing.T) {
	for _, secs := range []string{"", "3600"} {
		t.Setenv("MK_SOUND_SECONDS", secs) // "" is the default (soundEnv)
		run := mutantChildCmd(t, "corpus.test.exe", repoRoot(t), Mutant{ID: "M-x", Class: 'R'}, 1)
		i := slices.Index(run.Args, "-test.timeout")
		if i < 0 || i+1 >= len(run.Args) {
			t.Fatalf("no -test.timeout in %q", run.Args)
		}
		got, err := time.ParseDuration(run.Args[i+1])
		if err != nil {
			t.Fatal(err)
		}
		limit := mutantChildCap(t)
		if got <= 0 || got > limit {
			t.Errorf("MK_SOUND_SECONDS=%q: -test.timeout %v, want in (0, %v]", secs, got, limit)
		}
		if d, ok := t.Deadline(); ok && time.Now().Add(got).After(d) {
			t.Errorf("MK_SOUND_SECONDS=%q: -test.timeout %v outlives the parent's deadline %v", secs, got, d)
		}
		if want := time.Duration(soundInt(t, "MK_SOUND_SECONDS", 900)) * time.Second; limit < want {
			t.Errorf("MK_SOUND_SECONDS=%q: cap %v is under the child's budget %v", secs, limit, want)
		}
	}
}

// mutantWorkers is the worker count of a mutant run and of the unmutated
// sample: MK_SOUND_WORKERS when it is set above 0 (the child inherits the
// parent's environment), else soundWorkers().
func mutantWorkers(t testing.TB) int {
	if w := soundInt(t, "MK_SOUND_WORKERS", 0); w > 0 {
		return w
	}
	return soundWorkers()
}

// mutantProfile is the profile of the mutants' programs: MK_SOUND_PROFILE,
// full by default, vec for a rerun on the full language, whose shape S45 is
// what M-E0609-flow needs. The child inherits the parent's environment, so
// both sides agree.
func mutantProfile() Profile {
	if p, ok := ParseProfile(soundEnv("MK_SOUND_PROFILE", "full")); ok {
		return p
	}
	return ProfileFull
}
