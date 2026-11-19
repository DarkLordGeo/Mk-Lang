// Why: a Windows executable's DependentLoadFlags (where the loader looks for
// DLLs) lives in its load configuration, which the linker writes only when
// asked.
// What: TestLoadConfigArgs, TestLoadConfigLink and TestLoadConfigObject.
// How: checks the link line, then links real images and reads the load
// configuration directory's DependentLoadFlags (dependentLoadFlags).

package toolchain

import (
	"debug/pe"
	"encoding/binary"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

// TestLoadConfigArgs: DependentLoadFlags puts /dependentloadflag
// after /out on a COFF line; 0 leaves the line TestLinkArchives pins; an
// ELF link refuses it.
func TestLoadConfigArgs(t *testing.T) {
	out, o1 := absPath(t, "a.exe"), absPath(t, "1.o")
	got, err := linkArgs(LinkJob{Format: COFF, Output: out, Objects: []string{o1}, DependentLoadFlags: 0xA00})
	want := []string{"/nologo", "/nodefaultlib", "/entry:mk_start", "/subsystem:console", "/osversion:10.0", "/machine:x64", "/Brepro",
		"/out:" + out, "/dependentloadflag:0xA00", o1, "/opt:noicf"}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("COFF =\n%q, %v\nwant\n%q", got, err, want)
	}
	if _, err := linkArgs(LinkJob{Format: ELF, Output: out, Objects: []string{o1}, DependentLoadFlags: 0xA00}); err == nil {
		t.Error("an ELF link took DependentLoadFlags")
	}
}

// TestLoadConfigLink: a COFF link with DependentLoadFlags 0xA00
// has a load configuration directory whose DependentLoadFlags is 0xA00,
// in a plain, a ThinLTO (thinLink) and a --debug (pdbLink) link, and one
// without has no load configuration; a second link leaves no mklc- directory. The plain image
// runs (its exit code is the module's).
func TestLoadConfigLink(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("links and runs a Windows image")
	}
	tl := realTools(t)
	for _, v := range []struct {
		name       string
		v          smokeVariant
		debug      bool
		flags      uint16
		wantConfig bool
	}{
		{"none", smokeVariants[0], false, 0, false},
		{"O0", smokeVariants[0], false, 0xA00, true},
		{"thinlto", smokeVariants[2], false, 0xA00, true},
		{"debug", smokeVariants[0], true, 0xA00, true},
	} {
		t.Run(v.name, func(t *testing.T) {
			dir := t.TempDir()
			exe := loadCfgBuild(t, tl, v.v, v.debug, v.flags, dir)
			got, has := dependentLoadFlags(t, exe)
			if has != v.wantConfig || got != v.flags {
				t.Errorf("load configuration %v, DependentLoadFlags %#x; want %v, %#x", has, got, v.wantConfig, v.flags)
			}
			if v.debug {
				return
			}
			cmd := exec.Command(exe)
			err := cmd.Run()
			code := 0
			if ee, ok := err.(*exec.ExitError); ok {
				code = ee.ExitCode()
			} else if err != nil {
				t.Fatal(err)
			}
			if code != smokeExit {
				t.Errorf("exit %d, want %d", code, smokeExit)
			}
		})
	}
}

// loadCfgBuild is smokeBuild's Windows link with DependentLoadFlags and
// Debug set.
func loadCfgBuild(t *testing.T, tl *Tools, v smokeVariant, debug bool, flags uint16, dir string) string {
	t.Helper()
	exe := smokeBuild(t, tl, "x86_64-pc-windows-msvc", "smoke_windows.ll", v, dir)
	if flags == 0 && !debug {
		return exe
	}
	// Link again with the same inputs and the flag.
	job := LinkJob{Format: COFF, Objects: []string{filepath.Join(dir, "smoke.o")}, Libs: []string{filepath.Join(dir, "kernel32.lib")},
		Release: v.opt == O3, LTO: v.lto, Debug: debug, DependentLoadFlags: flags, Output: filepath.Join(dir, "flagged.exe")}
	if v.lto {
		job.LTOCacheDir, job.LTOCacheSize, job.Jobs = filepath.Join(dir, "thinlto"), 64<<20, 2
	}
	if err := tl.Link(t.Context(), job); err != nil {
		t.Fatal(err)
	}
	if err := tl.Link(t.Context(), job); err != nil { // a second link finds no stale mklc- directory
		t.Fatal(err)
	}
	if m, _ := filepath.Glob(filepath.Join(dir, "mklc-*")); len(m) > 0 {
		t.Errorf("left %q", m)
	}
	return job.Output
}

// dependentLoadFlags reads the image's load configuration directory
// (data directory 10) and its DependentLoadFlags (offset 0x4E of
// IMAGE_LOAD_CONFIG_DIRECTORY64).
func dependentLoadFlags(t *testing.T, exe string) (uint16, bool) {
	t.Helper()
	f, err := pe.Open(exe)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	oh, ok := f.OptionalHeader.(*pe.OptionalHeader64)
	if !ok || len(oh.DataDirectory) <= pe.IMAGE_DIRECTORY_ENTRY_LOAD_CONFIG {
		t.Fatal("no 64-bit optional header")
	}
	dd := oh.DataDirectory[pe.IMAGE_DIRECTORY_ENTRY_LOAD_CONFIG]
	if dd.VirtualAddress == 0 {
		return 0, false
	}
	for _, s := range f.Sections {
		if dd.VirtualAddress >= s.VirtualAddress && dd.VirtualAddress < s.VirtualAddress+s.VirtualSize {
			b, err := s.Data()
			if err != nil {
				t.Fatal(err)
			}
			off := dd.VirtualAddress - s.VirtualAddress
			if size := binary.LittleEndian.Uint32(b[off:]); size != loadConfigSize || dd.Size != loadConfigSize {
				t.Errorf("load configuration Size %#x, directory size %#x, want %#x", size, dd.Size, loadConfigSize)
			}
			return binary.LittleEndian.Uint16(b[off+0x4E:]), true
		}
	}
	t.Fatalf("load configuration RVA %#x in no section", dd.VirtualAddress)
	return 0, false
}

// TestLoadConfigObject: the object is a COFF x64 object whose one symbol
// is _load_config_used, external, in section 1.
func TestLoadConfigObject(t *testing.T) {
	b := loadConfigObject()
	f, err := pe.NewFile(strings.NewReader(string(b)))
	if err != nil {
		t.Fatal(err)
	}
	if f.Machine != pe.IMAGE_FILE_MACHINE_AMD64 || len(f.Sections) != 1 || f.Sections[0].Name != ".rdata" || f.Sections[0].Size != loadConfigSize {
		t.Errorf("header %#x, sections %d", f.Machine, len(f.Sections))
	}
	if len(f.Symbols) != 1 || f.Symbols[0].Name != "_load_config_used" || f.Symbols[0].SectionNumber != 1 || f.Symbols[0].StorageClass != 2 {
		t.Errorf("symbols %+v", f.Symbols)
	}
}
