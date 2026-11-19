// Why: the evaluator and compiled code must give the same bits for every
// SIMD operation, at dev and release, and dispatch must pick a working path.
// What: TestDifferentialNative: each program of testdata/simd prints
// Kernel() as the const C (the evaluator) and as a run-time call, and the
// two must be equal; TestDispatchNative runs the dispatch programs.
// How: mk runs as a process (simdMkExe), built once from this repository
// (MK_TEST_EXE names another), since codegen may import neither driver nor
// corpus. The programs make no NaN (its payload is unspecified), so every
// bit must agree.

package codegen

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

var simdMk struct {
	once sync.Once
	exe  string
	err  string
}

func simdMkExe(t *testing.T) string {
	t.Helper()
	simdMk.once.Do(func() {
		if simdMk.exe = os.Getenv("MK_TEST_EXE"); simdMk.exe != "" {
			return
		}
		// Under the package's shared directory, which TestMain removes
		// (mksimd* directories once filled TMP).
		dir := sharedTempDir(t, "mksimd")
		simdMk.exe = filepath.Join(dir, "mk")
		if runtime.GOOS == "windows" {
			simdMk.exe += ".exe"
		}
		goTool := filepath.Join(runtime.GOROOT(), "bin", "go")
		if runtime.GOOS == "windows" {
			goTool += ".exe"
		}
		if _, err := os.Stat(goTool); err != nil {
			if p, err := exec.LookPath("go"); err == nil {
				goTool = p
			}
		}
		cmd := exec.Command(goTool, "build", "-o", simdMk.exe, "./cmd/mk")
		cmd.Dir = filepath.Join("..", "..")
		if out, err := cmd.CombinedOutput(); err != nil {
			simdMk.err = "go build ./cmd/mk: " + err.Error() + "\n" + string(out)
		}
	})
	if simdMk.err != "" {
		t.Fatal(simdMk.err)
	}
	return simdMk.exe
}

func TestDifferentialNative(t *testing.T) {
	needLLVM(t)
	files, err := filepath.Glob(filepath.Join("testdata", "simd", "*.mk"))
	if err != nil || len(files) < 4 {
		t.Fatalf("quick set: %v %d programs", err, len(files))
	}
	// A directory with a pkg.mk is a package (dispatch needs unsafe_modules).
	pkgs, _ := filepath.Glob(filepath.Join("testdata", "simd", "*", "pkg.mk"))
	for _, p := range pkgs {
		files = append(files, filepath.Dir(p))
	}
	exe := simdMkExe(t)
	for _, f := range files {
		srcs := map[string][]byte{}
		if strings.HasSuffix(f, ".mk") {
			b, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			srcs["main.mk"] = b
		} else {
			for _, n := range []string{"main.mk", "pkg.mk"} {
				b, err := os.ReadFile(filepath.Join(f, n))
				if err != nil {
					t.Fatal(err)
				}
				srcs[n] = b
			}
		}
		name := strings.TrimSuffix(filepath.Base(f), ".mk")
		for _, prof := range []string{"dev", "release"} {
			t.Run(name+"/"+prof, func(t *testing.T) {
				t.Parallel()
				dir := t.TempDir()
				for n, b := range srcs {
					if err := os.WriteFile(filepath.Join(dir, n), b, 0o644); err != nil {
						t.Fatal(err)
					}
				}
				args := []string{"run"}
				if prof == "release" {
					args = append(args, "--release")
				}
				root := "main.mk"
				if _, ok := srcs["pkg.mk"]; ok {
					root = "."
				}
				cmd := exec.Command(exe, append(args, root)...)
				cmd.Dir = dir
				cmd.Env = append(os.Environ(), "MK_CACHE="+filepath.Join(dir, ".mk-cache"))
				out, err := cmd.CombinedOutput()
				if err != nil {
					t.Fatalf("mk %v: %v\n%s", args, err, out)
				}
				fields := strings.Fields(string(out))
				if len(fields) < 2 || fields[0] != fields[1] {
					t.Fatalf("evaluator and native disagree: %q (want two equal numbers: the const, then the run)", out)
				}
			})
		}
	}
}

// TestDispatchNative (std.simd's Choice and Dispatch build on these
// rules): the two dispatch packages built with mk and run at dev and at
// release. testdata/simd/dispatch calls a cpu "avx2" function directly
// under a Cpu() test in unsafe; testdata/simd/dispatchfn picks fn values (a
// cpu function named as a value in unsafe, and Fma named as a value inside
// a cpu "fma" function). Each prints the const (the evaluator's baseline
// path), the run (the machine's path) and whether the machine has the
// features; the first two must agree and the third must be a bool.
func TestDispatchNative(t *testing.T) {
	needLLVM(t)
	exe := simdMkExe(t)
	for _, pkg := range []string{"dispatch", "dispatchfn"} {
		srcs := map[string][]byte{}
		for _, n := range []string{"main.mk", "pkg.mk"} {
			b, err := os.ReadFile(filepath.Join("testdata", "simd", pkg, n))
			if err != nil {
				t.Fatal(err)
			}
			srcs[n] = b
		}
		for _, prof := range []string{"dev", "release"} {
			t.Run(pkg+"/"+prof, func(t *testing.T) {
				t.Parallel()
				dir := t.TempDir()
				for n, b := range srcs {
					if err := os.WriteFile(filepath.Join(dir, n), b, 0o644); err != nil {
						t.Fatal(err)
					}
				}
				args := []string{"run"}
				if prof == "release" {
					args = append(args, "--release")
				}
				cmd := exec.Command(exe, append(args, ".")...)
				cmd.Dir = dir
				cmd.Env = append(os.Environ(), "MK_CACHE="+filepath.Join(dir, ".mk-cache"))
				out, err := cmd.CombinedOutput()
				if err != nil {
					t.Fatalf("mk %v: %v\n%s", args, err, out)
				}
				f := strings.Fields(string(out))
				if len(f) != 3 || f[0] != f[1] || (f[2] != "true" && f[2] != "false") {
					t.Fatalf("want `<const> <run> <bool>` with the two numbers equal, got %q", out)
				}
			})
		}
	}
}
