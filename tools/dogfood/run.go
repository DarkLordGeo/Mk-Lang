// Why: the run command is the dogfoods' test suite: every program built for
// every target and profile, every case run and judged, one report out.
//
// What: the report types (Report, DogReport, RunReport, CaseReport, Bench),
// the run loop with finishRun and benchTime, the build step, and runQemu
// for freestanding dogfoods.
//
// How: each dogfood is built with mk for each target and profile, its
// cases run one at a time (Windows on the host, Linux through wsl.exe under
// the WSL lock, a freestanding dogfood in QEMU), and stdout, stderr and
// the exit code are compared with the case's files; the result is the JSON
// report (dogfood-last.json).

package main

import (
	"bytes"
	"context"
	"debug/elf"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Report is dogfood-last.json.
type Report struct {
	Command     string        `json:"command"`
	Root        string        `json:"root"`
	Dir         string        `json:"dir"`
	Mk          string        `json:"mk"`
	MkVersion   string        `json:"mk_version"`
	Host        string        `json:"host"`
	Started     time.Time     `json:"started"`
	WallSeconds float64       `json:"wall_seconds"`
	OK          bool          `json:"ok"`
	Notes       []string      `json:"notes,omitempty"`
	Dogfoods    []*DogReport  `json:"dogfoods"`
	Bench       []*Bench      `json:"bench,omitempty"`
	Coverage    *CoverSummary `json:"coverage,omitempty"`
}

// DogReport is one dogfood's runs.
type DogReport struct {
	Name     string `json:"name"`
	Lines    int    `json:"lines"`
	Selected string `json:"selected,omitempty"` // why -changed chose it
	// Problems are the dogfood's failures of a rule outside its cases
	// (the minimum line count); any fails the run.
	Problems []string     `json:"problems,omitempty"`
	Runs     []*RunReport `json:"runs"`
}

// RunReport is one (target, profile) of a dogfood.
type RunReport struct {
	Target   string            `json:"target"`
	Profile  string            `json:"profile"`
	Status   string            `json:"status"` // pass, fail or skip
	Reason   string            `json:"reason,omitempty"`
	Builds   []*BuildReport    `json:"builds,omitempty"`
	Cases    []*CaseReport     `json:"cases"`
	Pass     int               `json:"pass"`
	Fail     int               `json:"fail"`
	Skip     int               `json:"skip"`
	Seconds  float64           `json:"wall_seconds"`
	exes     map[string]string // bin -> executable
	digest   string
	profiles string        // the directory the runs' .mkprof files go to (cover)
	used     time.Duration // the cases' time so far, against the suite's limit
}

// BuildReport is one mk build.
type BuildReport struct {
	Bin     string   `json:"bin"`
	Command string   `json:"command"`
	OK      bool     `json:"ok"`
	Seconds float64  `json:"wall_seconds"`
	Output  []string `json:"output,omitempty"`
}

// CaseReport is one case's result.
type CaseReport struct {
	Name    string   `json:"name"`
	Status  string   `json:"status"` // pass, fail, skip, or checked (an ELF checked without QEMU)
	Reason  string   `json:"reason,omitempty"`
	Seconds float64  `json:"wall_seconds"`
	Seeds   int      `json:"seeds,omitempty"`
	Diffs   []string `json:"diffs,omitempty"`
	Command string   `json:"command,omitempty"`
}

// Bench is the timed case of one run, a rough clock for gross
// regressions: never published.
type Bench struct {
	Dogfood string  `json:"dogfood"`
	Target  string  `json:"target"`
	Profile string  `json:"profile"`
	Seconds float64 `json:"wall_seconds"`
	Locked  bool    `json:"machine_locked"`
	// Previous is the baseline: the best time of this (dogfood, target,
	// profile) kept in its state file since the bench case's files last
	// changed, and whether that time was taken under the machine lock;
	// Regression is set over 2x.
	Previous       float64 `json:"previous_seconds,omitempty"`
	PreviousLocked bool    `json:"previous_machine_locked,omitempty"`
	Regression     bool    `json:"regression,omitempty"`
}

// result is what one run of a program gave.
type result struct {
	stdout, stderr []byte
	code           int
	seconds        float64
	timedOut       bool
	err            string // the program did not start, or the runner failed
}

// runAll is the run command over the selected dogfoods.
func (h *harness) runAll(dogs []*Dogfood) {
	for _, d := range dogs {
		dr := &DogReport{Name: d.Name, Lines: d.Lines, Selected: h.why[d.Name]}
		h.rep.Dogfoods = append(h.rep.Dogfoods, dr)
		if p := h.linesProblem(d); p != "" {
			dr.Problems = append(dr.Problems, p)
			h.say("%s: %s", d.Name, p)
		}
		cases, err := loadCases(filepath.Join(d.Dir, "cases"))
		if err != nil {
			dr.Runs = append(dr.Runs, &RunReport{Status: "fail", Reason: err.Error()})
			continue
		}
		var linux []*RunReport
		for _, t := range d.Targets() {
			if !contains(h.targets, t) {
				continue
			}
			for _, p := range h.profiles {
				rr := &RunReport{Target: t, Profile: p}
				dr.Runs = append(dr.Runs, rr)
				if t == targetWindows && runtime.GOOS != "windows" {
					rr.Status, rr.Reason = "skip", "not a Windows host"
					continue
				}
				h.say("%s %s %s", d.Name, t, p)
				if !h.buildRun(d, rr, false) {
					continue
				}
				if t == targetLinux && runtime.GOOS == "windows" {
					linux = append(linux, rr)
					continue
				}
				h.runCases(d, cases, rr)
			}
		}
		if len(linux) > 0 {
			h.runLinux(d, cases, linux)
		}
		for _, rr := range dr.Runs {
			h.finishRun(d, rr)
			if rr.Status == "pass" && len(dr.Problems) == 0 && h.partial == "" && rr.digest != "" {
				// Only a run of the whole folder stands for the gate's,
				// which -changed selects against; the digest
				// holds the compiler's identity.
				h.passed[stateKey(d.Name, rr.Target, rr.Profile)] = rr.digest
			}
		}
	}
}

// finishRun sets a run's status from its cases (a run where no case executed,
// or whose cases took over the suite's limit, fails) and records its bench
// time against the baseline.
func (h *harness) finishRun(d *Dogfood, rr *RunReport) {
	for _, c := range rr.Cases {
		switch c.Status {
		case "pass", "checked":
			rr.Pass++
		case "fail":
			rr.Fail++
		default:
			rr.Skip++
		}
		if c.Name == "bench" && c.Status == "pass" && rr.profiles == "" {
			h.benchTime(d, rr, c)
		}
	}
	var cases float64
	for _, c := range rr.Cases {
		cases += c.Seconds
	}
	rr.Seconds = cases
	for _, b := range rr.Builds {
		rr.Seconds += b.Seconds
	}
	if rr.Status == "" {
		switch {
		case rr.Fail > 0:
			rr.Status = "fail"
		case len(rr.Cases) == 0:
			rr.Status, rr.Reason = "fail", "no case in cases/ (every case runs on both hosted targets in dev and release)"
		case rr.Pass == 0:
			rr.Status, rr.Reason = "fail", fmt.Sprintf("no case ran on %s %s: every case is skipped by its .targets file", rr.Target, rr.Profile)
		case cases > h.suiteLimit.Seconds():
			rr.Status, rr.Reason = "fail", fmt.Sprintf("the cases took %.1f s, over the time limit of %v a suite per target and profile", cases, h.suiteLimit)
		default:
			rr.Status = "pass"
		}
	}
	h.say("  %s %s %s: %s (pass %d, fail %d, skip %d) %s", d.Name, rr.Target, rr.Profile, rr.Status, rr.Pass, rr.Fail, rr.Skip, rr.Reason)
}

// benchTime records the bench case's time and compares it with the baseline
// in the dogfood's state file: the best time seen since the bench case's
// files last changed, kept across runs, across -changed runs that skip the
// dogfood. A time over twice the baseline is a note, which says whether each
// time was taken under the machine lock; it never fails a run.
func (h *harness) benchTime(d *Dogfood, rr *RunReport, c *CaseReport) {
	b := &Bench{Dogfood: d.Name, Target: rr.Target, Profile: rr.Profile, Seconds: c.Seconds, Locked: h.locked}
	key := stateKey(d.Name, rr.Target, rr.Profile)
	files := benchFiles(d)
	base := h.bench[key]
	if base == nil {
		base = h.readState(d.Name).Bench[key]
	}
	if base != nil && base.Files == files && base.Seconds > 0 {
		b.Previous, b.PreviousLocked = base.Seconds, base.Locked
		b.Regression = b.Seconds > 2*base.Seconds
	}
	if base == nil || base.Files != files || b.Seconds < base.Seconds {
		base = &BenchBase{Seconds: b.Seconds, Locked: b.Locked, When: time.Now(), Mk: h.mkID, Files: files}
	}
	h.bench[key] = base
	h.rep.Bench = append(h.rep.Bench, b)
	if b.Regression {
		h.rep.Notes = append(h.rep.Notes, fmt.Sprintf("bench %s %s %s: %.2f s (%s), baseline %.2f s (%s): over 2x", d.Name, rr.Target, rr.Profile,
			b.Seconds, lockWord(b.Locked), b.Previous, lockWord(b.PreviousLocked)))
	}
}

func lockWord(locked bool) string {
	if locked {
		return "under the machine lock"
	}
	return "not under the machine lock, so load may explain it"
}

// buildRun builds every bin of d for rr's target and profile (with
// --pgo-gen when pgo), recording the builds; false when one failed.
func (h *harness) buildRun(d *Dogfood, rr *RunReport, pgo bool) bool {
	rr.exes = map[string]string{}
	rr.digest = h.digest(d)
	suffix := rr.Target + "-" + rr.Profile
	if pgo {
		suffix += "-pgo"
	}
	outDir := filepath.Join(h.scratch, "bin", d.Name, suffix)
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		rr.Status, rr.Reason = "fail", err.Error()
		return false
	}
	for _, bin := range d.Bins {
		exe := filepath.Join(outDir, bin)
		if rr.Target == targetWindows {
			exe += ".exe"
		}
		args := []string{"build", "--target", rr.Target, "--bin", bin, "-o", exe}
		if rr.Profile == "release" {
			args = append(args, "--release")
		}
		if pgo {
			args = append(args, "--pgo-gen")
		}
		args = append(args, d.Dir)
		br := &BuildReport{Bin: bin, Command: "mk " + strings.Join(args, " ")}
		rr.Builds = append(rr.Builds, br)
		start := time.Now()
		cmd := exec.Command(h.mk, args...)
		cmd.Dir = d.Dir
		cmd.Env = append(h.childEnv(), "MK_CACHE="+filepath.Join(h.work, "dogfood", "cache", d.Name))
		out, err := cmd.CombinedOutput()
		br.Seconds = secs(time.Since(start))
		br.Output = tail(lines(out), 40)
		if err != nil {
			rr.Status, rr.Reason = "fail", fmt.Sprintf("mk build --bin %s: %v", bin, err)
			return false
		}
		br.OK = true
		rr.exes[bin] = exe
	}
	if hasPtyCase(d.Dir) && rr.Target != targetFree {
		// ptyrun is the harness's tool: built here, with the dogfood's own
		// executables, so no case's time or the suite's budget pays for it
		// A failure is kept for the terminal cases, which
		// fail naming it.
		h.ptyrunExe(rr.Target)
	}
	return true
}

