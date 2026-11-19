// Why: the time from an edit to a diagnostic or a program is what a user of
// a large Mk workspace waits for, and only a workspace at engine scale shows
// it.
//
// What: `tools/bench editloop`: editLoop, editSequence, checkSeesBody,
// staleBuild, renderEditLoop and writeEditJSON, on the synthetic workspace
// tools/synth writes.
//
// How: for each mk binary and each mode (`mk check` or `mk build`), one
// sequence is: tools/synth -edit reset, a cold run in a fresh MK_CACHE, a
// no-op rerun, a one-line body edit in a leaf module (-edit body), a
// one-line edit to a public signature every package imports (-edit sig), and
// -edit reset again. Every run is timed (wall clock) and its peak working
// set read (the mk process only: the clang and linker processes it starts
// are not counted). The binaries and modes are interleaved within each
// round, and each round's sequences start from fresh caches, so no figure
// reuses another sequence's cache and a check never shares a cache with a
// build.
//
// This is a development measurement: it holds no machine lock and never
// writes tests/bench/results, so every figure it prints is provisional,
// labelled with who measured it, when, on what, and with which commands. A
// published figure needs a run on an idle machine.
//
//	tools/bench editloop -mk <mk> [-vs label=<mk>,...] -ws DIR [-lines 1000000] [-runs 3]
//	                     [-modes check,build] [-by NAME] [-synth <synth exe>] [-jobs N] [-json FILE]

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// editSteps is one sequence's timed steps, in order, and the tools/synth
// edit applied before each ("" for none).
var editSteps = []struct{ Name, Edit string }{
	{"cold", ""},
	{"noop", "noop"},
	{"body", "body"},
	{"sig", "sig"},
}

// editStepNames are the steps as the table names them.
var editStepNames = map[string]string{
	"cold": "cold (fresh MK_CACHE)",
	"noop": "no-op rerun",
	"body": "one-line body edit, leaf module",
	"sig":  "one-line public signature edit, imported by every package",
}

type editOptions struct {
	Root  string
	Work  string
	WS    string   // the synthetic workspace
	Lines int      // its size, when it must be generated
	Synth string   // a tools/synth executable ("" builds one into Work)
	Mks   []realMk // Mks[0] is the mk measured
	Modes []string // "check", "build"
	Runs  int
	Jobs  int // mk's -j (0: mk's default)
	By    string
	Log   func(string)
}

// editRun is one timed step of one sequence.
type editRun struct {
	Mk       string  `json:"mk"`
	Mode     string  `json:"mode"`
	Round    int     `json:"round"`
	Step     string  `json:"step"`
	Seconds  float64 `json:"seconds"`
	PeakWS   uint64  `json:"peak_working_set_bytes"`
	Written  int     `json:"synth_files_written"`      // .mk files the step's edit wrote
	Output   string  `json:"program_output,omitempty"` // build mode: what the built program printed (untimed)
	MkOutput string  `json:"mk_output,omitempty"`      // what mk printed
}

type editResults struct {
	Label    string    `json:"label"`
	Started  string    `json:"started"`
	Ended    string    `json:"ended"`
	Machine  string    `json:"machine"`
	OS       string    `json:"os"`
	Busy     []string  `json:"busy_processes"`
	WS       string    `json:"workspace"`
	Lines    int       `json:"lines"`
	Synth    string    `json:"synth_json"`
	Mks      []realMk  `json:"mks"`
	Commands []string  `json:"commands"`
	Runs     []editRun `json:"runs"`
}

// editRunner runs one mk command and returns its wall time, peak working
// set and combined output (an error for a nonzero exit, with the output);
// set; synthRunner applies one tools/synth edit and returns its output. Tests
// replace both.
var (
	editRunner  = runMkPeak
	synthRunner = runSynth
	progRunner  = func(exe string) (string, error) { out, _, err := runOnce(exe, nil); return out, err }
)

