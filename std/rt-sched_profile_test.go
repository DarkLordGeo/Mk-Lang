// Why: a --release --pgo-gen build must write its profile on every exit path,
// in a file mk profile merge accepts.
// What: TestRtSched_ProfileWritten and TestRtSched_LinuxProfile: the
// <name>-<pid>.mkprof file in MK_PROFILE_DIR or beside the executable.
// How: std's tests cannot import internal/pgo (internal/archtest gives std no
// edges), so the test decodes the layout itself and lets the mk command check.

package std

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// rtsProfRec is one decoded record.
type rtsProfRec struct {
	key    string
	sum    [16]byte
	counts []uint64
}

// rtsReadProf decodes a version-1 .mkprof, strictly: the
// magic, the count, every record, nothing after the last one.
func rtsReadProf(b []byte) ([]rtsProfRec, error) {
	const magic = "MKPROF\x00\x01"
	if len(b) < len(magic)+8 || string(b[:len(magic)]) != magic {
		return nil, fmt.Errorf("bad magic or short header (%d bytes)", len(b))
	}
	n := binary.LittleEndian.Uint64(b[len(magic):])
	p := len(magic) + 8
	var out []rtsProfRec
	for i := uint64(0); i < n; i++ {
		if p+4 > len(b) {
			return nil, fmt.Errorf("record %d: short key length", i)
		}
		kl := int(binary.LittleEndian.Uint32(b[p:]))
		p += 4
		if p+kl+16+4 > len(b) {
			return nil, fmt.Errorf("record %d: short key or checksum", i)
		}
		r := rtsProfRec{key: string(b[p : p+kl])}
		p += kl
		copy(r.sum[:], b[p:p+16])
		p += 16
		c := int(binary.LittleEndian.Uint32(b[p:]))
		p += 4
		if p+8*c > len(b) {
			return nil, fmt.Errorf("record %d (%s): short counters", i, r.key)
		}
		for j := 0; j < c; j++ {
			r.counts = append(r.counts, binary.LittleEndian.Uint64(b[p+8*j:]))
		}
		p += 8 * c
		if len(out) > 0 {
			q := out[len(out)-1]
			if q.key > r.key || (q.key == r.key && bytes.Compare(q.sum[:], r.sum[:]) >= 0) {
				return nil, fmt.Errorf("record %d (%s) out of order after %s", i, r.key, q.key)
			}
		}
		out = append(out, r)
	}
	if p != len(b) {
		return nil, fmt.Errorf("%d bytes after the last of %d records", len(b)-p, n)
	}
	return out, nil
}

// rtsProfPrograms are the exit paths: Main returns, Main fails (the profile
// is written before the error exit), proc.Exit.
var rtsProfPrograms = []struct {
	name, src string
	status    int
}{
	{"ok", `Count(n: int): int {
    var s := 0
    for i in 0..n {
        if i % 3 == 0 {
            s += i
        }
    }
    return s
}

export Main(sys: &System): int {
    if Count(1000) == 166833 {
        return 0
    }
    return 7
}
`, 0},
	{"fails", `import std.fs

export Main(sys: &System): fs.Error!void {
    fail fs.NotFound
}
`, 1},
	{"exit", `import std.proc

export Main(sys: &System): int {
    proc.Exit(3)
}
`, 3},
}

