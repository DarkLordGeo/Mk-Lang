// csv-read, Go: read a log file (tools/bench gen kind log) as CSV records
// with encoding/csv, Comma ' ', ReuseRecord and FieldsPerRecord -1, over
// bufio.Reader (64 KiB) over an os.File, r times; prints the records, the
// fields and the field bytes. Usage: csv-read <file> <r>.
package main

import (
	"bufio"
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"strconv"
)

func read(p string) (records, fields, bytes int64) {
	f, err := os.Open(p)
	if err != nil {
		panic(err)
	}
	defer f.Close()
	r := csv.NewReader(bufio.NewReaderSize(f, 65536))
	r.Comma = ' '
	r.ReuseRecord = true
	r.FieldsPerRecord = -1
	for {
		rec, err := r.Read()
		if err == io.EOF {
			return
		}
		if err != nil {
			panic(err)
		}
		records++
		fields += int64(len(rec))
		for _, s := range rec {
			bytes += int64(len(s))
		}
	}
}

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: csv-read <file> <r>")
		os.Exit(2)
	}
	reps, _ := strconv.Atoi(os.Args[2])
	var records, fields, bytes int64
	for range reps {
		a, b, c := read(os.Args[1])
		records += a
		fields += b
		bytes += c
	}
	fmt.Println(records, fields, bytes)
}
