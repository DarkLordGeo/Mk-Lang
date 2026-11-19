// Why: every borrow rule must be needed, and every witness program must keep
// its verdict; a rule no mutant can break is not shown to be needed.
// What: TestFlowsWitnesses, TestFlowsMutants with its child
// TestFlowsWitnessChild, and tests of the flip rule and child timeouts.
// How: mutants are built as child test binaries through go test -overlay and
// judge the witnesses their row names.
//
// Every file of tests/soundness/witness/ is a one-file unit judged by its
// verdict with the test prelude (TestFlowsWitnesses), and every row of the
// mutant tables that names witnesses must flip each of them to acceptance,
// with nothing emitted (TestFlowsMutants, MK_SOUND=1: one child test
// binary per mutant, built through go test -overlay, running
// TestFlowsWitnessChild).

package corpus

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"mk/internal/diag"
	"mk/internal/driver"
)

// witnessJudged is one witness's outcome: its status and the judge's
// reason, and the codes the compiler emitted.
type witnessJudged struct {
	File    string   `json:"file"`
	Status  string   `json:"status"`
	Reason  string   `json:"reason,omitempty"`
	Emitted []string `json:"emitted,omitempty"`
}

// minWitnesses is the number of witnesses tests/soundness/witness
// held when the count was first asserted: 169 files and 3 directories.
// Fewer is a lost witness.
const minWitnesses = 172

// witnessFiles lists tests/soundness/witness/*.mk and the directory
// witnesses (a package: pkg.mk and main.mk, for an unsafe module or a C
// source, CSR-unsafe-u1, CSR-extern-e1, FIX12-unsafe-u1) in name order.
// The directory fld3 holds the shallow-loan rule's one-file witnesses, which
// TestWitnesses judges (fieldLoansFiles). Every other entry of the
// directory is returned in others: no witness is dropped without a word.
func witnessFiles(t testing.TB) (ps, others []string) {
	t.Helper()
	dir := filepath.Join(soundRoot(t), "witness")
	es, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range es {
		p := filepath.Join(dir, e.Name())
		switch {
		case !e.IsDir() && strings.HasSuffix(e.Name(), ".mk"):
			ps = append(ps, p)
		case e.IsDir() && isFile(filepath.Join(p, "pkg.mk")) && isFile(filepath.Join(p, "main.mk")):
			ps = append(ps, p)
		case e.IsDir() && e.Name() == "fld3":
			// TestWitnesses'.
		default:
			others = append(others, e.Name())
		}
	}
	slices.Sort(ps)
	return ps, others
}

// isFile reports whether p names a regular file.
func isFile(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.Mode().IsRegular()
}

