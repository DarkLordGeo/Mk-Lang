// Why: task stack guards, stack bounds, Raise and the cancel check after a
// resubmission must be tested on a real kernel, not only argued.
// What: TestRtIo_StackGuardWindows and TestRtIo_StackGuardLinux over probe4.mk's cases
// GuardCheck, StackBounds, Raise and ResubCancel.
// How: builds std/rtioprobe with probe4.mk and its own Main; on Linux the
// guard is madvise(MADV_GUARD_INSTALL) on 6.13 and later, PROT_NONE before.
//
// The Linux test starts wsl.exe: run it only under the WSL lock,
// `pwsh -NoProfile -File tools/wsl-lock.ps1 go test -p 1 ./std`; parallel
// WSL clients hang WSL.

package std

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// rtio5Main runs the probe4.mk cases once each.
const rtio5Main = `import std.rtioprobe

export Main(sys: &System): WriteError!void {
    var out := sys.Stdout()
    b := rtioprobe.Backend()
    try Print(mut out, "backend {b}\n", (b))
    gk := rtioprobe.GuardKind()
    try Print(mut out, "guard {gk}\n", (gk))
    gc := rtioprobe.GuardCheck()
    try Print(mut out, "guardcheck {gc}\n", (gc))
    gm := rtioprobe.GuardMaps()
    try Print(mut out, "guardmaps {gm}\n", (gm))
    sb := rtioprobe.StackBounds()
    try Print(mut out, "stackbounds {sb}\n", (sb))
    rs := rtioprobe.RaiseCase()
    try Print(mut out, "raise {rs}\n", (rs))
    var rc: u32 = 0
    for _ in 0..20 {
        x := rtioprobe.ResubCancel()
        if x != 0 && rc == 0 {
            rc = x
        }
    }
    try Print(mut out, "resubcancel {rc}\n", (rc))
    try out.Flush()
}
`

// rtio5Cases are the lines whose value must be 0.
var rtio5Cases = []string{"guardcheck", "stackbounds", "raise", "resubcancel"}

// buildRTIO5 builds the probe4.mk probe for target (empty: the host), over the
// tree's std or over MK_RTIO_STD (a std of the caller's choosing, for
// mutants), and returns the executable's path.
func buildRTIO5(t *testing.T, target string) string {
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
	if err := os.WriteFile(filepath.Join(root, "main.mk"), []byte(rtio5Main), 0o644); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(root, "probe5")
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

// rtio5Judge checks one run: the backend, every case 0, and GuardStack's
// answer against wantGuard (1 a light guard, 0 refused); it returns the
// lines by name.
func rtio5Judge(t *testing.T, out, backend, wantGuard string) map[string]string {
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
	for _, c := range rtio5Cases {
		if got[c] != "0" {
			t.Errorf("%s: %q, want 0 (else the number of the failed step)\n%s", c, got[c], out)
		}
	}
	if got["guard"] != wantGuard {
		t.Errorf("GuardStack installed %q, want %s\n%s", got["guard"], wantGuard, out)
	}
	if got["guard"] == "1" {
		// Light guards cost no VMA: eight prepared slots are one mapping.
		if n, err := strconv.Atoi(got["guardmaps"]); err != nil || n > 1 {
			t.Errorf("guardmaps %q: eight slots with light guards added more than one mapping", got["guardmaps"])
		}
	}
	t.Logf("backend %s: GuardStack %s; eight 16 KiB slots prepared as region.mk does changed the mapping count by %s (-1: not counted on this OS)", backend, got["guard"], got["guardmaps"])
	return got
}

// TestRtIo_StackGuardWindows: GuardStack is false, ThreadStackBounds of a started
// thread holds its stack, Raise runs the console handler, and the IOCP
// accept reissue cancels a cancel that ran in its window.
func TestRtIo_StackGuardWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("the IOCP backend runs on Windows")
	}
	t.Parallel()
	exe := buildRTIO5(t, "")
	r := runNative(t, exe, "")
	if r.code != 0 {
		t.Fatalf("exit %d\nstdout:\n%s\nstderr:\n%s", r.code, r.stdout, r.stderr)
	}
	rtio5Judge(t, r.stdout, "1", "0")
}

// wslKernel is the WSL kernel's release, major and minor (`uname -r`).
func wslKernel(t *testing.T) (string, int, int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	wslGuard()
	cmd := exec.CommandContext(ctx, "wsl.exe", "-e", "uname", "-r")
	cmd.WaitDelay = 5 * time.Second
	b, err := cmd.Output()
	if err != nil {
		t.Fatalf("wsl.exe -e uname -r: %v", err)
	}
	rel := strings.TrimSpace(string(b))
	parts := strings.SplitN(rel, ".", 3)
	if len(parts) < 2 {
		t.Fatalf("uname -r: %q", rel)
	}
	maj, _ := strconv.Atoi(parts[0])
	minor := 0
	for _, c := range parts[1] {
		if c < '0' || c > '9' {
			break
		}
		minor = minor*10 + int(c-'0')
	}
	return rel, maj, minor
}

// TestRtIo_StackGuardLinux: the same cases on x86_64-linux with io_uring and with
// MK_IO=epoll. GuardStack must install a light guard on Linux 6.13 and
// later and be refused before; ThreadStackBounds must name the stack
// ThreadStart mapped, over more threads than the table holds at once;
// Raise must deliver through kill; the io_uring Resubmit must cancel a
// cancel that named the old submission. Both backends agree on every case.
func TestRtIo_StackGuardLinux(t *testing.T) {
	needWSL(t)
	rel, maj, minor := wslKernel(t)
	want := "0"
	if maj > 6 || maj == 6 && minor >= 13 {
		want = "1"
	}
	t.Logf("WSL kernel %s: GuardStack expected %s", rel, want)
	exe := buildRTIO5(t, "x86_64-linux")
	var outs [2]map[string]string
	for i, b := range []struct{ name, env, backend string }{{"io_uring", "MK_IO=auto", "2"}, {"epoll", "MK_IO=epoll", "3"}} {
		stdout, stderr, code := runWSL(t, exe, []string{b.env})
		if code != 0 {
			t.Errorf("%s: exit %d\nstdout:\n%s\nstderr:\n%s", b.name, code, stdout, stderr)
			continue
		}
		outs[i] = rtio5Judge(t, stdout, b.backend, want)
	}
	if outs[0] != nil && outs[1] != nil {
		for _, c := range append([]string{"guard"}, rtio5Cases...) {
			if outs[0][c] != outs[1][c] {
				t.Errorf("%s: io_uring %q, epoll %q; the backends must agree", c, outs[0][c], outs[1][c])
			}
		}
	}
}
