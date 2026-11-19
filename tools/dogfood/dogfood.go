// Why: every command of the harness starts from the same picture of a
// dogfood: which programs exist, what they build, which library lines they
// claim and how large they are.
//
// What: Dogfood and its Targets, discover (every directory under Dogfoods/
// with a pkg.mk), loadDogfood, countLines and testLines, and ownedLines for
// the README's "## Section 18 lines" block.
//
// How: bins and targets come from pkg.mk by regular expression (no Mk
// parser here), the owned library lines from the README, imports from the
// sources' import lines, and the line count from .mk files outside tests,
// generated views and repro/. A test block is found by mk fmt's layout.

package main

import (
	"bufio"
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
)

// The targets a case can run on.
const (
	targetWindows = "x86_64-windows"
	targetLinux   = "x86_64-linux"
	targetFree    = "x86_64-freestanding"
)

// Dogfood is one program under Dogfoods/.
type Dogfood struct {
	Name string
	Dir  string // absolute
	// Bins are pkg.mk's bin names, in order; the first is a case's default.
	Bins []string
	// Free is a freestanding dogfood (a pkg.Bin with a link_script, which
	// only x86_64-freestanding accepts, or pkg.mk naming
	// x86_64-freestanding): it is built for that target only and its cases
	// boot it in QEMU.
	Free bool
	// Lines counts the non-blank lines of Mk outside tests, generated views,
	// repro/ and pkg.mk, for the minimum line count.
	Lines int
	// Owned are the IDs (Item.ID) of the library lines its README lists;
	// OwnedErr the README's lines that name no library line.
	Owned    []string
	OwnedErr []string
	// Imports are the std modules its sources import, sorted.
	Imports []string
}

// Targets are the targets the dogfood's cases run on.
func (d *Dogfood) Targets() []string {
	if d.Free {
		return []string{targetFree}
	}
	return []string{targetWindows, targetLinux}
}

var (
	binName    = regexp.MustCompile(`pkg\.Bin\(\s*name:\s*"([^"]*)"`)
	linkScript = regexp.MustCompile(`link_script:\s*"[^"]+"`)
	// importLine is the grammar's Import = "import" [ Name "=" ]
	// Path, for a std path: `import std.http` and `import h = std.http`.
	importLine = regexp.MustCompile(`(?m)^import[ \t]+(?:[A-Za-z_][A-Za-z0-9_]*[ \t]*=[ \t]*)?(std(?:\.[a-z0-9_]+)+)`)
)