// childEnv is the environment of every process the harness starts: its
// own, without the switches the gate clears (MK_LINUX, MK_TIMING, ...).
func (h *harness) childEnv() []string {
	var out []string
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		switch strings.ToUpper(k) {
		case "MK_LINUX", "MK_TIMING", "MK_GATE", "PERFRT", "RTS_ONLY", "MK_PROFILE_DIR", "MK_CACHE":
			continue
		}
		out = append(out, kv)
	}
	return out
}

// caseWork makes the empty working directory of one run of a case, under
// this run's scratch directory.
func (h *harness) caseWork(d *Dogfood, rr *RunReport, name string) (string, error) {
	sfx := ""
	if rr.profiles != "" {
		sfx = "-pgo"
	}
	w := filepath.Join(h.scratch, "work", d.Name, rr.Target+"-"+rr.Profile+sfx, name)
	if err := os.RemoveAll(w); err != nil {
		return "", err
	}
	return w, os.MkdirAll(w, 0o755)
}

// runCases runs every case of a host or freestanding run.
func (h *harness) runCases(d *Dogfood, cases []*Case, rr *RunReport) {
	for _, c := range cases {
		cr := &CaseReport{Name: c.Name}
		rr.Cases = append(rr.Cases, cr)
		s, err := c.resolve(rr.Target, rr.Profile)
		if err != nil {
			cr.Status, cr.Reason = "fail", err.Error()
			continue
		}
		if s.Skip != "" {
			cr.Status, cr.Reason = "skip", s.Skip
			continue
		}
		if rr.used >= h.suiteLimit {
			cr.Status, cr.Reason = "fail", fmt.Sprintf("not run: the suite's time limit of %v is spent", h.suiteLimit)
			continue
		}
		start := time.Now()
		if rr.Target == targetFree {
			h.runQemu(d, c, s, rr, cr, start)
		} else {
			h.runHosted(d, c, s, rr, cr, start)
		}
		took := time.Since(start)
		rr.used += took
		cr.Seconds = secs(took)
		h.say("    %-4s %s %s", cr.Status, c.Name, firstOf(cr.Diffs, cr.Reason))
	}
}

