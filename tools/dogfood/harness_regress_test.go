// Why: the harness's coverage, budgets, state and concurrency rules fail in
// ways a passing run does not show; these tests pin each without a toolchain.
// What: regression tests of the harness, one or more per fixed defect, and the fakes they
// share (dogfoodFiles, the DFH_FAKE=1 stand-in).
// How: the test binary stands in for mk (build copies itself, version, query
// uses, profile merge) and for the dogfood's program (echo, sleep, a
// working-directory check, a profile table at exit), selected by DFH_FAKE=1.
// No test starts wsl.exe: the Linux batch runs through a stub runner.

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	if os.Getenv("DFH_FAKE") == "1" {
		os.Exit(fakeMain(os.Args[1:]))
	}
	os.Exit(m.Run())
}

// fakeMain is the test binary as mk or as a dogfood's program.
func fakeMain(args []string) int {
	self, _ := os.Executable()
	verb := ""
	if len(args) > 0 {
		verb = args[0]
	}
	switch {
	case verb == "-kernel":
		return fakeQemu(args) // serial_test.go
	case verb == "version":
		id := os.Getenv("DFH_FAKE_ID")
		if id == "" {
			id = "fake0001"
		}
		fmt.Printf("mk 1.1.0 commit test\nmk build %s (test)\n", id)
		return 0
	case verb == "build":
		if d := os.Getenv("DFH_PTYRUN_BUILD"); d != "" && filepath.Base(args[len(args)-1]) == "ptyrun" {
			// ptyrun's build, made slow.
			sleep, _ := time.ParseDuration(d)
			time.Sleep(sleep)
		}
		for i, a := range args {
			if a == "-o" && i+1 < len(args) {
				b, err := os.ReadFile(self)
				if err == nil {
					err = os.WriteFile(args[i+1], b, 0o755)
				}
				if err != nil {
					fmt.Fprintln(os.Stderr, err)
					return 1
				}
			}
		}
		return 0
	case verb == "query":
		u := os.Getenv("DFH_USES")
		if u == "" {
			u = `{"query":"uses","uses":[]}`
		}
		fmt.Println(u)
		return 0
	case verb == "profile" && len(args) > 3 && args[1] == "merge" && args[2] == "-o":
		sum := map[string]uint64{}
		for _, f := range args[4:] {
			b, err := os.ReadFile(f)
			if err != nil {
				return 1
			}
			recs, err := readProfile(b)
			if err != nil {
				return 1
			}
			for _, r := range recs {
				sum[r.Key] += r.Entry
			}
		}
		var recs []Record
		for k, v := range sum {
			recs = append(recs, Record{k, v})
		}
		sort.Slice(recs, func(i, j int) bool { return recs[i].Key < recs[j].Key })
		if os.WriteFile(args[3], mkprof(recs...), 0o644) != nil {
			return 1
		}
		return 0
	}
	// The program: its bin is its file name without .exe.
	bin := strings.TrimSuffix(filepath.Base(self), ".exe")
	switch verb {
	case "", "--help":
		fmt.Println("ok")
	case "echo":
		fmt.Println(strings.Join(args[1:], " "))
	case "sleep":
		d, _ := time.ParseDuration(args[1])
		time.Sleep(d)
		fmt.Println("slept")
	case "work":
		// Writes a file into its working directory, waits, and reads it back.
		mark := fmt.Sprintf("%d %d", os.Getpid(), time.Now().UnixNano())
		if err := os.WriteFile("f", []byte(mark), 0o644); err != nil {
			fmt.Println("write:", err)
			return 1
		}
		time.Sleep(400 * time.Millisecond)
		b, err := os.ReadFile("f")
		switch {
		case err != nil:
			fmt.Println("gone")
		case string(b) != mark:
			fmt.Println("changed")
		default:
			fmt.Println("same")
		}
	case "--limit":
		// As ptyrun: the program exited 0.
		fmt.Println("exit 0")
		return 0
	case "panic":
		fmt.Fprintln(os.Stderr, "panic: on purpose")
		return 101 // a panicking process writes no profile
	}
	// The profile table std/rt writes at exit: DFH_KEYS_<bin>'s keys, the
	// case's runs entered (a --help run enters them too; cover must not
	// count it).
	// DFH_CASEKEYS_<case> adds keys for one case's processes, and
	// DFH_PROFMODE_<case> spoils its profiles as a process killed while it
	// writes one does: "empty" writes 0 bytes in place of
	// the profile, "cut" writes a second profile cut in half.
	if dir := os.Getenv("MK_PROFILE_DIR"); dir != "" && os.Getenv("DFH_NOPROF_"+bin) == "" {
		cs := filepath.Base(dir)
		var recs []Record
		for _, k := range strings.Split(os.Getenv("DFH_KEYS_"+bin)+","+os.Getenv("DFH_CASEKEYS_"+cs), ",") {
			if k != "" {
				recs = append(recs, Record{k, 1})
			}
		}
		data := mkprof(recs...)
		switch os.Getenv("DFH_PROFMODE_" + cs) {
		case "empty":
			data = nil
		case "cut":
			os.WriteFile(filepath.Join(dir, fmt.Sprintf("%s-%d9.mkprof", bin, os.Getpid())), data[:len(data)/2], 0o644)
		}
		os.WriteFile(filepath.Join(dir, fmt.Sprintf("%s-%d.mkprof", bin, os.Getpid())), data, 0o644)
	}
	return 0
}

