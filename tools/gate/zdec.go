// Why: where Go's zstd decoder and std.compress disagree on a frame, std's
// zstd tests (c2ZRef in std/std-compress_zstd_test.go) ask a third,
// reference decoder; the repository holds no C, so the gate builds that
// decoder, zdec, from pinned files outside it.
//
// What: zdecStep (build or reuse zdec, then self-check it), zdecEnv and
// zdecFor (MK_ZDEC for the go test commands that run ./std), findClang,
// cHeaders, msvcEnv, zdecKey, unpackZstd and zdecSelfCheck over zdecChecks.
//
// How: the inputs live in the evidence directory (evidenceDir): the pinned
// zstd 1.5.7 release tarball, whose decompression sources are unpacked, and
// the driver zdec.c beside it in vendor/zdec there; both must match their
// SHA-256 pins before clang builds anything. The step runs at most once per
// gate, before the first command that runs ./std, and ends one of three ways:
//
//	pass  zdec was built, or found already built, under <work>/zdec/<key>,
//	      and gave the expected verdict on each frame of zdecChecks
//	skip  a prerequisite is absent: the evidence files, clang, or on
//	      Windows the MSVC and Windows SDK headers and libraries; the zstd
//	      tests then run with MK_ZDEC unset, and the reason is in the report
//	fail  a prerequisite is present but wrong: a SHA-256 other than the
//	      pin, a tarball without the sources, a clang that fails, or a
//	      self-check verdict other than the expected one; MK_ZDEC stays
//	      unset and the gate fails
//
// MK_ZDEC reaches only the go test commands whose packages include ./std
// (the test, Linux, timing, math and fuzz steps); a caller's MK_ZDEC never
// reaches a child (cleared). The key is a hash of the pins, clang's path
// and version, the command line and the C environment (msvcEnv's, or the
// caller's INCLUDE and LIB), so a quick-tier gate reuses the same file (go
// test's cache records the files a test stats, and a rebuilt zdec would
// rerun ./std) and a changed input builds a new one.

package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
)

// The pinned inputs, relative to the evidence directory. zstdTarSHA is the
// release's own zstd-1.5.7.tar.gz.sha256 (vendor/zstd-1.5.7/SOURCE.txt);
// zdecSrcSHA is vendor/zdec/SHA256SUMS's line for zdec.c. A changed driver
// changes this pin in the same change.
const (
	zstdTarRel = "vendor/zstd-1.5.7/zstd-1.5.7.tar.gz"
	zstdTarSHA = "eb33e51f49a15e023950cd7825ca74a4a2b43db8354825ac24fc1b7ee09e6fa3"
	zdecSrcRel = "vendor/zdec/zdec.c"
	zdecSrcSHA = "1d6edf1d33a739581fc5a6fae207688ed720a2820df5bf4844995a59f3db8c1d"
	// zstdTarRoot is the tarball's top directory.
	zstdTarRoot = "zstd-1.5.7/"
)

// zstdSources are the C files of libzstd's decoder that zdec links, under
// lib/ of the tarball (no compressor, no legacy formats, no threads).
var zstdSources = []string{
	"common/debug.c", "common/entropy_common.c", "common/error_private.c", "common/fse_decompress.c",
	"common/xxhash.c", "common/zstd_common.c",
	"decompress/huf_decompress.c", "decompress/zstd_ddict.c", "decompress/zstd_decompress.c",
	"decompress/zstd_decompress_block.c",
}

// zdecFlags are clang's flags before the sources: the portable C decoder
// (no amd64 assembly), RFC 8878 frames only (no legacy ones), no trace
// hooks.
var zdecFlags = []string{"-O2", "-DZSTD_DISABLE_ASM", "-DZSTD_LEGACY_SUPPORT=0", "-DZSTD_TRACE=0"}

// zdecCheck is one frame of the self-check and the verdict zdec must give.
type zdecCheck struct {
	name  string
	frame []byte
	want  string
}

