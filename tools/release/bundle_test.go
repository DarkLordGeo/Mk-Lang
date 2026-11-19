// Why: a release bundle carries documentation and a layout users rely on, and
// must build to the same bytes on any host.
// What: tests of the bundle's doc/ (the reference, the release notes and `mk doc
// --std`'s output), the Linux archive's layout and reproducibility, and
// the whole Windows bundle.
// How: the Linux archive is built on any host from stand-ins, the Windows bundle
// from a real mk, LLVM and seed, twice; every licence is a synthetic
// placeholder.

package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"mk/internal/release"
)

// syntheticLicense writes the placeholder licence a test passes as
// -license.
func syntheticLicense(t *testing.T) string {
	p := filepath.Join(t.TempDir(), "LICENSE")
	if err := os.WriteFile(p, []byte("SYNTHETIC licence for the tests\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// fakeRepo is a repository root with doc/: the reference, other guides,
// a std guide of its own and a file that is not Markdown.
func fakeRepo(t *testing.T) string {
	r := t.TempDir()
	for n, body := range map[string]string{
		"CHANGELOG.md":         "# Changelog\n",
		"doc/reference.md":     "# Reference\n",
		"doc/errors.md":        "# Errors\n",
		"doc/std/README.md":    "# The standard library\n",
		"doc/std/fs.md":        "# std.fs\n",
		"doc/not-markdown.txt": "not shipped\n",
		"design/memory.md":     "not shipped\n",
	} {
		p := filepath.Join(r, filepath.FromSlash(n))
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(body), 0o644)
	}
	return r
}

// stdDocStub serves mk doc's Markdown with its items in an order that
// changes on every call, as an mk doc with no fixed item order could, so the
// bundle must sort them (sortDocItems).
func stdDocStub(t *testing.T) {
	old := mkStdDoc
	n := 0
	mkStdDoc = func(string) ([]byte, error) {
		n++
		a := "### `Capped.Bytes`\n\n```\nexport Capped.Bytes(&self): []u8 from self\n```\n\nThe bytes kept.\n\n"
		b := "### `Capped.New`\n\n```\n### not a heading inside a fence\n```\n\nAn empty target.\n\n"
		if n%2 == 0 {
			a, b = b, a
		}
		return []byte("# std\n\n## Module `std.atomic`\n\nStable\n\n### `Z`\n\nz\n\n### `A`\n\na\n\n## Module `std.text`\n\nStable\n\n" + a + b), nil
	}
	t.Cleanup(func() { mkStdDoc = old })
}

func TestSortDocItems(t *testing.T) {
	in := "# std\n\n## Module `m`\n\nStable\n\n### `b`\n\n```\n### x\n```\n\n### `a`\n\ntext\n\n## Module `n`\n\n### `d`\n\n### `c`\n"
	want := "# std\n\n## Module `m`\n\nStable\n\n### `a`\n\ntext\n\n### `b`\n\n```\n### x\n```\n\n## Module `n`\n\n### `c`\n### `d`\n\n"
	if got := string(sortDocItems([]byte(in))); got != want {
		t.Errorf("sortDocItems:\n%q\nwant\n%q", got, want)
	}
	if again := string(sortDocItems([]byte(want))); again != want {
		t.Errorf("not idempotent:\n%q", again)
	}
}

// TestBuildBundleDocs: with no -doc, doc/ holds doc/*.md and std-api.md
// from mk doc --std, whose item order does not reach the bytes.
func TestBuildBundleDocs(t *testing.T) {
	j := testJob(t)
	stdDocStub(t)
	j.Doc = ""
	j.Repo = fakeRepo(t)
	j.License = syntheticLicense(t)
	var outs [2][]byte
	var p string
	for i := range outs {
		j.Out = t.TempDir()
		var err error
		if p, _, err = buildArchive(j); err != nil {
			t.Fatal(err)
		}
		outs[i], _ = os.ReadFile(p)
	}
	if !bytes.Equal(outs[0], outs[1]) {
		t.Fatal("two builds differ (mk doc's item order reached the archive)")
	}
	host := release.HostOf(runtime.GOOS, runtime.GOARCH)
	dest := filepath.Join(t.TempDir(), "x")
	if err := release.Extract(p, dest, release.TopDir("0.3.0", host), release.Limits{}, nil); err != nil {
		t.Fatal(err)
	}
	var docs []string
	des, _ := os.ReadDir(filepath.Join(dest, "doc"))
	for _, de := range des {
		docs = append(docs, de.Name())
	}
	if want := []string{"errors.md", "reference.md", "std", "std-api.md"}; !slices.Equal(docs, want) {
		t.Errorf("doc/ holds %v, want %v", docs, want)
	}
	md, _ := os.ReadFile(filepath.Join(dest, "doc", "std-api.md"))
	if i, k := bytes.Index(md, []byte("`Capped.Bytes`")), bytes.Index(md, []byte("`Capped.New`")); i < 0 || k < i {
		t.Errorf("std-api.md items not sorted:\n%s", md)
	}
	if page, _ := os.ReadFile(filepath.Join(dest, "doc", "std", "fs.md")); string(page) != "# std.fs\n" {
		t.Errorf("doc/std/fs.md is not the repository's module page: %q", page)
	}
	if guide, _ := os.ReadFile(filepath.Join(dest, "doc", "std", "README.md")); string(guide) != "# The standard library\n" {
		t.Errorf("doc/std/README.md is not the repository's guide:\n%s", guide)
	}
	// An mk whose doc fails stops the build.
	mkStdDoc = func(string) ([]byte, error) { return nil, errors.New("doc failed") }
	j.Out = t.TempDir()
	if _, _, err := buildArchive(j); err == nil || !strings.Contains(err.Error(), "doc failed") {
		t.Errorf("a failing mk doc: %v", err)
	}
}

// TestBuildRefusesWithoutLicense: with no -license a build reads
// <repo>/LICENSE, which the fake repository does not have, and refuses
// before writing anything.
func TestBuildRefusesWithoutLicense(t *testing.T) {
	j := testJob(t)
	j.License = ""
	j.Repo = fakeRepo(t)
	j.Out = t.TempDir()
	if _, _, err := buildArchive(j); err == nil || !strings.Contains(err.Error(), "mk's licence (-license)") {
		t.Errorf("no -license: %v", err)
	}
	repo, _ := filepath.Abs(filepath.Join("..", ".."))
	if _, err := os.Stat(filepath.Join(repo, "LICENSE")); err == nil {
		t.Log("the repository now has a LICENSE: a build without -license takes it")
	}
	if des, _ := os.ReadDir(j.Out); len(des) != 0 {
		t.Errorf("a refused build wrote %v", des)
	}
}

// TestBuildLinuxArchive builds the x86_64-linux archive on any host from a
// stand-in mk cross-built for linux/amd64 and a synthetic LLVM directory (the
// real one is built on Linux): the tar.gz's layout, the executable bits and
// two identical builds.
func TestBuildLinuxArchive(t *testing.T) {
	stubClang(t)
	stdDocStub(t)
	oldV := mkVersion
	mkVersion = func(string) (string, error) { return fakeVersionText("0.3.0", 21), nil }
	t.Cleanup(func() { mkVersion = oldV })
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	os.MkdirAll(src, 0o755)
	os.WriteFile(filepath.Join(src, "go.mod"), []byte("module fakemk\n\ngo 1.22\n"), 0o644)
	os.WriteFile(filepath.Join(src, "main.go"), []byte("package main\n\nfunc main() { println(\"fake linux mk\") }\n"), 0o644)
	mk := filepath.Join(dir, "mk")
	cmd := exec.Command(goTool(t), "build", "-trimpath", "-o", mk, ".")
	cmd.Dir = src
	cmd.Env = append(os.Environ(), "GOOS=linux", "GOARCH=amd64", "CGO_ENABLED=0", "GOFLAGS=", "GOWORK=off")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, b)
	}
	llvm := t.TempDir()
	for n, body := range map[string]string{
		"bin/clang":                     "fake clang",
		"bin/lld":                       "fake lld",
		"bin/llvm-lib":                  "never in a Linux archive",
		"lib/clang/21/include/stddef.h": "#define NULL 0\n",
		"LICENSE.TXT":                   "SYNTHETIC LLVM licence\n",
	} {
		p := filepath.Join(llvm, filepath.FromSlash(n))
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(body), 0o755)
	}
	j := buildJob{MK: mk, LLVM: llvm, Version: "0.3.0", License: syntheticLicense(t), Repo: fakeRepo(t), Commit: "0123456789ab", LLVMLib: true}
	var outs [2][]byte
	var p string
	for i := range outs {
		j.Out = t.TempDir()
		var err error
		if p, _, err = buildArchive(j); err != nil {
			t.Fatal(err)
		}
		outs[i], _ = os.ReadFile(p)
	}
	if filepath.Base(p) != "mk-0.3.0-x86_64-linux.tar.gz" {
		t.Errorf("archive %s", filepath.Base(p))
	}
	if !bytes.Equal(outs[0], outs[1]) {
		t.Fatal("two Linux builds differ")
	}
	dest := filepath.Join(t.TempDir(), "x")
	if err := release.Extract(p, dest, release.TopDir("0.3.0", release.HostLinux), release.Limits{}, nil); err != nil {
		t.Fatal(err)
	}
	var got []string
	filepath.WalkDir(dest, func(q string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			r, _ := filepath.Rel(dest, q)
			got = append(got, filepath.ToSlash(r))
		}
		return nil
	})
	want := []string{"CHANGELOG.md", "LICENSE", "LICENSE-LLVM.txt", "RELEASE.txt", "bin/mk", "doc/errors.md", "doc/reference.md",
		"doc/std-api.md", "doc/std/README.md", "doc/std/fs.md", "llvm/bin/clang", "llvm/bin/lld", "llvm/lib/clang/21/include/stddef.h"}
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Errorf("Linux archive holds\n%v\nwant\n%v", got, want)
	}
	if runtime.GOOS != "windows" {
		for _, x := range []string{"bin/mk", "llvm/bin/clang", "llvm/bin/lld"} {
			if fi, err := os.Stat(filepath.Join(dest, x)); err != nil || fi.Mode().Perm()&0o111 == 0 {
				t.Errorf("%s is not executable: %v", x, err)
			}
		}
	}
}

