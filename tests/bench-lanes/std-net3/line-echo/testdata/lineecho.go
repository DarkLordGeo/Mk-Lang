// line-echo, Go: a buffered line echo through bufio (4096-byte
// bufio.Reader and bufio.Writer at each end). Each of n rounds writes 16
// lines through the client's Writer and flushes, reads each line at the
// server with ReadSlice('\n') and writes it back through the server's
// Writer, flushes, and reads the 16 lines back at the client with
// ReadSlice('\n'). A line is 32 bytes: byte 0 is 32 + k mod 64 for the
// k-th line sent, then 30 'x' and a newline. sum = 31 sum + byte 0 + 256
// * length of each line read back at the client (wrapping). Mode tcp runs
// over one loopback net.TCPConn (the netpoller, TCP_NODELAY at both
// ends); mode inmem over two in-memory pipes of 4096 bytes, each under
// its own sync.Mutex, one connection per end that takes a read or a write
// mutex of its own and calls its transport through an interface value
// (the shape of net.Conn over a netFD, or crypto/tls.Conn over its
// net.Conn). The in-memory transport is picked at run time from the mode,
// as in every cell: null for null (every read is the end of the stream),
// else end. Prints n, sum and the failed calls.
// Usage: lineecho <n> tcp|inmem|null.
package main

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"sync"
)

const lines = 16

// pipe holds the bytes written to one direction and not yet read.
type pipe struct {
	mu         sync.Mutex
	data       [4096]byte
	start, end int
}

// end is one end of the in-memory duplex: it reads rx and writes tx.
type end struct {
	rx, tx *pipe
}

func (e *end) Read(b []byte) (int, error) {
	p := e.rx
	p.mu.Lock()
	defer p.mu.Unlock()
	n := copy(b, p.data[p.start:p.end])
	p.start += n
	if p.start == p.end {
		p.start, p.end = 0, 0
	}
	return n, nil
}

func (e *end) Write(b []byte) (int, error) {
	p := e.tx
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(b) > len(p.data)-p.end {
		panic("lineecho: a pipe overflowed")
	}
	copy(p.data[p.end:], b)
	p.end += len(b)
	return len(b), nil
}

// null reads the end of the stream and drops what is written.
type null struct{}

func (null) Read(b []byte) (int, error) { return 0, nil }

func (null) Write(b []byte) (int, error) { return len(b), nil }

type conn struct {
	rmu, wmu sync.Mutex
	t        io.ReadWriter
}

func (c *conn) Read(b []byte) (int, error) {
	c.rmu.Lock()
	defer c.rmu.Unlock()
	return c.t.Read(b)
}

func (c *conn) Write(b []byte) (int, error) {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	return c.t.Write(b)
}

func echo(n int, c, s io.ReadWriter) (uint64, int) {
	cw := bufio.NewWriterSize(c, 4096)
	cr := bufio.NewReaderSize(c, 4096)
	sw := bufio.NewWriterSize(s, 4096)
	sr := bufio.NewReaderSize(s, 4096)
	line := make([]byte, 32)
	for i := range line {
		line[i] = 'x'
	}
	line[31] = '\n'
	var sum uint64
	fails := 0
	k := 0
	for i := 0; i < n; i++ {
		for j := 0; j < lines; j++ {
			line[0] = byte(32 + k%64)
			k++
			if _, err := cw.Write(line); err != nil {
				fails++
			}
		}
		if cw.Flush() != nil {
			fails++
		}
		for j := 0; j < lines; j++ {
			got, err := sr.ReadSlice('\n')
			if err != nil {
				fails++
			}
			if _, err := sw.Write(got); err != nil {
				fails++
			}
		}
		if sw.Flush() != nil {
			fails++
		}
		for j := 0; j < lines; j++ {
			got, err := cr.ReadSlice('\n')
			if err != nil || len(got) != 32 {
				fails++
			}
			var b0 uint64
			if len(got) > 0 {
				b0 = uint64(got[0])
			}
			sum = sum*31 + b0 + 256*uint64(len(got))
		}
	}
	return sum, fails
}

func main() {
	n, _ := strconv.Atoi(os.Args[1])
	var sum uint64
	var fails int
	mode := ""
	if len(os.Args) > 2 {
		mode = os.Args[2]
	}
	if mode == "inmem" || mode == "null" {
		up, down := &pipe{}, &pipe{}
		var ct, st io.ReadWriter = &end{rx: down, tx: up}, &end{rx: up, tx: down}
		if mode == "null" {
			ct, st = null{}, null{}
		}
		var c io.ReadWriter = &conn{t: ct}
		var s io.ReadWriter = &conn{t: st}
		sum, fails = echo(n, c, s)
	} else {
		l, err := net.Listen("tcp4", "127.0.0.1:0")
		if err != nil {
			fmt.Println("no listener")
			return
		}
		d, err := net.Dial("tcp4", l.Addr().String())
		if err != nil {
			fmt.Println("no connection")
			return
		}
		a, err := l.Accept()
		if err != nil {
			fmt.Println("no accept")
			return
		}
		d.(*net.TCPConn).SetNoDelay(true)
		a.(*net.TCPConn).SetNoDelay(true)
		sum, fails = echo(n, d, a)
	}
	fmt.Printf("%d %d %d\n", n, sum, fails)
}
