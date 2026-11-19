// mutex, Go, idiomatic std: the task of README.md with a sync.Mutex over t
// goroutines (sync.WaitGroup.Go; GOMAXPROCS from the row). Each goroutine
// does n times: Lock, add 1 to the shared count, Unlock. Prints the count
// (t * n). Usage: mutex <n> <t>.
package main

import (
	"fmt"
	"os"
	"strconv"
	"sync"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: mutex <n> <t>")
		os.Exit(2)
	}
	n, _ := strconv.Atoi(os.Args[1])
	t, _ := strconv.Atoi(os.Args[2])
	var m sync.Mutex
	var count int64
	var wg sync.WaitGroup
	for range t {
		wg.Go(func() {
			for range n {
				m.Lock()
				count++
				m.Unlock()
			}
		})
	}
	wg.Wait()
	fmt.Println(count)
}
