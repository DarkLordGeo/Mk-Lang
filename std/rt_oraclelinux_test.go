// Why: the Linux leg of the oracle for rt's call functions (std/rt/call.mk,
// proc.mk): Go's standard library, cross-compiled, reads the same files.
// What: TestRt_OracleLinux (MK_LINUX=1, under tools/wsl-lock.ps1 with go
// test -p 1).
// How: the blocks read a fresh directory under the distribution's /tmp
// (linuxScratchSh); the executables stay on the drive; one wsl.exe client.

package std

import (
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// rt8OracleHelper is the Go program the Linux leg runs in the distribution.
// "calls exe index dir": makes the oracle files in dir, runs the block there
// with MK_RT8_ORACLE=dir, and prints its exit status (X), each line of its
// standard error (M), and Go's facts (G S, G L, G C, G A, G T) after it.
// "env exe index": runs the block with MK_RT8_ENVORACLE=1 and prints its
// status and standard error, then the listing /bin/sh -c env gives under
// os/exec with the block's environment and the block's list appended (G E,
// hex).
const rt8OracleHelper = `package main

import (
	"bufio"
	"bytes"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func kind(m os.FileMode) int64 {
	switch {
	case m&os.ModeSymlink != 0:
		return 3
	case m.IsDir():
		return 2
	case m.IsRegular():
		return 1
	}
	return 4
}

func run(exe, index string, env []string) {
	cmd := exec.Command(exe, index)
	cmd.Env = env
	var e bytes.Buffer
	cmd.Stderr = &e
	err := cmd.Run()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		code = -1
	}
	fmt.Printf("X %d\n", code)
	sc := bufio.NewScanner(&e)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		fmt.Printf("M %s\n", sc.Text())
	}
}

func must(err error) {
	if err != nil {
		fmt.Printf("FAIL %v\n", err)
		os.Exit(2)
	}
}

func main() {
	exe, index := os.Args[2], os.Args[3]
	switch os.Args[1] {
	case "calls":
		dir := os.Args[4]
		f := filepath.Join(dir, "f")
		must(os.WriteFile(f, make([]byte, 1234), 0o644))
		when := time.Date(2021, 3, 4, 5, 6, 7, 123456789, time.UTC)
		must(os.Chtimes(f, when, when))
		must(os.WriteFile(filepath.Join(dir, "ro"), []byte("x"), 0o644))
		must(os.Chmod(filepath.Join(dir, "ro"), 0o444))
		must(os.Mkdir(filepath.Join(dir, "d"), 0o755))
		must(os.WriteFile(filepath.Join(dir, "g"), []byte("g"), 0o644))
		must(os.Symlink("f", filepath.Join(dir, "l")))
		must(os.Chdir(dir))
		run(exe, index, append(os.Environ(), "MK_RT8_ORACLE="+dir))
		for _, name := range []string{"f", "ro", "d", "l", "missing", "g"} {
			for _, follow := range []string{"0", "1"} {
				var fi os.FileInfo
				var err error
				if follow == "1" {
					fi, err = os.Stat(filepath.Join(dir, name))
				} else {
					fi, err = os.Lstat(filepath.Join(dir, name))
				}
				if err != nil {
					fmt.Printf("G S %s %s 0\n", name, follow)
					continue
				}
				fmt.Printf("G S %s %s 1 %d %d %d %d\n", name, follow, kind(fi.Mode()), fi.Size(), fi.ModTime().UnixNano(), int64(fi.Mode().Perm()))
			}
		}
		l, err := os.Readlink(filepath.Join(dir, "l"))
		must(err)
		fmt.Printf("G L %s\n", l)
		wd, err := os.Getwd()
		must(err)
		fmt.Printf("G C %s\n", wd)
		for _, in := range []string{"rel", ".", dir} {
			a, err := filepath.Abs(in)
			must(err)
			fmt.Printf("G A %s %s\n", in, a)
		}
	case "env":
		dir := os.Args[4]
		must(os.Chdir(dir))
		env := append(os.Environ(), "MK_RT8_ENVORACLE=1")
		run(exe, index, env)
		cmd := exec.Command("/bin/sh", "-c", "env")
		cmd.Env = append(env, strings.Split(os.Args[5], "\x1f")...)
		out, err := cmd.Output()
		must(err)
		fmt.Printf("G E %s\n", hex.EncodeToString(out))
	}
}
`

// rt8BuildHelper cross-compiles rt8OracleHelper for x86_64-linux into dir.
func rt8BuildHelper(t *testing.T, dir string) string {
	t.Helper()
	src := filepath.Join(dir, "rt8oracle.go")
	if err := os.WriteFile(src, []byte(rt8OracleHelper), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "rt8oracle")
	cmd := exec.Command("go", "build", "-o", out, src)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOOS=linux", "GOARCH=amd64", "CGO_ENABLED=0", "GOFLAGS=")
	if o, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build of the oracle helper: %v\n%s", err, o)
	}
	return out
}

