// Why: an error code in a diagnostic is only useful if the user can ask what
// it means and see it fixed.
//
// What: runExplain, the `mk explain [--json] E0501` command.
//
// How: driver.Explain looks the code up in a generated table: its trigger,
// its fix class, and one rejecting and one accepting example, printed as
// text or with --json as one object. An unknown code is a usage error
// (exit 2).

package main

import (
	"flag"
	"fmt"
	"io"

	"mk/internal/driver"
)

func init() {
	register(command{"explain", "explain [--json] E0501           a code's trigger, fix class and examples", runExplain})
}

func runExplain(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("mk explain", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "one JSON object: code, name, phase, trigger, fix_class, reject, accept")
	code, _, err := parseArgs(fs, args, false)
	if err != nil {
		return usageErr(stderr, fs, err)
	}
	if code == "" {
		return usageErr(stderr, fs, fmt.Errorf("which code? (for example mk explain E0501)"))
	}
	e, ok := driver.Explain(code)
	if !ok {
		fmt.Fprintf(stderr, "mk explain: %q is not a code of doc/errors.md\n", code)
		return 2
	}
	if *asJSON {
		_, err = stdout.Write(e.JSON())
	} else {
		_, err = io.WriteString(stdout, e.Text())
	}
	if err != nil {
		return 2
	}
	return 0
}
