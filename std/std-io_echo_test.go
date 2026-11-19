// Why: an echo server under many concurrent connections exercises std.net,
// the poller and the scheduler together, as a real server does.
// What: TestStdIo_EchoServer, natively and through TestStdIo_LinuxRuns.
// How: tests/programs/echo.mk serves N connections, one task each, and
// tests/programs/echoclient.mk opens N connections at once, sends each
// connection's bytes in writes of 1 to 1,024 bytes and checks every echo.
// Both are the repository's own programs, built with the std under test,
// on Windows natively and on x86_64-linux under WSL (one wsl.exe call per
// batch, under tools/wsl-lock.ps1 like the rest of TestStdIo_LinuxRuns).

package std

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// echoCase is one server and client run.
type echoCase struct {
	conns, size, carriers int
}

var echoCases = []echoCase{
	{1, 100_000, 1}, {8, 1 << 20, 1}, {8, 1 << 20, 8}, {32, 65_536, 8}, {32, 65_536, 16},
}

// echoSource reads a program of tests/programs: from the repository when
// the test runs in std/, else from the copy gotest.sh puts beside std.
func echoSource(t *testing.T, name string) string {
	t.Helper()
	for _, dir := range []string{filepath.Join("..", "tests", "programs"), filepath.Join("..", "programs")} {
		if b, err := os.ReadFile(filepath.Join(dir, name)); err == nil {
			return string(b)
		}
	}
	t.Fatalf("tests/programs/%s not found", name)
	return ""
}

// TestStdIo_EchoServer: the echo server answers 1 to 32 concurrent
// clients byte for byte with MK_CARRIERS 1, 8 and 16, and exits 0 once
// every connection closed.
func TestStdIo_EchoServer(t *testing.T) { stdIoEcho(t, hostIo) }

func stdIoEcho(t *testing.T, h ioHost) {
	server := h.build(t, nil, echoSource(t, "echo.mk"))
	client := h.build(t, nil, echoSource(t, "echoclient.mk"))
	if h.linux {
		echoLinux(t, server, client)
		return
	}
	for _, c := range echoCases {
		name := fmt.Sprintf("conns=%d/size=%d/carriers=%d", c.conns, c.size, c.carriers)
		t.Run(name, func(t *testing.T) { echoWindows(t, server, client, c) })
	}
}

// freePort is a loopback port the OS just handed out and released.
func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func echoWindows(t *testing.T, server, client string, c echoCase) {
	env := append(os.Environ(), fmt.Sprintf("MK_CARRIERS=%d", c.carriers))
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	port := freePort(t)
	srv := exec.CommandContext(ctx, server, fmt.Sprint(port), fmt.Sprint(c.conns))
	srv.Dir, srv.Env = filepath.Dir(server), env
	out, err := srv.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var serr strings.Builder
	srv.Stderr = &serr
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(out).ReadString('\n')
	if want := fmt.Sprintf("listening %d\n", port); err != nil || line != want {
		_ = srv.Process.Kill()
		_ = srv.Wait()
		t.Fatalf("server said %q (%v), want %q\nstderr: %s", line, err, want, serr.String())
	}
	cl := exec.CommandContext(ctx, client, fmt.Sprint(port), fmt.Sprint(c.conns), fmt.Sprint(c.size))
	cl.Dir, cl.Env = filepath.Dir(client), env
	start := time.Now()
	got, cerr := cl.CombinedOutput()
	took := time.Since(start)
	werr := srv.Wait()
	if ctx.Err() != nil {
		t.Fatalf("no exit within 60 s (server %v, client %v)", werr, cerr)
	}
	if cerr != nil || string(got) != fmt.Sprintf("ok %d\n", c.conns) {
		t.Fatalf("client: %v, output %q", cerr, got)
	}
	if werr != nil {
		t.Fatalf("server: %v\nstderr: %s", werr, serr.String())
	}
	t.Logf("%d connections x %d bytes echoed in %v", c.conns, c.size, took)
}

// echoLinux runs every case in one bash script under one wsl.exe: each
// case picks a port, starts the server in the background, waits for its
// line, runs the client, then waits for the server; a busy port is tried
// again with another (three tries).
func echoLinux(t *testing.T, server, client string) {
	dir := filepath.Dir(server)
	if filepath.Dir(client) != dir {
		cp := filepath.Join(dir, "echoclient")
		b, err := os.ReadFile(client)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(cp, b, 0o755); err != nil {
			t.Fatal(err)
		}
		client = cp
	}
	var sb strings.Builder
	sb.WriteString("#!/bin/bash\n")
	for i, c := range echoCases {
		fmt.Fprintf(&sb, `r=fail
for try in 1 2 3; do
  p=$((20000 + RANDOM %% 30000))
  MK_CARRIERS=%[2]d timeout -k 5 60 ./%[3]s $p %[4]d > echo_%[1]d.srv 2> echo_%[1]d.err &
  pid=$!
  for w in $(seq 200); do grep -q listening echo_%[1]d.srv && break; kill -0 $pid 2>/dev/null || break; sleep 0.05; done
  if grep -q "listening $p" echo_%[1]d.srv; then
    MK_CARRIERS=%[2]d timeout -k 5 60 ./%[5]s $p %[4]d %[6]d > echo_%[1]d.cli 2>&1
    c=$?
    wait $pid
    r="client $c server $?"
    break
  fi
  wait $pid
done
echo "$r" > echo_%[1]d.res
`, i, c.carriers, filepath.Base(server), c.conns, filepath.Base(client), c.size)
	}
	if err := os.WriteFile(filepath.Join(dir, "echo_run.sh"), []byte(sb.String()), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	wslGuard()
	cmd := exec.CommandContext(ctx, "wsl.exe", "--cd", stdioWSLPath(dir), "-e", "bash", "./echo_run.sh")
	cmd.WaitDelay = 10 * time.Second
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("wsl.exe bash echo_run.sh: %v\n%s", err, b)
	}
	for i, c := range echoCases {
		read := func(ext string) string {
			b, _ := os.ReadFile(filepath.Join(dir, fmt.Sprintf("echo_%d.%s", i, ext)))
			return string(b)
		}
		res, cli := strings.TrimSpace(read("res")), read("cli")
		if res != "client 0 server 0" || cli != fmt.Sprintf("ok %d\n", c.conns) {
			t.Errorf("conns=%d size=%d carriers=%d: %s, client %q, server stderr %q", c.conns, c.size, c.carriers, res, cli, read("err"))
		}
	}
}
