// Why: timed runs pin to one CPU, but off Windows Go cannot set the affinity
// mask without the unsafe package.
//
// What: setAffinity for every system but Windows, which always fails.
//
// How: on Linux the mask is set from outside: the whole Linux table runs as
// one `taskset -c 2` invocation inside WSL, because sched_setaffinity needs a
// pointer that only the unsafe package could pass.

//go:build !windows

package main

import "fmt"

func setAffinity(mask uint64) error {
	return fmt.Errorf("set the affinity from outside on this system (taskset -c 2)")
}
