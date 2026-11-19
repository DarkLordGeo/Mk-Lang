// log-stderr, Go: write R records of about 80 bytes, an access-log line
// with five integer fields, through the standard logger (log.Default(),
// which writes to os.Stderr, one write per record under its mutex, as Mk's
// log.Stderr), then print the records and the sum of the bytes fields.
// The flags are 0 and the prefix "INFO ", so each record is the bytes Mk
// writes: the default date and time would add work Mk's record does not
// do. Usage: log-stderr <records>.
package main

import (
	"fmt"
	"log"
	"os"
	"strconv"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: log-stderr <records>")
		os.Exit(2)
	}
	records, _ := strconv.Atoi(os.Args[1])
	log.SetFlags(0)
	log.SetPrefix("INFO ")
	sum := 0
	for i := range records {
		b := i * 7 % 100000
		log.Printf("request id=%d path=/api/v1/items/%d status=200 bytes=%d took_us=%d", i, i%1000, b, i%977)
		sum += b
	}
	fmt.Println(records, sum)
}
