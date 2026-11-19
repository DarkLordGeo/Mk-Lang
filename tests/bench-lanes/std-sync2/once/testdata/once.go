// once, Go, idiomatic std: the workload of ../../README.md with
// sync.OnceValue (sync.Once with its value). t goroutines each do n rounds
// of the getter, whose function returns 7 the first time, and add the
// value. Prints the sum. Usage: once <n> <t>.
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
		fmt.Fprintln(os.Stderr, "usage: once <n> <t>")
		os.Exit(2)
	}
	n, _ := strconv.Atoi(os.Args[1])
	t, _ := strconv.Atoi(os.Args[2])
	get := sync.OnceValue(func() int64 { return 7 })
	var sum atomic.Int64
	var wg sync.WaitGroup
	for range t {
		wg.Go(func() {
			var k int64
			for range n {
				k += get()
			}
			sum.Add(k)
		})
	}
	wg.Wait()
	fmt.Println(sum.Load())
}
