// sieve, Go: the reference algorithm of ../README.md.
// A []byte of n + 1 flags indexed with flags[i] (bounds checked). int
// indices and counters (they wrap). Built with GOAMD64=v2 -pgo=off.
package main

import (
	"fmt"
	"os"
	"strconv"
)

// tools/bench flips this line in a copy for the counting build.
const benchCount = false

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: sieve <n> <rounds>")
		os.Exit(2)
	}
	n, _ := strconv.Atoi(os.Args[1])
	rounds, _ := strconv.Atoi(os.Args[2])
	flags := make([]byte, n+1)
	total := 0
	marks := 0
	for r := 0; r < rounds; r++ {
		for i := 0; i < n+1; i++ {
			flags[i] = 1
		}
		count := 0
		for i := 2; i < n+1; i++ {
			if flags[i] != 0 {
				count += 1
				j := i * i
				for j <= n {
					flags[j] = 0
					if benchCount {
						marks++
					}
					j += i
				}
			}
		}
		total += count
	}
	fmt.Println(total)
	if benchCount {
		fmt.Printf("counts marks=%d\n", marks)
	}
}
