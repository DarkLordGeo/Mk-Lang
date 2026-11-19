// Why: on Windows, sys.StatEx asks GetFileAttributesExW first and takes the
// handle path (CreateFileW and the information classes) only when that step
// does not answer; the two routes must give the same answer on every path.
// What: TestFs_StatRoutesAgree and TestFs_StatStandardUser, with the planted
// probe module statRouteProbe and its printer statRoutePrint.
// How: plants a probe beside std.sys's Windows files that runs each route on
// one path, over files, links, a junction, devices, pipes and a mailslot.

//go:build windows

package std

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The probe runs, on one path, the shipped StatEx, the handle path alone,
// the attributes step as StatEx takes it, the step with no guard, and the
// shipped StatPath, over the paths whose answers could differ: ordinary
// files and directories, missing paths, links, a junction, devices, named
// pipes and a mailslot by every spelling, and files another process holds.
// StatEx's every word, error and OS code
// must equal the handle path's (TestFs_StatStandardUser covers the one
// difference, an entry a standard user may list but not open);
// the step must answer for the ordinary entries and the missing paths and
// leave links, the junction and the devices to the handle path; StatPath
// must give StatEx's kind, size and time. A named pipe of one listening
// instance must answer StatEx and StatPath as it answers the handle path
// alone: the step opens a pipe as a client and takes an instance, so
// neither takes it for a UNC, device or \??\ path.

// statRouteProbe sits in std/sys/windows of the test's std copy, so it can
// call the routes StatEx is made of.
const statRouteProbe = `// The stat-route probe (std/std-fs_statroute_test.go).

import std.core
import std.raw

// Declared here for the probe only; the test adds the name to the copy's
// kernel32.def.
extern CreateMailslotW(name: *u16, max: u32, timeout: u32, sa: *u8): uint

/// One route's answer: the error, the OS code and the four words.
export struct StatRoute {
    export err: u32
    export os: i64
    export a: i64
    export b: i64
    export c: i64
    export d: i64
}

RouteOf(r: OsResult, w: core.IoWords): StatRoute {
    return StatRoute(err: u32(r.err), os: r.os, a: w.a, b: w.b, c: w.c, d: w.d)
}

/// StatEx as std ships it.
export StatRouteFull(p: []u8, nofollow: bool): StatRoute {
    var w := core.IoWords(a: -1, b: -1, c: -1, d: -1)
    r := StatEx(p, nofollow, mut w)
    return RouteOf(r, w)
}

/// The handle path alone.
export StatRouteHandle(p: []u8, nofollow: bool): StatRoute {
    var w := core.IoWords(a: -1, b: -1, c: -1, d: -1)
    var small: [1024]u16 = [0; 1024]
    wp := WidePath(PathOf(p), mut small[..])
    if wp.err != core.SysErr.None {
        return RouteOf(FailWith(wp.err), w)
    }
    r := StatExOpen(wp.p, nofollow, mut w)
    FreePath(wp)
    return RouteOf(r, w)
}

/// The attributes step, as StatEx takes it when guarded is true (not for a
/// path AttributesStep refuses), or on any path: err 255 when it leaves the
/// path to the handle path.
export StatRouteStep(p: []u8, guarded: bool): StatRoute {
    var w := core.IoWords(a: -1, b: -1, c: -1, d: -1)
    var out := StatRoute(err: 255, os: 0, a: -1, b: -1, c: -1, d: -1)
    if guarded && !AttributesStep(p) {
        return out
    }
    var small: [1024]u16 = [0; 1024]
    wp := WidePath(PathOf(p), mut small[..])
    if wp.err != core.SysErr.None {
        return RouteOf(FailWith(wp.err), w)
    }
    if r := StatExAttributes(wp.p, mut w) {
        out = RouteOf(r, w)
    }
    FreePath(wp)
    return out
}

/// StatPath as std ships it, its kind, size and time in a, b and c (it has
/// no mode bits: d is -1).
export StatRoutePath(p: []u8): StatRoute {
    var st := core.IoStat(kind: 0, size: -1, modified: -1)
    s := PathOf(p)
    r := StatPath(s, mut st)
    var w := core.IoWords(a: -1, b: -1, c: -1, d: -1)
    if r.err == core.SysErr.None {
        w.a = i64(st.kind)
        w.b = st.size
        w.c = st.modified
    }
    return RouteOf(r, w)
}

/// Makes n listening instances of the named pipe p (\\.\pipe\...), which
/// stay open until the process ends; returns how many were made.
export PipeServe(p: []u8, n: int): int {
    var small: [1024]u16 = [0; 1024]
    wp := WidePath(PathOf(p), mut small[..])
    if wp.err != core.SysErr.None {
        return 0
    }
    var made := 0
    unsafe "kernel32; wp.p is NUL-terminated; the handles live until the process ends" {
        for _ in 0..n {
            // PIPE_ACCESS_DUPLEX, PIPE_TYPE_BYTE, PIPE_UNLIMITED_INSTANCES
            h := CreateNamedPipeW(wp.p, 3, 0, 255, 512, 512, 0, raw.Null[u8]())
            if h != InvalidHandle {
                made += 1
            }
        }
    }
    FreePath(wp)
    return made
}

/// Makes the mailslot p (\\.\mailslot\...), open until the process ends; 1
/// when made.
export MailslotServe(p: []u8): int {
    var small: [1024]u16 = [0; 1024]
    wp := WidePath(PathOf(p), mut small[..])
    if wp.err != core.SysErr.None {
        return 0
    }
    var made := 0
    unsafe "kernel32; wp.p is NUL-terminated; the handle lives until the process ends" {
        h := CreateMailslotW(wp.p, 0, 0, raw.Null[u8]())
        if h != InvalidHandle {
            made = 1
        }
    }
    FreePath(wp)
    return made
}
`

