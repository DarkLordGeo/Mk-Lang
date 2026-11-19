// tcp-pingpong, Go, net.TCPConn over the netpoller: one loopback TCP
// connection of one goroutine, both ends with TCP_NODELAY (Go's default,
// set again); each of n rounds writes 32 bytes (byte 0 the round's low
// byte) at the dialled end, reads them whole at the accepted end with
// io.ReadFull, writes them back and reads them whole at the dialled end.
// sum = 31 sum + byte 0 of each answer (wrapping). Prints n, sum and the
// failed calls. The mode argument is Mk's and ignored.
// Usage: tcppingpong <n> [mode].
package main

import (
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
)

func main() {
	n, _ := strconv.Atoi(os.Args[1])
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
	s, err := l.Accept()
	if err != nil {
		fmt.Println("no accept")
		return
	}
	d.(*net.TCPConn).SetNoDelay(true)
	s.(*net.TCPConn).SetNoDelay(true)
	msg := make([]byte, 32)
	for i := range msg {
		msg[i] = 7
	}
	buf := make([]byte, 32)
	var sum uint64
	fails := 0
	for i := 0; i < n; i++ {
		msg[0] = byte(i)
		_, e1 := d.Write(msg)
		_, e2 := io.ReadFull(s, buf)
		_, e3 := s.Write(buf)
		_, e4 := io.ReadFull(d, buf)
		if e1 != nil || e2 != nil || e3 != nil || e4 != nil {
			fails++
		}
		sum = sum*31 + uint64(buf[0])
	}
	fmt.Printf("%d %d %d\n", n, sum, fails)
}
