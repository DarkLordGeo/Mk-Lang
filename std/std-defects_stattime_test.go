// Why: rt.StatEx's time was one i64 of nanoseconds computed with wrapping arithmetic,
// so an mtime past 2262 read as the 1700s and one before 1677 as the 2200s.
// What: TestStdDefects_StatTimeSaturates and TestStdDefects_StatTimeSaturatesLinux.
// How: std.sys now saturates (StatNanos on Linux, UnixNanos on Windows), so fs.Stat's
// modified is Time's greatest or least value, as time.FromUnix saturates.
//
// Windows runs natively; Linux under MK_LINUX=1 with the files on Linux's
// own file system.

package std

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// sd8StatProg prints, for each name in args[2..] under the directory
// args[1], the name and Info.modified's nanoseconds.
const sd8StatProg = `import std.fs

export Main(sys: &System): WriteError!void {
    var out := sys.Stdout()
    dir := sys.args[1]
    for name in sys.args[2..] {
        p := sys.arena.Fmt("{dir}/{name}", (dir: dir, name: name)) else {
            try out.Str("oom\n")
            return
        }
        info := fs.Stat(sys.io, p) else e {
            try Print(mut out, "{name} error {e}\n", (name: name, e: e))
            continue
        }
        try Print(mut out, "{name} {m}\n", (name: name, m: info.modified.UnixNanos()))
    }
}
`

// sd8StatCases: a file's name, its modification time, and the nanoseconds
// fs.Stat must report.
var sd8StatCases = []struct {
	name string
	at   time.Time
	want string
}{
	{"late", time.Date(2300, 1, 1, 0, 0, 0, 0, time.UTC), "9223372036854775807"},
	{"edge", time.Date(2262, 4, 11, 23, 47, 16, 0, time.UTC), "9223372036000000000"},
	{"now", time.Date(2001, 9, 9, 1, 46, 40, 5000, time.UTC), "1000000000000005000"},
	{"early", time.Date(1650, 1, 1, 0, 0, 0, 0, time.UTC), "-9223372036854775808"},
}

func TestStdDefects_StatTimeSaturates(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("the native leg sets FILETIMEs; Linux is TestStdDefects_StatTimeSaturatesLinux")
	}
	dir := t.TempDir()
	var names []string
	for _, c := range sd8StatCases {
		p := filepath.Join(dir, c.name)
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		// os.Chtimes goes through UnixNano, which wraps past 2262; the
		// FILETIME is set from the seconds instead.
		if err := sd8SetModified(p, c.at); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		names = append(names, c.name)
	}
	for _, flags := range [][]string{nil, {"--release"}} {
		exe := fs2Build(t, sd8StatProg, flags...)
		b, err := exec.Command(exe, append([]string{filepath.ToSlash(dir)}, names...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("%v: %v\n%s", flags, err, b)
		}
		sd8StatJudge(t, flags, string(b), sd8StatCases)
	}
}

func TestStdDefects_StatTimeSaturatesLinux(t *testing.T) {
	linuxGate(t)
	dir, err := packageTempDir("mksd8stat")
	if err != nil {
		t.Fatal(err)
	}
	// ext4 keeps 1901 to 2446, so the early case (1650) is not made here.
	cases := sd8StatCases[:3]
	var sh strings.Builder
	sh.WriteString("#!/bin/sh\n")
	for _, release := range []bool{false, true} {
		flags := []string{"--target", "x86_64-linux"}
		name := "dev"
		if release {
			flags = append(flags, "--release")
			name = "release"
		}
		exe := fs2Build(t, sd8StatProg, flags...)
		keep := filepath.Join(dir, "sd8stat-"+name)
		b, err := os.ReadFile(exe)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(keep, b, 0o755); err != nil {
			t.Fatal(err)
		}
		cmd := `cd "$MK_LINUX_TMP"`
		var names []string
		for _, c := range cases {
			cmd += ` && touch "` + c.name + `" && touch -d "` + c.at.Format("2006-01-02T15:04:05.000000000Z") + `" "` + c.name + `"`
			names = append(names, c.name)
		}
		cmd += ` && ` + rtsWSLPath(keep) + ` "$MK_LINUX_TMP" ` + strings.Join(names, " ") + `; echo "exit $?"`
		sh.WriteString("echo 'LEG " + name + "'\n")
		sh.WriteString(linuxScratchSh(cmd))
	}
	out := linuxRunScript(t, dir, "sd8stat.sh", sh.String(), 3*time.Minute)
	parts := strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "LEG ")
	if len(parts) != 3 {
		t.Fatalf("want two legs:\n%s", out)
	}
	for _, part := range parts[1:] {
		leg, rest, _ := strings.Cut(part, "\n")
		if !strings.Contains(rest, "exit 0") {
			t.Errorf("%s: the program failed:\n%s", leg, rest)
		}
		sd8StatJudge(t, []string{leg}, rest, cases)
	}
}

// sd8StatJudge wants each case's line: its name, then the nanoseconds.
func sd8StatJudge(t *testing.T, leg []string, out string, cases []struct {
	name string
	at   time.Time
	want string
}) {
	t.Helper()
	lines := strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n")
	for _, c := range cases {
		want := c.name + " " + c.want
		found := false
		for _, l := range lines {
			if l == want {
				found = true
			}
		}
		if !found {
			t.Errorf("%v: want the line %q in\n%s", leg, want, out)
		}
	}
}
