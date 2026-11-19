// Why: a lost wake or a missed completion that one run in thousands meets
// shows in the full Linux pass (TestRtSched_LinuxBlocks) only once in a while.
// What: TestRwRt_LinuxRepeat, opt-in with RWRT_REPEAT=<runs>, RTS_ONLY naming
// the blocks and MK_LINUX=1.
// How: runs those runtime blocks many times on x86_64-linux in one WSL call,
// under tools/wsl-lock.ps1 with go test -p 1.

package std

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestRwRt_LinuxRepeat runs each block RTS_ONLY selects RWRT_REPEAT times
// with MK_IO=auto and again with MK_IO=epoll, at RWRT_REPEAT_CARRIERS
// carriers (default 8), in the RWRT_REPEAT_PROFILE build (default
// linux-dev). One shell script runs every case, so the test starts one
// wsl.exe client. Each case runs under `timeout -k 5 60`; a case still
// alive at 55 s has its threads' state, wait channel and system call
// written to diag-<index>-<io>-<run>.txt before it is killed. The script
// stops after three stalls (status 124 or 137), so its worst case is bounded
// well inside the test's own limit, and it appends each result to
// progress.txt beside the executable, which can be read from Windows while
// it runs. It first prints the kernel and which ring each backend sets up
// (strace, when the distribution has it), so an auto run is known to be
// io_uring and not epoll's fallback.
func TestRwRt_LinuxRepeat(t *testing.T) {
	runs, _ := strconv.Atoi(os.Getenv("RWRT_REPEAT"))
	if runs <= 0 {
		t.Skip("set RWRT_REPEAT=<runs> and RTS_ONLY=<blocks> to repeat blocks on Linux")
	}
	if os.Getenv("RTS_ONLY") == "" {
		t.Fatal("RWRT_REPEAT needs RTS_ONLY to name the blocks")
	}
	rtsNeedWSL(t)
	profile := "linux-dev"
	if p := os.Getenv("RWRT_REPEAT_PROFILE"); p != "" {
		profile = p
	}
	carriers := 8
	if s := os.Getenv("RWRT_REPEAT_CARRIERS"); s != "" {
		carriers, _ = strconv.Atoi(s)
	}
	exe, all := rtsBuild(t, profile)
	blocks := rtsOnly(t, all)
	if len(blocks) == 0 {
		t.Fatalf("RTS_ONLY=%q selects no block", os.Getenv("RTS_ONLY"))
	}
	dir := filepath.Dir(exe)
	base := filepath.Base(exe)
	var sh strings.Builder
	sh.WriteString("#!/bin/sh\ncd \"$(dirname \"$0\")\"\n: > progress.txt\n")
	sh.WriteString("echo \"# kernel $(uname -r); io_uring_disabled $(cat /proc/sys/kernel/io_uring_disabled 2>/dev/null || echo absent)\" | tee -a progress.txt\n")
	// Which backend each MK_IO setting starts, from its system calls.
	b0 := blocks[0]
	sh.WriteString("if command -v strace >/dev/null 2>&1; then\n")
	for _, io := range []string{"auto", "epoll"} {
		fmt.Fprintf(&sh, "  %s MK_CARRIERS=%d MK_IO=%s timeout -k 5 60 strace -f -qq -e trace=io_uring_setup,epoll_create1 -o strace-%s.txt ./%s %d </dev/null >/dev/null 2>&1\n",
			strings.Join(rtsEnv(b0), " "), carriers, io, io, base, b0.index)
		fmt.Fprintf(&sh, "  echo \"# MK_IO=%s: $(grep -o 'io_uring_setup([^)]*) *= *[-0-9]*\\|epoll_create1([^)]*) *= *[-0-9]*' strace-%s.txt | sort | uniq -c | tr '\\n' ';')\" | tee -a progress.txt\n", io, io)
	}
	sh.WriteString("else\n  echo \"# strace absent: backend not checked\" | tee -a progress.txt\nfi\n")
	sh.WriteString("stalls=0\n")
	for _, b := range blocks {
		env := strings.Join(rtsEnv(b), " ")
		fmt.Fprintf(&sh, "i=1\nwhile [ $i -le %d ] && [ $stalls -lt 3 ]; do\n  for io in auto epoll; do\n", runs)
		fmt.Fprintf(&sh, "    t0=$(date +%%s%%N)\n")
		fmt.Fprintf(&sh, "    %s MK_CARRIERS=%d MK_IO=$io timeout -k 5 60 ./%s %d </dev/null 2>err.txt &\n", env, carriers, base, b.index)
		sh.WriteString("    p=$!\n")
		fmt.Fprintf(&sh, "    ( sleep 55; c=$(pgrep -P $p); if [ -n \"$c\" ]; then { echo \"pid $c\"; for d in /proc/$c/task/*; do echo \"$d state $(cut -d' ' -f3 $d/stat) wchan $(cat $d/wchan) syscall $(cat $d/syscall)\"; done; } > diag-%d-$io-$i.txt 2>&1; fi ) &\n", b.index)
		sh.WriteString("    w=$!\n    wait $p; s=$?\n    pkill -P $w 2>/dev/null; kill $w 2>/dev/null; wait $w 2>/dev/null\n")
		sh.WriteString("    ms=$(( ($(date +%s%N) - t0) / 1000000 ))\n")
		fmt.Fprintf(&sh, "    if [ $s -ne 0 ]; then cp err.txt err-%d-$io-$i.txt; fi\n", b.index)
		sh.WriteString("    if [ $s -eq 124 ] || [ $s -eq 137 ]; then stalls=$((stalls + 1)); fi\n")
		fmt.Fprintf(&sh, "    echo \"%d $io $i $s $ms\" | tee -a progress.txt\n", b.index)
		// Keeps the directory's time current, so a sweep of test
		// directories left untouched for hours spares a long run.
		sh.WriteString("    touch .\n  done\n  i=$((i + 1))\ndone\n")
	}
	script := filepath.Join(dir, "repeat.sh")
	if err := os.WriteFile(script, []byte(sh.String()), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Logf("script %s; progress in %s", script, filepath.Join(dir, "progress.txt"))
	// Above the script's worst case: every run of every block at 65 s
	// would pass three stalls first, so at most 2 runs an index at 65 s
	// plus the stalls; the limit only guards a WSL that stops answering.
	limit := time.Duration(len(blocks)*runs*2)*20*time.Second + 30*time.Minute
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()
	wslGuard()
	cmd := exec.CommandContext(ctx, "wsl.exe", "-e", "sh", rtsWSLPath(script))
	cmd.WaitDelay = 10 * time.Second
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("wsl.exe: %v\n%s", err, out)
	}
	type key struct {
		index int
		io    string
	}
	got := map[key][]string{}
	for _, line := range strings.Split(strings.ReplaceAll(string(out), "\r\n", "\n"), "\n") {
		if strings.HasPrefix(line, "#") {
			t.Log(line)
			continue
		}
		f := strings.Fields(line)
		if len(f) != 5 {
			continue
		}
		idx, _ := strconv.Atoi(f[0])
		got[key{idx, f[1]}] = append(got[key{idx, f[1]}], line)
	}
	for _, b := range blocks {
		for _, io := range []string{"auto", "epoll"} {
			b, io := b, io
			t.Run(fmt.Sprintf("%s/%s/carriers=%d/io=%s", profile, b.name, carriers, io), func(t *testing.T) {
				lines := got[key{b.index, io}]
				if len(lines) < runs {
					t.Errorf("%d of %d runs reported (the script stops after three stalls)", len(lines), runs)
				}
				var maxMs int
				for _, line := range lines {
					f := strings.Fields(line)
					run := f[2]
					ms, _ := strconv.Atoi(f[4])
					maxMs = max(maxMs, ms)
					r := rtsResult{timeout: f[3] == "124" || f[3] == "137", took: time.Duration(ms) * time.Millisecond}
					r.code, _ = strconv.Atoi(f[3])
					if f[3] != "0" {
						e, _ := os.ReadFile(filepath.Join(dir, fmt.Sprintf("err-%d-%s-%s.txt", b.index, io, run)))
						r.stderr = string(e)
						if d, err := os.ReadFile(filepath.Join(dir, fmt.Sprintf("diag-%d-%s-%s.txt", b.index, io, run))); err == nil {
							t.Logf("run %s: threads at 55 s:\n%s", run, d)
						}
					}
					want, text := rtsWant(b)
					if r.timeout || r.code != want || !strings.Contains(r.stderr, text) {
						why := ""
						if r.timeout {
							why = " (no exit within 60 s)"
						}
						t.Errorf("run %s: exit %d after %v%s, want %d\nstderr:\n%s", run, r.code, r.took, why, want, r.stderr)
					}
				}
				t.Logf("%d runs, slowest %d ms", len(lines), maxMs)
			})
		}
	}
}
