// Why: every gate step starts its commands the same way, with a clean
// environment, a log and a recorded exit code, so a report entry can be
// rerun by hand and two steps cannot leak settings into each other.
//
// What: spec and exec (the one way a step starts a command), env and
// envWith, and the steps in the order run calls them: buildSteps, testStep,
// linuxStep (linuxPlan, wslAnswers), corpusSteps and timingSteps, with
// splitStd, testRuns and fold for the go test runs.
//
// How: the test and Linux steps run the packages other than ./std in one
// go test with pkgTimeout, then ./std alone with -stdtimeout; fold merges
// each run's parsed results into the step. The Linux step reruns, under
// the WSL lock with -p 1, only the top-level tests that skipped for
// MK_LINUX in the test step, after one WSL probe that also reads the
// repository through WSL's file bridge.

package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strings"
	"time"

	"mk/tools/machinelock"
)

// cleared are removed from every child's environment before a step sets
// what it needs; PERFRT is among them because its tests hold 100,000
// sockets, and MK_ZDEC because the gate sets it only to the zdec it built
// (zdec.go).
var cleared = []string{"MK_GATE", "MK_LINUX", "MK_TIMING", "PERFRT", "RTS_ONLY", "GOOS", "GOARCH", "MK_GATE_TRIPWIRE", "MK_ZDEC"}

// spec is one command of a step.
type spec struct {
	argv     []string
	set      []string // K=V the step sets
	lock     bool     // run under tools/wsl-lock.ps1
	tripwire bool     // put the tripwire wsl.exe first on PATH
	tripLog  string   // the tripwire's log for this step, when steps run at once (default the gate's)
	noZdec   bool     // never MK_ZDEC (zdec's own build, which runs under zdecs' lock)
}

// newStep appends a step to the report and prints its header.
func (g *gate) newStep(name string) *Step {
	s := &Step{Name: name, Status: "pass"}
	g.mu.Lock()
	g.rep.Steps = append(g.rep.Steps, s)
	g.mu.Unlock()
	fmt.Printf("\n== %s\n", name)
	return s
}

// skipStep records a step that did not run.
func (g *gate) skipStep(name, why string) {
	s := g.newStep(name)
	s.Status, s.Reason = "skip", why
	fmt.Printf("   skipped: %s\n", why)
}

// fail marks s failed with why (the first reason wins).
func fail(s *Step, why string) {
	if s.Status != "fail" {
		s.Reason = why
	}
	s.Status = "fail"
}

// env is the child environment for sp.
func (g *gate) env(sp spec) []string {
	return g.envWith(sp, g.specZdec(sp))
}

// specZdec is sp's MK_ZDEC entries: none for a noZdec command (zdec's own
// build, which runs under zdecs' lock, so it must never reach zdecEnv),
// else zdecEnv's for its argv.
func (g *gate) specZdec(sp spec) []string {
	if sp.noZdec {
		return nil
	}
	return g.zdecEnv(sp.argv)
}

// envWith is the child environment for sp with zenv as its MK_ZDEC
// entries, so exec records the same entries the child gets. MK_WORK is
// the resolved work root, so a child that resolves the machine lock finds
// the gate's, though TMP and TEMP change under it.
func (g *gate) envWith(sp spec, zenv []string) []string {
	drop := append(slices.Clone(cleared), machinelock.WorkEnv)
	if g.work != "" {
		drop = append(drop, "TMP", "TEMP", "GOTMPDIR", "GOCACHE", "MK_CACHE")
	}
	for _, kv := range sp.set {
		// A step's own entry replaces the inherited one.
		k, _, _ := strings.Cut(kv, "=")
		drop = append(drop, k)
	}
	var out []string
	for _, kv := range os.Environ() {
		// Windows keeps per-drive directories as "=C:=C:\x": no key.
		k, _, _ := strings.Cut(kv, "=")
		if k == "" || !slices.ContainsFunc(drop, func(d string) bool { return sameKey(d, k) }) {
			out = append(out, kv)
		}
	}
	if g.work != "" {
		tmp := filepath.Join(g.work, "tmp")
		// MK_CACHE: no mk a step starts reaches the per-user cache.
		out = append(out, "TMP="+tmp, "TEMP="+tmp, "GOTMPDIR="+tmp, "GOCACHE="+filepath.Join(g.work, "gocache"), "MK_CACHE="+filepath.Join(g.work, "mkcache"))
	}
	out = append(out, machinelock.WorkEnv+"="+machinelock.WorkRoot())
	out = append(out, sp.set...)
	out = append(out, zenv...) // a go test of ./std only (zdec.go)
	if sp.tripwire && g.tripDir != "" {
		for i, kv := range out {
			if k, v, _ := strings.Cut(kv, "="); sameKey(k, "PATH") {
				out[i] = k + "=" + g.tripDir + string(os.PathListSeparator) + v
			}
		}
		out = append(out, "MK_GATE_TRIPWIRE="+g.trip(sp))
	}
	return out
}

