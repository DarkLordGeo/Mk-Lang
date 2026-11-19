// Why: editors talk to Mk through the Language Server Protocol, and the
// server must ship inside the one `mk` binary so that installing Mk installs
// it, with std found without setup.
//
// What: the `mk lsp` command: flag parsing, the help text that tells an
// editor how to start the server, `--check-capabilities`, and the hand-off
// to lsp.Serve over standard input and output.
//
// How: the server speaks LSP 3.17 over stdio only. std comes from --std,
// else MK_STD, else the copy embedded in the binary, so a `go build
// ./cmd/mk` binary works anywhere. Exit 0 after shutdown and exit (and for
// --help), 1 when the stream ends or exit comes first, 2 on a usage error.

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"mk/internal/lsp"
)

func init() {
	register(command{"lsp", "lsp     [--std dir] [--cache dir] [--log file] [--check-capabilities]   language server over stdio (LSP 3.17)", runLSP})
}

// lspStdin is the server's input; tests replace it.
var lspStdin io.Reader = os.Stdin

// lspHelp is the head of `mk lsp --help`; the flag list follows it.
const lspHelp = `usage: mk lsp [--stdio] [--std dir] [--cache dir] [--log file]
       mk lsp --check-capabilities

mk lsp is the Mk language server. It speaks the Language Server Protocol
3.17 over standard input and output, which is the only transport. An editor
starts it as

    mk lsp

with no arguments (--stdio is accepted and changes nothing), for files with
the .mk extension and the language id "mk". The standard library is built
into mk, so no further setup is needed; --std or MK_STD point the server at
another std tree. Checks share the module cache (--cache, else MK_CACHE,
else the per-user cache).

flags:
`

func runLSP(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("mk lsp", flag.ContinueOnError)
	// Parse writes the usage itself on -h; it goes to stdout for help and
	// to stderr for a usage error, once.
	var usage bytes.Buffer
	fs.SetOutput(&usage)
	var opt lsp.Options
	var logPath string
	fs.StringVar(&opt.StdDir, "std", "", "the standard library directory (default MK_STD, else the std built into mk)")
	fs.StringVar(&opt.CacheDir, "cache", "", "the module cache directory (default MK_CACHE, else the per-user cache)")
	fs.StringVar(&logPath, "log", "", "append one line per message and its wall time to this file")
	var caps bool
	fs.BoolVar(&caps, "check-capabilities", false, "print the server's capabilities as JSON and exit")
	fs.Bool("stdio", true, "speak LSP over standard input and output (the only transport, the default)")
	fs.Usage = func() {
		fmt.Fprint(fs.Output(), lspHelp)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			stdout.Write(usage.Bytes())
			return 0
		}
		stderr.Write(usage.Bytes())
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "mk lsp: unexpected argument %q\n", fs.Arg(0))
		return 2
	}
	if opt.CacheDir == "" {
		opt.CacheDir = userCacheDir()
	}
	if caps {
		b, _ := json.MarshalIndent(lsp.Capabilities("utf-16"), "", "  ")
		fmt.Fprintln(stdout, string(b))
		return 0
	}
	if logPath != "" {
		f, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			fmt.Fprintf(stderr, "mk lsp: %v\n", err)
			return 2
		}
		defer f.Close()
		opt.Log = f
	}
	if err := lsp.Serve(lspStdin, stdout, opt); err != nil {
		fmt.Fprintf(stderr, "mk lsp: %v\n", err)
		return 1
	}
	return 0
}
