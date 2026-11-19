// Why: a toolchain release is a set of archives, their checksums and a
// signature; one tool makes and checks them so a release is reproducible
// and every step can be tested.
//
// What: the subcommand table and run: keygen, build, seed, sums, sign and
// verify, each in build.go, seed.go or sign.go.
//
// How: plain flag parsing per subcommand; exit 0 on success, 1 when verify
// finds a bad release, 2 on a usage or I/O error.

// Command release builds, signs and verifies toolchain releases. Go,
// standard library only; of the compiler it imports internal/release (and,
// for the seed, internal/fetch and internal/git).
//
//	go run ./tools/release keygen -out <file>
//	go run ./tools/release build -mk <exe> -llvm <dir> -version <v> -out <dir>
//	go run ./tools/release seed -kits <dir> -list <file> -out <dir>
//	go run ./tools/release sums -dir <dir>
//	go run ./tools/release sign -key <file> -dir <dir>
//	go run ./tools/release verify -dir <dir>
//
// Exit 0 on success, 1 when verify finds a bad release, 2 on a usage or
// I/O error.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

type subcommand struct {
	usage string
	run   func(args []string, stdout, stderr io.Writer) int
}

var subcommands = map[string]subcommand{
	"keygen": {"keygen -out <file>                 write a new Ed25519 key pair; print the public key for internal/release/keys.go", runKeygen},
	"build":  {"build -mk <exe> -llvm <dir> -version <v> -out <dir>   assemble the host's archive", runBuild},
	"seed":   {"seed -kits <dir> -list <file> -out <dir>   build seed/ from kits/ and the first-party list, checking each tag's tree hash", runSeed},
	"sums":   {"sums -dir <dir>                    write SHA256SUMS over the archives in dir", runSums},
	"sign":   {"sign -key <file> -dir <dir>        write SHA256SUMS.sig", runSign},
	"verify": {"verify -dir <dir>                  check SHA256SUMS.sig against the built-in keys and every archive", runVerify},
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		usage(stderr)
		return 2
	}
	if c, ok := subcommands[args[0]]; ok {
		return c.run(args[1:], stdout, stderr)
	}
	if args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		usage(stdout)
		return 0
	}
	fmt.Fprintf(stderr, "release: unknown subcommand %q\n", args[0])
	usage(stderr)
	return 2
}

func usage(w io.Writer) {
	fmt.Fprintln(w, "usage: go run ./tools/release <subcommand> [flags]")
	var names []string
	for n := range subcommands {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		fmt.Fprintln(w, "  "+subcommands[n].usage)
	}
}

// flags is a flag set that reports to stderr and refuses positional
// arguments.
func flags(name string, stderr io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet("release "+name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	return fs
}

func parse(fs *flag.FlagSet, args []string, stderr io.Writer, required ...string) bool {
	if err := fs.Parse(args); err != nil {
		return false
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "%s: unexpected argument %q\n", fs.Name(), fs.Arg(0))
		return false
	}
	for _, r := range required {
		if fs.Lookup(r).Value.String() == "" {
			fmt.Fprintf(stderr, "%s: -%s is required\n", fs.Name(), r)
			return false
		}
	}
	return true
}

func fail(stderr io.Writer, name string, err error) int {
	fmt.Fprintf(stderr, "release %s: %v\n", name, err)
	return 2
}
