// Why: a non-blocking PollWait that takes a PollWake must leave it for the
// next blocking wait; on IOCP it dropped the wake, and the scheduler hung.
// What: TestRtIo_PollWakeWindows and TestRtIo_PollWakeLinux over probe7.mk's cases
// WakeKeep, WakeLate, PushAll and RenameDir.
// How: builds std/rtioprobe with probe7.mk and its own Main and wants every
// case 0.
//
// The Linux test starts wsl.exe: run it only under the WSL lock,
// `pwsh -NoProfile -File tools/wsl-lock.ps1 go test -p 1 ./std`; parallel
// WSL clients hang WSL.

package std

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// rtio54Main runs the probe7.mk cases once each.
const rtio54Main = `import std.rtioprobe

export Main(sys: &System): WriteError!void {
    var out := sys.Stdout()
    b := rtioprobe.Backend()
    try Print(mut out, "backend {b}\n", (b))
    wk := rtioprobe.WakeKeep()
    try Print(mut out, "wakekeep {wk}\n", (wk))
    wl := rtioprobe.WakeLate(20)
    try Print(mut out, "wakelate {wl}\n", (wl))
    pa := rtioprobe.PushAll()
    try Print(mut out, "pushall {pa}\n", (pa))
    rd := rtioprobe.RenameDir()
    try Print(mut out, "renamedir {rd}\n", (rd))
    o0 := rtioprobe.RenameDirOs(0)
    o1 := rtioprobe.RenameDirOs(1)
    o2 := rtioprobe.RenameDirOs(2)
    o3 := rtioprobe.RenameDirOs(3)
    try Print(mut out, "renameos {o0},{o1},{o2},{o3}\n", (o0, o1, o2, o3))
    n := rtioprobe.Cpus()
    try Print(mut out, "cpus {n}\n", (n))
    try out.Flush()
}
`

// rtio54Cases are the lines whose value must be 0.
var rtio54Cases = []string{"wakekeep", "wakelate", "pushall", "renamedir"}

// buildRTIO54 builds the probe7.mk probe for target (empty: the host), over
// the tree's std or over MK_RTIO_STD (for mutants), and returns the
// executable's path.
func buildRTIO54(t *testing.T, target string) string {
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
		"rtioprobe/probe7.mk": rtioProbe7,
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
	if err := os.WriteFile(filepath.Join(root, "main.mk"), []byte(rtio54Main), 0o644); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(root, "probe54")
	rtio54Fixtures(t, root)
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

// rtio54Fixtures makes RenameDir's fixtures afresh in dir, the directory
// the probe runs in: every run renames them, so each run needs its own
// (the epoll run after the io_uring one found them moved).
func rtio54Fixtures(t *testing.T, dir string) {
	t.Helper()
	for _, n := range []string{"rtio_rfile", "rtio_rdir", "rtio_rdir2", "rtio_rfull"} {
		if err := os.RemoveAll(filepath.Join(dir, n)); err != nil {
			t.Fatal(err)
		}
	}
	for _, d := range []string{"rtio_rdir", "rtio_rdir2", "rtio_rfull"} {
		if err := os.Mkdir(filepath.Join(dir, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range []string{"rtio_rfile", filepath.Join("rtio_rfull", "x")} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte{1}, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// rtio54Judge checks one run: the backend and every case 0; it returns the
// lines by name.
func rtio54Judge(t *testing.T, out, backend string) map[string]string {
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
	for _, c := range rtio54Cases {
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

// TestRtIo_PollWakeWindows: the probe7.mk cases on IOCP.
func TestRtIo_PollWakeWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows only")
	}
	t.Parallel()
	exe := buildRTIO54(t, "")
	r := runNative(t, exe, "")
	if r.code != 0 {
		t.Fatalf("exit %d\nstdout:\n%s\nstderr:\n%s", r.code, r.stdout, r.stderr)
	}
	got := rtio54Judge(t, r.stdout, "1")
	t.Logf("renamedir %s (os %s)", got["renamedir"], got["renameos"])
	// CpuCount against Go's answer (both read the process's affinity).
	if want := strconv.Itoa(runtime.NumCPU()); got["cpus"] != want {
		t.Errorf("CpuCount %s, runtime.NumCPU %s", got["cpus"], want)
	}
}

// TestRtIo_PollWakeLinux: the probe7.mk cases on x86_64-linux with io_uring and
// with MK_IO=epoll; both backends agree on every case.
func TestRtIo_PollWakeLinux(t *testing.T) {
	// MK_RTIO_BUILD_ONLY: build the Linux probe without starting wsl.exe
	// (a type error in the probe then costs no lock hold).
	if os.Getenv("MK_RTIO_BUILD_ONLY") != "" {
		t.Logf("built %s", buildRTIO54(t, "x86_64-linux"))
		return
	}
	needWSL(t)
	exe := buildRTIO54(t, "x86_64-linux")
	var outs [2]map[string]string
	for i, b := range []struct{ name, env, backend string }{{"io_uring", "MK_IO=auto", "2"}, {"epoll", "MK_IO=epoll", "3"}} {
		rtio54Fixtures(t, filepath.Dir(exe))
		stdout, stderr, code := runWSL(t, exe, []string{b.env})
		if code != 0 {
			t.Errorf("%s: exit %d\nstdout:\n%s\nstderr:\n%s", b.name, code, stdout, stderr)
			continue
		}
		outs[i] = rtio54Judge(t, stdout, b.backend)
	}
	// CpuCount against nproc (sched_getaffinity too) in the same WSL.
	wslGuard()
	np, err := exec.Command("wsl.exe", "-e", "nproc").Output()
	if err != nil {
		t.Errorf("wsl.exe -e nproc: %v", err)
	} else {
		for i := range outs {
			if outs[i] != nil && outs[i]["cpus"] != strings.TrimSpace(string(np)) {
				t.Errorf("backend %d: CpuCount %s, nproc %s", i, outs[i]["cpus"], strings.TrimSpace(string(np)))
			}
		}
	}
	if outs[0] != nil && outs[1] != nil {
		for _, c := range rtio54Cases {
			if outs[0][c] != outs[1][c] && rtioJudged(c) {
				t.Errorf("%s: io_uring %q, epoll %q; the backends must agree", c, outs[0][c], outs[1][c])
			}
		}
	}
}
