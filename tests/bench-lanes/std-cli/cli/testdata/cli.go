// cli, Go: the standard library's flag.FlagSet over one generated argument
// vector of n arguments (README.md), parsed reps times. The FlagSet and its
// five flags are defined once, as Mk's gen cli.Args builds its parser at
// compile time; Parse runs per rep. Prints the checksum. Built with
// GOAMD64=v2 -pgo=off.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
)

func main() {
	n, err := strconv.Atoi(os.Args[1])
	if err != nil {
		panic(err)
	}
	reps, err := strconv.Atoi(os.Args[2])
	if err != nil {
		panic(err)
	}
	argv := make([]string, 0, n)
	for c := 0; c < n/10; c++ {
		argv = append(argv, "--verbose", "--count="+strconv.Itoa(c), "--name", "n"+strconv.Itoa(c),
			"--level", strconv.Itoa(c*3), "--tag=t"+strconv.Itoa(c))
	}
	for j := 0; len(argv) < n; j++ {
		argv = append(argv, "f"+strconv.Itoa(j))
	}
	fs := flag.NewFlagSet("cli", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	verbose := fs.Bool("verbose", false, "")
	count := fs.Int("count", 0, "")
	name := fs.String("name", "", "")
	level := fs.Int("level", 0, "")
	tag := fs.String("tag", "", "")
	var total uint64
	for r := 0; r < reps; r++ {
		if err := fs.Parse(argv); err != nil {
			panic(err)
		}
		files := fs.Args()
		s := uint64(*count + *level + len(*name) + len(*tag) + len(files))
		if *verbose {
			s++
		}
		if len(files) > 0 {
			s += uint64(len(files[r%len(files)]))
		}
		total += s
	}
	fmt.Println(total)
}
