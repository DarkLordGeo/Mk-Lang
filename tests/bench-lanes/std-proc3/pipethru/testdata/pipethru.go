// pipethru, Go, os/exec: this program runs itself with "child n", which
// writes n bytes of a splitmix64 stream (each word's 8 bytes, little
// endian, from state 0) to its standard output in 64 KiB writes; the
// parent reads the child's stdout from cmd.StdoutPipe() into a 64 KiB
// buffer and prints the bytes read, their sum and the child's exit code.
// stdin and stderr are the null device. Usage: pipethru <n>.
package main

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
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
	if os.Args[1] == "child" {
		n, _ := strconv.Atoi(os.Args[2])
		b := make([]byte, chunk)
		fill(b, 0)
		for left := n; left > 0; {
			k := min(left, chunk)
			if _, err := os.Stdout.Write(b[:k]); err != nil {
				os.Exit(1)
			}
			left -= k
		}
		return
	}
	self, _ := os.Executable()
	cmd := exec.Command(self, "child", os.Args[1])
	r, err := cmd.StdoutPipe()
	if err != nil {
		os.Exit(3)
	}
	if err := cmd.Start(); err != nil {
		os.Exit(3)
	}
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
			os.Exit(4)
		}
	}
	code := 0
	if err := cmd.Wait(); err != nil {
		code = -1
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			code = ee.ExitCode()
		}
	}
	fmt.Printf("%d %d %d\n", total, sum, code)
}