func selfExe(t *testing.T) string {
	t.Helper()
	p, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func hostTarget() string {
	if runtime.GOOS == "windows" {
		return targetWindows
	}
	return targetLinux
}

func otherHosted() string {
	if hostTarget() == targetWindows {
		return targetLinux
	}
	return targetWindows
}

// testHarness is a harness on work for the host target in dev, with the
// test binary as mk.
func testHarness(t *testing.T, work string) *harness {
	t.Helper()
	h := newHarness(io.Discard)
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	h.root, h.work, h.mk, h.mkID = root, work, selfExe(t), "fake0001"
	h.targets, h.profiles = []string{hostTarget()}, []string{"dev"}
	h.rep = &Report{Started: time.Now()}
	if err := h.newScratch(); err != nil {
		t.Fatal(err)
	}
	return h
}

const dfhPkg = `const Package = pkg.Package(
    name: "x",
    bins: [%s],
    deps: [],
    c_sources: [],
    c_imports: [],
    asm_sources: [],
    targets: [],
    unsafe_modules: [],
    links: [],
)
`

// dogfoodFiles is a dogfood with bins and enough lines for the line-count
// minimum.
func dogfoodFiles(bins ...string) map[string]string {
	var bs []string
	for _, b := range bins {
		bs = append(bs, fmt.Sprintf(`pkg.Bin(name: %q, root: "src", link_script: "", cpu: "", subsystem: "console")`, b))
	}
	var pad strings.Builder
	for i := range 800 {
		fmt.Fprintf(&pad, "const Pad%d: int = %d\n", i, i)
	}
	return map[string]string{"pkg.mk": fmt.Sprintf(dfhPkg, strings.Join(bs, ", ")), "src/pad.mk": pad.String()}
}

func fakeEnv(t *testing.T, kv ...string) {
	t.Setenv("DFH_FAKE", "1")
	for i := 0; i+1 < len(kv); i += 2 {
		t.Setenv(kv[i], kv[i+1])
	}
}

// coverRow is the verdict cell of a COVERAGE.md row.
func coverRow(t *testing.T, cov, line string) string {
	t.Helper()
	for _, l := range strings.Split(cov, "\n") {
		if strings.HasPrefix(l, "| `"+line+"` |") {
			cells := strings.Split(l, " | ")
			return strings.TrimSuffix(cells[len(cells)-1], " |")
		}
	}
	t.Fatalf("COVERAGE.md has no row for %s", line)
	return ""
}

// TestUnrunBinIsNotInline: a function only
// a bin no case runs calls, and one only a test block calls, are gaps, not
// "compiled inline"; a function no bin's table holds stays inline; and
// when a bin's table cannot be read at all, nothing of its dogfood counts
// as inline.
func TestUnrunBinIsNotInline(t *testing.T) {
	tmp := t.TempDir()
	dogs := filepath.Join(tmp, "Dogfoods")
	files := dogfoodFiles("two", "client")
	files["src/main.mk"] = "import std.math\n\nexport Main(sys: &System) {\n    _ = \"x\".Trim()\n}\n\ntest \"min\" {\n    Assert(math.Min(1, 2) == 1)\n}\n"
	files["cases/a.out"] = "ok\n"
	write(t, filepath.Join(dogs, "two"), files)
	uses := `{"query":"uses","uses":[
		{"item":"std.core.str:Trim","sites":["src/main.mk:4:13"]},
		{"item":"std.core.str:Len","sites":["src/main.mk:4:5"]},
		{"item":"std.hash.Crc32","sites":["client/main.mk:2:5"]},
		{"item":"std.math.Min","sites":["src/main.mk:8:17"]}]}`
	fakeEnv(t, "DFH_USES", strings.ReplaceAll(uses, "\n", ""),
		"DFH_KEYS_two", "mk.std.core.str:Trim,mk.std.math.Max.00ff",
		"DFH_KEYS_client", "mk.std.hash.Crc32")
	cover := func() (string, string) {
		cov := filepath.Join(tmp, "COVERAGE.md")
		var out bytes.Buffer
		code := run([]string{"cover", "-dir", dogs, "-work", filepath.Join(tmp, "work"), "-mk", selfExe(t), "-target", hostTarget(), "-coverage", cov}, &out, &out)
		if code != 1 {
			t.Fatalf("cover: exit %d, want 1 (gaps)\n%s", code, out.String())
		}
		b, err := os.ReadFile(cov)
		if err != nil {
			t.Fatal(err)
		}
		return string(b), out.String()
	}
	cov, log := cover()
	for line, want := range map[string]string{
		"str.Trim(self): str":                     "covered (executed)",
		"hash.Crc32(data: []u8): u32":             "gap: referenced, not executed by any case",
		"math.Min[T: Ord + Plain](a: T, b: T): T": "gap: referenced, not executed by any case",
		"str.Len(self): int":                      "covered (referenced; no instrumented body: compiled inline)",
	} {
		if got := coverRow(t, cov, line); got != want {
			t.Errorf("%s: %q, want %q\n%s", line, got, want, log)
		}
	}
	// client's --help writes no table: its dogfood gives no inline credit.
	t.Setenv("DFH_NOPROF_client", "1")
	cov, log = cover()
	if got := coverRow(t, cov, "str.Len(self): int"); got != "gap: referenced, not executed by any case" {
		t.Errorf("str.Len with client's table unknown: %q\n%s", got, log)
	}
	if !strings.Contains(log, "bin client wrote no profile table") {
		t.Errorf("no note names client:\n%s", log)
	}
}

// TestTestSite: a site in a test block or a tests_ file is test code.
func TestTestSite(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, map[string]string{
		"src/main.mk":     "a\n\ntest \"t\" {\n    b\n}\nc\n",
		"src/tests_x.mk":  "d\n",
		"lib/src/main.mk": "e\n",
	})
	is := testSite(dir)
	for site, want := range map[string]bool{
		"src/main.mk:1:1": false, "src/main.mk:3:1": true, "src/main.mk:4:5": true, "src/main.mk:5:1": true,
		"src/main.mk:6:1": false, "src/tests_x.mk:1:1": true, "lib/src/main.mk:1:1": false, "nope.mk:1:1": false, "garbage": false,
	} {
		if is(site) != want {
			t.Errorf("testSite(%q) = %v, want %v", site, !want, want)
		}
	}
}

