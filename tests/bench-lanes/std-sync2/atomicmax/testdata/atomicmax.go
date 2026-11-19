// atomicmax, Go, idiomatic std: the workload of ../../README.md with two
// sync/atomic.Int64 over t goroutines. Each takes a ticket n times with
// Add(1) and raises a shared maximum to it with a CompareAndSwap loop (Go's
// atomics are sequentially consistent and its CAS is strong; on x86-64
// each is the same lock-prefixed instruction). Prints the counter and the
// maximum (t * n and t * n - 1). Usage: atomicmax <n> <t>.
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
		fmt.Fprintln(os.Stderr, "usage: atomicmax <n> <t>")
		os.Exit(2)
	}
	n, _ := strconv.Atoi(os.Args[1])
	t, _ := strconv.Atoi(os.Args[2])
	var c, m atomic.Int64
	var wg sync.WaitGroup
	for range t {
		wg.Go(func() {
			for range n {
				x := c.Add(1) - 1
				cur := m.Load()
				for x > cur {
					if m.CompareAndSwap(cur, x) {
						break
					}
					cur = m.Load()
				}
			}
		})
	}
	wg.Wait()
	fmt.Println(c.Load(), m.Load())
}
