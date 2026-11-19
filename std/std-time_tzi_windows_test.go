// Why: Zone.Local on Windows must be compared with the per-year zone records it
// reads, not with Go's time.Local, which applies the record in force to every year.
// What: the zone records from kernel32, and a child process's peak commit for
// TestStdTime_LoadReadsTheFileSize.
// How: GetDynamicTimeZoneInformation and GetTimeZoneInformationForYear, called
// as Zone.Local calls them; pointers go through reflect, so no unsafe import.

//go:build windows

package std

import (
	"encoding/binary"
	"os/exec"
	"reflect"
	"runtime"
	"syscall"
	"testing"
	"unicode/utf16"
)

var (
	time2K32     = syscall.NewLazyDLL("kernel32.dll")
	time2DynProc = time2K32.NewProc("GetDynamicTimeZoneInformation")
	time2YrProc  = time2K32.NewProc("GetTimeZoneInformationForYear")
	time2MemProc = time2K32.NewProc("K32GetProcessMemoryInfo")
)

// time2TziOf reads a TIME_ZONE_INFORMATION record (172 bytes).
func time2TziOf(b []byte) time2Tzi {
	sys := func(at int) [7]int {
		var d [7]int
		for k := range d {
			d[k] = int(binary.LittleEndian.Uint16(b[at+2*k:]))
		}
		return d
	}
	return time2Tzi{
		bias:    int32(binary.LittleEndian.Uint32(b[0:])),
		std:     sys(68),
		stdBias: int32(binary.LittleEndian.Uint32(b[84:])),
		dst:     sys(152),
		dstBias: int32(binary.LittleEndian.Uint32(b[168:])),
	}
}

// time2ZoneRecords returns the records for each year from first to last
// and the record in force, of the zone named key ("" for the host's), as
// Zone.Local reads them; ok is false when a call fails.
func time2ZoneRecords(key string, first, last int) (years []time2Tzi, now time2Tzi, ok bool) {
	dyn := make([]byte, 432)
	if key == "" {
		r, _, _ := time2DynProc.Call(reflect.ValueOf(dyn).Pointer())
		runtime.KeepAlive(dyn)
		if uint32(r) == 0xFFFFFFFF {
			return nil, now, false
		}
		now = time2TziOf(dyn)
	} else {
		for i, c := range utf16.Encode([]rune(key)) {
			binary.LittleEndian.PutUint16(dyn[172+2*i:], c)
		}
	}
	var zone uintptr
	if key != "" {
		zone = reflect.ValueOf(dyn).Pointer()
	}
	rec := make([]byte, 172)
	for y := first; y <= last; y++ {
		r, _, _ := time2YrProc.Call(uintptr(y), zone, reflect.ValueOf(rec).Pointer())
		runtime.KeepAlive(rec)
		runtime.KeepAlive(dyn)
		if r == 0 {
			return nil, now, false
		}
		years = append(years, time2TziOf(rec))
	}
	if key != "" {
		now = years[len(years)-1]
	}
	return years, now, true
}

// time2HostRecords is time2ZoneRecords of the host's zone.
func time2HostRecords(first, last int) ([]time2Tzi, time2Tzi, bool) {
	return time2ZoneRecords("", first, last)
}

// time2PeakCommit runs cmd and returns its peak commit charge in bytes
// (PROCESS_MEMORY_COUNTERS.PeakPagefileUsage), read after it exits through
// a handle opened while it ran.
func time2PeakCommit(t *testing.T, cmd *exec.Cmd) uint64 {
	t.Helper()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	const queryLimited = 0x1000
	h, err := syscall.OpenProcess(queryLimited, false, uint32(cmd.Process.Pid))
	if err != nil {
		cmd.Wait()
		t.Fatalf("OpenProcess: %v", err)
	}
	defer syscall.CloseHandle(h)
	if err := cmd.Wait(); err != nil {
		t.Fatalf("%v: %v", cmd.Args, err)
	}
	// PROCESS_MEMORY_COUNTERS: cb and PageFaultCount (u32), then eight
	// SIZE_Ts; PeakPagefileUsage is the last. Little-endian, so cb is the
	// low half of the first word.
	pmc := make([]uint64, 9)
	pmc[0] = 72
	r, _, e := time2MemProc.Call(uintptr(h), reflect.ValueOf(pmc).Pointer(), 72)
	runtime.KeepAlive(pmc)
	if r == 0 {
		t.Fatalf("GetProcessMemoryInfo: %v", e)
	}
	return pmc[8]
}
