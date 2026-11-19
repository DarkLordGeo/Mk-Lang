// Why: users pick a build's trade-off by name (small, fast, or checked) and
// may want symbols in an optimized binary; the flags must refuse
// combinations that contradict each other.
//
// What: profileFlags, which applies mk build's and mk run's --profile (size,
// release or checked) and --symbols to a driver.Config.
//
// How: each profile maps to a driver setting (driver/profile.go); --release
// contradicts --profile, and --symbols needs an optimized build.

package main

import (
	"fmt"
	"io"

	"mk/internal/driver"
)

// profileFlags applies --profile and --symbols to cfg, or explains why
// not: --profile is size (a release build at -Oz), release, or checked (a
// build compiled and linked as release is, whose build.Profile is Checked
// and whose contracts are checked), which --release contradicts;
// --symbols needs an optimized build, size and checked included.
func profileFlags(cfg *driver.Config, profile string, symbols bool, stderr io.Writer) bool {
	switch profile {
	case "":
	case "release":
		cfg.Profile = driver.ProfileRelease
	case "size":
		cfg.Profile, cfg.Size = driver.ProfileRelease, true
	case "checked":
		if cfg.Profile == driver.ProfileRelease {
			fmt.Fprintln(stderr, "mk: --profile checked and --release cannot be combined: checked is optimized as release is, with contracts and build.Profile Checked")
			return false
		}
		cfg.Profile = driver.ProfileChecked
	default:
		fmt.Fprintf(stderr, "mk: unknown --profile %q (size, release or checked)\n", profile)
		return false
	}
	if symbols && !cfg.Profile.Optimized() {
		fmt.Fprintln(stderr, "mk: --symbols applies to --release, --profile size and --profile checked builds only")
		return false
	}
	cfg.Symbols = symbols
	return true
}
