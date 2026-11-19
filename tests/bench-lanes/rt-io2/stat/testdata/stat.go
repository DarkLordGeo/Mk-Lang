// stat, Go: the facts of one existing file, n times, through os.Stat; sum
// adds each answer's size. Prints n, sum and the failed calls. Usage: stat
// <path> <n>.
package main

import (
	"fmt"
	"os"
	"strconv"
)

func main() {
	p := os.Args[1]
	n, err := strconv.Atoi(os.Args[2])
	if err != nil {
		os.Exit(2)
	}
	var sum int64
	fails := 0
	for i := 0; i < n; i++ {
		fi, err := os.Stat(p)
		if err != nil {
			fails++
			continue
		}
		sum += fi.Size()
	}
	fmt.Printf("%d %d %d\n", n, sum, fails)
}
