// trysend, Go, idiomatic std: a select with a default case sends on a
// chan int64 of capacity 1,024 without waiting, on one goroutine
// (README.md): reps rounds of 1,040 tries, the last 16 finding the channel
// full and keeping the value, then 1,024 receives. The checksum adds every
// value received and every value kept.
// Usage: trysend <reps>.
package main

import (
	"fmt"
	"os"
	"strconv"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: trysend <reps>")
		os.Exit(2)
	}
	reps, _ := strconv.Atoi(os.Args[1])
	c := make(chan int64, 1024)
	var acc, v int64
	for r := 0; r < reps; r++ {
		for k := 0; k < 1040; k++ {
			v++
			select {
			case c <- v:
			default:
				acc += v
			}
		}
		for k := 0; k < 1024; k++ {
			acc += <-c
		}
	}
	fmt.Println(acc)
}
