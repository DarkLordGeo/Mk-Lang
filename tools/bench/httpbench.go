// Why: a server's speed cannot be timed by hyperfine; the idiomatic table's
// http row (hello world under a load generator at 1,000 connections, below
// the 10,000-socket cap) needs its own load generator and its own section
// of RESULTS.md.
//
// What: httpTable, which builds each language's std HTTP server and runs
// generate against it; the session interface the server rows share
// (helloSession here, others in serverbench.go), checkLoad, and renderHTTP.
//
// How: the server runs on CPUs 0-7 and the generator on CPUs 8-15, with
// keep-alive, 5 s of warm-up and 30 s measured, reporting req/s, p50 and
// p99. A raw HTTP/1.1 client keeps one request outstanding per connection.
//
//	tools/bench http             build the servers, a short check of every response, provisional numbers
//	tools/bench http -publish    the table, under the machine lock

package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// httpSpec is tests/bench/idiomatic/http/http.json.
type httpSpec struct {
	Name        string            `json:"name"`
	Task        string            `json:"task"`
	Body        string            `json:"body"` // every response's body
	Sources     map[string]string `json:"sources"`
	Skip        map[string]string `json:"skip"`
	Models      map[string]string `json:"models"` // per column
	Connections int               `json:"connections"`
	WarmupS     int               `json:"warmup_s"`
	MeasureS    int               `json:"measure_s"`
	ServerMask  string            `json:"server_mask"`
	ClientMask  string            `json:"client_mask"`
	Threads     int               `json:"threads"` // GOMAXPROCS of the Go server and of the generator
	Sweeps      int               `json:"sweeps"`
	Notes       []string          `json:"notes"`

	Dir string `json:"-"`
}

// socketCap is the benchmark machine's rule: never more than 10,000 sockets
// at once. The generator and the server each hold one per connection.
const socketCap = 10000

func loadHTTPSpec(root string) (*httpSpec, error) {
	dir := filepath.Join(root, "tests", "bench", "idiomatic", "http")
	b, err := os.ReadFile(filepath.Join(dir, "http.json"))
	if err != nil {
		return nil, err
	}
	var s httpSpec
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, fmt.Errorf("http.json: %w", err)
	}
	s.Dir = dir
	if 2*s.Connections+64 > socketCap || s.Connections < 1 {
		return nil, fmt.Errorf("http.json: %d connections: the generator and the server hold two sockets each, and the cap is %d", s.Connections, socketCap)
	}
	if s.Body == "" || s.MeasureS < 1 || s.Threads < 1 || s.Sweeps < 1 {
		return nil, errors.New("http.json: body, measure_s, threads and sweeps are required")
	}
	for _, m := range []string{s.ServerMask, s.ClientMask} {
		if _, err := strconv.ParseUint(strings.TrimPrefix(m, "0x"), 16, 64); err != nil || m == "" {
			return nil, fmt.Errorf("http.json: bad mask %q", m)
		}
	}
	return &s, nil
}

// row is the spec as a row, so the servers build with the columns' commands.
func (s *httpSpec) row() *row {
	return &row{Name: s.Name, Table: "idiomatic", Dir: s.Dir, Sources: s.Sources, Skip: s.Skip}
}

// loadResult is one sweep of one server.
type loadResult struct {
	Requests int64   `json:"requests"`
	Seconds  float64 `json:"seconds"`
	RPS      float64 `json:"rps"`
	P50      float64 `json:"p50"` // seconds
	P99      float64 `json:"p99"`
	Errors   int64   `json:"errors"`
}

// loadOptions is one run of the generator.
type loadOptions struct {
	Addr            string
	Body            []byte
	Connections     int
	Warmup, Measure time.Duration
	// Session is connection i's conversation: the kv
	// generator and httpapi's route mix. nil is the hello row's, every
	// request GET / and every response Body.
	Session func(i int) session
}

// session is one connection's side of a load run: the request to write
// next, and the check of its reply, which reads exactly that reply and
// advances the session's model of the server. Each
// connection owns its own keys or items, so every reply is predictable
// from its own requests alone.
type session interface {
	request() []byte
	check(br *bufio.Reader) error
}

