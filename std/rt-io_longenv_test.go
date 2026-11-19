// Why: GetEnvRaw and ExePath failed on long values (Windows read 1,024
// units or more as unset or as a failure) and on names holding NUL or =.
// What: rtioProbe9 (probe9.mk), TestRtIo_LinuxEnv and TestRtIo_WindowsEnv.
// How: runs the probe with long values and adversarial names in its
// environment and from a path past 1,024 units, and checks lengths and sums.
// A name that is empty or holds a NUL or an = is never set, on both targets.

package std

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// rtioProbe9 is std/rtioprobe/probe9.mk of the copy.
const rtioProbe9 = `// A probe of std.sys, ninth file (std/rt-io_longenv_test.go).

import std.raw
import std.rt
import std.sys

// Sum is the sum of p[i] * (i % 251 + 1) over the first n bytes of p (the
// Go side computes the same over the bytes it expects).
Sum(p: *u8, n: int): int {
    var s := 0
    unsafe "p holds at least n bytes" {
        for i in 0..n {
            s += int(raw.Read(raw.Offset(p, i))) * (i % 251 + 1)
        }
    }
    return s
}

// EnvSum reads name with sys.GetEnvRaw into 256 KiB mapped here: the
// length it answers (-1 unset) and the Sum of the bytes it wrote; -9 when
// the buffer cannot be mapped.
export EnvSum(name: str): (n: int, s: int) {
    size := 262144
    p := sys.Reserve(size, 4096)
    if raw.IsNull(p) || !sys.Commit(p, size) {
        return (n: -9, s: 0)
    }
    var n := 0
    unsafe "p holds size bytes; init_sp is Start's sp, set before Main" {
        n = sys.GetEnvRaw((*rt.R()).init_sp, name, raw.SliceMut(p, size))
    }
    var s := 0
    if n > 0 && n <= size {
        s = Sum(p, n)
    }
    sys.Release(p, size)
    return (n: n, s: s)
}

// EnvShort reads name into a 4-byte buffer: the length GetEnvRaw answers
// (the whole value's, as far as it fits) and the Sum of the 4 bytes.
export EnvShort(name: str): (n: int, s: int) {
    var b: [4]u8 = [0; 4]
    var n := 0
    unsafe "init_sp is Start's sp" {
        n = sys.GetEnvRaw((*rt.R()).init_sp, name, mut b[..])
    }
    unsafe "b is 4 bytes" {
        return (n: n, s: Sum(raw.Data(b[..]), 4))
    }
}

// ExeSum is sys.ExePath into 256 KiB mapped here: its length (0: failure)
// and the Sum of its bytes.
export ExeSum(): (n: int, s: int) {
    size := 262144
    p := sys.Reserve(size, 4096)
    if raw.IsNull(p) || !sys.Commit(p, size) {
        return (n: -9, s: 0)
    }
    var n := 0
    unsafe "p holds size bytes" {
        n = sys.ExePath(raw.SliceMut(p, size))
    }
    s := Sum(p, n)
    sys.Release(p, size)
    return (n: n, s: s)
}

// ExeShort is sys.ExePath into 8 bytes: 0 when the path is longer.
export ExeShort(): int {
    var b: [8]u8 = [0; 8]
    return sys.ExePath(mut b[..])
}
`

// rtio56Main prints one line per name: "env<i> N S" for MK_RTIO56_<i>
// (i 0 to 7) through EnvSum, then the adversarial names: "eq" for
// "MK_RTIO56A=B", "nul" for "MK_RTIO56A" with a NUL after it, "nulmid"
// for "MK_RTIO56A", a NUL, "=" and "B", "empty" for "", "plain" for
// "MK_RTIO56A", "short" for MK_RTIO56_3 into 4 bytes, and "exe N S" and
// "exeshort N" for ExePath.
const rtio56Main = `import std.rtioprobe

export Main(sys: &System): WriteError!void {
    var out := sys.Stdout()
    names := ["MK_RTIO56_0", "MK_RTIO56_1", "MK_RTIO56_2", "MK_RTIO56_3", "MK_RTIO56_4", "MK_RTIO56_5", "MK_RTIO56_6", "MK_RTIO56_7"]
    for i in 0..8 {
        r := rtioprobe.EnvSum(names[i])
        try Print(mut out, "env{i} {n} {s}\n", (i: i, n: r.n, s: r.s))
    }
    keys := ["eq", "nul", "nulmid", "empty", "plain"]
    odd := ["MK_RTIO56A=B", "MK_RTIO56A\u{0}", "MK_RTIO56A\u{0}=B", "", "MK_RTIO56A"]
    for i in 0..5 {
        r := rtioprobe.EnvSum(odd[i])
        try Print(mut out, "{k} {n} {s}\n", (k: keys[i], n: r.n, s: r.s))
    }
    m := rtioprobe.EnvShort("MK_RTIO56_3")
    try Print(mut out, "short {n} {s}\n", (n: m.n, s: m.s))
    x := rtioprobe.ExeSum()
    try Print(mut out, "exe {n} {s}\n", (n: x.n, s: x.s))
    z := rtioprobe.ExeShort()
    try Print(mut out, "exeshort {z}\n", (z))
    try out.Flush()
}
`

