// Why: under the gate a mismatch or a blocked build must fail, never pass as
// a log line or a skip.
// What: the TestCORPUSREV*_ tests and their child sides
// TestCheckProgramCaseLeaf and TestLayoutEvalLeaf.
// How: each leaf runs in a child process of this test binary, so the parent
// can watch it fail.
//
// Under MK_GATE=1 checkProgramCase fails a leaf of a program strictProgram
// does not name (wc, report, copy, echo) when its output differs, and
// fails, rather than skips, a leaf whose build stops on a std module "not
// provided yet". A plain run still logs the first and skips the second.
// TestDiffLayout's evaluator half fails, rather than skips, under MK_GATE=1
// when std has errors of its own.
//
// A Go test cannot watch its own subtest fail without failing, so each leaf
// runs in a child process of this test binary (TestCheckProgramCaseLeaf,
// which does nothing unless MK_CORPUS_LEAF is set), and the parent reads the
// child's verdict. The leaves are host-target builds only: no WSL.

package corpus

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"mk/internal/driver"
)

// leafJob is one checkProgramCase call for the child: the repository root,
// an optional std directory, the case and where to run it.
type leafJob struct {
	Root, Std       string
	Case            ProgramCase
	Target, Profile string
}

// TestCheckProgramCaseLeaf is the child's side: with MK_CORPUS_LEAF set to a
// leafJob, it runs that one leaf through checkProgramCase.
func TestCheckProgramCaseLeaf(t *testing.T) {
	spec := os.Getenv("MK_CORPUS_LEAF")
	if spec == "" {
		return
	}
	var job leafJob
	if err := json.Unmarshal([]byte(spec), &job); err != nil {
		t.Fatal(err)
	}
	tools := needLLVM(t)
	r := NewRunner(job.Root, driver.Config{Tools: tools, CacheDir: t.TempDir(), StdDir: job.Std}, t.TempDir())
	checkProgramCase(t, r, job.Case, job.Target, job.Profile)
}

// runLeaf runs job in a child of this test binary through
// TestCheckProgramCaseLeaf, with MK_GATE=1 when gate, and returns the
// child's verdict (PASS, FAIL or SKIP) and output.
func runLeaf(t *testing.T, job leafJob, gate bool) (string, string) {
	t.Helper()
	return runChild(t, "TestCheckProgramCaseLeaf", "MK_CORPUS_LEAF", job, gate)
}

// runChild runs the one test name of this test binary in a child process,
// with env set to job as JSON and MK_GATE=1 when gate, and returns the
// child's verdict for that test and its output.
func runChild(t *testing.T, name, env string, job any, gate bool) (string, string) {
	t.Helper()
	spec, err := json.Marshal(job)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^"+name+"$", "-test.v", "-test.count=1")
	var vars []string
	for _, e := range os.Environ() {
		if !strings.HasPrefix(e, "MK_GATE=") && !strings.HasPrefix(e, env+"=") {
			vars = append(vars, e)
		}
	}
	vars = append(vars, env+"="+string(spec))
	if gate {
		vars = append(vars, "MK_GATE=1")
	}
	cmd.Env = vars
	out, err := cmd.CombinedOutput()
	var ee *exec.ExitError
	if err != nil && !errors.As(err, &ee) {
		t.Fatalf("child test binary: %v", err)
	}
	m := regexp.MustCompile(`(?m)^--- (PASS|FAIL|SKIP): ` + name + `\b`).FindStringSubmatch(string(out))
	if m == nil {
		t.Fatalf("child printed no verdict for %s:\n%s", name, out)
	}
	return m[1], string(out)
}

