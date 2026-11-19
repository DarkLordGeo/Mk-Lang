// Why: the http row's numbers mean something only if the generator keeps
// every connection busy and catches a wrong or stalled server.
// What: tests of generate, loadHTTPSpec and renderHTTP against Go test
// servers, including a server that hangs.
// How: serveFor starts a net/http server on a free port for each case; the tests
// run short load windows and read the errors and counts.

package main

// Tests of the http row (httpbench.go).

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func serveFor(t *testing.T, h http.HandlerFunc) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: h}
	go srv.Serve(l)
	t.Cleanup(func() { srv.Close() })
	return l.Addr().String()
}

// TestHTTPGenerator: the generator keeps every connection busy, counts
// only the measured window, and fails a server whose body is wrong or that
// closes the connection after each response.
func TestHTTPGenerator(t *testing.T) {
	body := []byte("Hello, World!\n")
	good := serveFor(t, func(w http.ResponseWriter, r *http.Request) { w.Write(body) })
	r, err := generate(loadOptions{Addr: good, Body: body, Connections: 20, Warmup: 100 * time.Millisecond, Measure: 300 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if r.Requests < 20 || r.RPS <= 0 || r.P99 < r.P50 || r.Seconds < 0.3 {
		t.Fatalf("result %+v", r)
	}
	wrong := serveFor(t, func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("Hello, World?\n")) })
	if _, err := generate(loadOptions{Addr: wrong, Body: body, Connections: 2, Warmup: 0, Measure: 100 * time.Millisecond}); err == nil || !strings.Contains(err.Error(), "body") {
		t.Fatalf("a wrong body passed: %v", err)
	}
	closing := serveFor(t, func(w http.ResponseWriter, r *http.Request) { w.Header().Set("Connection", "close"); w.Write(body) })
	if _, err := generate(loadOptions{Addr: closing, Body: body, Connections: 2, Warmup: 0, Measure: 100 * time.Millisecond}); err == nil || !strings.Contains(err.Error(), "keep-alive") {
		t.Fatalf("a closing server passed: %v", err)
	}
}

// TestHTTPSpec: the tree's spec loads, its cells are Mk (std.http, a
// model stated), Zig and Go with C and Rust stated, and a spec over the
// machine's socket cap is refused.
func TestHTTPSpec(t *testing.T) {
	s, err := loadHTTPSpec(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, c := range rowColumns(s.row()) {
		ids = append(ids, c.ID)
	}
	if strings.Join(ids, " ") != "mk zig-fast zig-safe go" || s.Skip["c"] == "" || s.Skip["rust"] == "" || s.Skip["mk"] != "" || s.Models["mk"] == "" {
		t.Fatalf("columns %v skip %v", ids, s.Skip)
	}
	if s.Connections != 1000 || s.WarmupS != 5 || s.MeasureS != 30 || s.ServerMask != "0xff" || s.ClientMask != "0xff00" {
		t.Fatalf("B19's load is not the spec's: %+v", s)
	}
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "tests/bench/idiomatic/http/http.json"), `{"name":"http","body":"x","connections":6000,"measure_s":1,"threads":1,"sweeps":1,"server_mask":"0x1","client_mask":"0x2"}`)
	if _, err := loadHTTPSpec(root); err == nil || !strings.Contains(err.Error(), "cap") {
		t.Fatalf("6,000 connections accepted: %v", err)
	}
	if d := clockStep(); d <= 0 || d > 0.1 {
		t.Fatalf("clock step %v", d)
	}
}

// TestRenderHTTP: the section prints each cell's model, req/s with its
// sweeps, the exact mean latency, p50 and p99, the ratio to the best, and
// the languages with no std server.
func TestRenderHTTP(t *testing.T) {
	res := &httpResults{ClockStep: 0.0005, Spec: httpSpec{Task: "Serve.", Connections: 1000, WarmupS: 5, MeasureS: 30, Sweeps: 2, ServerMask: "0xff", ClientMask: "0xff00", Threads: 8,
		Skip: map[string]string{"rust": "no std HTTP", "c": "no std HTTP (C)"}},
		Cells: []httpCell{
			{Col: "go", Model: "goroutines", RPS: 200000, P50: 0.005, P99: 0.009, Sweeps: []loadResult{{RPS: 199000}, {RPS: 201000}}},
			{Col: "zig-fast", Model: "OS threads", RPS: 100000, P50: 0.01, P99: 0.02, Sweeps: []loadResult{{RPS: 100000}}},
			{Col: "zig-safe", Model: "OS threads", Excluded: "did not accept"},
		}}
	md := renderHTTP(res, "2026-11-08")
	for _, w := range []string{"| go | goroutines | 200000 (199000, 201000) | 5.0 ms | 5.0 ms | 9.0 ms | 1.00 |",
		"| zig-fast | OS threads | 100000 (100000) | 10.0 ms |", "| 0.50 |", "excluded: did not accept", "(CPUs 0-7)", "(CPUs 8-15)", "| c | | no std HTTP (C) |", "| rust | | no std HTTP |", "500 us"} {
		if !strings.Contains(md, w) {
			t.Errorf("render lacks %q:\n%s", w, md)
		}
	}
}

// TestHTTPGeneratorHungServer: a server that answers the first
// request of each connection and then never writes again fails the cell,
// naming the stalled connection, within warm-up + measure + httpGrace and a
// margin, instead of hanging generate (and, under http -publish, the
// machine lock) forever.
func TestHTTPGeneratorHungServer(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	body := []byte("Hello, World!\n")
	resp := fmt.Sprintf("HTTP/1.1 200 OK\r\nContent-Length: %d\r\n\r\n%s", len(body), body)
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				br := bufio.NewReader(c)
				for {
					line, err := br.ReadString('\n')
					if err != nil {
						return
					}
					if line == "\r\n" {
						break
					}
				}
				c.Write([]byte(resp))
				io.Copy(io.Discard, c) // read on, never answer again
			}(c)
		}
	}()
	o := loadOptions{Addr: l.Addr().String(), Body: body, Connections: 3, Warmup: 100 * time.Millisecond, Measure: 200 * time.Millisecond}
	limit := o.Warmup + o.Measure + httpGrace + 5*time.Second
	done := make(chan error, 1)
	start := time.Now()
	go func() {
		_, err := generate(o)
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "connection ") || !strings.Contains(err.Error(), "stopped answering") {
			t.Fatalf("a hung server passed or was not named: %v", err)
		}
		if d := time.Since(start); d > limit {
			t.Fatalf("generate took %s, over %s", d, limit)
		}
	case <-time.After(limit):
		t.Fatalf("generate still blocked after %s: a server that stops answering hangs the generator", limit)
	}
}
