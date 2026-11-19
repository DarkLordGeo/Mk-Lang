// Why: std's I/O modules must behave the same on Linux as on Windows.
// What: TestStdIo_LinuxRuns and ioHost, ioCall and the batch runner the
// std-io tests share.
// How: each program is cross-built with --target x86_64-linux, run under
// WSL and judged by the Windows run's functions, with the differences the
// OS makes stated where they are judged (an exit status is 8 bits on Linux;
// System.Env compares names exactly).
//
// TestStdIo_LinuxRuns starts wsl.exe, so it runs only under
// tools/wsl-lock.ps1 with go test -p 1 (parallel WSL clients hang WSL).
// Each judged batch of runs is one bash script and one wsl.exe call, never
// one per case, and the subtests run one after another.

package std

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// ioHost builds and runs the std-io test programs on one target: the
// host (natively) or x86_64-linux (through WSL).
type ioHost struct{ linux bool }

var (
	hostIo  = ioHost{}
	linuxIo = ioHost{linux: true}
)

// ioCall is one run of a test program: its standard input, extra
// environment (NAME=VALUE) and arguments; watch > 0 is a watchdog in
// seconds, after which the run is killed and reports status 124 (as
// timeout(1) does), so a deadlock fails the test instead of hanging it.
// pipe: on Linux, standard input and output are pipes (cat in | exe |
// cat > out), not the files the batch redirects, so a program can open
// /dev/stdin or /dev/stdout by path and get a descriptor with no position
// (TestSTDIO_FileOnNoPositionHandle). On Windows they are pipes already (os/exec).
type ioCall struct {
	stdin string
	env   []string
	args  []string
	watch int
	pipe  bool
}

// build builds src (with fixtures) for the host's target.
func (h ioHost) build(t *testing.T, fixtures map[string]string, src string) string {
	t.Helper()
	if h.linux {
		return buildNative(t, fixtures, src, "--target", "x86_64-linux")
	}
	return buildNative(t, fixtures, src)
}

// run runs exe once in its own directory.
func (h ioHost) run(t *testing.T, exe, stdin string, args ...string) nativeRun {
	t.Helper()
	return h.runMany(t, exe, []ioCall{{stdin: stdin, args: args}})[0]
}

// runMany runs exe once per call, in order, in exe's directory. On Linux
// the calls are one bash script run by one wsl.exe.
func (h ioHost) runMany(t *testing.T, exe string, calls []ioCall) []nativeRun {
	t.Helper()
	if h.linux {
		return runWSLBatch(t, exe, calls)
	}
	out := make([]nativeRun, len(calls))
	for i, c := range calls {
		if len(c.env) == 0 && c.watch == 0 {
			out[i] = runNative(t, exe, c.stdin, c.args...)
			continue
		}
		out[i] = runNativeEnv(t, exe, c)
	}
	return out
}

// exitCode is the status a parent sees for a program's exit(code): the
// low 8 bits on Linux, all 32 on Windows.
func (h ioHost) exitCode(code int) int {
	if h.linux {
		return code & 0xff
	}
	return code
}

// runNativeEnv is runNative with extra environment variables and the
// call's watchdog.
func runNativeEnv(t *testing.T, exe string, c ioCall) nativeRun {
	t.Helper()
	ctx := context.Background()
	if c.watch > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(c.watch)*time.Second)
		defer cancel()
	}
	cmd := exec.CommandContext(ctx, exe, c.args...)
	cmd.Dir = filepath.Dir(exe)
	cmd.Env = append(os.Environ(), c.env...)
	cmd.Stdin = strings.NewReader(c.stdin)
	var o, e bytes.Buffer
	cmd.Stdout, cmd.Stderr = &o, &e
	start := time.Now()
	err := cmd.Run()
	r := nativeRun{stdout: o.String(), stderr: e.String(), took: time.Since(start)}
	if ctx.Err() != nil {
		r.code = 124
		return r
	}
	if ee, ok := err.(*exec.ExitError); ok {
		r.code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return r
}

// shQuote is s as one bash word.
func shQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// stdioWSLPath is the /mnt form of a Windows path.
func stdioWSLPath(p string) string {
	p = filepath.ToSlash(p)
	if len(p) > 2 && p[1] == ':' {
		return "/mnt/" + strings.ToLower(p[:1]) + p[2:]
	}
	return p
}