// wcRoot is a repository root holding this repository's
// tests/programs/wc.mk as tests/programs/<name>.mk, and wc.in, and the case
// that runs it with an expected stdout that differs from what wc prints.
func wcRoot(t *testing.T, name string) (string, ProgramCase) {
	t.Helper()
	root := repoRoot(t)
	dir := t.TempDir()
	programs := filepath.Join(dir, filepath.FromSlash(ProgramsDir))
	if err := os.MkdirAll(programs, 0o755); err != nil {
		t.Fatal(err)
	}
	for from, to := range map[string]string{"wc.mk": name + ".mk", "wc.in": "wc.in"} {
		if err := copyFile(filepath.Join(root, ProgramsDir, from), filepath.Join(programs, to), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(programs, "wrong.out"), []byte("not what wc prints\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := ProgramCase{
		Program: ProgramsDir + "/" + name + ".mk", Name: "wrong", Args: []string{"wc.in"},
		Files: []string{ProgramsDir + "/wc.in"}, Stdout: ProgramsDir + "/wrong.out",
		Timeout: DefaultCaseTimeout, At: ProgramsDir + "/wrong.cases:1",
	}
	return dir, c
}

// The strictness rule itself: under the gate every program is strict; in a
// plain run exactly those strictProgram names, and the four gate programs
// (wc, report, copy, echo) are among them.
func TestGateJudgesEveryProgramStrictly(t *testing.T) {
	root := repoRoot(t)
	cases, err := LoadCases(root)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, c := range cases {
		seen[c.Program] = true
		if !judgedStrictly(c.Program, true) {
			t.Errorf("%s: not judged strictly under MK_GATE=1", c.Program)
		}
		if judgedStrictly(c.Program, false) != strictProgram(c.Program) {
			t.Errorf("%s: a plain run's strictness is not strictProgram's", c.Program)
		}
	}
	for _, p := range []string{"wc.mk", "report.mk", "copy.mk", "echo.mk"} {
		if !seen[ProgramsDir+"/"+p] {
			t.Errorf("no .cases file runs %s/%s, a gate program", ProgramsDir, p)
		}
	}
}

// The unit form: wc's leaf with an expected stdout that differs
// from its run must FAIL under MK_GATE=1, and in a plain run too now that
// wc is strict. The same program under a name strictProgram does not list
// (a program not yet promoted) is logged in a plain run, and must still
// FAIL under MK_GATE=1.
func TestGateFailsLoggedProgramMismatch(t *testing.T) {
	needLLVM(t)
	for _, name := range []string{"wc", "wcunlisted"} {
		t.Run(name, func(t *testing.T) {
			dir, c := wcRoot(t, name)
			job := leafJob{Root: dir, Case: c, Target: driver.HostTarget().Name, Profile: "dev"}
			verdict, out := runLeaf(t, job, true)
			if verdict != "FAIL" || !strings.Contains(out, "stdout ") {
				t.Errorf("MK_GATE=1: %s with a wrong .out gave %s, want FAIL on stdout:\n%s", c.Program, verdict, out)
			}
			want := "PASS"
			if strictProgram(c.Program) {
				want = "FAIL"
			}
			if verdict, out := runLeaf(t, job, false); verdict != want || !strings.Contains(out, "stdout ") {
				t.Errorf("plain run: %s with a wrong .out gave %s, want %s naming stdout:\n%s", c.Program, verdict, want, out)
			}
		})
	}
	if !strictProgram(ProgramsDir+"/wc.mk") || strictProgram(ProgramsDir+"/wcunlisted.mk") {
		t.Error("strictProgram must list wc.mk and not wcunlisted.mk")
	}
}

// With a std that lacks std/fs, wc's build stops on E0202
// "does not provide it yet". The leaf must FAIL under MK_GATE=1 and SKIP in
// a plain run.
func TestGateFailsBlockedBuild(t *testing.T) {
	tools := needLLVM(t)
	std := digitsStd(t, nil)
	if err := os.RemoveAll(filepath.Join(std, "fs")); err != nil {
		t.Fatal(err)
	}
	dir, c := wcRoot(t, "wc")
	host := driver.HostTarget().Name
	r := NewRunner(dir, driver.Config{Tools: tools, CacheDir: t.TempDir(), StdDir: std}, t.TempDir())
	_, err := r.Build(c.Program, host, "dev")
	if blockedBuild(err) == "" {
		t.Fatalf("wc over a std without std/fs: %v; want only E0202 \"does not provide it yet\"", err)
	}
	job := leafJob{Root: dir, Std: std, Case: c, Target: host, Profile: "dev"}
	if verdict, out := runLeaf(t, job, true); verdict != "FAIL" || !strings.Contains(out, "BLOCKED") {
		t.Errorf("MK_GATE=1: gave %s, want FAIL naming BLOCKED:\n%s", verdict, out)
	}
	if verdict, out := runLeaf(t, job, false); verdict != "SKIP" {
		t.Errorf("plain run: gave %s, want SKIP:\n%s", verdict, out)
	}
}

// layoutJob is one layoutEval call for the child: the target and the std
// directory.
type layoutJob struct {
	Target, Std string
}

// TestLayoutEvalLeaf is the child's side of
// TestGateFailsLayoutOverBrokenStd: with
// MK_CORPUS_LAYOUT_LEAF set to a layoutJob, it runs TestDiffLayout's
// evaluator half for that target over that std.
func TestLayoutEvalLeaf(t *testing.T) {
	spec := os.Getenv("MK_CORPUS_LAYOUT_LEAF")
	if spec == "" {
		return
	}
	var job layoutJob
	if err := json.Unmarshal([]byte(spec), &job); err != nil {
		t.Fatal(err)
	}
	tgt, _, ok := driver.TargetNamed(job.Target)
	if !ok {
		t.Fatalf("no target %s", job.Target)
	}
	root := repoRoot(t)
	src, err := os.ReadFile(filepath.Join(root, DiffDir, "layout.mk"))
	if err != nil {
		t.Fatal(err)
	}
	golden := DiffDir + "/layout." + job.Target + ".golden"
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(golden)))
	if err != nil {
		t.Fatal(err)
	}
	rows, err := ParseLayoutGolden(golden, data)
	if err != nil {
		t.Fatal(err)
	}
	layoutEval(t, src, rows, tgt, job.Std, golden)
}

// With a std that has an error of its own on x86_64-windows
// (one E0301 in an added std.core file), TestDiffLayout's evaluator half
// must FAIL under MK_GATE=1, as BLOCKED on that E0301, and SKIP in a plain
// run.
func TestGateFailsLayoutOverBrokenStd(t *testing.T) {
	std := digitsStd(t, map[string]string{"core/zz_corpusbroken.mk": "ZzCorpusBroken(): i32 {\n    return true\n}\n"})
	job := layoutJob{Target: "x86_64-windows", Std: std}
	if verdict, out := runChild(t, "TestLayoutEvalLeaf", "MK_CORPUS_LAYOUT_LEAF", job, true); verdict != "FAIL" || !strings.Contains(out, "BLOCKED") || !strings.Contains(out, "E0301") {
		t.Errorf("MK_GATE=1 over a broken std: %s, want FAIL naming BLOCKED and the E0301:\n%s", verdict, out)
	}
	if verdict, out := runChild(t, "TestLayoutEvalLeaf", "MK_CORPUS_LAYOUT_LEAF", job, false); verdict != "SKIP" {
		t.Errorf("plain run over a broken std: %s, want SKIP:\n%s", verdict, out)
	}
}