// helloSession is the idiomatic http row's: GET / and the one body.
type helloSession struct{ body []byte }

func (h helloSession) request() []byte              { return httpRequest }
func (h helloSession) check(br *bufio.Reader) error { return readResponse(br, h.body) }

// sessions is o's sessions, one per connection.
func (o loadOptions) sessions() []session {
	out := make([]session, o.Connections)
	for i := range out {
		if o.Session == nil {
			out[i] = helloSession{o.Body}
		} else {
			out[i] = o.Session(i)
		}
	}
	return out
}

// request is the generator's one request: keep-alive is HTTP/1.1's default.
var httpRequest = []byte("GET / HTTP/1.1\r\nHost: 127.0.0.1\r\nUser-Agent: mk-bench\r\n\r\n")

// generate opens o.Connections keep-alive connections and keeps one
// request outstanding on each until the end, counting and timing the
// requests that complete in the measured window. A raw HTTP/1.1 client on
// net.Conn (Go's std, one goroutine per connection) rather than
// net/http.Client, so the generator spends little per request; every
// response's status and body are checked. Connections close with a reset
// (linger 0), so no TIME_WAIT sockets pile up between sweeps.
func generate(o loadOptions) (loadResult, error) {
	var phase atomic.Int32 // 0 warm-up, 1 measured, 2 stop
	var errs atomic.Int64
	var firstErr atomic.Value
	lats := make([][]int64, o.Connections)
	var wg sync.WaitGroup
	conns, err := dialAll(o.Addr, o.Connections)
	if err != nil {
		return loadResult{}, err
	}
	ss := o.sessions()
	for i, c := range conns {
		wg.Add(1)
		go func(i int, c net.Conn) {
			defer wg.Done()
			br := bufio.NewReaderSize(c, 4096)
			buf := make([]int64, 0, 1<<16)
			s := ss[i]
			for phase.Load() < 2 {
				// The request is built before the clock starts, so a
				// sample spans its write, the response's read and the
				// session's check of it.
				req := s.request()
				t0 := time.Now()
				if _, err := c.Write(req); err != nil {
					fail(&errs, &firstErr, connErr(i, err))
					return
				}
				if err := s.check(br); err != nil {
					fail(&errs, &firstErr, connErr(i, err))
					return
				}
				if phase.Load() == 1 {
					buf = append(buf, int64(time.Since(t0)))
				}
			}
			lats[i] = buf
		}(i, c)
	}
	time.Sleep(o.Warmup)
	phase.Store(1)
	start := time.Now()
	time.Sleep(o.Measure)
	phase.Store(2)
	secs := time.Since(start).Seconds()
	// A goroutine stops only between requests, so a server that
	// stops answering one connection would block it in readResponse, and
	// wg.Wait, forever (the server is killed only after generate returns,
	// and a publish holds the machine lock meanwhile). Every connection
	// gets httpGrace to finish its last request; a read or write still
	// blocked then fails with the deadline and names its connection.
	end := time.Now().Add(httpGrace)
	for _, c := range conns {
		c.SetDeadline(end)
	}
	wg.Wait()
	for _, c := range conns {
		c.Close()
	}
	var all []int64
	for _, l := range lats {
		all = append(all, l...)
	}
	r := loadResult{Requests: int64(len(all)), Seconds: secs, Errors: errs.Load()}
	if e := firstErr.Load(); e != nil {
		return r, fmt.Errorf("%d connections failed, the first: %v", r.Errors, e)
	}
	if len(all) == 0 {
		return r, errors.New("no request completed in the measured window")
	}
	slices.Sort(all)
	r.RPS = float64(len(all)) / secs
	r.P50 = float64(all[len(all)/2]) / 1e9
	r.P99 = float64(all[len(all)*99/100]) / 1e9
	return r, nil
}

