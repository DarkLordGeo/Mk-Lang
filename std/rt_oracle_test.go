// Why: rt's call functions (std/rt/call.mk) report facts Go's standard library
// reports too, so Go is their oracle.
// What: TestRt_CallOracle, TestRt_EnvOracle and TestRt_ZoneOracle, on
// Windows; the Linux leg is rt_oraclelinux_test.go's.
// How: the RT8-Oracle-Calls block (std/task/tests_oracle.mk) reads a directory
// of known files; each line it prints is checked against os and filepath.

package std

import (
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

// rt8Kind is core.IoKind* for a Go FileMode.
func rt8Kind(m os.FileMode) int64 {
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

// rt8Mode is the mode bits rt.StatEx reports for a Go FileMode: Unix's
// permission bits, which Go's Windows Stat derives from the read-only
// attribute as rt8's does (0o444 or 0o666, 0o111 more on a directory).
func rt8Mode(m os.FileMode) int64 {
	return int64(m.Perm())
}

// TestRt_CallOracle makes a directory of known files (a sized file with a
// set modification time, a read-only file, a directory, a symbolic link where
// the account may make one) and runs the RT8-Oracle-Calls block on it; the
// block prints what rt.StatEx (with and without following), rt.ReadLink,
// rt.Cwd and rt.Absolute answer, and sets one file's time with
// rt.SetModified. Each line is checked against os.Stat, os.Lstat,
// os.Readlink, os.Getwd, filepath.Abs and, for the time the block set,
// os.Stat after it (os.Chtimes' oracle).
func TestRt_CallOracle(t *testing.T) {
	rtsNative(t)
	dir := t.TempDir()
	f := filepath.Join(dir, "f")
	if err := os.WriteFile(f, make([]byte, 1234), 0o644); err != nil {
		t.Fatal(err)
	}
	when := time.Date(2021, 3, 4, 5, 6, 7, 123456700, time.UTC)
	if err := os.Chtimes(f, when, when); err != nil {
		t.Fatal(err)
	}
	ro := filepath.Join(dir, "ro")
	if err := os.WriteFile(ro, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(ro, 0o444); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(ro, 0o644)
	if err := os.Mkdir(filepath.Join(dir, "d"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "g"), []byte("g"), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = os.Symlink("f", filepath.Join(dir, "l")) // only where the account may make one
	for _, profile := range []string{"dev", "release"} {
		exe, all := rtsBuild(t, profile)
		var found bool
		for _, b := range all {
			if !strings.HasPrefix(b.name, "RT8-Oracle-Calls") {
				continue
			}
			found = true
			r := rtsRun(t, exe, b.index, 60*time.Second, "MK_RT8_ORACLE="+dir)
			rtsJudge(t, b, r, "")
			rt8CheckOracle(t, profile, rt8LocalFacts(dir, filepath.Dir(exe)), r.stderr)
		}
		if !found {
			t.Fatal("no RT8-Oracle-Calls block")
		}
	}
}

// rt8Facts is what Go's os and path/filepath say about the oracle
// directory: stat[name+" "+follow] (follow "1" os.Stat, "0" os.Lstat), the
// link's target, the block's working directory, the absolute form of each
// input the block prints, and each file's modification time after the
// block ran. The Windows leg reads them here; the Linux leg's helper
// (rt_oraclelinux_test.go) reads them in the distribution and prints them.
type rt8Facts struct {
	stat   map[string][]int64 // ok, kind, size, mtime, mode
	link   string
	linkOK bool
	cwd    string
	abs    map[string]string
	mtime  map[string]int64
}

// rt8LocalFacts reads rt8Facts on this host for dir, with the block run in
// cwd.
func rt8LocalFacts(dir, cwd string) rt8Facts {
	fs := rt8Facts{stat: map[string][]int64{}, cwd: cwd, abs: map[string]string{}, mtime: map[string]int64{}}
	for _, name := range []string{"f", "ro", "d", "l", "missing", "g"} {
		p := filepath.Join(dir, name)
		for _, follow := range []string{"0", "1"} {
			var fi os.FileInfo
			var err error
			if follow == "1" {
				fi, err = os.Stat(p)
			} else {
				fi, err = os.Lstat(p)
			}
			if err != nil {
				fs.stat[name+" "+follow] = []int64{0}
				continue
			}
			fs.stat[name+" "+follow] = []int64{1, rt8Kind(fi.Mode()), fi.Size(), fi.ModTime().UnixNano(), rt8Mode(fi.Mode())}
			if follow == "1" {
				fs.mtime[name] = fi.ModTime().UnixNano()
			}
		}
	}
	fs.link, _ = os.Readlink(filepath.Join(dir, "l"))
	fs.linkOK = fs.link != ""
	for _, in := range []string{"rel", ".", dir} {
		if filepath.IsAbs(in) {
			fs.abs[in] = filepath.Clean(in)
		} else {
			fs.abs[in] = filepath.Join(cwd, in)
		}
	}
	return fs
}

// rt8CheckOracle checks each line the block printed against Go's facts.
func rt8CheckOracle(t *testing.T, profile string, fs rt8Facts, out string) {
	t.Helper()
	seen := map[string]bool{}
	for _, line := range strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n") {
		f := strings.Fields(line)
		if len(f) == 0 {
			continue
		}
		seen[f[0]] = true
		switch f[0] {
		case "S": // S name follow ok kind size mtime mode
			if len(f) != 8 {
				t.Errorf("%s: bad line %q", profile, line)
				continue
			}
			if f[1] == "l" && !fs.linkOK {
				continue
			}
			want, ok := fs.stat[f[1]+" "+f[2]]
			if !ok {
				t.Errorf("%s: %q: no Go fact", profile, line)
				continue
			}
			if (want[0] == 1) != (f[3] == "1") {
				t.Errorf("%s: %s: Go's ok %d, Mk's ok %s", profile, line, want[0], f[3])
				continue
			}
			if want[0] == 0 {
				continue
			}
			for i, w := range want[1:] {
				got, _ := strconv.ParseInt(f[4+i], 10, 64)
				// A directory's or a link's size is the OS's own; compared for files only.
				if i == 1 && want[1] != 1 {
					continue
				}
				if got != w {
					t.Errorf("%s: %s: field %d is %d, Go says %d", profile, line, 4+i, got, w)
				}
			}
		case "L": // L target
			if !fs.linkOK {
				continue
			}
			if len(f) != 2 || f[1] != fs.link {
				t.Errorf("%s: %q, Go's Readlink %q", profile, line, fs.link)
			}
		case "C": // C cwd
			if strings.TrimPrefix(line, "C ") != fs.cwd {
				t.Errorf("%s: %q, Go's Getwd %q", profile, line, fs.cwd)
			}
		case "A": // A input absolute
			in := f[1]
			got := strings.TrimPrefix(line, "A "+in+" ")
			// Linux's Absolute joins without cleaning (rt/call.mk); Go's Abs cleans.
			if path.Clean(filepath.ToSlash(got)) != filepath.ToSlash(fs.abs[in]) {
				t.Errorf("%s: %q, Go's Abs %q", profile, line, fs.abs[in])
			}
		case "T": // T name nanos: the block set g's time
			n, _ := strconv.ParseInt(f[2], 10, 64)
			if fs.mtime[f[1]] != n {
				t.Errorf("%s: %q, Go's Stat after it %v", profile, line, fs.mtime[f[1]])
			}
		}
	}
	for _, k := range []string{"S", "C", "A", "T"} {
		if !seen[k] {
			t.Errorf("%s: no %s line in\n%s", profile, k, out)
		}
	}
	if fs.linkOK && !seen["L"] {
		t.Errorf("%s: no L line in\n%s", profile, out)
	}
}

// rt8EnvAdd is the list RT8-Oracle-Env appends with IoEnvExtend
// (std/task/tests_oracle.mk), for this host.
func rt8EnvAdd(windows bool) []string {
	if windows {
		return []string{"MK_RT8_X=1", `path=C:\rt8;C:\Windows\System32`, "MK_RT8_EMPTY=", "MK_RT8_DUP=a", "MK_RT8_DUP=b", "MK_RT8_Y=2"}
	}
	return []string{"MK_RT8_X=1", "PATH=/rt8:/usr/bin:/bin", "MK_RT8_EMPTY=", "MK_RT8_DUP=a", "MK_RT8_DUP=b", "mk_rt8_x=lower"}
}

// rt8EnvLines decodes the block's E lines (hex chunks of the child's
// output) and returns the child's lines, sorted.
func rt8EnvLines(out string) ([]string, error) {
	var all []byte
	for _, line := range strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n") {
		if !strings.HasPrefix(line, "E ") {
			continue
		}
		b, err := hex.DecodeString(strings.TrimSpace(line[2:]))
		if err != nil {
			return nil, err
		}
		all = append(all, b...)
	}
	return rt8SortedLines(string(all)), nil
}

// rt8SortedLines splits a shell's environment listing into sorted lines.
func rt8SortedLines(s string) []string {
	var lines []string
	for _, l := range strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n") {
		if l != "" {
			lines = append(lines, l)
		}
	}
	sort.Strings(lines)
	return lines
}

// rt8DiffLines reports the lines only one side has.
func rt8DiffLines(got, want []string) string {
	g, w := map[string]int{}, map[string]int{}
	for _, l := range got {
		g[l]++
	}
	for _, l := range want {
		w[l]++
	}
	var b strings.Builder
	for _, l := range got {
		if g[l] > w[l] {
			fmt.Fprintf(&b, "  Mk only: %q\n", l)
			g[l] = w[l]
		}
	}
	for _, l := range want {
		if w[l] > g[l] {
			fmt.Fprintf(&b, "  Go only: %q\n", l)
			w[l] = g[l]
		}
	}
	return b.String()
}

// TestRt_EnvOracle: rt.Spawn with IoEnvExtend gives the child the
// environment os/exec gives it for the block's own environment with the
// same entries appended (later entries win; names ignore case on Windows).
// The child is the shell listing its environment (cmd.exe /c set, /bin/sh
// -c env), started both ways; the sorted listings must be equal.
func TestRt_EnvOracle(t *testing.T) {
	rtsNative(t)
	for _, profile := range []string{"dev", "release"} {
		exe, all := rtsBuild(t, profile)
		var found bool
		for _, b := range all {
			if !strings.HasPrefix(b.name, "RT8-Oracle-Env") {
				continue
			}
			found = true
			blockEnv := append(append(os.Environ(), "MK_TEST_STDIN=open"), "MK_RT8_ENVORACLE=1")
			r := rtsRun(t, exe, b.index, 60*time.Second, "MK_RT8_ENVORACLE=1")
			rtsJudge(t, b, r, "")
			got, err := rt8EnvLines(r.stderr)
			if err != nil {
				t.Fatalf("%s: %v", profile, err)
			}
			var cmd *exec.Cmd
			if runtime.GOOS == "windows" {
				cmd = exec.Command("cmd.exe", "/c", "set")
			} else {
				cmd = exec.Command("/bin/sh", "-c", "env")
			}
			cmd.Dir = filepath.Dir(exe)
			cmd.Env = append(blockEnv, rt8EnvAdd(runtime.GOOS == "windows")...)
			o, err := cmd.Output()
			if err != nil {
				t.Fatal(err)
			}
			want := rt8SortedLines(string(o))
			if d := rt8DiffLines(got, want); d != "" {
				t.Errorf("%s: the child's environment differs from os/exec's:\n%s", profile, d)
			}
		}
		if !found {
			t.Fatal("no RT8-Oracle-Env block")
		}
	}
}

// TestRt_ZoneOracle: rt.TimeZone's record (Windows only; Linux answers
// Unsupported, RT8-Call-Term) against Go's time.Local, which Go builds from
// the same zone: the offsets Go gives on the 15th of each month of the year
// are the record's standard offset, and its daylight offset when the record
// names a daylight month and automatic daylight time is not disabled.
func TestRt_ZoneOracle(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("rt.TimeZone is Windows' record")
	}
	year := time.Now().Year()
	exe, all := rtsBuild(t, "dev")
	for _, b := range all {
		if !strings.HasPrefix(b.name, "RT8-Oracle-Zone") {
			continue
		}
		r := rtsRun(t, exe, b.index, 60*time.Second, fmt.Sprintf("MK_RT8_ZONE=%d", year))
		rtsJudge(t, b, r, "")
		goSet := map[int]bool{}
		for m := time.January; m <= time.December; m++ {
			_, off := time.Date(year, m, 15, 12, 0, 0, 0, time.UTC).In(time.Local).Zone()
			goSet[off] = true
		}
		var lines int
		for _, line := range strings.Split(strings.ReplaceAll(r.stderr, "\r\n", "\n"), "\n") {
			f := strings.Fields(line)
			if len(f) != 8 || f[0] != "Z" {
				continue
			}
			lines++
			var v [7]int
			for i := range v {
				v[i], _ = strconv.Atoi(f[1+i])
			}
			bias, sb, db, dm, off := v[1], v[2], v[3], v[5], v[6]
			mk := map[int]bool{-(bias + sb) * 60: true}
			if dm != 0 && off == 0 {
				mk[-(bias+db)*60] = true
			}
			if fmt.Sprint(rt8Keys(mk)) != fmt.Sprint(rt8Keys(goSet)) {
				t.Errorf("%q: offsets %v, Go's time.Local %v in %d", line, rt8Keys(mk), rt8Keys(goSet), year)
			}
		}
		if lines != 2 {
			t.Fatalf("%d Z lines, want 2:\n%s", lines, r.stderr)
		}
		return
	}
	t.Fatal("no RT8-Oracle-Zone block")
}

// rt8Keys is a set's members, sorted.
func rt8Keys(m map[int]bool) []int {
	var k []int
	for x := range m {
		k = append(k, x)
	}
	sort.Ints(k)
	return k
}