func runMkPeak(c buildCmd) (time.Duration, uint64, string, error) {
	cmd := exec.Command(c.Args[0], c.Args[1:]...)
	cmd.Dir = c.Dir
	cmd.Env = compileEnv(os.Environ(), c.Env)
	var out strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &out
	start := time.Now()
	p, err := runWithPeak(cmd)
	d := time.Since(start)
	if err != nil {
		return d, 0, out.String(), fmt.Errorf("%s: %v: %s", strings.Join(c.Args[1:], " "), err, firstLines(out.String(), 12))
	}
	return d, p.RSS, out.String(), nil
}

func runSynth(exe string, args ...string) (string, error) {
	b, err := exec.Command(exe, args...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("synth %s: %v: %s", strings.Join(args, " "), err, firstLines(string(b), 6))
	}
	return string(b), nil
}

var synthWritten = regexp.MustCompile(`: (\d+) files written`)

// editWritten is the count of .mk files a synth edit says it wrote; a
// noop writes none ("nothing written").
func editWritten(out string) (int, error) {
	if strings.Contains(out, "noop: nothing written") {
		return 0, nil
	}
	m := synthWritten.FindStringSubmatch(out)
	if m == nil {
		return 0, fmt.Errorf("synth output %q names no file count", firstLines(out, 2))
	}
	return strconv.Atoi(m[1])
}

// editCmd is the mk command of a mode, run in the workspace's root.
func editCmd(o editOptions, m realMk, mode, cache string) buildCmd {
	args := []string{m.Path, mode}
	if o.Jobs > 0 {
		args = append(args, "-j", strconv.Itoa(o.Jobs))
	}
	if mode == "build" {
		args = append(args, "-o", filepath.Join(filepath.Dir(cache), "lux"+exeSuffix()))
	}
	// GOMAXPROCS and GOGC cleared: mk is a Go program, and a caller's
	// throttle would measure the throttle. MK_FRAME_POINTERS cleared: the
	// build measures mk's own default.
	return buildCmd{Dir: o.WS, Env: []string{"MK_CACHE=" + cache, "GOMAXPROCS=", "GOGC=", "MK_FRAME_POINTERS="},
		Args: append(args, ".")}
}

// editSequence runs one mk and mode's sequence in a fresh cache under
// Work, then deletes that cache. Any failure ends the sequence.
func editSequence(o editOptions, m realMk, mode string, round int) ([]editRun, error) {
	dir := filepath.Join(o.Work, "editloop", fmt.Sprintf("%s-%s-%d", m.Label, mode, round))
	if err := os.RemoveAll(dir); err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir) // the 1M caches are gigabytes (only this sequence's directory, under -work)
	cache := filepath.Join(dir, "mkcache")
	if _, err := synthRunner(o.Synth, "-out", o.WS, "-edit", "reset"); err != nil {
		return nil, err
	}
	var runs []editRun
	for _, s := range editSteps {
		written := 0
		if s.Edit != "" {
			out, err := synthRunner(o.Synth, "-out", o.WS, "-edit", s.Edit)
			if err != nil {
				return runs, err
			}
			if written, err = editWritten(out); err != nil {
				return runs, err
			}
			if (s.Edit == "noop") != (written == 0) {
				return runs, fmt.Errorf("synth -edit %s wrote %d files", s.Edit, written)
			}
		}
		d, peak, mkOut, err := editRunner(editCmd(o, m, mode, cache))
		if err != nil {
			return runs, fmt.Errorf("%s %s round %d, %s: %w", m.Label, mode, round, s.Name, err)
		}
		r := editRun{Mk: m.Label, Mode: mode, Round: round, Step: s.Name, Seconds: d.Seconds(), PeakWS: peak, Written: written, MkOutput: mkOut}
		// A no-op rerun says what the cold run said: mk's own output, not
		// only synth's (nothing written), shows nothing changed.
		if s.Name == "noop" && len(runs) > 0 && mkOut != runs[len(runs)-1].MkOutput {
			return runs, fmt.Errorf("%s %s round %d: the no-op run printed %q, the cold run %q", m.Label, mode, round, firstLines(mkOut, 4), firstLines(runs[len(runs)-1].MkOutput, 4))
		}
		if mode == "build" {
			// The built program prints a sum over the workspace that the
			// body edit changes (tools/synth: the edited line's constant),
			// so a stale build fails the sequence instead of being timed.
			out, err := progRunner(filepath.Join(filepath.Dir(cache), "lux"+exeSuffix()))
			if err != nil {
				return runs, fmt.Errorf("%s %s round %d, %s: running the built program: %w", m.Label, mode, round, s.Name, err)
			}
			r.Output = out
			if err := staleBuild(runs, r); err != nil {
				return runs, fmt.Errorf("%s %s round %d: %w", m.Label, mode, round, err)
			}
		}
		runs = append(runs, r)
		o.Log(fmt.Sprintf("%s %s round %d %s: %s, peak %s", m.Label, mode, round, s.Name, fmtSec(r.Seconds), fmtGiB(peak)))
		if mode == "check" && s.Name == "body" {
			// Untimed: the warm check must see the edited body. The edit's
			// line gets an unknown name, which the check must report; then
			// the line goes back and the check must pass again.
			if err := checkSeesBody(o, m, cache, round); err != nil {
				return runs, fmt.Errorf("%s check round %d, body: %w", m.Label, round, err)
			}
		}
	}
	if _, err := synthRunner(o.Synth, "-out", o.WS, "-edit", "reset"); err != nil {
		return runs, err
	}
	return runs, nil
}

