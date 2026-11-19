// time-duration, Go: Duration format and parse. The task and generator are README.md's.
// Usage: time-duration <n> <rounds>.
package main

import (
	"fmt"
	"math"
	"os"
	"strconv"
	"time"
)

type gen struct{ st uint64 }

func (g *gen) next() uint64 {
	g.st += 0x9E3779B97F4A7C15
	z := g.st
	z = (z ^ z>>30) * 0xBF58476D1CE4E5B9
	z = (z ^ z>>27) * 0x94D049BB133111EB
	return z ^ z>>31
}

func main() {
	n, _ := strconv.Atoi(os.Args[1])
	rounds, _ := strconv.Atoi(os.Args[2])
	g := &gen{st: 1}
	ds := make([]time.Duration, n)
	for i := range ds {
		v := int64(g.next())
		k := g.next() % 64
		ds[i] = time.Duration(v >> k)
	}
	var sum uint64
	for range rounds {
		for _, d := range ds {
			s := d.String()
			p, err := time.ParseDuration(s)
			if err != nil {
				panic(err)
			}
			sum += uint64(p&math.MaxInt64) + uint64(len(s))
		}
	}
	fmt.Println(sum)
}
