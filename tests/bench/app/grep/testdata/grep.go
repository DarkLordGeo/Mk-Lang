// grep, Go, std: the task of README.md with os.ReadDir (entries sorted by
// name, bytewise), os.ReadFile, utf8.Valid, bytes.Index and regexp. The walk
// is mgrep's: each directory's entries in byte order, files and directories
// interleaved, depth first, names starting with "." skipped. A file with a
// NUL byte in its first 1,024 bytes is skipped as binary; every other file
// prints "path:count", the number of its lines that hold a match. A file
// that is valid UTF-8 is searched whole: the next match from the current
// line's start is found, its line counted, and the search goes on at the
// next line. A file that is not has its lines judged one by one, and a line
// that is not valid UTF-8 is skipped. -F, or a pattern with no regex meta
// byte, is a literal; -i on a literal folds ASCII case only (the haystack's
// ASCII letters are lowered into a second buffer), and -i on a regex is
// regexp's (?i).
// Usage: grep -c -j 1 --path-separator / [-F|-i] PATTERN DIR.
package main

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"regexp"
	"strings"
	"unicode/utf8"
)

const sniff = 1024

// meta are the bytes that make a pattern a regex (mgrep's MetaByte).
const meta = `\.+*?()|[]{}^$`

type searcher struct {
	re     *regexp.Regexp
	needle []byte
	fold   bool
	low    []byte // the lowered haystack of a folded literal, reused
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: grep -c -j 1 --path-separator / [-F|-i] PATTERN DIR")
	os.Exit(2)
}

func main() {
	args := os.Args[1:]
	var fixed, fold, count bool
	var rest []string
	for i := 0; i < len(args); i++ {
		switch a := args[i]; a {
		case "-c":
			count = true
		case "-F":
			fixed = true
		case "-i":
			fold = true
		case "-j":
			i++
			if i == len(args) || args[i] != "1" {
				usage()
			}
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
	if !count || len(rest) != 2 {
		usage()
	}
	pat, root := rest[0], rest[1]
	s := &searcher{}
	ascii := true
	for i := 0; i < len(pat); i++ {
		ascii = ascii && pat[i] < 0x80
	}
	if pat != "" && (fixed || !strings.ContainsAny(pat, meta)) && (!fold || ascii) {
		s.needle = []byte(pat)
		if fold {
			s.fold = true
			s.needle = lowerASCII(nil, s.needle)
		}
	} else {
		if fixed {
			pat = regexp.QuoteMeta(pat)
		}
		if fold {
			pat = "(?i)" + pat
		}
		re, err := regexp.Compile(pat)
		if err != nil {
			fmt.Fprintf(os.Stderr, "grep: %v\n", err)
			os.Exit(2)
		}
		s.re = re
	}
	w := bufio.NewWriterSize(os.Stdout, 64<<10)
	matched, failed := walk(w, s, root, display(root))
	if err := w.Flush(); err != nil {
		fmt.Fprintf(os.Stderr, "grep: %v\n", err)
		os.Exit(2)
	}
	switch {
	case failed:
		os.Exit(2)
	case !matched:
		os.Exit(1)
	}
}

// display is a root as printed: each separator written as "/".
func display(p string) string {
	return strings.ReplaceAll(p, `\`, "/")
}

// joined is the printed name of leaf inside the directory printed as name.
func joined(name, leaf string) string {
	if strings.HasSuffix(name, "/") || strings.HasSuffix(name, `\`) {
		return name + leaf
	}
	return name + "/" + leaf
}

// walk searches every file under dir, printed as name, in mgrep's order. It
// reports whether any line matched and whether anything failed.
func walk(w *bufio.Writer, s *searcher, dir, name string) (matched, failed bool) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "grep: %s: %v\n", name, err)
		return false, true
	}
	for _, e := range entries {
		leaf := e.Name()
		if strings.HasPrefix(leaf, ".") {
			continue
		}
		path := dir + string(os.PathSeparator) + leaf
		shown := joined(name, leaf)
		if e.IsDir() {
			m, f := walk(w, s, path, shown)
			matched, failed = matched || m, failed || f
			continue
		}
		buf, err := os.ReadFile(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "grep: %s: %v\n", shown, err)
			failed = true
			continue
		}
		if bytes.IndexByte(buf[:min(len(buf), sniff)], 0) >= 0 {
			continue
		}
		n := s.count(buf)
		matched = matched || n > 0
		fmt.Fprintf(w, "%s:%d\n", shown, n)
	}
	return matched, failed
}

// count is the number of lines of buf that hold a match. A buffer that is
// not valid UTF-8 is judged line by line, and its invalid lines are skipped.
func (s *searcher) count(buf []byte) int {
	if utf8.Valid(buf) {
		return s.lines(buf)
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
		if utf8.Valid(line) && s.lines(line) > 0 {
			n++
		}
		buf = buf[next:]
	}
	return n
}

// lines counts the lines of buf that hold a match: the next match from the
// start of the current line, then on from the line after it. The row's
// patterns match no line break, so a match never spans two lines.
func (s *searcher) lines(buf []byte) int {
	hay := buf
	if s.fold {
		s.low = lowerASCII(s.low[:0], buf)
		hay = s.low
	}
	n, pos := 0, 0
	for pos < len(hay) {
		var at int
		if s.re != nil {
			loc := s.re.FindIndex(hay[pos:])
			if loc == nil {
				break
			}
			at = pos + loc[0]
		} else {
			i := bytes.Index(hay[pos:], s.needle)
			if i < 0 {
				break
			}
			at = pos + i
		}
		n++
		j := bytes.IndexByte(hay[at:], '\n')
		if j < 0 {
			break
		}
		pos = at + j + 1
	}
	return n
}

// lowerASCII appends b with its ASCII capitals lowered to dst.
func lowerASCII(dst, b []byte) []byte {
	for _, c := range b {
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		dst = append(dst, c)
	}
	return dst
}
