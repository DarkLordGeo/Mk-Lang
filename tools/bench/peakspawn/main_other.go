// Why: peakspawn is a Linux tool, but its package must still build on the
// other systems the repository is tested on.
// What: a main that refuses to run.
// How: prints that the spawner runs on Linux only and exits 125.

//go:build !linux

// Command peakspawn is tools/bench's Linux peak-memory spawner (main.go).
// Elsewhere it starts nothing: Windows reads a program's own counters from
// its handle (tools/bench/mem_windows.go), and no other system reads a peak.
package main

import "os"

func main() {
	os.Stderr.WriteString("peakspawn: the peak spawner runs on Linux only\n")
	os.Exit(125)
}
