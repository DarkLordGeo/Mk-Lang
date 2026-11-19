// Why: fs.Dir lists OS names that are not UTF-8, the reason Path exists,
// and such a name must still be shown and reopened.
// What: TestSTDMISS_DirNonUtf8NameLinux; the Windows half is
// std-missing_nonutf8_windows_test.go.
// How: on Linux (MK_LINUX=1, under the WSL lock) it creates "a\xff\xfeb";
// the program prints each name's bytes, Format and Str(), and reopens it.

package std

import (
	"slices"
	"strings"
	"testing"
)

const missR6Prog = `import std.fs

PutHex(out: mut Writer, b: []u8): WriteError!void {
    for c in b {
        if c < 16 {
            try out.Str("0")
        }
        try Print(out, "{c:x}", (c: c))
    }
}

export Main(sys: &System): WriteError!void {
    var out := sys.Stdout()
    io := sys.io
    a := sys.arena
    p := sys.args[1]
    base := Path.New(a, p.AsBytes()) else {
        return
    }
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
        try out.Str("n ")
        try PutHex(mut out, x.name.AsBytes())
        var t := Text.New(a)
        var w := t.Writer()
        try Print(mut w, "{p}", (p: &x.name))
        try out.Str(" f ")
        try PutHex(mut out, t.Str().AsBytes())
        s := x.name.Str() else {
            try out.Str(" s0\n")
            continue
        }
        j := base.Join(a, s) else {
            return
        }
        js := j.Str() else {
            return
        }
        _ = fs.Stat(io, js) else {
            try out.Str(" s1 stat-fail\n")
            continue
        }
        try out.Str(" s1 stat-ok\n")
    }
    // Every listed name that is not a str reopens from its bytes through
    // fs.OpenPath, here as fs.Walk lists it.
    var w := fs.Walk(sys.heap, io, p) else {
        try out.Str("walk failed\n")
        return
    }
    defer w.Close()
    for {
        e := w.Next(a, io) else {
            try out.Str("walk next failed\n")
            break
        }
        x := e else {
            break
        }
        if x.depth != 1 || x.path.Str().IsSome() {
            continue
        }
        try out.Str("r ")
        try PutHex(mut out, x.path.Base().AsBytes())
        f := fs.OpenPath(io, &x.path, fs.ReadOnly) else {
            try out.Str(" open-fail\n")
            continue
        }
        size := f.Size(io) else {
            f.Abandon()
            try out.Str(" size-fail\n")
            continue
        }
        f.Abandon()
        try Print(mut out, " size {size}\n", (size))
    }
}
`

// missR6Lines is the program's output lines, sorted.
func missR6Lines(out string) []string {
	var got []string
	for _, l := range strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n") {
		if l != "" {
			got = append(got, l)
		}
	}
	slices.Sort(got)
	return got
}

// TestSTDMISS_DirNonUtf8NameLinux lists and reopens a name that is not
// UTF-8 on the Linux file system.
// A name that is a str is Stat-ed by that str, and one that is not is
// reopened from its bytes through fs.OpenPath as fs.Walk lists it. On the
// Linux file system any bytes but NUL and '/' are a name.
func TestSTDMISS_DirNonUtf8NameLinux(t *testing.T) {
	stdioNeedWSL(t)
	exe := missBuild(t, missR6Prog, "--target", "x86_64-linux")
	script := `set -e; d=$(mktemp -d "$HOME/mkmiss_r6.XXXXXX"); ` +
		`printf x > "$d/$(printf 'a\377\376b')"; printf y > "$d/plain"; printf z > "$d/$(printf '\303\251')"; ` +
		`echo "fs $(df -T "$d" | tail -1 | awk '{print $2}')"; ` +
		`'` + wslPath(exe) + `' "$d"; cd /; rm -rf "$d"`
	out, stderr, code := wslSh(t, script)
	if code != 0 {
		t.Fatalf("exit %d\nstdout:\n%s\nstderr:\n%s", code, out, stderr)
	}
	fs, rest, _ := strings.Cut(strings.TrimPrefix(strings.ReplaceAll(out, "\r\n", "\n"), "fs "), "\n")
	t.Logf("file system: %s", fs)
	if fs == "9p" || fs == "drvfs" || fs == "" {
		t.Errorf("the probe ran on %q, not a Linux file system", fs)
	}
	got := missR6Lines(rest)
	// a FF FE b: two maximal ill-formed subparts, so two U+FFFD (EF BF BD)
	// in Format, and Str() is none. The entry set is exactly what the
	// script created (the listing has no . or ..).
	want := []string{
		"n 61fffe62 f 61efbfbdefbfbd62 s0",
		"n 706c61696e f 706c61696e s1 stat-ok",
		"n c3a9 f c3a9 s1 stat-ok",
		"r 61fffe62 size 1",
	}
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("listing:\ngot  %q\nwant %q", got, want)
	}
}
