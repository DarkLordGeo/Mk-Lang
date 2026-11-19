// Why: mk must behave the same when it runs natively on Linux.
// What: TestLinuxNativeMk, with the linuxHost helper that runs commands
// through wsl.exe on Windows or directly on Linux.
// How: compares a Linux mk's corpus report with this process's, byte for
// byte, and runs fizz.mk against WSL's own clang.
//
// TestLinuxNativeMk builds mk itself for x86_64-linux with
// GOOS=linux CGO_ENABLED=0 and runs it inside WSL (natively on a Linux
// host): `mk corpus` must print the same report as the corpus run in this
// process, byte for byte and with the same exit status, and
// `mk run tests/programs/fizz.mk` must print fizz.out against WSL's own
// clang, in dev and in release.

package corpus

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// linuxHost runs commands on Linux: through wsl.exe on Windows, directly on
// a Linux host.
type linuxHost struct {
	wsl bool
}

// needLinux returns the Linux host, or skips (fatal under MK_GATE=1) when
// there is none. A cold WSL is warmed first, with a generous timeout: a
// cold start can take far longer than a warm command.
func needLinux(t *testing.T) linuxHost {
	t.Helper()
	switch runtime.GOOS {
	case "linux":
		if runtime.GOARCH != "amd64" {
			t.Skipf("x86_64-linux mk needs an amd64 host, not %s", runtime.GOARCH)
		}
		return linuxHost{}
	case "windows":
	default:
		t.Skipf("x86_64-linux mk runs on Windows through WSL or on Linux, not on %s", runtime.GOOS)
	}
	if !linuxRun() {
		t.Skip(linuxOff)
	}
	wslGuard()
	skip := func(why string) {
		if gateRun() {
			t.Fatalf("needs WSL: %s", why)
		}
		t.Skipf("needs WSL: %s", why)
	}
	if _, err := exec.LookPath("wsl.exe"); err != nil {
		skip(err.Error())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "wsl.exe", "-e", "uname", "-sm").Output()
	if err != nil {
		skip("wsl.exe -e uname -sm: " + err.Error())
	}
	if got := strings.TrimSpace(string(out)); got != "Linux x86_64" {
		skip("uname -sm says " + got)
	}
	return linuxHost{wsl: true}
}

// path returns the Linux form of a host path.
func (h linuxHost) path(t *testing.T, p string) string {
	t.Helper()
	if !h.wsl {
		return p
	}
	wslGuard()
	out, err := exec.Command("wsl.exe", "-e", "wslpath", "-a", p).Output()
	if err != nil {
		t.Fatalf("wslpath -a %s: %v", p, err)
	}
	return strings.TrimSpace(string(out))
}

// run runs argv on Linux in dir (a Linux path) and returns stdout, stderr
// and the exit status.
func (h linuxHost) run(t *testing.T, dir string, argv ...string) (stdout, stderr []byte, status int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	var cmd *exec.Cmd
	if h.wsl {
		wslGuard()
		cmd = exec.CommandContext(ctx, "wsl.exe", append([]string{"--cd", dir, "--"}, argv...)...)
	} else {
		cmd = exec.CommandContext(ctx, argv[0], argv[1:]...)
		cmd.Dir = dir
	}
	var o, e bytes.Buffer
	cmd.Stdout, cmd.Stderr = &o, &e
	err := cmd.Run()
	var ee *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &ee):
		status = ee.ExitCode()
	default:
		t.Fatalf("%s: %v", strings.Join(argv, " "), err)
	}
	return o.Bytes(), e.Bytes(), status
}

// buildLinuxMk is GOOS=linux CGO_ENABLED=0 go build -o <dir>/mk ./cmd/mk.
func buildLinuxMk(t *testing.T, root string) string {
	t.Helper()
	gobin := goCommand(t, "to build mk for Linux")
	exe := filepath.Join(t.TempDir(), "mk")
	cmd := exec.Command(gobin, "build", "-o", exe, "./cmd/mk")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GOOS=linux", "GOARCH=amd64", "CGO_ENABLED=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("GOOS=linux CGO_ENABLED=0 go build ./cmd/mk: %v\n%s", err, out)
	}
	return exe
}

func TestLinuxNativeMk(t *testing.T) {
	root := repoRoot(t)
	host := needLinux(t)
	mk := host.path(t, buildLinuxMk(t, root))
	lroot := host.path(t, root)

	t.Run("corpus", func(t *testing.T) {
		var want bytes.Buffer
		res := designRun(t)
		if err := WriteText(&want, res); err != nil {
			t.Fatal(err)
		}
		got, stderr, status := host.run(t, lroot, mk, "corpus")
		if status != ExitStatus(res) {
			t.Errorf("Linux mk corpus exits %d, this process's run %d; stderr:\n%s", status, ExitStatus(res), stderr)
		}
		if !bytes.Equal(got, want.Bytes()) {
			t.Errorf("Linux mk corpus report differs from this process's:\n%s", firstDiff(got, want.Bytes()))
		}
		t.Logf("Linux mk corpus: %s", summaryLine(got))
	})

	want, err := readCommitted(filepath.Join(root, ProgramsDir, "fizz.out"))
	if err != nil {
		t.Fatal(err)
	}
	for _, prof := range []string{"dev", "release"} {
		t.Run("fizz/"+prof, func(t *testing.T) {
			// MK_CACHE in the test's scratch: a fetched package's C sources
			// reach the cache even under --no-cache, and the per-user
			// default is WSL's ~/.cache/mk (env does not cross wsl.exe).
			argv := []string{"env", "MK_CACHE=" + host.path(t, t.TempDir()), mk, "run", "--no-cache"}
			if prof == "release" {
				argv = append(argv, "--release")
			}
			argv = append(argv, ProgramsDir+"/fizz.mk")
			stdout, stderr, status := host.run(t, lroot, argv...)
			if status == 0 && bytes.Equal(stdout, want) && len(stderr) == 0 {
				return
			}
			t.Fatalf("Linux mk run fizz.mk (%s): exit %d, want 0\nstdout:\n%s\nwant:\n%s\nstderr:\n%s", prof, status, stdout, want, stderr)
		})
	}
}

// summaryLine is the totals line of a text report.
func summaryLine(report []byte) string {
	for _, l := range strings.Split(string(report), "\n") {
		if strings.HasPrefix(l, "pass ") {
			return l
		}
	}
	return "(no totals line)"
}

// firstDiff shows the first line where got and want differ.
func firstDiff(got, want []byte) string {
	g, w := strings.Split(string(got), "\n"), strings.Split(string(want), "\n")
	for i := 0; i < len(g) || i < len(w); i++ {
		var gl, wl string
		if i < len(g) {
			gl = g[i]
		}
		if i < len(w) {
			wl = w[i]
		}
		if gl != wl {
			return "line " + itoa(i+1) + ":\n got  " + gl + "\n want " + wl
		}
	}
	return "(equal lines; the bytes differ)"
}

// goCommand finds the go command (go test puts GOROOT/bin on PATH; measured
// with a PATH that lacks it). Missing, the test skips, and fails under
// MK_GATE=1.
func goCommand(t *testing.T, why string) string {
	t.Helper()
	gobin, err := exec.LookPath("go")
	if err != nil {
		if gateRun() {
			t.Fatalf("needs the go command %s: %v", why, err)
		}
		t.Skipf("needs the go command %s: %v", why, err)
	}
	return gobin
}
