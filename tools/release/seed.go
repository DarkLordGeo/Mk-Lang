// Why: the toolchain ships its first-party frameworks inside it (seed/), and
// those bytes must be exactly what a fetch of the published tag would give,
// or a seeded package and a fetched one would differ.
//
// What: runSeed and buildSeed (the `seed` subcommand), readSeedList,
// kitTree, and makeReadOnly and makeWritable for the written trees.
//
// How: the list names, per framework, its short name, its source and the
// version the release carries: `<short name> <source> v<version>`, one per
// line, `#` comments. For each, the tag v<version> of the source is read with
// internal/git (internal/fetch's GitTag, which checks the tree as a fetch
// does), its tree hash is compared with the hash of kits/<short name> on
// disk, and only when they are equal is the tree written to seed/pkg/<hex>/,
// read-only, and the line `<short name> <source> v<version> <commit>
// tree-sha256:<hex>` added to seed/first-party.txt, sorted by short name. So
// the seed holds exactly the bytes a fetch of that tag would give, and they
// are the repository's kits/. Tests serve the tag from a synthetic git
// server, so nothing reaches the network there; nothing here reaches the
// network unless the list's source does.

package main

import (
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"mk/internal/fetch"
)

// seedJob is one seed's inputs.
type seedJob struct {
	Kits string // the directory holding one directory per framework (kits/)
	List string // the release's first-party list
	Out  string // the seed directory to write; it must not exist
	// Fetcher reads the tags; nil is a fetcher with the default client
	// and the user's netrc.
	Fetcher *fetch.Fetcher
}

// seedEntry is one line of seed/first-party.txt.
type seedEntry struct {
	Short, Source, Version, Commit string
	Hash                           fetch.TreeHash
}

func (e seedEntry) line() string {
	return fmt.Sprintf("%s %s v%s %s %s", e.Short, e.Source, e.Version, e.Commit, e.Hash)
}

func runSeed(args []string, stdout, stderr io.Writer) int {
	fs := flags("seed", stderr)
	var j seedJob
	fs.StringVar(&j.Kits, "kits", "", "the kits/ directory, one directory per framework")
	fs.StringVar(&j.List, "list", "", "the release's first-party list: <short name> <source> v<version> per line")
	fs.StringVar(&j.Out, "out", "seed", "the seed directory to write (must not exist)")
	if !parse(fs, args, stderr, "kits", "list", "out") {
		return 2
	}
	es, err := buildSeed(j)
	if err != nil {
		return fail(stderr, "seed", err)
	}
	for _, e := range es {
		fmt.Fprintln(stdout, "seeded "+e.line())
	}
	return 0
}

var (
	shortName  = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	seedSource = regexp.MustCompile(`^[a-z0-9-]+(\.[a-z0-9-]+)+(/[A-Za-z0-9._-]+)+$`)
	seedVer    = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)
)

// readSeedList reads the release's first-party list.
func readSeedList(path string) ([]seedEntry, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var out []seedEntry
	seen := map[string]bool{}
	for i, l := range strings.Split(string(b), "\n") {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		f := strings.Fields(l)
		bad := func(why string) error { return fmt.Errorf("%s:%d: %s: %q", path, i+1, why, l) }
		switch {
		case len(f) != 3:
			return nil, bad("a line is <short name> <source> v<version>")
		case !shortName.MatchString(f[0]):
			return nil, bad("the short name is a lower-case identifier")
		case !seedSource.MatchString(f[1]) || strings.HasSuffix(f[1], ".git") || strings.Contains(f[1], ".."):
			return nil, bad("the source is host/path, with no scheme and no .git")
		case !seedVer.MatchString(f[2]):
			return nil, bad("the version is vX.Y.Z, a release")
		case seen[f[0]]:
			return nil, bad("the short name is listed twice")
		}
		seen[f[0]] = true
		out = append(out, seedEntry{Short: f[0], Source: f[1], Version: f[2][1:]})
	}
	return out, nil
}

