// Why: std.sys's threads, parking and pages had defects at the limits of
// their argument types; each needs the case that failed before its fix.
// What: TestRtIo_ArgEdgesWindows and TestRtIo_ArgEdgesLinux over probe6.mk's cases
// SemLong, StackArg, ReserveLimits and KindRefused.
// How: builds std/rtioprobe with probe6.mk and its own Main and wants every
// case 0; test point 8 shrinks SemWait's 49.7-day millisecond cap to 2 ms.
//
// The Linux test starts wsl.exe: run it only under the WSL lock,
// `pwsh -NoProfile -File tools/wsl-lock.ps1 go test -p 1 ./std`; parallel
// WSL clients hang WSL.

package std

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// rtio53Main runs the probe6.mk cases once each.
const rtio53Main = `import std.rtioprobe

export Main(sys: &System): WriteError!void {
    var out := sys.Stdout()
    b := rtioprobe.Backend()
    try Print(mut out, "backend {b}\n", (b))
    sl := rtioprobe.SemLong()
    try Print(mut out, "semlong {sl}\n", (sl))
    sa := rtioprobe.StackArg()
    try Print(mut out, "stackarg {sa}\n", (sa))
    rl := rtioprobe.ReserveLimits()
    try Print(mut out, "reservelimits {rl}\n", (rl))
    kr := rtioprobe.KindRefused()
    try Print(mut out, "kindrefused {kr}\n", (kr))
    try out.Flush()
}
`

// rtio53Cases are the lines whose value must be 0.
var rtio53Cases = []string{"semlong", "stackarg", "reservelimits", "kindrefused"}

// buildRTIO53 builds the probe6.mk probe for target (empty: the host), over
// the tree's std or over MK_RTIO_STD (for mutants), and returns the
// executable's path.
func buildRTIO53(t *testing.T, target string) string {
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
		"rtioprobe/probe.mk":  rtioProbe,
		"rtioprobe/probe2.mk": rtioProbe2,
		"rtioprobe/probe3.mk": rtioProbe3,
		"rtioprobe/probe4.mk": rtioProbe4,
		"rtioprobe/probe5.mk": rtioProbe5,
		"rtioprobe/probe6.mk": rtioProbe6,
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
	if err := os.WriteFile(filepath.Join(root, "main.mk"), []byte(rtio53Main), 0o644); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(root, "probe53")
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

// rtio53Judge checks one run: the backend and every case 0; it returns the
// lines by name.
func rtio53Judge(t *testing.T, out, backend string) map[string]string {
	t.Helper()
	got := map[string]string{}
	for _, l := range strings.Split(strings.TrimSpace(strings.ReplaceAll(out, "\r\n", "\n")), "\n") {
		if k, v, ok := strings.Cut(l, " "); ok {
			got[k] = v
		}
	}
	if got["backend"] != backend {
		t.Errorf("backend %q, want %s\n%s", got["backend"], backend, out)
	}
	for _, c := range rtio53Cases {
		if got[c] != "0" {
			if !rtioJudged(c) {
				t.Logf("%s: %q (%s: the case has a wall-clock step, so it is not judged)", c, got[c], timingOff)
				continue
			}
			t.Errorf("%s: %q, want 0 (else the number of the failed step)\n%s", c, got[c], out)
		}
	}
	return got
}

// TestRtIo_ArgEdgesWindows: the probe6.mk cases on IOCP.
func TestRtIo_ArgEdgesWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows only")
	}
	t.Parallel()
	exe := buildRTIO53(t, "")
	r := runNative(t, exe, "")
	if r.code != 0 {
		t.Fatalf("exit %d\nstdout:\n%s\nstderr:\n%s", r.code, r.stdout, r.stderr)
	}
	rtio53Judge(t, r.stdout, "1")
}

// TestRtIo_ArgEdgesLinux: the probe6.mk cases on x86_64-linux with io_uring and
// with MK_IO=epoll; both backends agree on every case.
func TestRtIo_ArgEdgesLinux(t *testing.T) {
	needWSL(t)
	exe := buildRTIO53(t, "x86_64-linux")
	var outs [2]map[string]string
	for i, b := range []struct{ name, env, backend string }{{"io_uring", "MK_IO=auto", "2"}, {"epoll", "MK_IO=epoll", "3"}} {
		stdout, stderr, code := runWSL(t, exe, []string{b.env})
		if code != 0 {
			t.Errorf("%s: exit %d\nstdout:\n%s\nstderr:\n%s", b.name, code, stdout, stderr)
			continue
		}
		outs[i] = rtio53Judge(t, stdout, b.backend)
	}
	if outs[0] != nil && outs[1] != nil {
		for _, c := range rtio53Cases {
			if outs[0][c] != outs[1][c] && rtioJudged(c) {
				t.Errorf("%s: io_uring %q, epoll %q; the backends must agree", c, outs[0][c], outs[1][c])
			}
		}
	}
}
