// Why: the Linux peak-memory reading is subtle (exec folds the parent's
// peak in), so its floor rule and both spawners need checks on a real kernel.
// What: TestSpawnPeakLinux and spawnChecks, run against peakspawn and the
// fallback spawner.
// How: helpers of known size (16 and 256 MiB, the tiny ELF) are run through
// each spawner, and the readings are compared with the floor and VmHWM.

package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// buildMinSpawner builds tools/bench/peakspawn as publish-linux does
// (linux.go: -trimpath, CGO_ENABLED=0) into a temporary directory.
func buildMinSpawner(t *testing.T) string {
	t.Helper()
	exe := filepath.Join(t.TempDir(), minSpawnerName)
	c := exec.Command(filepath.Join(runtime.GOROOT(), "bin", "go"), "build", "-trimpath", "-o", exe, "./peakspawn")
	c.Env = append(os.Environ(), "CGO_ENABLED=0", "GOFLAGS=", "GOTOOLCHAIN=local")
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("building peakspawn: %v\n%s", err, out)
	}
	return exe
}

// testSpawners are both spawners: the minimal one, built here, and the
// fallback, the test binary itself (TestMain dispatches spawnArg).
func testSpawners(t *testing.T) []peakSpawner {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return []peakSpawner{{Path: buildMinSpawner(t)}, {Path: self, Self: true}}
}

func spawnRun(c *exec.Cmd, s peakSpawner) (string, spawnReport, error) {
	var out bytes.Buffer
	c.Stdout, c.Stderr = &out, &out
	rep, err := spawnPeak(c, s)
	return out.String(), rep, err
}

// TestSpawnPeakLinux: the Linux peak spawners (mem_linux.go,
// tools/bench/peakspawn), each one through the same checks. The raw
// reading and the floor are logged, since the floor decides which small
// programs get only a bound.
func TestSpawnPeakLinux(t *testing.T) {
	// A test binary has no peakspawn beside it, so runProgram's spawner is
	// the fallback.
	if s, err := chosenSpawner(); err != nil || !s.Self || s.name() != "tools/bench" {
		t.Errorf("the test binary's spawner: %+v, %v (want tools/bench itself)", s, err)
	}
	// Beside benchtool, as publish-linux builds them, peakspawn is chosen; a
	// peakspawn that is no executable file is an error, not the fallback.
	dir := t.TempDir()
	tool := filepath.Join(dir, "benchtool")
	writeFile(t, tool, "")
	if s, err := pickSpawner(tool); err != nil || s != (peakSpawner{Path: tool, Self: true}) {
		t.Errorf("no peakspawn: %+v, %v", s, err)
	}
	writeFile(t, filepath.Join(dir, minSpawnerName), "")
	if s, err := pickSpawner(tool); err == nil || !strings.Contains(err.Error(), "not an executable file") {
		t.Errorf("a peakspawn that is not executable: %+v, %v", s, err)
	}
	if err := os.Chmod(filepath.Join(dir, minSpawnerName), 0o755); err != nil {
		t.Fatal(err)
	}
	if s, err := pickSpawner(tool); err != nil || s != (peakSpawner{Path: filepath.Join(dir, minSpawnerName)}) {
		t.Errorf("peakspawn beside benchtool: %+v, %v", s, err)
	}
	env := append(runEnv(os.Environ()), "BENCH_HELPER_APP=size")
	for _, s := range testSpawners(t) {
		t.Run(s.name(), func(t *testing.T) { spawnChecks(t, s, env) })
	}
}