// caseLeft is the time a case that started at start may still run: the
// per-case limit (caseLimit, all its processes and seeds, start-up
// included) and what is left of the suite's (suiteLimit).
func (h *harness) caseLeft(rr *RunReport, start time.Time) time.Duration {
	el := time.Since(start)
	return min(h.caseLimit-el, h.suiteLimit-rr.used-el)
}

// runHosted runs one case on the host, once or once per seed, within the
// case's budget.
func (h *harness) runHosted(d *Dogfood, c *Case, s *Spec, rr *RunReport, cr *CaseReport, start time.Time) {
	runs := []string{""}
	if len(s.Seeds) > 0 {
		runs = nil
		for _, n := range s.Seeds {
			runs = append(runs, strconv.FormatUint(n, 10))
		}
		cr.Seeds = len(runs)
	}
	cr.Status = "pass"
	for i, seed := range runs {
		left := h.caseLeft(rr, start)
		if left <= 0 {
			cr.Status = "fail"
			cr.Diffs = append(cr.Diffs, fmt.Sprintf("over the time limit of %v a case (or the suite's %v) after %d of %d runs", h.caseLimit, h.suiteLimit, i, len(runs)))
			return
		}
		work, err := h.caseWork(d, rr, c.Name)
		if err != nil {
			cr.Status, cr.Reason = "fail", err.Error()
			return
		}
		x := Subst{Cases: c.Dir, Work: work, Seed: seed}
		if s.UsesTmp {
			if x.Tmp, err = hostTmp(work); err != nil {
				cr.Status, cr.Reason = "fail", err.Error()
				return
			}
		}
		argv, env, ok := h.command(d, c, s, rr, x, cr)
		if !ok {
			return
		}
		if s.Pty != "" {
			pr, err := h.ptyrunExe(rr.Target)
			if err != nil {
				cr.Status, cr.Reason = "fail", err.Error()
				return
			}
			argv = ptyArgv(pr, left, ptyRaw(work), filepath.Join(work, transcriptName), s.Pty, argv)
		}
		cr.Command = strings.Join(argv, " ")
		res := runProgram(argv, work, append(h.childEnv(), env...), s.Stdin, left)
		if s.Pty != "" {
			res = ptyResult(res, work)
		}
		if x.Tmp != "" {
			os.RemoveAll(x.Tmp)
		}
		h.judge(c, s, rr, x, res, cr)
		if cr.Status == "fail" && len(cr.Diffs) >= 6 {
			cr.Diffs = append(cr.Diffs, "stopped after the failures above")
			return
		}
	}
}

