// Why: whole programs are timed from outside, by hyperfine, so every
// language's process start and exit is part of its time.
//
// What: hfOptions, hyperfineCell and hyperfineCellEnv (run one cell and
// return its wall times) and hyperfineCmd, the command they run.
//
// How: no shell (-N), 3 warm-up runs, at least 10 runs, JSON export.
// hyperfine 2.0 writes schema 2 (results[].measurements[]
// .time_wall_clock.value); schema 1 (results[].times) is read too.

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// hfOptions are one hyperfine invocation's settings.
type hfOptions struct {
	Warmup int
	Runs   int
	// Prepare is run by hyperfine before every run, warm-up included (an
	// app row's output file deleted, prepareCmd); never in results.json.
	Prepare string `json:"-"`
}

// quoteArg quotes a command word for hyperfine's shell-style splitting.
func quoteArg(s string) string {
	if s != "" && !strings.ContainsAny(s, " \t\"'\\") {
		return s
	}
	return `"` + strings.ReplaceAll(strings.ReplaceAll(s, `\`, `\\`), `"`, `\"`) + `"`
}

// hyperfineCell times exe with args and writes the JSON to jsonPath. It
// returns the wall times in seconds.
func hyperfineCell(hf, exe string, args []string, opt hfOptions, jsonPath string) ([]float64, error) {
	return hyperfineCellEnv(hf, exe, args, opt, jsonPath, runEnv(os.Environ()))
}

// hyperfineCellEnv is hyperfineCell with the program's environment given
// (rowEnv); hyperfine -N passes its own environment on.
func hyperfineCellEnv(hf, exe string, args []string, opt hfOptions, jsonPath string, env []string) ([]float64, error) {
	cmd := hyperfineCmd(hf, exe, args, opt, jsonPath)
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("hyperfine: %v: %s", err, firstLines(string(out), 20))
	}
	b, err := os.ReadFile(jsonPath)
	if err != nil {
		return nil, err
	}
	times, err := parseHyperfine(b)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", jsonPath, err)
	}
	return times, nil
}

// hyperfineCmd is hyperfineCell's command. With -N hyperfine starts the
// program itself, which inherits hyperfine's environment: runEnv, so the
// caller's GOGC, GODEBUG or MK_CARRIERS cannot reach a timed program.
func hyperfineCmd(hf, exe string, args []string, opt hfOptions, jsonPath string) *exec.Cmd {
	words := []string{quoteArg(exe)}
	for _, a := range args {
		words = append(words, quoteArg(a))
	}
	args = []string{"-N", "--style", "basic", "--warmup", fmt.Sprint(opt.Warmup), "--runs", fmt.Sprint(opt.Runs)}
	if opt.Prepare != "" {
		args = append(args, "--prepare", opt.Prepare)
	}
	cmd := exec.Command(hf, append(args, "--export-json", jsonPath, strings.Join(words, " "))...)
	cmd.Env = runEnv(os.Environ())
	return cmd
}

// parseHyperfine reads the wall times of the first result of a hyperfine
// JSON export, schema 2 or 1.
func parseHyperfine(b []byte) ([]float64, error) {
	var doc struct {
		Results []struct {
			Times        []float64 `json:"times"`
			Measurements []struct {
				Wall struct {
					Value float64 `json:"value"`
					Unit  string  `json:"unit"`
				} `json:"time_wall_clock"`
				ExitCode *int `json:"exit_code"`
			} `json:"measurements"`
		} `json:"results"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		return nil, err
	}
	if len(doc.Results) == 0 {
		return nil, fmt.Errorf("no results")
	}
	r := doc.Results[0]
	if len(r.Measurements) == 0 {
		if len(r.Times) == 0 {
			return nil, fmt.Errorf("no measurements")
		}
		return r.Times, nil
	}
	var times []float64
	for _, m := range r.Measurements {
		if m.ExitCode != nil && *m.ExitCode != 0 {
			return nil, fmt.Errorf("a run exited %d", *m.ExitCode)
		}
		v := m.Wall.Value
		switch m.Wall.Unit {
		case "second", "s", "":
		case "millisecond", "ms":
			v /= 1e3
		case "microsecond", "us":
			v /= 1e6
		default:
			return nil, fmt.Errorf("unit %q", m.Wall.Unit)
		}
		times = append(times, v)
	}
	return times, nil
}

// resumeRaw returns the wall times of a raw export that a dead publish of
// the same date left at jsonPath, when it may stand for this run's cell: it
// names the same program and arguments (hyperfine's command string, as
// hyperfineCmd writes it), it holds exactly runs times, and it was written
// after the program was built, so it timed these very bytes (a stamped build
// that is not rebuilt keeps its older time). Anything else is timed again.
func resumeRaw(jsonPath, exe string, args []string, runs int) ([]float64, bool) {
	st, err := os.Stat(jsonPath)
	if err != nil {
		return nil, false
	}
	pst, err := os.Stat(exe)
	if err != nil || !st.ModTime().After(pst.ModTime()) {
		return nil, false
	}
	b, err := os.ReadFile(jsonPath)
	if err != nil {
		return nil, false
	}
	var doc struct {
		Results []struct {
			Command string `json:"command"`
		} `json:"results"`
	}
	if json.Unmarshal(b, &doc) != nil || len(doc.Results) != 1 {
		return nil, false
	}
	words := []string{quoteArg(exe)}
	for _, a := range args {
		words = append(words, quoteArg(a))
	}
	if doc.Results[0].Command != strings.Join(words, " ") {
		return nil, false
	}
	ts, err := parseHyperfine(b)
	if err != nil || len(ts) != runs {
		return nil, false
	}
	return ts, true
}