// statRoutePrint is a std module of the copy that prints the routes.
const statRoutePrint = `// The stat-route printer (std/std-fs_statroute_test.go).

import std.sys

Line(out: mut Writer, r: sys.StatRoute): WriteError!void {
    e := r.err
    o := r.os
    a := r.a
    b := r.b
    c := r.c
    d := r.d
    try Print(out, " {e} {o} {a} {b} {c} {d}", (e, o, a, b, c, d))
}

/// args: [1] a pipe to make with 200 instances, [2] a prefix for pipes of
/// one instance, [3] a mailslot to make, [4] the count n of spellings,
/// [5..5 + n] the spellings of a pipe of one instance, @ standing for its
/// name, then the paths. For each spelling: "single <i> handle ... | full
/// ... | path ...", the handle route alone, StatEx and StatPath each on a
/// pipe of its own, of one instance. For each path and nofollow: "<k> <nf>
/// full ... | handle ... | fast ... | step ... | path ...", the handle route
/// asked first and then StatEx on an odd path, the other way round on an
/// even one; fast is the step as StatEx takes it, step the step on any
/// path, path StatPath.
export Run(own: &System, out: mut Writer, args: []str): WriteError!void {
    made := sys.PipeServe(args[1].AsBytes(), 200)
    slot := sys.MailslotServe(args[3].AsBytes())
    n := Cast[int](args[4].ParseInt() else { 0 })
    try Print(out, "made {made} {slot}\n", (made, slot))
    var pipes := 0
    for i in 0..n {
        var r: [3]sys.StatRoute = [sys.StatRoute(err: 0, os: 0, a: 0, b: 0, c: 0, d: 0); 3]
        for role in 0..3 {
            name := own.arena.Fmt("{p}-{k}", (p: args[2], k: pipes)) else { "" }
            pipes += 1
            full := own.arena.Fmt("\\\\.\\pipe\\{name}", (name)) else { "" }
            _ = sys.PipeServe(full.AsBytes(), 1)
            spelled := args[5 + i].Replace(own.arena, "@", name) else { "" }
            r[role] = if role == 0 {
                sys.StatRouteHandle(spelled.AsBytes(), false)
            } else if role == 1 {
                sys.StatRouteFull(spelled.AsBytes(), false)
            } else {
                sys.StatRoutePath(spelled.AsBytes())
            }
        }
        try Print(out, "single {i} handle", (i))
        try Line(out, r[0])
        try out.Str(" | full")
        try Line(out, r[1])
        try out.Str(" | path")
        try Line(out, r[2])
        try out.Str("\n")
    }
    for k in 5 + n..args.Len() {
        p := args[k].AsBytes()
        for nf in 0..2 {
            nofollow := nf == 1
            var full := sys.StatRoute(err: 0, os: 0, a: 0, b: 0, c: 0, d: 0)
            var handle := full
            if k % 2 == 0 {
                full = sys.StatRouteFull(p, nofollow)
                handle = sys.StatRouteHandle(p, nofollow)
            } else {
                handle = sys.StatRouteHandle(p, nofollow)
                full = sys.StatRouteFull(p, nofollow)
            }
            fast := sys.StatRouteStep(p, true)
            step := sys.StatRouteStep(p, false)
            path := sys.StatRoutePath(p)
            try Print(out, "{k} {nf} full", (k, nf))
            try Line(out, full)
            try out.Str(" | handle")
            try Line(out, handle)
            try out.Str(" | fast")
            try Line(out, fast)
            try out.Str(" | step")
            try Line(out, step)
            try out.Str(" | path")
            try Line(out, path)
            try out.Str("\n")
        }
    }
}
`

