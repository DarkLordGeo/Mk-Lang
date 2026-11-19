// spin, Go, idiomatic std: the workload of ../../README.md with sync.Mutex
// (Go's std has no spin lock; its Mutex spins briefly before it parks).
// t goroutines (on GOMAXPROCS threads, which tools/bench sets to the row's
// thread count) each take the lock n times and add one to the shared
// counter. Prints the counter. Usage: spin <n> <t>.
package main

import (
	"fmt"
	"os"
	"strconv"
	"sync"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: spin <n> <t>")
		os.Exit(2)
	}
	n, _ := strconv.Atoi(os.Args[1])
	t, _ := strconv.Atoi(os.Args[2])
	var mu sync.Mutex
	var c uint64
	var wg sync.WaitGroup
	for range t {
		wg.Go(func() {
			for range n {
				mu.Lock()
				c++
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	fmt.Println(c)
}
