// Why: a server's memory per open connection and its throughput are only seen
// under many concurrent connections.
// What: TestHttp_Load (the notes service, --release) and TestHttp_SocketWatch.
// How: a Go load generator holds n keep-alive loopback connections and records private
// bytes per connection and requests per second, every response checked.
//
// It runs only when MK_HTTP_LOAD names the connection count (for example
// MK_HTTP_LOAD=4800), alone on the machine and in the background: the
// machine is capped at 10,000 sockets, so it refuses a count whose 2n + 1
// sockets, with those already open, would pass 9,900. Its numbers are a
// measurement on a shared machine, not a published benchmark (those come
// from tools/bench).

package std

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// htSocketCap leaves 100 of the machine's 10,000-socket cap for the rest of the
// machine.
const htSocketCap = 9900

const htHelloReq = "GET /hello HTTP/1.1\r\nHost: load\r\n\r\n"
const htHelloBody = "hello, world\n"

// htOpenSockets counts the machine's TCP sockets (netstat -an) that a
// process holds, and apart from them those in TIME_WAIT, which no process
// holds (a closed connection's kernel record), or -1.
func htOpenSockets() (held, timeWait int) {
	out, err := exec.Command("netstat", "-an", "-p", "tcp").Output()
	if err != nil {
		return -1, -1
	}
	for _, l := range strings.Split(string(out), "\n") {
		f := strings.Fields(l)
		if len(f) < 4 || !strings.EqualFold(f[0], "tcp") {
			continue
		}
		if f[3] == "TIME_WAIT" {
			timeWait++
		} else {
			held++
		}
	}
	return held, timeWait
}

// htSocketWatch samples the held sockets every period on a goroutine and
// calls over once, with the count, when it passes limit; the returned stop
// ends the goroutine and waits for it.
func htSocketWatch(period time.Duration, limit int, sample func() int, over func(held int)) (stop func()) {
	done := make(chan struct{})
	ended := make(chan struct{})
	go func() {
		defer close(ended)
		tick := time.NewTicker(period)
		defer tick.Stop()
		for {
			select {
			case <-done:
				return
			case <-tick.C:
			}
			if h := sample(); h > limit {
				over(h)
				return
			}
		}
	}()
	return func() {
		close(done)
		<-ended
	}
}

