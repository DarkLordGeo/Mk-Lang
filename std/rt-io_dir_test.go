// Why: fs.Dir, fs.MakeDir and fs.Stat go through each OS's own calls, and
// all of them must report the same tree that Go sees.
// What: TestRtIo_DirWindows and TestRtIo_DirLinux (io_uring and epoll).
// How: the probe std/rtio2probe/dir.mk, in a copy of std (or MK_RTIO2_STD), runs
// mkdir, stat, opendir and readdir; the output is compared with os.ReadDir and os.Lstat.

package std

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// rtio2DirProbe calls NativeTable's entries directly with an Io over it.
const rtio2DirProbe = `// A directory probe of std.sys (std/rt-io_dir_test.go).

import std.core
import std.raw
import std.sys
import std.task

NIo(): Io {
    unsafe "NativeTable is a global" {
        return core.MakeIo(raw.Of(&task.NativeTable), 0)
    }
}

Tab(): &core.IoTable {
    return &task.NativeTable
}

export Mkdir(p: str): u32 {
    f := Tab().mkdir
    return u32(f(NIo(), p).err)
}

// Stat prints "<tag> <err> <kind> <size> <modified>".
export Stat(out: mut Writer, tag: str, p: str): WriteError!void {
    var st := core.IoStat(kind: 0, size: 0, modified: 0)
    f := Tab().stat
    r := f(NIo(), p, mut st)
    e := u32(r.err)
    k := st.kind
    n := st.size
    m := st.modified
    try Print(out, "{tag} {e} {k} {n} {m}\n", (tag, e, k, n, m))
}

export OpenErr(p: str): u32 {
    f := Tab().opendir
    r := f(NIo(), p)
    if r.err == core.SysErr.None {
        c := Tab().close
        _ = c(NIo(), Trunc[uint](r.value))
    }
    return u32(r.err)
}

// List prints one line per entry of p: "e <kind> <size> <modified> <name>",
// then "end <count> <err>".
export List(out: mut Writer, p: str): WriteError!void {
    f := Tab().opendir
    r := f(NIo(), p)
    if r.err != core.SysErr.None {
        e := u32(r.err)
        try Print(out, "end 0 {e}\n", (e))
        return
    }
    h := Trunc[uint](r.value)
    var buf: [1024]u8 = [0; 1024]
    var st := core.IoStat(kind: 0, size: 0, modified: 0)
    var count := 0
    var e: u32 = 0
    rd := Tab().readdir
    for {
        x := rd(NIo(), h, mut buf[..], mut st)
        if x.err != core.SysErr.None {
            e = u32(x.err)
            break
        }
        if x.value == 0 {
            break
        }
        count += 1
        k := st.kind
        n := st.size
        m := st.modified
        var name := ""
        unsafe "buf holds x.value bytes of the name" {
            name = raw.Str(raw.Data(buf[..]), Trunc[int](x.value))
        }
        try Print(out, "e {k} {n} {m} {name}\n", (k, n, m, name))
    }
    c := Tab().close
    _ = c(NIo(), h)
    try Print(out, "end {count} {e}\n", (count, e))
}

// Short reads p with a 4-byte name buffer: for the first entry whose name
// is longer, it prints "short <needed> <err>" and then the same entry read
// again with a large buffer, "again <len> <name>".
export Short(out: mut Writer, p: str): WriteError!void {
    f := Tab().opendir
    r := f(NIo(), p)
    if r.err != core.SysErr.None {
        return
    }
    h := Trunc[uint](r.value)
    var small: [4]u8 = [0; 4]
    var big: [1024]u8 = [0; 1024]
    var st := core.IoStat(kind: 0, size: 0, modified: 0)
    rd := Tab().readdir
    for {
        x := rd(NIo(), h, mut small[..], mut st)
        if x.err == core.SysErr.NoSpace {
            need := x.value
            e := u32(x.err)
            try Print(out, "short {need} {e}\n", (need, e))
            y := rd(NIo(), h, mut big[..], mut st)
            n := y.value
            var name := ""
            unsafe "big holds y.value bytes" {
                name = raw.Str(raw.Data(big[..]), Trunc[int](y.value))
            }
            try Print(out, "again {n} {name}\n", (n, name))
            break
        }
        if x.err != core.SysErr.None || x.value == 0 {
            break
        }
    }
    c := Tab().close
    _ = c(NIo(), h)
}

// ListShort lists p as List does, but asks for every entry first with a
// 1-byte name buffer: each such call must be NoSpace with the length the
// next call then answers (the entry stays next, at every block boundary
// too). "shortbad <count>" counts the calls that broke that rule.
export ListShort(out: mut Writer, p: str): WriteError!void {
    f := Tab().opendir
    r := f(NIo(), p)
    if r.err != core.SysErr.None {
        e := u32(r.err)
        try Print(out, "end 0 {e}\n", (e))
        return
    }
    h := Trunc[uint](r.value)
    var tiny: [1]u8 = [0; 1]
    var buf: [1024]u8 = [0; 1024]
    var st := core.IoStat(kind: 0, size: 0, modified: 0)
    var count := 0
    var bad := 0
    var e: u32 = 0
    rd := Tab().readdir
    for {
        s := rd(NIo(), h, mut tiny[..], mut st)
        x := rd(NIo(), h, mut buf[..], mut st)
        if x.err != core.SysErr.None {
            e = u32(x.err)
            break
        }
        if x.value == 0 {
            if s.err != core.SysErr.None || s.value != 0 {
                bad += 1
            }
            break
        }
        if s.err != core.SysErr.NoSpace || s.value != x.value {
            bad += 1
        }
        count += 1
        k := st.kind
        n := st.size
        m := st.modified
        var name := ""
        unsafe "buf holds x.value bytes of the name" {
            name = raw.Str(raw.Data(buf[..]), Trunc[int](x.value))
        }
        try Print(out, "e {k} {n} {m} {name}\n", (k, n, m, name))
    }
    c := Tab().close
    _ = c(NIo(), h)
    try Print(out, "end {count} {e}\n", (count, e))
    try Print(out, "shortbad {bad}\n", (bad))
}

// Leak: open, list and close p n times, and open the file q n times (the
// NotDirectory path); the handle counts before and after.
export Leak(p: str, q: str, n: int): (a: int, b: int) {
    var buf: [1024]u8 = [0; 1024]
    var st := core.IoStat(kind: 0, size: 0, modified: 0)
    f := Tab().opendir
    rd := Tab().readdir
    c := Tab().close
    // Warm-up: the first pool job starts the pool's threads.
    _ = OpenErr(p)
    before := sys.HandleCount()
    for _ in 0..n {
        r := f(NIo(), p)
        if r.err == core.SysErr.None {
            h := Trunc[uint](r.value)
            for {
                x := rd(NIo(), h, mut buf[..], mut st)
                if x.err != core.SysErr.None || x.value == 0 {
                    break
                }
            }
            _ = c(NIo(), h)
        }
        _ = f(NIo(), q)
    }
    return (a: before, b: sys.HandleCount())
}
`

