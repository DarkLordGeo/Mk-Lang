// spawn, Go, os/exec: n runs of this program with "child" (which exits 0
// at once), standard streams the null device, each waited for. Prints n,
// the sum of the exit codes and the failed spawns. Usage: spawn <n>.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
)

func main() {
	if os.Args[1] == "child" {
		return
	}
	n, _ := strconv.Atoi(os.Args[1])
	self, _ := os.Executable()
	sum, fails := 0, 0
	for i := 0; i < n; i++ {
		err := exec.Command(self, "child").Run()
		if ee, ok := err.(*exec.ExitError); ok {
			sum += ee.ExitCode()
		} else if err != nil {
			fails++
		}
	}
	fmt.Printf("%d %d %d\n", n, sum, fails)
}
