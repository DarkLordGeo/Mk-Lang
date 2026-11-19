// Why: std.fs must answer as the OS does; Go's os, io/fs and path/filepath
// are a trusted oracle over generated trees.
// What: TestFs_OracleWindows and OracleLinux, the block runners
// TestFs_LinuxBlocks and WindowsBlocks, DeepTree and RemoveAllJunction.
// How: one Go helper makes two identical trees from a seed; the Mk program and
// Go run the same script on one each, and their outputs are compared.

package std

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"
)

// One Go helper (fs2Helper, built for the host and for linux/amd64) makes
// two identical trees, a and b, from a seed ("gen"), and runs the oracle
// script on b with Go's functions ("oracle"); the Mk program (fs2Prog)
// runs the same script on a with std.fs. Each prints sections of lines; a
// walk's lines are sorted before the two are compared, since Walk reports
// in the OS's order and filepath.WalkDir in lexical order.

// fs2Prog is the Mk side: args[1] is the directory holding the tree a, and
// the working directory.
const fs2Prog = `import std.fs
import std.time

Err(a: &Arena, e: fs.Error | OutOfMemory): str from a {
    return a.Fmt("E {e}", (e)).Must()
}

P(a: &Arena, x: str, rel: str): str from a {
    return a.Fmt("{x}/{rel}", (x, rel)).Must()
}

KindName(k: fs.Kind): str {
    return match k {
        .File { "file" }
        .Dir { "dir" }
        .Link { "link" }
        .Other { "other" }
        _ {
            Panic("fs.Kind gained a member this match does not handle")
        }
    }
}

Report(h: &Heap, a: &Arena, io: Io, out: mut Writer, root: str, times: bool): fs.Error|OutOfMemory|WriteError!void {
    var w := try fs.Walk(h, io, root)
    defer w.Close()
    for {
        e := try w.Next(a, io)
        entry := e else {
            break
        }
        p := entry.path.Str().Must()
        rel := if p.Len() > root.Len() { p[root.Len() + 1..] } else { "." }
        st := try fs.StatLink(io, p)
        size := if entry.kind == fs.Kind.Dir { i64(0) } else { st.size }
        kind := KindName(entry.kind)
        if times && entry.kind == fs.Kind.File {
            try Print(out, "W {rel} {kind} {size} {m:o} {t} {d}\n", (rel, kind, size, m: st.permissions.mode, t: st.modified.UnixNanos(), d: entry.depth))
        } else {
            try Print(out, "W {rel} {kind} {size} {m:o} {d}\n", (rel, kind, size, m: st.permissions.mode, d: entry.depth))
        }
        if st.kind != entry.kind {
            try out.Str("W kind differs between Walk and StatLink\n")
        }
    }
}

// Size is p's size, or -1 when p cannot be stat-ed.
Size(io: Io, p: str): i64 {
    st := fs.Stat(io, p) else {
        return -1
    }
    return st.size
}

Hex(out: mut Writer, b: []u8): WriteError!void {
    for c in b {
        try Print(out, "{c:02x}", (c))
    }
}

Ops(a: &Arena, io: Io, out: mut Writer, x: str): WriteError|OutOfMemory!void {
    var s := "ok"
    fs.MakeDirAll(io, P(a, x, "new/a/b/c")) else e { s = Err(a, e) }
    try Print(out, "mda1 {s}\n", (s))
    s = "ok"
    fs.MakeDirAll(io, P(a, x, "new/a/b/c/")) else e { s = Err(a, e) }
    try Print(out, "mda2 {s}\n", (s))
    s = "ok"
    fs.MakeDirAll(io, P(a, x, "f0/sub/deeper")) else e { s = Err(a, e) }
    try Print(out, "mda3 {s}\n", (s))
    n := fs.Copy(io, P(a, x, "f0"), P(a, x, "new/cp")) else { -1 }
    try Print(out, "cp {n}\n", (n))
    s = "ok"
    _ = fs.Copy(io, P(a, x, "nope"), P(a, x, "new/cp2")) else e { s = Err(a, e) }
    try Print(out, "cpm {s}\n", (s))
    t := time.FromUnix(1_600_000_000, 987_654_300)
    s = "ok"
    fs.SetModified(io, P(a, x, "f1"), t) else e { s = Err(a, e) }
    m := fs.Stat(io, P(a, x, "f1")) else { fs.Stat(io, x).Must() }
    try Print(out, "sm {s} {t}\n", (s, t: m.modified.UnixNanos()))
    s = "ok"
    fs.SetPermissions(io, P(a, x, "f2"), fs.Permissions(mode: 0o444)) else e { s = Err(a, e) }
    q := fs.Stat(io, P(a, x, "f2")) else { fs.Stat(io, x).Must() }
    try Print(out, "sp {s} {m:o}\n", (s, m: q.permissions.mode))
    s = "ok"
    fs.Symlink(io, "f0", P(a, x, "lnk")) else e { s = Err(a, e) }
    try Print(out, "sl {s}\n", (s))
    if s == "ok" {
        target := fs.ReadLink(a, io, P(a, x, "lnk")) else e { Err(a, e) }
        lk := fs.StatLink(io, P(a, x, "lnk")).Must()
        fo := fs.Stat(io, P(a, x, "lnk")).Must()
        try Print(out, "rl {target} {k} {size}\n", (target, k: KindName(lk.kind), size: fo.size))
    }
    s = "ok"
    _ = fs.ReadLink(a, io, P(a, x, "f0")) else { s = "E" }
    try Print(out, "rlf {s}\n", (s))
    s = "ok"
    fs.RemoveAll(io, P(a, x, "d1")) else e { s = Err(a, e) }
    try Print(out, "ra {s}\n", (s))
    // d1/out was a link to outside, which keeps its file.
    try Print(out, "rao {n}\n", (n: Size(io, P(a, x, "outside/keep"))))
    // A link to a directory named with a trailing separator goes itself.
    s = "ok"
    fs.RemoveAll(io, P(a, x, "lnkd/")) else e { s = Err(a, e) }
    var lk := "kept"
    _ = fs.StatLink(io, P(a, x, "lnkd")) else { lk = "gone" }
    try Print(out, "ra4 {s} {n} {lk}\n", (s, n: Size(io, P(a, x, "outside/keep")), lk))
    s = "ok"
    fs.RemoveAll(io, P(a, x, "missing")) else e { s = Err(a, e) }
    try Print(out, "ra2 {s}\n", (s))
    s = "ok"
    fs.RemoveAll(io, P(a, x, "f3")) else e { s = Err(a, e) }
    try Print(out, "ra3 {s}\n", (s))
    wd := fs.Cwd(a, io) else e { Err(a, e) }
    try Print(out, "cwd {wd}\n", (wd))
    for rel in ["q/../r/./s", "", ".", "..//x/"] {
        abs := fs.Absolute(a, io, rel) else e { Err(a, e) }
        try Print(out, "abs {abs}\n", (abs))
    }
    abs := fs.Absolute(a, io, a.Fmt("{wd}/z/..//y/.", (wd)).Must()) else e { Err(a, e) }
    try Print(out, "abs {abs}\n", (abs))
    s = "ok"
    g := fs.OpenWith(io, P(a, x, "ow"), fs.CreateNew) else e {
        s = Err(a, e)
        fs.OpenWith(io, P(a, x, "ow"), fs.ReadOnly).Must()
    }
    g.Abandon()
    try Print(out, "ow1 {s}\n", (s))
    var ap := fs.OpenWith(io, P(a, x, "ow"), fs.Append).Must()
    ap.WriteAll(io, "xy".AsBytes()).Must()
    ap.Close(io).Must()
    try out.Str("ow2 ")
    try Hex(out, fs.ReadBytes(a, io, P(a, x, "ow")).Must())
    try out.Str("\n")
    var rw := fs.OpenWith(io, P(a, x, "ow"), fs.ReadWrite).Must()
    pos := rw.Seek(io, -2, fs.Whence.End).Must()
    var b: [8]u8 = [0; 8]
    k := rw.Read(io, mut b[..]).Must()
    rw.Truncate(io, 1).Must()
    size := rw.Size(io).Must()
    rw.Close(io).Must()
    try Print(out, "ow3 {pos} {size} ", (pos, size))
    try Hex(out, b[0..k])
    try out.Str("\n")
    s = "ok"
    h := fs.OpenWith(io, P(a, x, "nope2"), fs.ReadWrite) else e {
        s = Err(a, e)
        fs.OpenWith(io, P(a, x, "ow"), fs.ReadOnly).Must()
    }
    h.Abandon()
    try Print(out, "ow4 {s}\n", (s))
    s = "ok"
    _ = fs.Stat(io, P(a, x, "missing")) else e { s = Err(a, e) }
    try Print(out, "st1 {s}\n", (s))
    td := fs.TempDir(a, io) else e { Err(a, e) }
    try Print(out, "td {td}\n", (td))
    (file: tf, path: tp) := fs.CreateTemp(a, io, P(a, x, "new"), "pre") else e {
        try Print(out, "ct {e}\n", (e))
        return
    }
    tf.Close(io).Must()
    base := tp[P(a, x, "new").Len() + 1..]
    ok := base.StartsWith("pre") && fs.Stat(io, tp).Must().size == 0
    fs.Remove(io, tp).Must()
    try Print(out, "ct {ok}\n", (ok))
}

export Main(sys: &System): fs.Error|OutOfMemory|WriteError!void {
    var out := sys.Stdout()
    dir := sys.args[1]
    x := sys.arena.Fmt("{dir}/a", (dir)).Must()
    try out.Str("== walk1\n")
    try Report(sys.heap, sys.arena, sys.io, mut out, x, true)
    try out.Str("== ops\n")
    try Ops(sys.arena, sys.io, mut out, x)
    try out.Str("== walk2\n")
    try Report(sys.heap, sys.arena, sys.io, mut out, x, false)
    try out.Flush()
}
`

