// Why: the runtime's budgets for task switch, task spawn, idle-task memory
// and held connections mean nothing until a program measures them.
// What: TestPerfRuntime_Windows, TestPerfRuntime_EchoWindows and
// TestPerfRuntime_Linux, all skipped unless PERFRT=1.
// How: times tests/programs/tasks/pingpong.mk, spawn.mk and idle.mk, and drives
// echo.mk with a Go client (perfRtClientSrc) that binds each connection to its
// own loopback source address and port, so ephemeral ports do not run out
// (Windows 16,384; this WSL 4,096). TestPerfRuntime_Linux starts wsl.exe, so
// it runs only under tools/wsl-lock.ps1 with go test -p 1.

package std

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// perfRtOn skips unless PERFRT=1.
// perfRtSocketCap is the most sockets an echo run holds at once on the
// development machine: two Windows bugchecks (0x133
// DPC_WATCHDOG_VIOLATION) came while TestPerfRuntime_EchoWindows held
// 100,000 loopback connections through WSL mirrored networking, Hyper-V and
// two VPN filter drivers. The 100k budget row is extrapolated from this
// count, not measured here.
const perfRtSocketCap = 10_000

func perfRtOn(t *testing.T) {
	t.Helper()
	if os.Getenv("PERFRT") == "" {
		t.Skip("set PERFRT=1 to run the perf-runtime measurements")
	}
}

// perfRtEnvInt is the integer in environment variable k, else def.
func perfRtEnvInt(k string, def int) int {
	if v, err := strconv.Atoi(os.Getenv(k)); err == nil && v > 0 {
		return v
	}
	return def
}

// perfRtStd copies std into root (copyStd); when PERFRT_MUTANT names a
// directory laid out like std/, its .mk files replace the copy's, so an
// alternative is measured without touching the tree.
func perfRtStd(t *testing.T, root string) string {
	t.Helper()
	dir := os.Getenv("PERFRT_MUTANT")
	if dir == "" {
		return copyStd(t, root, nil)
	}
	fix := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".mk") {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		fix[filepath.ToSlash(rel)] = string(b)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("PERF mutant %s: %d files", dir, len(fix))
	return copyStd(t, root, fix)
}

// perfRtProgDir is tests/programs (PERFRT_PROGRAMS overrides it).
func perfRtProgDir() string {
	if d := os.Getenv("PERFRT_PROGRAMS"); d != "" {
		return d
	}
	return filepath.Join("..", "tests", "programs")
}

// perfRtBuild builds tests/programs/<rel> with the std copy stdDir into
// root, release, for Windows or (linux) x86_64-linux, and returns the
// executable's path.
func perfRtBuild(t *testing.T, root, stdDir, rel string, linux bool) string {
	t.Helper()
	src, err := os.ReadFile(filepath.Join(perfRtProgDir(), filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	name := strings.TrimSuffix(filepath.Base(rel), ".mk")
	work := filepath.Join(root, name)
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "main.mk"), src, 0o644); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(root, name)
	args := []string{"build", "--release"}
	if linux {
		args = append(args, "--target", "x86_64-linux")
		exe += "-linux" // root/<name> is the work directory
	} else {
		exe += ".exe"
	}
	args = append(args, "-o", exe, "main.mk")
	if out, status := mkCmd(t, work, stdDir, args...); status != 0 {
		if strings.Contains(out, "no usable LLVM") {
			t.Skipf("needs the LLVM toolchain: %s", out)
		}
		t.Fatalf("mk build %s: exit %d\n%s", rel, status, out)
	}
	return exe
}

// perfRtField is the integer after key in line ("ns 123 per-switch 4":
// key "per-switch" gives 4), or -1.
func perfRtField(line, key string) int64 {
	f := strings.Fields(line)
	for i := 0; i+1 < len(f); i++ {
		if f[i] == key {
			if v, err := strconv.ParseInt(f[i+1], 10, 64); err == nil {
				return v
			}
		}
	}
	return -1
}

// perfRtMedian is the median of xs (xs is sorted in place).
func perfRtMedian(xs []int64) int64 {
	slices.Sort(xs)
	return xs[len(xs)/2]
}

// perfRtRun runs exe with args and env (added to the environment), within
// limit, and returns its standard output; a failure fails the test.
func perfRtRun(t *testing.T, exe string, limit time.Duration, env []string, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, args...)
	cmd.Dir = filepath.Dir(exe)
	cmd.Env = append(os.Environ(), env...)
	var se strings.Builder
	cmd.Stderr = &se
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("%s %v %v: %v\nstderr:\n%s", filepath.Base(exe), env, args, err, se.String())
	}
	return strings.ReplaceAll(string(out), "\r\n", "\n")
}

