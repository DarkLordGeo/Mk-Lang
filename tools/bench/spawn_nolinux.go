// Why: tools/bench re-executes itself as the peak-memory spawner only on
// Linux; elsewhere spawnMain must exist but refuse.
// What: spawnMain for every system but Linux.
// How: prints that the spawner runs on Linux only and returns 125. Windows
// reads the program's own counters from its handle (mem_windows.go): a new
// process gets a new address space there, so no spawner is needed.

//go:build !linux

package main

import (
	"fmt"
	"os"
)

func spawnMain(args []string) int {
	fmt.Fprintln(os.Stderr, "tools/bench: the peak spawner runs on Linux only")
	return 125
}