// fs2Helper is the Go side, a program of its own so it runs in the
// distribution too: "gen DIR SEED" makes DIR/a and DIR/b, "oracle DIR"
// prints the script's lines for DIR/b with Go's functions.
const fs2Helper = `package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math/rand/v2"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type node struct {
	rel  string
	dir  bool
	data []byte
	ro   bool
}

var names = []string{"f", "g", "\u00e9t\u00e9", "\u65e5\u672c", "a b", "x.y", "UP", "z-_~"}

func spec(seed uint64) []node {
	r := rand.New(rand.NewPCG(seed, 7))
	var ns []node
	file := func(rel string, n int) {
		b := make([]byte, n)
		for i := range b {
			b[i] = byte(r.IntN(256))
		}
		ns = append(ns, node{rel: rel, data: b, ro: r.IntN(5) == 0})
	}
	file("f0", 100)
	file("f1", 10)
	file("f2", 20)
	file("f3", 5)
	ns = append(ns, node{rel: "ow", data: []byte("hello")})
	var tree func(rel string, depth int)
	tree = func(rel string, depth int) {
		ns = append(ns, node{rel: rel, dir: true})
		nf := r.IntN(5)
		for i := 0; i < nf; i++ {
			file(rel+"/"+names[r.IntN(len(names))]+strconv.Itoa(i), r.IntN(3000))
		}
		if depth < 3 {
			nd := r.IntN(4)
			for i := 0; i < nd; i++ {
				tree(rel+"/d"+names[r.IntN(len(names))]+strconv.Itoa(i), depth+1)
			}
		}
	}
	tree("d1", 1)
	tree("t", 0)
	tree("e", 2)
	ns = append(ns, node{rel: "outside", dir: true})
	file("outside/keep", 4)
	return ns
}

// links are the symbolic links gen makes in each twin, target then link:
// a link to a directory outside d1 inside it, a loop to e's parent, and a
// link to a directory at the top. Walk follows none of them, and RemoveAll
// removes each itself.
var links = [][2]string{{"../outside", "d1/out"}, {"..", "e/self"}, {"outside", "lnkd"}}

func gen(dir string, seed uint64) error {
	ns := spec(seed)
	for _, side := range []string{"a", "b"} {
		root := filepath.Join(dir, side)
		if err := os.MkdirAll(root, 0o777); err != nil {
			return err
		}
		for i, n := range ns {
			p := filepath.Join(root, filepath.FromSlash(n.rel))
			if n.dir {
				if err := os.MkdirAll(p, 0o777); err != nil {
					return err
				}
				continue
			}
			if err := os.WriteFile(p, n.data, 0o644); err != nil {
				return err
			}
			mt := time.Unix(1_500_000_000+int64(i)*3_600, 123_456_700)
			if err := os.Chtimes(p, mt, mt); err != nil {
				return err
			}
		}
		for _, l := range links {
			// Windows without the privilege makes none, in both twins.
			_ = os.Symlink(filepath.FromSlash(l[0]), filepath.Join(root, filepath.FromSlash(l[1])))
		}
		for _, n := range ns {
			if n.ro && n.rel != "f0" && n.rel != "f2" {
				if err := os.Chmod(filepath.Join(root, filepath.FromSlash(n.rel)), 0o444); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func ename(err error) string {
	var en syscall.Errno
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, syscall.ENOTDIR):
		// First: on Windows Go's invented ENOTDIR also reads as ErrNotExist.
		return "E NotDirectory"
	case errors.Is(err, fs.ErrExist):
		return "E Exists"
	case errors.Is(err, fs.ErrNotExist):
		return "E NotFound"
	case errors.Is(err, syscall.EISDIR):
		return "E IsDirectory"
	case runtime.GOOS == "windows" && errors.As(err, &en) && en == 1314:
		return "E Unsupported"
	case errors.Is(err, fs.ErrPermission):
		return "E Denied"
	}
	return "E Os"
}

func kind(m fs.FileMode) string {
	switch {
	case m&fs.ModeSymlink != 0:
		return "link"
	case m.IsDir():
		return "dir"
	case m.IsRegular():
		return "file"
	}
	return "other"
}

func report(root string, times bool) {
	filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			fmt.Printf("W error %v\n", err)
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		st, err := os.Lstat(p)
		if err != nil {
			fmt.Printf("W error %v\n", err)
			return nil
		}
		k := kind(st.Mode())
		size := st.Size()
		if k == "dir" || k == "link" {
			// std.fs reports a link's size as 0 on both targets (Entry's
			// rule); Linux's lstat gives the target's length.
			size = 0
		}
		depth := 0
		if rel != "." {
			depth = strings.Count(filepath.ToSlash(rel), "/") + 1
		}
		perm := uint32(st.Mode().Perm())
		if times && k == "file" {
			fmt.Printf("W %s %s %d %o %d %d\n", rel, k, size, perm, st.ModTime().UnixNano(), depth)
		} else {
			fmt.Printf("W %s %s %d %o %d\n", rel, k, size, perm, depth)
		}
		return nil
	})
}

func copyFile(src, dst string) (int64, error) {
	in, err := os.Open(src)
	if err != nil {
		return -1, err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return -1, err
	}
	n, err := io.Copy(out, in)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	return n, err
}

func oracle(dir string) {
	x := filepath.Join(dir, "b")
	P := func(rel string) string { return x + "/" + rel }
	fmt.Println("== walk1")
	report(x, true)
	fmt.Println("== ops")
	fmt.Printf("mda1 %s\n", ename(os.MkdirAll(P("new/a/b/c"), 0o777)))
	fmt.Printf("mda2 %s\n", ename(os.MkdirAll(P("new/a/b/c/"), 0o777)))
	fmt.Printf("mda3 %s\n", ename(os.MkdirAll(P("f0/sub/deeper"), 0o777)))
	n, _ := copyFile(P("f0"), P("new/cp"))
	fmt.Printf("cp %d\n", n)
	_, err := copyFile(P("nope"), P("new/cp2"))
	fmt.Printf("cpm %s\n", ename(err))
	t := time.Unix(1_600_000_000, 987_654_300)
	err = os.Chtimes(P("f1"), time.Time{}, t)
	st, _ := os.Stat(P("f1"))
	fmt.Printf("sm %s %d\n", ename(err), st.ModTime().UnixNano())
	err = os.Chmod(P("f2"), 0o444)
	st, _ = os.Stat(P("f2"))
	fmt.Printf("sp %s %o\n", ename(err), uint32(st.Mode().Perm()))
	err = os.Symlink("f0", P("lnk"))
	fmt.Printf("sl %s\n", ename(err))
	if err == nil {
		target, err := os.Readlink(P("lnk"))
		if err != nil {
			target = ename(err)
		}
		lk, _ := os.Lstat(P("lnk"))
		fo, _ := os.Stat(P("lnk"))
		fmt.Printf("rl %s %s %d\n", target, kind(lk.Mode()), fo.Size())
	}
	_, err = os.Readlink(P("f0"))
	if err != nil {
		fmt.Println("rlf E")
	} else {
		fmt.Println("rlf ok")
	}
	fmt.Printf("ra %s\n", ename(os.RemoveAll(P("d1"))))
	size := func(p string) int64 {
		st, err := os.Stat(p)
		if err != nil {
			return -1
		}
		return st.Size()
	}
	fmt.Printf("rao %d\n", size(P("outside/keep")))
	err = os.RemoveAll(P("lnkd/"))
	lk := "kept"
	if _, e := os.Lstat(P("lnkd")); e != nil {
		lk = "gone"
	}
	fmt.Printf("ra4 %s %d %s\n", ename(err), size(P("outside/keep")), lk)
	fmt.Printf("ra2 %s\n", ename(os.RemoveAll(P("missing"))))
	fmt.Printf("ra3 %s\n", ename(os.RemoveAll(P("f3"))))
	wd, err := os.Getwd()
	if err != nil {
		wd = ename(err)
	}
	fmt.Printf("cwd %s\n", wd)
	for _, rel := range []string{"q/../r/./s", "", ".", "..//x/", wd + "/z/..//y/."} {
		abs, err := filepath.Abs(rel)
		if err != nil {
			abs = ename(err)
		}
		fmt.Printf("abs %s\n", abs)
	}
	g, err := os.OpenFile(P("ow"), os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o666)
	if err == nil {
		g.Close()
	}
	fmt.Printf("ow1 %s\n", ename(err))
	ap, _ := os.OpenFile(P("ow"), os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o666)
	ap.Write([]byte("xy"))
	ap.Close()
	b, _ := os.ReadFile(P("ow"))
	fmt.Printf("ow2 %x\n", b)
	rw, _ := os.OpenFile(P("ow"), os.O_RDWR, 0)
	pos, _ := rw.Seek(-2, io.SeekEnd)
	buf := make([]byte, 8)
	k, _ := rw.Read(buf)
	rw.Truncate(1)
	fi, _ := rw.Stat()
	rw.Close()
	fmt.Printf("ow3 %d %d %x\n", pos, fi.Size(), buf[:k])
	h, err := os.OpenFile(P("nope2"), os.O_RDWR, 0)
	if err == nil {
		h.Close()
	}
	fmt.Printf("ow4 %s\n", ename(err))
	_, err = os.Stat(P("missing"))
	fmt.Printf("st1 %s\n", ename(err))
	fmt.Printf("td %s\n", os.TempDir())
	tf, err := os.CreateTemp(P("new"), "pre")
	if err != nil {
		fmt.Printf("ct %s\n", ename(err))
	} else {
		tf.Close()
		os.Remove(tf.Name())
		fmt.Println("ct true")
	}
	fmt.Println("== walk2")
	report(x, false)
}

func main() {
	switch os.Args[1] {
	case "gen":
		seed, _ := strconv.ParseUint(os.Args[3], 10, 64)
		if err := gen(os.Args[2], seed); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	case "oracle":
		oracle(os.Args[2])
	}
}
`

