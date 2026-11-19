// semaphore, Go, idiomatic std: the workload of ../../README.md. Go's
// standard library has no semaphore type; its idiom is a buffered channel
// of capacity 4 (send to acquire, receive to release), which is weight 1
// only (golang.org/x/sync/semaphore is not std). t goroutines each do n
// rounds of acquire, a check that at most 4 hold one, release. Prints t * n
// and the violations. Usage: semaphore <n> <t>.
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
		fmt.Fprintln(os.Stderr, "usage: semaphore <n> <t>")
		os.Exit(2)
	}
	n, _ := strconv.Atoi(os.Args[1])
	t, _ := strconv.Atoi(os.Args[2])
	sem := make(chan struct{}, 4)
	var inuse, bad, done atomic.Int64
	var wg sync.WaitGroup
	for range t {
		wg.Go(func() {
			var k int64
			for range n {
				sem <- struct{}{}
				if inuse.Add(1) > 4 {
					bad.Add(1)
				}
				inuse.Add(-1)
				<-sem
				k++
			}
			done.Add(k)
		})
	}
	wg.Wait()
	fmt.Println(done.Load(), bad.Load())
}
