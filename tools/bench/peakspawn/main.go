// Why: on Linux a child's ru_maxrss includes its parent's peak until exec,
// so tools/bench needs a tiny process to start each measured program.
//
// What: the peakspawn command: run starts the program and waits, report
// writes the result line on fd 3, and hwm reads this process's VmHWM.
//
// How: syscall.ForkExec and wait4, then one report line in the format
// tools/bench/spawn.go reads; imports kept to os, strconv and syscall so its
// own peak, the floor, stays small.

//go:build linux

// Command peakspawn is tools/bench's minimal Linux peak-memory spawner
// (tools/bench/mem_linux.go). tools/bench starts it in place of each
// checked program, so the program's ru_maxrss carries this small process's
// peak, not tools/bench's: Go's clone(CLONE_VM|CLONE_VFORK) child runs on its
// parent's address space until its exec, and exec folds that address space's
// high-water mark into the child's maxrss. It imports only os (for os.Args
// and os.Exit), strconv and syscall, so its own peak, the floor, is the Go
// runtime's start-up and little else. tools/bench/linux.go builds it beside
// benchtool with benchtool's flags; when it is missing, tools/bench spawns
// through a fresh copy of itself instead, whose floor is higher, and says so.
//
// Usage: peakspawn <path> <argv0> [args...] 3>report
//
// It starts path with that argv and its own environment, directory and
// standard files, waits, and writes one line on fd 3, the format
// tools/bench/spawn.go reads:
//
//	ok <maxrss KiB> <floor KiB> <exit code> <end>
//	start <error>
//
// where maxrss is the program's ru_maxrss from wait4, floor this process's
// VmHWM read after ForkExec returned (after the program's exec, which can
// only raise it), the exit code -1 for a program a signal ended, and end the
// text os.ProcessState prints ("exit status 2", "signal: killed"). A start
// error reads as os/exec's ("fork/exec <path>: <error>"). Its exit code is the
// program's, 128 plus the signal for a killed program, and 125 when the
// program did not start.
package main

import (
	"os"
	"strconv"
	"syscall"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	// fd 3 is the report pipe, passed with its close-on-exec flag cleared;
	// set it again, so the program never holds the write end.
	syscall.CloseOnExec(3)
	if len(args) < 2 {
		report("start peak spawner: no program\n")
		return 125
	}
	path := args[0]
	pid, err := syscall.ForkExec(path, args[1:], &syscall.ProcAttr{Env: syscall.Environ(), Files: []uintptr{0, 1, 2}})
	if err != nil {
		report("start fork/exec " + oneLine(path) + ": " + oneLine(err.Error()) + "\n")
		return 125
	}
	floor := hwm()
	var ws syscall.WaitStatus
	var ru syscall.Rusage
	for {
		_, err = syscall.Wait4(pid, &ws, 0, &ru)
		if err != syscall.EINTR {
			break
		}
	}
	if err != nil {
		report("start peak spawner: wait4: " + err.Error() + "\n")
		return 125
	}
	maxrss := ru.Maxrss
	if floor <= 0 {
		// No floor, so no reading: the report says the peak is unknown.
		floor, maxrss = 0, 0
	}
	code, end, exit := -1, "", 0
	switch {
	case ws.Exited():
		code = ws.ExitStatus()
		end, exit = "exit status "+strconv.Itoa(code), code
	case ws.Signaled():
		end, exit = "signal: "+ws.Signal().String(), 128+int(ws.Signal())
	default:
		end, exit = "wait status "+strconv.FormatUint(uint64(ws), 10), 125
	}
	if ws.CoreDump() {
		end += " (core dumped)"
	}
	report("ok " + strconv.FormatInt(maxrss, 10) + " " + strconv.FormatInt(floor, 10) + " " + strconv.Itoa(code) + " " + end + "\n")
	return exit
}

// report writes s on fd 3, whole.
func report(s string) {
	b := []byte(s)
	for len(b) > 0 {
		n, err := syscall.Write(3, b)
		if err == syscall.EINTR {
			continue
		}
		if err != nil || n <= 0 {
			return
		}
		b = b[n:]
	}
}

// hwm is this process's VmHWM in KiB (proc(5): "Peak resident set size"),
// or 0 when it cannot be read.
func hwm() int64 {
	fd, err := syscall.Open("/proc/self/status", syscall.O_RDONLY|syscall.O_CLOEXEC, 0)
	if err != nil {
		return 0
	}
	defer syscall.Close(fd)
	var buf [16384]byte
	n := 0
	for n < len(buf) {
		m, err := syscall.Read(fd, buf[n:])
		if err == syscall.EINTR {
			continue
		}
		if m <= 0 || err != nil {
			break
		}
		n += m
	}
	s := string(buf[:n])
	const key = "\nVmHWM:"
	for i := 0; i+len(key) <= len(s); i++ {
		if s[i:i+len(key)] != key {
			continue
		}
		j := i + len(key)
		for j < len(s) && (s[j] == ' ' || s[j] == '\t') {
			j++
		}
		k := j
		for k < len(s) && s[k] >= '0' && s[k] <= '9' {
			k++
		}
		v, err := strconv.ParseInt(s[j:k], 10, 64)
		if err != nil || v <= 0 {
			return 0
		}
		return v
	}
	return 0
}

// oneLine keeps a report field on its line.
func oneLine(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c == '\n' || c == '\r' {
			b[i] = ' '
		}
	}
	return string(b)
}
