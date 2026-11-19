// Why: a release archive must carry the documentation of the mk it holds,
// and std's API text must be the one that mk writes, not a copy that may
// be older.
//
// What: docEntries, doc/ of the archive: the repository's doc/**/*.md (the
// language reference and the std module pages under doc/std/ included) and
// doc/std-api.md, written by the archive's own mk with `mk doc --std std`
// (mkStdDoc); sortDocItems and throwawayEnv support it.
//
// How: the child mk runs with every per-user location in a throwaway
// directory. Its Markdown goes through sortDocItems, which puts each
// module's items in heading order, so the archive is byte-reproducible
// whatever order an mk writes them in.

package main

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"mk/internal/release"
)

// mkStdDoc runs `<mk> doc --std std` and returns its Markdown (tests
// replace it for an mk this machine cannot run). Every per-user location
// of the child is a throwaway directory, so a build never touches the
// builder's cache or home.
var mkStdDoc = func(mk string) ([]byte, error) {
	tmp, err := os.MkdirTemp("", "mk-release-doc-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	cmd := exec.Command(mk, "doc", "--std", "std")
	cmd.Env = throwawayEnv(tmp)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%s doc --std std: %v\n%s", mk, err, errb.String())
	}
	return out.Bytes(), nil
}

// throwawayEnv is the environment of an mk that release runs: the
// builder's, with MK_* removed and every per-user location under dir.
func throwawayEnv(dir string) []string {
	var env []string
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		switch strings.ToUpper(k) {
		case "TEMP", "TMP", "TMPDIR", "LOCALAPPDATA", "HOME", "USERPROFILE", "XDG_CACHE_HOME", "XDG_DATA_HOME":
			continue
		}
		if strings.HasPrefix(strings.ToUpper(k), "MK_") {
			continue
		}
		env = append(env, kv)
	}
	for _, k := range []string{"TEMP", "TMP", "TMPDIR", "LOCALAPPDATA", "HOME", "USERPROFILE", "XDG_CACHE_HOME"} {
		p := filepath.Join(dir, strings.ToLower(k))
		os.MkdirAll(p, 0o755)
		env = append(env, k+"="+p)
	}
	return append(env, "MK_CACHE="+filepath.Join(dir, "cache"))
}

// sortDocItems puts the items of each module section ("## " heading) of
// mk doc's Markdown in the order of their "### " headings, keeping each
// item's text whole. An older mk listed a module's items in an order that
// changed from run to run; for an mk that sorts its items this is the
// identity.
func sortDocItems(md []byte) []byte {
	lines := strings.SplitAfter(string(md), "\n")
	var out strings.Builder
	var items []string // the current module's item blocks
	var cur *strings.Builder
	inFence := false
	flush := func() {
		if cur != nil {
			items = append(items, cur.String())
			cur = nil
		}
		sort.Slice(items, func(i, k int) bool {
			if a, b := firstLine(items[i]), firstLine(items[k]); a != b {
				return a < b
			}
			return items[i] < items[k]
		})
		for _, it := range items {
			out.WriteString(it)
		}
		items = nil
	}
	for _, l := range lines {
		if strings.HasPrefix(l, "```") {
			inFence = !inFence
		}
		switch {
		case !inFence && strings.HasPrefix(l, "## "):
			flush()
			out.WriteString(l)
		case !inFence && strings.HasPrefix(l, "### "):
			if cur != nil {
				items = append(items, cur.String())
			}
			cur = &strings.Builder{}
			cur.WriteString(l)
		case cur != nil:
			cur.WriteString(l)
		default:
			out.WriteString(l)
		}
	}
	flush()
	return []byte(out.String())
}

func firstLine(s string) string {
	l, _, _ := strings.Cut(s, "\n")
	return l
}

// stdAPIDoc is the name, under doc/ in the archive, of std's API
// documentation from `mk doc --std std`. The repository's own guide to
// std, doc/std/README.md, ships beside it.
const stdAPIDoc = "std-api.md"

// docEntries is doc/ of the archive: the Markdown files under repo/doc/,
// subdirectories included (doc/std/fs.md), in byte order of path, which
// must hold the language reference (doc/reference.md), and doc/std-api.md
// from mk's `mk doc --std std`.
func docEntries(repo, mk string) ([]release.Entry, error) {
	dir := filepath.Join(repo, "doc")
	if _, err := os.Stat(filepath.Join(dir, "reference.md")); err != nil {
		return nil, fmt.Errorf("no reference at %s (pass -doc or -repo)", filepath.Join(dir, "reference.md"))
	}
	var docs []string
	err := filepath.WalkDir(dir, func(p string, e fs.DirEntry, err error) error {
		if err != nil || !e.Type().IsRegular() || !strings.HasSuffix(p, ".md") {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		docs = append(docs, filepath.ToSlash(rel))
		return err
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(docs)
	var es []release.Entry
	for _, r := range docs {
		p := filepath.Join(dir, filepath.FromSlash(r))
		if r == stdAPIDoc {
			return nil, fmt.Errorf("%s: doc/%s is written by mk doc --std, not taken from the repository", p, stdAPIDoc)
		}
		es = append(es, release.Entry{Name: "doc/" + r, Path: p})
	}
	md, err := mkStdDoc(mk)
	if err != nil {
		return nil, err
	}
	if !bytes.HasPrefix(md, []byte("# std\n")) {
		return nil, fmt.Errorf("mk doc --std std did not write std's documentation (first bytes %.40q)", md)
	}
	return append(es, release.Entry{Name: "doc/" + stdAPIDoc, Data: sortDocItems(md)}), nil
}
