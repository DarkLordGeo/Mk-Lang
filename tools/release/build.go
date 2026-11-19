// Why: a release archive must hold exactly the mk that was tested, the LLVM
// tools it was tested with, and nothing else, and two builds from the same
// inputs must give the same bytes.
//
// What: runBuild and buildArchive (the `build` subcommand), mkFacts,
// llvmMajor, checkMk and mkReports (what the mk and clang say they are),
// addTree and releaseNotes.
//
// How: the host's archive is made from an mk built with -trimpath and an
// LLVM release directory; the mk's version must be -version and its
// tested LLVM major clang's; entries are sorted and times fixed so two runs give
// the same bytes.

package main

import (
	"debug/buildinfo"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"mk/internal/release"
)

// buildJob is one archive's inputs.
type buildJob struct {
	MK          string // the mk executable, built with -trimpath
	LLVM        string // the LLVM release directory: bin/, lib/clang/<major>/include/
	Version     string // the release version, "0.1.0"
	Out         string // the directory the archive is written into
	License     string // mk's licence file
	Changelog   string // the release notes, CHANGELOG.md
	LLVMLicense string // LLVM's licence file (LICENSE.TXT of the release)
	Doc         string // a directory copied to doc/; "" means the reference from Repo
	Repo        string // the repository root, for the reference
	Seed        string // a directory copied to seed/ (the seed command's output); "" for none
	Commit      string // mk's commit; "" reads it from mk's build information
	// LLVMLib keeps llvm-lib in a Windows bundle. lld-link's /lib mode
	// writes the same archive byte for byte, so it is off by default.
	LLVMLib bool
}

// clangVersion runs `<clang> --version` (tests replace it).
var clangVersion = func(clang string) (string, error) {
	out, err := exec.Command(clang, "--version").Output()
	return string(out), err
}

func runBuild(args []string, stdout, stderr io.Writer) int {
	fs := flags("build", stderr)
	var j buildJob
	fs.StringVar(&j.MK, "mk", "", "the mk executable, built with go build -trimpath")
	fs.StringVar(&j.LLVM, "llvm", "", "the LLVM release directory (holding bin/ and lib/clang/)")
	fs.StringVar(&j.Version, "version", "", "the release version, for example 0.1.0")
	fs.StringVar(&j.Out, "out", "", "the directory to write the archive into")
	fs.StringVar(&j.License, "license", "", "mk's licence file (default <repo>/LICENSE)")
	fs.StringVar(&j.LLVMLicense, "llvm-license", "", "LLVM's licence file (default <llvm>/LICENSE.TXT)")
	fs.StringVar(&j.Changelog, "changelog", "", "the release notes (default <repo>/CHANGELOG.md)")
	fs.StringVar(&j.Doc, "doc", "", "a directory for doc/ (default: <repo>/doc/**/*.md and std's generated documentation)")
	fs.StringVar(&j.Repo, "repo", ".", "the repository root")
	fs.StringVar(&j.Seed, "seed", "", "a directory for seed/")
	fs.StringVar(&j.Commit, "commit", "", "mk's commit (default: mk's VCS stamp)")
	fs.BoolVar(&j.LLVMLib, "llvm-lib", false, "keep llvm-lib in a Windows bundle")
	if !parse(fs, args, stderr, "mk", "llvm", "version", "out") {
		return 2
	}
	p, n, err := buildArchive(j)
	if err != nil {
		return fail(stderr, "build", err)
	}
	fmt.Fprintf(stdout, "wrote %s (%d bytes)\n", p, n)
	return 0
}

// mkFacts reads the mk executable's build information: it must be built
// with -trimpath, and its GOOS and GOARCH give the host.
func mkFacts(mk string) (host, commit string, err error) {
	bi, err := buildinfo.ReadFile(mk)
	if err != nil {
		return "", "", fmt.Errorf("%s: no Go build information: %v", mk, err)
	}
	set := map[string]string{}
	for _, s := range bi.Settings {
		set[s.Key] = s.Value
	}
	if set["-trimpath"] != "true" {
		return "", "", fmt.Errorf("%s was not built with -trimpath (go build -trimpath -o ... ./cmd/mk)", mk)
	}
	host = release.HostOf(set["GOOS"], set["GOARCH"])
	if host == "" {
		return "", "", fmt.Errorf("%s is built for %s/%s, which no release carries", mk, set["GOOS"], set["GOARCH"])
	}
	commit = set["vcs.revision"]
	if set["vcs.modified"] == "true" {
		commit += "+dirty"
	}
	return host, commit, nil
}

