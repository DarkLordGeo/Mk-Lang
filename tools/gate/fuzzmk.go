// Why: Mk code has fuzz targets too (a `test` block that calls
// testing.Fuzz), and the gate fuzzes them beside Go's Fuzz functions.
//
// What: mkFuzzTargets, mkPackageDir, mkInputDigest, mkFuzzArgv, fuzzOneMk
// and mkAffected.
//
// How: each target runs as `go run ./cmd/mk test --fuzz <test name>
// --fuzztime D <package dir>`. Its package is the nearest directory above
// the file holding a pkg.mk, else the file's own directory, named "./dir"
// like a Go package. Its inputs digest is the SHA-256 of the package
// directory's files with the inputs digest of ./cmd/mk (the compiler and
// the embedded std), so -since last-fuzz refuzzes it when either changes.

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
)

// mkTestBlock is the head of a test block: its name.
var mkTestBlock = regexp.MustCompile(`(?m)^\s*test\s+"((?:[^"\\\n]|\\.)*)"`)

// mkFuzzCall is a call that registers a fuzz target.
var mkFuzzCall = regexp.MustCompile(`\btesting\.Fuzz\s*\(`)

// mkFuzzTargets are the Mk fuzz targets under root, in package then test
// order; testdata and dot directories are skipped, as for Go's, and so is
// tests/conformance.
func mkFuzzTargets(root string, selected func(string) bool) ([]fuzzTarget, error) {
	var out []fuzzTarget
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if n := d.Name(); path != root && (strings.HasPrefix(n, ".") || n == "testdata") {
				return filepath.SkipDir
			}
			// tests/conformance's units are one-file checks judged by mk
			// corpus, not packages: a unit that calls testing.Fuzz is no
			// target.
			if rel, _ := filepath.Rel(root, path); filepath.ToSlash(rel) == "tests/conformance" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".mk") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		text := string(b)
		heads, code := mkCodeOnly(text, true), mkCodeOnly(text, false)
		if !mkFuzzCall.MatchString(code) {
			return nil
		}
		rel, err := filepath.Rel(root, mkPackageDir(root, filepath.Dir(path)))
		if err != nil {
			return err
		}
		pkg := normPkg(rel)
		if !selected(pkg) {
			return nil
		}
		for _, m := range mkTestBlock.FindAllStringSubmatchIndex(heads, -1) {
			if mkFuzzCall.MatchString(mkBlockText(code[m[0]:])) {
				t := fuzzTarget{pkg: pkg, name: strings.ReplaceAll(text[m[2]:m[3]], `\"`, `"`), mk: true}
				if !slices.Contains(out, t) {
					out = append(out, t)
				}
			}
		}
		return nil
	})
	sort.SliceStable(out, func(i, j int) bool { return out[i].pkg < out[j].pkg })
	return out, err
}

// mkPackageDir is the nearest directory from dir up to root holding a
// pkg.mk, else dir.
func mkPackageDir(root, dir string) string {
	for d := dir; len(d) >= len(root); d = filepath.Dir(d) {
		if _, err := os.Stat(filepath.Join(d, "pkg.mk")); err == nil {
			return d
		}
		if d == filepath.Dir(d) {
			break
		}
	}
	return dir
}

// mkBlockText is t up to the brace closing its first brace (string
// literals and line comments not counted), or all of t.
func mkBlockText(t string) string {
	depth := 0
	for i := 0; i < len(t); i++ {
		switch c := t[i]; c {
		case '"', '\'':
			for i++; i < len(t) && t[i] != c && t[i] != '\n'; i++ {
				if t[i] == '\\' {
					i++
				}
			}
		case '/':
			if i+1 < len(t) && t[i+1] == '/' {
				for i < len(t) && t[i] != '\n' {
					i++
				}
			}
		case '{':
			depth++
		case '}':
			if depth--; depth == 0 {
				return t[:i+1]
			}
		}
	}
	return t
}

// mkDirDigest is the SHA-256 of the files under dir (relative slash paths
// and contents, in path order).
func mkDirDigest(dir string) (string, error) {
	var files []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && path != dir && strings.HasPrefix(d.Name(), ".") {
			return filepath.SkipDir
		}
		if d.Type().IsRegular() {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(files)
	h := sha256.New()
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			return "", err
		}
		rel, _ := filepath.Rel(dir, f)
		s := sha256.Sum256(b)
		fmt.Fprintf(h, "%s\x00%s\n", filepath.ToSlash(rel), hex.EncodeToString(s[:]))
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// mkInputDigest is a Mk fuzz package's inputs digest: its directory's
// files and ./cmd/mk's inputs digest.
func (g *gate) mkInputDigest(pkg string, digests map[string]string) (string, error) {
	own, err := mkDirDigest(filepath.Join(g.root, filepath.FromSlash(strings.TrimPrefix(pkg, "./"))))
	if err != nil {
		return "", err
	}
	mk, err := g.inputDigest("./cmd/mk", digests)
	if err != nil {
		return "", err
	}
	h := sha256.Sum256([]byte("mk " + own + " " + mk))
	return hex.EncodeToString(h[:]), nil
}

// mkFuzzArgv is the command that fuzzes Mk target t for the tier's time.
func (g *gate) mkFuzzArgv(t fuzzTarget) []string {
	return []string{g.goExe, "run", "./cmd/mk", "test", "--fuzz", t.name, "--fuzztime", g.fuzzTime.String(), t.pkg}
}

// mkSaved is the line mk test --fuzz prints for each failing input saved.
var mkSaved = regexp.MustCompile(`: saved (testdata/fuzz/\S.*)$`)

// fuzzOneMk fuzzes the Mk target t; a failing input saved (mk test's exit
// status 1) fails the step and names the input.
func (g *gate) fuzzOneMk(t fuzzTarget) bool {
	name := "fuzz " + t.pkg + " " + t.name
	s := g.newStep(name)
	r := &FuzzResult{Package: t.pkg, Target: t.name, FuzzTime: g.fuzzTime.String()}
	s.Fuzz = r
	var all []string
	code := g.exec(s, spec{argv: g.mkFuzzArgv(t), set: []string{"MK_GATE=1"}}, func(l string) {
		if m := mkSaved.FindStringSubmatch(strings.TrimSpace(l)); m != nil && r.Crasher == "" {
			r.Crasher = filepath.Join(g.root, filepath.FromSlash(strings.TrimPrefix(t.pkg, "./")), filepath.FromSlash(m[1]))
		}
		fmt.Printf("   %s\n", l)
		all = append(all, l)
		if len(all) > 400 {
			all = all[len(all)-200:]
		}
	})
	s.Output = tail(all, 60)
	switch {
	case r.Crasher != "":
		fail(s, "failing input saved to "+r.Crasher)
	case code != 0:
		fail(s, fmt.Sprintf("mk test --fuzz exit status %d", code))
	}
	return s.Status == "pass"
}

// mkAffected is the set of Mk fuzz packages with a changed file under
// their directory, or all of them when ./cmd/mk is affected.
func mkAffected(pkgs, changed []string, cmdAffected bool) map[string]bool {
	out := map[string]bool{}
	for _, p := range pkgs {
		dir := strings.TrimPrefix(p, "./")
		if cmdAffected || slices.ContainsFunc(changed, func(f string) bool { return dir == "." || strings.HasPrefix(f, dir+"/") }) {
			out[p] = true
		}
	}
	return out
}
