// Why: editors and coding agents need short, exact answers about a package
// (a signature, a type, the panic sites) without parsing compiler output.
//
// What: runQuery, the `mk query <query> [args] [--json] [--root path]`
// command, and queryMemberPanics for `mk query panics --member name`.
//
// How: one process start, one Check of the package (a warm run is
// a cache hit), one answer capped near 200 tokens. Exit 0 with an answer
// (one from declarations alone, when the package does not check as far as
// types, ends with a "(partial: ...)" line), 1 when the subject is not found
// or there is nothing to answer from (its diagnostics on stderr), 2 on a
// usage error.

package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"mk/internal/driver"
)

func init() {
	register(command{"query", "query   <query> [args] [--json] [--root path]   ask the checked package", runQuery})
}

func runQuery(args []string, stdout, stderr io.Writer) int {
	var cf compileFlags
	fs := newFlagSet("query", stderr, &cf)
	root := fs.String("root", "", "the package root, a directory or a .mk file (default: the working directory)")
	member := fs.String("member", "", "panics only: every panic site of the named workspace member, never capped")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: mk query <query> [args] [--json] [--root path]; queries:")
		for _, q := range driver.QueryNames {
			fmt.Fprintln(stderr, "  "+q.Help)
		}
		fmt.Fprintln(stderr, "  panics --member name: every panic site of a member's modules, located, never capped")
	}
	var pos []string
	rest := args
	for {
		if err := fs.Parse(rest); err != nil {
			return usageErr(stderr, fs, err)
		}
		if fs.NArg() == 0 {
			break
		}
		pos = append(pos, fs.Arg(0))
		rest = fs.Args()[1:]
	}
	if len(pos) == 0 {
		fs.Usage()
		return 2
	}
	cfg, ok := cf.config(*root, stderr)
	if !ok {
		return 2
	}
	cfg.GenView = false // a query writes nothing
	if *member != "" {
		if len(pos) != 1 || pos[0] != "panics" {
			fmt.Fprintln(stderr, "mk query: --member goes with panics alone: mk query panics --member <name> [--json]")
			return 2
		}
		return queryMemberPanics(cfg, *member, cf.json, stdout, stderr)
	}
	s, err := driver.NewSession(cfg)
	var ue *driver.UsageError
	if err != nil && *root == "" && errors.As(err, &ue) && ue.NoSources {
		// No package here: answer std's names over a one-file root of
		// its own, removed afterwards.
		dir, derr := os.MkdirTemp("", "mk-query-")
		if derr == nil {
			defer os.RemoveAll(dir)
			cfg.Root = filepath.Join(dir, "main.mk")
			if derr = os.WriteFile(cfg.Root, []byte("export Main(sys: &System) {\n}\n"), 0o644); derr == nil {
				s, err = driver.NewSession(cfg)
			}
		}
	}
	if err != nil {
		fmt.Fprintf(stderr, "mk query: %v\n", err)
		return 2
	}
	ans, res, err := s.Query(pos[0], pos[1:])
	if err != nil {
		fmt.Fprintf(stderr, "mk query: %v\n", err)
		return 2
	}
	if ans == nil {
		_ = res.WriteReport(stderr, false)
		fmt.Fprintf(stderr, "mk query: the package does not check far enough to answer %s\n", strings.Join(pos, " "))
		return 1
	}
	if cf.json {
		_, err = stdout.Write(ans.JSON())
	} else {
		_, err = io.WriteString(stdout, ans.Text())
	}
	if err != nil {
		return 2
	}
	if !ans.Found {
		return 1
	}
	return 0
}

// queryMemberPanics is `mk query panics --member name`: every
// panic site of the member's modules, complete, as text or as the version
// 1 JSON document of the panic listing. Exit 0 with the listing, 1 when the
// member does not check cleanly (the listing, when there is one, still
// printed; the diagnostics on stderr), 2 on a usage error.
func queryMemberPanics(cfg driver.Config, member string, asJSON bool, stdout, stderr io.Writer) int {
	dir, name, err := driver.MemberDir(cfg, member)
	if err != nil {
		fmt.Fprintf(stderr, "mk query: %v\n", err)
		return 2
	}
	cfg.Root = dir
	s, err := driver.NewSession(cfg)
	if err != nil {
		fmt.Fprintf(stderr, "mk query: %v\n", err)
		return 2
	}
	l, res := s.PanicSites(name)
	if l != nil {
		if asJSON {
			_, err = stdout.Write(l.JSON())
		} else {
			_, err = io.WriteString(stdout, l.Text())
		}
		if err != nil {
			return 2
		}
	}
	if len(res.Report.Diags) > 0 || l == nil {
		_ = res.WriteReport(stderr, false)
		if l == nil {
			fmt.Fprintf(stderr, "mk query: member %s does not check far enough to list its panic sites\n", name)
		}
		return 1
	}
	return 0
}
