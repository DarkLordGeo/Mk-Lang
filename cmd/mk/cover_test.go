// Why: coverage builds add counters and row tables to the executable; they
// must link and run in both profiles, inlined callees included.
// What: TestCoverLinksAndRuns.
// How: builds a package with driver Config.Cover in dev and release, runs it,
// and checks that the executable holds the counter and row sections (.mkcc
// and .mkcv on COFF, mk_cnts and mk_cover on ELF) and that release keeps the
// rows of inlined functions.

package main

import (
	"debug/elf"
	"debug/pe"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"mk/internal/driver"
)

const coverMain = `Small(x: int): int {
    if x > 3 {
        return x * 2
    }
    return x + 1
}

export Main(sys: &System): WriteError!void {
    var s := 0
    for i in 0..10 {
        s += Small(i)
    }
    try sys.Print("{s}\n", (s,))
}
`

func TestCoverLinksAndRuns(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.mk"), []byte(coverMain), 0o644); err != nil {
		t.Fatal(err)
	}
	// rows is each cover build's row count, by profile: release must keep
	// the rows of functions it inlines, as dev does (on COFF they once
	// went with their dropped function's comdat).
	rows := map[bool]uint32{}
	// Off first, then on, in one cache: the key must tell them apart.
	for _, c := range []struct{ release, cover bool }{{false, false}, {false, true}, {true, false}, {true, true}} {
		release := c.release
		var nf nativeFlags
		fs := newNativeFlagSet("build", io.Discard, &nf)
		args := []string{}
		if release {
			args = append(args, "--release")
		}
		if err := fs.Parse(args); err != nil {
			t.Fatal(err)
		}
		cfg, ok := nf.config(filepath.Join(dir, "main.mk"), io.Discard)
		if !ok {
			t.Fatal("config")
		}
		cfg.Cover = c.cover
		s, err := driver.NewSession(cfg)
		if err != nil {
			t.Fatal(err)
		}
		exe := filepath.Join(dir, "cov.exe")
		res, _, err := s.Build(exe)
		if err != nil {
			t.Fatal(err)
		}
		var b strings.Builder
		_ = res.WriteReport(&b, false)
		if driver.ExitStatus(res.Report) != 0 {
			t.Fatalf("release %v: %s", release, b.String())
		}
		out, err := exec.Command(exe).Output()
		if err != nil || strings.TrimSpace(string(out)) != "88" {
			t.Fatalf("release %v: %q %v", release, out, err)
		}
		var names []string
		if runtime.GOOS == "windows" {
			f, err := pe.Open(exe)
			if err != nil {
				t.Fatal(err)
			}
			for _, sec := range f.Sections {
				names = append(names, sec.Name)
				if sec.Name == ".mkcv" {
					rows[release] = sec.VirtualSize / 56
				}
			}
			f.Close()
		} else {
			f, err := elf.Open(exe)
			if err != nil {
				t.Fatal(err)
			}
			for _, sec := range f.Sections {
				names = append(names, sec.Name)
				if sec.Name == "mk_cover" {
					rows[release] = uint32(sec.Size / 56)
				}
			}
			f.Close()
		}
		want := []string{".mkcc", ".mkcv"}
		if runtime.GOOS != "windows" {
			want = []string{"mk_cnts", "mk_cover"}
		}
		for _, w := range want {
			found := false
			for _, n := range names {
				found = found || n == w
			}
			if found != c.cover {
				t.Errorf("release %v cover %v: section %s present %v in %v", release, c.cover, w, found, names)
			}
		}
	}
	if rows[false] < 2 || rows[true] != rows[false] {
		t.Errorf("descriptor rows: dev %d, release %d; want the same, at least 2 (Main and Small)", rows[false], rows[true])
	}
}