func spawnChecks(t *testing.T, s peakSpawner, env []string) {
	// The 16 MiB and 256 MiB helpers clear the floor, and the kept peak is
	// the reading (16 MiB, not 1, so the margin over the fallback's
	// floor is wide).
	for _, mib := range []string{"16", "256"} {
		c := exec.Command(os.Args[0], mib)
		c.Env = env
		out, rep, err := spawnRun(c, s)
		if err != nil {
			t.Fatalf("%s MiB: %v: %s", mib, err, out)
		}
		raw, floor, rss := rep.MaxrssKiB*1024, rep.FloorKiB*1024, rep.peak()
		t.Logf("%s MiB program: ru_maxrss %d bytes (%.1f MiB), spawner floor %d bytes (%.1f MiB), peak kept %d bytes", mib, raw, float64(raw)/(1<<20), floor, float64(floor)/(1<<20), rss)
		if rss == 0 || floor == 0 || rss <= floor || rss != raw {
			t.Fatalf("%s MiB: ru_maxrss %d, floor %d, peak kept %d: want the reading kept above a floor", mib, raw, floor, rss)
		}
		if mp := rep.memPeak(s.name()); mp != (memPeak{RSS: raw, Maxrss: raw, Floor: floor, Spawner: s.name()}) {
			t.Fatalf("%s MiB: recorded %+v", mib, mp)
		}
		if !strings.HasPrefix(out, "ok ") {
			t.Fatalf("%s MiB: stdout %q", mib, out)
		}
		if mib == "256" && rss < 256<<20 {
			t.Fatalf("256 MiB: peak %d", rss)
		}
	}

	// Exactness: the helper prints its own VmHWM just before it exits. Its
	// own peak is not above the reading by more than the slack of the
	// kernel's resident-set counter, and a reading above the floor is within
	// 1 MiB of it (the reading also holds the exit's last pages). Since Linux
	// 6.2 that counter is a per-CPU counter read without folding in each
	// CPU's pending delta, so a read can be off by up to one batch per CPU:
	// max(32, 2*CPUs) pages each. The two figures are read at different
	// moments, so they can differ by that much. A hosted 4-CPU runner showed
	// 224 KiB, against 512 KiB for this bound; WSL readings moved by 128 KiB.
	cpus := uint64(runtime.NumCPU())
	counterSlackKiB := 4 * max(32, 2*cpus) * cpus
	for _, tc := range [][]string{{"1", "hwm"}, {"16", "hwm"}, {"64", "hwm"}, {"16", "hwmfree"}} {
		c := exec.Command(os.Args[0], tc...)
		c.Env = env
		out, rep, err := spawnRun(c, s)
		if err != nil {
			t.Fatalf("%v: %v: %s", tc, err, out)
		}
		own := ownHWM(t, out)
		raw, floor := rep.MaxrssKiB, rep.FloorKiB
		t.Logf("%v: ru_maxrss %d KiB, floor %d KiB, the program's own VmHWM %d KiB", tc, raw, floor, own)
		if own > raw+counterSlackKiB {
			t.Errorf("%v: the program's own VmHWM %d KiB is more than %d KiB above the reading %d KiB", tc, own, counterSlackKiB, raw)
		}
		if raw > floor && raw > own && raw-own >= 1024 {
			t.Errorf("%v: the reading %d KiB is above the floor %d KiB but %d KiB over the program's own VmHWM %d KiB", tc, raw, floor, raw-own, own)
		}
	}

	// The invariant the floor rule rests on: a program smaller than either
	// spawner, the tiny ELF (tinyelf_test.go), reads at or under the floor,
	// so no peak is kept and the cell prints a bound. 20 runs as it is, then
	// 20 with about 1 MiB more environment: the spawner copies the
	// environment into fresh heap inside its start (syscall.forkExec's
	// SlicePtrFromStrings, in os/exec's Start and in peakspawn's ForkExec),
	// so a floor read before the start misses about 1 MiB that the exec folds
	// in, far over the kernel's 128 KiB counter step, in every run. The
	// program's own stack holds only one copy, under the spawner's two.
	if runtime.GOARCH != "amd64" {
		t.Fatalf("the tiny ELF is an x86-64 program (GOARCH %s)", runtime.GOARCH)
	}
	tiny := filepath.Join(t.TempDir(), "tiny")
	if err := os.WriteFile(tiny, tinyELFBytes(), 0o755); err != nil {
		t.Fatal(err)
	}
	padded := os.Environ()
	for i := 0; i < 10; i++ {
		padded = append(padded, "MK_PEAK_PAD"+strconv.Itoa(i)+"="+strings.Repeat("x", 100_000))
	}
	for _, run := range []struct {
		name string
		env  []string
	}{{"the tiny ELF", nil}, {"the tiny ELF with about 1 MiB more environment", padded}} {
		lo, hi, flo, fhi := ^uint64(0), uint64(0), ^uint64(0), uint64(0)
		for i := 0; i < 20; i++ {
			c := exec.Command(tiny)
			c.Env = run.env
			_, rep, err := spawnRun(c, s)
			if err != nil {
				t.Fatalf("%s, run %d: %v", run.name, i, err)
			}
			mp := rep.memPeak(s.name())
			text := peakText(&cell{Peak: &mp})
			if rep.FloorKiB == 0 || rep.MaxrssKiB > rep.FloorKiB || rep.peak() != 0 || mp.RSS != 0 || mp.Maxrss == 0 || !strings.HasPrefix(text, "at most ") {
				t.Fatalf("%s, run %d: ru_maxrss %d KiB, floor %d KiB, peak kept %d, recorded %+v, printed %q: want a bound at or under the floor", run.name, i, rep.MaxrssKiB, rep.FloorKiB, rep.peak(), mp, text)
			}
			lo, hi, flo, fhi = min(lo, rep.MaxrssKiB), max(hi, rep.MaxrssKiB), min(flo, rep.FloorKiB), max(fhi, rep.FloorKiB)
		}
		t.Logf("%s, 20 runs: ru_maxrss %d to %d KiB, floor %d to %d KiB, each at or under its floor", run.name, lo, hi, flo, fhi)
	}

	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Fatalf("no sh: %v", err)
	}

	// The program's environment, argv[0] and directory pass through the
	// spawner, and the report pipe does not reach the program.
	c := exec.Command(os.Args[0])
	c.Env = append(runEnv(os.Environ()), "BENCH_HELPER_ENV=PEAKPROBE", "PEAKPROBE=through")
	if out, _, err := spawnRun(c, s); err != nil || strings.TrimSpace(out) != "through" {
		t.Fatalf("environment: %q, %v", out, err)
	}
	dir := t.TempDir()
	c = exec.Command(sh, "-c", `printf '%s %s ' "$0" "$(pwd -P)"; if [ -e /proc/$$/fd/3 ]; then echo fd3; else echo none; fi`)
	c.Dir = dir
	want, _ := exec.Command("sh", "-c", "cd '"+dir+"' && pwd -P").Output()
	if out, _, err := spawnRun(c, s); err != nil || out != sh+" "+strings.TrimSpace(string(want))+" none\n" {
		t.Fatalf("argv[0], directory and fd 3: %q, %v (want argv[0] %s, directory %s)", out, err, sh, strings.TrimSpace(string(want)))
	}

	// A failing program is an error with no peak kept by runProgram's
	// callers, in the shapes os/exec gives without a spawner.
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{sh, "-c", "exit 3"}, "exit status 3"},
		{[]string{sh, "-c", "kill -KILL $$"}, "signal: killed"},
		{[]string{dir + "/nosuchprogram"}, "fork/exec " + dir + "/nosuchprogram: no such file or directory"},
	} {
		c := exec.Command(tc.args[0], tc.args[1:]...)
		_, _, err := spawnRun(c, s)
		if err == nil || !strings.Contains(err.Error(), tc.want) || strings.Contains(err.Error(), "peak spawner") {
			t.Errorf("%q: error %v, want %q", tc.args, err, tc.want)
		}
	}
	// A path os/exec refused before the start stays its error.
	c = exec.Command("no-such-program-on-path-11-31b")
	if _, _, err := spawnRun(c, s); err == nil || err != c.Err {
		t.Errorf("a LookPath failure: %v, want %v", err, c.Err)
	}
	// A field the spawner does not pass on is refused.
	c = exec.Command(sh, "-c", ":")
	c.ExtraFiles = []*os.File{os.Stdin}
	if _, _, err := spawnRun(c, s); err == nil || !strings.Contains(err.Error(), "ExtraFiles") {
		t.Errorf("ExtraFiles: %v", err)
	}

	// The spawner with no program: its report says so, and it exits 125.
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	var args []string
	if s.Self {
		args = []string{spawnArg}
	}
	c = exec.Command(s.Path, args...)
	c.ExtraFiles = []*os.File{pw}
	err = c.Start()
	pw.Close()
	if err != nil {
		t.Fatal(err)
	}
	var rep bytes.Buffer
	rep.ReadFrom(pr)
	pr.Close()
	c.Wait()
	if rep.String() != "start peak spawner: no program\n" || c.ProcessState.ExitCode() != 125 {
		t.Errorf("no program: report %q, exit %d", rep.String(), c.ProcessState.ExitCode())
	}
}

// ownHWM is the "hwm <KiB>" line the size helper prints.
func ownHWM(t *testing.T, out string) uint64 {
	t.Helper()
	for _, l := range strings.Split(out, "\n") {
		if v, ok := strings.CutPrefix(l, "hwm "); ok {
			n, err := strconv.ParseUint(strings.TrimSpace(v), 10, 64)
			if err != nil || n == 0 {
				t.Fatalf("own VmHWM %q", l)
			}
			return n
		}
	}
	t.Fatalf("no own VmHWM in %q", out)
	return 0
}