// TestRenamedImport: a renamed import, `import h = std.hash`, reaches
// std.hash, so the digest follows it.
func TestRenamedImport(t *testing.T) {
	dir := t.TempDir()
	files := dogfoodFiles("x")
	files["src/main.mk"] = "import h = std.hash\nimport  t=std.text.width\nimport std.fs\n// import std.net\n"
	write(t, dir, files)
	d, err := loadDogfood(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"std.fs", "std.hash", "std.text.width"}
	if strings.Join(d.Imports, " ") != strings.Join(want, " ") {
		t.Errorf("imports %q, want %q", d.Imports, want)
	}
}

// TestConcurrentRunsKeepTheirOwnFiles (several harnesses may run at
// once): two runs of one dogfood on one -work never share a working
// directory, an executable or a job file.
func TestConcurrentRunsKeepTheirOwnFiles(t *testing.T) {
	work := t.TempDir()
	dir := filepath.Join(t.TempDir(), "x")
	files := dogfoodFiles("x")
	files["cases/w.args"] = "work\n"
	files["cases/w.out"] = "same\n"
	write(t, dir, files)
	fakeEnv(t)
	d, err := loadDogfood(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	cases, err := loadCases(filepath.Join(dir, "cases"))
	if err != nil {
		t.Fatal(err)
	}
	hs := []*harness{testHarness(t, work), testHarness(t, work)}
	rrs := make([]*RunReport, 2)
	var wg sync.WaitGroup
	for i, h := range hs {
		rrs[i] = &RunReport{Target: hostTarget(), Profile: "dev"}
		if !h.buildRun(d, rrs[i], false) {
			t.Fatalf("build: %+v", rrs[i])
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			h.runCases(d, cases, rrs[i])
		}()
	}
	wg.Wait()
	for i, rr := range rrs {
		if c := rr.Cases[0]; c.Status != "pass" {
			t.Errorf("harness %d: %s %q", i, c.Status, c.Diffs)
		}
	}
	if rrs[0].exes["x"] == rrs[1].exes["x"] {
		t.Errorf("both runs built %s", rrs[0].exes["x"])
	}
	in0, out0 := hs[0].jobFiles(d)
	in1, out1 := hs[1].jobFiles(d)
	if in0 == in1 || out0 == out1 {
		t.Errorf("both runs use the job files %s, %s", in0, out0)
	}
}

// TestNoCaseRunFails: a dogfood with no
// case, and a target where every case is skipped, fail the run.
func TestNoCaseRunFails(t *testing.T) {
	tmp := t.TempDir()
	dogs := filepath.Join(tmp, "Dogfoods")
	write(t, filepath.Join(dogs, "nocases"), dogfoodFiles("nocases"))
	skipped := dogfoodFiles("skipped")
	skipped["cases/x.targets"] = otherHosted() + "\n"
	write(t, filepath.Join(dogs, "skipped"), skipped)
	fakeEnv(t)
	var out bytes.Buffer
	work := filepath.Join(tmp, "work")
	if code := run([]string{"run", "-dir", dogs, "-work", work, "-mk", selfExe(t), "-target", hostTarget(), "-profile", "dev"}, &out, &out); code != 1 {
		t.Fatalf("run: exit %d, want 1\n%s", code, out.String())
	}
	b, err := os.ReadFile(filepath.Join(work, "dogfood-last.json"))
	if err != nil {
		t.Fatal(err)
	}
	var rep Report
	if err := json.Unmarshal(b, &rep); err != nil {
		t.Fatal(err)
	}
	for _, d := range rep.Dogfoods {
		r := d.Runs[0]
		if r.Status != "fail" || r.Pass != 0 {
			t.Errorf("%s: %s (pass %d) %s", d.Name, r.Status, r.Pass, r.Reason)
		}
		if want := map[string]string{"nocases": "no case in cases/", "skipped": "every case is skipped"}[d.Name]; !strings.Contains(r.Reason, want) {
			t.Errorf("%s: reason %q, want %q", d.Name, r.Reason, want)
		}
	}
}

// TestLinuxBatchFailureLeavesNoCasePassed: when the batch under the
// WSL lock does not run (an error, WSL not answering, a wrong result
// count), no Linux case is passed and the cases say "not verified".
func TestLinuxBatchFailureLeavesNoCasePassed(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "x")
	files := dogfoodFiles("x")
	files["cases/a.out"] = "ok\n"
	files["cases/b.args"] = "echo\nhi\n"
	files["cases/b.out"] = "hi\n"
	write(t, dir, files)
	d, err := loadDogfood(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	cases, err := loadCases(filepath.Join(dir, "cases"))
	if err != nil {
		t.Fatal(err)
	}
	okResults := &JobResults{Results: []JobResult{{Stdout: []byte("ok\n")}, {Stdout: []byte("hi\n")}}}
	for _, tc := range []struct {
		name   string
		res    *JobResults
		err    error
		passed bool
	}{
		{"runner error", nil, errors.New("exec-jobs under the WSL lock: exit status 1 (no results)"), false},
		{"WSL did not answer", &JobResults{Probe: "wsl.exe -e true did not answer within 30s"}, nil, false},
		{"wrong count", &JobResults{Results: []JobResult{{Stdout: []byte("ok\n")}}}, nil, false},
		{"ran", okResults, nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := testHarness(t, t.TempDir())
			h.pwsh = "pwsh-stub"
			var got []Job
			h.lockedRunner = func(_ *Dogfood, jobs []Job) (*JobResults, error) {
				got = jobs
				return tc.res, tc.err
			}
			rr := &RunReport{Target: targetLinux, Profile: "dev", exes: map[string]string{"x": `V:\w\x`}}
			h.runLinux(d, cases, []*RunReport{rr})
			h.finishRun(d, rr)
			if len(got) != 2 || got[0].Case == got[1].Case || got[0].Suite != got[1].Suite || got[0].CaseSeconds != 30 || got[0].SuiteSeconds != 180 {
				t.Errorf("jobs %+v", got)
			}
			if tc.passed {
				if rr.Status != "pass" || rr.Pass != 2 {
					t.Errorf("%s (pass %d) %s", rr.Status, rr.Pass, rr.Reason)
				}
				return
			}
			if rr.Status != "fail" || rr.Pass != 0 || !strings.HasPrefix(rr.Reason, "not verified") {
				t.Errorf("run %s (pass %d) %q", rr.Status, rr.Pass, rr.Reason)
			}
			for _, c := range rr.Cases {
				if c.Status != "fail" || !strings.HasPrefix(c.Reason, "not verified") {
					t.Errorf("case %s: %s %q", c.Name, c.Status, c.Reason)
				}
			}
		})
	}
}

