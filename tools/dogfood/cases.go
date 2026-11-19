// Why: a dogfood's tests are cases, plain files under cases/ that say how to
// run the program once and what it must print; the harness needs one reader
// of that layout so list, run and cover agree on what a case is.
//
// What: Case and loadCases (a cases/ directory, by name), Spec and resolve
// (one case for one target and profile), parseSim for .sim seeds, and
// Subst with expand, normalize and want for the $CASES, $WORK, $SEED and
// {tmp} placeholders; diff and excerpt describe a mismatch.
//
// How: a case is the set of files <case>[.<target>][.<profile>].<ext>
// (Dogfoods/README.md, "Cases"); the most specific file of each kind wins
// for a run. Outputs are compared byte for byte after the paths the
// harness chose are written back as $CASES, $WORK and {tmp}; $SEED in an
// expected file is the run's seed.

package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// caseExts are the kinds of case file (Dogfoods/README.md, "Cases").
var caseExts = []string{"args", "in", "out", "err", "code", "env", "sim", "bin", "targets", "pty"}

// profiles are the build profiles a case runs in.
var profiles = []string{"dev", "release"}

var allTargets = []string{targetWindows, targetLinux, targetFree}

// Case is one case of a dogfood: its files by kind and qualifier.
type Case struct {
	Name string
	Dir  string // the cases directory, absolute
	// files maps "<qualifier>.<ext>" ("" qualifier: "args") to a file name
	// in Dir; qualifiers are a target, a profile, or "target.profile".
	files map[string]string
}

