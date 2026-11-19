// spawn, Go, idiomatic std: the task of README.md with goroutines: n
// goroutines started by go, each adding its index to one atomic.Int64, a
// sync.WaitGroup joining them. They run on GOMAXPROCS threads (tools/bench
// sets it to the row's thread count). Usage: spawn <n> <rounds>.
package main

import (
	"fmt"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: spawn <n> <rounds>")
		os.Exit(2)
	}
	n, _ := strconv.ParseInt(os.Args[1], 10, 64)
	rounds, _ := strconv.Atoi(os.Args[2])
	var total int64
	for range rounds {
		var sum atomic.Int64
		var wg sync.WaitGroup
		for i := range n {
			wg.Go(func() { sum.Add(i) })
		}
		wg.Wait()
		total += sum.Load()
	}
	fmt.Println(total)
}