// buildSeed writes j.Out: seed/pkg/<hex>/ and seed/first-party.txt. On any
// failure j.Out is removed, so a seed is whole or absent.
func buildSeed(j seedJob) (es []seedEntry, err error) {
	es, err = readSeedList(j.List)
	if err != nil {
		return nil, err
	}
	if _, err := os.Lstat(j.Out); err == nil {
		return nil, fmt.Errorf("%s already exists; the seed is written into a new directory", j.Out)
	}
	f := j.Fetcher
	if f == nil {
		f = &fetch.Fetcher{}
	}
	if err := os.MkdirAll(filepath.Join(j.Out, "pkg"), 0o755); err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			makeWritable(j.Out)
			os.RemoveAll(j.Out)
		}
	}()
	for i := range es {
		e := &es[i]
		local, err := kitTree(filepath.Join(j.Kits, e.Short))
		if err != nil {
			return nil, err
		}
		gt, err := f.GitTag(fetch.SourceURL(e.Source), "v"+e.Version)
		if err != nil {
			return nil, fmt.Errorf("%s: %v", e.Short, err)
		}
		if h := local.Hash(); gt.Hash != h {
			return nil, fmt.Errorf("%s: the tag v%s of %s has tree %s, but %s has %s; publish the kit's tree at that tag first",
				e.Short, e.Version, e.Source, gt.Hash, filepath.Join(j.Kits, e.Short), h)
		}
		e.Commit, e.Hash = gt.Commit, gt.Hash
		dir := filepath.Join(j.Out, "pkg", fmt.Sprintf("%x", gt.Hash[:]))
		if _, err := os.Lstat(dir); err == nil {
			continue // two short names of one tree share it
		}
		if err := gt.Tree.WriteVerified(dir, gt.Hash); err != nil {
			return nil, fmt.Errorf("%s: %v", e.Short, err)
		}
		if err := makeReadOnly(dir); err != nil {
			return nil, err
		}
	}
	sort.Slice(es, func(a, b int) bool { return es[a].Short < es[b].Short })
	var b bytes.Buffer
	b.WriteString("# The first-party frameworks this toolchain was tested with (see doc/packages.md).\n")
	b.WriteString("# <short name> <source> v<version> <commit> tree-sha256:<hex>; written by go run ./tools/release seed.\n")
	for _, e := range es {
		b.WriteString(e.line() + "\n")
	}
	if err := os.WriteFile(filepath.Join(j.Out, "first-party.txt"), b.Bytes(), 0o644); err != nil {
		return nil, err
	}
	return es, nil
}

// kitTree is the tree of the framework directory dir as the repository holds
// it: every regular file, by its slash path, except what mk writes beside
// sources and git never holds (the repository's .gitignore): the `gen/` views
// of generated code, a `.mk-cache/` and `.git/`, and executables a build of
// an example left (a file that starts with a PE or ELF header). A link is
// refused, as a fetch refuses one. The hash is then compared with the tag's,
// so a stray file makes the seed fail rather than differ.
func kitTree(dir string) (*fetch.Tree, error) {
	fi, err := os.Stat(dir)
	if err != nil {
		return nil, err
	}
	if !fi.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", dir)
	}
	t := &fetch.Tree{}
	err = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case "gen", ".mk-cache", ".git":
				if p != dir {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("%s is not a regular file; a kit holds no links", p)
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if bytes.HasPrefix(data, []byte("MZ")) || bytes.HasPrefix(data, []byte("\x7fELF")) {
			return nil
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if err := fetch.CheckPath(rel); err != nil {
			return fmt.Errorf("%s: %v", p, err)
		}
		t.Files = append(t.Files, fetch.File{Path: rel, Data: data})
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(t.Files) == 0 {
		return nil, fmt.Errorf("%s holds no files", dir)
	}
	return t, nil
}

// makeReadOnly clears the write bits of every file under dir (every listed
// tree is read-only); the directories stay writable, so the seed can be
// removed without a chmod first.
func makeReadOnly(dir string) error {
	return filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		return os.Chmod(p, 0o444)
	})
}

// makeWritable undoes makeReadOnly, so a failed seed can be removed.
func makeWritable(dir string) {
	filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			os.Chmod(p, 0o644)
		}
		return nil
	})
}
