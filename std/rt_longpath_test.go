// Why: Windows paths past MAX_PATH need the \\?\ form, also for a short
// relative name under a deep current directory.
// What: TestRt_LongPaths (WidePath in std/sys/windows/file.mk: LongPath, 248
// units, and ShortFull) and TestRt_LongAbsoluteSymlink.
// How: Go's os makes the deep directories and checks what the program made
// and removed.

package std

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

const rt8LongSrc = `import std.fs

// Rel works under the current directory, which the test makes about 240
// units long, so each name here resolves past 248 units.
Rel(sys: &System): fs.Error|OutOfMemory!void {
    io := sys.io
    try fs.MakeDirAll(io, "rel/sub")
    try fs.WriteAll(io, "rel/sub/f.txt", "hello long".AsBytes())
    info := try fs.Stat(io, "rel/sub/f.txt")
    _ = try fs.Copy(io, "rel/sub/f.txt", "rel/sub/g.txt")
    try fs.Rename(io, "rel/sub/g.txt", "rel/sub/h.txt")
    try fs.Remove(io, "rel/sub/f.txt")
    t := try fs.ReadText(sys.arena, io, "rel/sub/h.txt")
    sys.Print("rel {n} {t}\n", (n: info.size, t: t)) else {}
}

// Abs works on an absolute directory of about 600 units.
Abs(sys: &System, dir: str, f: str, g: str): fs.Error|OutOfMemory!void {
    io := sys.io
    try fs.MakeDirAll(io, dir)
    try fs.WriteAll(io, f, "hello long".AsBytes())
    info := try fs.Stat(io, f)
    try fs.Rename(io, f, g)
    t := try fs.ReadText(sys.arena, io, g)
    sys.Print("abs {n} {t}\n", (n: info.size, t: t)) else {}
    try fs.RemoveAll(io, dir)
    sys.Print("absgone\n", ()) else {}
}

export Main(sys: &System): WriteError!void {
    Rel(sys) else e {
        try sys.Print("rel failed {e}\n", (e: e))
    }
    Abs(sys, sys.args[1], sys.args[2], sys.args[3]) else e {
        try sys.Print("abs failed {e}\n", (e: e))
    }
    // A device name stays the device under a deep directory.
    fs.WriteAll(sys.io, "NUL", "x".AsBytes()) else e {
        try sys.Print("nul failed {e}\n", (e: e))
        return
    }
    try sys.Print("nul ok\n", ())
}
`

// rt8Deep is base extended by 10-unit elements to between 236 and 245
// units: short enough for a process's current directory, long enough that
// "rel\sub\f.txt" under it passes 248.
func rt8Deep(base string) string {
	d := base
	for len(d)+10 <= 245 {
		d += `\d23456789`
	}
	return d
}

// TestRt_LongPaths runs the program, dev and release, with its current
// directory about 240 units deep, and checks with Go's os what it left.
func TestRt_LongPaths(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows paths")
	}
	for _, flags := range [][]string{nil, {"--release"}} {
		exe := buildNative(t, nil, rt8LongSrc, flags...)
		deep := rt8Deep(t.TempDir())
		if len(deep) < 236 {
			t.Fatalf("deep directory is %d units", len(deep))
		}
		if err := os.MkdirAll(deep, 0o755); err != nil {
			t.Fatal(err)
		}
		abs := deep + strings.Repeat(`\e23456789`, 36)
		f, g := abs+`\f.txt`, abs+`\g.txt`
		ctx, cancel := context.WithTimeout(context.Background(), runTimeout)
		cmd := exec.CommandContext(ctx, exe, abs, f, g)
		cmd.Dir = deep
		var out, errb bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &errb
		err := cmd.Run()
		cancel()
		if err != nil {
			t.Fatalf("%v: %v\n%s%s", flags, err, out.String(), errb.String())
		}
		got := strings.ReplaceAll(out.String(), "\r\n", "\n")
		want := "rel 10 hello long\nabs 10 hello long\nabsgone\nnul ok\n"
		if got != want {
			t.Errorf("%v: stdout\n%s\nwant\n%s", flags, got, want)
		}
		// The oracle: what the program made, renamed and removed.
		h, err := os.ReadFile(filepath.Join(deep, "rel", "sub", "h.txt"))
		if err != nil || string(h) != "hello long" {
			t.Errorf("%v: rel\\sub\\h.txt: %q, %v", flags, h, err)
		}
		for _, gone := range []string{filepath.Join(deep, "rel", "sub", "f.txt"), filepath.Join(deep, "rel", "sub", "g.txt"), abs} {
			if _, err := os.Lstat(gone); !os.IsNotExist(err) {
				t.Errorf("%v: %d-unit %s still there (%v)", flags, len(gone), filepath.Base(gone), err)
			}
		}
		if _, err := os.Lstat(filepath.Dir(abs)); err != nil {
			t.Errorf("%v: RemoveAll went above its root: %v", flags, err)
		}
		if err := os.RemoveAll(filepath.Join(deep, "rel")); err != nil {
			t.Error(err)
		}
	}
}