// rtio56Sum is the probe's Sum over b.
func rtio56Sum(b []byte) int {
	s := 0
	for i, c := range b {
		s += int(c) * (i%251 + 1)
	}
	return s
}

// rtio56Values are the values of MK_RTIO56_0 to _7: lengths around the old
// 1,024-unit buffer (1,023 units fit it with the NUL, 1,024 do not), non-ASCII
// text whose UTF-16 and UTF-8 lengths differ (a surrogate pair among
// them), and the longest value Windows keeps (32,766 units with the NUL
// inside its 32,767).
func rtio56Values() []string {
	return []string{
		"x",
		strings.Repeat("a", 1022),
		strings.Repeat("b", 1023),
		strings.Repeat("c", 1024),
		strings.Repeat("é", 700) + strings.Repeat("\U0001F600", 300),
		strings.Repeat("0123456789", 500),
		strings.Repeat("z", 32766),
		"",
	}
}

// rtio56Env is the environment of a probe run, as NAME=VALUE entries.
func rtio56Env() []string {
	var env []string
	for i, v := range rtio56Values() {
		env = append(env, fmt.Sprintf("MK_RTIO56_%d=%s", i, v))
	}
	return append(env, "MK_RTIO56A=B=C")
}

// rtio56Check compares a probe run's output with what GetEnvRaw and
// ExePath must answer; exe is the path the probe ran from as the OS names
// it (empty: not checked).
func rtio56Check(t *testing.T, label, out, exe string) {
	t.Helper()
	got := rtio55Lines(out)
	for i, v := range rtio56Values() {
		want := fmt.Sprintf("%d %d", len(v), rtio56Sum([]byte(v)))
		if g := got[fmt.Sprintf("env%d", i)]; g != want {
			t.Errorf("%s: MK_RTIO56_%d (%d bytes): got %q, want %q", label, i, len(v), g, want)
		}
	}
	for _, k := range []string{"eq", "nul", "nulmid", "empty"} {
		if got[k] != "-1 0" {
			t.Errorf("%s: %s: got %q, want -1 0 (never set)", label, k, got[k])
		}
	}
	if want := fmt.Sprintf("3 %d", rtio56Sum([]byte("B=C"))); got["plain"] != want {
		t.Errorf("%s: plain: got %q, want %q", label, got["plain"], want)
	}
	v3 := rtio56Values()[3]
	if want := fmt.Sprintf("%d %d", len(v3), rtio56Sum([]byte(v3[:4]))); got["short"] != want {
		t.Errorf("%s: short: got %q, want %q", label, got["short"], want)
	}
	if got["exeshort"] != "0" {
		t.Errorf("%s: exeshort: got %q, want 0 (the path does not fit 8 bytes)", label, got["exeshort"])
	}
	if exe != "" {
		if want := fmt.Sprintf("%d %d", len(exe), rtio56Sum([]byte(exe))); got["exe"] != want {
			t.Errorf("%s: exe: got %q, want %q (%d bytes)", label, got["exe"], want, len(exe))
		}
	} else if f := strings.Fields(got["exe"]); len(f) != 2 || f[0] == "0" {
		t.Errorf("%s: exe: got %q, want a length and a sum", label, got["exe"])
	}
}

