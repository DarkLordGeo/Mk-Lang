// Why: Linux cases run through wsl.exe on a Windows host, and parallel WSL
// clients can hang WSL until a reboot, so every Linux run must go through
// the machine's WSL lock with one client at a time.
//
// What: Job, JobResult and JobResults (the job file and its answer),
// runLinux and linuxJob on the harness side, execLocked, and on the child
// side execJobs, runJobs, jobArgv, boundInVM and probeWSL.
//
// How: the harness writes every Linux run of one dogfood to a job file and
// starts itself as `exec-jobs` under tools/wsl-lock.ps1; the child refuses
// to run without MK_WSL_LOCKED=1, asks `wsl.exe -e true` to answer within
// 30 s, then runs the jobs one at a time inside what is left of their case
// and suite budgets. A WSL that does not answer leaves every Linux case not
// verified, and nothing is retried.

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

// probeLimit is how long wsl.exe -e true may take to answer.
const probeLimit = 30 * time.Second

// Job is one process the exec-jobs child starts. Case and Suite name the
// budgets it draws on (30 s a case, 3 min a suite): the jobs of one case (its
// seeds) share CaseSeconds, the jobs of one (target, profile) share
// SuiteSeconds, and the child gives each job only what is left of both.
type Job struct {
	Argv         []string `json:"argv"`
	Dir          string   `json:"dir"`
	Env          []string `json:"env,omitempty"` // added to the child's environment
	Stdin        string   `json:"stdin,omitempty"`
	Case         string   `json:"case"`
	CaseSeconds  float64  `json:"case_seconds"`
	Suite        string   `json:"suite"`
	SuiteSeconds float64  `json:"suite_seconds"`
	// Tmp is the job's scratch directory under the distribution's /tmp
	// ({tmp}), made before the program and removed after it; "" for none.
	Tmp string `json:"tmp,omitempty"`
	// PtyLimit is the index in Argv of a terminal case's ptyrun --limit
	// value, which the exec-jobs child sets from the time the job has left
	// (see jobArgv); 0 for none.
	PtyLimit int `json:"pty_limit,omitempty"`
}

// JobResult is one job's outcome.
type JobResult struct {
	Stdout   []byte  `json:"stdout"`
	Stderr   []byte  `json:"stderr"`
	Code     int     `json:"code"`
	Seconds  float64 `json:"wall_seconds"`
	TimedOut bool    `json:"timed_out,omitempty"`
	Err      string  `json:"err,omitempty"`
}

// JobResults is the exec-jobs child's output file.
type JobResults struct {
	// Probe is set when WSL did not answer: no job ran.
	Probe   string      `json:"probe,omitempty"`
	Results []JobResult `json:"results"`
}

