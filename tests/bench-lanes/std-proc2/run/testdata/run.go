// run, Go, os/exec: n runs of this program with "child" (which writes 64
// bytes to standard output and exits 0), standard output and standard
// error captured in buffers, standard input the null device, each waited
// for. Prints n, the bytes captured and the failed runs. Usage: run <n>.
package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strconv"
)

func main() {
	if os.Args[1] == "child" {
		os.Stdout.WriteString("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcde\n")
		return
	}
	n, _ := strconv.Atoi(os.Args[1])
	self, _ := os.Executable()
	total, fails := 0, 0
	for i := 0; i < n; i++ {
		var o, e bytes.Buffer
		cmd := exec.Command(self, "child")
		cmd.Stdout, cmd.Stderr = &o, &e
		if err := cmd.Run(); err != nil {
			fails++
			continue
		}
		total += o.Len() + e.Len()
	}
	fmt.Printf("%d %d %d\n", n, total, fails)
}