const rtio2DirMain = `import std.rtio2probe

export Main(sys: &System): WriteError!void {
    var out := sys.Stdout()
    m0 := rtio2probe.Mkdir("fx/made")
    m1 := rtio2probe.Mkdir("fx/made")
    m2 := rtio2probe.Mkdir("fx/missing/x")
    m3 := rtio2probe.Mkdir("fx/mixed/a.txt")
    m4 := rtio2probe.Mkdir("fx/bad\x00name")
    try Print(mut out, "mkdir {m0} {m1} {m2} {m3} {m4}\n", (m0, m1, m2, m3, m4))
    try rtio2probe.Stat(mut out, "statfile", "fx/mixed/a.txt")
    try rtio2probe.Stat(mut out, "statdir", "fx/mixed/sub")
    try rtio2probe.Stat(mut out, "statnone", "fx/nope")
    o1 := rtio2probe.OpenErr("fx/mixed/a.txt")
    o2 := rtio2probe.OpenErr("fx/nope")
    o3 := rtio2probe.OpenErr("fx/mixed/empty")
    try Print(mut out, "open {o1} {o2} {o3}\n", (o1, o2, o3))
    try Print(mut out, "list mixed\n", ())
    try rtio2probe.List(mut out, "fx/mixed")
    try Print(mut out, "list empty\n", ())
    try rtio2probe.List(mut out, "fx/mixed/empty")
    try Print(mut out, "list big\n", ())
    try rtio2probe.List(mut out, "fx/big")
    try Print(mut out, "list bigshort\n", ())
    try rtio2probe.ListShort(mut out, "fx/big")
    try rtio2probe.Short(mut out, "fx/long")
    (a, b) := rtio2probe.Leak("fx/mixed", "fx/mixed/a.txt", 300)
    try Print(mut out, "leak {a} {b}\n", (a, b))
    try out.Flush()
}
`

