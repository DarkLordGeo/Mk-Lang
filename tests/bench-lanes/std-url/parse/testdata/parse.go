// url-parse, Go, idiomatic std: net/url.Parse. n URLs made from splitmix64
// output (seed 1) by the generator README.md states (one in eight with an
// upper-case scheme and first host label, one in eight with an IPv4 host),
// then rounds times:
// parse each. The checksum adds, per URL and round, the lengths of the
// scheme, host, path, query and fragment, and the port.
// Usage: url-parse <n> <rounds>.
package main

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
)

const alnum = "abcdefghijklmnopqrstuvwxyz0123456789"
const upperAlnum = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
const pathChars = "abcdefghijklmnopqrstuvwxyz0123456789-_~"

type gen struct{ st uint64 }

func (g *gen) next() uint64 {
	g.st += 0x9E3779B97F4A7C15
	z := g.st
	z = (z ^ z>>30) * 0xBF58476D1CE4E5B9
	z = (z ^ z>>27) * 0x94D049BB133111EB
	return z ^ z>>31
}

func (g *gen) pick(n int) int { return int(g.next() % uint64(n)) }

func (g *gen) word(b *strings.Builder, chars string, count int) {
	for i := 0; i < count; i++ {
		k := g.pick(len(chars))
		b.WriteByte(chars[k])
	}
}

func (g *gen) makeURL() string {
	z := g.next()
	schemes := [5]string{"http", "https", "ws", "wss", "ftp"}
	upperSchemes := [5]string{"HTTP", "HTTPS", "WS", "WSS", "FTP"}
	ports := [4]int{0, 8080, 8443, 3000}
	tlds := [4]string{"com", "org", "net", "dev"}
	var b strings.Builder
	upper := (z>>44)%8 == 0
	if upper {
		b.WriteString(upperSchemes[z%5])
	} else {
		b.WriteString(schemes[z%5])
	}
	b.WriteString("://")
	if (z>>47)%8 == 0 {
		x := g.pick(256)
		y := g.pick(256)
		w := g.pick(256)
		fmt.Fprintf(&b, "10.%d.%d.%d", x, y, w)
	} else {
		labels := 1 + int((z>>16)%3)
		for i := 0; i < labels; i++ {
			chars := alnum
			if upper && i == 0 {
				chars = upperAlnum
			}
			g.word(&b, chars, 3+g.pick(8))
			b.WriteByte('.')
		}
		b.WriteString(tlds[(z>>24)%4])
	}
	if port := ports[(z>>8)%4]; port != 0 {
		fmt.Fprintf(&b, ":%d", port)
	}
	b.WriteByte('/')
	segments := int((z >> 32) % 6)
	for s := 0; s < segments; s++ {
		if s > 0 {
			b.WriteByte('/')
		}
		g.word(&b, pathChars, 1+g.pick(12))
	}
	if segments > 0 && (z>>40)&1 == 1 {
		b.WriteByte('/')
	}
	if (z>>41)&1 == 1 {
		b.WriteByte('?')
		pairs := 1 + g.pick(3)
		for p := 0; p < pairs; p++ {
			if p > 0 {
				b.WriteByte('&')
			}
			g.word(&b, alnum, 1+g.pick(6))
			b.WriteByte('=')
			g.word(&b, alnum, g.pick(8))
		}
	}
	if (z>>42)&3 == 0 {
		b.WriteByte('#')
		g.word(&b, alnum, 1+g.pick(10))
	}
	return b.String()
}

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: url-parse <n> <rounds>")
		os.Exit(2)
	}
	n, _ := strconv.Atoi(os.Args[1])
	rounds, _ := strconv.Atoi(os.Args[2])
	g := &gen{st: 1}
	urls := make([]string, n)
	for i := range urls {
		urls[i] = g.makeURL()
	}
	var total uint64
	for r := 0; r < rounds; r++ {
		for _, text := range urls {
			u, err := url.Parse(text)
			if err != nil {
				panic(err)
			}
			port := 0
			if p := u.Port(); p != "" {
				port, _ = strconv.Atoi(p)
			}
			total += uint64(len(u.Scheme)+len(u.Hostname())+len(u.EscapedPath())+len(u.RawQuery)+len(u.Fragment)) + uint64(port)
		}
	}
	fmt.Println(total)
}