// command is the argv and extra environment of one run of a case on the
// host: the program with its arguments (and --seed), or mk test --sim.
func (h *harness) command(d *Dogfood, c *Case, s *Spec, rr *RunReport, x Subst, cr *CaseReport) ([]string, []string, bool) {
	var env []string
	for _, kv := range s.Env {
		env = append(env, x.expand(kv))
	}
	if rr.profiles != "" {
		dir, err := caseProfiles(rr, c)
		if err != nil {
			cr.Status, cr.Reason = "fail", err.Error()
			return nil, nil, false
		}
		env = append(env, "MK_PROFILE_DIR="+dir)
	}
	if s.SimTest {
		argv := []string{h.mk, "test", "--sim", x.Seed, "--target", rr.Target}
		if rr.Profile == "release" {
			argv = append(argv, "--release")
		}
		return append(argv, d.Dir), append(env, "MK_CACHE="+filepath.Join(h.work, "dogfood", "cache", d.Name)), true
	}
	bin := s.Bin
	if bin == "" {
		bin = d.Bins[0]
	}
	exe := rr.exes[bin]
	if exe == "" {
		cr.Status, cr.Reason = "fail", fmt.Sprintf("the case names bin %q, which pkg.mk does not declare", bin)
		return nil, nil, false
	}
	argv := []string{exe}
	for _, a := range s.Args {
		argv = append(argv, x.expand(a))
	}
	if x.Seed != "" {
		argv = append(argv, "--seed", x.Seed)
	}
	return argv, env, true
}

