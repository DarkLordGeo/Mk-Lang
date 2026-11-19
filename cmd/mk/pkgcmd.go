// Why: managing a package's dependencies is a family of small commands that
// share argument handling and exit statuses; their flags live together here.
//
// What: runNew, runInstall, runRemove, runUpdate, runTree and runWhy (mk new,
// install, remove, update, tree and why), and the helpers pkgArgs,
// pkgSession, pkgOpen, pkgFail and pkgDone.
//
// How: the logic is internal/driver/pkgcmd.go's; this file parses flags and
// prints. Each command registers itself here, so main.go's table has no rows
// for them. Exit 0 when the command did its work, 1 when it failed or the
// package does not check afterwards (its diagnostics on stderr), 2 on a usage
// error.

package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"mk/internal/driver"
)

func init() {
	register(command{"new", "new     <name>   a package <name>/ with pkg.mk and src/main.mk", runNew})
	register(command{"install", "install <short name> | <source>[@<version>] [--name n] [path]   add a dependency", runInstall})
	register(command{"remove", "remove  <name> [path]   remove a dependency and its pkg.sum lines", runRemove})
	register(command{"update", "update  [name] [--dry-run] [path]   raise requirements to the newest tag of their major", runUpdate})
	register(command{"tree", "tree    [path]   the build list as a tree", runTree})
	register(command{"why", "why     <name or source> [path]   the requirement paths to a package", runWhy})
}

func runNew(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("new", stderr, nil)
	if err := fs.Parse(args); err != nil {
		return usageErr(stderr, fs, err)
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(stderr, "usage: mk new <name>")
		return 2
	}
	wd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(stderr, "mk new: %v\n", err)
		return 1
	}
	dir, err := driver.NewPackage(wd, fs.Arg(0), driver.MinorOf(driver.Release))
	if err != nil {
		return pkgFail(stderr, "new", err)
	}
	fmt.Fprintf(stdout, "created %s\n", dir)
	return 0
}

// pkgArgs parses the shared flags (and extra's) and returns the
// positional arguments.
func pkgArgs(name string, args []string, extra func(fs *flag.FlagSet), stderr io.Writer) ([]string, *compileFlags, int) {
	var cf compileFlags
	fs := newFlagSet(name, stderr, &cf)
	if extra != nil {
		extra(fs)
	}
	var pos []string
	rest := args
	for {
		if err := fs.Parse(rest); err != nil {
			return nil, nil, usageErr(stderr, fs, err)
		}
		if fs.NArg() == 0 {
			break
		}
		pos = append(pos, fs.Arg(0))
		rest = fs.Args()[1:]
	}
	return pos, &cf, -1
}

// pkgSession parses the arguments (want positional ones, then an optional
// package path) and opens the session; a nil session comes with the exit
// status.
func pkgSession(name string, args []string, want int, extra func(fs *flag.FlagSet), stderr io.Writer) (*driver.Session, []string, *compileFlags, int) {
	pos, cf, st := pkgArgs(name, args, extra, stderr)
	if cf == nil {
		return nil, nil, nil, st
	}
	if len(pos) < want || len(pos) > want+1 {
		fmt.Fprintf(stderr, "mk %s: want %d argument(s) and an optional package path, got %d\n", name, want, len(pos))
		return nil, nil, nil, 2
	}
	path := ""
	if len(pos) > want {
		path = pos[want]
	}
	s, st := pkgOpen(name, cf, path, stderr)
	return s, pos[:want], cf, st
}

// pkgOpen opens the session of the package at path.
func pkgOpen(name string, cf *compileFlags, path string, stderr io.Writer) (*driver.Session, int) {
	cfg, ok := cf.config(path, stderr)
	if !ok {
		return nil, 2
	}
	cfg.GenView = false
	s, err := driver.NewSession(cfg)
	if err != nil {
		fmt.Fprintf(stderr, "mk %s: %v\n", name, err)
		return nil, 2
	}
	return s, 0
}

