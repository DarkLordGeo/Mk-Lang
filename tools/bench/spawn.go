// Why: the Linux peak-memory spawners' command line and report format are
// plain data, and keeping them out of the Linux-only file lets every
// platform's tests check how they are made and read.
//
// What: peakSpawner and its command, spawnUnforwarded (refuses a cmd field
// the spawner cannot pass on), spawnReport and parseSpawnReport.
//
// How: the spawner (mem_linux.go, tools/bench/peakspawn) writes one line on
// fd 3; a reading counts as the program's peak only above the floor.

package main

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// spawnArg, as os.Args[1], makes tools/bench (main) or its test binary
// (TestMain) the Linux peak-memory spawner, before anything else runs: the
// fallback spawner, used when no minimal one is beside it.
const spawnArg = "__mk_bench_peak_spawner__"

// minSpawnerName is the minimal spawner's file name (tools/bench/peakspawn),
// which publish-linux builds beside benchtool (linux.go).
const minSpawnerName = "peakspawn"

// peakSpawner is the process runWithPeak starts in place of the program:
// the minimal spawner, or tools/bench itself run with spawnArg.
type peakSpawner struct {
	Path string
	Self bool
}

// name is how results.json and the page name the spawner.
func (s peakSpawner) name() string {
	if s.Self {
		return "tools/bench"
	}
	return minSpawnerName
}

// command is the spawner's command for cmd: the program's path, then its
// argv with argv[0], after spawnArg for tools/bench itself. The environment,
// directory and standard files are cmd's, which the spawner passes on.
func (s peakSpawner) command(cmd *exec.Cmd) *exec.Cmd {
	args := append([]string{cmd.Path}, cmd.Args...)
	if s.Self {
		args = append([]string{spawnArg}, args...)
	}
	sp := exec.Command(s.Path, args...)
	sp.Env, sp.Dir = cmd.Env, cmd.Dir
	sp.Stdin, sp.Stdout, sp.Stderr = cmd.Stdin, cmd.Stdout, cmd.Stderr
	return sp
}

// spawnUnforwarded refuses a cmd that sets a field the spawner does not
// pass on to the program: only the path, argv, environment, directory and
// standard files reach it.
func spawnUnforwarded(cmd *exec.Cmd) error {
	var fs []string
	if cmd.SysProcAttr != nil {
		fs = append(fs, "SysProcAttr")
	}
	if len(cmd.ExtraFiles) > 0 {
		fs = append(fs, "ExtraFiles")
	}
	if cmd.Cancel != nil {
		fs = append(fs, "Cancel (exec.CommandContext)")
	}
	if cmd.WaitDelay != 0 {
		fs = append(fs, "WaitDelay")
	}
	if cmd.Process != nil {
		fs = append(fs, "Process (already started)")
	}
	if len(fs) == 0 {
		return nil
	}
	return fmt.Errorf("peak spawner: the command sets %s, which the spawner does not pass on", strings.Join(fs, ", "))
}

// spawnReport is the spawner's one line on fd 3: "ok <maxrss> <floor>
// <code> <state>", the program's ru_maxrss and the spawner's own VmHWM in
// KiB, the program's exit code (-1 when a signal ended it) and its end as
// os.ProcessState prints it ("exit status 2", "signal: killed"), or "start
// <error>" when the program did not start.
type spawnReport struct {
	MaxrssKiB, FloorKiB uint64
	Code                int
	State               string
	Start               string
}

func parseSpawnReport(s string) (spawnReport, error) {
	line, rest, _ := strings.Cut(s, "\n")
	if strings.TrimSpace(rest) != "" || line == "" {
		return spawnReport{}, fmt.Errorf("report %q", s)
	}
	kind, body, _ := strings.Cut(line, " ")
	switch kind {
	case "start":
		if body == "" {
			return spawnReport{}, fmt.Errorf("report %q", s)
		}
		return spawnReport{Start: body}, nil
	case "ok":
		f := strings.SplitN(body, " ", 4)
		if len(f) != 4 || f[3] == "" {
			return spawnReport{}, fmt.Errorf("report %q", s)
		}
		m, err1 := strconv.ParseUint(f[0], 10, 64)
		fl, err2 := strconv.ParseUint(f[1], 10, 64)
		code, err3 := strconv.Atoi(f[2])
		if err1 != nil || err2 != nil || err3 != nil || code < -1 {
			return spawnReport{}, fmt.Errorf("report %q", s)
		}
		return spawnReport{MaxrssKiB: m, FloorKiB: fl, Code: code, State: f[3]}, nil
	}
	return spawnReport{}, fmt.Errorf("report %q", s)
}

// peak is the program's own ru_maxrss figure in bytes (its process and the
// children it waited for), or 0 when the reading is not above the spawner's
// floor: maxrss is the larger of the floor's share and the program's
// figure, so only a value above the floor is the program's.
func (r spawnReport) peak() uint64 {
	if r.MaxrssKiB <= r.FloorKiB {
		return 0
	}
	return r.MaxrssKiB * 1024
}

// memPeak is what a cell records of the report (app.go): the program's
// peak when the reading is above the floor, and the raw reading and the
// floor in every case, or nothing when either was not read.
func (r spawnReport) memPeak(spawner string) memPeak {
	if r.MaxrssKiB == 0 || r.FloorKiB == 0 {
		return memPeak{}
	}
	return memPeak{RSS: r.peak(), Maxrss: r.MaxrssKiB * 1024, Floor: r.FloorKiB * 1024, Spawner: spawner}
}

func (r spawnReport) success() bool { return r.Code == 0 }
