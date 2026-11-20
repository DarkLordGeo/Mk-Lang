// Why: several Mk versions can be installed side by side; the user needs to
// see them, switch between them and remove old ones.
//
// What: runToolchain, the `mk toolchain list`, `use <version>` and
// `remove <version>` commands.
//
// How: the work is driver.ToolchainList and driver.ToolchainUse or
// ToolchainRemove, on the install the running mk lives in, or MK_HOME. Exit
// 0, or 2 on a usage error or when nothing was changed.

package main

import (
	"fmt"
	"io"

	"mk/internal/driver"
)

func init() {
	register(command{"toolchain", "toolchain list | use <version> | remove <version>   the installed versions and current; needs a published release (0.1.0 or later)", runToolchain})
}

func runToolchain(args []string, stdout, stderr io.Writer) int {
	usage := func() int {
		fmt.Fprintln(stderr, "usage: mk toolchain list | use <version> | remove <version>")
		return 2
	}
	if len(args) == 0 {
		return usage()
	}
	cfg := driver.ToolchainConfig{}
	switch args[0] {
	case "list":
		if len(args) != 1 {
			return usage()
		}
		list, err := driver.ToolchainList(cfg)
		if err != nil {
			fmt.Fprintf(stderr, "mk toolchain list: %v\n", err)
			return 2
		}
		for _, t := range list {
			mark := " "
			if t.Current {
				mark = "*"
			}
			fmt.Fprintf(stdout, "%s %s\n", mark, t.Version)
		}
		return 0
	case "use", "remove":
		if len(args) != 2 {
			return usage()
		}
		op := driver.ToolchainUse
		if args[0] == "remove" {
			op = driver.ToolchainRemove
		}
		if err := op(cfg, args[1]); err != nil {
			fmt.Fprintf(stderr, "mk toolchain %s: %v\n", args[0], err)
			return 2
		}
		if args[0] == "use" {
			fmt.Fprintf(stdout, "current is mk %s\n", args[1])
		} else {
			fmt.Fprintf(stdout, "removed mk %s\n", args[1])
		}
		return 0
	}
	return usage()
}
