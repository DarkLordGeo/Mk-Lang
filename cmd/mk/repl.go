// Why: trying an expression or a small function should not need a package
// and a build.
//
// What: runREPL and runREPLFrom, the `mk repl` command: the interpreter on
// the compile-time evaluator, and isTerminal.
//
// How: entries are read from stdin; values go to stdout and diagnostics to
// stderr. The logic is internal/repl; this file parses flags and wires the
// process's streams. Edited functions are not hot-swapped.

package main

import (
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"mk/internal/repl"
)

func init() {
	register(command{"repl", "repl    [--root dir] [--fuel steps] [--prompt]   evaluate entries on the evaluator", runREPL})
}

func runREPL(args []string, stdout, stderr io.Writer) int {
	return runREPLFrom(os.Stdin, args, stdout, stderr)
}

// runREPLFrom is runREPL over a given input, for tests.
func runREPLFrom(in io.Reader, args []string, stdout, stderr io.Writer) int {
	var cf compileFlags
	fs := newFlagSet("repl", stderr, &cf)
	root := fs.String("root", "", "the package directory, or a directory of .mk files (default: the working directory if it holds pkg.mk, else std alone)")
	fuel := fs.String("fuel", "", "the step limit of each evaluation (default: the language's, 4_000_000)")
	prompt := fs.String("prompt", "auto", "write prompts: on, off, or auto (on when stdin is a terminal)")
	if err := fs.Parse(args); err != nil {
		return usageErr(stderr, fs, err)
	}
	if fs.NArg() > 0 {
		return usageErr(stderr, fs, fmt.Errorf("unexpected argument %q (the root is --root)", fs.Arg(0)))
	}
	cfg, ok := cf.config("", stderr)
	if !ok {
		return 2
	}
	opt := repl.Options{Root: *root, Config: cfg}
	if *fuel != "" {
		v, err := strconv.ParseUint(strings.ReplaceAll(*fuel, "_", ""), 10, 64)
		if err != nil || v == 0 {
			return usageErr(stderr, fs, fmt.Errorf("--fuel takes a positive number of steps, not %q", *fuel))
		}
		opt.Fuel = v
	}
	switch *prompt {
	case "on":
		opt.Prompt = true
	case "off":
	case "auto":
		opt.Prompt = in == os.Stdin && isTerminal(os.Stdin)
	default:
		return usageErr(stderr, fs, fmt.Errorf("--prompt is on, off or auto, not %q", *prompt))
	}
	s, err := repl.New(opt)
	if err != nil {
		fmt.Fprintf(stderr, "mk repl: %v\n", err)
		return 2
	}
	defer s.Close()
	if in != os.Stdin {
		return s.Serve(in, stdout, stderr)
	}
	// Ctrl+C, a termination and a hang-up (the terminal closed) end the
	// session and remove its scratch file, as the end of input does (exit
	// 128 plus the signal's number: 130 for Ctrl+C). On Windows, os/signal
	// delivers a console close, logoff or shutdown as SIGTERM only when
	// SIGTERM is notified; otherwise the process ends with the file left in
	// the package.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(sig)
	return s.ServeUntil(in, stdout, stderr, sig)
}

// isTerminal reports whether f is a character device (a console), so the
// prompts are written for a person and left out for a pipe.
func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}
