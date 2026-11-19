// chan, Go, idiomatic std: the task of README.md with two goroutines and two
// chan int64 of capacity 1: ping sends 0..n-1 on a and waits for each to come
// back on b; pong receives on a and sends the value back on b. They run on
// GOMAXPROCS threads (tools/bench sets it to the row's thread count). Usage:
// chan <n>.
package main

import (
	"fmt"
	"os"
	"strconv"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: chan <n>")
		os.Exit(2)
	}
	n, _ := strconv.ParseInt(os.Args[1], 10, 64)
	a := make(chan int64, 1)
	b := make(chan int64, 1)
	go func() {
		for range n {
			b <- <-a
		}
	}()
	var sum int64
	for i := range n {
		a <- i
		sum += <-b
	}
	fmt.Println(sum)
}