// TestCaseBudgetSpansSeeds: a case's 30
// seconds cover all its seeds; a suite's 3 minutes cover all its cases.
func TestCaseBudgetSpansSeeds(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "x")
	files := dogfoodFiles("x")
	files["cases/s.args"] = "sleep\n250ms\n"
	files["cases/s.sim"] = "seeds 1..6\n"
	files["cases/s.out"] = "slept\n"
	write(t, dir, files)
	fakeEnv(t)
	d, _ := loadDogfood(dir, nil)
	cases, err := loadCases(filepath.Join(dir, "cases"))
	if err != nil {
		t.Fatal(err)
	}
	h := testHarness(t, t.TempDir())
	h.caseLimit = time.Second
	rr := &RunReport{Target: hostTarget(), Profile: "dev"}
	h.buildRun(d, rr, false)
	// Only the load-proof direction is asserted: no wall-clock upper bound
	// outside MK_TIMING=1.
	h.runCases(d, cases, rr)
	if c := rr.Cases[0]; c.Status != "fail" || !strings.Contains(strings.Join(c.Diffs, "; "), "time limit") {
		t.Errorf("six 250 ms seeds under a 1 s budget: %s %q", c.Status, c.Diffs)
	}
}

func TestSuiteBudget(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "x")
	files := dogfoodFiles("x")
	for _, c := range []string{"c1", "c2", "c3", "c4"} {
		files["cases/"+c+".args"] = "sleep\n400ms\n"
		files["cases/"+c+".out"] = "slept\n"
	}
	write(t, dir, files)
	fakeEnv(t)
	d, _ := loadDogfood(dir, nil)
	cases, _ := loadCases(filepath.Join(dir, "cases"))
	h := testHarness(t, t.TempDir())
	h.suiteLimit = time.Second
	rr := &RunReport{Target: hostTarget(), Profile: "dev"}
	h.buildRun(d, rr, false)
	h.runCases(d, cases, rr)
	h.finishRun(d, rr)
	if rr.Status != "fail" || rr.Cases[3].Status != "fail" {
		t.Errorf("four 400 ms cases under a 1 s suite: %s %s; %+v", rr.Status, rr.Reason, rr.Cases[3])
	}
}