// zdecChecks: a whole frame, the same frame cut by one byte, a block of the
// reserved type, and a window just over and at zdec's 8 MiB limit (the
// limit std's tests give std.compress, so a zdec built without it fails
// here). Frames per RFC 8878 section 3.1.1.
var zdecChecks = []zdecCheck{
	// Single segment, a 1-byte content size of 5, one last raw block of 5.
	{"whole frame", []byte{0x28, 0xB5, 0x2F, 0xFD, 0x20, 0x05, 0x29, 0x00, 0x00, 'h', 'e', 'l', 'l', 'o'}, "ok"},
	{"cut frame", []byte{0x28, 0xB5, 0x2F, 0xFD, 0x20, 0x05, 0x29, 0x00, 0x00, 'h', 'e', 'l', 'l'}, "truncated"},
	{"reserved block type", []byte{0x28, 0xB5, 0x2F, 0xFD, 0x20, 0x05, 0x07, 0x00, 0x00}, "err"},
	// Window descriptor 0x70: 2^24 bytes; then an empty last raw block.
	{"window of 16 MiB", []byte{0x28, 0xB5, 0x2F, 0xFD, 0x00, 0x70, 0x01, 0x00, 0x00}, "err"},
	{"window of 8 MiB", []byte{0x28, 0xB5, 0x2F, 0xFD, 0x00, 0x68, 0x01, 0x00, 0x00}, "ok"},
}

// zdecStepName is the step's name in the report.
const zdecStepName = "zdec (reference zstd decoder for ./std)"

// zdecs holds each gate run's zdec: built at most once, by the first
// command that needs it; path is "" after a skip or a failure. A gate whose
// commands g.runCmd answers (this package's step tests) builds none: it
// has the entry a test put here, or none.
var zdecs = struct {
	sync.Mutex
	m map[*gate]*zdecRun
}{m: map[*gate]*zdecRun{}}

type zdecRun struct{ path string }

// zdecEnv is MK_ZDEC for a command with argv: zdec's path when argv's
// packages include ./std and zdec was built, else nil. The first such
// command builds it.
func (g *gate) zdecEnv(argv []string) []string {
	if !slices.Contains(argv, stdPkg) {
		return nil
	}
	zdecs.Lock()
	defer zdecs.Unlock()
	r, ok := zdecs.m[g]
	if !ok {
		if g.runCmd != nil {
			return nil
		}
		r = &zdecRun{path: g.zdecStep()}
		zdecs.m[g] = r
	}
	if r.path == "" {
		return nil
	}
	return []string{"MK_ZDEC=" + r.path}
}

// zdecFor builds zdec before a step's runs when one of them runs ./std,
// so its step comes before that step in the report.
func (g *gate) zdecFor(runs []goTestRun) {
	for _, r := range runs {
		if slices.Contains(r.pkgs, stdPkg) {
			g.zdecEnv([]string{stdPkg})
			return
		}
	}
}

// zdecStep is the step: it returns zdec's path, or "" after a skip or a
// failure, which it records.
func (g *gate) zdecStep() string {
	s := g.newStep(zdecStepName)
	skip := func(why string) string {
		s.Status, s.Reason = "skip", why
		fmt.Printf("   skipped: %s; the zstd tests run with MK_ZDEC unset\n", why)
		return ""
	}
	failed := func(why string) string {
		s.Status, s.Reason = "fail", why // over plain's "exit status N"
		fmt.Printf("   failed: %s\n", why)
		return ""
	}
	ev := evidenceDir(g.root)
	tarPath := filepath.Join(ev, filepath.FromSlash(zstdTarRel))
	srcPath := filepath.Join(ev, filepath.FromSlash(zdecSrcRel))
	for _, p := range []string{tarPath, srcPath} {
		if _, err := os.Stat(p); err != nil {
			return skip(fmt.Sprintf("%s is absent (the evidence directory is outside the repository; set MK_EVIDENCE)", p))
		}
	}
	for _, c := range []struct{ path, want string }{{tarPath, zstdTarSHA}, {srcPath, zdecSrcSHA}} {
		got, err := fileSHA256(c.path)
		if err != nil {
			return failed(err.Error())
		}
		if got != c.want {
			return failed(fmt.Sprintf("%s has SHA-256 %s, not the pinned %s (tools/gate/zdec.go)", c.path, got, c.want))
		}
	}
	clang, why := findClang(os.Getenv("MK_LLVM"), os.Getenv("PATH"))
	if clang == "" {
		return skip(why)
	}
	version, err := clangVersion(clang)
	if err != nil {
		return failed(err.Error())
	}
	var cEnv []string
	if runtime.GOOS == "windows" {
		var why string
		if cEnv, why = msvcEnv(os.Getenv, msvcBases()); why != "" {
			return skip(why)
		}
	} else if skipWhy, failWhy := cHeaders(clang); skipWhy != "" {
		return skip(skipWhy)
	} else if failWhy != "" {
		return failed(failWhy)
	}
	key := zdecKey(clang, version, zdecKeyEnv(os.Getenv, cEnv))
	dir := filepath.Join(g.work, "zdec", key)
	exe := filepath.Join(dir, "zdec"+exeSuffix())
	s.Notes = append(s.Notes, "clang "+clang+": "+version)
	if _, err := os.Stat(exe); err == nil {
		s.Notes = append(s.Notes, "built before, with the same key: "+exe)
	} else {
		src := filepath.Join(dir, "src")
		if err := unpackZstd(tarPath, src); err != nil {
			return failed(err.Error())
		}
		tmp := filepath.Join(dir, "zdec-build"+exeSuffix())
		os.Remove(tmp)
		// noZdec: this runs under zdecs' lock, which a nested zdecEnv
		// would take again.
		if _, code := g.plain(s, spec{argv: zdecArgv(clang, srcPath, src, tmp), set: cEnv, noZdec: true}); code != 0 {
			return failed(fmt.Sprintf("clang exit status %d", code))
		}
		if err := os.Rename(tmp, exe); err != nil {
			return failed(err.Error())
		}
	}
	if err := zdecSelfCheck(exe); err != nil {
		return failed(err.Error())
	}
	s.Env = append(s.Env, "MK_ZDEC="+exe+" for each go test of ./std")
	fmt.Printf("   MK_ZDEC=%s for each go test of ./std\n", exe)
	return exe
}

