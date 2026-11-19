// time-httpdate, Go: HTTP date format and parse. The task and generator are README.md's.
// Usage: time-httpdate <n> <rounds>.
package main

import (
	"fmt"
	"net/http"
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

// instants makes n instants of 1970 to 2099 from g: whole seconds below
// 4,102,444,800, then nanoseconds below 10^9 (none when whole).
func instants(g *gen, n int, whole bool) []time.Time {
	ts := make([]time.Time, n)
	for i := range ts {
		secs := int64(g.next() % 4_102_444_800)
		var ns int64
		if !whole {
			ns = int64(g.next() % 1_000_000_000)
		}
		ts[i] = time.Unix(secs, ns).UTC()
	}
	return ts
}

func main() {
	n, _ := strconv.Atoi(os.Args[1])
	rounds, _ := strconv.Atoi(os.Args[2])
	g := &gen{st: 1}
	ts := instants(g, n, true)
	var sum uint64
	var buf []byte
	for range rounds {
		for _, t := range ts {
			buf = t.AppendFormat(buf[:0], http.TimeFormat)
			p, err := http.ParseTime(string(buf))
			if err != nil {
				panic(err)
			}
			sum += uint64(p.Unix()) + uint64(len(buf))
		}
	}
	fmt.Println(sum)
}
