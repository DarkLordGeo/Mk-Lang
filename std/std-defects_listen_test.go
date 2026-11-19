// Why: rt.ListenWith and JobWrite's interruptible write are scheduler-level code
// whose blocks must pass on every carrier count and I/O backend.
// What: TestStdDefects_ListenBlocks and TestStdDefects_ListenBlocksLinux, over the
// "SD8-" blocks of std/task/tests_defects.mk.
// How: at 1 and 16 carriers, dev and release, natively and on x86_64-linux with
// MK_IO=auto and epoll (MK_LINUX=1, tools/wsl-lock.ps1, go test -p 1), through
// the harness of rt-sched_harness_test.go.

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

// sd8Blocks are the rts blocks whose names start with "SD8-".
func sd8Blocks(t *testing.T, all []rtsBlock) []rtsBlock {
	t.Helper()
	var keep []rtsBlock
	for _, b := range all {
		if strings.HasPrefix(b.name, "SD8-") {
			keep = append(keep, b)
		}
	}
	if len(keep) < 4 {
		t.Fatalf("%d SD8- blocks, want at least 4", len(keep))
	}
	return keep
}

func TestStdDefects_ListenBlocks(t *testing.T) {
	rtsNative(t)
	for _, profile := range []string{"dev", "release"} {
		exe, all := rtsBuild(t, profile)
		for _, b := range sd8Blocks(t, all) {
			for _, n := range []int{1, 16} {
				b, n := b, n
				t.Run(fmt.Sprintf("%s/%s/carriers=%d", profile, b.name, n), func(t *testing.T) {
					r := rtsRun(t, exe, b.index, 60*time.Second, append(rtsEnv(b), fmt.Sprintf("MK_CARRIERS=%d", n))...)
					rtsJudge(t, b, r, "")
					if strings.Contains(r.stderr, "sd8-") {
						t.Logf("notes: %s", strings.TrimSpace(r.stderr))
					}
				})
			}
		}
	}
}

func TestStdDefects_ListenBlocksLinux(t *testing.T) {
	linuxGate(t)
	rtsNeedWSL(t)
	for _, profile := range []string{"linux-dev", "linux-release"} {
		exe, all := rtsBuild(t, profile)
		blocks := sd8Blocks(t, all)
		dir := filepath.Dir(exe)
		var sh strings.Builder
		sh.WriteString("#!/bin/sh\ncd \"$(dirname \"$0\")\"\n")
		for _, b := range blocks {
			for _, n := range []int{1, 16} {
				for _, io := range []string{"auto", "epoll"} {
					fmt.Fprintf(&sh, "%s MK_CARRIERS=%d MK_IO=%s timeout -k 5 60 ./%s %d 2>sd8err-%d-%d-%s.txt </dev/null; echo \"%d %d %s $?\"\n",
						strings.Join(rtsEnv(b), " "), n, io, filepath.Base(exe), b.index, b.index, n, io, b.index, n, io)
				}
			}
		}
		script := filepath.Join(dir, "sd8run.sh")
		if err := os.WriteFile(script, []byte(sh.String()), 0o755); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
		wslGuard()
		cmd := exec.CommandContext(ctx, "wsl.exe", "-e", "sh", rtsWSLPath(script))
		cmd.WaitDelay = 10 * time.Second
		out, err := cmd.Output()
		cancel()
		if err != nil {
			t.Fatalf("wsl.exe: %v\n%s", err, out)
		}
		got := map[string]string{}
		for _, line := range strings.Split(strings.ReplaceAll(string(out), "\r\n", "\n"), "\n") {
			if f := strings.Fields(line); len(f) == 4 {
				got[f[0]+" "+f[1]+" "+f[2]] = f[3]
			}
		}
		for _, b := range blocks {
			for _, n := range []int{1, 16} {
				for _, io := range []string{"auto", "epoll"} {
					key := fmt.Sprintf("%d %d %s", b.index, n, io)
					if got[key] != "0" {
						errb, _ := os.ReadFile(filepath.Join(dir, fmt.Sprintf("sd8err-%d-%d-%s.txt", b.index, n, io)))
						t.Errorf("%s %s carriers=%d MK_IO=%s: exit %q\n%s", profile, b.name, n, io, got[key], errb)
					}
				}
			}
		}
	}
}