// perfRtWinMonitor starts one pwsh that samples process pid every 50 ms
// until it exits; the returned function waits for it and gives the peak
// working set (the OS's PeakWorkingSet64) and the largest private bytes
// sampled.
func perfRtWinMonitor(t *testing.T, pid int) func() (int64, int64) {
	t.Helper()
	script := fmt.Sprintf(`$ErrorActionPreference = 'SilentlyContinue'; $p = Get-Process -Id %d; $ws = 0; $pr = 0
while ($p -and -not $p.HasExited) { $p.Refresh(); if ($p.PeakWorkingSet64 -gt $ws) { $ws = $p.PeakWorkingSet64 }; if ($p.PrivateMemorySize64 -gt $pr) { $pr = $p.PrivateMemorySize64 }; Start-Sleep -Milliseconds 50 }
"$ws $pr"`, pid)
	cmd := exec.Command("pwsh", "-NoProfile", "-Command", script)
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	return func() (int64, int64) {
		b, _ := io.ReadAll(pipe)
		_ = cmd.Wait()
		var ws, pr int64
		fmt.Sscan(strings.TrimSpace(string(b)), &ws, &pr)
		return ws, pr
	}
}

// perfRtWinNow is the working set, private bytes and handle count of pid.
func perfRtWinNow(pid int) (int64, int64, int64, error) {
	cmd := exec.Command("pwsh", "-NoProfile", "-Command",
		fmt.Sprintf(`$p = Get-Process -Id %d; "$($p.WorkingSet64) $($p.PrivateMemorySize64) $($p.HandleCount)"`, pid))
	out, err := cmd.Output()
	if err != nil {
		return 0, 0, 0, err
	}
	var ws, pr, h int64
	if _, err := fmt.Sscan(strings.TrimSpace(string(out)), &ws, &pr, &h); err != nil {
		return 0, 0, 0, fmt.Errorf("Get-Process said %q: %v", out, err)
	}
	return ws, pr, h, nil
}

// TestPerfRuntime_Windows measures the task switch (pingpong.mk, 10^7
// round trips), the task spawn (spawn.mk, 10^6 spawns), and the memory of
// parked tasks (idle.mk at 1,000, 100,000 and 1,000,000 tasks, held 3 s):
// release, MK_CARRIERS 1 and 8, PERFRT_RUNS runs (default 3) for the
// timed programs, median.
func TestPerfRuntime_Windows(t *testing.T) {
	perfRtOn(t)
	if runtime.GOOS != "windows" {
		t.Skip("Windows; TestPerfRuntime_Linux runs the Linux side")
	}
	root := t.TempDir()
	stdDir := perfRtStd(t, root)
	pp := perfRtBuild(t, root, stdDir, "tasks/pingpong.mk", false)
	sp := perfRtBuild(t, root, stdDir, "tasks/spawn.mk", false)
	idle := perfRtBuild(t, root, stdDir, "tasks/idle.mk", false)
	runs := perfRtEnvInt("PERFRT_RUNS", 3)
	for _, c := range []string{"1", "8"} {
		env := []string{"MK_CARRIERS=" + c}
		var sw, spn []int64
		for range runs {
			out := perfRtRun(t, pp, 10*time.Minute, env, strconv.Itoa(perfRtEnvInt("PERFRT_PP_N", 10_000_000)), "time")
			sw = append(sw, perfRtField(out, "per-switch"))
			out = perfRtRun(t, sp, 10*time.Minute, env, strconv.Itoa(perfRtEnvInt("PERFRT_SPAWN_N", 1_000_000)), "time")
			spn = append(spn, perfRtField(out, "per-spawn"))
		}
		t.Logf("PERF switch carriers=%s: median %d ns of %v (pingpong.mk 10^7 round trips, time / 4n)", c, perfRtMedian(slices.Clone(sw)), sw)
		t.Logf("PERF spawn carriers=%s: median %d ns of %v (spawn.mk 10^6 spawns, 1,000 per scope)", c, perfRtMedian(slices.Clone(spn)), spn)
	}
	counts := []int{1_000, 100_000, 1_000_000}
	if s := os.Getenv("PERFRT_IDLE_N"); s != "" {
		counts = counts[:1]
		for _, f := range strings.Split(s, ",") {
			if v, err := strconv.Atoi(f); err == nil {
				counts = append(counts, v)
			}
		}
	}
	for _, c := range []string{"1", "8"} {
		var baseWS, basePr int64
		for _, n := range counts {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
			cmd := exec.CommandContext(ctx, idle, strconv.Itoa(n), "hold", "3000")
			cmd.Dir = root
			cmd.Env = append(os.Environ(), "MK_CARRIERS="+c)
			var so, se strings.Builder
			cmd.Stdout, cmd.Stderr = &so, &se
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			wait := perfRtWinMonitor(t, cmd.Process.Pid)
			err := cmd.Wait()
			ws, pr := wait()
			cancel()
			out := strings.ReplaceAll(so.String(), "\r\n", "\n")
			if err != nil || !strings.Contains(out, fmt.Sprintf("idle %d woke %d", n, n)) {
				t.Fatalf("idle %d carriers=%s: %v %q\nstderr:\n%s", n, c, err, out, se.String())
			}
			if n == counts[0] {
				baseWS, basePr = ws, pr
				t.Logf("PERF idle n=%d carriers=%s: peak working set %d, private %d (the base)", n, c, ws, pr)
				continue
			}
			k := int64(n - counts[0])
			t.Logf("PERF idle n=%d carriers=%s: peak working set %d (%d bytes per task over the base), private %d (%d per task); spawn and park %d ns per task",
				n, c, ws, (ws-baseWS)/k, pr, (pr-basePr)/k, perfRtField(out, "per-task"))
		}
	}
}

