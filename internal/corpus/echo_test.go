// Why: the async runtime and std's TCP stack must serve many concurrent
// connections correctly on every runnable target.
// What: TestEchoServer over tests/programs/echo.mk, with echoClients and
// echoClientInWSL.
// How: 100 seeded clients send 64 KiB each and compare the echo.
//
// tests/programs/echo.mk is built for each target and started with
// `0 100` (port 0, serve 100 connections). Its first line is `listening
// <port>`. Then 100 concurrent TCP clients on loopback each send 64 KiB in
// random-sized writes from a fixed seed, close their write half, and read
// the echo back; every echo must equal what was sent, and the server must
// exit 0 with nothing more on stdout or stderr once its scope joins. On
// Linux the clients run on Windows against WSL's forwarded loopback; when
// forwarding is unavailable (the first dial fails), the Mk client
// echoclient.mk runs inside WSL instead and must print "ok 100".

package corpus

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	echoConns = 100
	echoBytes = 64 << 10
	echoSeed  = 0x6d6b2d6563686f // "mk-echo"
)

func TestEchoServer(t *testing.T) {
	root := repoRoot(t)
	tools := needLLVM(t)
	r := testRunner(t, root, tools)
	for _, target := range []string{"x86_64-windows", "x86_64-linux"} {
		t.Run(target, func(t *testing.T) {
			exe, err := r.Build(ProgramsDir+"/echo.mk", target, "dev")
			if why := blockedBuild(err); why != "" {
				skipOrFatal(t, gateRun(), "BLOCKED: %s", why)
			}
			if err != nil {
				t.Fatal(err)
			}
			needWSLRun(t, target, true)
			if target == "x86_64-linux" {
				testWSLSlots <- struct{}{}
				defer func() { <-testWSLSlots }()
			}
			// The VM ends a Linux server 10 s before the client side gives up,
			// so no server outlives the test.
			argv, err := runArgv(target, filepath.Dir(exe), exe, []string{"0", strconv.Itoa(echoConns)}, 170*time.Second)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
			defer cancel()
			srv := exec.CommandContext(ctx, argv[0], argv[1:]...)
			srv.Dir = filepath.Dir(exe)
			srv.WaitDelay = 5 * time.Second
			out, err := srv.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			var stderr bytes.Buffer
			srv.Stderr = &stderr
			if err := srv.Start(); err != nil {
				t.Fatal(err)
			}
			waited := false
			defer func() {
				if !waited {
					_ = srv.Process.Kill()
					_ = srv.Wait()
				}
			}()
			br := bufio.NewReader(out)
			port, err := readListening(br)
			if err != nil {
				t.Fatalf("first line: %v (stderr %q)", err, stderr.String())
			}
			addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
			first, err := net.DialTimeout("tcp", addr, 10*time.Second)
			switch {
			case err == nil:
				echoClients(t, addr, first)
			case target == "x86_64-linux":
				t.Logf("dial %s from Windows: %v; running echoclient.mk inside WSL (21-testing 4.5)", addr, err)
				echoClientInWSL(t, r, port)
			default:
				t.Fatalf("dial %s: %v", addr, err)
			}
			rest, _ := io.ReadAll(br)
			err = srv.Wait()
			waited = true
			if err != nil || len(rest) != 0 || stderr.Len() != 0 {
				t.Fatalf("server: %v, more stdout %q, stderr %q; want exit 0 and nothing more", err, rest, stderr.String())
			}
		})
	}
}

// readListening reads the server's first line, "listening <port>\n", with a
// bound, and returns the port.
func readListening(br *bufio.Reader) (int, error) {
	type line struct {
		s   string
		err error
	}
	ch := make(chan line, 1)
	go func() {
		s, err := br.ReadString('\n')
		ch <- line{s, err}
	}()
	select {
	case l := <-ch:
		if l.err != nil {
			return 0, fmt.Errorf("%q: %v", l.s, l.err)
		}
		p, ok := strings.CutPrefix(l.s, "listening ")
		port, err := strconv.Atoi(strings.TrimSuffix(p, "\n"))
		if !ok || err != nil || port <= 0 || port > 65535 {
			return 0, fmt.Errorf("%q, want \"listening <port>\\n\"", l.s)
		}
		return port, nil
	case <-time.After(60 * time.Second):
		return 0, fmt.Errorf("no line within 60 s")
	}
}

// echoClients runs echoConns concurrent clients against addr; first is the
// first client's connection, already dialed.
func echoClients(t *testing.T, addr string, first net.Conn) {
	t.Helper()
	var wg sync.WaitGroup
	errs := make([]error, echoConns)
	for k := range echoConns {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c := first
			if k > 0 {
				var err error
				if c, err = net.DialTimeout("tcp", addr, 30*time.Second); err != nil {
					errs[k] = err
					return
				}
			}
			errs[k] = echoOne(c.(*net.TCPConn), k)
		}()
	}
	wg.Wait()
	bad := 0
	for k, err := range errs {
		if err != nil {
			bad++
			if bad <= 5 {
				t.Errorf("client %d: %v", k, err)
			}
		}
	}
	if bad > 0 {
		t.Fatalf("%d of %d clients failed", bad, echoConns)
	}
}

// echoOne sends echoBytes from client k's seeded stream in writes of 1 to
// 4096 bytes, closes the write half, and compares the bytes read back.
func echoOne(c *net.TCPConn, k int) error {
	defer c.Close()
	if err := c.SetDeadline(time.Now().Add(120 * time.Second)); err != nil {
		return err
	}
	rng := rand.New(rand.NewPCG(echoSeed, uint64(k)))
	sent := make([]byte, echoBytes)
	for i := range sent {
		sent[i] = byte(rng.Uint32())
	}
	got := make(chan []byte, 1)
	readErr := make(chan error, 1)
	go func() {
		b, err := io.ReadAll(c)
		got <- b
		readErr <- err
	}()
	for off := 0; off < len(sent); {
		n := min(1+rng.IntN(4096), len(sent)-off)
		if _, err := c.Write(sent[off : off+n]); err != nil {
			return fmt.Errorf("write at %d: %v", off, err)
		}
		off += n
	}
	if err := c.CloseWrite(); err != nil {
		return err
	}
	back := <-got
	if err := <-readErr; err != nil {
		return fmt.Errorf("read after %d bytes: %v", len(back), err)
	}
	if !bytes.Equal(back, sent) {
		i := 0
		for i < len(back) && i < len(sent) && back[i] == sent[i] {
			i++
		}
		return fmt.Errorf("echoed %d bytes, sent %d; first difference at byte %d", len(back), len(sent), i)
	}
	return nil
}

// echoClientInWSL builds echoclient.mk for x86_64-linux and runs it inside
// WSL against the server's port: it must print "ok <connections>".
func echoClientInWSL(t *testing.T, r *Runner, port int) {
	t.Helper()
	exe, err := r.Build(ProgramsDir+"/echoclient.mk", "x86_64-linux", "dev")
	if err != nil {
		t.Fatal(err)
	}
	argv, err := runArgv("x86_64-linux", filepath.Dir(exe), exe, []string{strconv.Itoa(port), strconv.Itoa(echoConns), strconv.Itoa(echoBytes)}, 110*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.WaitDelay = 5 * time.Second
	var o, e bytes.Buffer
	cmd.Stdout, cmd.Stderr = &o, &e
	err = cmd.Run()
	if want := fmt.Sprintf("ok %d\n", echoConns); err != nil || o.String() != want || e.Len() != 0 {
		t.Fatalf("echoclient: %v, stdout %q, stderr %q; want %q", err, o.String(), e.String(), want)
	}
}