// discover lists the dogfoods under dir, sorted by name: every directory
// that holds a pkg.mk. only, when not empty, keeps those names and is an
// error for a name that is not a dogfood.
func discover(dir string, only []string, items []Item) ([]*Dogfood, error) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []*Dogfood
	seen := map[string]bool{}
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		pkg := filepath.Join(dir, e.Name(), "pkg.mk")
		if _, err := os.Stat(pkg); err != nil {
			continue
		}
		seen[e.Name()] = true
		if len(only) > 0 && !slices.Contains(only, e.Name()) {
			continue
		}
		d, err := loadDogfood(filepath.Join(dir, e.Name()), items)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	for _, n := range only {
		if !seen[n] {
			return nil, fmt.Errorf("-only %s: no dogfood %s (a directory with a pkg.mk) under %s", n, n, dir)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// loadDogfood reads one dogfood's pkg.mk, README and sources.
func loadDogfood(dir string, items []Item) (*Dogfood, error) {
	d := &Dogfood{Name: filepath.Base(dir), Dir: dir}
	pkg, err := os.ReadFile(filepath.Join(dir, "pkg.mk"))
	if err != nil {
		return nil, err
	}
	for _, m := range binName.FindAllSubmatch(pkg, -1) {
		d.Bins = append(d.Bins, string(m[1]))
	}
	if len(d.Bins) == 0 {
		return nil, fmt.Errorf("%s: pkg.mk declares no pkg.Bin", filepath.Join(dir, "pkg.mk"))
	}
	d.Free = linkScript.Match(pkg) || bytes.Contains(pkg, []byte(targetFree))
	imports := map[string]bool{}
	err = filepath.WalkDir(dir, func(path string, e fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if e.IsDir() {
			switch n := e.Name(); {
			case path == dir:
			case n == "gen" || n == "repro" || n == "cases" || strings.HasPrefix(n, "."):
				return filepath.SkipDir
			}
			return nil
		}
		n := e.Name()
		if !strings.HasSuffix(n, ".mk") || path == filepath.Join(dir, "pkg.mk") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, m := range importLine.FindAllSubmatch(b, -1) {
			imports[string(m[1])] = true
		}
		if !strings.HasPrefix(n, "tests_") {
			d.Lines += countLines(b)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	for m := range imports {
		d.Imports = append(d.Imports, m)
	}
	sort.Strings(d.Imports)
	readme, err := os.ReadFile(filepath.Join(dir, "README.md"))
	if err == nil {
		d.Owned, d.OwnedErr = ownedLines(string(readme), items)
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	return d, nil
}

// countLines counts the non-blank lines of a Mk file outside its test
// blocks (testLines).
func countLines(b []byte) int {
	n := 0
	scanTests(b, func(l string, test bool) {
		if !test && strings.TrimSpace(l) != "" {
			n++
		}
	})
	return n
}

// testLines reports for each line of a Mk file (index 0 is line 1)
// whether it is part of a test block.
func testLines(b []byte) []bool {
	var out []bool
	scanTests(b, func(_ string, test bool) { out = append(out, test) })
	return out
}

// scanTests calls f with each line of a Mk file and whether it is part of
// a test block: a line `test "..." {` at column 0 through the next `}` at
// column 0, mk fmt's layout.
func scanTests(b []byte, f func(line string, test bool)) {
	inTest := false
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(make([]byte, 64<<10), 16<<20)
	for sc.Scan() {
		l := strings.TrimRight(sc.Text(), " \t\r")
		switch {
		case inTest:
			f(l, true)
			if l == "}" {
				inTest = false
			}
		case strings.HasPrefix(l, "test \"") && strings.HasSuffix(l, "{"):
			inTest = true
			f(l, true)
		default:
			f(l, false)
		}
	}
}

// ownedHeading opens the README section whose first fenced block lists
// the library lines a dogfood owns.
const ownedHeading = "## Section 18 lines"

// ownedLines reads the owned lines of a dogfood README: the first fenced
// block after the "## Section 18 lines" heading holds `// std.x` section
// comments, as in the library surface of doc/reference.md, and under each the
// lines copied from that module word for word; lines before the first comment
// are the prelude's, and a line `*` owns every line of its section. It
// returns the IDs owned and the lines that match no library line.
func ownedLines(readme string, items []Item) (owned, bad []string) {
	readme = strings.ReplaceAll(readme, "\r\n", "\n")
	i := strings.Index(readme, "\n"+ownedHeading)
	if i < 0 && !strings.HasPrefix(readme, ownedHeading) {
		return nil, nil
	}
	rest := readme[i+1:]
	open := strings.Index(rest, "\n```")
	if open < 0 {
		return nil, []string{ownedHeading + " has no fenced block"}
	}
	rest = rest[open+1:]
	nl := strings.IndexByte(rest, '\n')
	if nl < 0 {
		return nil, []string{ownedHeading + "'s block is not closed"}
	}
	body := rest[nl+1:]
	end := strings.Index(body, "\n```")
	if end < 0 && !strings.HasPrefix(body, "```") {
		return nil, []string{ownedHeading + "'s block is not closed"}
	}
	if end < 0 {
		end = 0
	}
	byID := map[string]*Item{}
	for k := range items {
		byID[items[k].ID()] = &items[k]
	}
	seen := map[string]bool{}
	add := func(id string) {
		if !seen[id] {
			seen[id] = true
			owned = append(owned, id)
		}
	}
	sect := ""
	lines := strings.Split(body[:end], "\n")
	for i := 0; i < len(lines); i++ {
		l := strings.TrimRight(lines[i], " \t")
		switch {
		case strings.TrimSpace(l) == "":
			continue
		case strings.HasPrefix(l, "//"):
			if m := sectionComment.FindStringSubmatch(l); m != nil {
				sect = m[1]
			} else if strings.TrimSpace(strings.TrimPrefix(l, "//")) == "prelude" {
				sect = ""
			}
			continue
		case l == "*":
			n := 0
			for k := range items {
				if items[k].InSect == sect {
					add(items[k].ID())
					n++
				}
			}
			if n == 0 {
				bad = append(bad, sectName(sect)+" | *: the section has no lines")
			}
			continue
		}
		id := sectName(sect) + " | " + l
		it := byID[id]
		if it == nil {
			bad = append(bad, id)
			continue
		}
		add(id)
		if it.Block {
			// A block is one line; its body, indented, and its closing
			// brace may be copied below it.
			for i+1 < len(lines) {
				next := strings.TrimRight(lines[i+1], " \t")
				if next != "}" && !strings.HasPrefix(next, " ") && !strings.HasPrefix(next, "\t") {
					break
				}
				i++
				if next == "}" {
					break
				}
			}
		}
	}
	return owned, bad
}
