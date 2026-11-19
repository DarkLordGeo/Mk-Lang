// Why: one canonical layout for all Mk code ends style debates and keeps
// diffs small.
//
// What: runFmt, the `mk fmt [--check|-l] [path]` command: one format, no
// options.
//
// How: mk fmt [path] rewrites every .mk file under path (default the
// working directory) that is not in its format and prints each rewritten
// path; --check (or -l) prints them and writes nothing. A file that does
// not parse is reported and left alone. Exit 0, 1 (a syntax error, or a
// file to change under --check), 2 (usage), 3 (internal error).

package main

import (
	"flag"
	"fmt"
	"io"

	"mk/internal/driver"
)

func init() {
	register(command{"fmt", "fmt     [--check] [path]       format the .mk files under path in place (one format, no options)", runFmt})
}

func runFmt(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("mk fmt", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var check bool
	fs.BoolVar(&check, "check", false, "list the files that are not formatted and write nothing; exit 1 if any")
	fs.BoolVar(&check, "l", false, "same as --check")
	path, _, err := parseArgs(fs, args, false)
	if err != nil {
		return usageErr(stderr, fs, err)
	}
	res, err := driver.FormatPath(path, driver.FmtOptions{Check: check})
	if err != nil {
		fmt.Fprintf(stderr, "mk fmt: %v\n", err)
		return 2
	}
	for _, f := range res.Files {
		switch {
		case f.Err != nil:
			fmt.Fprintf(stderr, "%s: %v\n", f.Path, f.Err)
		case f.Changed:
			fmt.Fprintln(stdout, f.Path)
		}
	}
	return res.ExitStatus(check)
}
