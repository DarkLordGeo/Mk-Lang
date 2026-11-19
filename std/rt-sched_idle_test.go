// Why: blocked tasks must stay cheap in memory, and a million idle tasks must
// work; only a measurement of a real process shows it.
// What: TestRtSched_IdleWindows and TestRtSched_LinuxIdle over the
// RT-Bench-Idle block of std/task/tests_bench.mk (only with RTS_BENCH=1).
// How: measures working set and commit around the spawns and logs bytes per
// task; RTS_IDLE_N lists the task counts (default 100000,1000000). The Linux
// test starts wsl.exe: run it under tools/wsl-lock.ps1 with go test -p 1.

package std

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// rtsIdleCounts is RTS_IDLE_N split at commas.
func rtsIdleCounts() []string {
	v := os.Getenv("RTS_IDLE_N")
	if v == "" {
		v = "100000,1000000"
	}
	return strings.Split(v, ",")
}

// rtsIdleBlock is the RT-Bench-Idle block of blocks.
func rtsIdleBlock(t *testing.T, blocks []rtsBlock) rtsBlock {
	t.Helper()
	for _, b := range blocks {
		if strings.HasPrefix(b.name, "RT-Bench-Idle ") {
			return b
		}
	}
	t.Fatal("no RT-Bench-Idle block")
	return rtsBlock{}
}

// rtsWinMem is the working set and the private bytes (commit) of process
// pid, read with Get-Process.
func rtsWinMem(pid int) (int64, int64, error) {
	cmd := exec.Command("pwsh", "-NoProfile", "-Command",
		fmt.Sprintf(`$p = Get-Process -Id %d; "$($p.WorkingSet64) $($p.PrivateMemorySize64)"`, pid))
	out, err := cmd.Output()
	if err != nil {
		return 0, 0, err
	}
	var ws, priv int64
	if _, err := fmt.Sscan(strings.TrimSpace(string(out)), &ws, &priv); err != nil {
		return 0, 0, fmt.Errorf("Get-Process said %q: %v", out, err)
	}
	return ws, priv, nil
}

// TestRtSched_IdleWindows runs RT-Bench-Idle (release) with each count and
// MK_CARRIERS 1 and 8, measuring the process's working set and private
// bytes with Get-Process while it pauses before the spawns and while every
// task is parked, and logs the differences per task.
func TestRtSched_IdleWindows(t *testing.T) {
	rtsNative(t)
	if os.Getenv("RTS_BENCH") == "" {
		t.Skip("set RTS_BENCH=1 to run the benchmarks")
	}
	exe, blocks := rtsBuild(t, "release")
	b := rtsIdleBlock(t, blocks)
	for _, n := range rtsIdleCounts() {
		for _, c := range []int{1, 8} {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
			cmd := exec.CommandContext(ctx, exe, fmt.Sprint(b.index))
			cmd.Env = append(os.Environ(), "RTS_IDLE="+n, fmt.Sprintf("MK_CARRIERS=%d", c))
			var stderr strings.Builder
			cmd.Stderr = &stderr
			pipe, err := cmd.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			var ws0, pr0 int64
			var lines []string
			sc := bufio.NewScanner(pipe)
			for sc.Scan() {
				line := sc.Text()
				lines = append(lines, line)
				switch {
				case strings.HasPrefix(line, "idle-base-ready"):
					ws0, pr0, err = rtsWinMem(cmd.Process.Pid)
					if err != nil {
						t.Errorf("n=%s carriers=%d: %v", n, c, err)
					}
				case strings.HasPrefix(line, "idle-parked-ready"):
					ws1, pr1, err := rtsWinMem(cmd.Process.Pid)
					if err != nil {
						t.Errorf("n=%s carriers=%d: %v", n, c, err)
						break
					}
					var k int64
					fmt.Sscan(n, &k)
					t.Logf("n=%s carriers=%d: working set %d -> %d (%d bytes per task), private bytes %d -> %d (%d bytes per task)",
						n, c, ws0, ws1, (ws1-ws0)/k, pr0, pr1, (pr1-pr0)/k)
				}
			}
			err = cmd.Wait()
			cancel()
			t.Logf("n=%s carriers=%d: %s", n, c, strings.Join(lines, " | "))
			if err != nil {
				t.Errorf("n=%s carriers=%d: %v\nstderr:\n%s", n, c, err, stderr.String())
			}
		}
	}
}

// TestRtSched_LinuxIdle runs RT-Bench-Idle (linux-release) in WSL with each
// count and MK_CARRIERS 1 and 8, in one wsl.exe client, and logs the lines
// (the block reads /proc/self/statm, /proc/self/maps and
// vm.max_map_count itself).
func TestRtSched_LinuxIdle(t *testing.T) {
	rtsNeedWSL(t)
	if os.Getenv("RTS_BENCH") == "" {
		t.Skip("set RTS_BENCH=1 to run the benchmarks")
	}
	exe, blocks := rtsBuild(t, "linux-release")
	b := rtsIdleBlock(t, blocks)
	dir := filepath.Dir(exe)
	var sh strings.Builder
	sh.WriteString("#!/bin/sh\ncd \"$(dirname \"$0\")\"\nuname -r\n")
	for _, n := range rtsIdleCounts() {
		for _, c := range []int{1, 8} {
			fmt.Fprintf(&sh, "echo \"== n=%s carriers=%d\"; RTS_IDLE=%s MK_CARRIERS=%d timeout 600 ./%s %d 2>&1; echo \"== exit $?\"\n",
				n, c, n, c, filepath.Base(exe), b.index)
		}
	}
	script := filepath.Join(dir, "idle.sh")
	if err := os.WriteFile(script, []byte(sh.String()), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Minute)
	defer cancel()
	wslGuard()
	cmd := exec.CommandContext(ctx, "wsl.exe", "-e", "sh", rtsWSLPath(script))
	cmd.WaitDelay = 10 * time.Second
	out, err := cmd.CombinedOutput()
	for _, line := range strings.Split(strings.ReplaceAll(string(out), "\r\n", "\n"), "\n") {
		if line == "" {
			continue
		}
		t.Log(line)
		if strings.HasPrefix(line, "== exit ") && line != "== exit 0" {
			t.Errorf("a run ended with %q", line)
		}
	}
	if err != nil {
		t.Fatalf("wsl.exe: %v", err)
	}
}
