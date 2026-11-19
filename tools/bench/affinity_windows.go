// Why: timings on a shared machine vary less when every timed program runs
// on one logical CPU.
//
// What: setAffinity on Windows: tools/bench pins itself to one logical CPU
// before it starts hyperfine, and every program it times inherits the mask.
//
// How: kernel32's SetProcessAffinityMask on the GetCurrentProcess
// pseudo-handle, called through syscall (no unsafe import).

package main

import (
	"fmt"
	"syscall"
)

var procSetProcessAffinityMask = syscall.NewLazyDLL("kernel32.dll").NewProc("SetProcessAffinityMask")

func setAffinity(mask uint64) error {
	h, err := syscall.GetCurrentProcess()
	if err != nil {
		return err
	}
	r, _, err := procSetProcessAffinityMask.Call(uintptr(h), uintptr(mask))
	if r == 0 {
		return fmt.Errorf("SetProcessAffinityMask(%#x): %v", mask, err)
	}
	return nil
}
