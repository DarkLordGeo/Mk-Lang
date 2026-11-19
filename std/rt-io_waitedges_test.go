// Why: std.sys's waits, semaphores and console reads had defects at their
// edges; each needs the case that failed before its fix.
// What: TestRtIo_WaitEdgesLinux (io_uring and epoll) and the probe build for
// probe5.mk's cases EpollMs, SemBig, StrayTimer and ConRead.
// How: builds std/rtioprobe with probe5.mk and its own Main, runs it, and
// wants every case 0. The Windows run is in rt-io_waitedges_windows_test.go.
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

// rtio52Main runs the probe5.mk cases once each.
const rtio52Main = `import std.rtioprobe

export Main(sys: &System): WriteError!void {
    var out := sys.Stdout()
    b := rtioprobe.Backend()
    try Print(mut out, "backend {b}\n", (b))
    // First, while no other thread of the probe exists: a wake from a
    // thread of a later case would end its waits early (the contract).
    st := rtioprobe.StrayTimer()
    try Print(mut out, "straytimer {st}\n", (st))
    em := rtioprobe.EpollMs()
    try Print(mut out, "epollms {em}\n", (em))
    sb := rtioprobe.SemBig()
    try Print(mut out, "sembig {sb}\n", (sb))
    nc := rtioprobe.NowCost()
    try Print(mut out, "nowcost {nc}\n", (nc))
    cr := rtioprobe.ConRead()
    try Print(mut out, "conread {cr}\n", (cr))
    try out.Flush()
}
`

// rtio52Cases are the lines whose value must be 0.
var rtio52Cases = []string{"epollms", "sembig", "straytimer", "conread"}

// buildRTIO52 builds the probe5.mk probe for target (empty: the host), over
// the tree's std or over MK_RTIO_STD (for mutants), and returns the
// executable's path.
func buildRTIO52(t *testing.T, target string) string {
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
	if err := os.WriteFile(filepath.Join(root, "main.mk"), []byte(rtio52Main), 0o644); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(root, "probe52")
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

// rtio52Judge checks one run: the backend and every case 0; it returns the
// lines by name.
func rtio52Judge(t *testing.T, out, backend string) map[string]string {
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
	for _, c := range rtio52Cases {
		if got[c] != "0" {
			if !rtioJudged(c) {
				t.Logf("%s: %q (%s: the case has a wall-clock step, so it is not judged)", c, got[c], timingOff)
				continue
			}
			t.Errorf("%s: %q, want 0 (else the number of the failed step)\n%s", c, got[c], out)
		}
	}
	t.Logf("backend %s: one sys.Now() costs %s ns (mean of 1,000,000 calls)", backend, got["nowcost"])
	return got
}

// TestRtIo_WaitEdgesLinux: the probe5.mk cases on x86_64-linux with io_uring and
// with MK_IO=epoll; with test point 6 epoll waits by epoll_pwait, as on
// every kernel before 5.11. Both backends agree on every case.
func TestRtIo_WaitEdgesLinux(t *testing.T) {
	needWSL(t)
	exe := buildRTIO52(t, "x86_64-linux")
	var outs [2]map[string]string
	for i, b := range []struct{ name, env, backend string }{{"io_uring", "MK_IO=auto", "2"}, {"epoll", "MK_IO=epoll", "3"}} {
		stdout, stderr, code := runWSL(t, exe, []string{b.env})
		if code != 0 {
			t.Errorf("%s: exit %d\nstdout:\n%s\nstderr:\n%s", b.name, code, stdout, stderr)
			continue
		}
		outs[i] = rtio52Judge(t, stdout, b.backend)
	}
	if outs[0] != nil && outs[1] != nil {
		for _, c := range rtio52Cases {
			if outs[0][c] != outs[1][c] && rtioJudged(c) {
				t.Errorf("%s: io_uring %q, epoll %q; the backends must agree", c, outs[0][c], outs[1][c])
			}
		}
	}
}
