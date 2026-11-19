// Why: tests that need the whole pipeline (compile-time requires, codegen,
// mk corpus) must run mk over std with the test copy of std.simd.
// What: overlayMk, which builds mk once and makes the std overlay once, and
// sweepOverlays.
// How: mk, built from this repository, runs over a temporary copy of the
// tree's std with testdata/simd/vec.mk at simd/vec.mk, given through MK_STD.
// check may import neither driver nor corpus (internal/archtest), so mk runs
// as a process, as std's own tests run it (std/mkcheck_test.go mkExe).

package check

import (
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

var overlay struct {
	once     sync.Once
	exe, std string
	err      string
}

// overlayMk builds mk once and makes the std overlay once; it returns the
// mk executable and the overlay's std directory. MK_TEST_EXE names another
// mk to use.
func overlayMk(t *testing.T) (string, string) {
	t.Helper()
	overlay.once.Do(func() {
		sweepOverlays()
		dir, err := os.MkdirTemp("", "mksys")
		if err != nil {
			overlay.err = err.Error()
			return
		}
		overlay.exe = os.Getenv("MK_TEST_EXE")
		if overlay.exe == "" {
			goTool := filepath.Join(runtime.GOROOT(), "bin", "go")
			if runtime.GOOS == "windows" {
				goTool += ".exe"
			}
			if _, err := os.Stat(goTool); err != nil {
				if p, err := exec.LookPath("go"); err == nil {
					goTool = p
				}
			}
			overlay.exe = filepath.Join(dir, "mk")
			if runtime.GOOS == "windows" {
				overlay.exe += ".exe"
			}
			cmd := exec.Command(goTool, "build", "-o", overlay.exe, "./cmd/mk")
			cmd.Dir = filepath.Join("..", "..")
			if out, err := cmd.CombinedOutput(); err != nil {
				overlay.err = "go build ./cmd/mk: " + err.Error() + "\n" + string(out)
				return
			}
		}
		overlay.std = filepath.Join(dir, "std")
		src := filepath.Join("..", "..", "std")
		err = filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(src, p)
			dst := filepath.Join(overlay.std, rel)
			if d.IsDir() {
				return os.MkdirAll(dst, 0o755)
			}
			if !strings.HasSuffix(p, ".mk") {
				return nil
			}
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			return os.WriteFile(dst, b, 0o644)
		})
		if err == nil {
			var vec []byte
			if vec, err = os.ReadFile(filepath.Join("testdata", "simd", "vec.mk")); err == nil {
				if err = os.MkdirAll(filepath.Join(overlay.std, "simd"), 0o755); err == nil {
					err = os.WriteFile(filepath.Join(overlay.std, "simd", "vec.mk"), vec, 0o644)
				}
			}
		}
		if err != nil {
			overlay.err = "std overlay: " + err.Error()
		}
	})
	if overlay.err != "" {
		t.Fatal(overlay.err)
	}
	return overlay.exe, overlay.std
}

// mkOverlay runs `mk <args> main.mk` over the overlay in a fresh directory
// holding src as main.mk; it returns the combined output and exit status.
func mkOverlay(t *testing.T, src string, args ...string) (string, int) {
	t.Helper()
	exe, std := overlayMk(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.mk"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe, append(args, "main.mk")...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "MK_STD="+std, "MK_CACHE="+filepath.Join(dir, ".mk-cache"))
	out, err := cmd.CombinedOutput()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return string(out), code
}

// sweepOverlays removes the mksys* directories earlier runs left in the
// temporary directory more than a day ago: this package has no TestMain to
// remove its own (its TestMain is a test of the Main rule), so each run
// clears what the earlier ones left. A directory of a run still going is
// younger than that.
func sweepOverlays() {
	ents, err := os.ReadDir(os.TempDir())
	if err != nil {
		return
	}
	for _, e := range ents {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), "mksys") {
			continue
		}
		if fi, err := e.Info(); err == nil && time.Since(fi.ModTime()) > 24*time.Hour {
			os.RemoveAll(filepath.Join(os.TempDir(), e.Name()))
		}
	}
}