// llvmMajor reads clang's version line: "clang version 21.1.8" gives 21
// and "21.1.8".
func llvmMajor(line string) (int, string, error) {
	first, _, _ := strings.Cut(line, "\n")
	first = strings.TrimSpace(first)
	_, rest, ok := strings.Cut(first, "clang version ")
	if !ok {
		return 0, "", fmt.Errorf("not a clang version line: %q", first)
	}
	end := 0
	for end < len(rest) && (rest[end] == '.' || rest[end] >= '0' && rest[end] <= '9') {
		end++
	}
	v := rest[:end]
	maj, _, _ := strings.Cut(v, ".")
	n, err := strconv.Atoi(maj)
	if err != nil {
		return 0, "", fmt.Errorf("no version number in %q", first)
	}
	return n, v, nil
}

// addTree adds every regular file under dir to entries under prefix,
// refusing links and anything else.
func addTree(entries []release.Entry, dir, prefix string, exec bool) ([]release.Entry, error) {
	// The root itself may be a link (Ubuntu's /usr/lib/llvm-21/lib/clang/21
	// points into /usr/lib/clang): walk what it names. Links below it are
	// still refused.
	if r, err := filepath.EvalSymlinks(dir); err == nil {
		dir = r
	}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("%s is not a regular file", p)
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		entries = append(entries, release.Entry{Name: path.Join(prefix, filepath.ToSlash(rel)), Path: p, Exec: exec})
		return nil
	})
	return entries, err
}

func existing(p, what string) error {
	fi, err := os.Stat(p)
	if err != nil {
		return fmt.Errorf("%s: %v", what, err)
	}
	if !fi.Mode().IsRegular() {
		return fmt.Errorf("%s: %s is not a regular file", what, p)
	}
	return nil
}

// buildArchive writes j's archive into j.Out and returns its path and size.
func buildArchive(j buildJob) (string, int64, error) {
	if _, err := release.ParseVersion(j.Version); err != nil {
		return "", 0, err
	}
	j.Version = strings.TrimPrefix(j.Version, "v")
	host, commit, err := mkFacts(j.MK)
	if err != nil {
		return "", 0, err
	}
	if j.Commit != "" {
		commit = j.Commit
	}
	if commit == "" {
		return "", 0, errors.New("mk carries no VCS stamp; pass -commit")
	}
	exe := ""
	if host == release.HostWindows {
		exe = ".exe"
	}
	if j.License == "" {
		j.License = filepath.Join(j.Repo, "LICENSE")
	}
	if j.LLVMLicense == "" {
		j.LLVMLicense = filepath.Join(j.LLVM, "LICENSE.TXT")
	}
	if j.Changelog == "" {
		j.Changelog = filepath.Join(j.Repo, "CHANGELOG.md")
	}
	for _, c := range [][2]string{{j.License, "mk's licence (-license)"}, {j.LLVMLicense, "LLVM's licence (-llvm-license)"}, {j.Changelog, "the release notes (-changelog)"}} {
		if err := existing(c[0], c[1]); err != nil {
			return "", 0, err
		}
	}
	clang := filepath.Join(j.LLVM, "bin", "clang"+exe)
	line, err := clangVersion(clang)
	if err != nil {
		return "", 0, fmt.Errorf("%s --version: %v", clang, err)
	}
	major, llvmVersion, err := llvmMajor(line)
	if err != nil {
		return "", 0, err
	}
	if err := checkMk(j.MK, j.Version, major); err != nil {
		return "", 0, err
	}
	tools := []string{"clang", "lld"}
	if host == release.HostWindows && j.LLVMLib {
		tools = append(tools, "llvm-lib")
	}
	entries := []release.Entry{
		{Name: "bin/mk" + exe, Path: j.MK, Exec: true},
		{Name: "LICENSE", Path: j.License},
		{Name: "LICENSE-LLVM.txt", Path: j.LLVMLicense},
		{Name: "CHANGELOG.md", Path: j.Changelog},
	}
	var llvmFiles []release.Entry
	for _, t := range tools {
		p := filepath.Join(j.LLVM, "bin", t+exe)
		if err := existing(p, "LLVM's "+t); err != nil {
			return "", 0, err
		}
		llvmFiles = append(llvmFiles, release.Entry{Name: "llvm/bin/" + t + exe, Path: p, Exec: true})
	}
	inc := filepath.Join(j.LLVM, "lib", "clang", strconv.Itoa(major), "include")
	if llvmFiles, err = addTree(llvmFiles, inc, fmt.Sprintf("llvm/lib/clang/%d/include", major), false); err != nil {
		return "", 0, fmt.Errorf("clang's headers: %v", err)
	}
	entries = append(entries, llvmFiles...)
	if j.Doc != "" {
		if entries, err = addTree(entries, j.Doc, "doc", false); err != nil {
			return "", 0, err
		}
	} else {
		docs, err := docEntries(j.Repo, j.MK)
		if err != nil {
			return "", 0, err
		}
		entries = append(entries, docs...)
	}
	if j.Seed != "" {
		if entries, err = addTree(entries, j.Seed, "seed", false); err != nil {
			return "", 0, err
		}
	}
	notes, err := releaseNotes(j.Version, commit, host, llvmVersion, strings.TrimSpace(strings.SplitN(line, "\n", 2)[0]), llvmFiles)
	if err != nil {
		return "", 0, err
	}
	entries = append(entries, release.Entry{Name: "RELEASE.txt", Data: notes})
	if err := os.MkdirAll(j.Out, 0o755); err != nil {
		return "", 0, err
	}
	out := filepath.Join(j.Out, release.ArchiveName(j.Version, host))
	tmp, err := os.CreateTemp(j.Out, ".archive-*")
	if err != nil {
		return "", 0, err
	}
	defer os.Remove(tmp.Name())
	if err := release.WriteArchive(tmp, release.TopDir(j.Version, host), entries, host == release.HostWindows); err != nil {
		tmp.Close()
		return "", 0, err
	}
	if err := tmp.Close(); err != nil {
		return "", 0, err
	}
	if err := os.Rename(tmp.Name(), out); err != nil {
		return "", 0, err
	}
	fi, err := os.Stat(out)
	if err != nil {
		return "", 0, err
	}
	return out, fi.Size(), nil
}