const statRouteMain = `import std.statprobe

export Main(sys: &System): WriteError!void {
    var out := sys.Stdout()
    try statprobe.Run(sys, mut out, sys.args)
    try out.Flush()
}
`

// statRouteCase is one path of the probe run and what the attributes step,
// as StatEx takes it, must do with it: answer (true) or leave it to the
// handle path (false).
type statRouteCase struct {
	name   string
	path   string
	answer bool
	any    bool   // the step may answer or not: only the agreement is judged
	skip   string // why the case is not run, when it is not
	unstep bool   // the unguarded step must leave it too (the zero-time rule)
}

// statRoutes is one probe line of a path: each route's six fields.
type statRoutes struct {
	full, handle, fast, step, path string
}

// statRouteParse splits the probe's output: the counts line, the single-
// instance lines, then two lines per path.
func statRouteParse(t *testing.T, out string, singles, paths int) (head string, single [][3]string, rows []statRoutes) {
	t.Helper()
	lines := strings.Split(strings.TrimSuffix(strings.ReplaceAll(out, "\r\n", "\n"), "\n"), "\n")
	if len(lines) != 1+singles+2*paths {
		t.Fatalf("%d lines for %d spellings and %d paths:\n%s", len(lines), singles, paths, out)
	}
	head = lines[0]
	for i := 0; i < singles; i++ {
		s := strings.TrimPrefix(lines[1+i], fmt.Sprintf("single %d handle ", i))
		f := strings.Split(s, " | ")
		if len(f) != 3 {
			t.Fatalf("bad line %q", lines[1+i])
		}
		single = append(single, [3]string{f[0], strings.TrimPrefix(f[1], "full "), strings.TrimPrefix(f[2], "path ")})
	}
	for i, ln := range lines[1+singles:] {
		parts := strings.Split(ln, " | ")
		if len(parts) != 5 {
			t.Fatalf("bad line %q", ln)
		}
		rows = append(rows, statRoutes{
			full:   strings.TrimPrefix(parts[0], fmt.Sprintf("%d %d full ", 5+singles+i/2, i%2)),
			handle: strings.TrimPrefix(parts[1], "handle "),
			fast:   strings.TrimPrefix(parts[2], "fast "),
			step:   strings.TrimPrefix(parts[3], "step "),
			path:   strings.TrimPrefix(parts[4], "path "),
		})
	}
	return head, single, rows
}

// statRouteFirst5 is a route's fields without the mode bits, StatPath's view.
func statRouteFirst5(r string) string {
	f := strings.Fields(r)
	if len(f) != 6 {
		return r
	}
	return strings.Join(f[:5], " ")
}

// statRoutePipeSpellings are the ways to name a pipe of one instance, @ its
// name: \\.\, NT \??\ (three forms), \\?\ and forward slashes.
var statRoutePipeSpellings = []string{`\\.\pipe\@`, `\??\pipe\@`, `\??\PIPE\@`, `\??\GLOBALROOT\Device\NamedPipe\@`, `\\?\pipe\@`, `//./pipe/@`}