// perfRtClientSrc is the echo client: it holds -n connections to -addr at
// once, each bound to its own loopback source (127.0.0.2 and up, ports
// 20000 to 59999, retrying the next pair on a bind or connect failure), at
// most -par dials in flight; each connection echoes -size bytes once while
// the others connect. Then it prints "held <n> ...", reads the server's
// memory itself when -pid is given on Linux, waits for a line on standard
// input when -wait is set, echoes -size bytes on every connection at once
// (round 2), closes them all and prints "ok <n>". Any mismatch, or a
// connection that cannot be made in 400 tries, exits 1.
const perfRtClientSrc = `package main

import (
	"bufio"
	"bytes"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

func pattern(i, size, round int) []byte {
	b := make([]byte, size)
	for j := range b {
		b[j] = byte(i*31 + j*7 + round)
	}
	return b
}

func echo(c net.Conn, i, size, round int) error {
	want := pattern(i, size, round)
	c.SetDeadline(time.Now().Add(120 * time.Second))
	if _, err := c.Write(want); err != nil {
		return err
	}
	got := make([]byte, size)
	if _, err := io.ReadFull(c, got); err != nil {
		return err
	}
	if !bytes.Equal(got, want) {
		return fmt.Errorf("echo mismatch on connection %d", i)
	}
	return nil
}

func serverMem(pid int) string {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil {
		return err.Error()
	}
	var rss, hwm string
	for _, l := range strings.Split(string(b), "\n") {
		f := strings.Fields(l)
		if len(f) >= 2 && f[0] == "VmRSS:" {
			rss = f[1]
		}
		if len(f) >= 2 && f[0] == "VmHWM:" {
			hwm = f[1]
		}
	}
	maps, _ := os.ReadFile(fmt.Sprintf("/proc/%d/maps", pid))
	fds, _ := os.ReadDir(fmt.Sprintf("/proc/%d/fd", pid))
	return fmt.Sprintf("rss_kib %s hwm_kib %s maps %d fds %d", rss, hwm, bytes.Count(maps, []byte("\n")), len(fds))
}

// goServer is echo.mk in Go, the reference the Mk server is read against:
// it listens on 127.0.0.1:0, prints "listening <port>", accepts n
// connections with one goroutine and a 4,096-byte buffer each, echoes each
// until the client closes, and exits 0 when all are done.
func goServer(n int) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("listening %d\n", l.Addr().(*net.TCPAddr).Port)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		c, err := l.Accept()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer c.Close()
			var buf [4096]byte
			for {
				k, err := c.Read(buf[:])
				if k > 0 {
					if _, werr := c.Write(buf[:k]); werr != nil {
						return
					}
				}
				if err != nil {
					return
				}
			}
		}()
	}
	wg.Wait()
	l.Close()
}

func main() {
	addr := flag.String("addr", "", "server address")
	n := flag.Int("n", 0, "connections")
	size := flag.Int("size", 64, "bytes per echo")
	pid := flag.Int("pid", 0, "server pid (Linux: read its memory when held)")
	par := flag.Int("par", 128, "dials in flight")
	wait := flag.Bool("wait", false, "wait for a line on stdin once held")
	serve := flag.Int("serve", 0, "be the Go reference server for this many connections instead")
	flag.Parse()
	if *serve > 0 {
		goServer(*serve)
		return
	}
	conns := make([]net.Conn, *n)
	var cursor, retries atomic.Int64
	var failed atomic.Value
	dial := func() (net.Conn, error) {
		var last error
		for try := 0; try < 400; try++ {
			k := cursor.Add(1) - 1
			idx := 2 + k/40000
			ip := net.IPv4(127, 0, byte(idx>>8), byte(idx)).To4()
			d := net.Dialer{LocalAddr: &net.TCPAddr{IP: ip, Port: 20000 + int(k%40000)}, Timeout: 30 * time.Second,
				Control: func(_, _ string, rc syscall.RawConn) error { return reuse(rc) }}
			c, err := d.Dial("tcp", *addr)
			if err == nil {
				return c, nil
			}
			last = err
			retries.Add(1)
			if !strings.Contains(err.Error(), "bind") && !strings.Contains(err.Error(), "address already in use") &&
				!strings.Contains(err.Error(), "access permissions") {
				time.Sleep(time.Duration(1+try) * 5 * time.Millisecond)
			}
		}
		return nil, last
	}
	t0 := time.Now()
	sem := make(chan struct{}, *par)
	var wg sync.WaitGroup
	for i := 0; i < *n; i++ {
		sem <- struct{}{}
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			defer func() { <-sem }()
			c, err := dial()
			if err == nil {
				err = echo(c, i, *size, 0)
			}
			if err != nil {
				failed.CompareAndSwap(nil, fmt.Sprintf("connection %d: %v", i, err))
				return
			}
			conns[i] = c
		}(i)
		if f := failed.Load(); f != nil {
			break
		}
	}
	wg.Wait()
	if f := failed.Load(); f != nil {
		fmt.Println("fail", f)
		os.Exit(1)
	}
	held := time.Since(t0)
	fmt.Printf("held %d connect-and-echo %d ms retries %d goos %s\n", *n, held.Milliseconds(), retries.Load(), runtime.GOOS)
	if *pid != 0 && runtime.GOOS == "linux" {
		fmt.Println("server", serverMem(*pid))
	}
	if *wait {
		bufio.NewReader(os.Stdin).ReadString('\n')
	}
	t1 := time.Now()
	for i := 0; i < *n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if err := echo(conns[i], i, *size, 1); err != nil {
				failed.CompareAndSwap(nil, fmt.Sprintf("round 2, connection %d: %v", i, err))
			}
		}(i)
	}
	wg.Wait()
	if f := failed.Load(); f != nil {
		fmt.Println("fail", f)
		os.Exit(1)
	}
	fmt.Printf("round2 %d ms (%d echoes of %d bytes, all connections at once)\n", time.Since(t1).Milliseconds(), *n, *size)
	seq := min(*n, 1000)
	t2 := time.Now()
	for i := 0; i < seq; i++ {
		if err := echo(conns[i], i, *size, 2); err != nil {
			fmt.Println("fail round 3, connection", i, err)
			os.Exit(1)
		}
	}
	fmt.Printf("round3 seq %d ns-per-echo %d\n", seq, time.Since(t2).Nanoseconds()/int64(seq))
	for _, c := range conns {
		c.Close()
	}
	fmt.Printf("ok %d\n", *n)
}
`

