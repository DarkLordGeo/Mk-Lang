// Why: the app table's peak-memory reading has a Windows and a Linux
// implementation; other systems still need runWithPeak to build.
// What: runWithPeak, which runs the program and reports no peak.
// How: cmd.Run with a zero memPeak.

//go:build !windows && !linux

package main

import "os/exec"

func runWithPeak(cmd *exec.Cmd) (memPeak, error) {
	return memPeak{}, cmd.Run()
}