// bodyEditLine0 is tools/synth's first body revision's constant (emit.go
// bodyLine0): the body edit's line is "var acc: i64 = <bodyEditLine0 +
// revision>" in the one file the edit wrote.
const bodyEditLine0 = 7

// checkSeesBody is check mode's stale guard after the body edit: in the
// newest .mk file of the workspace (the one the edit wrote), the edited
// line's constant becomes an unknown name, and an untimed check must fail
// naming it; the file is put back and an untimed check must pass.
func checkSeesBody(o editOptions, m realMk, cache string, round int) error {
	var mk synthMarker
	b, err := os.ReadFile(filepath.Join(o.WS, "synth.json"))
	if err == nil {
		err = json.Unmarshal(b, &mk)
	}
	if err != nil {
		return fmt.Errorf("synth.json: %v", err)
	}
	file, err := newestMk(o.WS)
	if err != nil {
		return err
	}
	orig, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	line := fmt.Sprintf("var acc: i64 = %d\n", bodyEditLine0+mk.Body)
	if strings.Count(string(orig), line) != 1 {
		return fmt.Errorf("%s: the body edit's line %q is not there once", file, strings.TrimSpace(line))
	}
	token := fmt.Sprintf("mkbenchUndefined%d", round)
	bad := strings.Replace(string(orig), line, "var acc: i64 = "+token+"\n", 1)
	if err := os.WriteFile(file, []byte(bad), 0o644); err != nil {
		return err
	}
	_, _, out, err := editRunner(editCmd(o, m, "check", cache))
	if werr := os.WriteFile(file, orig, 0o644); werr != nil {
		return werr
	}
	if err == nil || !strings.Contains(out, "`"+token+"`") {
		return fmt.Errorf("a check of the body with an unknown name did not report %s (a stale check): %s", token, firstLines(out, 6))
	}
	if _, _, out, err := editRunner(editCmd(o, m, "check", cache)); err != nil {
		return fmt.Errorf("the check after putting the body back failed: %v: %s", err, firstLines(out, 6))
	}
	return nil
}

// marker is the part of tools/synth's synth.json the guard reads.
type synthMarker struct {
	Body int `json:"body"`
}

// newestMk is the most recently written .mk file under dir, gen/ left out.
func newestMk(dir string) (string, error) {
	var best string
	var at time.Time
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && d.Name() == "gen" {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(p, ".mk") {
			return nil
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		if fi.ModTime().After(at) {
			best, at = p, fi.ModTime()
		}
		return nil
	})
	if err == nil && best == "" {
		err = fmt.Errorf("no .mk file under %s", dir)
	}
	return best, err
}