// TestFs_StatRoutesAgree: on every path, StatEx's full answer equals the
// handle path's, word for word, following links and not; the attributes
// step answers for the ordinary entries and the missing paths and leaves
// the rest to the handle path; StatPath agrees with StatEx; a pipe of one
// instance answers StatEx and StatPath as it answers the handle path, by
// every spelling.
func TestFs_StatRoutesAgree(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows only: the Linux StatEx is one statx")
	}
	root := t.TempDir()
	fx := filepath.Join(root, "fx")
	mk := func(p string, data string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(fx, p), []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(fx, "d", "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	mk("f.txt", "hello")
	mk("empty", "")
	mk("ro.txt", "read only")
	if err := os.Chmod(filepath.Join(fx, "ro.txt"), 0o444); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(filepath.Join(fx, "ro.txt"), 0o644) })
	mk("locked.txt", "held")
	mk("d/inner.bin", strings.Repeat("x", 70000))
	long := filepath.Join(fx, strings.Repeat("l", 120), strings.Repeat("m", 120))
	if err := os.MkdirAll(long, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(long, "deep.txt"), []byte("deep"), 0o644); err != nil {
		t.Fatal(err)
	}
	var linkSkip string
	if err := os.Symlink(filepath.Join(fx, "f.txt"), filepath.Join(fx, "lf")); err != nil {
		linkSkip = "symbolic links refused here: " + err.Error()
	} else {
		if err := os.Symlink(filepath.Join(fx, "d"), filepath.Join(fx, "ld")); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join(fx, "gone"), filepath.Join(fx, "dangling")); err != nil {
			t.Fatal(err)
		}
	}
	var junctionSkip string
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", filepath.Join(fx, "j"), filepath.Join(fx, "d")).CombinedOutput(); err != nil {
		junctionSkip = fmt.Sprintf("mklink /J failed: %v %s", err, out)
	}
	// A file this process holds with no sharing, so every other open of it
	// is a sharing violation, as pagefile.sys is.
	lp, err := syscall.UTF16PtrFromString(filepath.Join(fx, "locked.txt"))
	if err != nil {
		t.Fatal(err)
	}
	lh, err := syscall.CreateFile(lp, syscall.GENERIC_READ, 0, nil, syscall.OPEN_EXISTING, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { syscall.CloseHandle(lh) })
	pipe := fmt.Sprintf(`\\.\pipe\mk-statroute-stat-%d`, os.Getpid())
	slot := fmt.Sprintf(`\\.\mailslot\mk-statroute-stat-%d`, os.Getpid())
	// A directory link to the pipe directory: a path with one separator
	// that reaches a named pipe.
	pipeLinkSkip := linkSkip
	if pipeLinkSkip == "" {
		if err := os.Symlink(`\\.\pipe\`, filepath.Join(fx, "pd")); err != nil {
			pipeLinkSkip = "no link to the pipe directory: " + err.Error()
		}
	}
	pagefile := `C:\pagefile.sys`
	var pageSkip string
	if _, err := os.Lstat(pagefile); err != nil && os.IsNotExist(err) {
		pageSkip = "no " + pagefile + " on this machine"
	}
	absF := filepath.Join(fx, "f.txt")
	cases := []statRouteCase{
		{name: "file", path: "fx/f.txt", answer: true},
		{name: "directory", path: "fx/d", answer: true},
		{name: "directory with a trailing separator", path: `fx\d\`, answer: true},
		{name: "file of 70000 bytes", path: "fx/d/inner.bin", answer: true},
		{name: "empty file", path: "fx/empty", answer: true},
		{name: "read-only file", path: "fx/ro.txt", answer: true},
		{name: "file by a path past 248 units", path: filepath.Join(long, "deep.txt"), answer: true},
		{name: "current directory", path: ".", answer: true},
		{name: "missing path", path: "fx/nope", answer: true},
		{name: "missing parent", path: "fx/nope/x", answer: true},
		{name: "path through a file", path: "fx/f.txt/x", answer: true},
		{name: "file with a trailing separator", path: `fx\f.txt\`, any: true},
		{name: "name with a *", path: "fx/a*b", any: true},
		{name: "name with a ?", path: "fx/f.tx?", any: true},
		{name: "symlink to a file", path: "fx/lf", skip: linkSkip},
		{name: "symlink to a directory", path: "fx/ld", skip: linkSkip},
		{name: "file through a symlinked directory", path: "fx/ld/inner.bin", answer: true, skip: linkSkip},
		{name: "dangling symlink", path: "fx/dangling", skip: linkSkip},
		{name: "path through a dangling symlink", path: "fx/dangling/x", any: true, skip: linkSkip},
		{name: "junction", path: "fx/j", skip: junctionSkip},
		{name: "file through a junction", path: "fx/j/inner.bin", answer: true, skip: junctionSkip},
		{name: "NUL", path: "NUL"},
		{name: "CON", path: "CON"},
		{name: `\\.\NUL`, path: `\\.\NUL`},
		{name: `\\.\CONIN$`, path: `\\.\CONIN$`, any: true},
		{name: "named pipe", path: pipe, unstep: true},
		{name: "named pipe in capitals", path: strings.ToUpper(pipe), unstep: true},
		{name: "named pipe through GLOBALROOT", path: `\\?\GLOBALROOT\Device\NamedPipe\` + filepath.Base(pipe), unstep: true},
		{name: `named pipe by \??\pipe\`, path: `\??\pipe\` + filepath.Base(pipe), unstep: true},
		{name: `named pipe by \??\GLOBALROOT\`, path: `\??\GLOBALROOT\Device\NamedPipe\` + filepath.Base(pipe), unstep: true},
		{name: "the pipe directory", path: `\\.\pipe\`, any: true},
		{name: "mailslot", path: slot},
		{name: `mailslot by \??\mailslot\`, path: `\??\mailslot\` + filepath.Base(slot)},
		{name: "a volume", path: `\\.\C:`, any: true},
		{name: "a drive's root", path: `C:\`, answer: true},
		{name: `a drive's root by \\?\C:\`, path: `\\?\C:\`, answer: true},
		{name: `a drive's root by \??\C:\`, path: `\??\C:\`},
		{name: `file by a \\?\ path`, path: `\\?\` + absF, answer: true},
		{name: `file by a \\?\ path, lower-case drive`, path: `\\?\` + strings.ToLower(absF[:1]) + absF[1:], answer: true},
		{name: `file by a //?/ path`, path: `//?/` + filepath.ToSlash(absF)},
		{name: `file by a \??\ path`, path: `\??\` + absF},
		{name: `file by \\?\X: with no separator after`, path: `\\?\` + absF[:2]},
		{name: "named pipe through a link to the pipe directory", path: "fx/pd/" + filepath.Base(pipe), any: true, unstep: true, skip: pipeLinkSkip},
		{name: "file held with no sharing", path: "fx/locked.txt", answer: true},
		{name: "pagefile.sys", path: pagefile, skip: pageSkip},
	}
	var run []statRouteCase
	for _, c := range cases {
		if c.skip != "" {
			t.Logf("not run: %s: %s", c.name, c.skip)
			continue
		}
		run = append(run, c)
	}
	exe := statRouteBuild(t, root)
	// The known limit (AttributesStep's comment): through a directory link
	// to \\.\pipe\ the step still runs and takes the pipe's one instance.
	spellings := append([]string(nil), statRoutePipeSpellings...)
	viaLink := ""
	if pipeLinkSkip == "" {
		viaLink = "fx/pd/@"
		spellings = append(spellings, viaLink)
	}
	args := []string{pipe, filepath.Base(pipe) + "-one", slot, strconv.Itoa(len(spellings))}
	args = append(args, spellings...)
	for _, c := range run {
		args = append(args, c.path)
	}
	r := runNative(t, exe, "", args...)
	if r.code != 0 {
		t.Fatalf("exit %d\nstdout:\n%s\nstderr:\n%s", r.code, r.stdout, r.stderr)
	}
	head, single, rows := statRouteParse(t, r.stdout, len(spellings), len(run))
	if head != "made 200 1" {
		t.Fatalf("the pipe and the mailslot were not made: %q", head)
	}
	for i, s := range single {
		t.Logf("pipe of one instance by %-36s handle path [%s] StatEx [%s] StatPath [%s]", spellings[i], s[0], s[1], s[2])
		if spellings[i] == viaLink {
			t.Logf("known limit, not judged: by %s StatEx gives [%s] and StatPath [%s] where the handle path gives [%s]", viaLink, s[1], s[2], s[0])
			continue
		}
		if !strings.HasPrefix(s[0], "0 0 4 ") {
			t.Errorf("a pipe of one instance by %s: the handle path does not reach it as a pipe (IoKindOther): [%s]", spellings[i], s[0])
		}
		if s[0] != s[1] {
			t.Errorf("a pipe of one instance by %s: the handle path gives [%s], StatEx [%s]", spellings[i], s[0], s[1])
		}
		if statRouteFirst5(s[0]) != statRouteFirst5(s[2]) {
			t.Errorf("a pipe of one instance by %s: the handle path gives [%s], StatPath [%s]", spellings[i], s[0], s[2])
		}
	}
	for i, w := range rows {
		c := run[i/2]
		nf := i % 2
		t.Logf("%-46s nofollow=%d full [%s] handle [%s] step as taken [%s] unguarded [%s] StatPath [%s]", c.name, nf, w.full, w.handle, w.fast, w.step, w.path)
		if w.full != w.handle {
			t.Errorf("%s, nofollow=%d: StatEx gives [%s], the handle path [%s]", c.name, nf, w.full, w.handle)
		}
		answered := !strings.HasPrefix(w.fast, "255 ")
		if !c.any && answered != c.answer {
			t.Errorf("%s: the attributes step answered %v, want %v ([%s])", c.name, answered, c.answer, w.fast)
		}
		if answered && w.fast != w.full {
			t.Errorf("%s, nofollow=%d: the attributes step gives [%s], StatEx [%s]", c.name, nf, w.fast, w.full)
		}
		// The zero-time rule, on its own: the unguarded step leaves a named
		// pipe to the handle path (else it would say File).
		if c.unstep && !strings.HasPrefix(w.step, "255 ") {
			t.Errorf("%s: the unguarded attributes step answered [%s]: the zero-time rule did not leave the pipe to the handle path", c.name, w.step)
		}
		// StatPath follows links, as StatEx with nofollow false; it has no
		// mode bits.
		if nf == 0 && statRouteFirst5(w.path) != statRouteFirst5(w.full) {
			t.Errorf("%s: StatPath gives [%s], StatEx [%s]", c.name, w.path, w.full)
		}
	}
}

// TestFs_StatStandardUser: as a standard user (runas /trustlevel:0x20000,
// a token with Administrators for deny only and no privileges), an entry
// the user may list but not open, as System Volume Information, has its
// facts by X:\ and by \\?\X:\ alike, where the handle path alone is Denied
// (Go's os.Stat answers so too). Making such a token in-process needs
// advapi32 through Go's unsafe, which this tree does not import, so the test
// asks runas, which starts the probe on its own, and reads the output from a
// file.
func TestFs_StatStandardUser(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows only")
	}
	drive := os.Getenv("SystemDrive")
	if len(drive) != 2 {
		drive = "C:"
	}
	svi := drive + `\System Volume Information`
	if _, err := os.Lstat(svi); err != nil && os.IsNotExist(err) {
		t.Skipf("no %s on this machine", svi)
	}
	root := t.TempDir()
	if strings.ContainsAny(root, " \"%^&") {
		t.Skipf("the temporary directory %q needs quoting runas does not do", root)
	}
	exe := statRouteBuild(t, root)
	pipe := fmt.Sprintf(`\\.\pipe\mk-statroute-user-%d`, os.Getpid())
	slot := fmt.Sprintf(`\\.\mailslot\mk-statroute-user-%d`, os.Getpid())
	paths := []string{svi, `\\?\` + svi, drive + `\$Extend`, `\\?\` + drive + `\$Extend`, drive + `\Windows`, `\\?\` + drive + `\Windows`}
	var cmd strings.Builder
	fmt.Fprintf(&cmd, "@echo off\r\ncd /d %s\r\n\"%s\" %s %s-one %s 0", root, exe, pipe, filepath.Base(pipe), slot)
	for _, p := range paths {
		fmt.Fprintf(&cmd, " \"%s\"", p)
	}
	out := filepath.Join(root, "user.out")
	done := filepath.Join(root, "user.done")
	fmt.Fprintf(&cmd, " > \"%s\" 2>&1\r\necho %%ERRORLEVEL%% > \"%s\"\r\n", out, done)
	bat := filepath.Join(root, "user.cmd")
	if err := os.WriteFile(bat, []byte(cmd.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	comspec := filepath.Join(os.Getenv("SystemRoot"), "System32", "cmd.exe")
	c := exec.Command("runas.exe")
	c.SysProcAttr = &syscall.SysProcAttr{CmdLine: `runas.exe /trustlevel:0x20000 "` + comspec + ` /c ` + bat + `"`}
	if b, err := c.CombinedOutput(); err != nil {
		t.Skipf("runas /trustlevel:0x20000 refused: %v %s", err, b)
	}
	deadline := time.Now().Add(90 * time.Second)
	for {
		if b, err := os.ReadFile(done); err == nil && len(strings.TrimSpace(string(b))) > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the probe started by runas did not finish within 90 s (%s)", done)
		}
		time.Sleep(200 * time.Millisecond)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	head, _, rows := statRouteParse(t, string(b), 0, len(paths))
	if !strings.HasPrefix(head, "made ") {
		t.Fatalf("bad output:\n%s", b)
	}
	for i, w := range rows {
		t.Logf("standard user: %-40s nofollow=%d full [%s] handle [%s] step [%s] StatPath [%s]", paths[i/2], i%2, w.full, w.handle, w.fast, w.path)
	}
	// rows: path k at 2k (nofollow false) and 2k + 1 (true)
	if strings.HasPrefix(rows[0].handle, "0 ") {
		t.Skipf("the handle path opens %s under this token, so it is no standard user's: [%s]", svi, rows[0].handle)
	}
	for k := 0; k < len(paths); k += 2 {
		for nf := 0; nf < 2; nf++ {
			a, b := rows[2*k+nf], rows[2*(k+1)+nf]
			if a.full != b.full {
				t.Errorf("standard user, nofollow=%d: %s gives [%s], %s [%s]", nf, paths[k], a.full, paths[k+1], b.full)
			}
			if !strings.HasPrefix(a.full, "0 0 ") {
				t.Errorf("standard user, nofollow=%d: %s fails: [%s]", nf, paths[k], a.full)
			}
			if a.full != a.fast || b.full != b.fast {
				t.Errorf("standard user, nofollow=%d: %s and %s are not the attributes step's answers: [%s] [%s]", nf, paths[k], paths[k+1], a.fast, b.fast)
			}
		}
		if statRouteFirst5(rows[2*k].path) != statRouteFirst5(rows[2*k].full) || statRouteFirst5(rows[2*(k+1)].path) != statRouteFirst5(rows[2*(k+1)].full) {
			t.Errorf("standard user: StatPath of %s or %s differs from StatEx: [%s] [%s]", paths[k], paths[k+1], rows[2*k].path, rows[2*(k+1)].path)
		}
	}
}

// statRouteBuild builds the probe over a copy of this tree's std (or of
// MK_STATROUTE_STD, a scratch copy, to run the test against other sources), run
// from root (whose fx holds the fixtures); the copy's kernel32.def names
// CreateMailslotW, which the probe declares.
func statRouteBuild(t *testing.T, root string) string {
	t.Helper()
	src := os.Getenv("MK_STATROUTE_STD")
	if src == "" {
		src = "."
	}
	def, err := os.ReadFile(filepath.Join(src, "sys", "windows", "kernel32.def"))
	if err != nil {
		t.Fatal(err)
	}
	fixtures := map[string]string{
		"sys/windows/zz_statroute.mk": statRouteProbe,
		"sys/windows/kernel32.def":    strings.TrimRight(string(def), "\r\n") + "\nCreateMailslotW\n",
		"statprobe/stat.mk":           statRoutePrint,
	}
	var stdDir string
	if src == "." {
		stdDir = copyStd(t, root, fixtures)
	} else {
		stdDir = filepath.Join(root, "std")
		if err := os.CopyFS(stdDir, os.DirFS(src)); err != nil {
			t.Fatal(err)
		}
		for p, text := range fixtures {
			dst := filepath.Join(stdDir, filepath.FromSlash(p))
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(dst, []byte(text), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := os.WriteFile(filepath.Join(root, "main.mk"), []byte(statRouteMain), 0o644); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(root, "statroute.exe")
	out, status := mkCmd(t, root, stdDir, "build", "-o", exe, "main.mk")
	if status != 0 {
		if strings.Contains(out, "no usable LLVM") {
			t.Skipf("needs the LLVM toolchain: %s", out)
		}
		t.Fatalf("mk build: exit %d\n%s", status, out)
	}
	return exe
}
