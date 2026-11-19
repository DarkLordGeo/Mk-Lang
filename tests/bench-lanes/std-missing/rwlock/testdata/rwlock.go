// rwlock, Go, idiomatic std: the workload of ../../README.md with
// sync.RWMutex. t goroutines (on GOMAXPROCS threads, which tools/bench sets
// to the row's thread count); each does n operations on one shared pair:
// every tenth takes the write lock and adds one to both fields, the others
// take the read lock and count a pair whose fields differ. Prints the final
// first field and the mismatches. Usage: rwlock <n> <t>.
package main

import (
	"fmt"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
)

type pair struct{ a, b uint64 }

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: rwlock <n> <t>")
		os.Exit(2)
	}
	n, _ := strconv.Atoi(os.Args[1])
	t, _ := strconv.Atoi(os.Args[2])
	var mu sync.RWMutex
	var p pair
	var bad atomic.Int64
	var wg sync.WaitGroup
	for range t {
		wg.Go(func() {
			var miss int64
			for i := range n {
				if i%10 == 0 {
					mu.Lock()
					p.a++
					p.b++
					mu.Unlock()
				} else {
					mu.RLock()
					if p.a != p.b {
						miss++
					}
					mu.RUnlock()
				}
			}
			bad.Add(miss)
		})
	}
	wg.Wait()
	fmt.Println(p.a, bad.Load())
}
