// chan8, Go: 8 goroutines each send 0..n-1 on one chan int64 of capacity
// c, and 8 goroutines sum what they receive until it is closed; prints the
// total (8 * n * (n - 1) / 2). Usage: chan8 <n> <c>.
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
		fmt.Fprintln(os.Stderr, "usage: chan8 <n> <c>")
		os.Exit(2)
	}
	n, _ := strconv.ParseInt(os.Args[1], 10, 64)
	c, _ := strconv.Atoi(os.Args[2])
	ch := make(chan int64, c)
	var total atomic.Int64
	var rw, sw sync.WaitGroup
	for range 8 {
		rw.Add(1)
		go func() {
			defer rw.Done()
			var k int64
			for x := range ch {
				k += x
			}
			total.Add(k)
		}()
	}
	for range 8 {
		sw.Add(1)
		go func() {
			defer sw.Done()
			for i := range n {
				ch <- i
			}
		}()
	}
	sw.Wait()
	close(ch)
	rw.Wait()
	fmt.Println(total.Load())
}
