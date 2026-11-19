// csv-write, Go: write R records of 8 fields (generated once with
// splitmix64 from seed 42, as the Mk program does) with encoding/csv's
// Writer into one bytes.Buffer, reset before each of reps reps; then read
// the last output back with encoding/csv's Reader (through a file) and print the records,
// the fields, the field bytes and the sum of the field bytes. Usage:
// csv-write <records> <reps>.
package main

import (
	"bufio"
	"bytes"
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"strconv"
)

type rng struct{ s uint64 }

func (r *rng) next() uint64 {
	r.s += 0x9E3779B97F4A7C15
	z := r.s
	z = (z ^ (z >> 30)) * 0xBF58476D1CE4E5B9
	z = (z ^ (z >> 27)) * 0x94D049BB133111EB
	return z ^ (z >> 31)
}

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: csv-write <records> <reps>")
		os.Exit(2)
	}
	records, _ := strconv.Atoi(os.Args[1])
	reps, _ := strconv.Atoi(os.Args[2])
	const alpha = "abcdefghijklmnopqrstuvwxyz0123456789 ,\"\n"
	r := rng{42}
	recs := make([][]string, records)
	for i := range recs {
		rec := make([]string, 8)
		for f := range rec {
			n := r.next() % 12
			b := make([]byte, n)
			for j := range b {
				b[j] = alpha[r.next()%40]
			}
			rec[f] = string(b)
		}
		recs[i] = rec
	}
	var buf bytes.Buffer
	for range reps {
		buf.Reset()
		w := csv.NewWriter(&buf)
		for _, rec := range recs {
			if err := w.Write(rec); err != nil {
				panic(err)
			}
		}
		w.Flush()
	}
	// The read-back goes through a file, as Mk's does.
	const tmp = "csv-write-go.tmp"
	if err := os.WriteFile(tmp, buf.Bytes(), 0o644); err != nil {
		panic(err)
	}
	defer os.Remove(tmp)
	f, err := os.Open(tmp)
	if err != nil {
		panic(err)
	}
	defer f.Close()
	rd := csv.NewReader(bufio.NewReaderSize(f, 65536))
	rd.FieldsPerRecord = -1
	rd.ReuseRecord = true
	var nr, nf, nb, sum int
	for {
		rec, err := rd.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			panic(err)
		}
		nr++
		for _, f := range rec {
			nf++
			nb += len(f)
			for i := 0; i < len(f); i++ {
				sum += int(f[i])
			}
		}
	}
	fmt.Println(nr, nf, nb, sum)
}
