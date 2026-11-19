// log-sink, Go: write R records of about 100 bytes, an access-log line
// with five integer fields, through a log.Logger over a new *os.File (one
// write per record under the logger's mutex, as Mk's log.ToFile), then
// print the records and the file's size. Usage: log-sink <prefix> <records>;
// the file is <prefix>-go.log.
package main

import (
	"fmt"
	"log"
	"os"
	"strconv"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: log-sink <prefix> <records>")
		os.Exit(2)
	}
	records, _ := strconv.Atoi(os.Args[2])
	path := os.Args[1] + "-go.log"
	f, err := os.Create(path)
	if err != nil {
		panic(err)
	}
	l := log.New(f, "", 0)
	for i := range records {
		l.Printf("INFO request id=%d path=/api/v1/items/%d status=200 bytes=%d took_us=%d", i, i%1000, i*7%100000, i%977)
	}
	st, err := f.Stat()
	if err != nil {
		panic(err)
	}
	f.Close()
	os.Remove(path)
	fmt.Println(records, st.Size())
}
