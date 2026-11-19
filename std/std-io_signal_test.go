// Why: proc.WaitSignal must end on a real OS signal sent from outside, on
// both targets, and a watched signal loses its default action.
// What: TestSignalDelivery, with lib146Linux and lib146Judge.
// How: a program that never waited must still die of the signal, so the
// signal is real. Linux sends SIGINT, SIGTERM and SIGHUP with kill(1) under
// one wsl.exe; Windows raises Ctrl+C (Interrupt) and Ctrl+Break (Terminate)
// on the program's own console (std-io_signal_windows_test.go). Hangup never
// fires on Windows.

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
	"testing"
	"time"
)

// signalProgram: args[1] is int, term, hup or none. For a signal, a task
// waits for it while Main sleeps 200 ms (so the watch is installed before
// the test starts sending), prints "ready", and joins the waiter, then
// prints "got", sleeps 500 ms (the test sends the signal once more in that
// time: with the default action gone the program lives on) and prints
// "after". "none" prints "ready" and sleeps 3 s without ever waiting, so
// the signal's default action ends it.
const signalProgram = `import std.proc
import std.time

Pick(name: str): proc.Signal {
    if name == "int" {
        return proc.Signal.Interrupt
    }
    if name == "term" {
        return proc.Signal.Terminate
    }
    return proc.Signal.Hangup
}

Wait(io: Io, s: proc.Signal) {
    proc.WaitSignal(io, s) else {
        return
    }
}

export Main(sys: &System): WriteError|Canceled!void {
    var out := sys.Stdout()
    if sys.args[1] == "none" {
        try out.Str("ready\n")
        try out.Flush()
        try sys.io.Sleep(time.Millis(3_000))
        try out.Str("survived\n")
        return
    }
    s := Pick(sys.args[1])
    scope g := sys.io.Scope() {
        g.go Wait(sys.io, s)
        try sys.io.Sleep(time.Millis(200))
        try out.Str("ready\n")
        try out.Flush()
    }
    try out.Str("got\n")
    try out.Flush()
    try sys.io.Sleep(time.Millis(500))
    try out.Str("after\n")
}
`

// signalCase is one run: the program's argument and, on Linux, kill's
// signal name.
type signalCase struct {
	arg, sig string
}

// TestSignalDelivery: on the host, every signal the OS can raise
// to a console program ends a WaitSignal for it, the program survives a
// second delivery, and an unwatched Ctrl+C or SIGINT still kills.
func TestSignalDelivery(t *testing.T) { ioSignalDelivery(t, hostIo) }

func ioSignalDelivery(t *testing.T, h ioHost) {
	exe := h.build(t, nil, signalProgram)
	if h.linux {
		lib146Linux(t, exe)
		return
	}
	lib146Windows(t, exe)
}

// lib146Judge judges one watched run: exit 0 and every line in order.
func lib146Judge(t *testing.T, what string, r nativeRun) {
	t.Helper()
	if r.code != 0 || r.stdout != "ready\ngot\nafter\n" || r.stderr != "" {
		t.Errorf("%s: exit %d, stdout %q, stderr %q; want 0, ready, got, after and an empty stderr", what, r.code, r.stdout, r.stderr)
	}
}

// lib146Linux runs every case in one bash script under one wsl.exe: start
// the program, wait for "ready", send the signal every 50 ms until "got"
// shows (at most 4 s), send it once more, and wait for the exit (a 20 s
// watchdog kills a program that never ends, status 137).
func lib146Linux(t *testing.T, exe string) {
	t.Helper()
	// The unwatched case sends SIGTERM: bash starts a background job of a
	// non-interactive script with SIGINT ignored, which a watch overrides
	// (sigaction replaces SIG_IGN) but the default action would not.
	cases := []signalCase{{"int", "INT"}, {"term", "TERM"}, {"hup", "HUP"}, {"none", "TERM"}}
	var sb strings.Builder
	sb.WriteString("#!/bin/bash\n")
	sb.WriteString(`run() {
  ./` + shQuote(filepath.Base(exe)) + ` "$1" > sig_$3.out 2> sig_$3.err &
  pid=$!
  ( sleep 20; kill -KILL $pid 2>/dev/null ) & dog=$!
  for i in $(seq 200); do grep -q ready sig_$3.out && break; sleep 0.05; done
  sleep 0.3; grep -q got sig_$3.out && echo "got before any signal" >> sig_$3.err
  for i in $(seq 80); do grep -q got sig_$3.out && break; kill -$2 $pid 2>/dev/null; sleep 0.05; done
  kill -$2 $pid 2>/dev/null
  wait $pid; echo $? > sig_$3.code
  kill $dog 2>/dev/null; wait $dog 2>/dev/null
}
`)
	for i, c := range cases {
		fmt.Fprintf(&sb, "run %s %s %d\n", c.arg, c.sig, i)
	}
	sb.WriteString("exit 0\n") // the last wait reports the watchdog's SIGTERM
	dir := filepath.Dir(exe)
	if out, err := stdioWSLScript(dir, "sig_run.sh", sb.String()); err != nil {
		t.Fatalf("wsl.exe bash sig_run.sh: %v\n%s", err, out)
	}
	runs := make([]nativeRun, len(cases))
	for i := range cases {
		read := func(ext string) string {
			b, err := os.ReadFile(filepath.Join(dir, fmt.Sprintf("sig_%d.%s", i, ext)))
			if err != nil {
				t.Fatalf("case %d: %v", i, err)
			}
			return string(b)
		}
		code, err := strconv.Atoi(strings.TrimSpace(read("code")))
		if err != nil {
			t.Fatalf("case %d: status %v", i, err)
		}
		runs[i] = nativeRun{stdout: read("out"), stderr: read("err"), code: code}
	}
	for i, c := range cases[:3] {
		lib146Judge(t, "SIG"+c.sig, runs[i])
	}
	// SIGTERM's default action: the shell reports 128 + 15.
	if r := runs[3]; r.code != 143 || r.stdout != "ready\n" {
		t.Errorf("unwatched SIGTERM: exit %d, stdout %q, stderr %q; want 143 and only ready", r.code, r.stdout, r.stderr)
	}
}

// stdioWSLScript writes script to dir/name and runs it with one wsl.exe
// call in dir (bounded by 10 minutes), as runWSLBatch does; the combined
// output is returned for the failure message.
func stdioWSLScript(dir, name, script string) (string, error) {
	if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o755); err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	wslGuard()
	cmd := exec.CommandContext(ctx, "wsl.exe", "--cd", stdioWSLPath(dir), "-e", "bash", "./"+name)
	cmd.WaitDelay = 10 * time.Second
	var o bytes.Buffer
	cmd.Stdout, cmd.Stderr = &o, &o
	err := cmd.Run()
	return o.String(), err
}