// TestLinuxJobBudgets: the exec-jobs child gives each job what is
// left of its case's budget and starts none once it is spent; a probe that
// fails runs nothing.
func TestLinuxJobBudgets(t *testing.T) {
	self := selfExe(t)
	job := Job{Argv: []string{self, "sleep", "400ms"}, Dir: t.TempDir(), Env: []string{"DFH_FAKE=1"}, Case: "c", CaseSeconds: 1, Suite: "s", SuiteSeconds: 60}
	res := runJobs([]Job{job, job, job}, func() string { return "" })
	if len(res.Results) != 3 || res.Probe != "" {
		t.Fatalf("results %+v", res)
	}
	if r := res.Results[2]; !strings.Contains(r.Err, "not run") && !r.TimedOut {
		t.Errorf("third 400 ms job under a 1 s case budget: %+v", r)
	}
	if res := runJobs([]Job{job}, func() string { return "no answer" }); res.Probe != "no answer" || len(res.Results) != 0 {
		t.Errorf("probe failed: %+v", res)
	}
}

// TestDogfoodMinimumLines: run and list fail a dogfood under 800 lines (400
// freestanding); tools/gate runs `run`.
func TestDogfoodMinimumLines(t *testing.T) {
	tmp := t.TempDir()
	dogs := filepath.Join(tmp, "Dogfoods")
	write(t, filepath.Join(dogs, "tiny"), map[string]string{
		"pkg.mk":      fmt.Sprintf(dfhPkg, `pkg.Bin(name: "tiny", root: "src", link_script: "", cpu: "", subsystem: "console")`),
		"src/main.mk": "export Main(sys: &System) {\n}\n",
		"cases/a.out": "ok\n",
	})
	fakeEnv(t)
	work := filepath.Join(tmp, "work")
	var out bytes.Buffer
	if code := run([]string{"list", "-dir", dogs}, &out, &out); code != 1 || !strings.Contains(out.String(), "too small: 2 lines of Mk, under the minimum of 800") {
		t.Errorf("list: exit %d\n%s", code, out.String())
	}
	out.Reset()
	if code := run([]string{"run", "-dir", dogs, "-work", work, "-mk", selfExe(t), "-target", hostTarget(), "-profile", "dev"}, &out, &out); code != 1 {
		t.Errorf("run: exit %d, want 1\n%s", code, out.String())
	}
	b, _ := os.ReadFile(filepath.Join(work, "dogfood-last.json"))
	if !strings.Contains(string(b), `"problems": [`) || !strings.Contains(string(b), `"status": "pass"`) {
		t.Errorf("report: the case passes and the size minimum fails:\n%s", b)
	}
	if h := newHarness(io.Discard); h.minFor(&Dogfood{Free: true}) != 400 || h.minFor(&Dogfood{}) != 800 {
		t.Error("the size minimums are not 800 and 400")
	}
}

