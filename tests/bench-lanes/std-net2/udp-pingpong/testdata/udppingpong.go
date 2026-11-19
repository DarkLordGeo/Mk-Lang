// udp-pingpong, Go, net.UDPConn: two UDP sockets on 127.0.0.1; each of n
// rounds sends 32 bytes (byte 0 the round's low byte) from a to b,
// receives them on b, sends them back and receives them on a.
// sum = 31 sum + byte 0 of each answer (wrapping). Prints n, sum and the
// failed calls. Usage: udppingpong <n>.
package main

import (
	"fmt"
	"net"
	"os"
	"strconv"
)

func main() {
	n, _ := strconv.Atoi(os.Args[1])
	lo := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)}
	a, err1 := net.ListenUDP("udp4", lo)
	b, err2 := net.ListenUDP("udp4", lo)
	if err1 != nil || err2 != nil {
		fmt.Println("no sockets")
		return
	}
	tb := b.LocalAddr().(*net.UDPAddr)
	msg := make([]byte, 32)
	for i := range msg {
		msg[i] = 7
	}
	buf := make([]byte, 64)
	var sum uint64
	fails := 0
	for i := 0; i < n; i++ {
		msg[0] = byte(i)
		_, e1 := a.WriteToUDP(msg, tb)
		k1, from, e2 := b.ReadFromUDP(buf)
		_, e3 := b.WriteToUDP(buf[:32], from)
		k2, _, e4 := a.ReadFromUDP(buf)
		if e1 != nil || e2 != nil || e3 != nil || e4 != nil || k1 != 32 || k2 != 32 {
			fails++
		}
		sum = sum*31 + uint64(buf[0])
	}
	fmt.Printf("%d %d %d\n", n, sum, fails)
}
