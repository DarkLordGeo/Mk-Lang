// notifypermit, Go, idiomatic std: the workload of ../../README.md with
// Go's notification idiom, one chan struct{} of capacity 1 per side: notify
// is a non-blocking send (a full channel already holds the permit), wait a
// receive. The first goroutine notifies then waits, the second waits then
// notifies, n times each. Prints the total rounds (2 * n).
// Usage: notifypermit <n>.
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
		fmt.Fprintln(os.Stderr, "usage: notifypermit <n>")
		os.Exit(2)
	}
	n, _ := strconv.Atoi(os.Args[1])
	c := [2]chan struct{}{make(chan struct{}, 1), make(chan struct{}, 1)}
	var total atomic.Int64
	var wg sync.WaitGroup
	for me := range 2 {
		wg.Go(func() {
			notify := func() {
				select {
				case c[1-me] <- struct{}{}:
				default:
				}
			}
			var k int64
			for range n {
				if me == 0 {
					notify()
					<-c[me]
				} else {
					<-c[me]
					notify()
				}
				k++
			}
			total.Add(k)
		})
	}
	wg.Wait()
	fmt.Println(total.Load())
}