// TestStateIsTheGatesAndKeysOnMk (-changed selects by the last
// gate's pass): an -only pass records nothing, a whole-folder pass does,
// and a different mk reselects the dogfood.
func TestStateIsTheGatesAndKeysOnMk(t *testing.T) {
	tmp := t.TempDir()
	dogs := filepath.Join(tmp, "Dogfoods")
	files := dogfoodFiles("hello")
	files["cases/a.out"] = "ok\n"
	write(t, filepath.Join(dogs, "hello"), files)
	fakeEnv(t)
	work := filepath.Join(tmp, "work")
	common := []string{"-dir", dogs, "-work", work, "-mk", selfExe(t), "-target", hostTarget(), "-profile", "dev"}
	runIt := func(args ...string) string {
		t.Helper()
		var out bytes.Buffer
		if code := run(append(append([]string{"run"}, args...), common...), &out, &out); code != 0 {
			t.Fatalf("run %q: exit %d\n%s", args, code, out.String())
		}
		return out.String()
	}
	runIt("-only", "hello")
	if out := runIt("-changed"); strings.Contains(out, "unchanged since") {
		t.Errorf("a dogfood's -only pass stood for the gate's:\n%s", out)
	}
	if out := runIt("-changed"); !strings.Contains(out, "unchanged since") {
		t.Errorf("a whole-folder pass was not recorded:\n%s", out)
	}
	t.Setenv("DFH_FAKE_ID", "fake0002")
	if out := runIt("-changed"); strings.Contains(out, "unchanged since") {
		t.Errorf("another mk did not reselect the dogfood:\n%s", out)
	}
}

