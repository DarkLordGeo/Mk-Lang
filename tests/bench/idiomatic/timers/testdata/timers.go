// timers, Go, idiomatic std: the task of README.md with Go's timers. Mode
// sleep: per round n goroutines (sync.WaitGroup.Go), goroutine i sleeping
// 1 to 50 ms with time.Sleep, then adding i to one atomic.Int64 if
// time.Since shows it slept at least half its time. Mode deadline: n
// contexts one after another, each made by context.WithTimeout, Go's peer
// of a scope's deadline, and cancelled at once: 60 s, which the cancel
// stops, or 0 s on every 1000th, already expired; prints those whose Err
// was nil plus 3 times those whose Err was DeadlineExceeded, before the
// cancel. They run on GOMAXPROCS threads (tools/bench sets the
// row's thread count). Usage: timers sleep <n> <rounds> <seed> | timers
// deadline <n>.
package main

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

func mix(z uint64) uint64 {
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
	z = (z ^ (z >> 27)) * 0x94d049bb133111eb
	return z ^ (z >> 31)
}

func sleeps(n, rounds int64, seed uint64) int64 {
	st := seed
	var total int64
	for range rounds {
		var sum atomic.Int64
		var wg sync.WaitGroup
		for i := range n {
			st += 0x9e3779b97f4a7c15
			ms := int64(mix(st)%50) + 1
			wg.Go(func() {
				d := time.Duration(ms) * time.Millisecond
				t0 := time.Now()
				time.Sleep(d)
				if time.Since(t0) >= d/2 {
					sum.Add(i)
				}
			})
		}
		wg.Wait()
		total += sum.Load()
	}
	return total
}

func deadlines(n int64) int64 {
	var disarmed, fired int64
	bg := context.Background()
	for i := range n {
		d := 60 * time.Second
		if i%1000 == 999 {
			d = 0
		}
		ctx, cancel := context.WithTimeout(bg, d)
		switch ctx.Err() {
		case nil:
			disarmed++
		case context.DeadlineExceeded:
			fired++
		}
		cancel()
	}
	return disarmed + 3*fired
}

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "usage: timers sleep <n> <rounds> <seed> | timers deadline <n>")
		os.Exit(2)
	}
	n, _ := strconv.ParseInt(os.Args[2], 10, 64)
	var total int64
	if os.Args[1] == "sleep" {
		rounds, _ := strconv.ParseInt(os.Args[3], 10, 64)
		seed, _ := strconv.ParseUint(os.Args[4], 10, 64)
		total = sleeps(n, rounds, seed)
	} else {
		total = deadlines(n)
	}
	fmt.Println(total)
}
