// conn-churn, Go, net.TCPConn over the netpoller: n loopback TCP
// connections, one after another, of one goroutine. Each round dials the
// listener, accepts the connection with the peer's address from accept
// itself (RemoteAddr), writes two bytes (the round's low byte twice) at the
// accepted end, reads one at the dialled end, and closes the dialled end,
// then the accepted one. The dialled end closes with a byte unread, so its
// TCP resets the connection (RFC 1122 4.2.2.13) and no TIME_WAIT entry is
// left. sum = 31 sum + each byte read (wrapping); a round
// whose peer is not 127.0.0.1 with a port, or whose call failed, counts as
// failed. Prints n, sum and the failed rounds. Usage: connchurn <n>.
package main

import (
	"fmt"
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
	at := l.Addr().String()
	var sum uint64
	fails := 0
	two := make([]byte, 2)
	buf := make([]byte, 1)
	for i := 0; i < n; i++ {
		d, err := net.Dial("tcp4", at)
		if err != nil {
			fails++
			continue
		}
		s, err := l.Accept()
		if err != nil {
			d.Close()
			fails++
			continue
		}
		p, ok := s.RemoteAddr().(*net.TCPAddr)
		if !ok || !p.IP.Equal(net.IPv4(127, 0, 0, 1)) || p.Port == 0 {
			fails++
		}
		two[0], two[1] = byte(i), byte(i)
		buf[0] = 0
		if _, err := s.Write(two); err != nil {
			fails++
		}
		if k, err := d.Read(buf); err != nil || k != 1 {
			fails++
		}
		sum = sum*31 + uint64(buf[0])
		d.Close()
		s.Close()
	}
	fmt.Printf("%d %d %d\n", n, sum, fails)
}
