// Why: the app table reports each program's peak memory (app.go), and on
// Linux the obvious reading, ru_maxrss of a child of tools/bench, reports
// tools/bench's own peak instead of the program's.
//
// What: runWithPeak for Linux, the spawner choice (chosenSpawner,
// pickSpawner), spawnPeak, the fallback spawner spawnMain and selfHWM.
//
// How: the kernel's ru_maxrss for the program (wait4 in peakspawn, waitid
// through os/exec in the fallback below), read by a small spawner process,
// kept as the program's peak only when it is above the spawner's own peak,
// and otherwise recorded as a bound.
//
// Why a spawner. Go starts a child with clone(CLONE_VM|CLONE_VFORK), so until
// its exec the child runs on its parent's address space, and the kernel's
// exec folds that address space's high-water mark into the child's maxrss
// (exec_mmap: setmax_mm_hiwater_rss(&signal->maxrss, old_mm)). So ru_maxrss
// read by tools/bench itself is at least tools/bench's own peak: after the
// other App tests a 1 MiB program can read tens of MiB. Every exec in a chain
// keeps signal->maxrss, so the program must be started by a different, small
// process, whose address space is new at its own exec. It starts the program
// and reports its rusage on fd 3, and the program's maxrss is then at least
// that spawner's peak, not tools/bench's.
//
// Which spawner. The minimal one, tools/bench/peakspawn (os, strconv and
// syscall only), which publish-linux builds beside benchtool (linux.go).
// When none is beside tools/bench, as in a test binary, tools/bench
// re-executes itself as the spawner (spawnArg, spawnMain), whose floor is
// higher, and says so once on standard error; each cell records which one
// read it.
//
// The floor. The spawner reads its own peak (VmHWM, the same kernel figure,
// get_mm_hiwater_rss) after the start returns, so after the program's exec,
// which can only raise it: floor is at least what the exec folded in. A
// maxrss above floor is the program's own ru_maxrss figure: its process and
// the children it waited for (maxrss is the larger of the folded value and
// that figure). A maxrss at or under floor says only that the program's
// figure is no larger, so it is recorded as a bound ("at most", peakText),
// never as the program's peak.
//
// Not chosen: reading the exec'd image's own VmHWM. A zombie has no address
// space, so /proc/<pid>/status has no VmHWM after the exit, and the only read
// that cannot miss the end is a ptrace exit stop. That stop is not reliably
// reported on the traced leader when another thread calls exit_group (Go and
// Mk programs exit from any thread), and tracing every thread needs a wait
// loop on a locked OS thread that reinjects every signal: far more code, in a
// path no Windows run checks.

package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
)

// runWithPeak runs cmd through the spawner and reads the program's maximum
// resident set (ru_maxrss is in KiB on Linux). Linux has no peak commit to
// report. cmd's path, arguments, environment, directory and standard files
// are the program's; the spawner gets the same environment and directory
// and passes them on, and a cmd that sets any other field is refused
// (spawnUnforwarded). cmd itself is never started, so its ProcessState
// stays nil.
func runWithPeak(cmd *exec.Cmd) (memPeak, error) {
	if cmd.Err != nil {
		return memPeak{}, cmd.Err
	}
	s, err := chosenSpawner()
	if err != nil {
		return memPeak{}, err
	}
	rep, err := spawnPeak(cmd, s)
	return rep.memPeak(s.name()), err
}

var (
	spawnerOnce sync.Once
	spawnerUsed peakSpawner
	spawnerErr  error
)

// chosenSpawner is pickSpawner's choice for tools/bench's own executable,
// made once; the fallback is said once on standard error, which is
// tools/bench's own, never a program's.
func chosenSpawner() (peakSpawner, error) {
	spawnerOnce.Do(func() {
		self, err := os.Executable()
		if err != nil {
			spawnerErr = fmt.Errorf("peak spawner: %v", err)
			return
		}
		spawnerUsed, spawnerErr = pickSpawner(self)
		if spawnerErr == nil && spawnerUsed.Self {
			fmt.Fprintf(os.Stderr, "tools/bench: no minimal peak spawner at %s: peak memory is read through tools/bench itself, whose own peak, the floor at or under which a reading only bounds the program's, is higher\n", filepath.Join(filepath.Dir(self), minSpawnerName))
		}
	})
	return spawnerUsed, spawnerErr
}

