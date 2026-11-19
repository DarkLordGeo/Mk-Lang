// deadlineread, Go: n round trips of one byte over a loopback TCP
// connection; an echo goroutine reads each byte and writes it back, and
// the main goroutine writes i % 251, then reads the echo. With deadline 1
// each read is preceded by conn.SetReadDeadline(time.Now() + 1 s) (Go's
// one-operation timeout), which never fires; with 0 the read is bare.
// sum = 31 sum + the byte read (wrapping). Prints n, sum and the failed
// calls. Usage: deadlineread <n> <deadline>.
package main

import (
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"time"
)

func main() {
	n, err := strconv.Atoi(os.Args[1])
	if err != nil {
		os.Exit(2)
	}
	timed := os.Args[2] == "1"
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		os.Exit(1)
	}
	defer l.Close()
	go func() {
		s, err := l.Accept()
		if err != nil {
			return
		}
		defer s.Close()
		b := make([]byte, 1)
		for i := 0; i < n; i++ {
			if _, err := io.ReadFull(s, b); err != nil {
				return
			}
			if _, err := s.Write(b); err != nil {
				return
			}
		}
	}()
	c, err := net.Dial("tcp", l.Addr().String())
	if err != nil {
		os.Exit(1)
	}
	defer c.Close()
	var sum uint64
	fails := 0
	b := make([]byte, 1)
	for i := 0; i < n; i++ {
		if _, err := c.Write([]byte{byte(i % 251)}); err != nil {
			fails++
			break
		}
		if timed {
			c.SetReadDeadline(time.Now().Add(time.Second))
		}
		if k, err := c.Read(b); err != nil || k != 1 {
			fails++
			break
		}
		sum = sum*31 + uint64(b[0])
	}
	fmt.Println(n, sum, fails)
}