// caseProfiles is the directory a case's processes write their profiles
// to under a cover run (MK_PROFILE_DIR, std/rt/profile.mk).
func caseProfiles(rr *RunReport, c *Case) (string, error) {
	dir := filepath.Join(rr.profiles, c.Name)
	return dir, os.MkdirAll(dir, 0o755)
}

// judge compares one run with the case's files.
func (h *harness) judge(c *Case, s *Spec, rr *RunReport, x Subst, res result, cr *CaseReport) {
	var ds []string
	tag := ""
	if x.Seed != "" {
		tag = "seed " + x.Seed + ": "
	}
	switch {
	case res.err != "":
		ds = append(ds, tag+res.err)
	case res.timedOut:
		ds = append(ds, fmt.Sprintf("%sdid not exit within the time left of the time limits of %v a case and %v a suite", tag, h.caseLimit, h.suiteLimit))
	default:
		if res.code != s.Code {
			ds = append(ds, fmt.Sprintf("%sexit code %d, want %d", tag, res.code, s.Code))
		}
		compareOut := !s.SimTest || c.file("out", rr.Target, rr.Profile) != ""
		compareErr := !s.SimTest || c.file("err", rr.Target, rr.Profile) != ""
		if compareOut {
			if d := diff("stdout", x.want(s.Out), x.normalize(res.stdout)); d != "" {
				ds = append(ds, tag+d)
			}
		}
		if compareErr {
			if d := diff("stderr", x.want(s.Err), x.normalize(res.stderr)); d != "" {
				ds = append(ds, tag+d)
			}
		}
	}
	if len(ds) > 0 {
		cr.Status = "fail"
		cr.Diffs = append(cr.Diffs, ds...)
	}
}

// runProgram runs argv in dir with env and stdin (a path or ""), killing
// it after limit.
func runProgram(argv []string, dir string, env []string, stdin string, limit time.Duration) result {
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = dir
	cmd.Env = env
	cmd.WaitDelay = 5 * time.Second
	if stdin != "" {
		f, err := os.Open(stdin)
		if err != nil {
			return result{err: err.Error()}
		}
		defer f.Close()
		cmd.Stdin = f
	}
	var o, e bytes.Buffer
	cmd.Stdout, cmd.Stderr = &o, &e
	start := time.Now()
	err := cmd.Run()
	r := result{stdout: o.Bytes(), stderr: e.Bytes(), seconds: secs(time.Since(start))}
	var ee *exec.ExitError
	switch {
	case ctx.Err() != nil:
		r.timedOut = true
	case errors.As(err, &ee):
		r.code = ee.ExitCode()
	case err != nil:
		r.err = err.Error()
	}
	return r
}

