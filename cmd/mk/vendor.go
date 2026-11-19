// Why: a package that must build without the network, or keep its
// dependencies under review, needs their sources in its own tree.
//
// What: runVendor, the `mk vendor [--offline] [path]` command.
//
// How: mk vendor copies every dependency of the package into
// vendor/<id>/, writes vendor/index.txt and prints one line per dependency,
// "vendor/<id>  <source>".
// Exit 0, 1 (diagnostics, printed as mk check prints them), 2 (usage or
// I/O), 3 (internal error).

package main

import (
	"fmt"
	"io"

	"mk/internal/driver"
)

func init() {
	register(command{"vendor", "vendor  [--offline] [path]    copy every dependency into vendor/<id>/ and write vendor/index.txt", runVendor})
}

func runVendor(args []string, stdout, stderr io.Writer) int {
	var cf compileFlags
	fs := newFlagSet("vendor", stderr, &cf)
	path, _, err := parseArgs(fs, args, false)
	if err != nil {
		return usageErr(stderr, fs, err)
	}
	cfg, ok := cf.config(path, stderr)
	if !ok {
		return 2
	}
	cfg.GenView = false
	s, err := driver.NewSession(cfg)
	if err != nil {
		fmt.Fprintf(stderr, "mk vendor: %v\n", err)
		return 2
	}
	res, err := s.Vendor()
	if err != nil {
		fmt.Fprintf(stderr, "mk vendor: %v\n", err)
		return 2
	}
	if len(res.Result.Report.Diags) > 0 {
		out := stderr
		if cf.json {
			out = stdout
		}
		if err := res.Result.WriteReport(out, cf.json); err != nil {
			fmt.Fprintf(stderr, "mk vendor: %v\n", err)
			return 2
		}
		return driver.ExitStatus(res.Result.Report)
	}
	for _, d := range res.Deps {
		fmt.Fprintf(stdout, "%s  %s\n", d.Dir, d.From)
	}
	return 0
}