// runWSLBatch writes one bash script that runs exe for every call, each
// with its stdin from a file and its stdout, stderr and status into files,
// runs it with one wsl.exe call (bounded by 10 minutes), and reads the
// results back. The script, not wsl.exe's command line, carries the
// arguments, so empty and non-ASCII arguments arrive unchanged.
func runWSLBatch(t *testing.T, exe string, calls []ioCall) []nativeRun {
	t.Helper()
	dir := filepath.Dir(exe)
	var sb strings.Builder
	sb.WriteString("#!/bin/bash\n")
	for i, c := range calls {
		in := fmt.Sprintf("mkio_%d.in", i)
		if err := os.WriteFile(filepath.Join(dir, in), []byte(c.stdin), 0o644); err != nil {
			t.Fatal(err)
		}
		if c.pipe {
			fmt.Fprintf(&sb, "cat %s | ", in)
		}
		if c.watch > 0 {
			fmt.Fprintf(&sb, "timeout -k 5 %d ", c.watch)
		}
		sb.WriteString("env")
		for _, e := range c.env {
			sb.WriteString(" " + shQuote(e))
		}
		sb.WriteString(" ./" + shQuote(filepath.Base(exe)))
		for _, a := range c.args {
			sb.WriteString(" " + shQuote(a))
		}
		if c.pipe {
			fmt.Fprintf(&sb, " 2> mkio_%d.err | cat > mkio_%d.out; echo ${PIPESTATUS[1]} > mkio_%d.code\n", i, i, i)
			continue
		}
		fmt.Fprintf(&sb, " < %s > mkio_%d.out 2> mkio_%d.err; echo $? > mkio_%d.code\n", in, i, i, i)
	}
	if err := os.WriteFile(filepath.Join(dir, "mkio_run.sh"), []byte(sb.String()), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	wslGuard()
	cmd := exec.CommandContext(ctx, "wsl.exe", "--cd", stdioWSLPath(dir), "-e", "bash", "./mkio_run.sh")
	cmd.WaitDelay = 10 * time.Second
	var o bytes.Buffer
	cmd.Stdout, cmd.Stderr = &o, &o
	if err := cmd.Run(); err != nil {
		t.Fatalf("wsl.exe bash mkio_run.sh: %v\n%s", err, o.String())
	}
	out := make([]nativeRun, len(calls))
	for i := range calls {
		read := func(ext string) string {
			b, err := os.ReadFile(filepath.Join(dir, fmt.Sprintf("mkio_%d.%s", i, ext)))
			if err != nil {
				t.Fatalf("call %d %v: %v", i, calls[i].args, err)
			}
			return string(b)
		}
		code, err := strconv.Atoi(strings.TrimSpace(read("code")))
		if err != nil {
			t.Fatalf("call %d: status %v", i, err)
		}
		out[i] = nativeRun{stdout: read("out"), stderr: read("err"), code: code}
	}
	return out
}

var stdioWSL struct {
	once sync.Once
	why  string
}

// stdioNeedWSL skips the test (fails it under MK_GATE=1) unless
// `wsl.exe -e true` answers within 30 s. It never retries.
func stdioNeedWSL(t *testing.T) {
	t.Helper()
	linuxGate(t)
	stdioWSL.once.Do(func() {
		wslGuard()
		p, err := exec.LookPath("wsl.exe")
		if err != nil {
			stdioWSL.why = "wsl.exe not found"
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, p, "-e", "true")
		cmd.WaitDelay = 5 * time.Second
		if err := cmd.Run(); err != nil {
			stdioWSL.why = fmt.Sprintf("`wsl.exe -e true` did not answer within 30 s: %v", err)
		}
	})
	if stdioWSL.why != "" {
		if os.Getenv("MK_GATE") == "1" {
			t.Fatalf("needs WSL: %s", stdioWSL.why)
		}
		t.Skipf("needs WSL: %s", stdioWSL.why)
	}
}

// TestStdIo_LinuxRuns runs every std-io program test on x86_64-linux:
// the Writer and Reader traces against bufio, the process streams (with
// their M:N contention case), stdin, System.Env, std.fs, std.time,
// std.proc and std.net (std-io_rw_test.go, std-io_mn_test.go,
// std-io_mods_test.go; std.net skips while the runtime's sockets are
// stubs), the scripted-Io fault cases (std-io_fault_test.go), the test
// blocks (std-io_blocks_test.go, and under the real mk test, MkTest:
// std-io_coreblocks_test.go), signal delivery (std-io_signal_test.go), std.net
// against the real OS
// (std-io_netpaths_test.go) and the echo server
// (std-io_echo_test.go).
func TestStdIo_LinuxRuns(t *testing.T) {
	stdioNeedWSL(t)
	for _, c := range []struct {
		name string
		f    func(*testing.T, ioHost)
	}{
		{"WriterMatchesBufio", ioWriterMatchesBufio}, {"ReaderMatchesBufio", ioReaderMatchesBufio}, {"StreamsKeepEveryByte", ioStreamsKeepEveryByte}, {"StreamLockContention", ioStreamLockContention}, {"StdinRoundTrip", ioStdinRoundTrip},
		{"EnvNames", ioEnvNames}, {"FsErrorMapping", ioFsErrorMapping}, {"ReadBytesMatchesOsReadFile", ioReadBytesMatchesOsReadFile}, {"WholeFileOps", ioWholeFileOps},
		{"FilePositions", ioFilePositions}, {"TimeDurations", ioTimeDurations}, {"ProcExit", ioProcExit}, {"SignalDelivery", ioSignalDelivery}, {"NetLoopback", ioNetLoopback},
		{"Fault", stdIoFault}, {"TestBlocks", stdIoBlocks}, {"Echo", stdIoEcho},
		{"CancelledWrites", ioCancelledWrites}, {"NegativePosition", ioNegativePosition}, {"FileOnNoPositionHandle", ioFileOnNoPositionHandle}, {"NoSeekFallback", ioNoSeekFallback},
		{"ReadBytesArenaBound", ioReadBytesArenaBound}, {"ErrorPathsReleaseHandles", ioErrorPathsReleaseHandles}, {"MkTest", stdIoCoreBlocks},
		{"NetInvalidAddress", ioNetInvalidAddress}, {"NetPathsReleaseHandles", ioNetPathsReleaseHandles}, {"ConcurrentStdinReaders", ioConcurrentStdinReaders},
	} {
		t.Run(c.name, func(t *testing.T) { c.f(t, linuxIo) })
	}
}