// evidenceDir is $MK_EVIDENCE, else Mk-evidence beside the repository (as
// tools/bench finds it).
func evidenceDir(root string) string {
	if e := os.Getenv("MK_EVIDENCE"); e != "" {
		return e
	}
	return filepath.Join(filepath.Dir(root), "Mk-evidence")
}

func fileSHA256(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// findClang is the first directory holding clang of: the one MK_LLVM names,
// the host's default directories, then PATH's. That is internal/toolchain's
// rule order (Discover's EnvDir, DefaultDir and PathDir rules; an MK_LLVM
// without clang falls through as there), but tools/gate may not import
// internal/toolchain (internal/archtest). Unlike Discover it does not prefer,
// within a rule, a directory that also holds the linker. why lists what it
// searched when it finds none.
func findClang(mkLLVM, pathList string) (clang, why string) {
	name := "clang" + exeSuffix()
	defaults := clangDefaults(runtime.GOOS)
	dirs := append(append([]string{mkLLVM}, defaults...), filepath.SplitList(pathList)...)
	for _, d := range dirs {
		if p := filepath.Join(d, name); d != "" && isFile(p) {
			return p, ""
		}
	}
	return "", "no " + name + " in MK_LLVM (" + mkLLVM + "), " + strings.Join(defaults, ", ") + " or PATH"
}

// clangDefaults is internal/toolchain's DefaultDirs for goos, written out:
// on Linux /usr/lib/llvm-N/bin for N from MaxTestedMajor down to MinMajor
// (21 alone), then /usr/local/bin and /usr/bin. TestClangDefaults
// reads toolchain's constants and fails when they move.
func clangDefaults(goos string) []string {
	switch goos {
	case "windows":
		return []string{`C:\Program Files\LLVM\bin`}
	case "linux":
		return []string{"/usr/lib/llvm-21/bin", "/usr/local/bin", "/usr/bin"}
	}
	return []string{"/usr/local/bin", "/usr/bin"}
}

// cHeaders preprocesses the C headers zdec.c includes with clang (off
// Windows; no output kept). When clang reports a header it cannot find,
// skip says why: a host with clang but no libc headers skips the step
// instead of failing it, since Mk itself needs no C headers. Any other
// failure of clang is fail's, which fails the step, as any clang failure
// does. Both are "" when the headers are found.
func cHeaders(clang string) (skip, fail string) {
	cmd := exec.Command(clang, "-E", "-x", "c", "-o", os.DevNull, "-")
	cmd.Stdin = strings.NewReader("#include <stdio.h>\n#include <stdlib.h>\n#include <string.h>\n")
	var errb bytes.Buffer
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		msg, _, _ := strings.Cut(strings.TrimSpace(errb.String()), "\n")
		if strings.Contains(errb.String(), "file not found") {
			return fmt.Sprintf("%s finds no C library headers (%v: %s)", clang, err, msg), ""
		}
		return "", fmt.Sprintf("%s -E of the C library headers failed (%v: %s)", clang, err, msg)
	}
	return "", ""
}

