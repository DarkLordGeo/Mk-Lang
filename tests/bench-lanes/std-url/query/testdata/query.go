// url-query, Go, idiomatic std: net/url.ParseQuery. n queries made from
// splitmix64 output (seed 1) by the generator README.md states, then rounds
// times: parse each. The checksum adds, per query and round, the length of
// every name and every value.
// Usage: url-query <n> <rounds>.
package main

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
)

var pieces = []string{
	"a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k", "l", "m", "n", "o", "p", "q", "r", "s", "t", "u", "v", "w", "x", "y", "z",
	"0", "1", "2", "3", "4", "5", "6", "7", "8", "9", "-", ".", "_", "~",
	"+", "+", "+", "+", "%20", "%2B", "%C3%A9", "%26",
}

type gen struct{ st uint64 }

func (g *gen) next() uint64 {
	g.st += 0x9E3779B97F4A7C15
	z := g.st
	z = (z ^ z>>30) * 0xBF58476D1CE4E5B9
	z = (z ^ z>>27) * 0x94D049BB133111EB
	return z ^ z>>31
}

func (g *gen) pick(n int) int { return int(g.next() % uint64(n)) }

func (g *gen) makeQuery() string {
	var b strings.Builder
	pairs := 1 + g.pick(8)
	for p := 0; p < pairs; p++ {
		if p > 0 {
			b.WriteByte('&')
		}
		z := g.next()
		k := 40
		if z%4 == 0 {
			k = 48
		}
		name := 1 + int((z>>8)%8)
		for i := 0; i < name; i++ {
			b.WriteString(pieces[g.pick(k)])
		}
		b.WriteByte('=')
		value := int((z >> 16) % 12)
		for i := 0; i < value; i++ {
			b.WriteString(pieces[g.pick(k)])
		}
	}
	return b.String()
}

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: url-query <n> <rounds>")
		os.Exit(2)
	}
	n, _ := strconv.Atoi(os.Args[1])
	rounds, _ := strconv.Atoi(os.Args[2])
	g := &gen{st: 1}
	queries := make([]string, n)
	for i := range queries {
		queries[i] = g.makeQuery()
	}
	var total uint64
	for r := 0; r < rounds; r++ {
		for _, q := range queries {
			values, err := url.ParseQuery(q)
			if err != nil {
				panic(err)
			}
			for k, vs := range values {
				for _, v := range vs {
					total += uint64(len(k) + len(v))
				}
			}
		}
	}
	fmt.Println(total)
}
