// pipepair, Go, os.Pipe: one process, one pipe; a goroutine writes n
// bytes of a splitmix64 stream (each word's 8 bytes, little endian, from
// state 0) to the write end, 64 KiB at a time, then closes it; the main
// goroutine reads the read end into a 64 KiB buffer and prints the bytes
// read and their sum. Usage: pipepair <n>.
package main

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
)

const chunk = 65536

func fill(b []byte, x uint64) uint64 {
	for i := 0; i+8 <= len(b); i += 8 {
		x += 0x9e3779b97f4a7c15
		z := x
		z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
		z = (z ^ (z >> 27)) * 0x94d049bb133111eb
		z ^= z >> 31
		binary.LittleEndian.PutUint64(b[i:], z)
	}
	return x
}

func main() {
	n, _ := strconv.Atoi(os.Args[1])
	r, w, err := os.Pipe()
	if err != nil {
		os.Exit(3)
	}
	go func() {
		b := make([]byte, chunk)
		fill(b, 0)
		for left := n; left > 0; {
			k := min(left, chunk)
			if _, err := w.Write(b[:k]); err != nil {
				os.Exit(4)
			}
			left -= k
		}
		w.Close()
	}()
	b := make([]byte, chunk)
	var total, sum uint64
	for {
		k, err := r.Read(b)
		for _, v := range b[:k] {
			sum += uint64(v)
		}
		total += uint64(k)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			os.Exit(5)
		}
	}
	fmt.Printf("%d %d\n", total, sum)
}