// TestSignSyntheticKey: sums, sign and verify round-trip with a key pair
// keygen writes into the test's temporary directory (never a release key,
// never written elsewhere); a tampered archive, a wrong key and a truncated
// signature are refused; sign refuses the repository's test key, and
// verify's built-in keys (none yet) trust neither.
func TestSignSyntheticKey(t *testing.T) {
	keyFile := filepath.Join(t.TempDir(), "synthetic.key")
	var out, errb bytes.Buffer
	if st := run([]string{"keygen", "-out", keyFile}, &out, &errb); st != 0 {
		t.Fatalf("keygen: %s", errb.String())
	}
	data, _ := os.ReadFile(keyFile)
	priv, err := release.ParsePrivate(data)
	if err != nil {
		t.Fatal(err)
	}
	pub := release.PublicOf(priv)
	if release.IsTestKey(pub) {
		t.Fatal("keygen wrote the test key")
	}
	mkDir := func() string {
		dir := t.TempDir()
		for _, h := range []string{release.HostWindows, release.HostLinux} {
			os.WriteFile(filepath.Join(dir, release.ArchiveName("7.1.0", h)), []byte("synthetic archive for "+h), 0o644)
		}
		if st := run([]string{"sums", "-dir", dir}, &out, &errb); st != 0 {
			t.Fatalf("sums: %s", errb.String())
		}
		if st := run([]string{"sign", "-key", keyFile, "-dir", dir}, &out, &errb); st != 0 {
			t.Fatalf("sign: %s", errb.String())
		}
		return dir
	}
	keys := []release.Key{pub}
	if k, n, err := verifyDir(mkDir(), keys); err != nil || n != 2 || k.ID != pub.ID {
		t.Fatalf("round trip: %v %d %v", k, n, err)
	}
	d := mkDir()
	os.WriteFile(filepath.Join(d, release.ArchiveName("7.1.0", release.HostLinux)), []byte("tampered"), 0o644)
	if _, _, err := verifyDir(d, keys); err == nil || !strings.Contains(err.Error(), "SHA-256") {
		t.Errorf("tampered archive: %v", err)
	}
	_, other, _ := release.GenerateKey()
	if _, _, err := verifyDir(mkDir(), []release.Key{other}); !errors.Is(err, release.ErrUnknownKey) {
		t.Errorf("wrong key: %v", err)
	}
	d = mkDir()
	sig, _ := os.ReadFile(filepath.Join(d, release.SigFile))
	os.WriteFile(filepath.Join(d, release.SigFile), sig[:len(sig)-10], 0o644)
	if _, _, err := verifyDir(d, keys); !errors.Is(err, release.ErrBadSignature) {
		t.Errorf("truncated signature: %v", err)
	}
	if st := run([]string{"verify", "-dir", mkDir()}, &out, &errb); st != 1 {
		t.Errorf("verify with the built-in keys: status %d", st)
	}
	testFile := filepath.Join(t.TempDir(), "test.key")
	os.WriteFile(testFile, testKey(t), 0o600)
	d = mkDir()
	os.Remove(filepath.Join(d, release.SigFile))
	if st := run([]string{"sign", "-key", testFile, "-dir", d}, &out, &errb); st != 2 {
		t.Errorf("sign with the test key: status %d", st)
	}
}

