// Why: fs.Map may be called only inside unsafe (E1101), so its tests cannot
// be ordinary std test blocks.
// What: TestFs_MapWindows and TestFs_MapLinux.
// How: the tests are a package of their own whose manifest lists the module
// in unsafe_modules; a read past a truncation must be the SIGBUS panic report.

package std

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

const fs2MapManifest = `const Package = pkg.Package(
    name: "fsmap",
    bins: [pkg.Bin(name: "fsmap", root: "src/main", link_script: "", cpu: "", subsystem: "console")],
    deps: [],
    c_sources: [],
    c_imports: [],
    asm_sources: [],
    targets: [],
    unsafe_modules: ["src/main"],
    links: [],
)
`

// fs2MapProg maps args[1] (a file of 100,000 bytes, i % 251 at i) at
// unaligned offsets and lengths, compares every byte, checks len 0 and
// the refusals, then, with args[2] "trunc", truncates the mapped file to
// 0 and reads past the cut (Linux: the panic report; Windows: the
// truncation is refused, and the bytes still read).
const fs2MapProg = `import std.fs

MapIt(io: Io, f: &fs.File, off: i64, n: int): fs.Error!fs.Mapping {
    unsafe "nothing but this test changes its file; the fault test cuts it on purpose" {
        return try fs.Map(io, f, off, n)
    }
}

Check(sys: &System, f: &fs.File, off: i64, n: int): fs.Error!bool {
    var m := try MapIt(sys.io, f, off, n)
    b := m.Bytes()
    var ok := b.Len() == n
    for i in 0..b.Len() {
        if b[i] != Trunc[u8]((off + i64(i)) % 251) {
            ok = false
        }
    }
    m.Unmap()
    return ok
}

Refused(sys: &System, f: &fs.File, off: i64, n: int): str {
    m := MapIt(sys.io, f, off, n) else e {
        return sys.arena.Fmt("{e}", (e)).Must()
    }
    m.Unmap()
    return "mapped"
}

export Main(sys: &System): fs.Error|WriteError!void {
    p := sys.args[1]
    var f := try fs.OpenWith(sys.io, p, fs.ReadWrite)
    defer f.Abandon()
    for off in [i64(0), 1, 4095, 4096, 65535, 65536, 70001] {
        for n in [1, 7, 4096, 20000] {
            if !try Check(sys, &f, off, n) {
                try sys.Print("bad {off} {n}\n", (off, n))
            }
        }
    }
    try sys.Print("tail {ok}\n", (ok: try Check(sys, &f, 99_000, 1000)))
    try sys.Print("zero {ok}\n", (ok: try Check(sys, &f, 5, 0)))
    try sys.Print("past {r}\n", (r: Refused(sys, &f, 99_000, 1001)))
    try sys.Print("neg {r}\n", (r: Refused(sys, &f, -1, 10)))
    if sys.args.Len() > 2 {
        var m := try MapIt(sys.io, &f, 0, 100_000)
        defer m.Unmap()
        b := m.Bytes()
        f.Truncate(sys.io, 0) else e {
            try sys.Print("truncate {e} {x}\n", (e, x: b[90_000]))
            return
        }
        try sys.Print("truncated\n")
        try sys.Print("read {x}\n", (x: b[90_000]))
    }
    try sys.Print("done\n")
}
`

// fs2MapBuild writes the package and builds it against fs2Std.
func fs2MapBuild(t *testing.T, flags ...string) string {
	t.Helper()
	root := t.TempDir()
	stdDir := fs2Std(t, root)
	if err := os.MkdirAll(filepath.Join(root, "src", "main"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, text := range map[string]string{"pkg.mk": fs2MapManifest, "src/main/main.mk": fs2MapProg} {
		if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(name)), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	exe := filepath.Join(root, "fsmap")
	if runtime.GOOS == "windows" && !strings.Contains(strings.Join(flags, " "), "x86_64-linux") {
		exe += ".exe"
	}
	t.Setenv("MK_CACHE", filepath.Join(root, "cache"))
	out, status := mkCmd(t, root, stdDir, append(append([]string{"build"}, flags...), "-o", exe)...)
	if status != 0 {
		if strings.Contains(out, "no usable LLVM") {
			t.Skipf("needs the LLVM toolchain: %s", out)
		}
		t.Fatalf("mk build: exit %d\n%s", status, out)
	}
	return exe
}

func fs2MapFile(t *testing.T, dir string) string {
	t.Helper()
	b := make([]byte, 100_000)
	for i := range b {
		b[i] = byte(i % 251)
	}
	p := filepath.Join(dir, "mapped")
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestFs_MapWindows: every mapped byte equals the file's, len 0 maps
// nothing, a range past the end and a negative offset are refused, and
// truncating a mapped file is refused while the bytes still read.
func TestFs_MapWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("the Windows leg")
	}
	for _, flags := range [][]string{nil, {"--release"}} {
		exe := fs2MapBuild(t, flags...)
		p := fs2MapFile(t, t.TempDir())
		out, err := exec.Command(exe, p, "trunc").CombinedOutput()
		if err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		got := strings.ReplaceAll(string(out), "\r\n", "\n")
		want := "tail true\nzero true\npast Os(code: 0)\nneg Os(code: 0)\ntruncate Os(code: 1224) 142\n"
		if got != want {
			t.Errorf("flags %v:\ngot  %q\nwant %q", flags, got, want)
		}
	}
}

// TestFs_MapLinux: the same on x86_64-linux, dev and release, the file in
// the distribution's /tmp: the mapped bytes are the file's, and a
// read past a truncation under the mapping is the runtime's panic report
// (SIGBUS), not a silent value. MK_LINUX=1, under the WSL lock.
func TestFs_MapLinux(t *testing.T) {
	linuxGate(t)
	dir, err := packageTempDir("mkfs2map")
	if err != nil {
		t.Fatal(err)
	}
	src := fs2MapFile(t, dir)
	var sh strings.Builder
	sh.WriteString("#!/bin/sh\n")
	for _, release := range []bool{false, true} {
		flags := []string{"--target", "x86_64-linux"}
		if release {
			flags = append(flags, "--release")
		}
		exe := fs2MapBuild(t, flags...)
		keep := filepath.Join(dir, "fsmap-"+map[bool]string{false: "dev", true: "release"}[release])
		b, err := os.ReadFile(exe)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(keep, b, 0o755); err != nil {
			t.Fatal(err)
		}
		cmd := "cp " + rtsWSLPath(src) + " \"$MK_LINUX_TMP/m\" && " + rtsWSLPath(keep) + " \"$MK_LINUX_TMP/m\" trunc 2>&1; echo \"exit $?\""
		sh.WriteString("echo LEG\n" + linuxScratchSh(cmd))
	}
	out := linuxRunScript(t, dir, "fs2map.sh", sh.String(), 3*time.Minute)
	legs := strings.Split(out, "LEG\n")[1:]
	if len(legs) != 2 {
		t.Fatalf("%d legs, want 2:\n%s", len(legs), out)
	}
	for i, leg := range legs {
		if !strings.HasPrefix(leg, "tail true\nzero true\npast Os(code: 0)\nneg Os(code: 0)\ntruncated\n") ||
			strings.Contains(leg, "read ") || strings.Contains(leg, "exit 0") || !strings.Contains(leg, "panic") {
			t.Errorf("leg %d: want the mapped bytes, then the panic report at the read past the cut:\n%s", i, leg)
		}
	}
}