// qemuPath finds qemu-system-x86_64 on PATH or in QEMU's Windows install
// directory, or "" (internal/driver/qemu_test.go's qemuPath).
func qemuPath() string {
	if p := os.Getenv("MK_DOGFOOD_QEMU"); p != "" {
		if p == "none" {
			return ""
		}
		return p
	}
	if p, err := exec.LookPath("qemu-system-x86_64"); err == nil {
		return p
	}
	if runtime.GOOS == "windows" {
		p := filepath.Join(os.Getenv("ProgramFiles"), "qemu", "qemu-system-x86_64.exe")
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// runQemu boots a freestanding dogfood's ELF for one case: COM1 on a
// local TCP socket the harness serves (runSerial: .in typed a line at a
// time with flow control, the serial output as the case's stdout),
// isa-debug-exit for the status (QEMU exits with (code << 1) | 1, so
// .code holds QEMU's status). Without QEMU the ELF is checked and the
// case is "checked" with the reason.
func (h *harness) runQemu(d *Dogfood, c *Case, s *Spec, rr *RunReport, cr *CaseReport, start time.Time) {
	bin := s.Bin
	if bin == "" {
		bin = d.Bins[0]
	}
	exe := rr.exes[bin]
	q := qemuPath()
	if q == "" {
		if err := checkKernelELF(exe); err != nil {
			cr.Status, cr.Reason = "fail", "QEMU not found, and the ELF check failed: "+err.Error()
			return
		}
		cr.Status, cr.Reason = "checked", "qemu-system-x86_64 is not on PATH or under Program Files\\qemu: the boot did not run; the ELF was checked (x86_64, static, an entry point, a PT_NOTE for PVH)"
		return
	}
	work, err := h.caseWork(d, rr, c.Name)
	if err != nil {
		cr.Status, cr.Reason = "fail", err.Error()
		return
	}
	argv := []string{q, "-kernel", exe, "-display", "none", "-monitor", "none", "-no-reboot",
		"-device", "isa-debug-exit,iobase=0xf4,iosize=0x04", "-m", "128M"}
	cr.Command = strings.Join(append(argv, serialArgs(0, filepath.Join(work, "serial.log"))...), " ")
	cr.Status = "pass"
	x := Subst{Cases: c.Dir, Work: work}
	h.judge(c, s, rr, x, runSerial(argv, work, h.childEnv(), s.Stdin, max(h.caseLeft(rr, start), time.Millisecond)), cr)
}

// checkKernelELF is the check of a kernel image when QEMU is absent.
func checkKernelELF(path string) error {
	f, err := elf.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	if f.Class != elf.ELFCLASS64 || f.Machine != elf.EM_X86_64 || f.Type != elf.ET_EXEC {
		return fmt.Errorf("%s: %v %v %v, want ELFCLASS64 EM_X86_64 ET_EXEC", path, f.Class, f.Machine, f.Type)
	}
	if f.Entry == 0 {
		return fmt.Errorf("%s: no entry point", path)
	}
	for _, p := range f.Progs {
		if p.Type == elf.PT_INTERP || p.Type == elf.PT_DYNAMIC {
			return fmt.Errorf("%s: not static (%v)", path, p.Type)
		}
	}
	for _, p := range f.Progs {
		if p.Type == elf.PT_NOTE {
			return nil
		}
	}
	return fmt.Errorf("%s: no PT_NOTE, so no PVH entry for qemu -kernel", path)
}

func firstOf(l []string, def string) string {
	if len(l) > 0 {
		return l[0]
	}
	return def
}

func tail(l []string, n int) []string {
	if len(l) > n {
		return l[len(l)-n:]
	}
	return l
}

func secs(d time.Duration) float64 { return float64(d.Milliseconds()) / 1000 }
