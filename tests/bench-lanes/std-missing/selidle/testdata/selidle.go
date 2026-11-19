// selidle, Go: one goroutine sends 0..n-1 on a chan int64 of capacity 64
// and closes it, main sums what it receives, while k goroutines sit in a
// select over two channels nobody uses until the end (README.md). A parked
// select in Go queues one sudog per case on those channels only, so it
// costs the stream nothing. After
// the stream, quit is closed. Usage: selidle <n> <k>. Prints sum + k.
package main

import (
	"fmt"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: selidle <n> <k>")
		os.Exit(2)
	}
	n, _ := strconv.ParseInt(os.Args[1], 10, 64)
	k, _ := strconv.ParseInt(os.Args[2], 10, 64)
	idle := make(chan int64)
	quit := make(chan int64)
	var got atomic.Int64
	var wg sync.WaitGroup
	for range k {
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case <-idle:
			case <-quit:
				got.Add(1)
			}
		}()
	}
	time.Sleep(20 * time.Millisecond)
	c := make(chan int64, 64)
	go func() {
		for i := range n {
			c <- i
		}
		close(c)
	}()
	var sum int64
	for x := range c {
		sum += x
	}
	close(quit)
	wg.Wait()
	fmt.Println(sum + got.Load())
}
