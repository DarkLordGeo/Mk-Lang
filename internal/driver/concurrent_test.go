// Why: two commands may share a cache while one of them still runs a
// program, and Windows cannot overwrite a running executable.
// What: TestConcurrentRunSameBin over blockingProgram.
// How: mk run places its executable by content under <cache>/bin, so a
// running program is never overwritten; the test edits and rebuilds while
// the first run waits on stdin.

package driver

import (
	"bufio"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// blockingProgram writes "ready", then waits for one byte of stdin and
// exits with 1 plus base.
func blockingProgram(base string) string {
	return "struct Buf {\n    b: [1]u8\n}\n\n" +
		"Ready(sys: &System): WriteError!void {\n    var w := sys.Stdout()\n    try w.Str(\"ready\\n\")\n    try w.Flush()\n}\n\n" +
		"export Main(sys: &System): int {\n    match Ready(sys) {\n        ok(_) {\n        }\n        err(_) {\n            return 6\n        }\n    }\n" +
		"    var r := sys.Stdin()\n    var buf := Buf(b: [0])\n    match r.Read(mut buf.b[..]) {\n" +
		"        ok(n) {\n            return n + " + base + "\n        }\n        err(_) {\n            return 5\n        }\n    }\n}\n"
}

// TestConcurrentRunSameBin: while one mk run of a program waits on stdin,
// the program is edited, and a second mk run and an mk build of it both
// succeed (the second run links to a new content-named path); then the
// first finishes with its own status.
func TestConcurrentRunSameBin(t *testing.T) {
	tools := needTools(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "wait.mk")
	if err := os.WriteFile(src, []byte(blockingProgram("30")), 0o644); err != nil {
		t.Fatal(err)
	}
	cache := filepath.Join(dir, "cache")
	cfg := Config{Root: src, Tools: tools, CacheDir: cache}

	s1, err := NewSession(cfg)
	if err != nil {
		t.Fatal(err)
	}
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	type done struct {
		r      Result
		status int
		err    error
	}
	first := make(chan done, 1)
	go func() {
		r, st, err := s1.Run(nil, RunIO{Stdin: inR, Stdout: outW, Stderr: io.Discard})
		outW.Close()
		first <- done{r, st, err}
	}()
	ready := make(chan bool, 1)
	go func() {
		line, _ := bufio.NewReader(outR).ReadString('\n')
		ready <- line == "ready\n"
		io.Copy(io.Discard, outR)
	}()
	select {
	case ok := <-ready:
		if !ok {
			d := <-first
			skipKnownBlockers(t, d.r)
			t.Fatalf("first run did not start: status %d err %v\n%s", d.status, d.err, d.r.Report.Text(d.r.Sources))
		}
	case <-time.After(60 * time.Second):
		t.Fatal("first run not ready after 60 s")
	}
	defer inW.Close()

	if err := os.WriteFile(src, []byte(blockingProgram("40")), 0o644); err != nil {
		t.Fatal(err)
	}
	s2, err := NewSession(cfg)
	if err != nil {
		t.Fatal(err)
	}
	r, st, err := s2.Run(nil, RunIO{Stdin: strings.NewReader("x"), Stdout: io.Discard, Stderr: io.Discard})
	if err != nil || st != 41 || len(r.Report.Diags) != 0 {
		t.Errorf("second run: status %d err %v, want 41\n%s", st, err, r.Report.Text(r.Sources))
	}
	s3, err := NewSession(cfg)
	if err != nil {
		t.Fatal(err)
	}
	r, paths, err := s3.Build(filepath.Join(dir, "wait"+exeSuffix(cfg.Target)))
	if err != nil || len(paths) != 1 || len(r.Report.Diags) != 0 {
		t.Errorf("build: paths %q err %v\n%s", paths, err, r.Report.Text(r.Sources))
	}

	if _, err := inW.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	inW.Close()
	select {
	case d := <-first:
		if d.err != nil || d.status != 31 {
			t.Errorf("first run: status %d err %v, want 31", d.status, d.err)
		}
	case <-time.After(60 * time.Second):
		t.Fatal("first run did not finish")
	}
	if ents, _ := os.ReadDir(binOf(cache)); len(ents) != 2 {
		t.Errorf("the store's bin holds %d entries, want 2 (one per program text)", len(ents))
	}
}
