// Why: installing a new Mk release should be one command that never leaves
// a half-installed toolchain behind.
//
// What: runUpgrade, the `mk upgrade [version]` command.
//
// How: driver.Upgrade fetches a signed release, checks it, unpacks it beside
// the others and moves current. MK_RELEASES overrides the compiled-in
// release address and MK_HOME the install root. Exit 0, or 2 when nothing
// changed.

package main

import (
	"fmt"
	"io"

	"mk/internal/driver"
)

func init() {
	register(command{"upgrade", "upgrade [version]              install a signed release (the newest by default) and make it current; needs a published release (0.1.0 or later)", runUpgrade})
}

func runUpgrade(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("upgrade", stderr, nil)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 1 {
		fmt.Fprintln(stderr, "usage: mk upgrade [version]")
		return 2
	}
	res, err := driver.Upgrade(driver.UpgradeConfig{Version: fs.Arg(0)})
	if err != nil {
		fmt.Fprintln(stderr, driver.UpgradeFailure(res, err))
		return 2
	}
	switch {
	case !res.Installed:
		fmt.Fprintf(stdout, "mk %s -> %s: unpacked to %s; PATH was not changed (put its bin directory on PATH to use it)\n", res.Old, res.New, res.Dir)
	case res.Reused:
		fmt.Fprintf(stdout, "mk %s -> %s (already unpacked in %s; current moved)\n", res.Old, res.New, res.Dir)
	default:
		fmt.Fprintf(stdout, "mk %s -> %s (%s; current moved)\n", res.Old, res.New, res.Dir)
	}
	return 0
}