var caseName = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// loadCases reads dir (a dogfood's cases/), sorted by name. A file whose
// name does not parse as a case file is an error, so a typo is not a case
// silently skipped.
func loadCases(dir string) ([]*Case, error) {
	ents, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	byName := map[string]*Case{}
	for _, e := range ents {
		if e.IsDir() {
			continue // fixtures the cases name through $CASES
		}
		n := e.Name()
		parts := strings.Split(n, ".")
		if len(parts) < 2 {
			continue // a fixture without an extension
		}
		ext := parts[len(parts)-1]
		if !contains(caseExts, ext) {
			continue // a fixture: data.txt, image.ppm
		}
		name, quals := parts[0], parts[1:len(parts)-1]
		if !caseName.MatchString(name) {
			return nil, fmt.Errorf("%s: a case name is letters, digits, '-' and '_'", filepath.Join(dir, n))
		}
		q, err := qualifier(quals)
		if err != nil {
			return nil, fmt.Errorf("%s: %v", filepath.Join(dir, n), err)
		}
		c := byName[name]
		if c == nil {
			c = &Case{Name: name, Dir: dir, files: map[string]string{}}
			byName[name] = c
		}
		c.files[q+"."+ext] = n
	}
	var out []*Case
	for _, c := range byName {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// qualifier checks a case file's middle parts: none, a target, a profile,
// or a target then a profile.
func qualifier(q []string) (string, error) {
	switch {
	case len(q) == 0:
		return "", nil
	case len(q) == 1 && (contains(allTargets, q[0]) || contains(profiles, q[0])):
		return q[0], nil
	case len(q) == 2 && contains(allTargets, q[0]) && contains(profiles, q[1]):
		return q[0] + "." + q[1], nil
	}
	return "", fmt.Errorf("qualifier %q: want a target (%s), a profile (dev, release) or target.profile", strings.Join(q, "."), strings.Join(allTargets, ", "))
}

// file returns the path of the most specific file of kind ext for target
// and profile, or "".
func (c *Case) file(ext, target, profile string) string {
	for _, q := range []string{target + "." + profile, target, profile, ""} {
		if n, ok := c.files[q+"."+ext]; ok {
			return filepath.Join(c.Dir, n)
		}
	}
	return ""
}

// Has reports whether the case has a file of kind ext for any run.
func (c *Case) Has(ext string) bool {
	for k := range c.files {
		if strings.HasSuffix("."+k, "."+ext) {
			return true
		}
	}
	return false
}

// Spec is a case resolved for one target and profile.
type Spec struct {
	Args    []string
	Stdin   string // a path, "" for an empty input
	Out     []byte
	Err     []byte
	Code    int
	Env     []string // K=V
	Bin     string   // "" for the dogfood's first bin
	Seeds   []uint64 // a .sim case's seeds
	SimTest bool     // a .sim case run as mk test --sim
	Skip    string   // why the case does not run here, "" when it does
	// Pty is the path of a terminal case's script (.pty): the
	// program runs under ptyrun on a pseudo-terminal, and .out is the
	// transcript it writes.
	Pty string
	// UsesTmp is set when .args or .env name {tmp}, the case's scratch
	// directory.
	UsesTmp bool
}

// resolve reads the case's files for target and profile.
func (c *Case) resolve(target, profile string) (*Spec, error) {
	s := &Spec{}
	if p := c.file("targets", target, profile); p != "" {
		b, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		// The case runs where the file names its target, or names no
		// target, and names its profile, or names no profile.
		ts := strings.Fields(string(b))
		var tg, pr []string
		for _, t := range ts {
			switch {
			case contains(allTargets, t):
				tg = append(tg, t)
			case contains(profiles, t):
				pr = append(pr, t)
			default:
				return nil, fmt.Errorf("%s: %q is not a target or a profile", p, t)
			}
		}
		if (len(tg) > 0 && !contains(tg, target)) || (len(pr) > 0 && !contains(pr, profile)) {
			s.Skip = "the case's .targets file names " + strings.Join(ts, " ")
			return s, nil
		}
	}
	read := func(ext string) ([]byte, error) {
		p := c.file(ext, target, profile)
		if p == "" {
			return nil, nil
		}
		return os.ReadFile(p)
	}
	var err error
	if b, err := read("args"); err != nil {
		return nil, err
	} else {
		s.Args = lines(b)
	}
	s.Stdin = c.file("in", target, profile)
	if s.Out, err = read("out"); err != nil {
		return nil, err
	}
	if s.Err, err = read("err"); err != nil {
		return nil, err
	}
	if b, err := read("code"); err != nil {
		return nil, err
	} else if b != nil {
		if s.Code, err = strconv.Atoi(strings.TrimSpace(string(b))); err != nil {
			return nil, fmt.Errorf("%s: %v", c.file("code", target, profile), err)
		}
	}
	if b, err := read("env"); err != nil {
		return nil, err
	} else {
		for _, l := range lines(b) {
			if k, _, ok := strings.Cut(l, "="); !ok || k == "" {
				return nil, fmt.Errorf("%s: %q is not NAME=value", c.file("env", target, profile), l)
			}
			s.Env = append(s.Env, l)
		}
	}
	if b, err := read("bin"); err != nil {
		return nil, err
	} else if b != nil {
		s.Bin = strings.TrimSpace(string(b))
	}
	if b, err := read("sim"); err != nil {
		return nil, err
	} else if b != nil {
		if s.Seeds, s.SimTest, err = parseSim(b); err != nil {
			return nil, fmt.Errorf("%s: %v", c.file("sim", target, profile), err)
		}
	}
	for _, a := range append(append([]string(nil), s.Args...), s.Env...) {
		if strings.Contains(a, tmpHole) {
			s.UsesTmp = true
		}
	}
	if s.UsesTmp && s.SimTest {
		return nil, fmt.Errorf("%s: a mode test case takes no %s (the scratch directory is a program's argument)", c.file("sim", target, profile), tmpHole)
	}
	if s.Pty = c.file("pty", target, profile); s.Pty != "" {
		// The terminal is the program's standard input, output and error:
		// what it writes there is the pseudo-terminal's rendering, never
		// judged, and ptyrun runs one program once.
		for _, k := range []string{"in", "err", "sim"} {
			if p := c.file(k, target, profile); p != "" {
				return nil, fmt.Errorf("%s: a terminal case (.pty) has no .%s file: its program's streams are the terminal", p, k)
			}
		}
	}
	return s, nil
}

// tmpHole is the placeholder of a case's scratch directory in .args and
// .env.
const tmpHole = "{tmp}"

// lines splits a file into its lines, without "\r", dropping a final
// empty line; a line is kept whole, spaces included (one argument).
func lines(b []byte) []string {
	if b == nil {
		return nil
	}
	s := strings.ReplaceAll(string(b), "\r\n", "\n")
	s = strings.TrimSuffix(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// parseSim reads a .sim file: "seed N" and "seeds A..B" (inclusive) lines,
// "mode test" to run the package's tests with mk test --sim instead of the
// program with --seed, and # comments.
func parseSim(b []byte) (seeds []uint64, test bool, err error) {
	for _, l := range lines(b) {
		f := strings.Fields(l)
		if len(f) == 0 || strings.HasPrefix(f[0], "#") {
			continue
		}
		switch {
		case len(f) == 2 && f[0] == "seed":
			n, err := strconv.ParseUint(f[1], 10, 64)
			if err != nil {
				return nil, false, fmt.Errorf("seed %q: %v", f[1], err)
			}
			seeds = append(seeds, n)
		case len(f) == 2 && f[0] == "seeds":
			a, z, ok := strings.Cut(f[1], "..")
			lo, err1 := strconv.ParseUint(a, 10, 64)
			hi, err2 := strconv.ParseUint(z, 10, 64)
			if !ok || err1 != nil || err2 != nil || hi < lo || hi-lo >= 100000 {
				return nil, false, fmt.Errorf("seeds %q: want A..B, A <= B, at most 100000 seeds", f[1])
			}
			for n := lo; ; n++ {
				seeds = append(seeds, n)
				if n == hi {
					break
				}
			}
		case len(f) == 2 && f[0] == "mode" && (f[1] == "test" || f[1] == "bin"):
			test = f[1] == "test"
		default:
			return nil, false, fmt.Errorf("line %q: want seed N, seeds A..B or mode test|bin", l)
		}
	}
	if len(seeds) == 0 {
		return nil, false, fmt.Errorf("no seed")
	}
	return seeds, test, nil
}

// Subst maps the placeholders of args, env and expected outputs to the
// values of one run: $CASES (the cases directory), $WORK (the case's
// working directory, empty at the start), $SEED and {tmp} (the case's
// scratch directory), each as the program sees it.
type Subst struct {
	Cases, Work, Seed, Tmp string
}

// expand writes the run's values into s.
func (x Subst) expand(s string) string {
	s = strings.ReplaceAll(s, "$CASES", x.Cases)
	s = strings.ReplaceAll(s, "$WORK", x.Work)
	if x.Tmp != "" {
		s = strings.ReplaceAll(s, tmpHole, x.Tmp)
	}
	if x.Seed != "" {
		s = strings.ReplaceAll(s, "$SEED", x.Seed)
	}
	return s
}

// normalize writes the run's paths in out back as $WORK, $CASES and {tmp}
// (the longer first, in both slash directions), so an expected file holds
// no machine's path.
func (x Subst) normalize(out []byte) []byte {
	type pair struct{ from, to string }
	var ps []pair
	for _, p := range []pair{{x.Work, "$WORK"}, {x.Cases, "$CASES"}, {x.Tmp, tmpHole}} {
		if p.from == "" {
			continue
		}
		ps = append(ps, p)
		if alt := strings.ReplaceAll(p.from, `\`, "/"); alt != p.from {
			ps = append(ps, pair{alt, p.to})
		}
		if alt := strings.ReplaceAll(p.from, "/", `\`); alt != p.from {
			ps = append(ps, pair{alt, p.to})
		}
	}
	sort.SliceStable(ps, func(i, j int) bool { return len(ps[i].from) > len(ps[j].from) })
	for _, p := range ps {
		out = bytes.ReplaceAll(out, []byte(p.from), []byte(p.to))
	}
	return out
}

// diff describes the first difference of got from want, or "" when equal.
func diff(what string, want, got []byte) string {
	if bytes.Equal(want, got) {
		return ""
	}
	i := 0
	for i < len(want) && i < len(got) && want[i] == got[i] {
		i++
	}
	line := 1 + bytes.Count(got[:i], []byte("\n"))
	return fmt.Sprintf("%s differs at byte %d (line %d): want %s, got %s", what, i, line, excerpt(want, i), excerpt(got, i))
}

// excerpt quotes up to 60 bytes of b around i: from the start of i's line,
// or from 40 bytes before i (marked "...") when the line starts further
// back, so the byte that differs is always inside it.
func excerpt(b []byte, i int) string {
	if i >= len(b) {
		return "end of output"
	}
	start := bytes.LastIndexByte(b[:i], '\n') + 1
	lead := ""
	if i-start > 40 {
		start, lead = i-40, "..."
	}
	end := min(len(b), start+60)
	return lead + strconv.Quote(string(b[start:end]))
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

// want is an expected output for this run: its paths stay $CASES and
// $WORK (normalize writes the run's paths back to them), and $SEED is the
// run's seed.
func (x Subst) want(b []byte) []byte {
	if x.Seed == "" {
		return b
	}
	return bytes.ReplaceAll(b, []byte("$SEED"), []byte(x.Seed))
}
