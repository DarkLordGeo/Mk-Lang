// Why: the OS-backed runtime entries (processes, directories, datagrams,
// lookups, scheduler hooks, pool linger) must survive any task interleaving.
// What: TestRtIo_Stress (the host build) and TestRtIo_StressLinux (io_uring
// and epoll, in one WSL call).
// How: runs every RT-Os- test block of std/task over seeds and carrier counts
// with the scheduler's jitter points on (MK_TEST_SEED, std/task/jitter.mk);
// TestRtSched_Stress covers the other prefixes (rtsStressPrefixes).

package std

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// rtio2Seeds is RTS_SEEDS or 8.
func rtio2Seeds() int {
	seeds := 8
	if s := os.Getenv("RTS_SEEDS"); s != "" {
		fmt.Sscan(s, &seeds)
	}
	return seeds
}

// TestRtIo_Stress runs the RT-Os- blocks, release, with 2, 3, 8 and 16
// carriers and seeds 1 to RTS_SEEDS, each under a 120 s watchdog.
func TestRtIo_Stress(t *testing.T) {
	rtsNative(t)
	if testing.Short() {
		t.Skip("stress runs are long")
	}
	seeds := rtio2Seeds()
	exe, blocks := rtsBuild(t, "release")
	ran := 0
	for _, b := range blocks {
		if !strings.HasPrefix(b.name, "RT-Os-") {
			continue
		}
		ran++
		for _, n := range []int{2, 3, 8, 16} {
			b, n := b, n
			t.Run(fmt.Sprintf("%s/carriers=%d", b.name, n), func(t *testing.T) {
				for seed := 1; seed <= seeds; seed++ {
					r := rtsRun(t, exe, b.index, 120*time.Second, append(rtsEnv(b), fmt.Sprintf("MK_CARRIERS=%d", n),
						fmt.Sprintf("MK_TEST_SEED=%d", seed))...)
					if r.timeout {
						t.Fatalf("seed %d: no exit within 120 s (a lost datagram, wake or completion)\nstderr:\n%s", seed, r.stderr)
					}
					if r.code != 0 {
						t.Fatalf("seed %d: exit %d after %v\nstderr:\n%s", seed, r.code, r.took, r.stderr)
					}
				}
			})
		}
	}
	if ran == 0 {
		t.Fatal("no RT-Os- block was built")
	}
}

// TestRtIo_StressLinux runs the RT-Os- blocks of the x86_64-linux release
// build with 2 and 8 carriers, io_uring and epoll, seeds 1 to RTS_SEEDS, as
// one shell script in one wsl.exe call (MK_LINUX=1, under the WSL lock with
// -p 1). Each run has a 120 s watchdog, `timeout -k 5 120`: a run that
// ignores SIGTERM or sticks in the kernel is killed at 125 s (status 137)
// instead of holding the script until its 90-minute limit.
func TestRtIo_StressLinux(t *testing.T) {
	rtsNeedWSL(t)
	if testing.Short() {
		t.Skip("stress runs are long")
	}
	seeds := rtio2Seeds()
	exe, blocks := rtsBuild(t, "linux-release")
	dir := filepath.Dir(exe)
	var sh strings.Builder
	sh.WriteString("#!/bin/sh\ncd \"$(dirname \"$0\")\"\n")
	var mine []rtsBlock
	for _, b := range blocks {
		if !strings.HasPrefix(b.name, "RT-Os-") {
			continue
		}
		mine = append(mine, b)
		for _, n := range []int{2, 8} {
			for _, io := range []string{"auto", "epoll"} {
				for seed := 1; seed <= seeds; seed++ {
					fmt.Fprintf(&sh, "%s MK_CARRIERS=%d MK_IO=%s MK_TEST_SEED=%d timeout -k 5 120 ./%s %d </dev/null 2>err-%d-%d-%s-%d.txt; echo \"%d %d %s %d $?\"\n",
						strings.Join(rtsEnv(b), " "), n, io, seed, filepath.Base(exe), b.index, b.index, n, io, seed, b.index, n, io, seed)
				}
			}
		}
	}
	if len(mine) == 0 {
		t.Fatal("no RT-Os- block was built")
	}
	script := filepath.Join(dir, "rtio2-stress.sh")
	if err := os.WriteFile(script, []byte(sh.String()), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Minute)
	defer cancel()
	wslGuard()
	cmd := exec.CommandContext(ctx, "wsl.exe", "-e", "sh", rtsWSLPath(script))
	cmd.WaitDelay = 10 * time.Second
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("wsl.exe: %v\n%s", err, out)
	}
	got := map[string]string{}
	for _, line := range strings.Split(strings.ReplaceAll(string(out), "\r\n", "\n"), "\n") {
		f := strings.Fields(line)
		if len(f) == 5 {
			got[strings.Join(f[:4], " ")] = f[4]
		}
	}
	for _, b := range mine {
		for _, n := range []int{2, 8} {
			for _, io := range []string{"auto", "epoll"} {
				b, n, io := b, n, io
				t.Run(fmt.Sprintf("%s/carriers=%d/io=%s", b.name, n, io), func(t *testing.T) {
					for seed := 1; seed <= seeds; seed++ {
						st, ok := got[fmt.Sprintf("%d %d %s %d", b.index, n, io, seed)]
						if !ok {
							t.Fatalf("seed %d: no result line", seed)
						}
						if st != "0" {
							e, _ := os.ReadFile(filepath.Join(dir, fmt.Sprintf("err-%d-%d-%s-%d.txt", b.index, n, io, seed)))
							t.Fatalf("seed %d: exit %s (124 or 137: no exit within 120 s)\nstderr:\n%s", seed, st, e)
						}
					}
				})
			}
		}
	}
}