// TestHttp_SocketWatch checks the watch the load test
// runs: a count that passes the cap mid-run (another process opening 500
// sockets) stops the run once, and a count under it never does.
func TestHttp_SocketWatch(t *testing.T) {
	var mu sync.Mutex
	held := 9601
	sample := func() int {
		mu.Lock()
		defer mu.Unlock()
		return held
	}
	fired := make(chan int, 2)
	stop := htSocketWatch(time.Millisecond, htSocketCap, sample, func(h int) { fired <- h })
	time.Sleep(20 * time.Millisecond)
	select {
	case h := <-fired:
		t.Fatalf("fired at %d, under the cap", h)
	default:
	}
	mu.Lock()
	held += 500
	mu.Unlock()
	select {
	case h := <-fired:
		if h != 10101 {
			t.Errorf("fired at %d, want 10101", h)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the watch did not fire over the cap")
	}
	stop()
	if len(fired) != 0 {
		t.Error("the watch fired twice")
	}
}

// htPrivateBytes is the process's private (committed) bytes on Windows and
// its resident bytes on Linux, with the name of the measure.
func htPrivateBytes(pid int) (int64, string, error) {
	if runtime.GOOS == "windows" {
		out, err := exec.Command("pwsh", "-NoProfile", "-Command",
			fmt.Sprintf("(Get-Process -Id %d).PrivateMemorySize64", pid)).Output()
		if err != nil {
			return 0, "", err
		}
		v, err := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
		return v, "private bytes", err
	}
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil {
		return 0, "", err
	}
	for _, l := range strings.Split(string(b), "\n") {
		if f := strings.Fields(l); len(f) >= 2 && f[0] == "VmRSS:" {
			v, err := strconv.ParseInt(f[1], 10, 64)
			return v * 1024, "resident bytes", err
		}
	}
	return 0, "", errors.New("no VmRSS")
}

// htHelloOnce sends one /hello request on c and checks the reply.
func htHelloOnce(c net.Conn, r *bufio.Reader) error {
	if _, err := io.WriteString(c, htHelloReq); err != nil {
		return err
	}
	line, err := r.ReadString('\n')
	if err != nil {
		return err
	}
	if line != "HTTP/1.1 200 OK\r\n" {
		return fmt.Errorf("status line %q", line)
	}
	length := -1
	for {
		h, err := r.ReadString('\n')
		if err != nil {
			return err
		}
		if h == "\r\n" {
			break
		}
		name, value, ok := strings.Cut(strings.TrimSuffix(h, "\r\n"), ":")
		if !ok {
			return fmt.Errorf("field %q", h)
		}
		switch strings.ToLower(name) {
		case "content-length":
			length, err = strconv.Atoi(strings.TrimSpace(value))
			if err != nil {
				return fmt.Errorf("Content-Length %q", value)
			}
		case "connection":
			return fmt.Errorf("the server sent Connection: %s", value)
		}
	}
	if length != len(htHelloBody) {
		return fmt.Errorf("Content-Length %d", length)
	}
	var body [len(htHelloBody)]byte
	if _, err := io.ReadFull(r, body[:]); err != nil {
		return err
	}
	if string(body[:]) != htHelloBody {
		return fmt.Errorf("body %q", body[:])
	}
	return nil
}

func TestHttp_Load(t *testing.T) {
	spec := os.Getenv("MK_HTTP_LOAD")
	if spec == "" {
		t.Skip("MK_HTTP_LOAD is not set: the load test runs only when asked, alone on the machine")
	}
	n, err := strconv.Atoi(spec)
	if err != nil || n < 1 {
		t.Fatalf("MK_HTTP_LOAD=%q: want a connection count", spec)
	}
	open, tw := htOpenSockets()
	if open < 0 {
		t.Fatal("netstat failed: the socket count cannot be checked, so the test does not start")
	}
	if open+2*n+1 > htSocketCap {
		t.Fatalf("%d connections need %d sockets and %d are held: over %d", n, 2*n+1, open, htSocketCap)
	}
	warm, measure := 3*time.Second, 10*time.Second
	if s := os.Getenv("MK_HTTP_LOAD_SECONDS"); s != "" {
		v, err := strconv.Atoi(s)
		if err != nil || v < 1 {
			t.Fatalf("MK_HTTP_LOAD_SECONDS=%q", s)
		}
		measure = time.Duration(v) * time.Second
	}
	src, err := os.ReadFile(filepath.Join("..", "tests", "bench-lanes", "std-http", "notes", "main.mk"))
	if err != nil {
		t.Fatal(err)
	}
	p := htStartServerProc(t, t.TempDir(), string(src), "release")
	pid := p.cmd.Process.Pid

	// The base: the server after it has served one connection, closed.
	c0, err := net.Dial("tcp", p.addr)
	if err != nil {
		t.Fatal(err)
	}
	if err := htHelloOnce(c0, bufio.NewReader(c0)); err != nil {
		t.Fatal(err)
	}
	c0.Close()
	time.Sleep(200 * time.Millisecond)
	base, measureName, err := htPrivateBytes(pid)
	if err != nil {
		t.Fatalf("server memory: %v", err)
	}

	// Ramp: dial in batches of 200, retrying a refused dial (the listen
	// backlog is the OS's), one checked request on each connection, so
	// every connection task has read and answered once.
	conns := make([]net.Conn, n)
	readers := make([]*bufio.Reader, n)
	defer func() {
		for _, c := range conns {
			if c != nil {
				c.(*net.TCPConn).SetLinger(0)
				c.Close()
			}
		}
	}()
	// The cap is machine-wide and other processes open sockets
	// while this test holds its 2n + 1, so the held count is sampled every
	// second for as long as the connections live, not once before the
	// ramp. Over the cap every connection is reset at once (linger 0) and
	// the test fails.
	var stop atomic.Bool
	var liveMu sync.Mutex
	var live []net.Conn
	var overAt atomic.Int64
	track := func(c net.Conn) bool {
		liveMu.Lock()
		defer liveMu.Unlock()
		if overAt.Load() != 0 {
			c.(*net.TCPConn).SetLinger(0)
			c.Close()
			return false
		}
		live = append(live, c)
		return true
	}
	watch := htSocketWatch(time.Second, htSocketCap, func() int {
		h, _ := htOpenSockets()
		return h
	}, func(held int) {
		overAt.Store(int64(held))
		stop.Store(true)
		liveMu.Lock()
		for _, c := range live {
			c.(*net.TCPConn).SetLinger(0)
			c.Close()
		}
		liveMu.Unlock()
	})
	defer watch()
	overCap := func() {
		if h := overAt.Load(); h != 0 {
			t.Fatalf("the machine held %d TCP sockets, over the cap of %d: every connection was reset", h, htSocketCap)
		}
	}
	ramp := time.Now()
	retries := int64(0)
	for lo := 0; lo < n; lo += 200 {
		hi := min(lo+200, n)
		var wg sync.WaitGroup
		errs := make(chan error, hi-lo)
		for i := lo; i < hi; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				var c net.Conn
				var err error
				for try := 0; try < 100; try++ {
					if overAt.Load() != 0 {
						errs <- fmt.Errorf("dial %d: over the socket cap", i)
						return
					}
					c, err = net.DialTimeout("tcp", p.addr, 5*time.Second)
					if err == nil {
						break
					}
					atomic.AddInt64(&retries, 1)
					time.Sleep(20 * time.Millisecond)
				}
				if err != nil {
					errs <- fmt.Errorf("dial %d: %v", i, err)
					return
				}
				if !track(c) {
					errs <- fmt.Errorf("dial %d: over the socket cap", i)
					return
				}
				conns[i] = c
				readers[i] = bufio.NewReaderSize(c, 512)
				c.SetDeadline(time.Now().Add(30 * time.Second))
				if err := htHelloOnce(c, readers[i]); err != nil {
					errs <- fmt.Errorf("connection %d: %v", i, err)
				}
			}(i)
		}
		wg.Wait()
		close(errs)
		overCap()
		for err := range errs {
			t.Fatalf("ramp at %d connections: %v", lo, err)
		}
	}
	rampTime := time.Since(ramp)
	time.Sleep(500 * time.Millisecond)
	held, _, err := htPrivateBytes(pid)
	if err != nil {
		t.Fatalf("server memory: %v", err)
	}
	t.Logf("ramp: %d connections in %v (%d dial retries); TCP sockets on the machine before: %d held, %d in TIME_WAIT", n, rampTime.Round(time.Millisecond), retries, open, tw)

	// Load: every connection sends a request as soon as its reply is in.
	var measuring atomic.Bool
	var done atomic.Int64
	var firstErr atomic.Pointer[error]
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(c net.Conn, r *bufio.Reader) {
			defer wg.Done()
			for !stop.Load() {
				c.SetDeadline(time.Now().Add(30 * time.Second))
				if err := htHelloOnce(c, r); err != nil {
					firstErr.CompareAndSwap(nil, &err)
					stop.Store(true)
					return
				}
				if measuring.Load() {
					done.Add(1)
				}
			}
		}(conns[i], readers[i])
	}
	time.Sleep(warm)
	measuring.Store(true)
	start := time.Now()
	time.Sleep(measure)
	measuring.Store(false)
	elapsed := time.Since(start)
	count := done.Load()
	during, _, _ := htPrivateBytes(pid)
	stop.Store(true)
	wg.Wait()
	overCap()
	if e := firstErr.Load(); e != nil {
		t.Fatalf("under load: %v", *e)
	}

	// Close every connection and check the server still answers.
	for i, c := range conns {
		c.(*net.TCPConn).SetLinger(0)
		c.Close()
		conns[i] = nil
	}
	time.Sleep(time.Second)
	after, _, _ := htPrivateBytes(pid)
	c1, err := net.Dial("tcp", p.addr)
	if err != nil {
		t.Fatalf("after the load: %v", err)
	}
	if err := htHelloOnce(c1, bufio.NewReader(c1)); err != nil {
		t.Fatalf("after the load: %v", err)
	}
	c1.Close()
	select {
	case <-p.exited:
		t.Fatalf("the server exited: %s", p.stderr.String())
	default:
	}

	rps := float64(count) / elapsed.Seconds()
	per := float64(held-base) / float64(n)
	var b bytes.Buffer
	fmt.Fprintf(&b, "LOAD %s/%s, %d CPUs: %d keep-alive connections (%d sockets with the server's), /hello of the notes service (release)\n", runtime.GOOS, runtime.GOARCH, runtime.NumCPU(), n, 2*n+1)
	fmt.Fprintf(&b, "  requests: %d in %v = %.0f requests per second (after %v of warm-up), every reply checked\n", count, elapsed.Round(time.Millisecond), rps, warm)
	fmt.Fprintf(&b, "  server %s: %d before, %d with every connection open and idle, %d under load, %d after all closed\n", measureName, base, held, during, after)
	fmt.Fprintf(&b, "  per connection: %.0f bytes (open and idle, minus before, over %d)\n", per, n)
	t.Log(b.String())
	fmt.Print(b.String())
}