// pkgFail reports err: a usage error exits 2, any other 1.
func pkgFail(stderr io.Writer, name string, err error) int {
	fmt.Fprintf(stderr, "mk %s: %v\n", name, err)
	var ue *driver.UsageError
	if errors.As(err, &ue) {
		return 2
	}
	return 1
}

// pkgDone prints the result's lines, then the check's diagnostics.
func pkgDone(res driver.PkgResult, cf *compileFlags, stdout, stderr io.Writer) int {
	for _, l := range res.Lines {
		fmt.Fprintln(stdout, l)
	}
	if len(res.Check.Report.Diags) > 0 {
		_ = res.Check.WriteReport(stderr, cf.json)
		return 1
	}
	return 0
}

func runInstall(args []string, stdout, stderr io.Writer) int {
	var name string
	s, pos, cf, st := pkgSession("install", args, 1, func(fs *flag.FlagSet) {
		fs.StringVar(&name, "name", "", "the local name (default: the source's last element)")
	}, stderr)
	if s == nil {
		return st
	}
	res, err := s.Install(pos[0], driver.PkgOptions{Name: name})
	if err != nil {
		return pkgFail(stderr, "install", err)
	}
	// A missing grant is printed, never added: the install itself worked.
	for _, l := range res.Lines {
		fmt.Fprintln(stdout, l)
	}
	for _, d := range res.Check.Report.Diags {
		if d.Diag.Code.String() != "E0215" {
			_ = res.Check.WriteReport(stderr, cf.json)
			return 1
		}
	}
	return 0
}

func runRemove(args []string, stdout, stderr io.Writer) int {
	s, pos, cf, st := pkgSession("remove", args, 1, nil, stderr)
	if s == nil {
		return st
	}
	res, err := s.Remove(pos[0])
	if err != nil {
		return pkgFail(stderr, "remove", err)
	}
	return pkgDone(res, cf, stdout, stderr)
}

func runUpdate(args []string, stdout, stderr io.Writer) int {
	var dry bool
	pos, cf, st := pkgArgs("update", args, func(fs *flag.FlagSet) {
		fs.BoolVar(&dry, "dry-run", false, "print the raised requirements; write nothing")
	}, stderr)
	if cf == nil {
		return st
	}
	// [name] [path]: one argument is a path when it names a directory or a
	// .mk file, else a name.
	name, path := "", ""
	switch len(pos) {
	case 0:
	case 1:
		if fi, err := os.Stat(pos[0]); err == nil && (fi.IsDir() || strings.HasSuffix(pos[0], ".mk")) {
			path = pos[0]
		} else {
			name = pos[0]
		}
	case 2:
		name, path = pos[0], pos[1]
	default:
		fmt.Fprintln(stderr, "usage: mk update [name] [--dry-run] [path]")
		return 2
	}
	s, st := pkgOpen("update", cf, path, stderr)
	if s == nil {
		return st
	}
	res, err := s.Update(name, driver.PkgOptions{DryRun: dry})
	if err != nil {
		return pkgFail(stderr, "update", err)
	}
	return pkgDone(res, cf, stdout, stderr)
}

func runTree(args []string, stdout, stderr io.Writer) int {
	s, _, cf, st := pkgSession("tree", args, 0, nil, stderr)
	if s == nil {
		return st
	}
	res, err := s.Tree()
	if err != nil {
		return pkgFail(stderr, "tree", err)
	}
	return pkgDone(res, cf, stdout, stderr)
}

func runWhy(args []string, stdout, stderr io.Writer) int {
	s, pos, cf, st := pkgSession("why", args, 1, nil, stderr)
	if s == nil {
		return st
	}
	res, err := s.Why(pos[0])
	if err != nil {
		return pkgFail(stderr, "why", err)
	}
	return pkgDone(res, cf, stdout, stderr)
}
