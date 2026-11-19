// Why: a library author must know before a release whether a change breaks
// the code of the library's users; `mk apidiff` answers from public facts
// alone, without building either side.
//
// What: runAPIDiff, the `mk apidiff [--json] A B` command: the public-fact
// differences between two package roots (directories or .mk files), one
// line per change with its severity; with --std, between the public modules
// of two std trees.
//
// How: the comparison and the rules for open sets, options types and
// Experimental modules live in internal/driver (driver.APIDiff and
// APIDiffStd, apidiff_rules.go). Exit 0 when nothing is
// breaking, 1 when something is (or a root does not check far enough), 2 on
// a usage error.

package main

import (
	"encoding/json"
	"fmt"
	"io"

	"mk/internal/driver"
)

func init() {
	register(command{"apidiff", "apidiff [--json] [--std] A B    public-fact differences between two package roots, or two std trees", runAPIDiff})
}

func runAPIDiff(args []string, stdout, stderr io.Writer) int {
	var cf compileFlags
	fs := newFlagSet("apidiff", stderr, &cf)
	std := fs.Bool("std", false, "A and B are std directories: compare std's public modules")
	var pos []string
	rest := args
	for {
		if err := fs.Parse(rest); err != nil {
			return usageErr(stderr, fs, err)
		}
		if fs.NArg() == 0 {
			break
		}
		pos = append(pos, fs.Arg(0))
		rest = fs.Args()[1:]
	}
	if len(pos) != 2 {
		return usageErr(stderr, fs, fmt.Errorf("want two roots, got %d", len(pos)))
	}
	ca, ok := cf.config(pos[0], stderr)
	if !ok {
		return 2
	}
	cb, _ := cf.config(pos[1], stderr)
	var res *driver.APIDiffResult
	var err error
	if *std {
		res, err = driver.APIDiffStd(ca, pos[0], pos[1])
	} else {
		res, err = driver.APIDiff(ca, cb)
	}
	if err != nil {
		fmt.Fprintf(stderr, "mk apidiff: %v\n", err)
		return 2
	}
	if res.Incomplete {
		_ = res.A.WriteReport(stderr, false)
		_ = res.B.WriteReport(stderr, false)
	}
	if cf.json {
		doc := struct {
			Changes    []driver.APIChange `json:"changes"`
			Incomplete bool               `json:"incomplete,omitempty"`
		}{res.Changes, res.Incomplete}
		if doc.Changes == nil {
			doc.Changes = []driver.APIChange{}
		}
		b, _ := json.Marshal(doc)
		fmt.Fprintln(stdout, string(b))
	} else {
		io.WriteString(stdout, res.Text())
	}
	return res.ExitStatus()
}
