// Why: the Nutshell targets (x86_64 and aarch64) link as ELF with their own
// loader rules, and adding them must not change the Linux link line.
// What: TestNutshellTriples and TestNutshellLinkLine, over nutshell.go.
// How: both triples are ELF and need ld.lld; no host runs them; a Nutshell
// link is the hosted ELF line with --no-pie, -z norelro and -z
// separate-loadable-segments before the output, and the Linux line does not
// move.

package toolchain

import (
	"reflect"
	"slices"
	"testing"
)

func TestNutshellTriples(t *testing.T) {
	for _, tr := range []string{"x86_64-unknown-nutshell-elf", "aarch64-unknown-nutshell-elf"} {
		if !IsNutshellTriple(tr) || FormatOf(tr) != ELF {
			t.Errorf("%s: IsNutshellTriple %v, FormatOf %v; want true, ELF", tr, IsNutshellTriple(tr), FormatOf(tr))
		}
		if _, ok := Runner(tr); ok {
			t.Errorf("%s has a runner on this host", tr)
		}
		if miss := (&Tools{Clang: "clang"}).Need(tr); !slices.Equal(miss, []string{NameLDLLD}) {
			t.Errorf("Need(%s) without ld.lld = %v", tr, miss)
		}
		if miss := (&Tools{Clang: "clang", LDLLD: "ld.lld"}).Need(tr); len(miss) != 0 {
			t.Errorf("Need(%s) = %v", tr, miss)
		}
	}
	for _, tr := range []string{"x86_64-unknown-linux-gnu", "x86_64-unknown-none-elf", "x86_64-pc-windows-msvc", "nutshell-unknown-linux"} {
		if IsNutshellTriple(tr) {
			t.Errorf("IsNutshellTriple(%s)", tr)
		}
	}
}

func TestNutshellLinkLine(t *testing.T) {
	out, o1 := absPath(t, "a"), absPath(t, "1.o")
	got, err := linkArgs(LinkJob{Format: ELF, Output: out, Objects: []string{o1}, Nutshell: true})
	want := []string{"-static", "-nostdlib", "-e", "_start", "--build-id=none", "-z", "noexecstack",
		"--no-pie", "-z", "norelro", "-z", "separate-loadable-segments", "-o", out, o1}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("dev Nutshell ELF =\n%q, %v\nwant\n%q", got, err, want)
	}
	rel, err := linkArgs(LinkJob{Format: ELF, Output: out, Objects: []string{o1}, Nutshell: true, Release: true})
	if err != nil || !slices.Contains(rel, "--gc-sections") || !slices.Contains(rel, "--strip-all") || !slices.Contains(rel, "separate-loadable-segments") {
		t.Errorf("release Nutshell ELF = %q, %v", rel, err)
	}
	linux, err := linkArgs(LinkJob{Format: ELF, Output: out, Objects: []string{o1}})
	if wantLinux := []string{"-static", "-nostdlib", "-e", "_start", "--build-id=none", "-z", "noexecstack", "-o", out, o1}; err != nil || !reflect.DeepEqual(linux, wantLinux) {
		t.Errorf("Linux ELF = %q, %v", linux, err)
	}
}
