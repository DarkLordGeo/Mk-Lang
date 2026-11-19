// Why: many diagnostics carry a fix the compiler can prove safe; applying
// them by hand is slow and error-prone.
//
// What: runFix, the `mk fix [--dry-run] [path]` command, and fixJSON.
//
// How: proven fixes applied to a fixed point, each checked by recompiling; the
// chain is printed (round, phase, code, place, why), then what is left with
// its offered fixes, which mk fix never applies. Under --json stdout is one
// object holding the chain and that final report (fixJSON). Exit 0 when the
// package is clean at the end, 1 when diagnostics remain, 2 on a usage
// error.

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"

	"mk/internal/driver"
)

func init() {
	register(command{"fix", "fix     [--dry-run] [--canonical] [--std=module] [path]  apply proven fixes to a fixed point and report the chain", runFix})
}

func runFix(args []string, stdout, stderr io.Writer) int {
	var cf compileFlags
	fs := newFlagSet("fix", stderr, &cf)
	dry := fs.Bool("dry-run", false, "compute the chain and write nothing")
	canonical := fs.Bool("canonical", false, "then the canonical rounds of doc/style.md")
	stdMod := fs.String("std", "", "rewrite std's own files of this module and the modules under it (std for all), under MK_STD; takes no path")
	path, _, err := parseArgs(fs, args, false)
	if err != nil {
		return usageErr(stderr, fs, err)
	}
	cfg, ok := cf.config(path, stderr)
	if !ok {
		return 2
	}
	cfg.StdTests = *stdMod
	s, err := driver.NewSession(cfg)
	if err != nil {
		fmt.Fprintf(stderr, "mk fix: %v\n", err)
		return 2
	}
	res, err := s.Fix(driver.FixOptions{DryRun: *dry, Canonical: *canonical})
	if err != nil {
		fmt.Fprintf(stderr, "mk fix: %v\n", err)
		return 2
	}
	clean := len(res.Final.Report.Diags) == 0
	if cf.json {
		// Under --json stdout is one JSON object, the chain and the
		// final report together, and nothing else.
		if _, err := stdout.Write(fixJSON(res, *dry)); err != nil {
			fmt.Fprintf(stderr, "mk fix: %v\n", err)
			return 3
		}
		if clean {
			return 0
		}
		return 1
	}
	for i, r := range res.Rounds {
		noun := "diagnostics"
		if r.Diags == 1 {
			noun = "diagnostic"
		}
		if driver.IsRewriteRound(r.Phase) {
			// A rewrite round (the short form of Print-shaped calls) runs on a
			// clean package: no diagnostic and no code to show.
			fmt.Fprintf(stdout, "round %d (%s):\n", i+1, r.Phase)
			for _, a := range r.Applied {
				fmt.Fprintf(stdout, "  %s: %s\n", a.At, a.Why)
			}
			continue
		}
		fmt.Fprintf(stdout, "round %d (%s, %d %s):\n", i+1, r.Phase, r.Diags, noun)
		for _, a := range r.Applied {
			fmt.Fprintf(stdout, "  %s %s: %s\n", a.Code, a.At, a.Why)
		}
	}
	verb := "wrote"
	if *dry {
		verb = "would write"
	}
	for _, f := range res.Files {
		fmt.Fprintf(stdout, "%s %s\n", verb, f)
	}
	fmt.Fprintf(stdout, "stopped: %s\n", res.Stopped)
	if clean {
		return 0
	}
	_ = res.Final.WriteReport(stderr, false)
	return 1
}

// fixJSON is mk fix --json's one object: {"version": 1, "rounds": [{"phase",
// "diagnostics", "applied": [{"code", "at", "why"}]}], "files": [...],
// "written": bool, "stopped": "...", "report": <the mk check --json object
// of what is left>} and a newline.
func fixJSON(res *driver.FixResult, dry bool) []byte {
	type applied struct {
		Code string `json:"code"`
		At   string `json:"at"`
		Why  string `json:"why"`
	}
	type round struct {
		Phase       string    `json:"phase"`
		Diagnostics int       `json:"diagnostics"`
		Applied     []applied `json:"applied"`
	}
	doc := struct {
		Version int             `json:"version"`
		Rounds  []round         `json:"rounds"`
		Files   []string        `json:"files"`
		Written bool            `json:"written"`
		Stopped string          `json:"stopped"`
		Report  json.RawMessage `json:"report"`
	}{Version: driver.ReportJSONVersion, Rounds: []round{}, Files: []string{}, Written: !dry && len(res.Files) > 0, Stopped: res.Stopped,
		Report: json.RawMessage(bytes.TrimSuffix(res.Final.ReportJSON(), []byte("\n")))}
	for _, r := range res.Rounds {
		rd := round{Phase: r.Phase, Diagnostics: r.Diags, Applied: []applied{}}
		for _, a := range r.Applied {
			code := a.Code.String()
			if driver.IsRewriteRound(r.Phase) {
				code = "" // a rewrite of a clean package, no diagnostic
			}
			rd.Applied = append(rd.Applied, applied{Code: code, At: a.At, Why: a.Why})
		}
		doc.Rounds = append(doc.Rounds, rd)
	}
	doc.Files = append(doc.Files, res.Files...)
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(doc) // the object and a newline
	return b.Bytes()
}