// sys.Absolute and sys.Symlink once took paths through fixed 1,024-unit
// buffers, so a longer path was InvalidPath.
// rt8LongLinkSrc runs fs.Absolute on relative paths of 1,023, 1,024 and
// 1,200 units, and makes four links around args[1], a directory of about
// 1,100 units: a long link to a short relative target, a short link to a
// long relative target, a long link to a long relative target, and a
// short link to a long absolute target. Each link names a directory, so
// each must be a directory link.
const rt8LongLinkSrc = `import std.fs

// Rep is the first n bytes of "abcde/" repeated.
Rep(sys: &System, n: int): OutOfMemory!str {
    unit := "abcde/".AsBytes()
    b := try sys.arena.Slice[u8](n)
    for i in 0..n {
        b[i] = unit[i % 6]
    }
    return str.From(b[..]) else {
        return ""
    }
}

Abs(sys: &System, n: int): fs.Error|OutOfMemory!void {
    p := try fs.Absolute(sys.arena, sys.io, try Rep(sys, n))
    sys.Print("abs {n} {p}\n", (n: n, p: p)) else {}
}

Link(sys: &System, name: str, target: str, link: str): fs.Error|OutOfMemory!void {
    try fs.Symlink(sys.io, target, link)
    t := try fs.ReadLink(sys.arena, sys.io, link)
    sys.Print("link {name} {t}\n", (name: name, t: t)) else {}
}

export Main(sys: &System): WriteError!void {
    for n in [1023, 1024, 1200] {
        Abs(sys, n) else e {
            try sys.Print("abs {n} failed {e}\n", (n: n, e: e))
        }
    }
    d := sys.args[1]
    rel := sys.args[2]
    dots := sys.args[3]
    l1 := sys.arena.Fmt("{d}/l1", (d: d)) else {
        return
    }
    l3 := sys.arena.Fmt("{d}/l3", (d: d)) else {
        return
    }
    Link(sys, "l1", "..", l1) else e {
        try sys.Print("link l1 failed {e}\n", (e: e))
    }
    Link(sys, "l2", rel, "l2") else e {
        try sys.Print("link l2 failed {e}\n", (e: e))
    }
    Link(sys, "l3", dots, l3) else e {
        try sys.Print("link l3 failed {e}\n", (e: e))
    }
    Link(sys, "l4", d, "l4") else e {
        try sys.Print("link l4 failed {e}\n", (e: e))
    }
}
`

// TestRt_LongAbsoluteSymlink runs rt8LongLinkSrc, dev and release, in a
// fresh directory, and checks it against Go's os and path/filepath:
// filepath.Abs for each Absolute, and for each link os.Readlink (the
// target as stored), os.Stat's IsDir and os.ReadDir through the link,
// which a link made without the directory flag fails.
func TestRt_LongAbsoluteSymlink(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows paths")
	}
	probe := t.TempDir()
	if err := os.Symlink("x", filepath.Join(probe, "l")); err != nil {
		t.Skipf("this account cannot make symbolic links (Developer Mode off): %v", err)
	}
	for _, flags := range [][]string{nil, {"--release"}} {
		exe := buildNative(t, nil, rt8LongLinkSrc, flags...)
		wd := t.TempDir()
		var b strings.Builder
		for b.Len() < 1100 {
			b.WriteString(`d23456789\`)
		}
		rel := strings.TrimSuffix(b.String(), `\`)
		d := filepath.Join(wd, rel)
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, "mark"), []byte("m"), 0o644); err != nil {
			t.Fatal(err)
		}
		// 520 "./" and a "." name the link's own directory in 1,041 units.
		dots := strings.Repeat("./", 520) + "."
		ctx, cancel := context.WithTimeout(context.Background(), runTimeout)
		cmd := exec.CommandContext(ctx, exe, d, strings.ReplaceAll(rel, `\`, "/"), dots)
		cmd.Dir = wd
		var out, errb bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &errb
		err := cmd.Run()
		cancel()
		if err != nil {
			t.Fatalf("%v: %v\n%s%s", flags, err, out.String(), errb.String())
		}
		var want strings.Builder
		for _, n := range []int{1023, 1024, 1200} {
			p := strings.Repeat("abcde/", 1200/6+1)[:n]
			abs, err := filepath.Abs(filepath.Join(wd, p))
			if err != nil {
				t.Fatal(err)
			}
			want.WriteString("abs " + strconv.Itoa(n) + " " + abs + "\n")
		}
		links := []struct{ name, path, target, dir string }{
			{"l1", filepath.Join(d, "l1"), "..", filepath.Dir(d)},
			{"l2", filepath.Join(wd, "l2"), rel, d},
			{"l3", filepath.Join(d, "l3"), strings.ReplaceAll(dots, "/", `\`), d},
			{"l4", filepath.Join(wd, "l4"), d, d},
		}
		for _, l := range links {
			want.WriteString("link " + l.name + " " + l.target + "\n")
		}
		got := strings.ReplaceAll(out.String(), "\r\n", "\n")
		if got != want.String() {
			t.Errorf("%v: stdout\n%s\nwant\n%s", flags, got, want.String())
		}
		// The oracle: what each link holds and where it leads.
		for _, l := range links {
			if len(l.path) < 1024 && len(l.target) < 1024 {
				t.Fatalf("%s: neither its path (%d) nor its target (%d) is 1,024 units", l.name, len(l.path), len(l.target))
			}
			got, err := os.Readlink(l.path)
			if err != nil || got != l.target {
				t.Errorf("%v: os.Readlink(%s) = %q, %v; want %q", flags, l.name, got, err, l.target)
			}
			fi, err := os.Stat(l.path)
			if err != nil || !fi.IsDir() {
				t.Errorf("%v: os.Stat(%s): %v, %v; want a directory", flags, l.name, fi, err)
			}
			ents, err := os.ReadDir(l.path)
			if err != nil {
				t.Errorf("%v: os.ReadDir through %s: %v", flags, l.name, err)
				continue
			}
			wantEnts, err := os.ReadDir(l.dir)
			if err != nil {
				t.Fatal(err)
			}
			if len(ents) != len(wantEnts) {
				t.Errorf("%v: %s lists %d entries, its target %d", flags, l.name, len(ents), len(wantEnts))
			}
		}
	}
}