// TestRtSched_ProfileWritten builds each program with --release --pgo-gen,
// runs it with MK_PROFILE_DIR and without, and checks the file: its name,
// its layout, a nonzero entry count for Main, and that mk profile merge
// (pgo.Read and Merge) accepts it.
func TestRtSched_ProfileWritten(t *testing.T) {
	rtsNative(t)
	for _, prog := range rtsProfPrograms {
		t.Run(prog.name, func(t *testing.T) {
			root := t.TempDir()
			stdDir := copyStd(t, root, nil)
			if err := os.WriteFile(filepath.Join(root, "main.mk"), []byte(prog.src), 0o644); err != nil {
				t.Fatal(err)
			}
			exe := filepath.Join(root, "bin", "prog")
			if runtime.GOOS == "windows" {
				exe += ".exe"
			}
			if err := os.MkdirAll(filepath.Dir(exe), 0o755); err != nil {
				t.Fatal(err)
			}
			if out, st := mkCmd(t, root, stdDir, "build", "--release", "--pgo-gen", "-o", exe, "main.mk"); st != 0 {
				if strings.Contains(out, "no usable LLVM") {
					t.Skipf("needs the LLVM toolchain: %s", out)
				}
				t.Fatalf("mk build: exit %d\n%s", st, out)
			}
			for _, viaEnv := range []bool{true, false} {
				dir := filepath.Dir(exe)
				cmd := exec.Command(exe)
				cmd.Env = os.Environ()
				if viaEnv {
					dir = t.TempDir()
					cmd.Env = append(cmd.Env, "MK_PROFILE_DIR="+dir)
				}
				var stderr bytes.Buffer
				cmd.Stderr = &stderr
				err := cmd.Run()
				st := 0
				if ee, ok := err.(*exec.ExitError); ok {
					st = ee.ExitCode()
				} else if err != nil {
					t.Fatal(err)
				}
				if st != prog.status || strings.Contains(stderr.String(), "cannot write profile") {
					t.Fatalf("env=%v: exit %d, want %d; stderr:\n%s", viaEnv, st, prog.status, stderr.String())
				}
				path := filepath.Join(dir, fmt.Sprintf("prog-%d.mkprof", cmd.Process.Pid))
				b, err := os.ReadFile(path)
				if err != nil {
					ents, _ := os.ReadDir(dir)
					var names []string
					for _, e := range ents {
						names = append(names, e.Name())
					}
					t.Fatalf("env=%v: %v (directory holds %v)", viaEnv, err, names)
				}
				recs, err := rtsReadProf(b)
				if err != nil {
					t.Fatalf("env=%v: %s: %v", viaEnv, path, err)
				}
				var main *rtsProfRec
				for i := range recs {
					if recs[i].key == "mk.main.Main" {
						main = &recs[i]
					}
				}
				if len(recs) == 0 || main == nil || len(main.counts) == 0 || main.counts[0] == 0 {
					var keys []string
					for _, r := range recs {
						keys = append(keys, fmt.Sprintf("%s%v", r.key, r.counts))
					}
					t.Fatalf("env=%v: no record of Main with an entry count: %v", viaEnv, keys)
				}
				merged := filepath.Join(t.TempDir(), "merged.mkprof")
				if out, st := mkCmd(t, root, stdDir, "profile", "merge", "-o", merged, path); st != 0 {
					t.Fatalf("mk profile merge rejected %s: exit %d\n%s", path, st, out)
				}
				t.Logf("env=%v: %s: %d bytes, %d records, Main %s entry %d", viaEnv, filepath.Base(path), len(b), len(recs), main.key, main.counts[0])
				if viaEnv {
					// The loop closes: a --pgo build reads the merged profile.
					exe2 := filepath.Join(root, "bin", "prog2")
					if runtime.GOOS == "windows" {
						exe2 += ".exe"
					}
					if out, st := mkCmd(t, root, stdDir, "build", "--release", "--pgo="+merged, "-o", exe2, "main.mk"); st != 0 {
						t.Fatalf("mk build --pgo: exit %d\n%s", st, out)
					}
				}
				os.Remove(path)
			}
		})
	}
}

// TestRtSched_LinuxProfile is TestRtSched_ProfileWritten's ok and fails
// programs on x86_64-linux in WSL, with MK_PROFILE_DIR, in one wsl.exe
// client (run it under tools/wsl-lock.ps1 with go test -p 1).
func TestRtSched_LinuxProfile(t *testing.T) {
	rtsNeedWSL(t)
	for _, prog := range rtsProfPrograms[:2] {
		t.Run(prog.name, func(t *testing.T) {
			root := t.TempDir()
			stdDir := copyStd(t, root, nil)
			if err := os.WriteFile(filepath.Join(root, "main.mk"), []byte(prog.src), 0o644); err != nil {
				t.Fatal(err)
			}
			exe := filepath.Join(root, "prog")
			if out, st := mkCmd(t, root, stdDir, "build", "--release", "--pgo-gen", "--target", "x86_64-linux", "-o", exe, "main.mk"); st != 0 {
				if strings.Contains(out, "no usable LLVM") {
					t.Skipf("needs the LLVM toolchain: %s", out)
				}
				t.Fatalf("mk build: exit %d\n%s", st, out)
			}
			dir := filepath.Join(root, "out")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			sh := fmt.Sprintf("cd %q && MK_PROFILE_DIR=%q ./prog 2>err.txt; echo $?", rtsWSLPath(root), rtsWSLPath(dir))
			wslGuard()
			out, err := exec.Command("wsl.exe", "-e", "sh", "-c", sh).Output()
			if err != nil {
				t.Fatalf("wsl.exe: %v", err)
			}
			var st int
			fmt.Sscan(strings.TrimSpace(string(out)), &st)
			e, _ := os.ReadFile(filepath.Join(root, "err.txt"))
			if st != prog.status || strings.Contains(string(e), "cannot write profile") {
				t.Fatalf("exit %d, want %d; stderr:\n%s", st, prog.status, e)
			}
			files, _ := filepath.Glob(filepath.Join(dir, "prog-*.mkprof"))
			if len(files) != 1 {
				t.Fatalf("want one prog-<pid>.mkprof in %s, have %v", dir, files)
			}
			b, err := os.ReadFile(files[0])
			if err != nil {
				t.Fatal(err)
			}
			recs, err := rtsReadProf(b)
			if err != nil {
				t.Fatalf("%s: %v", files[0], err)
			}
			found := false
			for _, r := range recs {
				if r.key == "mk.main.Main" && len(r.counts) > 0 && r.counts[0] == 1 {
					found = true
				}
			}
			if !found {
				t.Fatalf("no record of Main with entry count 1 in %d records", len(recs))
			}
			merged := filepath.Join(t.TempDir(), "merged.mkprof")
			if out, st := mkCmd(t, root, stdDir, "profile", "merge", "-o", merged, files[0]); st != 0 {
				t.Fatalf("mk profile merge rejected %s: exit %d\n%s", files[0], st, out)
			}
			t.Logf("%s: %d bytes, %d records", filepath.Base(files[0]), len(b), len(recs))
		})
	}
}
