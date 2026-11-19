// atomicadd, Go, idiomatic std: the workload of ../../README.md with
// sync/atomic.Int64 over t goroutines. Each does n Add(1) on one shared
// counter (Go's atomics are sequentially consistent). Prints the counter
// (t * n). Usage: atomicadd <n> <t>.
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
		fmt.Fprintln(os.Stderr, "usage: atomicadd <n> <t>")
		os.Exit(2)
	}
	n, _ := strconv.Atoi(os.Args[1])
	t, _ := strconv.Atoi(os.Args[2])
	var c atomic.Int64
	var wg sync.WaitGroup
	for range t {
		wg.Go(func() {
			for range n {
				c.Add(1)
			}
		})
	}
	wg.Wait()
	fmt.Println(c.Load())
}
