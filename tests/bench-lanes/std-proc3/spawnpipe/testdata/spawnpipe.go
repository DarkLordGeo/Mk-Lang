// spawnpipe, Go, os/exec: n runs of this program with "child" (which
// copies standard input to standard output); each run gets 64 bytes on a
// stdin pipe, which is then closed, its stdout read to the end from a pipe,
// stderr the null device, and its exit waited for. Prints n, the bytes
// read back and the failed runs. Usage: spawnpipe <n>.
package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
)

const payload = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcde\n"

func once(self string) int {
	cmd := exec.Command(self, "child")
	w, err := cmd.StdinPipe()
	if err != nil {
		return -1
	}
	r, err := cmd.StdoutPipe()
	if err != nil {
		return -1
	}
	if err := cmd.Start(); err != nil {
		return -1
	}
	io.WriteString(w, payload)
	w.Close()
	b, _ := io.ReadAll(r)
	if err := cmd.Wait(); err != nil {
		return -1
	}
	return len(b)
}

func main() {
	if os.Args[1] == "child" {
		io.Copy(os.Stdout, os.Stdin)
		return
	}
	n, _ := strconv.Atoi(os.Args[1])
	self, _ := os.Executable()
	total, fails := 0, 0
	for i := 0; i < n; i++ {
		k := once(self)
		if k != 64 {
			fails++
			continue
		}
		total += k
	}
	fmt.Printf("%d %d %d\n", n, total, fails)
}
