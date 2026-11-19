// task-pingpong, Go: two goroutines pass a value back and forth over two
// unbuffered channels; each of n rounds sends i on ping, the partner
// answers i*3+1 on pong; sum = 31 sum + the answer (wrapping). Prints n,
// sum and 0. Usage: taskpingpong <n>.
package main

import (
	"fmt"
	"os"
	"strconv"
)

func main() {
	n, err := strconv.Atoi(os.Args[1])
	if err != nil {
		os.Exit(2)
	}
	ping := make(chan uint64)
	pong := make(chan uint64)
	go func() {
		for i := 0; i < n; i++ {
			v := <-ping
			pong <- v*3 + 1
		}
	}()
	var sum uint64
	for i := 0; i < n; i++ {
		ping <- uint64(i)
		sum = sum*31 + <-pong
	}
	fmt.Printf("%d %d 0\n", n, sum)
}