func isFile(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.Mode().IsRegular()
}

// clangVersion is the first line of `clang --version`.
func clangVersion(clang string) (string, error) {
	out, err := exec.Command(clang, "--version").Output()
	if err != nil {
		return "", fmt.Errorf("%s --version: %v", clang, err)
	}
	line, _, _ := strings.Cut(strings.ReplaceAll(string(out), "\r\n", "\n"), "\n")
	if !strings.Contains(line, "clang version") {
		return "", fmt.Errorf("%s --version printed %q, not a clang version", clang, line)
	}
	return strings.TrimSpace(line), nil
}

// msvcBases are the directories msvcEnv searches on Windows: Program Files
// and Program Files (x86).
func msvcBases() []string {
	pf, pf86 := os.Getenv("ProgramFiles"), os.Getenv("ProgramFiles(x86)")
	if pf == "" {
		pf = `C:\Program Files`
	}
	if pf86 == "" {
		pf86 = `C:\Program Files (x86)`
	}
	return []string{pf, pf86}
}

// msvcEnv is INCLUDE and LIB for clang on Windows, which does not find the C
// library's headers alone (tools/bench's columns.go sets the same): nil when
// the gate's environment has both; else from the newest MSVC tools under
// <base>\Microsoft Visual Studio\<year>\<edition> and the newest Windows 10
// SDK under <base>\Windows Kits\10 of bases, each with the files zdec needs.
// why says what is missing when there is none.
func msvcEnv(getenv func(string) string, bases []string) (env []string, why string) {
	if getenv("INCLUDE") != "" && getenv("LIB") != "" {
		return nil, ""
	}
	// vc is the MSVC tools directory; inc and lib are the SDK's Include
	// and Lib directories of one version.
	var vc, inc, lib string
	var vcVer, sdkVer []int
	for _, b := range bases {
		ds, _ := filepath.Glob(filepath.Join(b, "Microsoft Visual Studio", "*", "*", "VC", "Tools", "MSVC", "*"))
		for _, d := range ds {
			v := dotted(filepath.Base(d))
			if isFile(filepath.Join(d, "include", "vcruntime.h")) && isFile(filepath.Join(d, "lib", "x64", "libcmt.lib")) && newer(v, vcVer) {
				vc, vcVer = d, v
			}
		}
		kit := filepath.Join(b, "Windows Kits", "10")
		ds, _ = filepath.Glob(filepath.Join(kit, "Include", "*"))
		for _, d := range ds {
			v := dotted(filepath.Base(d))
			l := filepath.Join(kit, "Lib", filepath.Base(d))
			if isFile(filepath.Join(d, "ucrt", "stdio.h")) && isFile(filepath.Join(l, "ucrt", "x64", "libucrt.lib")) &&
				isFile(filepath.Join(l, "um", "x64", "kernel32.lib")) && newer(v, sdkVer) {
				inc, lib, sdkVer = d, l, v
			}
		}
	}
	if vc == "" || inc == "" {
		return nil, "no MSVC tools or Windows SDK with the C library for clang under " + strings.Join(bases, " or ") + ", and INCLUDE and LIB are not set"
	}
	return []string{
		"INCLUDE=" + strings.Join([]string{filepath.Join(vc, "include"), filepath.Join(inc, "ucrt"), filepath.Join(inc, "um"), filepath.Join(inc, "shared")}, ";"),
		"LIB=" + strings.Join([]string{filepath.Join(vc, "lib", "x64"), filepath.Join(lib, "ucrt", "x64"), filepath.Join(lib, "um", "x64")}, ";"),
	}, ""
}

// dotted is a version directory's name as numbers ("10.0.26100.0").
func dotted(s string) []int {
	var v []int
	for _, f := range strings.Split(s, ".") {
		n, err := strconv.Atoi(f)
		if err != nil {
			return nil
		}
		v = append(v, n)
	}
	return v
}

// newer reports whether version a is above b; a name that is not a
// version (nil) is never newer.
func newer(a, b []int) bool {
	if a == nil {
		return false
	}
	return b == nil || slices.Compare(a, b) > 0
}