// dialAll opens n connections to addr, each retried for a second, with
// linger 0 (a reset at close, so no TIME_WAIT sockets pile up) and no
// Nagle delay.
func dialAll(addr string, n int) ([]net.Conn, error) {
	conns := make([]net.Conn, n)
	for i := range conns {
		var c net.Conn
		var err error
		for try := 0; try < 50; try++ {
			if c, err = net.DialTimeout("tcp", addr, 5*time.Second); err == nil {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		if err != nil {
			for _, c := range conns[:i] {
				c.Close()
			}
			return nil, fmt.Errorf("connection %d: %w", i, err)
		}
		if tc, ok := c.(*net.TCPConn); ok {
			tc.SetLinger(0)
			tc.SetNoDelay(true)
		}
		conns[i] = c
	}
	return conns, nil
}

// checkLoad is the functional check of a server, which times
// nothing: n requests on each of the connections, one outstanding on each,
// every reply checked by its session, all within limit. It returns how
// many requests were answered and checked. No rate or latency is read, so
// it is a check, never a measurement.
func checkLoad(addr string, ss []session, n int, limit time.Duration) (int64, error) {
	conns, err := dialAll(addr, len(ss))
	if err != nil {
		return 0, err
	}
	end := time.Now().Add(limit)
	var done, errs atomic.Int64
	var firstErr atomic.Value
	var wg sync.WaitGroup
	for i, c := range conns {
		c.SetDeadline(end)
		wg.Add(1)
		go func(i int, c net.Conn) {
			defer wg.Done()
			br := bufio.NewReaderSize(c, 4096)
			for k := 0; k < n; k++ {
				if _, err := c.Write(ss[i].request()); err != nil {
					fail(&errs, &firstErr, fmt.Errorf("connection %d, request %d: %w", i, k, err))
					return
				}
				if err := ss[i].check(br); err != nil {
					fail(&errs, &firstErr, fmt.Errorf("connection %d, request %d: %w", i, k, err))
					return
				}
				done.Add(1)
			}
		}(i, c)
	}
	wg.Wait()
	for _, c := range conns {
		c.Close()
	}
	if e := firstErr.Load(); e != nil {
		return done.Load(), fmt.Errorf("%d connections failed, the first: %v", errs.Load(), e)
	}
	return done.Load(), nil
}

// httpGrace is how long a connection may take, after the measured window,
// to finish the request it has outstanding. 5 s is far beyond any p99 the
// http row has recorded, so a connection still waiting after it has a
// server that stopped answering it.
const httpGrace = 5 * time.Second

// connErr names the connection an error happened on, and says so plainly
// when it is the httpGrace deadline.
func connErr(i int, err error) error {
	if errors.Is(err, os.ErrDeadlineExceeded) {
		return fmt.Errorf("connection %d: no response within %s after the measured window, the server stopped answering it: %w", i, httpGrace, err)
	}
	return fmt.Errorf("connection %d: %w", i, err)
}

func fail(n *atomic.Int64, first *atomic.Value, err error) {
	n.Add(1)
	first.CompareAndSwap(nil, err)
}

// readResponse reads one HTTP/1.1 response: status 200, a Content-Length
// header, and exactly the expected body.
func readResponse(br *bufio.Reader, body []byte) error {
	line, err := br.ReadSlice('\n')
	if err != nil {
		return err
	}
	if !bytes.HasPrefix(line, []byte("HTTP/1.1 200")) {
		return fmt.Errorf("status line %q", bytes.TrimSpace(line))
	}
	n := -1
	for {
		line, err = br.ReadSlice('\n')
		if err != nil {
			return err
		}
		l := bytes.TrimSpace(line)
		if len(l) == 0 {
			break
		}
		if k, v, ok := bytes.Cut(l, []byte(":")); ok && strings.EqualFold(string(k), "content-length") {
			if n, err = strconv.Atoi(string(bytes.TrimSpace(v))); err != nil {
				return fmt.Errorf("content-length %q", v)
			}
		}
		if k, _, ok := bytes.Cut(l, []byte(":")); ok && strings.EqualFold(string(k), "connection") && bytes.Contains(bytes.ToLower(l), []byte("close")) {
			return errors.New("the server closes the connection (no keep-alive)")
		}
	}
	if n != len(body) {
		return fmt.Errorf("content-length %d, want %d", n, len(body))
	}
	got := make([]byte, n)
	if _, err := ioReadFull(br, got); err != nil {
		return err
	}
	if !bytes.Equal(got, body) {
		return fmt.Errorf("body %q, want %q", got, body)
	}
	return nil
}

func ioReadFull(br *bufio.Reader, b []byte) (int, error) {
	n := 0
	for n < len(b) {
		m, err := br.Read(b[n:])
		n += m
		if err != nil {
			return n, err
		}
	}
	return n, nil
}

// freePort asks the system for a free TCP port on 127.0.0.1.
func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

// startServer starts exe on a free port and waits until it accepts.
func startServer(exe string, env []string) (*exec.Cmd, string, error) {
	return startServerArgs(exe, func(port int) []string { return []string{strconv.Itoa(port)} }, env)
}

// startServerArgs starts exe with the arguments args gives for a free port
// and waits until it accepts on 127.0.0.1 at that port.
func startServerArgs(exe string, args func(port int) []string, env []string) (*exec.Cmd, string, error) {
	port, err := freePort()
	if err != nil {
		return nil, "", err
	}
	cmd := exec.Command(exe, args(port)...)
	cmd.Env = env
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return nil, "", err
	}
	addr := "127.0.0.1:" + strconv.Itoa(port)
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if c, err := net.DialTimeout("tcp", addr, time.Second); err == nil {
			c.Close()
			return cmd, addr, nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	cmd.Process.Kill()
	cmd.Wait()
	return nil, "", fmt.Errorf("%s did not accept on %s within 15 s: %s", filepath.Base(exe), addr, firstLines(stderr.String(), 6))
}

// httpCell is one server column.
type httpCell struct {
	Col      string       `json:"column"`
	Model    string       `json:"model"`
	Sweeps   []loadResult `json:"sweeps"`
	RPS      float64      `json:"rps"` // median of the sweeps
	P50      float64      `json:"p50"` // median of the sweeps' p50
	P99      float64      `json:"p99"` // median of the sweeps' p99
	Build    buildCmd     `json:"build"`
	Excluded string       `json:"excluded,omitempty"`
}

type httpResults struct {
	Run facts `json:"run"`
	// ClockStep is the step of Go's clock on this machine (clockStep): the
	// resolution of every latency.
	ClockStep float64    `json:"clock_step"`
	Spec      httpSpec   `json:"spec"`
	Cells     []httpCell `json:"cells"`
}

type httpOptions struct {
	Root, Work, Mk string
	Publish        bool
	Full           bool // without Publish: the spec's connections, a 5 s window
	LockPath       string
	Date           string
	Log            func(string)
}

// httpTable builds every server column and runs the generator against
// each, Sweeps times in rotated order when publishing (one short run
// otherwise), each sweep a fresh server process and fresh connections.
func httpTable(o httpOptions, w func(string)) error {
	if o.Publish {
		if err := refuseExperiment(o.Mk, ""); err != nil {
			return err
		}
	}
	if runtime.GOOS != "windows" {
		return errors.New("the http row runs on Windows")
	}
	spec, err := loadHTTPSpec(o.Root)
	if err != nil {
		return err
	}
	if o.Publish {
		lock, err := acquireLock(o.LockPath, o.Log)
		if err != nil {
			return err
		}
		defer lock.Release()
		o.Log("holding the machine lock " + lock.Path())
	}
	t, err := findToolchain(o.Work, o.Mk)
	if err != nil {
		return err
	}
	r := spec.row()
	bs := buildAll(t, "http", []*row{r}, nil, o.Log)
	lo := loadOptions{Body: []byte(spec.Body), Connections: spec.Connections,
		Warmup: time.Duration(spec.WarmupS) * time.Second, Measure: time.Duration(spec.MeasureS) * time.Second}
	sweeps := spec.Sweeps
	if !o.Publish {
		lo.Connections, lo.Warmup, lo.Measure, sweeps = min(50, spec.Connections), time.Second, 2*time.Second, 1
		if o.Full {
			lo.Connections, lo.Warmup, lo.Measure = spec.Connections, 2*time.Second, 5*time.Second
		}
	}
	res := httpResults{Spec: *spec, ClockStep: clockStep()}
	serverMask, _ := strconv.ParseUint(strings.TrimPrefix(spec.ServerMask, "0x"), 16, 64)
	clientMask, _ := strconv.ParseUint(strings.TrimPrefix(spec.ClientMask, "0x"), 16, 64)
	if o.Publish {
		res.Run = gatherFacts(o.Root, t)
		res.Run.Date = time.Now().Format("2006-01-02 15:04")
		res.Run.MkBinary, res.Run.MkSHA256, res.Run.MkStd = o.Mk, fileSHA(o.Mk), t.MkStd
		res.Run.Affinity = fmt.Sprintf("server %s, generator %s (set on tools/bench before each start; the server inherits it)", spec.ServerMask, spec.ClientMask)
		defer setAffinity(^uint64(0) >> (64 - runtime.NumCPU()))
	}
	env := runEnv(os.Environ())
	env = append(env, "GOMAXPROCS="+strconv.Itoa(spec.Threads), "MK_CARRIERS="+strconv.Itoa(spec.Threads))
	cells := map[string]*httpCell{}
	var order []string
	for _, b := range bs {
		c := &httpCell{Col: b.Col.ID, Model: spec.Models[b.Col.Lang], Build: b.Cmd}
		if c.Model == "" {
			c.Model = spec.Models[b.Col.ID]
		}
		if b.Err != nil {
			c.Excluded = firstLines(b.Err.Error(), 6)
		}
		cells[b.Col.ID], order = c, append(order, b.Col.ID)
	}
	exe := map[string]string{}
	for _, b := range bs {
		exe[b.Col.ID] = b.Exe
	}
	for s := 0; s < sweeps; s++ {
		for k := range order {
			id := order[(k+s)%len(order)]
			c := cells[id]
			if c.Excluded != "" {
				continue
			}
			if o.Publish {
				if err := setAffinity(serverMask); err != nil {
					return err
				}
			}
			cmd, addr, err := startServer(exe[id], env)
			if err == nil && o.Publish {
				err = setAffinity(clientMask)
			}
			var lr loadResult
			if err == nil {
				prev := runtime.GOMAXPROCS(spec.Threads)
				lo.Addr = addr
				lr, err = generate(lo)
				runtime.GOMAXPROCS(prev)
			}
			if cmd != nil {
				cmd.Process.Kill()
				cmd.Wait()
			}
			if err != nil {
				c.Excluded = firstLines(err.Error(), 6)
				w(fmt.Sprintf("FAIL http %s: %s", id, c.Excluded))
				continue
			}
			c.Sweeps = append(c.Sweeps, lr)
			o.Log(fmt.Sprintf("http %s sweep %d: %.0f req/s, p50 %s, p99 %s", id, s+1, lr.RPS, fmtSec(lr.P50), fmtSec(lr.P99)))
		}
	}
	failed := 0
	tag := "PROVISIONAL (no machine lock, unpinned, short)"
	if o.Publish {
		tag = "published"
	}
	for _, id := range order {
		c := cells[id]
		if c.Excluded != "" {
			failed++
			c.Sweeps = nil
		} else {
			var rps, p50, p99 []float64
			for _, s := range c.Sweeps {
				rps, p50, p99 = append(rps, s.RPS), append(p50, s.P50), append(p99, s.P99)
			}
			c.RPS, c.P50, c.P99 = median(rps), median(p50), median(p99)
			w(fmt.Sprintf("ok http %s (%s): %.0f req/s, p50 %s, p99 %s, %s", id, c.Model, c.RPS, fmtSec(c.P50), fmtSec(c.P99), tag))
		}
		res.Cells = append(res.Cells, *c)
	}
	if o.Publish {
		dir := filepath.Join(o.Root, "tests", "bench", "results", o.Date, "http")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		b, err := json.MarshalIndent(res, "", "  ")
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, "results.json"), append(b, '\n'), 0o644); err != nil {
			return err
		}
		if _, err := os.Stat(filepath.Join(o.Root, "tests", "bench", "results", o.Date, "results.json")); err == nil {
			if err := writeResultsMD(o.Root, o.Date); err != nil {
				return err
			}
		}
	}
	if failed > 0 {
		return fmt.Errorf("%d http cells failed", failed)
	}
	return nil
}

