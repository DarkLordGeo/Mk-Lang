// Why: static archives must sit at the right place on the linker's command
// line, or symbols resolve against the wrong library or not at all.
// What: TestLinkArchives, the archive placement for COFF, ELF and freestanding links.
// How: builds LinkJobs with and without archives and compares the arguments.

package toolchain

import (
	"reflect"
	"testing"
)

// TestLinkArchives: a COFF link puts the archives after the objects and
// before the import libraries, with /nodefaultlib kept; an ELF link puts
// them after the objects with -static -nostdlib kept; a freestanding link
// and a relative archive are refused; with no archives the line is the one
// TestLinkCommandLine pins.
func TestLinkArchives(t *testing.T) {
	out, o1, a1, a2, lib := absPath(t, "a.exe"), absPath(t, "1.o"), absPath(t, "x.lib"), absPath(t, "y.lib"), absPath(t, "kernel32.lib")
	got, err := linkArgs(LinkJob{Format: COFF, Output: out, Objects: []string{o1}, Archives: []string{a1, a2}, Libs: []string{lib}})
	want := []string{"/nologo", "/nodefaultlib", "/entry:mk_start", "/subsystem:console", "/osversion:10.0", "/machine:x64", "/Brepro",
		"/out:" + out, o1, a1, a2, lib, "/opt:noicf"}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("COFF =\n%q, %v\nwant\n%q", got, err, want)
	}
	ax := absPath(t, "libx.a")
	got, err = linkArgs(LinkJob{Format: ELF, Output: out, Objects: []string{o1}, Archives: []string{ax}, Release: true})
	want = []string{"-static", "-nostdlib", "-e", "_start", "--build-id=none", "-z", "noexecstack", "-o", out, o1, ax,
		"--gc-sections", "--strip-all"}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("ELF =\n%q, %v\nwant\n%q", got, err, want)
	}
	for _, bad := range []LinkJob{
		{Format: ELF, Free: true, Output: out, Objects: []string{o1}, Archives: []string{ax}},
		{Format: COFF, Output: out, Objects: []string{o1}, Archives: []string{"x.lib"}},
		{Format: ELF, Output: out, Objects: []string{o1}, Archives: []string{"libx.a"}},
	} {
		if _, err := linkArgs(bad); err == nil {
			t.Errorf("linkArgs(%+v) accepted", bad)
		}
	}
}
