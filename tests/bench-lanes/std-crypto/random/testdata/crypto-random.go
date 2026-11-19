// crypto-random, Go: crypto/rand.Read into one buffer of <size> bytes, <n>
// times, as mk/crypto-random.mk's comment says. Outside FIPS mode it reads
// the OS source on every call (crypto/internal/sysrand: ProcessPrng on
// Windows), as Mk. Built with GOAMD64=v2 -pgo=off.
package main

import (
	"crypto/rand"
	"fmt"
	"os"
	"strconv"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: crypto-random <n> <size>")
		os.Exit(2)
	}
	n, _ := strconv.Atoi(os.Args[1])
	size, _ := strconv.Atoi(os.Args[2])
	buf := make([]byte, size)
	var total uint64
	for i := 0; i < n; i++ {
		rand.Read(buf)
		total += uint64(len(buf))
	}
	fmt.Println(total)
}
