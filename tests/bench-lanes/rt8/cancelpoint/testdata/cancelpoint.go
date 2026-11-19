// cancelpoint, Go: n uncontended Lock and Unlock pairs of a sync.Mutex,
// each followed by ctx.Err() on this loop's context (Go's cancellation
// point), the guarded value updated as x = x * 31 + i (wrapping). With
// expired 1 another context, an unrelated one, has expired before the
// loop (context.WithTimeout of 0); this loop's context is not derived from
// it. Prints n, x and the failed calls. Usage: cancelpoint <n> <expired>.
package main

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"sync"
)

func main() {
	n, err := strconv.ParseInt(os.Args[1], 10, 64)
	if err != nil {
		os.Exit(2)
	}
	expired, err := strconv.Atoi(os.Args[2])
	if err != nil {
		os.Exit(2)
	}
	if expired == 1 {
		other, cancel := context.WithTimeout(context.Background(), 0)
		defer cancel()
		<-other.Done()
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var mu sync.Mutex
	var x uint64
	fails := 0
	for i := int64(0); i < n; i++ {
		mu.Lock()
		if ctx.Err() != nil {
			mu.Unlock()
			fails++
			break
		}
		x = x*31 + uint64(i)
		mu.Unlock()
	}
	fmt.Println(n, x, fails)
}