// trip is the tripwire log sp's step uses.
func (g *gate) trip(sp spec) string {
	if sp.tripLog != "" {
		return sp.tripLog
	}
	return g.tripLog
}

func sameKey(a, b string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// exec runs sp for step s, writing everything to s's log and each line to
// line; it records the command, the environment, the wall time and the
// exit code.
func (g *gate) exec(s *Step, sp spec, line func(string)) int {
	argv := sp.argv
	if sp.lock {
		argv = append([]string{g.pwsh, "-NoProfile", "-File", filepath.Join(g.root, "tools", "wsl-lock.ps1")}, argv...)
	}
	g.mu.Lock()
	g.seq++
	seq := g.seq
	g.mu.Unlock()
	logPath := filepath.Join(g.work, "logs", fmt.Sprintf("%02d-%s.log", seq, fileName(s.Name)))
	// A step of several commands (the test and Linux steps) keeps
	// them all: Command joins them with " ; ", Log is the first one's log
	// and Logs has every log.
	cmdLine := strings.Join(argv, " ")
	if s.Command == "" {
		s.Command = cmdLine
	} else {
		s.Command += " ; " + cmdLine
	}
	zenv := g.specZdec(sp) // once: the record and the child get the same entries
	env := append(slices.Clone(sp.set), zenv...)
	if sp.tripwire && g.tripDir != "" {
		env = append(env, "PATH=<tripwire>;...")
	}
	env = append(env, "unset: "+strings.Join(cleared[:5], " "))
	for _, kv := range env {
		if !slices.Contains(s.Env, kv) {
			s.Env = append(s.Env, kv)
		}
	}
	fmt.Printf("   $ %s\n", cmdLine)
	logf, err := os.Create(logPath)
	if err != nil {
		fail(s, err.Error())
		return -1
	}
	defer logf.Close()
	if s.Log == "" {
		s.Log = logPath
	} else {
		if len(s.Logs) == 0 {
			s.Logs = []string{s.Log}
		}
		s.Logs = append(s.Logs, logPath)
	}
	if sp.tripwire && g.tripLog != "" {
		os.Remove(g.trip(sp))
	}
	w := &lineWriter{log: logf, line: line}
	start := time.Now()
	code, err := g.start(argv, g.envWith(sp, zenv), w)
	w.flush()
	s.WallSeconds += secs(time.Since(start))
	if err != nil {
		fail(s, err.Error())
	}
	if s.ExitCode == 0 {
		s.ExitCode = code // the first non-zero status of the step's commands
	}
	if sp.tripwire && g.tripLog != "" {
		if b, err := os.ReadFile(g.trip(sp)); err == nil && len(b) > 0 {
			if s.Tests == nil {
				s.Tests = &TestCounts{}
			}
			s.Tests.TripwireHits = append(s.Tests.TripwireHits, strings.Split(strings.TrimSpace(string(b)), "\n")...)
			fail(s, "wsl.exe started while MK_LINUX=1 was not set (tripwire)")
		}
	}
	return code
}

// start runs argv in the root with env, its output to out, and returns its
// exit status; err is a failure to start it (status -1). With g.runCmd set
// (the tests), that records and answers the command instead.
func (g *gate) start(argv, env []string, out io.Writer) (int, error) {
	if g.runCmd != nil {
		return g.runCmd(argv, env, out)
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = g.root
	cmd.Env = env
	cmd.Stdout, cmd.Stderr = out, out
	err := cmd.Run()
	var ee *exec.ExitError
	switch {
	case err == nil:
		return 0, nil
	case errors.As(err, &ee):
		return ee.ExitCode(), nil
	}
	return -1, err
}

var nonName = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

func fileName(s string) string {
	n := strings.Trim(nonName.ReplaceAllString(s, "_"), "_")
	if len(n) > 60 {
		n = n[:60]
	}
	return n
}

// plain runs a command that is not go test: every line is echoed and the
// last 60 are kept.
func (g *gate) plain(s *Step, sp spec) ([]string, int) {
	var all []string
	code := g.exec(s, sp, func(l string) {
		fmt.Printf("   %s\n", l)
		all = append(all, l)
	})
	s.Output = tail(all, 60)
	if code != 0 {
		fail(s, fmt.Sprintf("exit status %d", code))
	}
	return all, code
}

func tail(l []string, n int) []string {
	if len(l) > n {
		return l[len(l)-n:]
	}
	return l
}

// buildSteps: go build and go vet for the host and for linux, gofmt -l.
func (g *gate) buildSteps() {
	names := []string{"build", "build GOOS=linux", "vet", "vet GOOS=linux", "gofmt -l"}
	if g.skip["build"] {
		for _, n := range names {
			g.skipStep(n, "-skip build")
		}
		return
	}
	for _, c := range []struct {
		name string
		args []string
		set  []string
	}{
		{names[0], []string{"build", "./..."}, nil},
		{names[1], []string{"build", "./..."}, []string{"GOOS=linux", "GOARCH=amd64"}},
		{names[2], []string{"vet", "./..."}, nil},
		{names[3], []string{"vet", "./..."}, []string{"GOOS=linux", "GOARCH=amd64"}},
	} {
		s := g.newStep(c.name)
		g.plain(s, spec{argv: append([]string{g.goExe}, c.args...), set: c.set, tripwire: true})
	}
	s := g.newStep(names[4])
	out, _ := g.plain(s, spec{argv: []string{g.gofmtExe, "-l", "."}, tripwire: true})
	if len(out) > 0 {
		s.Failed = out
		fail(s, fmt.Sprintf("%d files are not gofmt-formatted", len(out)))
	}
}

// pkgTimeout is go test's -timeout for every run but a whole-package run
// of ./std: a hung package fails within the hour.
const pkgTimeout = time.Hour

// stdPkg is the package the test and Linux steps run alone, with
// -stdtimeout: its tests take about an hour alone, more under load
// or with MK_LINUX=1 and -p 1.
const stdPkg = "./std"

// defaultStdTimeout is -stdtimeout's default. The slowest run it covers is
// the Linux step's whole ./std with MK_LINUX=1 and -p 1 when -skip test: in a
// gate run during the 1.2.0 release work, 362 tests took the whole 3600 s and
// the remaining ones 3879 s more, about 2.1 h in all. go test's cache key
// holds the -timeout value, so a changed value reruns ./std once.
const defaultStdTimeout = 4 * time.Hour

// testFlags are go test's flags for this tier, with timeout as -timeout
// in whole seconds ("3600s", the text the cache keys of earlier gates
// hold).
func (g *gate) testFlags(timeout time.Duration) []string {
	f := []string{"test", "-json", "-timeout", fmt.Sprintf("%ds", int64(timeout/time.Second))}
	if g.tier != "quick" {
		f = append(f, "-count=1")
	}
	return f
}

// goTestRun is one go test command of the test or Linux step: its
// packages, its -timeout and its -run ("" for every test).
type goTestRun struct {
	pkgs    []string
	timeout time.Duration
	run     string
}

// testArgv is r's command line, with extra (such as -p 1) after the tier's
// flags.
func (g *gate) testArgv(r goTestRun, extra ...string) []string {
	argv := append(append([]string{g.goExe}, g.testFlags(r.timeout)...), extra...)
	if r.run != "" {
		argv = append(argv, "-run", r.run)
	}
	return append(argv, r.pkgs...)
}

// splitStd splits pkgs (packages and patterns as -only or "./..." give
// them) into the runs of a step: the packages other than ./std in one run
// with pkgTimeout, then ./std alone with stdTimeout. A pattern with "..."
// that expand holds is replaced by the packages go list gave for it, so
// go list, not a matcher here, decides whether it holds ./std ("./...",
// "./s...", "./std/..."); a pattern expand lacks stays as it is. Either
// run is left out when it has no package.
func splitStd(pkgs []string, expand map[string][]string, stdTimeout time.Duration) []goTestRun {
	var rest []string
	std := false
	add := func(p string) {
		switch {
		case p == stdPkg:
			std = true
		case !slices.Contains(rest, p):
			rest = append(rest, p)
		}
	}
	for _, p := range pkgs {
		listed, ok := expand[p]
		if !ok {
			add(p)
			continue
		}
		for _, l := range listed {
			add(l)
		}
	}
	var runs []goTestRun
	if len(rest) > 0 {
		runs = append(runs, goTestRun{pkgs: rest, timeout: pkgTimeout})
	}
	if std {
		runs = append(runs, goTestRun{pkgs: []string{stdPkg}, timeout: stdTimeout})
	}
	return runs
}

// wildcards are the patterns of pkgs with "...", which the test step
// expands with go list before splitStd.
func wildcards(pkgs []string) []string {
	var out []string
	for _, p := range pkgs {
		if strings.Contains(p, "...") {
			out = append(out, p)
		}
	}
	return out
}

// goTest runs one go test -json command for step s and folds its result in.
func (g *gate) goTest(s *Step, sp spec) *testParse {
	tp := newTestParse()
	code := g.exec(s, sp, tp.line)
	tp.finish()
	g.fold(s, tp, code)
	return tp
}

// fold adds one go test run's result (tp, and its exit status code) to
// step s and to the gate's results, and writes the step's reason again
// over all its runs so far: a reason set outside fold first (the
// tripwire, a command that did not start), then a wslGuard hit, then each
// package that ended mid-run, by name and timeout, then the count
// of every failing test or package, else the first non-zero exit status.
func (g *gate) fold(s *Step, tp *testParse, code int) {
	if s.Status == "fail" && s.Reason != "" && s.Reason != s.foldReason && !slices.Contains(s.preReasons, s.Reason) {
		s.preReasons = append(s.preReasons, s.Reason)
	}
	if s.Tests == nil {
		s.Tests = &TestCounts{}
	}
	t := s.Tests
	t.Packages += tp.counts.Packages
	t.PackagesCached += tp.counts.PackagesCached
	t.PackagesFailed += tp.counts.PackagesFailed
	t.Pass += tp.counts.Pass
	t.Fail += tp.counts.Fail
	t.Skip += tp.counts.Skip
	t.LinuxSkips += tp.counts.LinuxSkips
	t.TimingSkips += tp.counts.TimingSkips
	t.TimingLogged += tp.counts.TimingLogged
	t.GuardHits = append(t.GuardHits, tp.counts.GuardHits...)
	s.Failed = append(s.Failed, tp.failed...)
	s.FailOutput = append(s.FailOutput, tp.failOut...)
	if g.testResults == nil {
		g.testResults = map[string]string{}
	}
	for k, r := range tp.results {
		g.testResults[k] = r
	}
	s.Output = append(s.Output, tail(tp.other, 40)...)
	if g.ended == nil {
		g.ended = map[string]pkgEnd{}
	}
	pkgs := make([]string, 0, len(tp.ended))
	for pkg := range tp.ended {
		pkgs = append(pkgs, pkg)
	}
	sort.Strings(pkgs)
	for _, pkg := range pkgs {
		why := tp.ended[pkg]
		where := fmt.Sprintf("%s in the %s step", why, s.Name)
		e := g.ended[pkg]
		if e.why != "" && e.why != where {
			where = e.why + ", then " + where
		}
		g.ended[pkg] = pkgEnd{why: where, linux: e.linux || s.Name == linuxStepName}
		s.endedWhy = append(s.endedWhy, pkg+" "+why)
	}
	if code != 0 && s.firstCode == 0 {
		s.firstCode = code
	}
	parts := slices.Clone(s.preReasons)
	if len(t.GuardHits) > 0 {
		parts = append(parts, "a leaf reached wsl.exe without MK_LINUX=1 (wslGuard)")
	}
	parts = append(parts, s.endedWhy...)
	switch {
	case len(s.Failed) > 0:
		parts = append(parts, fmt.Sprintf("%d failing tests or packages", len(s.Failed)))
	case s.firstCode != 0:
		parts = append(parts, fmt.Sprintf("go test exit status %d", s.firstCode))
	}
	if len(parts) > 0 {
		s.Status, s.Reason = "fail", strings.Join(parts, "; ")
		s.foldReason = s.Reason
	}
}

// pkgEnd is how a package ended mid-run in the gate's steps: why and
// where ("timed out after 4h0m0s in the test step"), and whether one of
// those ends was in the Linux step, where its Linux leaves were to run.
type pkgEnd struct {
	why   string
	linux bool
}

// testPkgs is the package list of the test step.
func (g *gate) testPkgs() []string {
	if len(g.only) > 0 {
		return g.only
	}
	return []string{"./..."}
}

// testRuns are the test step's go test runs over pkgs (testPkgs), given
// expand, go list's packages for each of pkgs' "..." patterns: the other
// packages with pkgTimeout, then ./std alone with -stdtimeout.
func (g *gate) testRuns(pkgs []string, expand map[string][]string) []goTestRun {
	return splitStd(pkgs, expand, g.stdTimeout)
}

// listPkgs is go list -e over each of pats, as "./dir" packages.
func (g *gate) listPkgs(pats []string) (map[string][]string, error) {
	out := map[string][]string{}
	for _, p := range pats {
		lines, err := g.goOut("list", "-e", p)
		if err != nil {
			return nil, fmt.Errorf("go list -e %s: %v", p, err)
		}
		for _, l := range lines {
			if l = strings.TrimSpace(l); l != "" {
				out[p] = append(out[p], normPkg(l))
			}
		}
	}
	return out, nil
}

// testStep: every package, MK_GATE=1, no Linux, no timing assertions; the
// packages other than ./std in one go test, then ./std alone, with zdec
// built first for it (zdec.go). A go list that fails fails the
// step, and the Linux step after it.
func (g *gate) testStep() {
	if g.skip["test"] {
		g.skipStep("test", "-skip test")
		return
	}
	pkgs := g.testPkgs()
	expand, err := g.listPkgs(wildcards(pkgs))
	var runs []goTestRun
	if err == nil {
		runs = g.testRuns(pkgs, expand)
		g.zdecFor(runs)
	}
	s := g.newStep("test")
	if err != nil {
		g.listErr = err.Error()
		fail(s, g.listErr)
		return
	}
	for _, r := range runs {
		tp := g.goTest(s, spec{argv: g.testArgv(r), set: []string{"MK_GATE=1"}, tripwire: true})
		for pkg, tests := range tp.linux {
			g.linuxTests[pkg] = tests
		}
		for pkg := range tp.reported {
			if g.linuxTests[pkg] == nil {
				g.linuxTests[pkg] = map[string]bool{}
			}
		}
	}
	g.testRan = true
}

// linuxStep reruns, under the WSL lock, the tests that skipped in the test
// step for MK_LINUX=1; a Linux package the test step did not report runs
// whole.
func (g *gate) linuxStep() {
	const name = linuxStepName
	switch {
	case g.skip["linux"]:
		g.skipStep(name, "-skip linux")
		return
	case runtime.GOOS != "windows":
		g.skipStep(name, "not a Windows host: the Linux leaves ran natively in the test step")
		return
	case g.pwsh == "":
		s := g.newStep(name)
		fail(s, "pwsh not found on PATH; tools/wsl-lock.ps1 needs it")
		return
	case g.listErr != "":
		// The test step did not run, but not by -skip test: running every
		// Linux package whole would hide its failure behind hours of work.
		s := g.newStep(name)
		fail(s, "not run: the test step's "+g.listErr)
		return
	}
	runs, notes := g.linuxPlan()
	if len(runs) == 0 {
		g.skipStep(name, "no Linux leaf to run")
		g.rep.Steps[len(g.rep.Steps)-1].Notes = notes
		return
	}
	g.zdecFor(runs)
	if !g.wslAnswers() {
		return
	}
	s := g.newStep(name)
	s.Notes = notes
	set := []string{"MK_GATE=1", "MK_LINUX=1"}
	for _, r := range runs {
		g.goTest(s, spec{argv: g.testArgv(r, "-p", "1"), set: set, lock: true})
	}
}

// linuxPlan is the Linux step's go test runs, each with -p 1 under the
// WSL lock, and its notes. A package whose leaves skipped for MK_LINUX in
// the test step reruns those top-level tests (-run); a Linux package the
// test step did not report runs whole. Each group runs its packages other
// than ./std together with pkgTimeout, then ./std alone with -stdtimeout,
// its -run naming only its own tests. A package that ended
// mid-run in the test step reruns only the leaves that skipped before it
// ended, and a note says so.
func (g *gate) linuxPlan() ([]goTestRun, []string) {
	var whole, some, notes []string
	for _, p := range linuxPkgs {
		if !g.selected(p) {
			continue
		}
		ts, reported := g.linuxTests[p]
		switch {
		case !g.testRan || !reported:
			whole = append(whole, p)
		case len(ts) == 0:
			notes = append(notes, p+": no leaf skipped for MK_LINUX in the test step")
		default:
			some = append(some, p)
		}
		if e := g.ended[p]; e.why != "" && g.testRan && reported {
			notes = append(notes, fmt.Sprintf("%s %s: only its Linux leaves that skipped before then run here; the others did not run", p, e.why))
		}
	}
	var runs []goTestRun
	for _, r := range splitStd(some, nil, g.stdTimeout) {
		var names []string
		for _, p := range r.pkgs {
			for t := range g.linuxTests[p] {
				if !slices.Contains(names, t) {
					names = append(names, t)
				}
			}
		}
		sort.Strings(names)
		r.run = "^(" + strings.Join(names, "|") + ")$"
		notes = append(notes, fmt.Sprintf("%d top-level tests of %s", len(names), strings.Join(r.pkgs, " ")))
		runs = append(runs, r)
	}
	if len(whole) > 0 {
		notes = append(notes, "whole packages (no test-step result): "+strings.Join(whole, " "))
		runs = append(runs, splitStd(whole, nil, g.stdTimeout)...)
	}
	return runs, notes
}

// wslAnswers probes WSL once, under the lock, with this program's
// -probe-wsl (30 s). A WSL that does not answer stops all Linux work.
func (g *gate) wslAnswers() bool {
	if g.wslDown != "" {
		return false
	}
	s := g.newStep("wsl probe (wsl.exe -e true, 30 s)")
	out, code := g.plain(s, spec{argv: []string{g.self, "-probe-wsl"}, lock: true})
	if code != 0 {
		g.wslDown = strings.Join(tail(out, 3), " ")
		fail(s, "WSL did not answer; Linux work stopped: "+g.wslDown)
		return false
	}
	return true
}

// probe runs `wsl.exe -e true` with a deadline and prints the outcome.
func probe(limit time.Duration) int {
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()
	// --cd into the repository: WSL can answer `true` while its file
	// bridge to the Windows drive is stuck (a process hung in
	// p9_client_rpc), and every Linux test reads through that bridge.
	cmd := exec.CommandContext(ctx, "wsl.exe", probeArgs()...)
	cmd.WaitDelay = 5 * time.Second
	start := time.Now()
	out, err := cmd.CombinedOutput()
	switch {
	case ctx.Err() != nil:
		fmt.Printf("wsl.exe %s did not answer within %v\n", strings.Join(probeArgs(), " "), limit)
		return 1
	case err != nil:
		fmt.Printf("wsl.exe %s: %v %s\n", strings.Join(probeArgs(), " "), err, bytes.TrimSpace(out))
		return 1
	}
	fmt.Printf("wsl.exe %s answered in %v\n", strings.Join(probeArgs(), " "), time.Since(start).Round(time.Millisecond))
	return 0
}

// probeArgs is the probe's command line after wsl.exe: start in the
// current directory (the repository, on the Windows drive) and list it, so
// a stuck file bridge fails the probe as a stuck VM does.
func probeArgs() []string {
	if wd, err := os.Getwd(); err == nil {
		return []string{"--cd", wd, "-e", "ls", "-d", "."}
	}
	return []string{"-e", "true"}
}

// corpusSteps: mk corpus (doc/'s verdict blocks), then mk corpus --all
// --strict (with the conformance units).
func (g *gate) corpusSteps() {
	runs := []struct{ name, args string }{{"corpus", "corpus"}, {"corpus --all --strict", "corpus --all --strict"}}
	for _, r := range runs {
		if g.skip["corpus"] {
			g.skipStep(r.name, "-skip corpus")
			continue
		}
		s := g.newStep(r.name)
		// mk corpus judges verdict blocks and conformance units; it runs no
		// Linux program, so it needs no lock (the tripwire checks that).
		out, _ := g.plain(s, spec{argv: append([]string{g.goExe, "run", "./cmd/mk"}, strings.Fields(r.args)...), set: g.corpusSet(), tripwire: true})
		for _, l := range out {
			if m := corpusTotals.FindStringSubmatch(l); m != nil {
				c := &CorpusCounts{}
				fmt.Sscan(m[1], &c.Pass)
				fmt.Sscan(m[2], &c.Fail)
				fmt.Sscan(m[3], &c.Pending)
				fmt.Sscan(m[4], &c.Listed)
				s.Corpus = c
			}
		}
		if s.Corpus == nil {
			fail(s, "no totals line in mk corpus's output")
		}
	}
}

var corpusTotals = regexp.MustCompile(`^pass (\d+), fail (\d+), pending (\d+) \(listed (\d+)\)`)

// timingSteps run timingRuns with MK_TIMING=1, one package at a time.
func (g *gate) timingSteps() {
	if g.skip["timing"] {
		g.skipStep("timing", "-skip timing")
		return
	}
	ran := false
	for _, r := range timingRuns {
		if !g.selected(r.pkg) {
			continue
		}
		ran = true
		name := "timing " + r.pkg + " " + r.run
		if len(name) > 90 {
			name = name[:90] + "..."
		}
		switch {
		case r.linux && runtime.GOOS != "windows":
			g.skipStep(name, "not a Windows host")
			continue
		case r.linux && g.skip["linux"]:
			g.skipStep(name, "-skip linux")
			continue
		case r.linux && g.pwsh == "":
			s := g.newStep(name)
			fail(s, "pwsh not found on PATH; tools/wsl-lock.ps1 needs it")
			continue
		case r.linux && !g.wslAnswers():
			g.skipStep(name, "WSL did not answer: "+g.wslDown)
			continue
		}
		s := g.newStep(name)
		set := append([]string{"MK_GATE=1", "MK_TIMING=1"}, r.env...)
		if r.linux {
			set = append(set, "MK_LINUX=1")
		}
		// -run selects a few tests, so ./std keeps the hour here too.
		argv := []string{g.goExe, "test", "-json", "-timeout", "3600s", "-count=1", "-p", "1", "-run", r.run, r.pkg}
		tp := g.goTest(s, spec{argv: argv, set: set, lock: r.linux, tripwire: !r.linux})
		if tp.counts.Pass+tp.counts.Fail == 0 && s.Status == "pass" {
			fail(s, "-run selected no test")
		}
	}
	if !ran {
		g.skipStep("timing", "-only selects no package of timingRuns")
	}
}

// goOut runs go with args in the root and returns its output lines.
func (g *gate) goOut(args ...string) ([]string, error) {
	return g.out(append([]string{g.goExe}, args...))
}

// out runs argv in the root with the gate's environment and returns its
// standard output lines.
func (g *gate) out(argv []string) ([]string, error) {
	if g.runCmd != nil {
		var o bytes.Buffer
		code, err := g.runCmd(argv, g.env(spec{tripwire: true}), &o)
		if err == nil && code != 0 {
			err = fmt.Errorf("exit status %d: %s", code, strings.TrimSpace(o.String()))
		}
		if err != nil {
			return nil, err
		}
		return strings.Split(strings.TrimSpace(strings.ReplaceAll(o.String(), "\r\n", "\n")), "\n"), nil
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = g.root
	cmd.Env = g.env(spec{tripwire: true})
	var o, e bytes.Buffer
	cmd.Stdout, cmd.Stderr = &o, &e
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%v: %s", err, strings.TrimSpace(e.String()))
	}
	return strings.Split(strings.TrimSpace(strings.ReplaceAll(o.String(), "\r\n", "\n")), "\n"), nil
}

// corpusSet is the corpus step's own environment entries: MK_CACHE in the
// gate's scratch, never the user's cache (the per-user default).
func (g *gate) corpusSet() []string {
	if g.work != "" {
		return nil // envWith sets MK_CACHE in the work directory
	}
	return []string{"MK_CACHE=" + filepath.Join(os.TempDir(), "mk-gate", "mkcache")}
}