// zdecArgv is clang's command line: zdec.c and zstdSources from src (the
// unpacked lib/) into out, linked by lld on Windows.
func zdecArgv(clang, driver, src, out string) []string {
	lib := filepath.Join(src, "lib")
	argv := append([]string{clang}, zdecFlags...)
	argv = append(argv, "-I"+lib, "-I"+filepath.Join(lib, "common"))
	if runtime.GOOS == "windows" {
		argv = append(argv, "-fuse-ld=lld")
	}
	argv = append(argv, driver)
	for _, f := range zstdSources {
		argv = append(argv, filepath.Join(lib, filepath.FromSlash(f)))
	}
	return append(argv, "-o", out)
}

// zdecKeyEnv is the C environment the key hashes: cEnv, or on Windows,
// when msvcEnv left the caller's INCLUDE and LIB in place (cEnv nil),
// those two, so a caller's other headers build a new zdec.
func zdecKeyEnv(getenv func(string) string, cEnv []string) []string {
	if cEnv != nil || runtime.GOOS != "windows" {
		return cEnv
	}
	return []string{"INCLUDE=" + getenv("INCLUDE"), "LIB=" + getenv("LIB")}
}

// zdecKey names the build directory: a hash of the pins, clang and the
// command line (with the build directory left out, since it holds the
// key), 16 hex digits.
func zdecKey(clang, version string, cEnv []string) string {
	h := sha256.New()
	fmt.Fprintf(h, "zdec 1\n%s\n%s\n%s\n%s\n%s/%s\n", zstdTarSHA, zdecSrcSHA, clang, version, runtime.GOOS, runtime.GOARCH)
	for _, a := range zdecArgv("clang", "zdec.c", "SRC", "OUT") {
		fmt.Fprintf(h, "%s\n", a)
	}
	for _, kv := range cEnv {
		fmt.Fprintf(h, "%s\n", kv)
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// unpackZstd writes the tarball's lib/zstd.h, lib/zstd_errors.h,
// lib/common/ and lib/decompress/ under dir/lib, regular files only, and
// checks that every file of zstdSources came.
func unpackZstd(tarPath, dir string) error {
	f, err := os.Open(tarPath)
	if err != nil {
		return err
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("%s: %v", tarPath, err)
	}
	tr := tar.NewReader(zr)
	got := map[string]bool{}
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("%s: %v", tarPath, err)
		}
		rel, ok := strings.CutPrefix(h.Name, zstdTarRoot+"lib/")
		if !ok || h.Typeflag != tar.TypeReg || !zstdWanted(rel) {
			continue
		}
		out := filepath.Join(dir, "lib", filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
			return err
		}
		b, err := io.ReadAll(tr)
		if err != nil {
			return fmt.Errorf("%s: %s: %v", tarPath, h.Name, err)
		}
		if err := os.WriteFile(out, b, 0o644); err != nil {
			return err
		}
		got[rel] = true
	}
	for _, s := range zstdSources {
		if !got[s] {
			return fmt.Errorf("%s holds no %slib/%s", tarPath, zstdTarRoot, s)
		}
	}
	return nil
}

// zstdWanted reports whether rel (a path under lib/) is one zdec's build
// reads: the two public headers and the files directly in common/ and
// decompress/.
func zstdWanted(rel string) bool {
	if rel == "zstd.h" || rel == "zstd_errors.h" {
		return true
	}
	d, base := path.Split(rel)
	return (d == "common/" || d == "decompress/") && base != "" && base != "." && base != ".."
}

// zdecSelfCheck runs exe on each frame of zdecChecks and compares the first
// word of its answer with the verdict wanted.
func zdecSelfCheck(exe string) error {
	for _, c := range zdecChecks {
		cmd := exec.Command(exe)
		cmd.Stdin = bytes.NewReader(c.frame)
		var errb bytes.Buffer
		cmd.Stderr = &errb
		out, err := cmd.Output()
		if err != nil {
			return fmt.Errorf("zdec self-check, %s: %v %s", c.name, err, strings.TrimSpace(errb.String()))
		}
		f := strings.Fields(string(out))
		if len(f) == 0 || f[0] != c.want {
			return fmt.Errorf("zdec self-check, %s: answered %q, want %s", c.name, strings.TrimSpace(string(out)), c.want)
		}
	}
	return nil
}
