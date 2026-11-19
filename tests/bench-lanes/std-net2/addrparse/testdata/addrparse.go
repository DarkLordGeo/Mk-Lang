// addrparse, Go, net/netip: AddrPort.String and ParseAddrPort round trips
// of splitmix64 addresses (README.md). Usage: addrparse <n>.
package main

import (
	"fmt"
	"net/netip"
	"os"
	"strconv"
)

var s uint64 = 1

func next() uint64 {
	s += 0x9E3779B97F4A7C15
	z := s
	z = (z ^ (z >> 30)) * 0xBF58476D1CE4E5B9
	z = (z ^ (z >> 27)) * 0x94D049BB133111EB
	return z ^ (z >> 31)
}

func main() {
	n, _ := strconv.Atoi(os.Args[1])
	var sum uint64
	for i := 0; i < n; i++ {
		x := next()
		var a netip.Addr
		if x&1 == 0 {
			a = netip.AddrFrom4([4]byte{byte(x >> 8), byte(x >> 16), byte(x >> 24), byte(x >> 32)})
		} else {
			y, z := next(), next()
			zero := (x >> 8) & (x >> 16)
			var ip [16]byte
			for k := 0; k < 8; k++ {
				w := y
				if k >= 4 {
					w = z
				}
				g := (w >> (16 * (k % 4))) & 0xFFFF
				if (zero>>k)&1 == 1 {
					g = 0
				}
				if k == 0 {
					g |= 0x2000
				}
				ip[2*k], ip[2*k+1] = byte(g>>8), byte(g)
			}
			a = netip.AddrFrom16(ip)
		}
		t := netip.AddrPortFrom(a, uint16((x>>40)&0xFFFF)).String()
		p, err := netip.ParseAddrPort(t)
		if err != nil {
			panic(err)
		}
		sum = sum*31 + uint64(p.Port())
		if p.Addr().Is4() {
			for _, b := range p.Addr().As4() {
				sum = sum*31 + uint64(b)
			}
		} else {
			for _, b := range p.Addr().As16() {
				sum = sum*31 + uint64(b)
			}
		}
	}
	fmt.Printf("%d %d\n", n, sum)
}
