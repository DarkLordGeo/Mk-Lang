// Why: the seed must hold exactly the published bytes of each framework, or a
// seeded install and a fetched one would differ.
// What: tests of seed: seed/ built from kits/ and the first-party list, the lines
// sorted and the trees read-only; a differing tree, a missing tag and a bad
// list are refused and leave no seed behind.
// How: each tree's hash is checked against the tag a synthetic git server
// (gitsrv_test.go) serves; nothing reaches the network.

package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mk/internal/fetch"
	"mk/internal/git"
)

// seedKit writes a framework directory under kits and returns its files.
func seedKit(t *testing.T, kits, name, greeting string) map[string]string {
	t.Helper()
	files := map[string]string{
		"pkg.mk":            "const Package = pkg.Package(name: \"" + name + "\")\n",
		name + ".mk":        "export Hello(): str {\n    return \"" + greeting + "\"\n}\n",
		"examples/a/pkg.mk": "const Package = pkg.Package(name: \"a\")\n",
	}
	for p, body := range files {
		full := filepath.Join(kits, name, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return files
}

// seedFetcher is a fetcher that reaches gs and reads no netrc.
func seedFetcher(gs *gitServer) *fetch.Fetcher {
	return &fetch.Fetcher{Client: gs.client(), Netrc: git.ParseNetrc(nil)}
}

func writeList(t *testing.T, text string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "first-party.txt")
	if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// treeOf is the fetch.Tree of files.
func treeOf(files map[string]string) *fetch.Tree {
	tr := &fetch.Tree{}
	for p, d := range files {
		tr.Files = append(tr.Files, fetch.File{Path: p, Data: []byte(d)})
	}
	return tr
}

func TestSeedBuildsLinesAndTrees(t *testing.T) {
	kits := t.TempDir()
	gs := newGitServer(t)
	web := seedKit(t, kits, "web", "web")
	cli := seedKit(t, kits, "cli", "cli")
	gs.tag("mk-test/web", "0.1.0", web)
	gs.tag("mk-test/web", "0.2.0", seedKit(t, t.TempDir(), "web", "newer"))
	gs.tag("mk-test/cli", "1.0.0", cli)
	// Output a build of an example and the gen/ view leave: not the kit's.
	os.MkdirAll(filepath.Join(kits, "web", "gen", "x86_64-windows"), 0o755)
	os.WriteFile(filepath.Join(kits, "web", "gen", "x86_64-windows", "gen.mk"), []byte("// view\n"), 0o644)
	os.WriteFile(filepath.Join(kits, "web", "examples", "a", "a.exe"), []byte("MZ\x90\x00binary"), 0o755)
	list := writeList(t, "# the release's frameworks\nweb "+gs.source("mk-test/web")+" v0.1.0\n\ncli "+gs.source("mk-test/cli")+" v1.0.0\n")
	out := filepath.Join(t.TempDir(), "seed")
	t.Cleanup(func() { makeWritable(out) })
	es, err := buildSeed(seedJob{Kits: kits, List: list, Out: out, Fetcher: seedFetcher(gs)})
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(out, "first-party.txt"))
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, l := range strings.Split(string(b), "\n") {
		if l != "" && !strings.HasPrefix(l, "#") {
			lines = append(lines, l)
		}
	}
	wh, ch := treeOf(web).Hash(), treeOf(cli).Hash()
	want := []string{
		fmt.Sprintf("cli %s v1.0.0 %s %s", gs.source("mk-test/cli"), gs.repos["mk-test/cli"].tags["v1.0.0"], ch),
		fmt.Sprintf("web %s v0.1.0 %s %s", gs.source("mk-test/web"), gs.repos["mk-test/web"].tags["v0.1.0"], wh),
	}
	if strings.Join(lines, "\n") != strings.Join(want, "\n") {
		t.Errorf("first-party.txt:\n%s\nwant the lines:\n%s", b, strings.Join(want, "\n"))
	}
	if len(es) != 2 || es[0].Short != "cli" {
		t.Errorf("entries %+v", es)
	}
	for _, c := range []struct {
		h     fetch.TreeHash
		files map[string]string
	}{{wh, web}, {ch, cli}} {
		dir := filepath.Join(out, "pkg", fmt.Sprintf("%x", c.h[:]))
		for p, body := range c.files {
			full := filepath.Join(dir, filepath.FromSlash(p))
			got, err := os.ReadFile(full)
			if err != nil || string(got) != body {
				t.Errorf("%s: %q, %v", full, got, err)
			}
			fi, _ := os.Stat(full)
			if fi != nil && fi.Mode().Perm()&0o222 != 0 {
				t.Errorf("%s is writable (%v)", full, fi.Mode())
			}
		}
		// The written tree hashes as the line says.
		tr, err := kitTree(dir)
		if err != nil || tr.Hash() != c.h {
			t.Errorf("%s re-hashed: %v", dir, err)
		}
	}
	if des, _ := os.ReadDir(filepath.Join(out, "pkg")); len(des) != 2 {
		t.Errorf("seed/pkg holds %d trees, want 2", len(des))
	}
}

func TestSeedRefuses(t *testing.T) {
	kits := t.TempDir()
	gs := newGitServer(t)
	web := seedKit(t, kits, "web", "web")
	gs.tag("mk-test/web", "0.1.0", web)
	// The tag v0.2.0 holds another tree than kits/web.
	gs.tag("mk-test/web", "0.2.0", seedKit(t, t.TempDir(), "web", "changed"))
	src := gs.source("mk-test/web")
	for _, c := range []struct{ name, list, want string }{
		{"tree differs", "web " + src + " v0.2.0\n", "publish the kit's tree"},
		{"no such tag", "web " + src + " v0.3.0\n", "v0.3.0"},
		{"no kit", "cli " + src + " v0.1.0\n", "cli"},
		{"two fields", "web " + src + "\n", "<short name> <source> v<version>"},
		{"a scheme", "web https://" + src + " v0.1.0\n", "host/path"},
		{"no v", "web " + src + " 0.1.0\n", "vX.Y.Z"},
		{"a pre-release", "web " + src + " v0.1.0-rc1\n", "vX.Y.Z"},
		{"twice", "web " + src + " v0.1.0\nweb " + src + " v0.1.0\n", "twice"},
		{"upper case", "Web " + src + " v0.1.0\n", "short name"},
	} {
		out := filepath.Join(t.TempDir(), "seed")
		_, err := buildSeed(seedJob{Kits: kits, List: writeList(t, c.list), Out: out, Fetcher: seedFetcher(gs)})
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want an error naming %q", c.name, err, c.want)
		}
		if _, err := os.Lstat(out); err == nil {
			t.Errorf("%s: a refused seed left %s", c.name, out)
		}
	}
	// An existing output directory is never written into.
	out := t.TempDir()
	if _, err := buildSeed(seedJob{Kits: kits, List: writeList(t, "web "+src+" v0.1.0\n"), Out: out, Fetcher: seedFetcher(gs)}); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("an existing output: %v", err)
	}
}

// The command line: flags, the printed lines, a usage error.
func TestSeedCommand(t *testing.T) {
	emptyNetrc(t)
	var stdout, stderr bytes.Buffer
	if st := run([]string{"seed", "-kits", t.TempDir()}, &stdout, &stderr); st != 2 || !strings.Contains(stderr.String(), "-list is required") {
		t.Errorf("seed without -list: %d %s", st, stderr.String())
	}
	stderr.Reset()
	list := writeList(t, "web example.com/mk-test/web v0.1.0\n")
	out := filepath.Join(t.TempDir(), "seed")
	// The kit is missing, so nothing is fetched.
	if st := run([]string{"seed", "-kits", t.TempDir(), "-list", list, "-out", out}, &stdout, &stderr); st != 2 || !strings.Contains(stderr.String(), "release seed:") {
		t.Errorf("seed of a missing kit: %d %s", st, stderr.String())
	}
}