// pickSpawner is the minimal spawner beside the executable self, or self
// when there is none. A minimal spawner that is there but not an
// executable file is an error, not a fallback.
func pickSpawner(self string) (peakSpawner, error) {
	min := filepath.Join(filepath.Dir(self), minSpawnerName)
	fi, err := os.Stat(min)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return peakSpawner{Path: self, Self: true}, nil
	case err != nil:
		return peakSpawner{}, fmt.Errorf("peak spawner: %v", err)
	case !fi.Mode().IsRegular() || fi.Mode().Perm()&0o111 == 0:
		return peakSpawner{}, fmt.Errorf("peak spawner: %s is not an executable file (%v)", min, fi.Mode())
	}
	return peakSpawner{Path: min}, nil
}

// spawnPeak runs cmd through spawner s and returns the spawner's report
// (its peak method is the program's peak, 0 when the reading is not above
// the floor) and the program's error.
func spawnPeak(cmd *exec.Cmd, s peakSpawner) (spawnReport, error) {
	if cmd.Err != nil {
		return spawnReport{}, cmd.Err
	}
	if err := spawnUnforwarded(cmd); err != nil {
		return spawnReport{}, err
	}
	pr, pw, err := os.Pipe()
	if err != nil {
		return spawnReport{}, fmt.Errorf("peak spawner: %v", err)
	}
	sp := s.command(cmd)
	sp.ExtraFiles = []*os.File{pw}
	if err := sp.Start(); err != nil {
		pr.Close()
		pw.Close()
		return spawnReport{}, fmt.Errorf("peak spawner: %v", err)
	}
	pw.Close()
	report, rerr := io.ReadAll(pr)
	pr.Close()
	werr := sp.Wait()
	rep, perr := parseSpawnReport(string(report))
	switch {
	case rerr != nil:
		return spawnReport{}, fmt.Errorf("peak spawner: report: %v", rerr)
	case perr != nil && werr != nil:
		return spawnReport{}, fmt.Errorf("peak spawner: %v (%v)", werr, perr)
	case perr != nil:
		return spawnReport{}, fmt.Errorf("peak spawner: %v", perr)
	case rep.Start != "":
		return spawnReport{}, errors.New(rep.Start)
	}
	if !rep.success() {
		return rep, errors.New(rep.State)
	}
	if werr != nil {
		return rep, fmt.Errorf("peak spawner: %v after the program's %s", werr, rep.State)
	}
	return rep, nil
}

// spawnMain is the fallback spawner, tools/bench itself (args after
// spawnArg: the program's path, then its argv with argv[0]), which speaks
// tools/bench/peakspawn's report. It starts the program with its own standard files,
// environment and directory, waits, and writes one report line to fd 3; its
// exit code is the program's (128 plus the signal for a killed one, 125 when
// the program did not start).
func spawnMain(args []string) int {
	// fd 3 is the report pipe. os/exec cleared its close-on-exec flag to pass
	// it here; set it again, or the program inherits the write end and a
	// program that outlives the spawner holds the report open.
	syscall.CloseOnExec(3)
	out := os.NewFile(3, "peak-report")
	defer out.Close()
	if len(args) < 2 {
		fmt.Fprintf(out, "start peak spawner: no program\n")
		return 125
	}
	c := &exec.Cmd{Path: args[0], Args: args[1:], Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr}
	if err := c.Start(); err != nil {
		fmt.Fprintf(out, "start %s\n", oneLine(err.Error()))
		return 125
	}
	floor, ferr := selfHWM()
	c.Wait()
	ps := c.ProcessState
	if ps == nil {
		fmt.Fprintf(out, "start peak spawner: no process state\n")
		return 125
	}
	var maxrss int64
	if ru, ok := ps.SysUsage().(*syscall.Rusage); ok {
		maxrss = ru.Maxrss
	}
	if ferr != nil {
		// No floor, so no reading: the report says the peak is unknown.
		floor, maxrss = 0, 0
	}
	fmt.Fprintf(out, "ok %d %d %d %s\n", maxrss, floor, ps.ExitCode(), oneLine(ps.String()))
	if ws, ok := ps.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return 128 + int(ws.Signal())
	}
	return ps.ExitCode()
}

// selfHWM is this process's VmHWM in KiB (proc(5): "Peak resident set
// size").
func selfHWM() (int64, error) {
	f, err := os.Open("/proc/self/status")
	if err != nil {
		return 0, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if rest, ok := strings.CutPrefix(sc.Text(), "VmHWM:"); ok {
			v, err := strconv.ParseInt(strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(rest), "kB")), 10, 64)
			if err != nil || v <= 0 {
				return 0, fmt.Errorf("VmHWM %q", rest)
			}
			return v, nil
		}
	}
	if err := sc.Err(); err != nil {
		return 0, err
	}
	return 0, errors.New("no VmHWM in /proc/self/status")
}

// oneLine keeps a report field on its line.
func oneLine(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "\r", " "), "\n", " ")
}