// staleBuild checks a build step's program output against the steps before
// it: a no-op rebuild prints what the cold build printed, and a body edit
// prints something else.
func staleBuild(prev []editRun, r editRun) error {
	if len(prev) == 0 {
		return nil
	}
	last := prev[len(prev)-1]
	switch r.Step {
	case "noop":
		if r.Output != last.Output {
			return fmt.Errorf("the no-op rebuild's program printed %q, the cold build's %q", r.Output, last.Output)
		}
	case "body":
		if r.Output == last.Output {
			return fmt.Errorf("after the body edit the program still prints %q (a stale build)", r.Output)
		}
	}
	return nil
}

func fmtGiB(b uint64) string {
	if b == 0 {
		return "-"
	}
	return fmt.Sprintf("%.2f GiB", float64(b)/(1<<30))
}

// editLoop is `tools/bench editloop`.
func editLoop(o editOptions, w io.Writer) (*editResults, error) {
	if len(o.Mks) == 0 {
		return nil, errors.New("editloop: no mk")
	}
	if o.Runs < 1 {
		return nil, errors.New("editloop: -runs must be at least 1")
	}
	if o.By == "" {
		return nil, errors.New("editloop: -by must name who measures")
	}
	for _, mode := range o.Modes {
		if mode != "check" && mode != "build" {
			return nil, fmt.Errorf("editloop: mode %q: check or build", mode)
		}
	}
	seen := map[string]bool{}
	for _, m := range o.Mks {
		if seen[m.Label] || m.Label == "" || strings.ContainsAny(m.Label, `/\: `) {
			return nil, fmt.Errorf("editloop: the label %q is repeated or names a directory", m.Label)
		}
		seen[m.Label] = true
	}
	if o.Synth == "" {
		o.Synth = filepath.Join(o.Work, "synth"+exeSuffix())
		cmd := exec.Command("go", "build", "-o", o.Synth, "./tools/synth")
		cmd.Dir = o.Root
		if b, err := cmd.CombinedOutput(); err != nil {
			return nil, fmt.Errorf("building tools/synth: %v: %s", err, firstLines(string(b), 6))
		}
	}
	if _, err := os.Stat(filepath.Join(o.WS, "synth.json")); err != nil {
		o.Log(fmt.Sprintf("generating the workspace: synth -out %s -lines %d", o.WS, o.Lines))
		if _, err := synthRunner(o.Synth, "-out", o.WS, "-lines", strconv.Itoa(o.Lines)); err != nil {
			return nil, err
		}
	}
	marker, _ := os.ReadFile(filepath.Join(o.WS, "synth.json"))
	lines, err := countMkLines(o.WS)
	if err != nil {
		return nil, err
	}
	res := &editResults{Started: time.Now().Format("2006-01-02 15:04 MST"), WS: o.WS, Lines: lines,
		Synth: strings.TrimSpace(string(marker)), OS: "windows"}
	f := gatherFacts(o.Root, &toolchain{versions: map[string]string{}})
	res.Machine, res.OS, res.Busy = f.CPU, f.OS, f.Busy
	for i := range o.Mks {
		m := &o.Mks[i]
		m.SHA256 = fileSHA(m.Path)
		m.Version = mkIdentity(m.Path, output(m.Path, "version"))
	}
	res.Mks = o.Mks
	for _, mode := range o.Modes {
		res.Commands = append(res.Commands, strings.Join(editCmd(o, realMk{Path: "mk"}, mode, "<fresh MK_CACHE per sequence>").Args, " ")+
			" (in the workspace; MK_CACHE fresh per sequence; GOMAXPROCS, GOGC and MK_FRAME_POINTERS cleared)")
	}
	for r := 1; r <= o.Runs; r++ {
		for _, mode := range o.Modes {
			// Rotate which binary runs first in each round.
			for k := range o.Mks {
				m := o.Mks[(k+r-1)%len(o.Mks)]
				runs, err := editSequence(o, m, mode, r)
				res.Runs = append(res.Runs, runs...)
				if err != nil {
					res.Ended = time.Now().Format("2006-01-02 15:04 MST")
					res.Label = fmt.Sprintf("measured by %s, %s to %s (FAILED, incomplete), busy PC, non-idle, provisional", o.By, res.Started, res.Ended)
					return res, err
				}
			}
		}
	}
	res.Ended = time.Now().Format("2006-01-02 15:04 MST")
	res.Label = fmt.Sprintf("measured by %s, %s to %s, busy PC, non-idle, provisional", o.By, res.Started, res.Ended)
	return res, nil
}

