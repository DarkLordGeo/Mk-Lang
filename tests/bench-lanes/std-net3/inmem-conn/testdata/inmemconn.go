// inmem-conn, Go: a connection over an in-memory transport, as a protocol
// layer sees one. The transport keeps one 32-byte message under its own
// sync.Mutex; the connection is an io.ReadWriter interface value whose
// concrete type takes a read or a write mutex of its own and calls the
// transport through another interface value (the shape of net.Conn over
// a netFD's fdMutex, or crypto/tls.Conn over its net.Conn). Each of n
// rounds writes 32 bytes (byte 0 the round's low byte) and reads them
// back. sum = 31 sum + byte 0 of each read (wrapping). The transport is
// picked at run time from the mode, as in every cell: null for null (every
// read is the end of the stream), else loop; Mk's direct mode is loop
// here. Prints n, sum and the failed calls.
// Usage: inmemconn <n> [mode].
package main

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"sync"
)

type loop struct {
	mu   sync.Mutex
	data [32]byte
	n    int
}

func (l *loop) Read(b []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := copy(b, l.data[:l.n])
	l.n = 0
	return n, nil
}

func (l *loop) Write(b []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.n = copy(l.data[:], b)
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

func main() {
	n, _ := strconv.Atoi(os.Args[1])
	var t io.ReadWriter = &loop{}
	if len(os.Args) > 2 && os.Args[2] == "null" {
		t = null{}
	}
	var c io.ReadWriter = &conn{t: t}
	msg := make([]byte, 32)
	for i := range msg {
		msg[i] = 7
	}
	buf := make([]byte, 32)
	var sum uint64
	fails := 0
	for i := 0; i < n; i++ {
		msg[0] = byte(i)
		_, e1 := c.Write(msg)
		k, e2 := c.Read(buf)
		if e1 != nil || e2 != nil || k != 32 {
			fails++
		}
		sum = sum*31 + uint64(buf[0])
	}
	fmt.Printf("%d %d %d\n", n, sum, fails)
}
