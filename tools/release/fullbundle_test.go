// Why: the bundle users download must build from real parts, reproducibly, and
// its own mk must find its LLVM.
// What: TestBuildRealBundle, the whole host bundle built twice to the same bytes.
// How: needs a real LLVM (MK_RELEASE_LLVM or the default install) and skips
// without one; MK_RELEASE_KEEP keeps the archive for a clean-machine check.

package main

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"mk/internal/release"
)

// TestBuildRealBundle builds the real release bundle end to end on this
// host: `build` from an mk built from this tree with -trimpath, the host's
// LLVM release directory, the seed (kits/web at a synthetic tag served by
// the test's git server), and doc/ with the std documentation the
// archive's own mk writes; twice, to the same bytes. The licences are
// synthetic. The version is the one the mk reports (no test names the
// running version). The unpacked archive's mk finds its LLVM by the
// bundle rule.
//
// LLVM is MK_RELEASE_LLVM, else C:\Program Files\LLVM on Windows and
// /usr/lib/llvm-21 on Linux; without one the test SKIPs. MK_RELEASE_KEEP
// names a directory the archive is copied into, for the clean-machine
// check (tools/release/cleancheck.ps1).
func TestBuildRealBundle(t *testing.T) {
	if testing.Short() {
		t.Skip("SKIP: -short (builds mk and two archives of about 90 MB)")
	}
	llvm := os.Getenv("MK_RELEASE_LLVM")
	if llvm == "" {
		llvm = `C:\Program Files\LLVM`
		if runtime.GOOS != "windows" {
			llvm = "/usr/lib/llvm-21"
		}
	}
	exe := ""
	if runtime.GOOS == "windows" {
		exe = ".exe"
	}
	if _, err := os.Stat(filepath.Join(llvm, "bin", "clang"+exe)); err != nil {
		t.Skipf("SKIP: no LLVM at %s (set MK_RELEASE_LLVM)", llvm)
	}
	repo, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()

	mk := filepath.Join(root, "mk"+exe)
	b := exec.Command(goTool(t), "build", "-trimpath", "-o", mk, "./cmd/mk")
	b.Dir = repo
	b.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := b.CombinedOutput(); err != nil {
		t.Fatalf("go build -trimpath ./cmd/mk: %v\n%s", err, out)
	}
	vt, err := mkVersion(mk)
	if err != nil {
		t.Fatal(err)
	}
	version, _, err := mkReports(vt)
	if err != nil {
		t.Fatal(err)
	}

	// The seed: kits/web at v0.1.0 of the synthetic repository.
	kits := filepath.Join(repo, "kits")
	tree, err := kitTree(filepath.Join(kits, "web"))
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{}
	for _, f := range tree.Files {
		files[f.Path] = string(f.Data)
	}
	gs := newGitServer(t)
	gs.tag("mk-test/web", "0.1.0", files)
	list := writeList(t, "web "+gs.source("mk-test/web")+" v0.1.0\n")
	seed := filepath.Join(root, "seed")
	t.Cleanup(func() { makeWritable(seed) })
	if _, err := buildSeed(seedJob{Kits: kits, List: list, Out: seed, Fetcher: seedFetcher(gs)}); err != nil {
		t.Fatal(err)
	}
	gs.srv.Close()

	llvmLic := filepath.Join(llvm, "LICENSE.TXT")
	if _, err := os.Stat(llvmLic); err != nil {
		llvmLic = filepath.Join(root, "LICENSE-LLVM.txt")
		os.WriteFile(llvmLic, []byte("SYNTHETIC: the installed LLVM carries no LICENSE.TXT; a release takes the one of LLVM's release archive\n"), 0o644)
	}
	j := buildJob{MK: mk, LLVM: llvm, Version: version, License: syntheticLicense(t), LLVMLicense: llvmLic,
		Repo: repo, Seed: seed, Commit: "release-test"}
	var outs [2][]byte
	var p string
	for i := range outs {
		j.Out = filepath.Join(root, "out", string(rune('a'+i)))
		var n int64
		if p, n, err = buildArchive(j); err != nil {
			t.Fatal(err)
		}
		t.Logf("build %d: %s, %d bytes", i+1, filepath.Base(p), n)
		outs[i], _ = os.ReadFile(p)
	}
	if !bytes.Equal(outs[0], outs[1]) {
		t.Fatal("two builds of the bundle differ")
	}
	if keep := os.Getenv("MK_RELEASE_KEEP"); keep != "" {
		os.MkdirAll(keep, 0o755)
		if err := os.WriteFile(filepath.Join(keep, filepath.Base(p)), outs[0], 0o644); err != nil {
			t.Fatal(err)
		}
	}

	host := release.HostOf(runtime.GOOS, runtime.GOARCH)
	dest := filepath.Join(root, "unpacked")
	if err := release.Extract(p, dest, release.TopDir(version, host), release.Limits{}, nil); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"bin/mk" + exe, "llvm/bin/clang" + exe, "llvm/bin/lld" + exe, "seed/first-party.txt",
		"doc/reference.md", "doc/std-api.md", "CHANGELOG.md", "LICENSE", "LICENSE-LLVM.txt", "RELEASE.txt"} {
		if _, err := os.Stat(filepath.Join(dest, filepath.FromSlash(want))); err != nil {
			t.Errorf("the bundle lacks %s", want)
		}
	}
	if _, err := os.Stat(filepath.Join(dest, "llvm", "bin", "llvm-lib"+exe)); err == nil {
		t.Error("the bundle carries llvm-lib (it is not shipped)")
	}
	md, _ := os.ReadFile(filepath.Join(dest, "doc", "std-api.md"))
	for _, w := range []string{"## Module `std.atomic`", "## Module `std.net`", "### `Atomic.FetchAdd`"} {
		if !bytes.Contains(md, []byte(w)) {
			t.Errorf("doc/std-api.md lacks %q", w)
		}
	}
	// The unpacked mk uses its own LLVM: MK_LLVM and PATH cannot point
	// elsewhere, and its version line names the bundle rule.
	cmd := exec.Command(filepath.Join(dest, "bin", "mk"+exe), "version")
	cmd.Env = append(throwawayEnv(filepath.Join(root, "env")), "PATH="+filepath.Join(dest, "bin"))
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, io.Discard
	if err := cmd.Run(); err != nil || !strings.Contains(out.String(), "(bundle)") {
		t.Errorf("the unpacked mk version: %v\n%s", err, out.String())
	}
}
