// Why: the build cache and profile files are the user's to inspect, trim
// and combine, and every command must agree on where the cache lives.
//
// What: userCacheDir and userCacheRoot (the per-user cache location),
// runCache (mk cache size | clean), runCachePrune (mk cache prune), and runProfile
// (mk profile merge -o out.mkprof in.mkprof...).
//
// How: MK_CACHE wins; else the platform's per-user cache directory under an
// mk subdirectory. Only the CLI applies it, so in-process tests keep the
// driver's <package root>/.mk-cache.

package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"

	"mk/internal/driver"
)

// userCacheDir is the cache directory of every command that builds, of mk
// cache and of mk lsp: one cache per user. MK_CACHE
// when set; else the per-user root (userCacheRoot); "" when no user
// directory is known, and then the driver's own default,
// <package root>/.mk-cache. Only the CLI applies it: driver.Config's empty
// CacheDir stays <package root>/.mk-cache, so no in-process test reaches a
// user's cache.
func userCacheDir() string {
	if d := os.Getenv("MK_CACHE"); d != "" {
		return d
	}
	return userCacheRoot(runtime.GOOS, os.Getenv)
}

// userCacheRoot is the per-user cache root on goos: %LOCALAPPDATA%\mk\cache
// on Windows (a subdirectory, because the installed toolchains\ and current
// live in %LOCALAPPDATA%\mk and mk cache clean must
// never reach them), else $XDG_CACHE_HOME/mk (an absolute XDG_CACHE_HOME
// only, as the XDG rules say), else ~/.cache/mk. "" when the variable it
// needs is unset.
func userCacheRoot(goos string, getenv func(string) string) string {
	if goos == "windows" {
		if d := getenv("LOCALAPPDATA"); d != "" {
			return filepath.Join(d, "mk", "cache")
		}
		return ""
	}
	if d := getenv("XDG_CACHE_HOME"); d != "" && filepath.IsAbs(d) {
		return filepath.Join(d, "mk")
	}
	if h := getenv("HOME"); h != "" {
		return filepath.Join(h, ".cache", "mk")
	}
	return ""
}

// runCache is `mk cache size [path]` and `mk cache clean [path]`. The cache
// is the one a build of path would use: MK_CACHE, else the per-user cache
// (userCacheDir), else <package root>/.mk-cache.
func runCache(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("mk cache", flag.ContinueOnError)
	fs.SetOutput(stderr)
	if len(args) > 0 && args[0] == "prune" {
		return runCachePrune(args[1:], stdout, stderr)
	}
	if len(args) == 0 || (args[0] != "size" && args[0] != "clean") {
		fmt.Fprintln(stderr, "usage: mk cache size | clean [path] | prune --packages <dir>...")
		return 2
	}
	sub := args[0]
	path, _, err := parseArgs(fs, args[1:], false)
	if err != nil {
		return usageErr(stderr, fs, err)
	}
	dir, err := driver.CacheDirFor(path, userCacheDir())
	if err != nil {
		fmt.Fprintf(stderr, "mk cache: %v\n", err)
		return 2
	}
	if sub == "clean" {
		if err := driver.CacheClean(dir); err != nil {
			fmt.Fprintf(stderr, "mk cache clean: %v\n", err)
			return 1
		}
		return 0
	}
	rep, err := driver.CacheSize(dir)
	if err != nil {
		fmt.Fprintf(stderr, "mk cache size: %v\n", err)
		return 1
	}
	fmt.Fprint(stdout, rep.Text())
	return 0
}

// runProfile is `mk profile merge -o out in...`: the merged profile is
// sorted by function key, so it does not depend on the order of the inputs.
func runProfile(args []string, _, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "merge" {
		fmt.Fprintln(stderr, "usage: mk profile merge -o out.mkprof in.mkprof...")
		return 2
	}
	fs := flag.NewFlagSet("mk profile merge", flag.ContinueOnError)
	fs.SetOutput(stderr)
	out := fs.String("o", "", "the merged profile")
	var ins []string
	rest := args[1:]
	for {
		if err := fs.Parse(rest); err != nil {
			return usageErr(stderr, fs, err)
		}
		if fs.NArg() == 0 {
			break
		}
		ins = append(ins, fs.Arg(0))
		rest = fs.Args()[1:]
	}
	if *out == "" || len(ins) == 0 {
		fmt.Fprintln(stderr, "usage: mk profile merge -o out.mkprof in.mkprof...")
		return 2
	}
	warns, err := driver.MergeProfiles(*out, ins)
	if err != nil {
		fmt.Fprintf(stderr, "mk profile merge: %v\n", err)
		return 1
	}
	for _, w := range warns {
		fmt.Fprintf(stderr, "mk profile merge: warning: %s\n", w)
	}
	return 0
}

// runCachePrune is `mk cache prune --packages <dir>...`: the package trees of
// the cache a build of the current directory would use that no pkg.sum under
// the named directories refers to are removed, one line per tree.
func runCachePrune(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("mk cache prune", flag.ContinueOnError)
	fs.SetOutput(stderr)
	packages := fs.Bool("packages", false, "remove package trees no pkg.sum under the named directories refers to")
	if err := fs.Parse(args); err != nil {
		return usageErr(stderr, fs, err)
	}
	if !*packages || fs.NArg() == 0 {
		fmt.Fprintln(stderr, "usage: mk cache prune --packages <dir>...")
		return 2
	}
	dir, err := driver.CacheDirFor("", userCacheDir())
	if err != nil {
		fmt.Fprintf(stderr, "mk cache prune: %v\n", err)
		return 2
	}
	removed, err := driver.PrunePackages(dir, fs.Args())
	if err != nil {
		fmt.Fprintf(stderr, "mk cache prune: %v\n", err)
		return 1
	}
	for _, r := range removed {
		fmt.Fprintf(stdout, "removed %s\n", r)
	}
	return 0
}