// releaseNotes is RELEASE.txt: mk's version and commit, the host, LLVM's
// version and clang's version line, then the SHA-256 of each LLVM file in
// sha256sum's format, sorted by name.
func releaseNotes(version, commit, host, llvmVersion, clangLine string, llvmFiles []release.Entry) ([]byte, error) {
	var b strings.Builder
	fmt.Fprintf(&b, "mk %s\ncommit %s\nhost %s\nllvm %s\nclang %s\n\n", version, commit, host, llvmVersion, clangLine)
	files := append([]release.Entry(nil), llvmFiles...)
	sort.Slice(files, func(i, k int) bool { return files[i].Name < files[k].Name })
	for _, f := range files {
		h, err := release.SumFile(f.Path)
		if err != nil {
			return nil, err
		}
		fmt.Fprintf(&b, "%s  %s\n", h, f.Name)
	}
	return []byte(b.String()), nil
}

// mkVersion runs `<mk> version`. The archive's mk must run on the machine
// that builds the archive.
var mkVersion = func(mk string) (string, error) {
	out, err := exec.Command(mk, "version").Output()
	return string(out), err
}

// mkReports reads `mk version`'s output: the version on its first line
// ("mk <version> commit <commit>") and the LLVM major it was tested on
// (the line "llvm tested <major> ...").
func mkReports(text string) (version string, tested int, err error) {
	lines := strings.Split(text, "\n")
	first := strings.Fields(lines[0])
	if len(first) < 2 || first[0] != "mk" {
		return "", 0, fmt.Errorf("`mk version` printed no version line: %q", lines[0])
	}
	version = first[1]
	for _, l := range lines {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(l), "llvm tested "); ok {
			f := strings.Fields(rest)
			if len(f) > 0 {
				if n, err := strconv.Atoi(f[0]); err == nil {
					return version, n, nil
				}
			}
		}
	}
	return "", 0, errors.New("`mk version` printed no \"llvm tested <major>\" line (an mk too old to print it)")
}

// checkMk refuses an mk whose version is not the release's or whose
// tested LLVM major is not clang's.
func checkMk(mk, version string, clangMajor int) error {
	out, err := mkVersion(mk)
	if err != nil {
		return fmt.Errorf("%s version: %v", mk, err)
	}
	v, tested, err := mkReports(out)
	if err != nil {
		return err
	}
	if v != version {
		return fmt.Errorf("%s is mk %s, not -version %s", mk, v, version)
	}
	if tested != clangMajor {
		return fmt.Errorf("%s was tested on LLVM %d, but -llvm's clang is LLVM %d", mk, tested, clangMajor)
	}
	return nil
}
