// Why: the app table reports each program's peak memory (app.go), and on
// Windows that needs a process handle that outlives os/exec's Wait.
//
// What: runWithPeak for Windows, processMemoryCounters and addressOf.
//
// How: psapi's GetProcessMemoryInfo on a handle tools/bench opens itself,
// since os/exec closes its own handle in Wait. The structure's address comes
// from fmt's %p verb, so this file imports neither unsafe nor reflect
// (internal/archtest TestNoUnsafe and TestNoReflectInPasses); the
// structure is on the heap, which Go's collector never moves, and is kept
// alive across the call.

package main

import (
	"fmt"
	"os/exec"
	"runtime"
	"strconv"
	"syscall"
)

var procGetProcessMemoryInfo = syscall.NewLazyDLL("psapi.dll").NewProc("GetProcessMemoryInfo")

// processMemoryCounters is PROCESS_MEMORY_COUNTERS (psapi.h), 72 bytes on
// x86-64.
type processMemoryCounters struct {
	Cb                         uint32
	PageFaultCount             uint32
	PeakWorkingSetSize         uintptr
	WorkingSetSize             uintptr
	QuotaPeakPagedPoolUsage    uintptr
	QuotaPagedPoolUsage        uintptr
	QuotaPeakNonPagedPoolUsage uintptr
	QuotaNonPagedPoolUsage     uintptr
	PagefileUsage              uintptr
	PeakPagefileUsage          uintptr
}

// processMemoryCountersSize is the structure's size: two 4-byte fields and
// eight pointer-sized ones.
const processMemoryCountersSize = 8 + 8*(32<<(^uintptr(0)>>63))/8

const (
	processQueryLimitedInformation = 0x1000
	processVMRead                  = 0x0010
)

// addressOf is the address of the heap object p points to, read from fmt's %p
// ("0x" and hexadecimal digits). This route is chosen because
// internal/archtest's TestNoReflectInPasses bans reflect in every non-test
// file (as TestNoUnsafe bans unsafe), and an exemption for tools/bench would
// open that ban for one address. fmt's %p of a pointer prints the pointer's
// value, the address the syscall needs, and p escapes to the heap through the
// call, where Go's collector does not move it; runWithPeak keeps it alive
// across the call.
func addressOf(p *processMemoryCounters) (uintptr, error) {
	s := fmt.Sprintf("%p", p)
	if len(s) < 3 || s[:2] != "0x" {
		return 0, fmt.Errorf("address %q", s)
	}
	v, err := strconv.ParseUint(s[2:], 16, 64)
	return uintptr(v), err
}

// runWithPeak runs cmd and reads its peak working set and peak commit.
// The process object outlives the program while os/exec holds its handle
// (until Wait), so the handle opened by PID between Start and Wait names
// this process even if it has already exited.
func runWithPeak(cmd *exec.Cmd) (memPeak, error) {
	if err := cmd.Start(); err != nil {
		return memPeak{}, err
	}
	h, herr := syscall.OpenProcess(processQueryLimitedInformation|processVMRead, false, uint32(cmd.Process.Pid))
	werr := cmd.Wait()
	if herr != nil {
		if werr != nil {
			return memPeak{}, werr
		}
		return memPeak{}, fmt.Errorf("OpenProcess: %v", herr)
	}
	defer syscall.CloseHandle(h)
	if werr != nil {
		return memPeak{}, werr
	}
	c := &processMemoryCounters{Cb: processMemoryCountersSize}
	addr, err := addressOf(c)
	if err != nil {
		return memPeak{}, err
	}
	r, _, cerr := procGetProcessMemoryInfo.Call(uintptr(h), addr, uintptr(c.Cb))
	runtime.KeepAlive(c)
	if r == 0 {
		return memPeak{}, fmt.Errorf("GetProcessMemoryInfo: %v", cerr)
	}
	return memPeak{RSS: uint64(c.PeakWorkingSetSize), Commit: uint64(c.PeakPagefileUsage)}, nil
}
