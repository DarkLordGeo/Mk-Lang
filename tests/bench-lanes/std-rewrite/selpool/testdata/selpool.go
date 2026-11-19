// selpool, Go: w goroutines each loop on select over jobs and quit; one
// producer sends 0..n-1 on jobs (capacity c) and closes jobs; a worker ends
// when it receives from the closed, drained jobs; quit is closed only after
// every worker has ended, so the quit case never fires and never drops a
// buffered value (Go's select picks at random among ready cases; with
// quit closed beside jobs, `1 64 64` printed 0 in 18 of 20
// runs). Prints the sum received, n * (n - 1) / 2. Usage: selpool <w> <n> <c>.
package main

import (
	"fmt"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
)

func main() {
	if len(os.Args) != 4 {
		fmt.Fprintln(os.Stderr, "usage: selpool <w> <n> <c>")
		os.Exit(2)
	}
	w, _ := strconv.Atoi(os.Args[1])
	n, _ := strconv.ParseInt(os.Args[2], 10, 64)
	c, _ := strconv.Atoi(os.Args[3])
	jobs := make(chan int64, c)
	quit := make(chan int64)
	var total atomic.Int64
	var wg sync.WaitGroup
	for range w {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var k int64
			defer func() { total.Add(k) }()
			for {
				select {
				case x, ok := <-jobs:
					if !ok {
						return
					}
					k += x
				case <-quit:
					return
				}
			}
		}()
	}
	for i := range n {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	close(quit)
	fmt.Println(total.Load())
}