// fs2Std is the std these programs and blocks build against: a copy of this
// tree's std with fs2RedFixtures, or, when MK_FS2_BASE names a snapshot of a
// whole std (for when other std files are mid-edit), a copy of it with this
// tree's std/fs.
func fs2Std(t *testing.T, root string) string {
	t.Helper()
	base := os.Getenv("MK_FS2_BASE")
	if base == "" {
		return copyStd(t, root, fs2RedFixtures(t))
	}
	stdDir := filepath.Join(root, "std")
	if err := os.CopyFS(stdDir, os.DirFS(base)); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(stdDir, "fs")); err != nil {
		t.Fatal(err)
	}
	if err := os.CopyFS(filepath.Join(stdDir, "fs"), os.DirFS("fs")); err != nil {
		t.Fatal(err)
	}
	return stdDir
}

// fs2Build builds src as a one-file package against fs2Std and returns
// the executable; a missing toolchain skips the test.
func fs2Build(t *testing.T, src string, flags ...string) string {
	t.Helper()
	root := t.TempDir()
	stdDir := fs2Std(t, root)
	if err := os.WriteFile(filepath.Join(root, "main.mk"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(root, "main")
	if runtime.GOOS == "windows" && !strings.Contains(strings.Join(flags, " "), "x86_64-linux") {
		exe += ".exe"
	}
	t.Setenv("MK_CACHE", filepath.Join(root, "cache"))
	args := append([]string{"build"}, flags...)
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

// fs2BuildHelper builds fs2Helper for goos into dir.
func fs2BuildHelper(t *testing.T, dir, goos string) string {
	t.Helper()
	src := filepath.Join(dir, "fs2helper.go")
	if err := os.WriteFile(src, []byte(fs2Helper), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "fs2helper-"+goos)
	if goos == "windows" {
		out += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", out, src)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOOS="+goos, "GOARCH=amd64", "CGO_ENABLED=0", "GOFLAGS=")
	if o, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build of the fs2 helper: %v\n%s", err, o)
	}
	return out
}

var fs2OsCode = regexp.MustCompile(`E Os\(code: -?\d+\)`)

// fs2Sections splits a run's output into its sections, a walk's lines
// sorted, separators made '/', the tree's letter and the Mk error payload
// made the Go side's.
func fs2Sections(out string) map[string][]string {
	secs := map[string][]string{}
	cur := ""
	for _, line := range strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n") {
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "== ") {
			cur = line[3:]
			continue
		}
		line = fs2OsCode.ReplaceAllString(line, "E Os")
		if strings.HasPrefix(line, "W ") {
			line = strings.ReplaceAll(line, `\`, "/")
		}
		secs[cur] = append(secs[cur], line)
	}
	for k, v := range secs {
		if strings.HasPrefix(k, "walk") {
			sort.Strings(v)
		}
	}
	return secs
}

// fs2Compare fails t for each line where the Mk run differs from Go's.
func fs2Compare(t *testing.T, leg, mk, gout string) {
	t.Helper()
	m, g := fs2Sections(mk), fs2Sections(gout)
	for _, sec := range []string{"walk1", "ops", "walk2"} {
		a, b := m[sec], g[sec]
		if len(b) == 0 {
			t.Errorf("%s %s: Go printed nothing", leg, sec)
		}
		n := max(len(a), len(b))
		bad := 0
		for i := 0; i < n && bad < 20; i++ {
			var x, y string
			if i < len(a) {
				x = a[i]
			}
			if i < len(b) {
				y = b[i]
			}
			if x != y {
				t.Errorf("%s %s line %d:\n  mk %q\n  go %q", leg, sec, i, x, y)
				bad++
			}
		}
	}
}

// TestFs_OracleWindows runs the script on Windows, dev and release, over
// three seeds, against Go's functions on the twin tree.
func TestFs_OracleWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("the Windows leg; TestFs_OracleLinux is Linux's")
	}
	dir := t.TempDir()
	helper := fs2BuildHelper(t, dir, "windows")
	for _, release := range []bool{false, true} {
		var flags []string
		if release {
			flags = append(flags, "--release")
		}
		exe := fs2Build(t, fs2Prog, flags...)
		for seed := 1; seed <= 3; seed++ {
			work := filepath.Join(dir, fmt.Sprintf("w%v%d", release, seed))
			if err := os.MkdirAll(work, 0o755); err != nil {
				t.Fatal(err)
			}
			if o, err := exec.Command(helper, "gen", work, fmt.Sprint(seed)).CombinedOutput(); err != nil {
				t.Fatalf("gen: %v\n%s", err, o)
			}
			mk := exec.Command(exe, work)
			mk.Dir = work
			mo, err := mk.CombinedOutput()
			if err != nil {
				t.Fatalf("mk program (release %v, seed %d): %v\n%s", release, seed, err, mo)
			}
			gcmd := exec.Command(helper, "oracle", work)
			gcmd.Dir = work
			gout, err := gcmd.CombinedOutput()
			if err != nil {
				t.Fatalf("go oracle: %v\n%s", err, gout)
			}
			if seed == 1 && !release {
				secs := fs2Sections(string(mo))
				t.Logf("ops, seed 1: %q; walk1 has %d lines", secs["ops"], len(secs["walk1"]))
			}
			fs2Compare(t, fmt.Sprintf("windows release=%v seed=%d", release, seed), string(mo), string(gout))
		}
	}
}

// TestFs_OracleLinux runs the script on x86_64-linux, dev and release,
// in fresh directories under the distribution's /tmp (linuxScratchSh),
// one wsl.exe client for the whole run. MK_LINUX=1, under the WSL lock.
func TestFs_OracleLinux(t *testing.T) {
	linuxGate(t)
	dir, err := packageTempDir("mkfs2oracle")
	if err != nil {
		t.Fatal(err)
	}
	helper := fs2BuildHelper(t, dir, "linux")
	var sh strings.Builder
	sh.WriteString("#!/bin/sh\n")
	var legs []string
	for _, release := range []bool{false, true} {
		flags := []string{"--target", "x86_64-linux"}
		if release {
			flags = append(flags, "--release")
		}
		exe := fs2Build(t, fs2Prog, flags...)
		keep := filepath.Join(dir, fmt.Sprintf("fs2prog-%v", release))
		b, err := os.ReadFile(exe)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(keep, b, 0o755); err != nil {
			t.Fatal(err)
		}
		for seed := 1; seed <= 3; seed++ {
			leg := fmt.Sprintf("linux release=%v seed=%d", release, seed)
			legs = append(legs, leg)
			cmd := fmt.Sprintf(`cd "$MK_LINUX_TMP" && %s gen . %d && echo BEGIN-MK && %s . ; echo END-MK; echo BEGIN-GO; %s oracle . ; echo END-GO`,
				rtsWSLPath(helper), seed, rtsWSLPath(keep), rtsWSLPath(helper))
			fmt.Fprintf(&sh, "echo 'LEG %s'\n", leg)
			sh.WriteString(linuxScratchSh(cmd))
		}
	}
	out := linuxRunScript(t, dir, "fs2oracle.sh", sh.String(), 5*time.Minute)
	parts := strings.Split(out, "LEG ")
	seen := 0
	for _, part := range parts[1:] {
		leg, rest, _ := strings.Cut(part, "\n")
		mkOut, ok1 := fs2Between(rest, "BEGIN-MK\n", "END-MK")
		goOut, ok2 := fs2Between(rest, "BEGIN-GO\n", "END-GO")
		if !ok1 || !ok2 {
			t.Errorf("%s: incomplete output:\n%s", leg, rest)
			continue
		}
		seen++
		fs2Compare(t, leg, mkOut, goOut)
	}
	if seen != len(legs) {
		t.Errorf("%d legs ran, want %d", seen, len(legs))
	}
}

// fs2Between is the text of s after open and before close.
func fs2Between(s, open, close string) (string, bool) {
	_, after, ok := strings.Cut(s, open)
	if !ok {
		return "", false
	}
	in, _, ok := strings.Cut(after, close)
	return in, ok
}

// fs2RedFixtures patches, in a copied std, two test files outside std/fs
// that may not compile against this tree: std/sim/tests_sim.mk's exhaustive
// match on fs.Error, which fs.Unsupported extends, and std/net/tests_net.mk's
// format of an error with no Format. Each patch applies only while its file
// still holds the old text, so the fixture goes quiet once that file is fixed.
func fs2RedFixtures(t *testing.T) map[string]string {
	t.Helper()
	fix := map[string]string{}
	sim, err := os.ReadFile("sim/tests_sim.mk")
	if err == nil && !strings.Contains(string(sim), "fs.Unsupported") {
		old := "        fs.InvalidPath {\n            return \"InvalidPath\"\n        }\n"
		fix["sim/tests_sim.mk"] = strings.Replace(string(sim), old, old+"        fs.Unsupported {\n            return \"Unsupported\"\n        }\n", 1)
	}
	net, err := os.ReadFile("net/tests_net.mk")
	if err == nil {
		old := `Panic(sys.arena.Fmt("dial of a closed port: {e}", (e)).Must())`
		if strings.Contains(string(net), old) {
			fix["net/tests_net.mk"] = strings.Replace(string(net), old, `Panic("dial of a closed port")`, 1)
		}
	}
	return fix
}

// TestFs_LinuxBlocks runs std.fs's test blocks on x86_64-linux, dev,
// through mk test: one wsl.exe per block, in the working directory on the
// drive (locks, links and renames there
// behave as on ext4 for these cases; permission bits keep only the write
// bit, which the blocks' permission checks read; the block of RemoveAll
// against a refilling creator makes its tree under TempDir, /tmp, since
// over 9p its 256 listings again reach its 30 s deadline). MK_LINUX=1,
// under the WSL lock.
func TestFs_LinuxBlocks(t *testing.T) {
	linuxGate(t)
	rtsNeedWSL(t)
	root := t.TempDir()
	stdDir := fs2Std(t, root)
	t.Setenv("MK_CACHE", filepath.Join(root, "cache"))
	out, status := mkCmd(t, root, stdDir, "test", "--std=std.fs", "--target", "x86_64-linux")
	if strings.Contains(out, "no usable LLVM") {
		t.Skipf("needs the LLVM toolchain: %s", out)
	}
	if status != 0 || !strings.Contains(out, " passed, 0 failed") {
		t.Fatalf("mk test --std=std.fs --target x86_64-linux: status %d\n%s", status, out)
	}
	t.Logf("%s", out[max(0, len(out)-200):])
}

// TestFs_WindowsBlocks runs std.fs's test blocks on Windows, dev and
// release, through mk test.
func TestFs_WindowsBlocks(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("the Windows leg; TestFs_LinuxBlocks is Linux's")
	}
	root := t.TempDir()
	stdDir := fs2Std(t, root)
	t.Setenv("MK_CACHE", filepath.Join(root, "cache"))
	for _, flags := range [][]string{nil, {"--release"}} {
		args := append([]string{"test", "--std=std.fs"}, flags...)
		out, status := mkCmd(t, root, stdDir, args...)
		if strings.Contains(out, "no usable LLVM") {
			t.Skipf("needs the LLVM toolchain: %s", out)
		}
		if status != 0 || !strings.Contains(out, " passed, 0 failed") {
			t.Fatalf("mk %s: status %d\n%s", strings.Join(args, " "), status, out)
		}
	}
}

// fs2DeepProg walks args[1], counting entries by kind and the deepest
// depth, then removes it with RemoveAll.
const fs2DeepProg = `import std.fs

export Main(sys: &System): fs.Error|OutOfMemory|WriteError!void {
    root := sys.args[1]
    var w := try fs.Walk(sys.heap, sys.io, root)
    defer w.Close()
    var dirs := 0
    var files := 0
    var deepest := 0
    for {
        e := try w.Next(sys.arena, sys.io) else {
            break
        }
        if e.kind == fs.Kind.Dir {
            dirs += 1
        } else {
            files += 1
        }
        if e.depth > deepest {
            deepest = e.depth
        }
    }
    try fs.RemoveAll(sys.io, root)
    try sys.Print("{dirs} {files} {deepest}\n", (dirs, files, deepest))
}
`

// TestFs_DeepTree: Walk and RemoveAll of a 300-level tree that Go makes
// (Go's os takes paths past MAX_PATH on Windows), each level holding a
// file; afterwards the tree is gone. Walk and RemoveAll open each
// directory inside its held parent, so no whole path is long.
func TestFs_DeepTree(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "deep")
	p := root
	for i := 0; i < 300; i++ {
		p = filepath.Join(p, "level")
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(p, "f"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	exe := fs2Build(t, fs2DeepProg)
	cmd := exec.Command(exe, "deep")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if got := strings.TrimSpace(string(out)); got != "301 300 301" {
		t.Errorf("walk: got %q, want %q (dirs, files, deepest)", got, "301 300 301")
	}
	if _, err := os.Lstat(root); !os.IsNotExist(err) {
		t.Errorf("RemoveAll left the tree: %v", err)
	}
}

// fs2RmProg removes args[1] with RemoveAll and walks args[2] first,
// printing each entry's kind, so a junction's kind in a walk is seen.
const fs2RmProg = `import std.fs

export Main(sys: &System): fs.Error|OutOfMemory|WriteError!void {
    var w := try fs.Walk(sys.heap, sys.io, sys.args[1])
    defer w.Close()
    for {
        e := w.Next(sys.arena, sys.io) else err {
            try sys.Print("walk {err}\n", (err))
            continue
        }
        entry := e else {
            break
        }
        k := match entry.kind {
            .File { "file" }
            .Dir { "dir" }
            .Link { "link" }
            .Other { "other" }
            _ {
                Panic("fs.Kind gained a member this match does not handle")
            }
        }
        try sys.Print("{k} {d}\n", (k, d: entry.depth))
    }
    try fs.RemoveAll(sys.io, sys.args[1])
    try sys.Print("removed\n")
}
`

// TestFs_RemoveAllJunction (Windows): RemoveAll of a tree holding a
// junction and a directory link to a directory outside it removes the
// junction and the link, ends, and keeps the outside directory's file.
func TestFs_RemoveAllJunction(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("junctions are Windows'")
	}
	dir := t.TempDir()
	outside := filepath.Join(dir, "outside")
	tree := filepath.Join(dir, "tree")
	for _, d := range []string{outside, filepath.Join(tree, "sub")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(outside, "keep"), []byte("k"), 0o644); err != nil {
		t.Fatal(err)
	}
	if o, err := exec.Command("cmd", "/c", "mklink", "/J", filepath.Join(tree, "sub", "junc"), outside).CombinedOutput(); err != nil {
		t.Fatalf("mklink /J: %v\n%s", err, o)
	}
	linked := os.Symlink(outside, filepath.Join(tree, "dlink")) == nil
	exe := fs2Build(t, fs2RmProg)
	cmd := exec.Command(exe, "tree")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "removed") {
		t.Fatalf("%v\n%s", err, out)
	}
	t.Logf("walk (directory link made: %v):\n%s", linked, out)
	if strings.Contains(string(out), "walk ") {
		t.Errorf("the walk reported an error on a junction or a link:\n%s", out)
	}
	if _, err := os.Lstat(tree); !os.IsNotExist(err) {
		t.Errorf("RemoveAll left the tree: %v", err)
	}
	if b, err := os.ReadFile(filepath.Join(outside, "keep")); err != nil || string(b) != "k" {
		t.Errorf("the junction's target lost its file: %v %q", err, b)
	}
}