// buildRTIO56 builds the probe9.mk probe for target (empty: the host).
func buildRTIO56(t *testing.T, target string) string {
	t.Helper()
	src := os.Getenv("MK_RTIO_STD")
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
		"pkg.mk":              text[:at+end] + `, "rtioprobe"` + text[at+end:],
		"rtioprobe/probe9.mk": rtioProbe9,
	}
	root := t.TempDir()
	var stdDir string
	if src != "" {
		stdDir = filepath.Join(root, "std")
		if err := os.CopyFS(stdDir, os.DirFS(src)); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(stdDir, "rtioprobe"), 0o755); err != nil {
			t.Fatal(err)
		}
		for p, text := range fixtures {
			if err := os.WriteFile(filepath.Join(stdDir, filepath.FromSlash(p)), []byte(text), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	} else {
		if err := os.MkdirAll(filepath.Join(root, "std", "rtioprobe"), 0o755); err != nil {
			t.Fatal(err)
		}
		stdDir = copyStd(t, root, fixtures)
	}
	if err := os.WriteFile(filepath.Join(root, "main.mk"), []byte(rtio56Main), 0o644); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(root, "probe56")
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

// TestRtIo_LinuxEnv: the same answers on Linux, on io_uring and epoll
// (the backend does not touch GetEnvRaw; both run so the two agree, as
// every rt-io Linux test does). The probe's path is $PWD/probe56 in WSL.
func TestRtIo_LinuxEnv(t *testing.T) {
	if os.Getenv("MK_RTIO_BUILD_ONLY") != "" {
		t.Logf("built %s", buildRTIO56(t, "x86_64-linux"))
		return
	}
	needWSL(t)
	exe := buildRTIO56(t, "x86_64-linux")
	// The values go through a file the script sources, not the command
	// line: 32 KiB values are past what one wsl.exe argument carries well.
	var sb strings.Builder
	for _, e := range rtio56Env() {
		k, v, _ := strings.Cut(e, "=")
		sb.WriteString("export " + k + "='" + v + "'\n")
	}
	envFile := filepath.Join(filepath.Dir(exe), "rtio56.env")
	if err := os.WriteFile(envFile, []byte(sb.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := wslPath(filepath.Dir(exe))
	for _, b := range []string{"auto", "epoll"} {
		out, stderr, code := wslSh(t, "cd '"+dir+"' || exit 9; . ./rtio56.env; MK_IO="+b+" ./probe56; echo \"status $?\"; echo \"path $(readlink -f ./probe56)\"")
		got := rtio55Lines(out)
		if code != 0 || got["status"] != "0" {
			t.Fatalf("MK_IO=%s: status %q\nstdout:\n%s\nstderr:\n%s", b, got["status"], out, stderr)
		}
		rtio56Check(t, "MK_IO="+b, out, got["path"])
		t.Logf("MK_IO=%s: exe %s", b, got["exe"])
	}
}

// TestRtIo_WindowsEnv: the answers on Windows, from the probe's own
// directory and again from a directory whose path is past 1,024 units
// (LongPathsEnabled must be 1; where the OS refuses to
// start a program there, that half is skipped with the reason).
func TestRtIo_WindowsEnv(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows only")
	}
	exe := buildRTIO56(t, "")
	run := func(label, path string) {
		cmd := exec.Command(path)
		cmd.Dir = filepath.Dir(exe)
		cmd.Env = append(os.Environ(), rtio56Env()...)
		var o, e bytes.Buffer
		cmd.Stdout, cmd.Stderr = &o, &e
		if err := cmd.Run(); err != nil {
			t.Fatalf("%s: %v\nstdout:\n%s\nstderr:\n%s", label, err, o.String(), e.String())
		}
		got := rtio55Lines(o.String())
		want := path
		if f := strings.Fields(got["exe"]); len(f) == 2 && !strings.HasPrefix(path, `\\?\`) {
			// GetModuleFileNameW names the image as CreateProcess was given
			// it; a long path may come back in its \\?\ form.
			if n, _ := strconv.Atoi(f[0]); n == len(path)+4 {
				want = `\\?\` + path
			}
		}
		rtio56Check(t, label, o.String(), want)
		t.Logf("%s: exe %s (path %d bytes)", label, got["exe"], len(path))
	}
	run("short path", exe)
	deep := filepath.Dir(exe)
	for len(deep) < 1100 {
		deep = filepath.Join(deep, strings.Repeat("d", 120))
	}
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Skipf("long directory: %v", err)
	}
	long := filepath.Join(deep, "probe56.exe")
	b, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(long, b, 0o755); err != nil {
		t.Skipf("long copy: %v", err)
	}
	if err := exec.Command(long).Run(); err != nil {
		if _, ok := err.(*exec.ExitError); !ok {
			t.Skipf("the OS does not start a program at a %d-byte path: %v", len(long), err)
		}
	}
	run("long path", long)
}