// rt8Section is one helper run's output: its block's status, the block's
// standard error, and Go's lines (without the "G ").
type rt8Section struct {
	code  int
	block strings.Builder
	goes  []string
}

// TestRt_OracleLinux runs RT8-Oracle-Calls and RT8-Oracle-Env on
// x86_64-linux, dev and release, against Go's facts read in the
// distribution: StatEx with and without following, ReadLink, Cwd, Absolute
// and SetModified (rt8CheckOracle, the Windows leg's check), and the child
// environment of IoEnvExtend against os/exec's.
func TestRt_OracleLinux(t *testing.T) {
	linuxGate(t)
	dir, err := packageTempDir("mkrt8oracle")
	if err != nil {
		t.Fatal(err)
	}
	helper := rt8BuildHelper(t, dir)
	var sh strings.Builder
	sh.WriteString("#!/bin/sh\n")
	add := strings.Join(rt8EnvAdd(false), "\x1f")
	type leg struct{ profile, kind string }
	var legs []leg
	for _, profile := range []string{"linux-dev", "linux-release"} {
		exe, all := rtsBuild(t, profile)
		for _, b := range all {
			var kind string
			switch {
			case strings.HasPrefix(b.name, "RT8-Oracle-Calls"):
				kind = "calls"
			case strings.HasPrefix(b.name, "RT8-Oracle-Env"):
				kind = "env"
			default:
				continue
			}
			legs = append(legs, leg{profile, kind})
			cmd := fmt.Sprintf("%s %s %s %d \"$MK_LINUX_TMP\"", rtsWSLPath(helper), kind, rtsWSLPath(exe), b.index)
			if kind == "env" {
				cmd += " '" + add + "'"
			}
			fmt.Fprintf(&sh, "echo \"BEGIN %s %s\"\n", profile, kind)
			sh.WriteString(linuxScratchSh(cmd))
			fmt.Fprintf(&sh, "echo \"END %s %s $s\"\n", profile, kind)
		}
	}
	if len(legs) != 4 {
		t.Fatalf("%d oracle legs, want 4 (two blocks, two profiles)", len(legs))
	}
	out := linuxRunScript(t, dir, "rt8oracle.sh", sh.String(), 5*time.Minute)
	got := map[string]*rt8Section{}
	var cur *rt8Section
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(line, "BEGIN "):
			cur = &rt8Section{code: -100}
			got[strings.TrimPrefix(line, "BEGIN ")] = cur
		case strings.HasPrefix(line, "END "):
			f := strings.Fields(line)
			if len(f) == 4 && f[3] != "0" {
				t.Errorf("%s %s: the helper's status %s\n%s", f[1], f[2], f[3], out)
			}
			cur = nil
		case cur == nil:
		case strings.HasPrefix(line, "X "):
			cur.code, _ = strconv.Atoi(line[2:])
		case strings.HasPrefix(line, "M "):
			cur.block.WriteString(line[2:] + "\n")
		case strings.HasPrefix(line, "G "):
			cur.goes = append(cur.goes, line[2:])
		case strings.HasPrefix(line, "FAIL "):
			t.Errorf("helper: %s", line)
		}
	}
	for _, l := range legs {
		s := got[l.profile+" "+l.kind]
		if s == nil || s.code != 0 {
			t.Errorf("%s %s: no section or the block failed\n%s", l.profile, l.kind, out)
			continue
		}
		switch l.kind {
		case "calls":
			fs := rt8Facts{stat: map[string][]int64{}, abs: map[string]string{}, mtime: map[string]int64{}}
			for _, g := range s.goes {
				f := strings.Fields(g)
				switch f[0] {
				case "S":
					var v []int64
					for _, x := range f[3:] {
						n, _ := strconv.ParseInt(x, 10, 64)
						v = append(v, n)
					}
					fs.stat[f[1]+" "+f[2]] = v
					if f[2] == "1" && len(v) == 5 {
						fs.mtime[f[1]] = v[3]
					}
				case "L":
					fs.link, fs.linkOK = strings.TrimPrefix(g, "L "), true
				case "C":
					fs.cwd = strings.TrimPrefix(g, "C ")
				case "A":
					fs.abs[f[1]] = strings.TrimPrefix(g, "A "+f[1]+" ")
				}
			}
			rt8CheckOracle(t, l.profile, fs, s.block.String())
		case "env":
			mk, err := rt8EnvLines(s.block.String())
			if err != nil {
				t.Fatal(err)
			}
			var want []string
			for _, g := range s.goes {
				if strings.HasPrefix(g, "E ") {
					b, err := hex.DecodeString(g[2:])
					if err != nil {
						t.Fatal(err)
					}
					want = rt8SortedLines(string(b))
				}
			}
			if len(mk) == 0 || len(want) == 0 {
				t.Errorf("%s env: an empty listing (Mk %d lines, Go %d)", l.profile, len(mk), len(want))
			}
			if d := rt8DiffLines(mk, want); d != "" {
				t.Errorf("%s: the child's environment differs from os/exec's:\n%s", l.profile, d)
			}
		}
	}
}
