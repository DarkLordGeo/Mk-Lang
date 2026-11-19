// Why: fs.Dir, fs.Stat and fs.MakeDir wrap OS calls, and Go's os package
// is a trusted reading of the same trees.
// What: TestSTDMISS_DirMatchesGo: names (bytes), kinds and sizes of 0, 1 and
// 1,000 entries, non-ASCII and long names, a subdirectory, and Stat.
// How: one tree read by both; on Windows a 546-byte name passes Next's frame
// buffer; modification times agree within 1 s of os.Stat.

package std

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

const missDirProg = `import std.fs

export Main(sys: &System): WriteError!void {
    var out := sys.Stdout()
    io := sys.io
    a := sys.arena
    p := sys.args[1]
    var d := fs.Dir.Open(io, p) else {
        try out.Str("open failed\n")
        return
    }
    defer d.Close()
    for {
        e := d.Next(a, io) else {
            try out.Str("next failed\n")
            break
        }
        x := e else {
            break
        }
        for b in x.name.AsBytes() {
            try Print(mut out, "{b:x}", (b: b))
        }
        try Print(mut out, " {d} {s}\n", (d: x.is_dir, s: x.size))
    }
    i := fs.Stat(io, p) else {
        try out.Str("stat failed\n")
        return
    }
    try Print(mut out, "stat {d} {m}\n", (d: i.kind == fs.Kind.Dir, m: i.modified.UnixNanos()))
    if sys.args.Len() > 2 {
        f := fs.Stat(io, sys.args[2]) else {
            try out.Str("stat failed\n")
            return
        }
        try Print(mut out, "file {d} {s} {m}\n", (d: f.kind == fs.Kind.Dir, s: f.size, m: f.modified.UnixNanos()))
        fs.MakeDir(io, sys.args[2]) else {
            try out.Str("mkdir failed\n")
            return
        }
        try out.Str("mkdir made\n")
    }
}
`

func missListing(t *testing.T, dir string) []string {
	t.Helper()
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range ents {
		info, err := e.Info()
		if err != nil {
			t.Fatal(err)
		}
		size := info.Size()
		if e.IsDir() {
			size = 0
		}
		out = append(out, fmt.Sprintf("%x %v %d", e.Name(), e.IsDir(), size))
	}
	return out
}

func TestSTDMISS_DirMatchesGo(t *testing.T) {
	exe := missBuild(t, missDirProg)
	for _, n := range []int{0, 1, 1000} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			dir := t.TempDir()
			for i := range n {
				name := fmt.Sprintf("f%04d", i)
				switch i % 5 {
				case 1:
					name += "-é-ü"
				case 2:
					name += "-" + strings.Repeat("x", 200)
					if i == 2 && runtime.GOOS == "windows" {
						// 186 UTF-16 units, 546 UTF-8 bytes: longer than Next's
						// 512-byte frame buffer, so it takes the arena path.
						name = fmt.Sprintf("f%04d-", i) + strings.Repeat("語", 180)
					}
				case 3:
					if err := os.Mkdir(filepath.Join(dir, name), 0o755); err != nil {
						t.Fatal(err)
					}
					continue
				}
				if err := os.WriteFile(filepath.Join(dir, name), make([]byte, i%37), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			file := filepath.Join(dir, "probe")
			if n > 0 {
				if err := os.WriteFile(file, []byte("probe!"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			args := []string{dir}
			if n > 0 {
				args = append(args, file)
			}
			out := missRunArgs(t, exe, args...)
			lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
			var got []string
			var tail []string
			for _, l := range lines {
				if strings.HasPrefix(l, "stat ") || strings.HasPrefix(l, "file ") || strings.HasPrefix(l, "mkdir ") {
					tail = append(tail, l)
				} else if l != "" {
					got = append(got, l)
				}
			}
			want := missListing(t, dir)
			slices.Sort(got)
			slices.Sort(want)
			if !slices.Equal(got, want) {
				t.Errorf("listing differs from os.ReadDir: %d entries, want %d\ngot  %q\nwant %q", len(got), len(want), head(got), head(want))
			}
			var isDir bool
			var mod int64
			if len(tail) == 0 || !strings.HasPrefix(tail[0], "stat ") {
				t.Fatalf("no stat line: %q", tail)
			}
			fmt.Sscanf(tail[0], "stat %t %d", &isDir, &mod)
			if !isDir {
				t.Errorf("Stat of the directory: is_dir false")
			}
			if n > 0 {
				var fd bool
				var size, fm int64
				if len(tail) < 3 {
					t.Fatalf("tail %q", tail)
				}
				fmt.Sscanf(tail[1], "file %t %d %d", &fd, &size, &fm)
				st, _ := os.Stat(file)
				if fd || size != 6 || absDur(time.Duration(fm-st.ModTime().UnixNano())) > time.Second {
					t.Errorf("Stat of the file: %q, os.Stat size %d mtime %d", tail[1], st.Size(), st.ModTime().UnixNano())
				}
				if tail[2] != "mkdir failed" {
					t.Errorf("MakeDir of a file: %q, want Exists", tail[2])
				}
			}
		})
	}
}

func head(s []string) []string {
	if len(s) > 4 {
		return s[:4]
	}
	return s
}

func absDur(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}

// missRunArgs is missRun with arguments.
func missRunArgs(t *testing.T, exe string, args ...string) string {
	t.Helper()
	out, err := exec.Command(exe, args...).CombinedOutput()
	if err != nil {
		t.Fatalf("%s %q: %v\n%s", exe, args, err, out)
	}
	return strings.ReplaceAll(string(out), "\r\n", "\n")
}