const perfRtReuseWindows = `package main

import "syscall"

func reuse(rc syscall.RawConn) error {
	var serr error
	err := rc.Control(func(fd uintptr) { serr = syscall.SetsockoptInt(syscall.Handle(fd), syscall.SOL_SOCKET, syscall.SO_REUSEADDR, 1) })
	if err != nil {
		return err
	}
	return serr
}
`

const perfRtReuseLinux = `package main

import "syscall"

func reuse(rc syscall.RawConn) error {
	var serr error
	err := rc.Control(func(fd uintptr) { serr = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_REUSEADDR, 1) })
	if err != nil {
		return err
	}
	return serr
}
`

// perfRtBuildClient builds the echo client for goos into root.
func perfRtBuildClient(t *testing.T, root, goos string) string {
	t.Helper()
	dir := filepath.Join(root, "client-"+goos)
	files := map[string]string{
		"go.mod":           "module echoclient\n\ngo 1.27\n",
		"main.go":          perfRtClientSrc,
		"reuse_windows.go": perfRtReuseWindows,
		"reuse_linux.go":   perfRtReuseLinux,
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for n, s := range files {
		if err := os.WriteFile(filepath.Join(dir, n), []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	goTool := filepath.Join(runtime.GOROOT(), "bin", "go")
	if runtime.GOOS == "windows" {
		goTool += ".exe"
	}
	exe := filepath.Join(root, "echoclient-"+goos)
	if goos == "windows" {
		exe += ".exe"
	}
	cmd := exec.Command(goTool, "build", "-o", exe, ".")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOOS="+goos, "GOARCH=amd64", "CGO_ENABLED=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build echo client: %v\n%s", err, out)
	}
	return exe
}

// perfRtEchoCounts is the comma list in environment variable k (default
// def), each count capped at perfRtSocketCap; counts below 1 are dropped.
func perfRtEchoCounts(k, def string) []int {
	s := os.Getenv(k)
	if s == "" {
		s = def
	}
	var ns []int
	for _, f := range strings.Split(s, ",") {
		if v, err := strconv.Atoi(strings.TrimSpace(f)); err == nil && v > 0 {
			ns = append(ns, min(v, perfRtSocketCap))
		}
	}
	return ns
}

// perfRtWinTimeWait waits, at most 5 minutes, until the machine holds at
// most 500 TCP connections in TIME_WAIT or a closing state (netstat), so one echo run's closed
// connections are gone from the kernel before the next run opens its own:
// the socket cap counts what the kernel holds, not only what a process
// does. It returns the count it saw last.
func perfRtWinTimeWait(t *testing.T) int {
	t.Helper()
	deadline := time.Now().Add(5 * time.Minute)
	for {
		out, err := exec.Command("netstat", "-an", "-p", "tcp").Output()
		if err != nil {
			t.Fatalf("netstat: %v", err)
		}
		n := 0
		for _, st := range []string{"TIME_WAIT", "FIN_WAIT_1", "FIN_WAIT_2", "CLOSE_WAIT", "CLOSING", "LAST_ACK"} {
			n += strings.Count(string(out), st)
		}
		if n <= 500 || time.Now().After(deadline) {
			return n
		}
		time.Sleep(5 * time.Second)
	}
}

// perfRtWinPool is the kernel's nonpaged pool in bytes (Win32_PerfRawData_
// PerfOS_Memory, which does not depend on the display language), or -1.
// Socket state of AFD and TCP lives there, outside every process counter.
func perfRtWinPool() int64 {
	out, err := exec.Command("pwsh", "-NoProfile", "-Command",
		"(Get-CimInstance Win32_PerfRawData_PerfOS_Memory).PoolNonpagedBytes").Output()
	if err != nil {
		return -1
	}
	v, err := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
	if err != nil {
		return -1
	}
	return v
}

// perfRtFit is the least-squares line y = a + b x through the points.
func perfRtFit(xs, ys []float64) (a, b float64) {
	n := float64(len(xs))
	var sx, sy, sxx, sxy float64
	for i := range xs {
		sx += xs[i]
		sy += ys[i]
		sxx += xs[i] * xs[i]
		sxy += xs[i] * ys[i]
	}
	d := n*sxx - sx*sx
	if d == 0 {
		return sy / n, 0
	}
	b = (n*sxy - sx*sy) / d
	return (sy - b*sx) / n, b
}

// perfRtEchoPoint is one echo run's server-side growth while every
// connection is held: working set, private bytes, handles and the
// machine's nonpaged pool, each minus its value once the server listened.
type perfRtEchoPoint struct {
	n                 int
	ws, pr, h, pool   int64
	round2ms, round3n int64
}

// TestPerfRuntime_EchoWindows runs tests/programs/echo.mk (release) at the
// PERFRT_ECHO_N counts (default 2,000, 5,000 and 10,000; each capped at
// perfRtSocketCap) with MK_CARRIERS 1 and the default (one per CPU,
// PERFRT_ECHO_CARRIERS overrides), the Go client holding all of them at
// once; with PERFRT_ECHO_GO=1 the client's own Go server (goServer, the
// same program in Go) runs the same counts as the reference. Each run logs
// the server's working set, private bytes and handle count and the
// machine's nonpaged pool while every connection is held, round 2 (one echo
// on every connection at once) and round 3 (1,000 echoes one after
// another); then the least-squares line of each memory counter against the
// count, and that line at 100,000 connections (the budget's count, which
// this machine does not hold: perfRtSocketCap). Before each run it waits
// for the previous run's TIME_WAIT connections to leave the kernel.
func TestPerfRuntime_EchoWindows(t *testing.T) {
	perfRtOn(t)
	if runtime.GOOS != "windows" {
		t.Skip("Windows; TestPerfRuntime_Linux runs the Linux side")
	}
	root := t.TempDir()
	stdDir := perfRtStd(t, root)
	server := perfRtBuild(t, root, stdDir, "echo.mk", false)
	client := perfRtBuildClient(t, root, "windows")
	counts := perfRtEchoCounts("PERFRT_ECHO_N", "2000,5000,10000")
	type kind struct{ name, carriers string }
	var kinds []kind
	carriers := []string{"1", ""}
	if s := os.Getenv("PERFRT_ECHO_CARRIERS"); s != "" {
		carriers = strings.Split(s, ",")
	}
	for _, c := range carriers {
		kinds = append(kinds, kind{"mk", c})
	}
	if os.Getenv("PERFRT_ECHO_GO") == "1" {
		kinds = append(kinds, kind{"go", ""})
	}
	for _, k := range kinds {
		var pts []perfRtEchoPoint
		for _, n := range counts {
			tw := perfRtWinTimeWait(t)
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
			var srv *exec.Cmd
			if k.name == "go" {
				srv = exec.CommandContext(ctx, client, "-serve", strconv.Itoa(n))
			} else {
				srv = exec.CommandContext(ctx, server, "0", strconv.Itoa(n))
			}
			srv.Dir = root
			srv.Env = os.Environ()
			if k.carriers != "" {
				srv.Env = append(srv.Env, "MK_CARRIERS="+k.carriers)
			}
			var serr strings.Builder
			srv.Stderr = &serr
			sout, err := srv.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			if err := srv.Start(); err != nil {
				t.Fatal(err)
			}
			line, _ := bufio.NewReader(sout).ReadString('\n')
			port := perfRtField(line, "listening")
			if port <= 0 {
				cancel()
				t.Fatalf("%s server said %q, stderr %q", k.name, line, serr.String())
			}
			ws0, pr0, h0, _ := perfRtWinNow(srv.Process.Pid)
			pool0 := perfRtWinPool()
			cl := exec.CommandContext(ctx, client, "-addr", fmt.Sprintf("127.0.0.1:%d", port), "-n", strconv.Itoa(n), "-wait")
			cl.Dir = root
			cin, _ := cl.StdinPipe()
			cout, _ := cl.StdoutPipe()
			var cerr strings.Builder
			cl.Stderr = &cerr
			if err := cl.Start(); err != nil {
				t.Fatal(err)
			}
			p := perfRtEchoPoint{n: n}
			sc := bufio.NewScanner(cout)
			var lines []string
			for sc.Scan() {
				l := sc.Text()
				lines = append(lines, l)
				if strings.HasPrefix(l, "held ") {
					ws, pr, h, err := perfRtWinNow(srv.Process.Pid)
					if err != nil {
						t.Errorf("Get-Process: %v", err)
					}
					pool := perfRtWinPool()
					p.ws, p.pr, p.h, p.pool = ws-ws0, pr-pr0, h-h0, pool-pool0
					t.Logf("PERF echo server=%s carriers=%q n=%d held: working set %d -> %d (%d per connection), private %d -> %d (%d per connection), handles %d -> %d, nonpaged pool %d -> %d (%d per connection, whole machine); TIME_WAIT before the run %d",
						k.name, k.carriers, n, ws0, ws, p.ws/int64(n), pr0, pr, p.pr/int64(n), h0, h, pool0, pool, p.pool/int64(n), tw)
					io.WriteString(cin, "go\n")
				}
				if strings.HasPrefix(l, "round2 ") {
					p.round2ms = perfRtField(l, "round2")
				}
				if strings.HasPrefix(l, "round3 ") {
					p.round3n = perfRtField(l, "ns-per-echo")
				}
			}
			cerrv := cl.Wait()
			done := make(chan error, 1)
			go func() { done <- srv.Wait() }()
			var srvErr error
			select {
			case srvErr = <-done:
			case <-time.After(60 * time.Second):
				srvErr = fmt.Errorf("server did not exit 60 s after the client")
				_ = srv.Process.Kill()
			}
			cancel()
			t.Logf("PERF echo server=%s carriers=%q n=%d: client %s", k.name, k.carriers, n, strings.Join(lines, " | "))
			if cerrv != nil || srvErr != nil || !slices.Contains(lines, fmt.Sprintf("ok %d", n)) {
				t.Errorf("echo server=%s carriers=%q n=%d: client %v %q, server %v\nserver stderr: %s", k.name, k.carriers, n, cerrv, cerr.String(), srvErr, serr.String())
				continue
			}
			t.Logf("PERF echo server=%s carriers=%q n=%d: round 2 %d ms, %d ns per echo (all at once); round 3 %d ns per echo (one at a time)",
				k.name, k.carriers, n, p.round2ms, p.round2ms*1_000_000/int64(n), p.round3n)
			pts = append(pts, p)
		}
		if len(pts) < 2 {
			continue
		}
		for _, m := range []struct {
			name string
			get  func(perfRtEchoPoint) int64
		}{
			{"working set", func(p perfRtEchoPoint) int64 { return p.ws }},
			{"private", func(p perfRtEchoPoint) int64 { return p.pr }},
			{"nonpaged pool", func(p perfRtEchoPoint) int64 { return p.pool }},
		} {
			var xs, ys []float64
			for _, p := range pts {
				xs = append(xs, float64(p.n))
				ys = append(ys, float64(m.get(p)))
			}
			a, b := perfRtFit(xs, ys)
			t.Logf("PERF echo server=%s carriers=%q fit %s: %.0f + %.1f bytes per connection over n = %v; at 100,000 (extrapolated, not measured): %.2f GB",
				k.name, k.carriers, m.name, a, b, xs, (a+b*100_000)/1e9)
		}
	}
}

// TestPerfRuntime_Linux runs the same measurements on x86_64-linux in WSL,
// in one wsl.exe client: pingpong.mk and spawn.mk timed (MK_CARRIERS 1 and
// 8, PERFRT_RUNS runs); idle.mk at 1,000 and the PERFRT_LIN_IDLE counts
// (default 30,000 and 100,000) held 3 s, the shell sampling VmHWM and the
// mapping count every 50 ms and killing a run still alive after 60 s (the
// mapping bound: past (vm.max_map_count - maps) / 2 tasks s.go waits for good);
// and echo.mk at the PERFRT_LIN_ECHO counts (default 2,000, 5,000 and
// 10,000, each capped at perfRtSocketCap), MK_CARRIERS 1 and 8, with the
// Linux client, which reads the server's memory itself (the shell reads the
// base once the server listens), after waiting for the previous run's
// TIME_WAIT connections to leave the kernel; PERFRT_ECHO_GO=1 adds the
// client's Go reference server. PERFRT_LIN_PARTS (default
// "time,idle,echo") picks the parts, so a run holds the WSL lock only as
// long as it needs.
func TestPerfRuntime_Linux(t *testing.T) {
	perfRtOn(t)
	rtsNeedWSL(t)
	root := t.TempDir()
	stdDir := perfRtStd(t, root)
	pp := perfRtBuild(t, root, stdDir, "tasks/pingpong.mk", true)
	sp := perfRtBuild(t, root, stdDir, "tasks/spawn.mk", true)
	idle := perfRtBuild(t, root, stdDir, "tasks/idle.mk", true)
	echo := perfRtBuild(t, root, stdDir, "echo.mk", true)
	client := perfRtBuildClient(t, root, "linux")
	runs := perfRtEnvInt("PERFRT_RUNS", 3)
	idleN := os.Getenv("PERFRT_LIN_IDLE")
	if idleN == "" {
		idleN = "30000,100000"
	}
	echoN := perfRtEchoCounts("PERFRT_LIN_ECHO", "2000,5000,10000")
	parts := os.Getenv("PERFRT_LIN_PARTS")
	if parts == "" {
		parts = "time,idle,echo"
	}
	var sh strings.Builder
	sh.WriteString(`#!/bin/sh
cd "$(dirname "$0")"
echo "uname $(uname -r) max_map_count $(cat /proc/sys/vm/max_map_count) nofile $(ulimit -n)/$(ulimit -Hn) nproc $(nproc) ports $(cat /proc/sys/net/ipv4/ip_local_port_range | tr '\t' ' ')"
mon() {
  pid=$1; lim=$2; hwm=0; maps=0; i=0
  while kill -0 $pid 2>/dev/null; do
    h=$(awk '/VmHWM/{print $2}' /proc/$pid/status 2>/dev/null)
    m=$(wc -l < /proc/$pid/maps 2>/dev/null)
    [ -n "$h" ] && hwm=$h
    [ -n "$m" ] && [ "$m" -gt "$maps" ] && maps=$m
    i=$((i+1)); if [ $i -gt $lim ]; then kill -9 $pid; echo "killed after $((lim/20)) s"; break; fi
    sleep 0.05
  done
  echo "mem hwm_kib $hwm maps $maps"
}
# tw waits, at most 300 s, until at most 500 TCP entries other than
# listeners are left (TIME_WAIT and every closing state; read 2 s after the
# run, when the last closes have reached TIME_WAIT), so one echo run's
# closed connections leave the kernel before the next run opens its own
# (the socket cap counts what the kernel holds).
tw() {
  sleep 2; i=0; while [ $i -lt 60 ]; do
    n=$(cat /proc/net/tcp /proc/net/tcp6 2>/dev/null | awk '$4!="0A" && $4!="st"' | wc -l)
    [ "$n" -le 500 ] && break; sleep 5; i=$((i+1))
  done
  echo "time_wait $n"
}
`)
	if strings.Contains(parts, "time") {
		for _, c := range []string{"1", "8"} {
			for range runs {
				fmt.Fprintf(&sh, "echo \"pingpong carriers=%s $(MK_CARRIERS=%s timeout 300 ./%s %d time | tail -1)\"\n", c, c, filepath.Base(pp), perfRtEnvInt("PERFRT_PP_N", 10_000_000))
				fmt.Fprintf(&sh, "echo \"spawn carriers=%s $(MK_CARRIERS=%s timeout 300 ./%s %d time | tail -1)\"\n", c, c, filepath.Base(sp), perfRtEnvInt("PERFRT_SPAWN_N", 1_000_000))
			}
		}
	}
	if strings.Contains(parts, "idle") {
		for _, c := range []string{"1", "8"} {
			for _, n := range append([]string{"1000"}, strings.Split(idleN, ",")...) {
				fmt.Fprintf(&sh, "echo \"== idle n=%s carriers=%s\"; MK_CARRIERS=%s ./%s %s hold 3000 > idle.out 2>&1 & p=$!; mon $p 1200; wait $p; echo \"exit $? $(tr '\n' ' ' < idle.out)\"\n",
					n, c, c, filepath.Base(idle), n)
			}
		}
	}
	servers := []struct{ name, cmd, carriers string }{
		{"mk", "./" + filepath.Base(echo) + " 0", "1"},
		{"mk", "./" + filepath.Base(echo) + " 0", "8"},
	}
	// PERFRT_LIN_IO (for instance "epoll") adds the Mk server with MK_IO set
	// to that backend, 1 and 8 carriers, after the default (io_uring) ones.
	if io := os.Getenv("PERFRT_LIN_IO"); io != "" {
		for _, c := range []string{"1", "8"} {
			servers = append(servers, struct{ name, cmd, carriers string }{"mk-" + io, "MK_IO=" + io + " ./" + filepath.Base(echo) + " 0", c})
		}
	}
	if os.Getenv("PERFRT_ECHO_GO") == "1" {
		servers = append(servers, struct{ name, cmd, carriers string }{"go", "./" + filepath.Base(client) + " -serve", ""})
	}
	if strings.Contains(parts, "echo") {
		for _, sv := range servers {
			for _, n := range echoN {
				fmt.Fprintf(&sh, `tw
echo "== echo server=%[5]s n=%[1]d carriers=%[2]s"
MK_CARRIERS=%[2]s %[3]s %[1]d > srv.out 2> srv.err & sp=$!
for i in $(seq 1 200); do grep -q listening srv.out && break; sleep 0.05; done
port=$(awk '/listening/{print $2}' srv.out)
echo "server base rss_kib $(awk '/VmRSS/{print $2}' /proc/$sp/status)"
timeout 300 ./%[4]s -addr 127.0.0.1:$port -n %[1]d -pid $sp
echo "client exit $?"
i=0; while kill -0 $sp 2>/dev/null && [ $i -lt 600 ]; do sleep 0.1; i=$((i+1)); done
kill -9 $sp 2>/dev/null; wait $sp; echo "server exit $? stderr: $(head -c 400 srv.err)"
`, n, sv.carriers, sv.cmd, filepath.Base(client), sv.name)
			}
		}
	}
	script := filepath.Join(root, "perf.sh")
	if err := os.WriteFile(script, []byte(sh.String()), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Minute)
	defer cancel()
	// The output is also kept in perf.sh.log beside the script, so a long run can
	// be followed from Windows (the test logs it only at the end).
	wslGuard()
	cmd := exec.CommandContext(ctx, "wsl.exe", "-e", "sh", "-c", "sh \"$0\" 2>&1 | tee \"$0.log\"", rtsWSLPath(script))
	cmd.WaitDelay = 10 * time.Second
	out, err := cmd.CombinedOutput()
	for _, line := range strings.Split(strings.ReplaceAll(string(out), "\r\n", "\n"), "\n") {
		if line != "" {
			t.Log("PERF " + line)
		}
	}
	if err != nil {
		t.Fatalf("wsl.exe: %v", err)
	}
}