// linuxPath is the path a WSL process sees for a Windows drive path:
// `V:\a\b` is `/mnt/v/a/b` (internal/toolchain HostPathFor).
func linuxPath(p string) string {
	if len(p) < 3 || p[1] != ':' || (p[2] != '\\' && p[2] != '/') {
		return filepath.ToSlash(p)
	}
	d := p[0] | 0x20
	if d < 'a' || d > 'z' {
		return filepath.ToSlash(p)
	}
	return "/mnt/" + string(d) + "/" + strings.ReplaceAll(p[3:], `\`, "/")
}

// pending ties a job to the case run it judges; work is its working
// directory as the harness sees it, where a terminal case's transcript is.
type pending struct {
	rr   *RunReport
	c    *Case
	s    *Spec
	cr   *CaseReport
	x    Subst
	work string
}

// runLinux runs every case of the Linux runs of d under one hold of the
// WSL lock.
func (h *harness) runLinux(d *Dogfood, cases []*Case, runs []*RunReport) {
	if h.wslDown != "" {
		for _, rr := range runs {
			rr.Status, rr.Reason = "fail", "not verified: WSL did not answer earlier in this run ("+h.wslDown+")"
		}
		return
	}
	if h.pwsh == "" {
		for _, rr := range runs {
			rr.Status, rr.Reason = "fail", "not verified: pwsh is not on PATH, and tools/wsl-lock.ps1 needs it"
		}
		return
	}
	var jobs []Job
	var refs []pending
	for _, rr := range runs {
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
			// queued until a result of the case is judged: a batch that
			// does not run leaves no case passed.
			cr.Status = "queued"
			seeds := []string{""}
			if len(s.Seeds) > 0 {
				seeds = nil
				for _, n := range s.Seeds {
					seeds = append(seeds, strconv.FormatUint(n, 10))
				}
				cr.Seeds = len(seeds)
			}
			for _, seed := range seeds {
				work, err := h.caseWork(d, rr, c.Name+suffixSeed(seed))
				if err != nil {
					cr.Status, cr.Reason = "fail", err.Error()
					break
				}
				j, x, ok := h.linuxJob(d, s, rr, c, work, seed, cr)
				if !ok {
					break
				}
				j.Case, j.CaseSeconds = rr.Target+" "+rr.Profile+" "+c.Name, h.caseLimit.Seconds()
				j.Suite, j.SuiteSeconds = rr.Target+" "+rr.Profile, h.suiteLimit.Seconds()
				cr.Command = strings.Join(j.Argv, " ")
				jobs = append(jobs, j)
				refs = append(refs, pending{rr, c, s, cr, x, work})
			}
		}
	}
	if len(jobs) == 0 {
		return
	}
	h.say("  %s: %d Linux runs under the WSL lock", d.Name, len(jobs))
	runner := h.lockedRunner
	if runner == nil {
		runner = h.execLocked
	}
	res, err := runner(d, jobs)
	if err == nil && res.Probe != "" {
		h.wslDown = res.Probe
		err = errors.New("not verified: WSL did not answer (" + res.Probe + "); Linux work stopped")
	}
	if err == nil && len(res.Results) != len(jobs) {
		err = fmt.Errorf("not verified: exec-jobs returned %d results for %d jobs", len(res.Results), len(jobs))
	}
	if err != nil {
		reason := err.Error()
		if !strings.HasPrefix(reason, "not verified") {
			reason = "not verified: " + reason
		}
		for _, rr := range runs {
			rr.Status, rr.Reason = "fail", reason
		}
		for _, p := range refs {
			if p.cr.Status == "queued" {
				p.cr.Status, p.cr.Reason = "fail", reason
			}
		}
		return
	}
	for i, p := range refs {
		r := res.Results[i]
		got := result{stdout: r.Stdout, stderr: r.Stderr, code: r.Code, seconds: r.Seconds, timedOut: r.TimedOut, err: r.Err}
		if p.s.Pty != "" {
			got = ptyResult(got, p.work)
		}
		if p.cr.Status == "queued" {
			p.cr.Status = "pass"
		}
		p.cr.Seconds += r.Seconds
		if len(p.cr.Diffs) < 6 {
			h.judge(p.c, p.s, p.rr, p.x, got, p.cr)
		}
	}
	for _, rr := range runs {
		for _, cr := range rr.Cases {
			h.say("    %-4s %s %s %s", cr.Status, rr.Profile, cr.Name, firstOf(cr.Diffs, cr.Reason))
		}
	}
}

func suffixSeed(seed string) string {
	if seed == "" {
		return ""
	}
	return "-" + seed
}

// linuxJob is one Linux run of a case: the program through wsl.exe with
// its working directory and environment (WSLENV carries the names), or mk
// test --sim, which starts wsl.exe itself.
func (h *harness) linuxJob(d *Dogfood, s *Spec, rr *RunReport, c *Case, work, seed string, cr *CaseReport) (Job, Subst, bool) {
	j := Job{Dir: work, Stdin: s.Stdin}
	if s.SimTest {
		x := Subst{Cases: c.Dir, Work: work, Seed: seed}
		argv, env, ok := h.command(d, c, s, rr, x, cr)
		j.Argv, j.Env = argv, env
		return j, x, ok
	}
	x := Subst{Cases: linuxPath(c.Dir), Work: linuxPath(work), Seed: seed}
	if s.UsesTmp {
		x.Tmp = h.linuxTmp(d, rr, c, seed)
		j.Tmp = x.Tmp
	}
	bin := s.Bin
	if bin == "" {
		bin = d.Bins[0]
	}
	exe := rr.exes[bin]
	if exe == "" {
		cr.Status, cr.Reason = "fail", fmt.Sprintf("the case names bin %q, which pkg.mk does not declare", bin)
		return j, x, false
	}
	program := []string{linuxPath(exe)}
	for _, a := range s.Args {
		program = append(program, x.expand(a))
	}
	if seed != "" {
		program = append(program, "--seed", seed)
	}
	if s.Pty != "" {
		pr, err := h.ptyrunExe(rr.Target)
		if err != nil {
			cr.Status, cr.Reason = "fail", err.Error()
			return j, x, false
		}
		program = ptyArgv(linuxPath(pr), h.caseLimit, linuxPath(ptyRaw(work)), x.Work+"/"+transcriptName, linuxPath(s.Pty), program)
	}
	j.Argv = append([]string{"wsl.exe", "--cd", work, "-e"}, program...)
	if s.Pty != "" {
		j.PtyLimit = 4 + ptyLimitAt
	}
	var names []string
	for _, kv := range s.Env {
		kv = x.expand(kv)
		k, _, _ := strings.Cut(kv, "=")
		names = append(names, k)
		j.Env = append(j.Env, kv)
	}
	if rr.profiles != "" {
		dir, err := caseProfiles(rr, c)
		if err != nil {
			cr.Status, cr.Reason = "fail", err.Error()
			return j, x, false
		}
		j.Env = append(j.Env, "MK_PROFILE_DIR="+linuxPath(dir))
		names = append(names, "MK_PROFILE_DIR")
	}
	if len(names) > 0 {
		w := strings.Join(names, ":")
		if old := os.Getenv("WSLENV"); old != "" {
			w = old + ":" + w
		}
		j.Env = append(j.Env, "WSLENV="+w)
	}
	return j, x, true
}

// jobFiles are the job and result files of d's Linux batch, under this
// run's scratch directory, so no other harness reads or writes them.
func (h *harness) jobFiles(d *Dogfood) (in, out string) {
	dir := filepath.Join(h.scratch, "jobs")
	return filepath.Join(dir, d.Name+".jobs.json"), filepath.Join(dir, d.Name+".results.json")
}

// execLocked writes jobs to a file and runs `<self> exec-jobs` under
// tools/wsl-lock.ps1, then reads the results.
func (h *harness) execLocked(d *Dogfood, jobs []Job) (*JobResults, error) {
	in, out := h.jobFiles(d)
	if err := os.MkdirAll(filepath.Dir(in), 0o755); err != nil {
		return nil, err
	}
	os.Remove(out)
	b, err := json.Marshal(jobs)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(in, b, 0o644); err != nil {
		return nil, err
	}
	self, err := os.Executable()
	if err != nil {
		return nil, err
	}
	argv := []string{h.pwsh, "-NoProfile", "-File", filepath.Join(h.root, "tools", "wsl-lock.ps1"), self, "exec-jobs", in, out}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = h.root
	cmd.Env = h.childEnv()
	cmd.Stdout, cmd.Stderr = h.out, h.out
	runErr := cmd.Run()
	rb, err := os.ReadFile(out)
	if err != nil {
		return nil, fmt.Errorf("exec-jobs under the WSL lock: %v (no results: %v)", runErr, err)
	}
	var res JobResults
	if err := json.Unmarshal(rb, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

// execJobs is the child: `exec-jobs <jobs.json> <results.json>`, run only
// by tools/wsl-lock.ps1.
func execJobs(args []string) int {
	if len(args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: dogfood exec-jobs <jobs.json> <results.json> (under tools/wsl-lock.ps1)")
		return 2
	}
	if os.Getenv("MK_WSL_LOCKED") != "1" {
		fmt.Fprintln(os.Stderr, "dogfood exec-jobs: MK_WSL_LOCKED=1 is not set: it runs only under tools/wsl-lock.ps1")
		return 2
	}
	b, err := os.ReadFile(args[0])
	if err != nil {
		fmt.Fprintln(os.Stderr, "dogfood exec-jobs:", err)
		return 2
	}
	var jobs []Job
	if err := json.Unmarshal(b, &jobs); err != nil {
		fmt.Fprintln(os.Stderr, "dogfood exec-jobs:", err)
		return 2
	}
	res := runJobs(jobs, probeWSL)
	ob, err := json.Marshal(&res)
	if err == nil {
		err = os.WriteFile(args[1], ob, 0o644)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "dogfood exec-jobs:", err)
		return 2
	}
	return 0
}

// runJobs is the child's work once it holds the lock: probe (probeWSL, or
// a test's stand-in) must answer first; then the jobs run one at a time,
// each given what is left of its case's and its suite's budgets, and a
// job whose budget is spent does not start.
func runJobs(jobs []Job, probe func() string) JobResults {
	var res JobResults
	if msg := probe(); msg != "" {
		res.Probe = msg
		return res
	}
	used := map[string]float64{} // budget key -> seconds spent
	var roots []string           // the /tmp/mk-<run> directories the jobs used
	for _, j := range jobs {
		ck, sk := "case "+j.Case, "suite "+j.Suite
		left := min(j.CaseSeconds-used[ck], j.SuiteSeconds-used[sk])
		if left <= 0 {
			res.Results = append(res.Results, JobResult{Err: fmt.Sprintf("not run: the time limits of %v s a case or %v s a suite are spent", j.CaseSeconds, j.SuiteSeconds)})
			continue
		}
		argv := j.Argv
		limit := time.Duration(left * float64(time.Second))
		vmLimit := 0
		if argv[0] == "wsl.exe" {
			p, err := exec.LookPath("wsl.exe")
			if err != nil {
				res.Results = append(res.Results, JobResult{Err: err.Error()})
				continue
			}
			argv, vmLimit = jobArgv(j, p, left)
			if j.Tmp != "" {
				if r := path.Dir(j.Tmp); !slices.Contains(roots, r) {
					roots = append(roots, r)
				}
			}
			if vmLimit > 0 {
				// The VM's own timeout ends the case first; the Windows
				// limit stays as the backstop.
				limit += vmGrace
			}
		}
		start := time.Now()
		r := runProgram(argv, j.Dir, append(os.Environ(), j.Env...), j.Stdin, limit)
		took := time.Since(start).Seconds()
		used[ck] += took
		used[sk] += took
		// 124: timeout's TERM ended it; 137: KILL ended it; 9: KILL went to
		// timeout's process group, timeout itself included, and wsl.exe
		// reports the signal (the case hung through TERM).
		timedOut := r.timedOut || (vmLimit > 0 && (r.code == 124 || r.code == 137 || r.code == 9) && r.seconds >= float64(vmLimit))
		res.Results = append(res.Results, JobResult{Stdout: r.stdout, Stderr: r.stderr, Code: r.code, Seconds: r.seconds, TimedOut: timedOut, Err: r.err})
	}
	for _, r := range roots {
		// One client after the batch, under the same hold of the lock.
		if p, err := exec.LookPath("wsl.exe"); err == nil {
			argv := sweepArgv(p, r)
			runProgram(argv, "", os.Environ(), "", probeLimit)
		}
	}
	return res
}

// jobArgv is the command line the exec-jobs child runs for a wsl.exe job
// with left seconds of its budgets: wsl.exe's path, a terminal case's
// ptyrun limit set from left less the margin, as on the host (`timeout`
// would otherwise end ptyrun before its own limit, and the
// case would read as a plain timeout instead of naming the step), the
// VM's own timeout, and the scratch shell; and the VM's limit in seconds.
func jobArgv(j Job, wsl string, left float64) ([]string, int) {
	argv := append([]string{wsl}, j.Argv[1:]...)
	if j.PtyLimit > 0 && j.PtyLimit < len(argv) && argv[j.PtyLimit-1] == "--limit" {
		argv[j.PtyLimit] = ptyLimitMs(time.Duration(left * float64(time.Second)))
	}
	argv, vmLimit := boundInVM(argv, left)
	if j.Tmp != "" {
		argv = wrapTmp(argv, j.Tmp)
	}
	return argv, vmLimit
}

// vmGrace is how much longer the Windows side waits than the VM's timeout,
// so the VM ends a case that runs over before wsl.exe is killed.
const vmGrace = 15 * time.Second

// boundInVM puts `timeout -k 5 <s>` before the program of a `wsl.exe ... -e
// <prog> <args>` command line, s the case's seconds left (at least 1), and
// returns s; 0 and argv unchanged when argv has no -e. Killing only the
// Windows wsl.exe client can leave the Linux process running and holding
// WSL (a killed Linux run can hang WSL for every client), so the case is
// also bounded inside the VM.
func boundInVM(argv []string, left float64) ([]string, int) {
	for i, a := range argv {
		if a != "-e" || i+1 >= len(argv) {
			continue
		}
		s := max(1, int(left))
		out := append([]string{}, argv[:i+1]...)
		out = append(out, "timeout", "-k", "5", strconv.Itoa(s))
		return append(out, argv[i+1:]...), s
	}
	return argv, 0
}

// probeWSL asks wsl.exe -e true to answer within probeLimit; "" when it
// did.
func probeWSL() string {
	ctx, cancel := context.WithTimeout(context.Background(), probeLimit)
	defer cancel()
	// --cd into the current directory (on the Windows drive) and list it:
	// WSL can answer while its file bridge is stuck (2026-11-12, a process
	// hung in p9_client_rpc), and every Linux case runs through that bridge.
	args := []string{"-e", "true"}
	if wd, err := os.Getwd(); err == nil {
		args = []string{"--cd", wd, "-e", "ls", "-d", "."}
	}
	cmd := exec.CommandContext(ctx, "wsl.exe", args...)
	cmd.WaitDelay = 5 * time.Second
	out, err := cmd.CombinedOutput()
	switch {
	case ctx.Err() != nil:
		return fmt.Sprintf("wsl.exe %s did not answer within %v", strings.Join(args, " "), probeLimit)
	case err != nil:
		return fmt.Sprintf("wsl.exe %s: %v %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return ""
}
