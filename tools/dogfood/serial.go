// Why: the kernel dogfood talks over its serial port, and QEMU's Windows
// stdio backend hands standard input to the guest's UART with no flow
// control: a byte the UART cannot take when it arrives is dropped, so a .in
// written at once reaches the guest as nothing.
//
// What: serialArgs (QEMU's chardev and serial options), runSerial (one QEMU
// run with COM1 on a socket), and serialLine, the harness's end of it.
//
// How: a TCP socket chardev reads from the socket only as far as the UART has
// room, so the bytes it has not taken wait in the socket's buffers. The
// harness listens on 127.0.0.1 port 0 and QEMU connects to it (-chardev
// socket,server=off): the OS picks the port, so two harnesses at once never
// race for one, and QEMU prints no "waiting for connection" line to the
// stderr a case compares. The guest's output is read from the socket from the
// moment QEMU connects. .in is typed a line at a time, as a person at the
// console would: each line once the guest has answered the previous one
// (printed something since and then paused for serialPause), the first once
// it has printed its boot lines (it has programmed its UART by then, and a
// UART's FIFO reset drops what it held), and any line after serialQuiet of
// silence. So what a command reports about the line (the kernel's `stat`:
// bytes received) does not depend on how fast the harness writes. The socket
// is never half-closed: QEMU takes end of input as a hang-up and stops
// sending the guest's output.
//
// QEMU's chardev logfile records every byte of output it sent; it stands
// for the socket's stream when that stream is a prefix of it, which is
// the case where QEMU exited with input the guest never read and the
// close reset the connection before the harness read the last bytes.

package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// serialPause is the pause in the guest's output after which the
// harness takes the guest to have answered and types the next line, and
// serialQuiet how long it waits for any answer before it types the next
// line anyway. Variables so a test can shorten them.
var serialPause, serialQuiet = 100 * time.Millisecond, 5 * time.Second

// serialArgs is the chardev and serial options of a QEMU line whose COM1
// connects to port and logs its output to logfile.
func serialArgs(port int, logfile string) []string {
	// A chardev option value doubles a comma (QEMU's option syntax).
	lf := strings.ReplaceAll(logfile, ",", ",,")
	return []string{
		"-chardev", fmt.Sprintf("socket,id=com1,host=127.0.0.1,port=%d,server=off,nodelay=on,logfile=%s,logappend=off", port, lf),
		"-serial", "chardev:com1",
	}
}

// runSerial runs QEMU's argv (no -serial option) in dir with COM1 on a
// socket the harness serves: stdin (a path or "") is the serial input,
// and the result's stdout is the guest's serial output followed by
// whatever QEMU itself wrote to its stdout. QEMU is killed after limit.
func runSerial(argv []string, dir string, env []string, stdin string, limit time.Duration) result {
	var input []byte
	if stdin != "" {
		b, err := os.ReadFile(stdin)
		if err != nil {
			return result{err: err.Error()}
		}
		input = b
	}
	ln, err := net.ListenTCP("tcp", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		return result{err: "serial: " + err.Error()}
	}
	defer ln.Close()
	logfile := filepath.Join(dir, "serial.log")
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()
	args := append(append([]string{}, argv[1:]...), serialArgs(ln.Addr().(*net.TCPAddr).Port, logfile)...)
	cmd := exec.CommandContext(ctx, argv[0], args...)
	cmd.Dir = dir
	cmd.Env = env
	cmd.WaitDelay = 5 * time.Second
	var o, e bytes.Buffer
	cmd.Stdout, cmd.Stderr = &o, &e
	start := time.Now()
	if err := cmd.Start(); err != nil {
		return result{err: err.Error()}
	}
	line := &serialLine{done: make(chan struct{})}
	accepted := make(chan struct{})
	go func() {
		defer close(accepted)
		ln.SetDeadline(time.Now().Add(limit))
		c, err := ln.Accept()
		if err != nil {
			close(line.done)
			return
		}
		line.conn = c
		go line.read()
		go line.write(input)
	}()
	err = cmd.Wait()
	ln.Close() // QEMU exited before it connected: Accept returns
	<-accepted
	if line.conn != nil {
		// What QEMU sent before it exited is in the socket; read it out,
		// then close the line, which ends the writer too.
		select {
		case <-line.done:
		case <-time.After(5 * time.Second):
		}
		line.conn.Close()
		<-line.done
	}
	out := line.output()
	if lb, lerr := os.ReadFile(logfile); lerr == nil && len(lb) > len(out) && bytes.HasPrefix(lb, out) {
		out = lb
	}
	r := result{stdout: append(out, o.Bytes()...), stderr: e.Bytes(), seconds: secs(time.Since(start))}
	var ee *exec.ExitError
	switch {
	case ctx.Err() != nil:
		r.timedOut = true
	case errors.As(err, &ee):
		r.code = ee.ExitCode()
	case err != nil:
		r.err = err.Error()
	}
	return r
}

// serialLine is the harness's end of COM1.
type serialLine struct {
	conn net.Conn
	mu   sync.Mutex
	buf  []byte
	last time.Time     // when the last output arrived
	done chan struct{} // closed when the reader stops
}

// read collects the guest's output until QEMU closes the socket (or
// resets it) or the harness does.
func (l *serialLine) read() {
	defer close(l.done)
	b := make([]byte, 32<<10)
	for {
		n, err := l.conn.Read(b)
		if n > 0 {
			l.mu.Lock()
			l.buf = append(l.buf, b[:n]...)
			l.last = time.Now()
			l.mu.Unlock()
		}
		if err != nil { // io.EOF, a reset, or the harness's Close
			return
		}
	}
}

// write types input a line at a time, each once the guest has answered
// (answered). A write blocks while QEMU does not read (the UART is full):
// that is the flow control. It stops once QEMU exits or the line closes.
func (l *serialLine) write(input []byte) {
	mark := 0
	for len(input) > 0 {
		n := bytes.IndexByte(input, '\n') + 1
		if n == 0 {
			n = len(input)
		}
		if !l.answered(mark) {
			return
		}
		mark = l.outLen()
		if _, err := l.conn.Write(input[:n]); err != nil {
			return
		}
		input = input[n:]
	}
}

// answered waits until the guest has printed past mark bytes and then
// paused for serialPause, or has printed nothing past mark for
// serialQuiet; false when the line closed first.
func (l *serialLine) answered(mark int) bool {
	start := time.Now()
	tick := time.NewTicker(max(serialPause/10, time.Millisecond))
	defer tick.Stop()
	for {
		l.mu.Lock()
		n, last := len(l.buf), l.last
		l.mu.Unlock()
		if n > mark && time.Since(last) >= serialPause || n <= mark && time.Since(start) >= serialQuiet {
			return true
		}
		select {
		case <-l.done:
			return false
		case <-tick.C:
		}
	}
}

func (l *serialLine) outLen() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.buf)
}

func (l *serialLine) output() []byte {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]byte(nil), l.buf...)
}