// countMkLines is the lines of the workspace's .mk files.
func countMkLines(dir string) (int, error) {
	n := 0
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && d.Name() == "gen" {
			return filepath.SkipDir
		}
		if !d.IsDir() && strings.HasSuffix(p, ".mk") {
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			n += strings.Count(string(b), "\n")
		}
		return nil
	})
	return n, err
}

// renderEditLoop is the provisional edit-loop table: per mk, mode and step,
// the median and range of the rounds' wall times and the highest peak
// working set, then each other mk's median over the first's.
func renderEditLoop(w io.Writer, res *editResults) {
	fmt.Fprintf(w, "The edit loop at engine scale: PROVISIONAL (%s; no machine lock; never published)\n", orDash(res.Label))
	fmt.Fprintf(w, "Machine: %s; %s. Other compilers and test binaries at the start: %s.\n", orDash(res.Machine), orDash(res.OS), orDash(strings.Join(res.Busy, ", ")))
	fmt.Fprintf(w, "Workspace: %s, %d lines of .mk (synth.json %s).\n", res.WS, res.Lines, strings.Join(strings.Fields(res.Synth), " "))
	for _, m := range res.Mks {
		fmt.Fprintf(w, "mk %s: %s (%s, sha256 %s)\n", m.Label, m.Path, m.Version, short(m.SHA256))
	}
	for _, c := range res.Commands {
		fmt.Fprintf(w, "command: %s\n", c)
	}
	fmt.Fprintf(w, "\n| mode | step | mk | runs | median | min to max | peak working set (highest) |")
	if len(res.Mks) > 1 {
		fmt.Fprintf(w, " / %s |", res.Mks[0].Label)
	}
	fmt.Fprintln(w)
	fmt.Fprintf(w, "|---|---|---|---|---|---|---|")
	if len(res.Mks) > 1 {
		fmt.Fprintf(w, "---|")
	}
	fmt.Fprintln(w)
	for _, mode := range []string{"check", "build"} {
		for _, s := range editSteps {
			var head float64
			for i, m := range res.Mks {
				var xs []float64
				var peak uint64
				for _, r := range res.Runs {
					if r.Mk == m.Label && r.Mode == mode && r.Step == s.Name {
						xs = append(xs, r.Seconds)
						peak = max(peak, r.PeakWS)
					}
				}
				if len(xs) == 0 {
					continue
				}
				sum := summarize(xs)
				fmt.Fprintf(w, "| %s | %s | %s | %d | %s | %s to %s | %s |", mode, editStepNames[s.Name], m.Label, len(xs),
					fmtSec(sum.Median), fmtSec(sum.Min), fmtSec(sum.Max), fmtGiB(peak))
				if i == 0 {
					head = sum.Median
				}
				if len(res.Mks) > 1 {
					ratio := ""
					if i > 0 && head > 0 {
						ratio = fmt.Sprintf("%.2f", sum.Median/head)
					}
					fmt.Fprintf(w, " %s |", ratio)
				}
				fmt.Fprintln(w)
			}
		}
	}
}

// writeEditJSON writes the raw runs.
func writeEditJSON(path string, res *editResults) error {
	b, err := json.MarshalIndent(res, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}
