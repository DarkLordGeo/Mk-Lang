// Why: a task program's output must not depend on how many carriers run it.
// What: the tests/programs/tasks/ programs with MK_CARRIERS 1, 2 and 8, dev
// and release, against each program's expected .out file.
// How: Windows only (the corpus's TestPrograms covers Linux); signal.mk is
// left out, as it needs an outside signal (TestRtSched_SignalWindows).

package std

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// rtsDetDir is tests/programs/tasks, beside std/ (RTS_TASKS_DIR overrides
// it, for a run from a scratch copy of std).
func rtsDetDir() string {
	if d := os.Getenv("RTS_TASKS_DIR"); d != "" {
		return d
	}
	return filepath.Join("..", "tests", "programs", "tasks")
}

// rtsDetWant is the expected standard output named by prog's .cases file
// ("stdout <file>"), or nil when the file names none.
func rtsDetWant(t *testing.T, dir, prog string) []byte {
	t.Helper()
	c, err := os.ReadFile(filepath.Join(dir, strings.TrimSuffix(prog, ".mk")+".cases"))
	if err != nil {
		return nil
	}
	for _, line := range strings.Split(strings.ReplaceAll(string(c), "\r\n", "\n"), "\n") {
		f := strings.Fields(line)
		if len(f) == 2 && f[0] == "stdout" {
			b, err := os.ReadFile(filepath.Join(dir, f[1]))
			if err != nil {
				t.Fatalf("%s: %v", prog, err)
			}
			return bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n"))
		}
	}
	return nil
}

// rtsDetArgs is the first case's arguments in prog's .cases file (the
// args line; none when there is no file or no such line). Some tasks
// programs (pingpong.mk, spawn.mk) take their size from it.
func rtsDetArgs(dir, prog string) []string {
	c, err := os.ReadFile(filepath.Join(dir, strings.TrimSuffix(prog, ".mk")+".cases"))
	if err != nil {
		return nil
	}
	for _, line := range strings.Split(strings.ReplaceAll(string(c), "\r\n", "\n"), "\n") {
		f := strings.Fields(line)
		if len(f) > 1 && f[0] == "args" {
			return f[1:]
		}
	}
	return nil
}

func TestRtSched_DetScheduler(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows; the corpus runs these programs on Linux")
	}
	dir := rtsDetDir()
	progs, err := filepath.Glob(filepath.Join(dir, "*.mk"))
	if err != nil {
		t.Fatal(err)
	}
	if len(progs) == 0 {
		if os.Getenv("MK_GATE") == "1" {
			t.Fatalf("no programs in %s", dir)
		}
		t.Skipf("no programs in %s", dir)
	}
	root := t.TempDir()
	stdDir := copyStd(t, root, nil)
	for _, p := range progs {
		name := filepath.Base(p)
		if name == "signal.mk" {
			continue
		}
		src, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		want := rtsDetWant(t, dir, name)
		for _, profile := range []string{"dev", "release"} {
			t.Run(name+"/"+profile, func(t *testing.T) {
				work := filepath.Join(root, strings.TrimSuffix(name, ".mk")+"-"+profile)
				if err := os.MkdirAll(work, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(work, "main.mk"), src, 0o644); err != nil {
					t.Fatal(err)
				}
				exe := filepath.Join(work, "prog.exe")
				args := []string{"build"}
				if profile == "release" {
					args = append(args, "--release")
				}
				args = append(args, "-o", exe, "main.mk")
				if out, status := mkCmd(t, work, stdDir, args...); status != 0 {
					if strings.Contains(out, "no usable LLVM") {
						t.Skipf("needs the LLVM toolchain: %s", out)
					}
					t.Fatalf("mk build: exit %d\n%s", status, out)
				}
				var first []byte
				for _, n := range []string{"1", "2", "8"} {
					ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
					cmd := exec.CommandContext(ctx, exe, rtsDetArgs(dir, name)...)
					cmd.Dir = work
					cmd.Env = append(os.Environ(), "MK_CARRIERS="+n)
					var so, se bytes.Buffer
					cmd.Stdout, cmd.Stderr = &so, &se
					err := cmd.Run()
					cancel()
					if err != nil {
						t.Fatalf("MK_CARRIERS=%s: %v\nstderr:\n%s", n, err, se.String())
					}
					got := bytes.ReplaceAll(so.Bytes(), []byte("\r\n"), []byte("\n"))
					if first == nil {
						first = got
					} else if !bytes.Equal(got, first) {
						t.Fatalf("MK_CARRIERS=%s output differs from MK_CARRIERS=1:\n%s\nwant:\n%s", n, got, first)
					}
					if want != nil && !bytes.Equal(got, want) {
						t.Fatalf("MK_CARRIERS=%s output is not the .out file:\n%s\nwant:\n%s", n, got, want)
					}
				}
			})
		}
	}
}
