// grep-j8, Go, std: the task of README.md, app/grep's re variant searched
// by -j goroutines, with os.ReadDir (entries sorted by name, bytewise),
// os.ReadFile, utf8.Valid and regexp. The walk is mgrep's and lists every
// file first: each directory's entries in byte order, files and directories
// interleaved, depth first, names starting with "." skipped. Then -j worker
// goroutines take file indexes from a channel; each reads its file whole,
// skips it as binary when a NUL byte is in its first 1,024 bytes, and counts
// the lines that hold a match into the file's slot. A file that is valid
// UTF-8 is searched whole: the next match from the current line's start is
// found, its line counted, and the search goes on at the next line. A file
// that is not has its lines judged one by one, and a line that is not valid
// UTF-8 is skipped. When every worker is done, "path:count" is printed for
// every file not skipped, in walk order. -i is regexp's (?i).
// Usage: grepj8 -c -j N --path-separator / -i PATTERN DIR.
package main

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"
)

const sniff = 1024

type file struct {
	path, shown string
}

// result is one file's outcome: its count, or that it was skipped as
// binary, or the error that stopped it.
type result struct {
	count  int
	binary bool
	err    error
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: grepj8 -c -j N --path-separator / -i PATTERN DIR")
	os.Exit(2)
}

func main() {
	args := os.Args[1:]
	var fold, count bool
	threads := 0
	var rest []string
	for i := 0; i < len(args); i++ {
		switch a := args[i]; a {
		case "-c":
			count = true
		case "-i":
			fold = true
		case "-j":
			i++
			if i == len(args) {
				usage()
			}
			n, err := strconv.Atoi(args[i])
			if err != nil || n < 1 || n > 256 {
				usage()
			}
			threads = n
		case "--path-separator":
			i++
			if i == len(args) || args[i] != "/" {
				usage()
			}
		default:
			if strings.HasPrefix(a, "-") {
				usage()
			}
			rest = append(rest, a)
		}
	}
	if !count || threads == 0 || len(rest) != 2 {
		usage()
	}
	pat, root := rest[0], rest[1]
	if fold {
		pat = "(?i)" + pat
	}
	re, err := regexp.Compile(pat)
	if err != nil {
		fmt.Fprintf(os.Stderr, "grepj8: %v\n", err)
		os.Exit(2)
	}
	var files []file
	failed := walk(&files, root, strings.ReplaceAll(root, `\`, "/"))
	results := make([]result, len(files))
	jobs := make(chan int, 64)
	var wg sync.WaitGroup
	for range threads {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				results[i] = search(re, files[i].path)
			}
		}()
	}
	for i := range files {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	w := bufio.NewWriterSize(os.Stdout, 64<<10)
	matched := false
	for i, r := range results {
		switch {
		case r.err != nil:
			fmt.Fprintf(os.Stderr, "grepj8: %s: %v\n", files[i].shown, r.err)
			failed = true
		case !r.binary:
			matched = matched || r.count > 0
			fmt.Fprintf(w, "%s:%d\n", files[i].shown, r.count)
		}
	}
	if err := w.Flush(); err != nil {
		fmt.Fprintf(os.Stderr, "grepj8: %v\n", err)
		os.Exit(2)
	}
	switch {
	case failed:
		os.Exit(2)
	case !matched:
		os.Exit(1)
	}
}

// joined is the printed name of leaf inside the directory printed as name.
func joined(name, leaf string) string {
	if strings.HasSuffix(name, "/") || strings.HasSuffix(name, `\`) {
		return name + leaf
	}
	return name + "/" + leaf
}

// walk lists every file under dir, printed as name, in mgrep's order, and
// reports whether a directory could not be listed.
func walk(files *[]file, dir, name string) (failed bool) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "grepj8: %s: %v\n", name, err)
		return true
	}
	for _, e := range entries {
		leaf := e.Name()
		if strings.HasPrefix(leaf, ".") {
			continue
		}
		path := dir + string(os.PathSeparator) + leaf
		shown := joined(name, leaf)
		if e.IsDir() {
			failed = walk(files, path, shown) || failed
			continue
		}
		*files = append(*files, file{path, shown})
	}
	return failed
}

// search reads one file and counts its lines that hold a match.
func search(re *regexp.Regexp, path string) result {
	buf, err := os.ReadFile(path)
	if err != nil {
		return result{err: err}
	}
	if bytes.IndexByte(buf[:min(len(buf), sniff)], 0) >= 0 {
		return result{binary: true}
	}
	if utf8.Valid(buf) {
		return result{count: lines(re, buf)}
	}
	n := 0
	for len(buf) > 0 {
		line := buf
		next := len(buf)
		if i := bytes.IndexByte(buf, '\n'); i >= 0 {
			line, next = buf[:i], i+1
		}
		if k := len(line); k > 0 && line[k-1] == '\r' {
			line = line[:k-1]
		}
		if utf8.Valid(line) && lines(re, line) > 0 {
			n++
		}
		buf = buf[next:]
	}
	return result{count: n}
}

// lines counts the lines of buf that hold a match: the next match from the
// start of the current line, then on from the line after it. The row's
// pattern matches no line break, so a match never spans two lines.
func lines(re *regexp.Regexp, buf []byte) int {
	n, pos := 0, 0
	for pos < len(buf) {
		loc := re.FindIndex(buf[pos:])
		if loc == nil {
			break
		}
		at := pos + loc[0]
		n++
		j := bytes.IndexByte(buf[at:], '\n')
		if j < 0 {
			break
		}
		pos = at + j + 1
	}
	return n
}
