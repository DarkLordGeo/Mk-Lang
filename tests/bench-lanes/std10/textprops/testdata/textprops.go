// textprops, Go, idiomatic std: package unicode, unicode.In(r,
// unicode.Letter, unicode.Nl, unicode.Other_Alphabetic) (variant
// alphabetic: Alphabetic's definition, DerivedCoreProperties.txt) or
// unicode.IsUpper (variant upper: General_Category Lu) over 65,536 scalars
// drawn by splitmix64 from six stable ranges (README.md), reps passes; the
// checksum counts the hits plus the pass's index times each hit.
// Usage: textprops <variant> <reps> <seed>.
package main

import (
	"fmt"
	"os"
	"strconv"
	"unicode"
)

const n = 65536

func mix(z uint64) uint64 {
	z = (z ^ z>>30) * 0xBF58476D1CE4E5B9
	z = (z ^ z>>27) * 0x94D049BB133111EB
	return z ^ z>>31
}

var lo = [6]uint32{0x20, 0xC0, 0x391, 0x410, 0x3041, 0x4E00}
var ln = [6]uint32{95, 400, 57, 64, 86, 20902}

func main() {
	if len(os.Args) != 4 {
		fmt.Fprintln(os.Stderr, "usage: textprops <variant> <reps> <seed>")
		os.Exit(2)
	}
	upper := os.Args[1] == "upper"
	reps, _ := strconv.Atoi(os.Args[2])
	st, _ := strconv.ParseUint(os.Args[3], 10, 64)
	cs := make([]rune, n)
	for i := range cs {
		st += 0x9E3779B97F4A7C15
		z := mix(st)
		k := z % 6
		cs[i] = rune(lo[k] + uint32((z>>8)%uint64(ln[k])))
	}
	var acc uint64
	for r := 0; r < reps; r++ {
		for _, c := range cs {
			var hit bool
			if upper {
				hit = unicode.IsUpper(c)
			} else {
				hit = unicode.In(c, unicode.Letter, unicode.Nl, unicode.Other_Alphabetic)
			}
			if hit {
				acc += 1 + uint64(r)
			}
		}
	}
	fmt.Println(acc)
}