// clockStep is the median of ten successive nonzero steps of time.Now: on
// Windows Go's clock advances with the interrupt time, about every 0.5 ms
// on the benchmark machine, so a latency is known only to that step.
func clockStep() float64 {
	var ds []float64
	last := time.Now()
	for len(ds) < 10 {
		if n := time.Now(); n.After(last) {
			ds = append(ds, n.Sub(last).Seconds())
			last = n
		}
	}
	return median(ds)
}

func loadHTTPIfAny(root, date string) (*httpResults, error) {
	b, err := os.ReadFile(filepath.Join(root, "tests", "bench", "results", date, "http", "results.json"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var r httpResults
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, fmt.Errorf("http results of %s: %w", date, err)
	}
	return &r, nil
}

// renderHTTP is the http section of RESULTS.md.
func renderHTTP(res *httpResults, date string) string {
	var w strings.Builder
	s := res.Spec
	fmt.Fprintf(&w, "## Table \"idiomatic\", row http\n\nFrom `tests/bench/results/%s/http/results.json`. %s Each language's std HTTP server under tools/bench's load generator: %d keep-alive connections, one request outstanding on each, %d s of warm-up and %d s measured, %d sweeps in rotated order, each a fresh server; the server on affinity mask %s (CPUs %s), the generator on %s (CPUs %s), %d threads each (GOMAXPROCS, MK_CARRIERS). Every response's status and body are checked. req/s is the median of the sweeps, p50 and p99 the medians of the sweeps' latencies (request written to response read), timed by Go's clock, whose step on this machine was %s, so each latency is known to that step; the mean latency is exact by Little's law (connections / req/s).\n\n",
		date, s.Task, s.Connections, s.WarmupS, s.MeasureS, s.Sweeps, s.ServerMask, maskCPUs(s.ServerMask), s.ClientMask, maskCPUs(s.ClientMask), s.Threads, fmtSec(res.ClockStep))
	fmt.Fprintf(&w, "Machine: %s; %s; power scheme %s; affinity %s; measured %s.\n\n", orDash(res.Run.CPU), orDash(res.Run.OS), orDash(res.Run.Power), orDash(res.Run.Affinity), orDash(res.Run.Date))
	best := 0.0
	for _, c := range res.Cells {
		if c.Excluded == "" && c.RPS > best {
			best = c.RPS
		}
	}
	w.WriteString("| Column | Model | req/s (sweeps) | Mean latency | p50 | p99 | req/s / best |\n|---|---|---|---|---|---|---|\n")
	for _, c := range res.Cells {
		if c.Excluded != "" {
			fmt.Fprintf(&w, "| %s | %s | excluded: %s | | | | |\n", c.Col, cellText(c.Model), cellText(c.Excluded))
			continue
		}
		var sw []string
		for _, x := range c.Sweeps {
			sw = append(sw, strconv.FormatFloat(x.RPS, 'f', 0, 64))
		}
		fmt.Fprintf(&w, "| %s | %s | %.0f (%s) | %s | %s | %s | %.2f |\n", c.Col, cellText(c.Model), c.RPS, strings.Join(sw, ", "), fmtSec(float64(s.Connections)/c.RPS), fmtSec(c.P50), fmtSec(c.P99), c.RPS/best)
	}
	var langs []string
	for l := range s.Skip {
		langs = append(langs, l)
	}
	slices.Sort(langs)
	for _, l := range langs {
		fmt.Fprintf(&w, "| %s | | %s | | | | |\n", l, cellText(s.Skip[l]))
	}
	w.WriteString("\n")
	return w.String()
}

// maskCPUs lists the CPUs of a hex affinity mask, "0-7" or "0, 2, 4".
func maskCPUs(mask string) string {
	m, err := strconv.ParseUint(strings.TrimPrefix(mask, "0x"), 16, 64)
	if err != nil {
		return "?"
	}
	var cpus []int
	for i := 0; i < 64; i++ {
		if m&(1<<i) != 0 {
			cpus = append(cpus, i)
		}
	}
	if len(cpus) > 1 && cpus[len(cpus)-1]-cpus[0] == len(cpus)-1 {
		return fmt.Sprintf("%d-%d", cpus[0], cpus[len(cpus)-1])
	}
	var s []string
	for _, c := range cpus {
		s = append(s, strconv.Itoa(c))
	}
	return strings.Join(s, ", ")
}