// runWitness reports whether witness p is a one-file witness whose first
// line is a run verdict (`// verdict: run N`), which only a run can judge.
func runWitness(t testing.TB, p string) bool {
	t.Helper()
	if fi, err := os.Stat(p); err != nil || fi.IsDir() {
		return false
	}
	text, err := readText(p)
	if err != nil {
		t.Fatal(err)
	}
	first, _, _ := strings.Cut(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	return strings.HasPrefix(first, "// verdict: run ")
}

// judgeWitness judges one witness file as a one-file unit with the test
// prelude on the session. A directory witness is judged by its main.mk,
// as a doc block is: in an unsafe module, where an extern needs no
// pkg.mk of its own, so its pkg.mk and C source are not read; `mk
// check` in the directory gives the same verdict.
func judgeWitness(t testing.TB, s *driver.CorpusSession, p string) witnessJudged {
	t.Helper()
	name := filepath.Base(p)
	src, file := p, name
	multi := false
	if fi, err := os.Stat(p); err == nil && fi.IsDir() {
		src, file = filepath.Join(p, "main.mk"), "main.mk"
		es, _ := os.ReadDir(p)
		multi = slices.ContainsFunc(es, func(e os.DirEntry) bool { return e.IsDir() })
	}
	var u Unit
	var err error
	if multi {
		// A package of several modules (a const of module k read by main,
		// the G756-const-* witnesses): every file, pkg.mk with them, as a
		// conformance directory unit is read (loadDirUnit).
		u, err = loadDirUnit(p, "tests/soundness/witness/"+name, "soundness", true)
	} else {
		var text string
		if text, err = readText(src); err != nil {
			t.Fatal(err)
		}
		u, err = makeUnit("tests/soundness/witness/"+name, "soundness", true, []UnitFile{{Path: file, Text: text}})
	}
	if err != nil {
		t.Fatal(err)
	}
	u.Name = fmt.Sprintf("%s#%d", u.Name, soundUnitN.Add(1))
	pre, err := Prelude(repoRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	// A witness is judged as a doc block is: the one module of a
	// package that lists it in unsafe_modules, so an extern call needs no
	// pkg.mk of its own (S2-t2-extern.mk).
	in := ToUnitInput(&u, pre, driver.UnitCheck)
	in.Design = true
	r := s.CheckUnit(in)
	j := JudgeUnit(&u, r, s.StdState(), driver.Available())
	// The judge's status as it gave it: "pass", "fail" or "pending". A
	// pending judgement (std not clean, a stage missing) is no flip
	// (witnessFlipped).
	out := witnessJudged{File: name, Status: strings.ToLower(j.Status.String()), Reason: j.Reason}
	for _, d := range r.Report.Diags {
		out.Emitted = append(out.Emitted, d.Diag.Code.String())
	}
	return out
}

// TestFlowsWitnesses: every witness meets its verdict on the compiler
// as built.
func TestFlowsWitnesses(t *testing.T) {
	files, others := witnessFiles(t)
	for _, o := range others {
		t.Errorf("tests/soundness/witness/%s is not a witness: neither a .mk file nor a directory with pkg.mk and main.mk", o)
	}
	if len(files) < minWitnesses {
		t.Fatalf("%d witnesses under tests/soundness/witness, want at least %d", len(files), minWitnesses)
	}
	t.Logf("%d witnesses judged", len(files))
	// The switch is on before the session is made, so a session made here
	// checks std with it; the count starts after.
	g := soundSplitUnion(t, true)
	s := soundSess(t)
	g.from()
	for _, p := range files {
		if runWitness(t, p) {
			// A run verdict (`// verdict: run 20`, D827-pr1.mk and
			// D827-pr1e.mk) is no check verdict: std's
			// TestWitnesses builds and runs it.
			t.Logf("%s: a run witness, judged by std's TestWitnesses", filepath.Base(p))
			continue
		}
		if j := judgeWitness(t, s, p); j.Status != "pass" {
			t.Errorf("%s: %s", j.File, j.Reason)
		}
	}
}

// TestFlowsWitnessChild is the child of TestFlowsMutants: with
// MK_FLOWS8_MUTANT set, it judges the witnesses MK_FLOWS8_WITNESS names on
// this (mutated) binary and prints one FLOWS8-RESULT line.
func TestFlowsWitnessChild(t *testing.T) {
	id := os.Getenv("MK_FLOWS8_MUTANT")
	if id == "" {
		t.Skip("MK_FLOWS8_MUTANT not set (the child of TestFlowsMutants)")
	}
	// A mutant that makes std itself refused leaves no witness to judge;
	// the parent reads this line (a precision row's mutant has then
	// rejected code the tree accepts).
	if s, err := soundSession(); err == nil && s.Through() < diag.PhaseFlow {
		fmt.Printf("FLOWS8-STD %s %s\n", id, strings.ReplaceAll(firstStdDiag(s), "\n", " "))
	}
	s := soundSess(t)
	var out []witnessJudged
	for _, w := range strings.Fields(os.Getenv("MK_FLOWS8_WITNESS")) {
		out = append(out, judgeWitness(t, s, filepath.Join(soundRoot(t), "witness", w)))
	}
	b, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("FLOWS8-RESULT %s %s\n", id, b)
}

// witnessFlipped reports whether a witness judged on a mutated binary was
// flipped to acceptance: it fails its verdict (a pending judgement is not a
// failure) and the compiler emitted nothing. A mutant that only moves the
// error to another line or code, or that a backstop catches as an internal
// error (E0000, E0001: mir.Validate's checks), has not shown that its rule is
// needed.
func witnessFlipped(j witnessJudged) bool {
	return j.Status == "fail" && len(j.Emitted) == 0
}

// TestFlowsMutants (MK_SOUND=1; MK_SOUND_MUTANTS narrows it as for
// TestSound_Mutants): each witness row's mutant, built into a child binary,
// flips every witness it names to acceptance (witnessFlipped), so each rule
// the row mutates is needed. A class P row names accepted witnesses, and
// its mutant must reject each (the shallow-loan rule's precision rows).
func TestFlowsMutants(t *testing.T) {
	if os.Getenv("MK_SOUND") != "1" {
		t.Skip("MK_SOUND=1 not set (builds one child test binary per mutant)")
	}
	want := map[string]bool{}
	for _, id := range strings.Split(os.Getenv("MK_SOUND_MUTANTS"), ",") {
		if id = strings.TrimSpace(id); id != "" {
			want[id] = true
		}
	}
	root := repoRoot(t)
	n := 0
	for _, m := range readMutantTable(t) {
		if m.Witness == "" || m.Pending != "" || len(want) > 0 && !want[m.ID] {
			continue
		}
		n++
		dir := t.TempDir()
		overlay, err := mutantOverlay(dir, root, m)
		if err != nil {
			t.Errorf("%s: %v", m.ID, err)
			continue
		}
		bin := filepath.Join(dir, "corpus.test.exe")
		if err := buildMutantChild(root, overlay, bin); err != nil {
			t.Errorf("%s: %v", m.ID, err)
			continue
		}
		run := witnessChildCmd(t, bin, root, m)
		var stdout bytes.Buffer
		run.Stdout, run.Stderr = &stdout, &stdout
		runErr := run.Run()
		var got []witnessJudged
		found := false
		stdRefused := ""
		sc := bufio.NewScanner(&stdout)
		sc.Buffer(make([]byte, 1<<20), 1<<24)
		for sc.Scan() {
			if rest, ok := strings.CutPrefix(sc.Text(), "FLOWS8-RESULT "+m.ID+" "); ok {
				if err := json.Unmarshal([]byte(rest), &got); err != nil {
					t.Fatal(err)
				}
				found = true
			}
			if rest, ok := strings.CutPrefix(sc.Text(), "FLOWS8-STD "+m.ID+" "); ok {
				stdRefused = rest
			}
		}
		if !found && stdRefused != "" && m.Class == 'P' {
			// The precision row's mutant refuses std's own code, which the
			// tree accepts: it has shown its rule is needed before any
			// witness is judged (M-D756-io and x509.SystemPool's Io).
			t.Logf("%s: the mutant refuses std, so its precision is shown there: %s", m.ID, stdRefused)
			continue
		}
		if !found {
			if stdRefused != "" {
				t.Errorf("%s: the mutant refuses std, so no witness was judged: %s", m.ID, stdRefused)
				continue
			}
			t.Errorf("%s: no result line (err %v):\n%s", m.ID, runErr, stdout.String())
			continue
		}
		for _, j := range got {
			flipped := witnessFlipped(j)
			if m.Class == 'P' {
				// A precision row: its witness is accepted on
				// the tree, and the mutant rejects it.
				flipped = j.Status == "fail" && len(j.Emitted) > 0
			}
			t.Logf("%s on %s: %s, emitted %v (%s)", m.ID, j.File, j.Status, j.Emitted, j.Reason)
			if !flipped {
				t.Errorf("%s does not flip %s: %s, emitted %v", m.ID, j.File, j.Status, j.Emitted)
			}
		}
	}
	if n == 0 {
		t.Fatal("no witness mutant selected")
	}
}

// witnessChildCap bounds a TestFlowsMutants child's run. A child judges the
// few witnesses its row names after checking std once, well within it.
const witnessChildCap = 30 * time.Minute

// witnessChildCmd is the child run of row m's binary bin. Its -test.timeout
// comes from the parent: the parent's remaining time less a minute, at most
// witnessChildCap, so a child never outlives the parent that would read it,
// even when the parent is killed at its own timeout and leaves the child
// behind.
func witnessChildCmd(t *testing.T, bin, root string, m Mutant) *exec.Cmd {
	run := exec.Command(bin, "-test.run", "^TestFlowsWitnessChild$", "-test.timeout", witnessChildTimeout(t).String())
	run.Dir = filepath.Join(root, "internal", "corpus")
	run.Env = append(os.Environ(), "MK_FLOWS8_MUTANT="+m.ID, "MK_FLOWS8_WITNESS="+m.Witness)
	return run
}

// witnessChildTimeout is the child's -test.timeout (witnessChildCmd).
func witnessChildTimeout(t *testing.T) time.Duration { return childTimeout(t, witnessChildCap) }

// childTimeout is a child test binary's -test.timeout: the parent's
// remaining time less a minute, at most limit (limit when the parent has no
// deadline), and at least a second.
func childTimeout(t *testing.T, limit time.Duration) time.Duration {
	d, ok := t.Deadline()
	if !ok {
		return limit
	}
	left := time.Until(d) - time.Minute
	switch {
	case left > limit:
		return limit
	case left < time.Second:
		return time.Second
	}
	return left
}

// TestFlowsWitnessChildTimeout: a mutant's child gets a nonzero
// -test.timeout no longer than the parent has left (and the cap), never the
// 0 that left orphans running after their parent was killed.
func TestFlowsWitnessChildTimeout(t *testing.T) {
	run := witnessChildCmd(t, "corpus.test.exe", repoRoot(t), Mutant{ID: "M-x", Witness: "w.mk"})
	i := slices.Index(run.Args, "-test.timeout")
	if i < 0 || i+1 >= len(run.Args) {
		t.Fatalf("no -test.timeout in %q", run.Args)
	}
	got, err := time.ParseDuration(run.Args[i+1])
	if err != nil {
		t.Fatal(err)
	}
	if got <= 0 || got > witnessChildCap {
		t.Errorf("-test.timeout %v, want in (0, %v]", got, witnessChildCap)
	}
	if d, ok := t.Deadline(); ok && time.Now().Add(got).After(d) {
		t.Errorf("-test.timeout %v outlives the parent's deadline %v", got, d)
	}
}

// TestFlowsFlipIsAcceptance: only an acceptance counts as a mutant's
// flip; a witness that still emits a diagnostic, at another line or with
// another code, or an internal error, does not, and
// neither does a judgement left pending.
func TestFlowsFlipIsAcceptance(t *testing.T) {
	cases := []struct {
		name string
		j    witnessJudged
		want bool
	}{
		{"accepted: nothing emitted", witnessJudged{Status: "fail"}, true},
		{"the error moved to another line", witnessJudged{Status: "fail", Emitted: []string{"E0505"}}, false},
		{"another code", witnessJudged{Status: "fail", Emitted: []string{"E0602"}}, false},
		{"an internal error", witnessJudged{Status: "fail", Emitted: []string{"E0000"}}, false},
		{"the verdict still met", witnessJudged{Status: "pass", Emitted: []string{"E0505"}}, false},
		{"pending: nothing emitted", witnessJudged{Status: "pending", Reason: "needs StageFlow"}, false},
		{"pending: std not clean", witnessJudged{Status: "pending", Reason: "std: judged through types only"}, false},
	}
	for _, c := range cases {
		if got := witnessFlipped(c.j); got != c.want {
			t.Errorf("%s: flipped = %v, want %v", c.name, got, c.want)
		}
	}
}