// TestCompilerID: an mk built from the tree is identified by the
// compiler's Go sources: a change there changes it; a std file (mk embeds
// std, which each digest follows on its own), a test file or a commit
// does not.
func TestCompilerID(t *testing.T) {
	root := t.TempDir()
	write(t, root, map[string]string{
		"go.mod":               "module mk\n",
		"cmd/mk/main.go":       "package main\n",
		"internal/x/x.go":      "package x\n",
		"internal/x/x_test.go": "package x\n",
		"std/embed.go":         "package std\n",
		"std/core/a.mk":        "// a\n",
	})
	id := func() string {
		s, err := compilerID(root)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	base := id()
	write(t, root, map[string]string{"std/core/a.mk": "// b\n", "std/embed.go": "package std // b\n", "internal/x/x_test.go": "package x // b\n"})
	if id() != base {
		t.Error("a std or test file changed the compiler's identity")
	}
	write(t, root, map[string]string{"internal/x/x.go": "package x // b\n"})
	if id() == base {
		t.Error("a compiler source did not change the compiler's identity")
	}
}

// TestBenchBaselineLasts: the bench
// baseline is the best time kept in the state file, compared whether or
// not the machine lock is held, kept when a run skips the dogfood, and
// reset when the bench case's files change.
func TestBenchBaselineLasts(t *testing.T) {
	work := t.TempDir()
	dir := filepath.Join(t.TempDir(), "x")
	write(t, dir, map[string]string{"pkg.mk": "", "cases/bench.out": "a\n"})
	d := &Dogfood{Name: "x", Dir: dir}
	bench := func(secs float64) (*harness, *Bench) {
		h := testHarness(t, work)
		rr := &RunReport{Target: hostTarget(), Profile: "release", Cases: []*CaseReport{{Name: "bench", Status: "pass", Seconds: secs}}}
		h.finishRun(d, rr)
		if err := h.writeState(); err != nil {
			t.Fatal(err)
		}
		return h, h.rep.Bench[0]
	}
	if _, b := bench(0.1); b.Previous != 0 || b.Regression {
		t.Errorf("first: %+v", b)
	}
	h, b := bench(0.3)
	if b.Previous != 0.1 || !b.Regression || len(h.rep.Notes) != 1 || !strings.Contains(h.rep.Notes[0], "not under the machine lock") {
		t.Errorf("0.3 s after 0.1 s: %+v %q", b, h.rep.Notes)
	}
	// A run that does not run x (-changed skipped it) keeps the baseline.
	if err := testHarness(t, work).writeState(); err != nil {
		t.Fatal(err)
	}
	if _, b := bench(0.25); b.Previous != 0.1 || !b.Regression {
		t.Errorf("the baseline did not stay the best time: %+v", b)
	}
	write(t, dir, map[string]string{"cases/bench.out": "b\n"})
	if _, b := bench(0.5); b.Previous != 0 || b.Regression {
		t.Errorf("after the bench case changed: %+v", b)
	}
	if _, b := bench(0.6); b.Previous != 0.5 || b.Regression {
		t.Errorf("new baseline: %+v", b)
	}
}

// TestExcerptReachesTheDifference: the excerpt of a long line shows
// the bytes that differ.
func TestExcerptReachesTheDifference(t *testing.T) {
	x := strings.Repeat("x", 80)
	got := diff("stdout", []byte(x+"WANT\n"), []byte(x+"GOT!\n"))
	if !strings.Contains(got, "WANT") || !strings.Contains(got, "GOT!") || !strings.Contains(got, "at byte 80 (line 1)") {
		t.Errorf("diff: %s", got)
	}
	if got := diff("stdout", []byte("ab\ncd\n"), []byte("ab\nce\n")); !strings.Contains(got, `want "cd\n", got "ce\n"`) {
		t.Errorf("short line: %s", got)
	}
}

// TestStdDependencyReselects: a change in a std
// module the dogfood reaches, through a renamed import and std's own
// imports, reselects it; a change elsewhere in std does not.
func TestStdDependencyReselects(t *testing.T) {
	root := t.TempDir()
	write(t, root, map[string]string{
		"std/hash/crc.mk": "import std.text\n",
		"std/text/t.mk":   "// text\n",
		"std/core/c.mk":   "// core\n",
		"std/net/n.mk":    "// net\n",
	})
	dir := filepath.Join(root, "Dogfoods", "x")
	files := dogfoodFiles("x")
	files["src/main.mk"] = "import h = std.hash\n"
	write(t, dir, files)
	d, err := loadDogfood(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	work := t.TempDir()
	harn := func() *harness {
		h := testHarness(t, work)
		h.root = root
		return h
	}
	h := harn()
	if got := h.stdClosure(d.Imports); !strings.Contains(strings.Join(got, " "), "std.hash std.raw std.rt std.sys std.task std.text") {
		t.Errorf("closure %q", got)
	}
	h.passed[stateKey("x", hostTarget(), "dev")] = h.digest(d)
	if err := h.writeState(); err != nil {
		t.Fatal(err)
	}
	if got := harn().selectChanged([]*Dogfood{d}); len(got) != 0 {
		t.Fatalf("selected with nothing changed")
	}
	write(t, root, map[string]string{"std/net/n.mk": "// net, changed\n"})
	if got := harn().selectChanged([]*Dogfood{d}); len(got) != 0 {
		t.Errorf("a std module it does not reach reselected it")
	}
	write(t, root, map[string]string{"std/text/t.mk": "// text, changed\n"})
	h = harn()
	if got := h.selectChanged([]*Dogfood{d}); len(got) != 1 || !strings.Contains(h.why["x"], "changed since its last pass") {
		t.Errorf("a change in std.text (reached through std.hash) did not reselect it: %q", h.why)
	}
}

// TestKernelELFCheck: without QEMU, a file that is not a static
// x86_64 ELF with a PT_NOTE fails the check.
func TestKernelELFCheck(t *testing.T) {
	p := filepath.Join(t.TempDir(), "k.elf")
	write(t, filepath.Dir(p), map[string]string{"k.elf": "MZ not an ELF"})
	if err := checkKernelELF(p); err == nil {
		t.Error("a non-ELF file passes the kernel check")
	}
}

// TestReferenceExemptIsExempt: a line
// referenced only through its EXEMPT.md reference entry is counted as
// exempt, not covered, whether it declares a function or not.
func TestReferenceExemptIsExempt(t *testing.T) {
	items := referenceItems(t)
	h := &harness{items: items}
	c := newCoverage(items)
	c.refsOK = true
	c.hosted = []string{targetWindows}
	var sys, trim *LineCov
	for _, lc := range c.lines {
		switch lc.Item.Text {
		case "struct System {":
			sys = lc
		case "str.Trim(self): str":
			trim = lc
		}
	}
	sys.Exempt = &Exemption{Kind: "reference"}
	trim.Exempt = &Exemption{Kind: "reference"}
	trim.Exec, trim.ExecOn, trim.Present = []string{"d/c (windows)"}, []string{targetWindows}, true
	sum := &CoverSummary{}
	h.judgeLines(c, sum)
	if sys.Verdict != "exempt (reference)" || trim.Verdict != "exempt (reference; executed)" {
		t.Errorf("verdicts %q, %q", sys.Verdict, trim.Verdict)
	}
	if sum.Exempt != 2 || sum.Covered != 0 {
		t.Errorf("summary %+v", sum)
	}
	// Referenced by a dogfood as well: covered.
	sys.Refs = []string{"d"}
	h.judgeLines(c, &CoverSummary{})
	if sys.Verdict != "covered (referenced)" {
		t.Errorf("referenced and exempt: %q", sys.Verdict)
	}
}
