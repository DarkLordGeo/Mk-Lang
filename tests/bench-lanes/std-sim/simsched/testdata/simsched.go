// simsched, Go: the same job as ../mk/simsched.mk in a testing/synctest
// bubble (Go 1.25): n goroutines each sleep 1 ms of the bubble's fake
// clock and send their index to one channel, r times; the bubble's main
// goroutine receives all n * r values. The fake clock jumps when every
// goroutine of the bubble is durably blocked, as std.sim's does when no
// task can run. synctest.Test needs a *testing.T, so main runs one test
// through testing.Main with the testing package's own output sent to the
// null device. Usage: simsched <tasks> <rounds>.
package main

import (
	"fmt"
	"os"
	"strconv"
	"testing"
	"testing/synctest"
	"time"
)

func main() {
	n, _ := strconv.Atoi(os.Args[1])
	r, _ := strconv.Atoi(os.Args[2])
	stdout := os.Stdout
	if null, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0); err == nil {
		os.Stdout = null
	}
	run := func(t *testing.T) {
		var sum int
		var elapsed time.Duration
		synctest.Test(t, func(t *testing.T) {
			ch := make(chan int, 1)
			start := time.Now()
			for i := 0; i < n; i++ {
				go func(i int) {
					for k := 0; k < r; k++ {
						time.Sleep(time.Millisecond)
						ch <- i
					}
				}(i)
			}
			for k := 0; k < n*r; k++ {
				sum += <-ch
			}
			elapsed = time.Since(start)
		})
		fmt.Fprintf(stdout, "sum %d simulated %d ms\n", sum, elapsed.Milliseconds())
	}
	os.Args = os.Args[:1]
	testing.Main(func(pat, str string) (bool, error) { return true, nil },
		[]testing.InternalTest{{Name: "TestSimSched", F: run}}, nil, nil)
}