// TestAddTreeLinkedRoot: a header directory that is itself a link (Ubuntu's
// /usr/lib/llvm-21/lib/clang/21 points into /usr/lib/clang) is walked; a
// link below the root is still refused.
func TestAddTreeLinkedRoot(t *testing.T) {
	d := t.TempDir()
	real := filepath.Join(d, "real", "include")
	os.MkdirAll(filepath.Join(real, "sub"), 0o755)
	os.WriteFile(filepath.Join(real, "stddef.h"), []byte("#define NULL 0\n"), 0o644)
	os.WriteFile(filepath.Join(real, "sub", "x.h"), []byte("int x;\n"), 0o644)
	link := filepath.Join(d, "llvm", "lib", "clang", "21")
	os.MkdirAll(filepath.Dir(link), 0o755)
	if err := os.Symlink(filepath.Join(d, "real"), link); err != nil {
		t.Skipf("SKIP: cannot make a symbolic link here: %v", err)
	}
	es, err := addTree(nil, filepath.Join(link, "include"), "llvm/lib/clang/21/include", false)
	if err != nil {
		t.Fatalf("a linked root: %v", err)
	}
	var names []string
	for _, e := range es {
		names = append(names, e.Name)
	}
	if want := []string{"llvm/lib/clang/21/include/stddef.h", "llvm/lib/clang/21/include/sub/x.h"}; !slices.Equal(names, want) {
		t.Errorf("entries %v, want %v", names, want)
	}
	es, err = addTree(nil, link, "x", false)
	if err != nil || len(es) != 2 {
		t.Errorf("the link itself as the root: %v %d", err, len(es))
	}
	if err := os.Symlink(filepath.Join(real, "stddef.h"), filepath.Join(real, "inner.h")); err == nil {
		if _, err := addTree(nil, real, "x", false); err == nil {
			t.Error("a link below the root was accepted")
		}
	}
}