// rtio2BigCount is how many files fx/big holds: enough for several refills
// of the 16 KiB block on both targets (Linux: 8,160 bytes of 24-byte
// records at least, 255 of these 32-byte ones a block; Windows: 16,352
// bytes of 80-byte records, 204 a block).
const rtio2BigCount = 3000

// rtio2LongName is 255 bytes, the longest name both file systems take.
var rtio2LongName = strings.Repeat("n", 251) + ".txt"

// rtio2Fixtures makes the tree the probe reads, in dir/fx.
func rtio2Fixtures(t *testing.T, dir string) {
	t.Helper()
	fx := filepath.Join(dir, "fx")
	if err := os.RemoveAll(fx); err != nil {
		t.Fatal(err)
	}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, d := range []string{"mixed/sub", "mixed/empty", "big", "long"} {
		must(os.MkdirAll(filepath.Join(fx, filepath.FromSlash(d)), 0o755))
	}
	must(os.WriteFile(filepath.Join(fx, "mixed", "a.txt"), []byte("hello"), 0o644))
	must(os.WriteFile(filepath.Join(fx, "mixed", "é日本.txt"), []byte("xy"), 0o644))
	must(os.WriteFile(filepath.Join(fx, "mixed", rtio2LongName), make([]byte, 70000), 0o644))
	must(os.WriteFile(filepath.Join(fx, "mixed", "sub", "inner"), []byte("i"), 0o644))
	for i := 0; i < rtio2BigCount; i++ {
		must(os.WriteFile(filepath.Join(fx, "big", fmt.Sprintf("f%04d", i)), make([]byte, i%17), 0o644))
	}
	must(os.WriteFile(filepath.Join(fx, "long", "abcdefgh"), nil, 0o644))
}

