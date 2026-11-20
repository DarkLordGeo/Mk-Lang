// Why: the corpus of Mk programs with expected verdicts is the main
// conformance test of the compiler; `mk corpus` runs it from the command
// line.
//
// What: runCorpus, the `mk corpus` command; its flags are corpus.Options
// (newCorpusFlagSet, --target among them) plus --strict.
//
// How: the report goes to stdout (the text report, or JSON Lines with
// --json); a root, extraction, allowlist or usage error goes to stderr with
// exit 2; otherwise the exit status is corpus.ExitStatus (0 nothing failed,
// 1 a unit failed).

package main

import (
	"fmt"
	"io"
	"strings"

	"mk/internal/corpus"
	"mk/internal/driver"
)

func runCorpus(args []string, stdout, stderr io.Writer) int {
	var c corpusFlags
	fs := newCorpusFlagSet(stderr, &c)
	// --strict is another name for --no-pending: an unlisted pending unit
	// counts as failed. tools/gate runs `mk corpus --all --strict`. It is
	// registered here, beside the command that reads it, not in
	// newCorpusFlagSet (main.go).
	var strict bool
	fs.BoolVar(&strict, "strict", false, "same as --no-pending: an unlisted pending unit counts as failed")
	if path, _, err := parseArgs(fs, args, false); err != nil || path != "" {
		if err == nil {
			err = fmt.Errorf("mk corpus takes no path; use --root")
		}
		return usageErr(stderr, fs, err)
	}
	if c.target != "" {
		if _, names, ok := driver.TargetNamed(c.target); !ok {
			fmt.Fprintf(stderr, "mk: unknown target %q (one of %s)\n", c.target, strings.Join(names, ", "))
			return 2
		}
	}
	opts := corpus.Options{Root: c.root, Conformance: c.all, Only: c.only,
		NoPending: c.noPending || strict, Build: c.build, Jobs: c.jobs, Driver: c.driverConfig(stderr)}
	res, err := corpus.Run(opts)
	if err != nil {
		fmt.Fprintf(stderr, "mk corpus: %v\n", err)
		return 2
	}
	write := corpus.WriteText
	if c.json {
		write = corpus.WriteJSON
	}
	if err := write(stdout, res); err != nil {
		fmt.Fprintf(stderr, "mk corpus: %v\n", err)
		return 2
	}
	return corpus.ExitStatus(res)
}
