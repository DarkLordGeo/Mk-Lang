// notify, Go, idiomatic std: the workload of ../../README.md with a
// sync.Mutex guarding whose turn it is and one sync.Cond per side, over
// two goroutines. Each side, n times: lock, Wait while it is not its turn,
// give the turn to the other, unlock, Signal the other's Cond. Prints the
// total rounds (2 * n). Usage: notify <n>.
package main

import (
	"fmt"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: notify <n>")
		os.Exit(2)
	}
	n, _ := strconv.Atoi(os.Args[1])
	var mu sync.Mutex
	cv := [2]*sync.Cond{sync.NewCond(&mu), sync.NewCond(&mu)}
	turn := 0
	var total atomic.Int64
	var wg sync.WaitGroup
	for me := range 2 {
		wg.Go(func() {
			var k int64
			for range n {
				mu.Lock()
				for turn != me {
					cv[me].Wait()
				}
				turn = 1 - me
				mu.Unlock()
				cv[1-me].Signal()
				k++
			}
			total.Add(k)
		})
	}
	wg.Wait()
	fmt.Println(total.Load())
}