// buildRTIO2 builds probe (as std/rtio2probe/<file>) and mainSrc for target
// (empty: the host) over the tree's std, or over MK_RTIO2_STD.
func buildRTIO2(t *testing.T, target, file, probe, mainSrc, name string) string {
	t.Helper()
	src := os.Getenv("MK_RTIO2_STD")
	manifestPath := "pkg.mk"
	if src != "" {
		manifestPath = filepath.Join(src, "pkg.mk")
	}
	manifest, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(manifest)
	at := strings.Index(text, "unsafe_modules: [")
	end := -1
	if at >= 0 {
		end = strings.Index(text[at:], "]")
	}
	if end < 0 {
		t.Fatal("pkg.mk: no unsafe_modules list")
	}
	fixtures := map[string]string{
		"pkg.mk":             text[:at+end] + `, "rtio2probe"` + text[at+end:],
		"rtio2probe/" + file: probe,
	}
	root := t.TempDir()
	var stdDir string
	if src != "" {
		stdDir = filepath.Join(root, "std")
		if err := os.CopyFS(stdDir, os.DirFS(src)); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(stdDir, "rtio2probe"), 0o755); err != nil {
			t.Fatal(err)
		}
		for p, text := range fixtures {
			if err := os.WriteFile(filepath.Join(stdDir, filepath.FromSlash(p)), []byte(text), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	} else {
		stdDir = copyStd(t, root, fixtures)
	}
	if err := os.WriteFile(filepath.Join(root, "main.mk"), []byte(mainSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(root, name)
	args := []string{"build"}
	if target != "" {
		args = append(args, "--target", target)
	} else if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	args = append(args, "-o", exe, "main.mk")
	out, status := mkCmd(t, root, stdDir, args...)
	if status != 0 {
		if strings.Contains(out, "no usable LLVM") {
			t.Skipf("needs the LLVM toolchain: %s", out)
		}
		t.Fatalf("mk build: exit %d\n%s", status, out)
	}
	return exe
}

// rtio2Entry is one directory entry as the probe printed it, or as Go saw it.
type rtio2Entry struct {
	kind uint32
	size int64
	mod  int64
	name string
}

// rtio2ParseList splits the probe's output into its sections: the entries
// after "list <x>" up to "end", and the other lines by their first word.
func rtio2ParseList(t *testing.T, out string) (map[string][]rtio2Entry, map[string]string) {
	t.Helper()
	lists := map[string][]rtio2Entry{}
	lines := map[string]string{}
	cur := ""
	for _, ln := range strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n") {
		if ln == "" {
			continue
		}
		word, rest, _ := strings.Cut(ln, " ")
		switch word {
		case "list":
			cur = rest
			lists[cur] = []rtio2Entry{}
		case "e":
			f := strings.SplitN(rest, " ", 4)
			if len(f) != 4 || cur == "" {
				t.Fatalf("bad entry line %q", ln)
			}
			k, _ := strconv.ParseUint(f[0], 10, 32)
			n, _ := strconv.ParseInt(f[1], 10, 64)
			m, _ := strconv.ParseInt(f[2], 10, 64)
			lists[cur] = append(lists[cur], rtio2Entry{uint32(k), n, m, f[3]})
		case "end":
			lines["end "+cur] = rest
			cur = ""
		default:
			lines[word] = rest
		}
	}
	return lists, lines
}

// rtio2GoList is Go's view of dir: os.ReadDir and Lstat, with the probe's
// kind numbers and the rule that a directory's or link's size is 0.
func rtio2GoList(t *testing.T, dir string) []rtio2Entry {
	t.Helper()
	des, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var r []rtio2Entry
	for _, de := range des {
		fi, err := de.Info()
		if err != nil {
			t.Fatal(err)
		}
		e := rtio2Entry{kind: 1, size: fi.Size(), mod: fi.ModTime().UnixNano(), name: de.Name()}
		switch {
		case fi.Mode()&os.ModeSymlink != 0:
			e.kind, e.size = 3, 0
		case fi.IsDir():
			e.kind, e.size = 2, 0
		case !fi.Mode().IsRegular():
			e.kind = 4
		}
		r = append(r, e)
	}
	return r
}

// rtio2CheckDir judges one probe run whose fixtures are in dir.
func rtio2CheckDir(t *testing.T, label, out, dir string) {
	t.Helper()
	lists, lines := rtio2ParseList(t, out)
	want := map[string]string{
		// made, Exists, NotFound, Exists (a file is there), InvalidPath
		"mkdir":    "0 2 1 2 7",
		"statnone": "1 0 0 0",
		// NotDirectory, NotFound, success
		"open":      "5 1 0",
		"end empty": "0 0",
		"end mixed": "5 0",
		"end big":   fmt.Sprintf("%d 0", rtio2BigCount),
		// every entry of fx/big read first with a 1-byte buffer
		"end bigshort": fmt.Sprintf("%d 0", rtio2BigCount),
		"shortbad":     "0",
		"short":        "8 6",
		"again":        "8 abcdefgh",
	}
	for k, v := range want {
		if lines[k] != v {
			t.Errorf("%s: %s: got %q, want %q", label, k, lines[k], v)
		}
	}
	if fi, err := os.Stat(filepath.Join(dir, "fx", "made")); err != nil || !fi.IsDir() {
		t.Errorf("%s: fx/made was not made: %v", label, err)
	}
	fi, err := os.Stat(filepath.Join(dir, "fx", "mixed", "a.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if w := fmt.Sprintf("0 1 5 %d", fi.ModTime().UnixNano()); lines["statfile"] != w {
		t.Errorf("%s: statfile: got %q, want %q", label, lines["statfile"], w)
	}
	if f := strings.Fields(lines["statdir"]); len(f) != 4 || f[0] != "0" || f[1] != "2" || f[2] != "0" {
		t.Errorf("%s: statdir: got %q, want 0 2 0 <time>", label, lines["statdir"])
	}
	for _, d := range []string{"mixed", "empty", "big", "bigshort"} {
		path := filepath.Join(dir, "fx", d)
		if d == "empty" {
			path = filepath.Join(dir, "fx", "mixed", "empty")
		}
		if d == "bigshort" {
			path = filepath.Join(dir, "fx", "big")
		}
		got := lists[d]
		goList := rtio2GoList(t, path)
		sort.Slice(got, func(i, j int) bool { return got[i].name < got[j].name })
		sort.Slice(goList, func(i, j int) bool { return goList[i].name < goList[j].name })
		if len(got) != len(goList) {
			t.Errorf("%s: list %s: %d entries, Go sees %d", label, d, len(got), len(goList))
			continue
		}
		for i := range got {
			g, w := got[i], goList[i]
			if g.name != w.name || g.kind != w.kind || g.size != w.size {
				t.Errorf("%s: list %s: entry %d: got %+v, want %+v", label, d, i, g, w)
				continue
			}
			// A directory's time moves when this run makes fx/made; files
			// are compared exactly.
			if g.kind == 1 && g.mod != w.mod {
				t.Errorf("%s: list %s: %s: modified %d, Go %d", label, d, g.name, g.mod, w.mod)
			}
		}
	}
	// A handle leaked per cycle would show as 300 or more. The blocking
	// pool may start (or restart) a thread during the cycles, one handle
	// each: measured on the development machine, 2 of 12 runs ended 1 or 2
	// above the start, so up to 2 is allowed.
	f := strings.Fields(lines["leak"])
	var before, after int
	if len(f) == 2 {
		before, _ = strconv.Atoi(f[0])
		after, _ = strconv.Atoi(f[1])
	}
	if len(f) != 2 || before <= 0 || after-before > 2 || before-after > 2 {
		t.Errorf("%s: handle counts before and after 300 cycles: %q", label, lines["leak"])
	}
}

// TestRtIo_DirWindows: the directory entries on Windows.
func TestRtIo_DirWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows only")
	}
	exe := buildRTIO2(t, "", "dir.mk", rtio2DirProbe, rtio2DirMain, "rtio2dir")
	dir := filepath.Dir(exe)
	rtio2Fixtures(t, dir)
	r := runNative(t, exe, "")
	if r.code != 0 {
		t.Fatalf("exit %d\nstdout:\n%s\nstderr:\n%s", r.code, r.stdout, r.stderr)
	}
	rtio2CheckDir(t, "windows", r.stdout, dir)
}

// TestRtIo_DirLinux: the same on Linux, io_uring (default) and epoll; the
// fixtures are remade before each run (it makes fx/made).
func TestRtIo_DirLinux(t *testing.T) {
	if os.Getenv("MK_RTIO_BUILD_ONLY") != "" {
		t.Logf("built %s", buildRTIO2(t, "x86_64-linux", "dir.mk", rtio2DirProbe, rtio2DirMain, "rtio2dir"))
		return
	}
	needWSL(t)
	exe := buildRTIO2(t, "x86_64-linux", "dir.mk", rtio2DirProbe, rtio2DirMain, "rtio2dir")
	dir := filepath.Dir(exe)
	for _, b := range []string{"auto", "epoll"} {
		rtio2Fixtures(t, dir)
		out, stderr, code := runWSL(t, exe, []string{"MK_IO=" + b})
		if code != 0 {
			t.Fatalf("MK_IO=%s: exit %d\nstdout:\n%s\nstderr:\n%s", b, code, out, stderr)
		}
		rtio2CheckDir(t, "linux MK_IO="+b, out, dir)
	}
}
