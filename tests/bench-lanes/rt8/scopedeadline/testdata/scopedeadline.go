// scopedeadline, Go: n rounds of a context with a one-second timeout
// (context.WithTimeout, Go's per-operation deadline on its per-P timer
// heaps), which never fires, split evenly over t goroutines. Goroutine k
// runs rounds i = k m .. k m + m - 1 (m = n / t), each deriving the
// context, updating x = x * 31 + i (wrapping), checking ctx.Err() and
// cancelling it (which stops its timer). Prints n, t, the wrapping sum of
// the goroutines' x and the rounds whose context expired (0). Usage:
// scopedeadline <n> <t>.
package main

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"sync"
	"time"
)

func main() {
	n, err := strconv.Atoi(os.Args[1])
	if err != nil {
		os.Exit(2)
	}
	t, err := strconv.Atoi(os.Args[2])
	if err != nil || t < 1 {
		os.Exit(2)
	}
	m := n / t
	xs := make([]uint64, t)
	es := make([]uint64, t)
	var wg sync.WaitGroup
	for k := 0; k < t; k++ {
		wg.Add(1)
		go func(k int) {
			defer wg.Done()
			var x, e uint64
			for j := 0; j < m; j++ {
				i := k*m + j
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				x = x*31 + uint64(i)
				if ctx.Err() != nil {
					e++
				}
				cancel()
			}
			xs[k] = x
			es[k] = e
		}(k)
	}
	wg.Wait()
	var sum, expired uint64
	for k := 0; k < t; k++ {
		sum += xs[k]
		expired += es[k]
	}
	fmt.Println(n, t, sum, expired)
}
