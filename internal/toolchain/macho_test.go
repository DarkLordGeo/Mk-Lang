// Why: a macOS link must be one exact command line and read a libSystem
// stub whose bytes depend only on the names it exports.
// What: TestMachOArgs, TestMachOArgsRefused, TestTBD and TestFormatOfMacOS.
// How: machoArgs and TBD are compared with literal expectations.

package toolchain

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestFormatOfMacOS(t *testing.T) {
	for _, tr := range []string{"arm64-apple-macosx11.0.0", "x86_64-apple-macosx11.0.0"} {
		if f := FormatOf(tr); f != MachO {
			t.Errorf("FormatOf(%s) = %v, want Mach-O", tr, f)
		}
	}
	if a := MachOArch("arm64-apple-macosx11.0.0"); a != "arm64" {
		t.Errorf("MachOArch arm64: %q", a)
	}
	if a := MachOArch("x86_64-apple-macosx11.0.0"); a != "x86_64" {
		t.Errorf("MachOArch x86_64: %q", a)
	}
}

// TestMachOArgs: dev is the plain line with no debug map (-S); release
// adds -dead_strip, the local-symbol strip without Debug, and in-process
// ThinLTO at -O3.
func TestMachOArgs(t *testing.T) {
	d := t.TempDir()
	o, p, s, tbd := filepath.Join(d, "hello"), filepath.Join(d, "p0000.o"), filepath.Join(d, "support.o"), filepath.Join(d, "libSystem.tbd")
	j := LinkJob{Format: MachO, MachOArch: "arm64", Output: o, Objects: []string{p, s}, Libs: []string{tbd}}
	got, err := linkArgs(j)
	if err != nil {
		t.Fatal(err)
	}
	want := "-arch arm64 -platform_version macos 11.0 11.0 -e _main -o hello " + p + " " + s + " " + tbd + " -S"
	if s := strings.Join(got, " "); s != want {
		t.Errorf("dev:\n got %s\nwant %s", s, want)
	}
	j.Release, j.LTO, j.Jobs = true, true, 4
	got, err = linkArgs(j)
	if err != nil {
		t.Fatal(err)
	}
	line := strings.Join(got, " ")
	if w := " -S -dead_strip -x --lto-O3 --thinlto-jobs=4 -mllvm -align-loops=32"; !strings.Contains(line, w) {
		t.Errorf("release: no %q in %s", w, line)
	}
	j.Debug = true
	got, _ = linkArgs(j)
	if s := strings.Join(got, " "); strings.Contains(s, " -x") {
		t.Errorf("release --debug strips the symbols: %s", s)
	}
}

// TestMachOArgsRefused: a Mach-O job with no arch, no stub, or an ELF or
// COFF option is a job error.
func TestMachOArgsRefused(t *testing.T) {
	d := t.TempDir()
	base := LinkJob{Format: MachO, MachOArch: "x86_64", Output: filepath.Join(d, "a"), Objects: []string{filepath.Join(d, "a.o")},
		Libs: []string{filepath.Join(d, "libSystem.tbd")}}
	for name, mut := range map[string]func(*LinkJob){
		"no arch":    func(j *LinkJob) { j.MachOArch = "" },
		"no stub":    func(j *LinkJob) { j.Libs = nil },
		"interp":     func(j *LinkJob) { j.Interp = filepath.Join(d, "ld.so") },
		"stack":      func(j *LinkJob) { j.StackReserve = 1 << 20 },
		"free":       func(j *LinkJob) { j.Free = true },
		"load flags": func(j *LinkJob) { j.DependentLoadFlags = 0xA00 },
	} {
		j := base
		mut(&j)
		if _, err := linkArgs(j); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

// TestTBD: the stub names libSystem's install path for both macOS
// architectures and exports each name once, with Mach-O's '_', sorted,
// plus dyld_stub_binder; the order of the input does not matter.
func TestTBD(t *testing.T) {
	a := TBD([]string{"write", "__error", "write", "mmap"})
	b := TBD([]string{"mmap", "__error", "write"})
	if a != b {
		t.Errorf("the stub depends on the order or repeats of its names:\n%s\n%s", a, b)
	}
	for _, w := range []string{"--- !tapi-tbd\ntbd-version:     4\n", "targets:         [ x86_64-macos, arm64-macos ]",
		"install-name:    '/usr/lib/libSystem.B.dylib'", "symbols:     [ ___error,\n                   _mmap,\n                   _write,\n                   dyld_stub_binder ]\n...\n"} {
		if !strings.Contains(a, w) {
			t.Errorf("no %q in\n%s", w, a)
		}
	}
}
